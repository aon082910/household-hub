package core

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ---------- shared lists (shopping, to-do) ----------

// List is a shared shopping or to-do list. Everyone in the house (kids too) can use shared lists.
type List struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"` // shopping, todo
	OwnerID int64  `json:"owner_id"`
	Private bool   `json:"private"`
	Open    int    `json:"open"` // items not done
	Items   []Item `json:"items,omitempty"`
}

// Item is one line of a list.
type Item struct {
	ID         int64  `json:"id"`
	ListID     int64  `json:"list_id"`
	Text       string `json:"text"`
	Qty        string `json:"qty"`
	Done       bool   `json:"done"`
	DoneBy     *int64 `json:"done_by"`
	AssignedTo *int64 `json:"assigned_to"`
	DueDate    string `json:"due_date"`
}

func (s *Store) canSeeList(uid, id int64) bool {
	var n int
	s.DB.QueryRow(`SELECT COUNT(*) FROM lists WHERE id=? AND `+visible, id, uid).Scan(&n)
	return n == 1
}

// ListLists returns visible lists with their open counts.
func (s *Store) ListLists(uid int64) ([]List, error) {
	rows, err := s.DB.Query(`SELECT id,name,kind,owner_id,private,(SELECT COUNT(*) FROM list_items i WHERE i.list_id=lists.id AND i.done=0) FROM lists WHERE `+visible+` ORDER BY kind DESC, name`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []List{}
	for rows.Next() {
		var l List
		var p int
		rows.Scan(&l.ID, &l.Name, &l.Kind, &l.OwnerID, &p, &l.Open)
		l.Private = p == 1
		out = append(out, l)
	}
	return out, nil
}

// GetList returns a list with its items (open first, then the last 30 done).
func (s *Store) GetList(uid, id int64) (*List, error) {
	if !s.canSeeList(uid, id) {
		return nil, ErrNotFound
	}
	l := &List{}
	var p int
	s.DB.QueryRow(`SELECT id,name,kind,owner_id,private FROM lists WHERE id=?`, id).Scan(&l.ID, &l.Name, &l.Kind, &l.OwnerID, &p)
	l.Private = p == 1
	rows, err := s.DB.Query(`SELECT id,list_id,text,qty,done,done_by,assigned_to,due_date FROM list_items WHERE list_id=? AND (done=0 OR id IN (SELECT id FROM list_items WHERE list_id=? AND done=1 ORDER BY done_at DESC LIMIT 30)) ORDER BY done, position, id`, id, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	l.Items = []Item{}
	for rows.Next() {
		var it Item
		var d int
		var db, at sql.NullInt64
		rows.Scan(&it.ID, &it.ListID, &it.Text, &it.Qty, &d, &db, &at, &it.DueDate)
		it.Done, it.DoneBy, it.AssignedTo = d == 1, idPtr(db), idPtr(at)
		if !it.Done {
			l.Open++
		}
		l.Items = append(l.Items, it)
	}
	return l, nil
}

// SaveList adds or renames a list.
func (s *Store) SaveList(uid int64, l *List) error {
	l.Name = strings.TrimSpace(l.Name)
	if l.Name == "" {
		return errors.New("name the list")
	}
	if l.Kind != "todo" {
		l.Kind = "shopping"
	}
	if l.ID == 0 {
		res, err := s.DB.Exec(`INSERT INTO lists(name,kind,owner_id,private,created) VALUES(?,?,?,?,?)`, l.Name, l.Kind, uid, b2i(l.Private), s.nowStr())
		if err == nil {
			l.ID, _ = res.LastInsertId()
		}
		return err
	}
	return mustChange(s.DB.Exec(`UPDATE lists SET name=?,kind=?,private=? WHERE id=? AND `+visible, l.Name, l.Kind, b2i(l.Private), l.ID, uid))
}

// DeleteList removes a list and its items.
func (s *Store) DeleteList(uid, id int64) error {
	l, err := s.GetList(uid, id)
	if err != nil {
		return err
	}
	return s.trashThen("list", l.Name, l.OwnerID, l.Private, uid, []partQuery{
		{"lists", "id=?", []any{id}},
		{"list_items", "list_id=?", []any{id}},
	}, func() error { return mustChange(s.DB.Exec(`DELETE FROM lists WHERE id=?`, id)) })
}

// AddItems adds one item per line ("2 milk" keeps "2" as the quantity when it starts with a number).
func (s *Store) AddItems(uid, listID int64, text string, assigned *int64, due string) ([]Item, error) {
	if !s.canSeeList(uid, listID) {
		return nil, ErrNotFound
	}
	var out []Item
	var pos int
	s.DB.QueryRow(`SELECT COALESCE(MAX(position),0) FROM list_items WHERE list_id=?`, listID).Scan(&pos)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "-*•"))
		if line == "" {
			continue
		}
		if len(line) > 200 {
			line = line[:200]
		}
		qty := ""
		if f := strings.Fields(line); len(f) > 1 && (f[0][0] >= '0' && f[0][0] <= '9') {
			qty, line = f[0], strings.Join(f[1:], " ")
		}
		pos++
		res, err := s.DB.Exec(`INSERT INTO list_items(list_id,text,qty,assigned_to,due_date,position,created_by,created) VALUES(?,?,?,?,?,?,?,?)`, listID, line, qty, nullID(assigned), due, pos, uid, s.nowStr())
		if err != nil {
			return out, err
		}
		id, _ := res.LastInsertId()
		out = append(out, Item{ID: id, ListID: listID, Text: line, Qty: qty, AssignedTo: assigned, DueDate: due})
	}
	if len(out) == 0 {
		return nil, errors.New("type at least one item")
	}
	return out, nil
}

// SetItemDone ticks or unticks an item.
func (s *Store) SetItemDone(uid, itemID int64, done bool) error {
	var list int64
	if s.DB.QueryRow(`SELECT list_id FROM list_items WHERE id=?`, itemID).Scan(&list) != nil || !s.canSeeList(uid, list) {
		return ErrNotFound
	}
	at := ""
	var by any
	if done {
		at, by = s.nowStr(), uid
	}
	_, err := s.DB.Exec(`UPDATE list_items SET done=?, done_by=?, done_at=? WHERE id=?`, b2i(done), by, at, itemID)
	return err
}

// DeleteItem removes an item; ClearDone removes ticked items of a list.
func (s *Store) DeleteItem(uid, itemID int64) error {
	var list int64
	if s.DB.QueryRow(`SELECT list_id FROM list_items WHERE id=?`, itemID).Scan(&list) != nil || !s.canSeeList(uid, list) {
		return ErrNotFound
	}
	_, err := s.DB.Exec(`DELETE FROM list_items WHERE id=?`, itemID)
	return err
}

// ClearDone removes the ticked items of a list.
func (s *Store) ClearDone(uid, listID int64) error {
	if !s.canSeeList(uid, listID) {
		return ErrNotFound
	}
	_, err := s.DB.Exec(`DELETE FROM list_items WHERE list_id=? AND done=1`, listID)
	return err
}

// ---------- chores and allowance ----------

// Chore is a repeating job for someone, optionally paying an allowance reward.
type Chore struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	AssignedTo  *int64 `json:"assigned_to"`
	Freq        string `json:"freq"`
	Interval    int    `json:"interval"`
	StartDate   string `json:"start_date"`
	RewardCents int64  `json:"reward_cents"`
	Active      bool   `json:"active"`
}

// ChoreDue is one chore on one day.
type ChoreDue struct {
	ChoreID     int64  `json:"chore_id"`
	Name        string `json:"name"`
	Date        string `json:"date"`
	AssignedTo  *int64 `json:"assigned_to"`
	RewardCents int64  `json:"reward_cents"`
	Status      string `json:"status"` // todo, waiting (done, needs an OK), approved, rejected
	DoneBy      *int64 `json:"done_by"`
	DoneID      *int64 `json:"done_id"`
	Late        bool   `json:"late"`
}

// ListChores returns every chore.
func (s *Store) ListChores() ([]Chore, error) {
	rows, err := s.DB.Query(`SELECT id,name,assigned_to,freq,interval,start_date,reward_cents,active FROM chores ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Chore{}
	for rows.Next() {
		var c Chore
		var a sql.NullInt64
		var act int
		rows.Scan(&c.ID, &c.Name, &a, &c.Freq, &c.Interval, &c.StartDate, &c.RewardCents, &act)
		c.AssignedTo, c.Active = idPtr(a), act == 1
		out = append(out, c)
	}
	return out, nil
}

// SaveChore adds or updates a chore (adults only; checked by the caller).
func (s *Store) SaveChore(uid int64, c *Chore) error {
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" {
		return errors.New("name the chore")
	}
	if _, err := time.Parse(DateFmt, c.StartDate); err != nil {
		return errors.New("start date must be YYYY-MM-DD")
	}
	if c.Freq == "none" {
		c.Freq = "once"
	}
	if !Freqs[c.Freq] {
		return errors.New("bad repeat")
	}
	if c.Interval < 1 {
		c.Interval = 1
	}
	if c.RewardCents < 0 {
		return errors.New("reward cannot be negative")
	}
	if c.ID == 0 {
		res, err := s.DB.Exec(`INSERT INTO chores(name,assigned_to,freq,interval,start_date,reward_cents,active,created_by) VALUES(?,?,?,?,?,?,1,?)`, c.Name, nullID(c.AssignedTo), c.Freq, c.Interval, c.StartDate, c.RewardCents, uid)
		if err == nil {
			c.ID, _ = res.LastInsertId()
			c.Active = true
		}
		return err
	}
	return mustChange(s.DB.Exec(`UPDATE chores SET name=?,assigned_to=?,freq=?,interval=?,start_date=?,reward_cents=?,active=? WHERE id=?`, c.Name, nullID(c.AssignedTo), c.Freq, c.Interval, c.StartDate, c.RewardCents, b2i(c.Active), c.ID))
}

// DeleteChore removes a chore.
func (s *Store) DeleteChore(by, id int64) error {
	var name string
	if s.DB.QueryRow(`SELECT name FROM chores WHERE id=?`, id).Scan(&name) != nil {
		return ErrNotFound
	}
	return s.trashThen("chore", name, 0, false, by, []partQuery{
		{"chores", "id=?", []any{id}},
		{"chore_done", "chore_id=?", []any{id}},
	}, func() error { return mustChange(s.DB.Exec(`DELETE FROM chores WHERE id=?`, id)) })
}

// ChoresDue lists chore occurrences from `from` to `to` (dates) with their status. A chore that is not done
// stays on the list as "late" for up to 7 days.
func (s *Store) ChoresDue(from, to time.Time) ([]ChoreDue, error) {
	chores, err := s.ListChores()
	if err != nil {
		return nil, err
	}
	done := map[string]ChoreDue{}
	rows, err := s.DB.Query(`SELECT id,chore_id,occ,user_id,status FROM chore_done WHERE occ>=? AND occ<=?`, from.AddDate(0, 0, -7).Format(DateFmt), to.Format(DateFmt))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, cid, uid int64
		var occ, st string
		rows.Scan(&id, &cid, &occ, &uid, &st)
		did, by := id, uid
		done[fmt.Sprint(cid, "|", occ)] = ChoreDue{Status: st, DoneBy: &by, DoneID: &did}
	}
	rows.Close()
	today := s.Now().Format(DateFmt)
	out := []ChoreDue{}
	for _, c := range chores {
		if !c.Active {
			continue
		}
		st, err := ParseLocal(c.StartDate)
		if err != nil {
			continue
		}
		for _, t := range Occurrences(st, c.Freq, c.Interval, "", from.AddDate(0, 0, -7), to) {
			d := t.Format(DateFmt)
			cd := ChoreDue{ChoreID: c.ID, Name: c.Name, Date: d, AssignedTo: c.AssignedTo, RewardCents: c.RewardCents, Status: "todo"}
			if x, ok := done[fmt.Sprint(c.ID, "|", d)]; ok {
				cd.Status, cd.DoneBy, cd.DoneID = x.Status, x.DoneBy, x.DoneID
			}
			if d < from.Format(DateFmt) && cd.Status != "todo" {
				continue // older ones only while still open
			}
			cd.Late = cd.Status == "todo" && d < today
			out = append(out, cd)
		}
	}
	return out, nil
}

// MarkChoreDone records that someone did a chore. Kids may only tick their own (or unassigned) chores; it then
// waits for an adult's OK before any allowance is paid. An adult's tick is approved at once.
func (s *Store) MarkChoreDone(u *User, choreID int64, occ string) error {
	var assigned sql.NullInt64
	var reward int64
	if s.DB.QueryRow(`SELECT assigned_to,reward_cents FROM chores WHERE id=?`, choreID).Scan(&assigned, &reward) != nil {
		return ErrNotFound
	}
	if _, err := time.Parse(DateFmt, occ); err != nil {
		return errors.New("bad date")
	}
	if u.Role == RoleKid && assigned.Valid && assigned.Int64 != u.ID {
		return ErrForbidden
	}
	status := "waiting"
	if u.CanFinance() {
		status = "approved"
	}
	res, err := s.DB.Exec(`INSERT OR IGNORE INTO chore_done(chore_id,occ,user_id,done_at,status) VALUES(?,?,?,?,?)`, choreID, occ, u.ID, s.nowStr(), status)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("already ticked")
	}
	if status == "approved" {
		id, _ := res.LastInsertId()
		s.payChore(id, u.ID)
	}
	return nil
}

// DecideChore approves (pays the reward to whoever did it) or rejects a ticked chore. Adults only.
func (s *Store) DecideChore(by *User, doneID int64, approve bool) error {
	if !by.CanFinance() {
		return ErrForbidden
	}
	st := "rejected"
	if approve {
		st = "approved"
	}
	if err := mustChange(s.DB.Exec(`UPDATE chore_done SET status=?, decided_by=? WHERE id=? AND status='waiting'`, st, by.ID, doneID)); err != nil {
		return errors.New("already decided")
	}
	if approve {
		s.payChore(doneID, by.ID)
	}
	return nil
}

func (s *Store) payChore(doneID, by int64) {
	var reward, kid int64
	var name, occ string
	if s.DB.QueryRow(`SELECT c.reward_cents, d.user_id, c.name, d.occ FROM chore_done d JOIN chores c ON c.id=d.chore_id WHERE d.id=?`, doneID).Scan(&reward, &kid, &name, &occ) != nil || reward <= 0 {
		return
	}
	s.DB.Exec(`INSERT INTO allowance(user_id,date,cents,note,chore_done_id,by_user) VALUES(?,?,?,?,?,?)`, kid, s.Now().Format(DateFmt), reward, name+" ("+occ+")", doneID, by)
}

// UndoChore removes a tick that has not been paid out yet (and its reward).
func (s *Store) UndoChore(u *User, doneID int64) error {
	var owner int64
	var st string
	if s.DB.QueryRow(`SELECT user_id,status FROM chore_done WHERE id=?`, doneID).Scan(&owner, &st) != nil {
		return ErrNotFound
	}
	if owner != u.ID && !u.CanFinance() {
		return ErrForbidden
	}
	if st == "approved" && !u.CanFinance() {
		return errors.New("ask an adult to undo an approved chore")
	}
	s.DB.Exec(`DELETE FROM allowance WHERE chore_done_id=?`, doneID)
	_, err := s.DB.Exec(`DELETE FROM chore_done WHERE id=?`, doneID)
	return err
}

// AllowanceEntry is one line of a kid's allowance ledger.
type AllowanceEntry struct {
	ID    int64  `json:"id"`
	Date  string `json:"date"`
	Cents int64  `json:"cents"`
	Note  string `json:"note"`
	By    int64  `json:"by"`
}

// Allowance returns a member's balance and recent entries. Kids may only see their own.
func (s *Store) Allowance(viewer *User, userID int64) (int64, []AllowanceEntry, error) {
	if viewer.Role == RoleKid && viewer.ID != userID {
		return 0, nil, ErrForbidden
	}
	var bal int64
	s.DB.QueryRow(`SELECT COALESCE(SUM(cents),0) FROM allowance WHERE user_id=?`, userID).Scan(&bal)
	rows, err := s.DB.Query(`SELECT id,date,cents,note,by_user FROM allowance WHERE user_id=? ORDER BY date DESC, id DESC LIMIT 100`, userID)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	out := []AllowanceEntry{}
	for rows.Next() {
		var e AllowanceEntry
		rows.Scan(&e.ID, &e.Date, &e.Cents, &e.Note, &e.By)
		out = append(out, e)
	}
	return bal, out, nil
}

// AddAllowance records a payout (negative), bonus or weekly allowance (positive). Adults only.
func (s *Store) AddAllowance(by *User, userID, cents int64, note string) error {
	if !by.CanFinance() {
		return ErrForbidden
	}
	if cents == 0 {
		return errors.New("amount is required")
	}
	if _, err := s.GetUser(userID); err != nil {
		return err
	}
	_, err := s.DB.Exec(`INSERT INTO allowance(user_id,date,cents,note,by_user) VALUES(?,?,?,?,?)`, userID, s.Now().Format(DateFmt), cents, strings.TrimSpace(note), by.ID)
	return err
}

// ---------- meal plan ----------

// Meal is one planned meal.
type Meal struct {
	ID          int64  `json:"id"`
	Date        string `json:"date"`
	Slot        string `json:"slot"` // breakfast, lunch, dinner, snack
	Title       string `json:"title"`
	Ingredients string `json:"ingredients"` // one per line
	Notes       string `json:"notes"`
}

var mealSlots = map[string]bool{"breakfast": true, "lunch": true, "dinner": true, "snack": true}

// Meals lists meals between two dates.
func (s *Store) Meals(from, to string) ([]Meal, error) {
	rows, err := s.DB.Query(`SELECT id,date,slot,title,ingredients,notes FROM meals WHERE date>=? AND date<=? ORDER BY date, CASE slot WHEN 'breakfast' THEN 1 WHEN 'lunch' THEN 2 WHEN 'snack' THEN 3 ELSE 4 END`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Meal{}
	for rows.Next() {
		var m Meal
		rows.Scan(&m.ID, &m.Date, &m.Slot, &m.Title, &m.Ingredients, &m.Notes)
		out = append(out, m)
	}
	return out, nil
}

// SaveMeal adds or updates a meal.
func (s *Store) SaveMeal(uid int64, m *Meal) error {
	m.Title = strings.TrimSpace(m.Title)
	if m.Title == "" {
		return errors.New("what's for the meal?")
	}
	if _, err := time.Parse(DateFmt, m.Date); err != nil {
		return errors.New("date must be YYYY-MM-DD")
	}
	if !mealSlots[m.Slot] {
		m.Slot = "dinner"
	}
	if m.ID == 0 {
		res, err := s.DB.Exec(`INSERT INTO meals(date,slot,title,ingredients,notes,created_by) VALUES(?,?,?,?,?,?)`, m.Date, m.Slot, m.Title, m.Ingredients, m.Notes, uid)
		if err == nil {
			m.ID, _ = res.LastInsertId()
		}
		return err
	}
	return mustChange(s.DB.Exec(`UPDATE meals SET date=?,slot=?,title=?,ingredients=?,notes=? WHERE id=?`, m.Date, m.Slot, m.Title, m.Ingredients, m.Notes, m.ID))
}

// DeleteMeal removes a meal.
func (s *Store) DeleteMeal(by, id int64) error {
	var t, d string
	if s.DB.QueryRow(`SELECT title,date FROM meals WHERE id=?`, id).Scan(&t, &d) != nil {
		return ErrNotFound
	}
	return s.trashThen("meal", t+" ("+d+")", 0, false, by, []partQuery{{"meals", "id=?", []any{id}}},
		func() error { return mustChange(s.DB.Exec(`DELETE FROM meals WHERE id=?`, id)) })
}

// MealsToList adds the ingredients of meals in a date range to a shopping list, skipping ones already open on it.
func (s *Store) MealsToList(uid, listID int64, from, to string) (int, error) {
	l, err := s.GetList(uid, listID)
	if err != nil {
		return 0, err
	}
	have := map[string]bool{}
	for _, it := range l.Items {
		if !it.Done {
			have[strings.ToLower(it.Text)] = true
		}
	}
	meals, err := s.Meals(from, to)
	if err != nil {
		return 0, err
	}
	var lines []string
	for _, m := range meals {
		for _, ing := range strings.Split(m.Ingredients, "\n") {
			ing = strings.TrimSpace(ing)
			key := strings.ToLower(ing)
			if f := strings.Fields(key); len(f) > 1 && f[0][0] >= '0' && f[0][0] <= '9' {
				key = strings.Join(f[1:], " ")
			}
			if ing == "" || have[key] {
				continue
			}
			have[key] = true
			lines = append(lines, ing)
		}
	}
	if len(lines) == 0 {
		return 0, nil
	}
	items, err := s.AddItems(uid, listID, strings.Join(lines, "\n"), nil, "")
	return len(items), err
}
