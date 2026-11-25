package core

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ---------- document vault ----------

// Document is an important paper (insurance card, license, registration, warranty) with optional files and an
// expiry reminder. Documents are private to their owner unless shared with the adults of the house; kids never
// see them.
type Document struct {
	ID         int64  `json:"id"`
	Title      string `json:"title"`
	Kind       string `json:"kind"`
	Expires    string `json:"expires"`
	RemindDays string `json:"remind_days"`
	Notes      string `json:"notes"`
	OwnerID    int64  `json:"owner_id"`
	Private    bool   `json:"private"`
	Files      int    `json:"files"`
	DaysLeft   *int   `json:"days_left"`
}

// DocKinds are the document types.
var DocKinds = map[string]bool{"insurance": true, "id": true, "vehicle": true, "warranty": true, "medical": true, "legal": true, "financial": true, "other": true}

func init() {
	canSeeDocHook = func(s *Store, uid, id int64) bool {
		u, err := s.GetUser(uid)
		if err != nil || !u.CanFinance() {
			return false
		}
		var n int
		s.DB.QueryRow(`SELECT COUNT(*) FROM documents WHERE id=? AND `+visible, id, uid).Scan(&n)
		return n == 1
	}
}

// ListDocuments returns the documents a user may see.
func (s *Store) ListDocuments(u *User) ([]Document, error) {
	if !u.CanFinance() {
		return []Document{}, nil
	}
	rows, err := s.DB.Query(`SELECT id,title,kind,expires,remind_days,notes,owner_id,private,(SELECT COUNT(*) FROM attachments a WHERE a.document_id=documents.id) FROM documents WHERE `+visible+` ORDER BY CASE WHEN expires='' THEN 1 ELSE 0 END, expires, title`, u.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Document{}
	today := s.Now().Format(DateFmt)
	t0, _ := time.Parse(DateFmt, today)
	for rows.Next() {
		var d Document
		var p int
		rows.Scan(&d.ID, &d.Title, &d.Kind, &d.Expires, &d.RemindDays, &d.Notes, &d.OwnerID, &p, &d.Files)
		d.Private = p == 1
		if e, err := time.Parse(DateFmt, d.Expires); err == nil {
			n := int(e.Sub(t0).Hours() / 24)
			d.DaysLeft = &n
		}
		out = append(out, d)
	}
	return out, nil
}

// SaveDocument adds or updates a document.
func (s *Store) SaveDocument(u *User, d *Document) error {
	if !u.CanFinance() {
		return ErrForbidden
	}
	d.Title = strings.TrimSpace(d.Title)
	if d.Title == "" {
		return errors.New("name the document")
	}
	if !DocKinds[d.Kind] {
		d.Kind = "other"
	}
	if d.Expires != "" {
		if _, err := time.Parse(DateFmt, d.Expires); err != nil {
			return errors.New("expiry must be YYYY-MM-DD")
		}
	}
	var days []string
	for _, n := range ParseInts(d.RemindDays) {
		days = append(days, strconv.Itoa(n))
	}
	d.RemindDays = strings.Join(days, ",")
	if d.ID == 0 {
		res, err := s.DB.Exec(`INSERT INTO documents(title,kind,expires,remind_days,notes,owner_id,private,created) VALUES(?,?,?,?,?,?,?,?)`, d.Title, d.Kind, d.Expires, d.RemindDays, d.Notes, u.ID, b2i(d.Private), s.nowStr())
		if err == nil {
			d.ID, _ = res.LastInsertId()
		}
		return err
	}
	return mustChange(s.DB.Exec(`UPDATE documents SET title=?,kind=?,expires=?,remind_days=?,notes=?,private=? WHERE id=? AND `+visible, d.Title, d.Kind, d.Expires, d.RemindDays, d.Notes, b2i(d.Private), d.ID, u.ID))
}

// DeleteDocument removes a document and its files.
func (s *Store) DeleteDocument(u *User, id int64) error {
	if !s.canSeeDoc(u.ID, id) {
		return ErrNotFound
	}
	var title string
	var owner int64
	var p int
	s.DB.QueryRow(`SELECT title,owner_id,private FROM documents WHERE id=?`, id).Scan(&title, &owner, &p)
	return s.trashThen("document", title, owner, p == 1, u.ID, []partQuery{
		{"documents", "id=?", []any{id}},
		{"attachments", "document_id=?", []any{id}},
	}, func() error {
		s.DB.Exec(`DELETE FROM attachments WHERE document_id=?`, id)
		return mustChange(s.DB.Exec(`DELETE FROM documents WHERE id=?`, id))
	})
}

// docReminders sends expiry reminders (once per document, expiry date and lead).
func (s *Store) docReminders(now time.Time, send Sender, cfg map[string]string) {
	rows, err := s.DB.Query(`SELECT id,title,expires,remind_days,owner_id,private FROM documents WHERE expires<>''`)
	if err != nil {
		return
	}
	type doc struct {
		id, owner     int64
		title, exp, r string
		priv          bool
	}
	var list []doc
	for rows.Next() {
		var d doc
		var p int
		rows.Scan(&d.id, &d.title, &d.exp, &d.r, &d.owner, &p)
		d.priv = p == 1
		list = append(list, d)
	}
	rows.Close()
	hour, _ := strconv.Atoi(cfg["bill_remind_hour"])
	for _, d := range list {
		exp, err := ParseLocal(d.exp)
		if err != nil {
			continue
		}
		for _, n := range ParseInts(d.r) {
			fire := time.Date(exp.Year(), exp.Month(), exp.Day()-n, hour, 0, 0, 0, exp.Location())
			if fire.After(now) || now.Sub(fire) > 20*time.Hour {
				continue
			}
			if !s.claim("doc", d.id, d.exp, strconv.Itoa(n), 0) {
				continue
			}
			m := Message{Title: fmt.Sprintf("%s expires %s", d.title, whenText(now, exp, true)), Body: "Renew it before " + exp.Format("Jan 2, 2006") + "."}
			if d.priv {
				if u, err := s.GetUser(d.owner); err == nil && u.Active {
					s.notify(send, u, m)
				}
			} else {
				s.alertAdults(send, m)
			}
		}
	}
}

// ---------- wall display (kitchen tablet) ----------

// DisplayToken is a read-only link for a wall display.
type DisplayToken struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Created  string `json:"created"`
	LastSeen string `json:"last_seen"`
}

// NewDisplayToken makes a wall-display link (shown once).
func (s *Store) NewDisplayToken(label string) (string, error) {
	if strings.TrimSpace(label) == "" {
		label = "Wall display"
	}
	t := randToken(24)
	_, err := s.DB.Exec(`INSERT INTO display_tokens(token_hash,label,created) VALUES(?,?,?)`, hashToken(t), trunc(label, 60), s.nowStr())
	return t, err
}

// ListDisplayTokens lists wall displays (id = first 12 chars of the hash).
func (s *Store) ListDisplayTokens() ([]DisplayToken, error) {
	rows, err := s.DB.Query(`SELECT token_hash,label,created,last_seen FROM display_tokens ORDER BY created`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DisplayToken{}
	for rows.Next() {
		var d DisplayToken
		var h string
		rows.Scan(&h, &d.Label, &d.Created, &d.LastSeen)
		d.ID = h[:12]
		out = append(out, d)
	}
	return out, nil
}

// RevokeDisplayToken removes a wall display link.
func (s *Store) RevokeDisplayToken(id string) error {
	return mustChange(s.DB.Exec(`DELETE FROM display_tokens WHERE substr(token_hash,1,12)=?`, id))
}

// DisplayData is everything a wall display shows: no money, nothing private.
type DisplayData struct {
	Household string       `json:"household"`
	Now       string       `json:"now"`
	Members   []member     `json:"members"`
	Events    []Occurrence `json:"events"`
	Chores    []ChoreDue   `json:"chores"`
	Lists     []List       `json:"lists"`
	Meals     []Meal       `json:"meals"`
}

type member struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// Display returns the wall data for a valid token.
func (s *Store) Display(token string) (*DisplayData, error) {
	var h string
	if len(token) < 20 || s.DB.QueryRow(`SELECT token_hash FROM display_tokens WHERE token_hash=?`, hashToken(token)).Scan(&h) != nil {
		return nil, ErrNotFound
	}
	s.DB.Exec(`UPDATE display_tokens SET last_seen=? WHERE token_hash=?`, s.nowStr(), h)
	now := s.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	d := &DisplayData{Household: s.Setting("household_name"), Now: now.Format(time.RFC3339), Events: []Occurrence{}, Lists: []List{}}
	users, _ := s.ListUsers()
	for _, u := range users {
		if u.Active {
			d.Members = append(d.Members, member{u.ID, u.DisplayName, u.Color})
		}
	}
	// shared events only: a pretend kid with no private items and no money pages
	viewer := &User{ID: -1, Role: RoleKid}
	occ, _ := s.Calendar(viewer, today, today.AddDate(0, 0, 6), false)
	for _, o := range occ {
		if !o.Private {
			d.Events = append(d.Events, o)
		}
	}
	d.Chores, _ = s.ChoresDue(today, today)
	lists, _ := s.ListLists(-1)
	for _, l := range lists {
		if l.Private {
			continue
		}
		full, err := s.GetList(-1, l.ID)
		if err != nil {
			continue
		}
		var open []Item
		for _, it := range full.Items {
			if !it.Done {
				open = append(open, it)
			}
		}
		full.Items = open
		d.Lists = append(d.Lists, *full)
	}
	d.Meals, _ = s.Meals(today.Format(DateFmt), today.AddDate(0, 0, 1).Format(DateFmt))
	return d, nil
}

var _ = sql.ErrNoRows
