package core

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ---------- daily summary ----------

// Digests sends each person who asked for it one morning summary (once a day, at their hour).
func (s *Store) Digests(now time.Time, send Sender) {
	users, _ := s.ListUsers()
	day := now.Format(DateFmt)
	for _, u := range users {
		if !u.Active || u.DigestHour < 0 || now.Hour() < u.DigestHour {
			continue
		}
		if !s.claim("digest", 0, day, "", u.ID) {
			continue
		}
		s.notify(send, u, s.DigestFor(u, now))
	}
}

// DigestFor builds a person's summary of today.
func (s *Store) DigestFor(u *User, now time.Time) Message {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	cur := s.Setting("currency")
	var lines []string
	occ, _ := s.Calendar(u, today, today, false)
	for _, o := range occ {
		if ids := (&Event{Members: o.Members}).MemberIDs(); len(ids) > 0 {
			mine := false
			for _, id := range ids {
				mine = mine || id == u.ID
			}
			if !mine {
				continue
			}
		}
		when := "all day"
		if !o.AllDay {
			if t, err := ParseLocal(o.Start); err == nil {
				when = t.Format("3:04 PM")
			}
		}
		lines = append(lines, "• "+when+" "+o.Title)
	}
	if u.CanFinance() {
		dues, _ := s.BillDues(u.ID, today.AddDate(0, 0, -30), today.AddDate(0, 0, 3))
		for _, d := range dues {
			if d.Paid {
				continue
			}
			if d.Overdue {
				lines = append(lines, fmt.Sprintf("• OVERDUE %s %s%s", d.Name, cur, FormatCents(d.AmountCents)))
			} else {
				dd, _ := ParseLocal(d.Date)
				lines = append(lines, fmt.Sprintf("• %s %s%s due %s", d.Name, cur, FormatCents(d.AmountCents), whenText(now, dd, true)))
			}
		}
		fc, _, _ := s.Forecast(u.ID, 7)
		for _, f := range fc {
			if f.Warning {
				lines = append(lines, fmt.Sprintf("• %s may drop to %s%s this week", f.Name, cur, FormatCents(f.LowestCents)))
			}
		}
	}
	chores, _ := s.ChoresDue(today, today)
	for _, c := range chores {
		if c.Status == "todo" && (c.AssignedTo == nil || *c.AssignedTo == u.ID) {
			lines = append(lines, "• Chore: "+c.Name)
		}
	}
	m := Message{Title: "Today: " + now.Format("Mon Jan 2")}
	if len(lines) == 0 {
		m.Body = "Nothing on today."
	} else {
		m.Body = strings.Join(lines, "\n")
	}
	return m
}

// ---------- search ----------

// SearchHit is one result.
type SearchHit struct {
	Kind  string `json:"kind"`
	ID    int64  `json:"id"`
	Title string `json:"title"`
	Sub   string `json:"sub"`
	Date  string `json:"date"`
	Link  string `json:"link"`
}

func likeArg(q string) string {
	return "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(q) + "%"
}

// Search looks through everything the user may see.
func (s *Store) Search(u *User, q string) ([]SearchHit, error) {
	q = strings.TrimSpace(q)
	out := []SearchHit{}
	if len([]rune(q)) < 2 {
		return out, nil
	}
	l := likeArg(q)
	add := func(kind, link, query string, args ...any) {
		rows, err := s.DB.Query(query+` LIMIT 25`, args...)
		if err != nil {
			return
		}
		defer rows.Close()
		for rows.Next() {
			h := SearchHit{Kind: kind}
			rows.Scan(&h.ID, &h.Title, &h.Sub, &h.Date)
			h.Link = link
			if kind == "event" {
				h.Link = "#calendar?d=" + h.Date
			}
			out = append(out, h)
		}
	}
	add("event", "", `SELECT id,title,location||' '||notes,substr(start_at,1,10) FROM events WHERE (private=0 OR owner_id=?) AND (title LIKE ? ESCAPE '\' OR location LIKE ? ESCAPE '\' OR notes LIKE ? ESCAPE '\') ORDER BY start_at DESC`, u.ID, l, l, l)
	add("list item", "#family", `SELECT i.id,i.text,l.name,substr(i.created,1,10) FROM list_items i JOIN lists l ON l.id=i.list_id WHERE (l.private=0 OR l.owner_id=?) AND i.text LIKE ? ESCAPE '\' ORDER BY i.done, i.id DESC`, u.ID, l)
	add("meal", "#family", `SELECT id,title,slot,date FROM meals WHERE title LIKE ? ESCAPE '\' OR ingredients LIKE ? ESCAPE '\' ORDER BY date DESC`, l, l)
	if u.CanFinance() {
		add("transaction", "#money", `SELECT id,payee,notes||' '||printf('%.2f',amount_cents/100.0),date FROM transactions WHERE parent_id IS NULL AND (private=0 OR user_id=?) AND (payee LIKE ? ESCAPE '\' OR notes LIKE ? ESCAPE '\') ORDER BY date DESC`, u.ID, l, l)
		add("bill", "#bills", `SELECT id,name,notes,start_date FROM bills WHERE (private=0 OR owner_id=?) AND (name LIKE ? ESCAPE '\' OR notes LIKE ? ESCAPE '\')`, u.ID, l, l)
		add("account", "#accounts", `SELECT id,name,type,'' FROM accounts WHERE (private=0 OR owner_id=?) AND name LIKE ? ESCAPE '\'`, u.ID, l)
		add("document", "#documents", `SELECT id,title,kind,expires FROM documents WHERE (private=0 OR owner_id=?) AND (title LIKE ? ESCAPE '\' OR notes LIKE ? ESCAPE '\')`, u.ID, l, l)
		add("goal", "#reports", `SELECT id,name,'',target_date FROM goals WHERE (private=0 OR owner_id=?) AND name LIKE ? ESCAPE '\'`, u.ID, l)
	}
	return out, nil
}

// ---------- recently deleted ----------

type trashPart struct {
	Table string           `json:"table"`
	Rows  []map[string]any `json:"rows"`
}

type partQuery struct {
	table, where string
	args         []any
}

func (s *Store) snapshot(parts []partQuery) ([]trashPart, error) {
	var out []trashPart
	for _, p := range parts {
		rows, err := s.DB.Query(`SELECT * FROM `+p.table+` WHERE `+p.where+` ORDER BY rowid`, p.args...)
		if err != nil {
			return nil, err
		}
		cols, _ := rows.Columns()
		tp := trashPart{Table: p.table}
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			rows.Scan(ptrs...)
			m := map[string]any{}
			for i, c := range cols {
				if b, ok := vals[i].([]byte); ok {
					m[c] = string(b)
				} else {
					m[c] = vals[i]
				}
			}
			tp.Rows = append(tp.Rows, m)
		}
		rows.Close()
		out = append(out, tp)
	}
	return out, nil
}

// trashThen saves the rows to "recently deleted" and then runs del. Nothing is kept if the delete fails.
func (s *Store) trashThen(kind, label string, owner int64, private bool, by int64, parts []partQuery, del func() error) error {
	snap, err := s.snapshot(parts)
	if err != nil {
		return err
	}
	if len(snap) == 0 || len(snap[0].Rows) == 0 {
		return ErrNotFound
	}
	if err := del(); err != nil {
		return err
	}
	b, _ := json.Marshal(snap)
	_, err = s.DB.Exec(`INSERT INTO trash(kind,label,owner_id,private,deleted_by,deleted_at,data) VALUES(?,?,?,?,?,?,?)`, kind, trunc(label, 120), owner, b2i(private), by, s.nowStr(), string(b))
	return err
}

// TrashItem is one deleted thing that can come back.
type TrashItem struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	Label     string `json:"label"`
	DeletedBy int64  `json:"deleted_by"`
	DeletedAt string `json:"deleted_at"`
}

// ListTrash lists what the user may restore (adults; private items only for their owner).
func (s *Store) ListTrash(u *User) ([]TrashItem, error) {
	out := []TrashItem{}
	if !u.CanFinance() {
		return out, nil
	}
	rows, err := s.DB.Query(`SELECT id,kind,label,deleted_by,deleted_at FROM trash WHERE private=0 OR owner_id=? OR deleted_by=? ORDER BY id DESC LIMIT 200`, u.ID, u.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var t TrashItem
		rows.Scan(&t.ID, &t.Kind, &t.Label, &t.DeletedBy, &t.DeletedAt)
		out = append(out, t)
	}
	return out, nil
}

// fkTables says which table each link column points to, so a renumbered parent takes its parts along.
var fkTables = map[string]string{"parent_id": "transactions", "transaction_id": "transactions", "bill_id": "bills", "list_id": "lists",
	"document_id": "documents", "goal_id": "goals", "chore_id": "chores"}

// RestoreTrash puts a deleted item (and its parts) back. If its old number was taken in the meantime it gets a
// new one and its parts follow.
func (s *Store) RestoreTrash(u *User, id int64) (string, error) {
	if !u.CanFinance() {
		return "", ErrForbidden
	}
	var kind, data string
	if s.DB.QueryRow(`SELECT kind,data FROM trash WHERE id=? AND (private=0 OR owner_id=? OR deleted_by=?)`, id, u.ID, u.ID).Scan(&kind, &data) != nil {
		return "", ErrNotFound
	}
	dec := json.NewDecoder(strings.NewReader(data))
	dec.UseNumber()
	var parts []trashPart
	if err := dec.Decode(&parts); err != nil {
		return "", err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	remap := map[string]map[int64]int64{} // table -> old id -> new id
	for pi, p := range parts {
		for _, row := range p.Rows {
			oldID := numVal(row["id"])
			// children follow a renumbered parent
			for c, table := range fkTables {
				if v, ok := row[c]; ok && v != nil {
					if nv, ok := remap[table][numVal(v)]; ok {
						row[c] = nv
					}
				}
			}
			_ = pi
			cols, marks, vals := []string{}, []string{}, []any{}
			for k, v := range row {
				cols = append(cols, `"`+k+`"`)
				marks = append(marks, "?")
				if n, ok := v.(json.Number); ok {
					if i, err := n.Int64(); err == nil {
						v = i
					} else if f, err := n.Float64(); err == nil {
						v = f
					}
				}
				vals = append(vals, v)
			}
			q := `INSERT INTO ` + p.Table + `(` + strings.Join(cols, ",") + `) VALUES(` + strings.Join(marks, ",") + `)`
			if _, err := tx.Exec(q, vals...); err != nil {
				if !strings.Contains(err.Error(), "UNIQUE") && !strings.Contains(err.Error(), "PRIMARY KEY") {
					return "", fmt.Errorf("could not restore: %w", err)
				}
				// number taken: insert without the id
				var c2, m2 []string
				var v2 []any
				for i, c := range cols {
					if c == `"id"` {
						continue
					}
					c2, m2, v2 = append(c2, c), append(m2, "?"), append(v2, vals[i])
				}
				res, err := tx.Exec(`INSERT INTO `+p.Table+`(`+strings.Join(c2, ",")+`) VALUES(`+strings.Join(m2, ",")+`)`, v2...)
				if err != nil {
					return "", fmt.Errorf("could not restore: %w", err)
				}
				nid, _ := res.LastInsertId()
				if remap[p.Table] == nil {
					remap[p.Table] = map[int64]int64{}
				}
				remap[p.Table][oldID] = nid
			}
		}
	}
	tx.Exec(`DELETE FROM trash WHERE id=?`, id)
	return kind, tx.Commit()
}

func numVal(v any) int64 {
	switch x := v.(type) {
	case json.Number:
		n, _ := x.Int64()
		return n
	case int64:
		return x
	case float64:
		return int64(x)
	}
	return 0
}

// PurgeTrash forgets items deleted more than 30 days ago and removes their files.
func (s *Store) PurgeTrash() {
	cut := s.Now().AddDate(0, 0, -30).Format(time.RFC3339)
	rows, err := s.DB.Query(`SELECT id,data FROM trash WHERE deleted_at<?`, cut)
	if err != nil {
		return
	}
	type t struct {
		id   int64
		data string
	}
	var list []t
	for rows.Next() {
		var x t
		rows.Scan(&x.id, &x.data)
		list = append(list, x)
	}
	rows.Close()
	for _, x := range list {
		var parts []trashPart
		json.Unmarshal([]byte(x.data), &parts)
		for _, p := range parts {
			if p.Table != "attachments" {
				continue
			}
			for _, r := range p.Rows {
				if rel, ok := r["path"].(string); ok && rel != "" {
					var still int
					s.DB.QueryRow(`SELECT COUNT(*) FROM attachments WHERE path=?`, rel).Scan(&still)
					if still == 0 {
						os.Remove(filepath.Join(s.FilesDir(), filepath.FromSlash(rel)))
					}
				}
			}
		}
		s.DB.Exec(`DELETE FROM trash WHERE id=?`, x.id)
	}
}

var errGone = errors.New("already gone")
var _ = sql.ErrNoRows
var _ = errGone
