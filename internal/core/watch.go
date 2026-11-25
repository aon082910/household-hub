package core

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// WatchFolder is a folder whose statement files (CSV, OFX, QFX) are imported into one account by themselves.
// Files are only read, never moved or deleted; each file's checksum is remembered so it is imported once.
type WatchFolder struct {
	ID        int64  `json:"id"`
	Label     string `json:"label"`
	Path      string `json:"path"`
	AccountID int64  `json:"account_id"`
	Enabled   bool   `json:"enabled"`
	LastScan  string `json:"last_scan"`
	LastError string `json:"last_error"`
	Files     int    `json:"files"`
}

// WatchRoot limits watch folders to one place (the Docker image sets /statements). Empty = anywhere.
func WatchRoot() string { return os.Getenv("HHUB_WATCH_ROOT") }

func checkWatchPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", errors.New("folder is required")
	}
	root := WatchRoot()
	if root != "" && !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	p = filepath.Clean(p)
	if root != "" {
		rel, err := filepath.Rel(filepath.Clean(root), p)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("the folder must be inside %s (the Statements folder of the container)", root)
		}
	}
	return p, nil
}

// ListWatchFolders returns the watch folders.
func (s *Store) ListWatchFolders() ([]WatchFolder, error) {
	rows, err := s.DB.Query(`SELECT w.id,w.label,w.path,w.account_id,w.enabled,w.last_scan,w.last_error,(SELECT COUNT(*) FROM watched_files f WHERE f.watch_id=w.id) FROM watch_folders w ORDER BY w.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WatchFolder{}
	for rows.Next() {
		var w WatchFolder
		var en int
		rows.Scan(&w.ID, &w.Label, &w.Path, &w.AccountID, &en, &w.LastScan, &w.LastError, &w.Files)
		w.Enabled = en == 1
		out = append(out, w)
	}
	return out, nil
}

// SaveWatchFolder adds or updates a watch folder.
func (s *Store) SaveWatchFolder(w *WatchFolder) error {
	p, err := checkWatchPath(w.Path)
	if err != nil {
		return err
	}
	w.Path = p
	if strings.TrimSpace(w.Label) == "" {
		w.Label = filepath.Base(p)
	}
	var n int
	s.DB.QueryRow(`SELECT COUNT(*) FROM accounts WHERE id=?`, w.AccountID).Scan(&n)
	if n == 0 {
		return errors.New("pick an account")
	}
	if w.ID == 0 {
		res, err := s.DB.Exec(`INSERT INTO watch_folders(label,path,account_id,enabled) VALUES(?,?,?,?)`, w.Label, w.Path, w.AccountID, b2i(w.Enabled))
		if err == nil {
			w.ID, _ = res.LastInsertId()
		}
		return err
	}
	return mustChange(s.DB.Exec(`UPDATE watch_folders SET label=?,path=?,account_id=?,enabled=? WHERE id=?`, w.Label, w.Path, w.AccountID, b2i(w.Enabled), w.ID))
}

// DeleteWatchFolder removes a watch folder (its files and what they added stay).
func (s *Store) DeleteWatchFolder(id int64) error {
	return mustChange(s.DB.Exec(`DELETE FROM watch_folders WHERE id=?`, id))
}

var statementExt = map[string]bool{".csv": true, ".ofx": true, ".qfx": true}

// ScanWatchFolder imports new statement files of one folder (and its subfolders, 2 levels). It returns how
// many transactions were added.
func (s *Store) ScanWatchFolder(id int64, send Sender) (int, error) {
	var w WatchFolder
	var en int
	if err := s.DB.QueryRow(`SELECT id,label,path,account_id,enabled FROM watch_folders WHERE id=?`, id).Scan(&w.ID, &w.Label, &w.Path, &w.AccountID, &en); err != nil {
		return 0, ErrNotFound
	}
	setErr := func(e string) { s.DB.Exec(`UPDATE watch_folders SET last_scan=?, last_error=? WHERE id=?`, s.nowStr(), trunc(e, 500), id) }
	if _, err := checkWatchPath(w.Path); err != nil {
		setErr(err.Error())
		return 0, err
	}
	if st, err := os.Stat(w.Path); err != nil || !st.IsDir() {
		e := "folder not found: " + w.Path
		if WatchRoot() != "" {
			e += " (is the Statements folder mapped in the container?)"
		}
		setErr(e)
		return 0, errors.New(e)
	}
	uid := s.firstAdmin()
	added := 0
	var errs []string
	var imported []string
	base := strings.Count(filepath.Clean(w.Path), string(filepath.Separator))
	filepath.WalkDir(w.Path, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if strings.Count(filepath.Clean(p), string(filepath.Separator))-base > 2 || (p != w.Path && strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !statementExt[strings.ToLower(filepath.Ext(p))] || strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > 20<<20 {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			errs = append(errs, d.Name()+": "+err.Error())
			return nil
		}
		sum := sha256.Sum256(data)
		h := hex.EncodeToString(sum[:])
		var n int
		s.DB.QueryRow(`SELECT COUNT(*) FROM watched_files WHERE watch_id=? AND sha256=?`, id, h).Scan(&n)
		if n > 0 {
			return nil
		}
		result := ""
		pr, err := ParseStatement(data, s.LoadMapping(w.AccountID))
		if err != nil {
			pr, err = ParseStatement(data, nil)
		}
		if err != nil {
			result = "error: " + err.Error()
			errs = append(errs, d.Name()+": "+err.Error())
		} else {
			counts, err := s.CommitImport(uid, w.AccountID, pr, false)
			if err != nil {
				result = "error: " + err.Error()
				errs = append(errs, d.Name()+": "+err.Error())
			} else {
				result = fmt.Sprintf("%d added, %d matched, %d already there, %d skipped", counts["added"], counts["matched"], counts["duplicate"], counts["skip"])
				added += counts["added"]
				imported = append(imported, d.Name())
			}
		}
		rel, _ := filepath.Rel(w.Path, p)
		s.DB.Exec(`INSERT OR IGNORE INTO watched_files(watch_id,sha256,name,seen,result) VALUES(?,?,?,?,?)`, id, h, rel, s.nowStr(), result)
		return nil
	})
	setErr(strings.Join(errs, "; "))
	if len(imported) > 0 && send != nil {
		if u, err := s.GetUser(uid); err == nil {
			s.notify(send, u, Message{Title: fmt.Sprintf("Imported %d transaction(s) into %s", added, w.Label), Body: "From " + strings.Join(imported, ", ")})
		}
	}
	return added, nil
}

// WatchedFile is one file a watch folder has seen.
type WatchedFile struct {
	Name   string `json:"name"`
	Seen   string `json:"seen"`
	Result string `json:"result"`
}

// ListWatchedFiles lists the newest files of a watch folder.
func (s *Store) ListWatchedFiles(id int64) ([]WatchedFile, error) {
	rows, err := s.DB.Query(`SELECT name,seen,result FROM watched_files WHERE watch_id=? ORDER BY seen DESC LIMIT 100`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WatchedFile{}
	for rows.Next() {
		var f WatchedFile
		rows.Scan(&f.Name, &f.Seen, &f.Result)
		out = append(out, f)
	}
	return out, nil
}

// ScanAllWatchFolders checks every enabled watch folder.
func (s *Store) ScanAllWatchFolders(send Sender) {
	ws, err := s.ListWatchFolders()
	if err != nil {
		return
	}
	for _, w := range ws {
		if !w.Enabled {
			continue
		}
		if n, err := s.ScanWatchFolder(w.ID, send); err != nil {
			log.Printf("watch folder %s: %v", w.Label, err)
		} else if n > 0 {
			log.Printf("watch folder %s: %d added", w.Label, n)
		}
	}
}
