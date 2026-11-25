package core

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// CopyBackupOffsite copies a finished daily backup (and the attachments) to a second place, normally another
// disk or share mounted into the container (for example /backup-copy). It keeps the newest `keep` copies there.
func (s *Store) CopyBackupOffsite(dir, name string, keep int) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return fmt.Errorf("backup copy folder %s not found (is it mapped into the container?)", dir)
	}
	if abs, _ := filepath.Abs(dir); strings.HasPrefix(abs, filepath.Dir(s.Path)) {
		return errors.New("the backup copy folder must be outside the app's data folder")
	}
	src := filepath.Join(s.BackupDir(), name)
	if err := copyFile(src, filepath.Join(dir, name)); err != nil {
		return err
	}
	if keep < 1 {
		keep = 30
	}
	if err := copyFilesDir(filepath.Join(filepath.Dir(s.Path), "files"), filepath.Join(dir, "files"), keep); err != nil {
		return fmt.Errorf("database copied, attachments not: %w", err)
	}
	ents, _ := os.ReadDir(dir)
	var names []string
	for _, e := range ents {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "household-") && strings.HasSuffix(e.Name(), ".db") {
			names = append(names, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for i := keep; i < len(names); i++ {
		os.Remove(filepath.Join(dir, names[i]))
	}
	s.SaveSettings(map[string]string{"last_backup_copy": name})
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	os.Remove(dst)
	return os.Rename(tmp, dst)
}
