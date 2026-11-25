package core

import (
	"database/sql"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Event is an appointment, due date or other calendar entry.
type Event struct {
	ID            int64  `json:"id"`
	Title         string `json:"title"`
	Start         string `json:"start"` // "2006-01-02T15:04", or "2006-01-02" when all day
	End           string `json:"end"`
	AllDay        bool   `json:"all_day"`
	Location      string `json:"location"`
	Notes         string `json:"notes"`
	Kind          string `json:"kind"` // appointment, due, birthday, school, other
	Freq          string `json:"freq"`
	Interval      int    `json:"interval"`
	Until         string `json:"until"`
	RemindMinutes string `json:"remind_minutes"` // "60,1440"
	Members       string `json:"members"`        // user ids "1,3"; empty = everyone who can see it
	OwnerID       int64  `json:"owner_id"`
	Private       bool   `json:"private"`
	Color         string `json:"color"`
}

// MemberIDs parses Members.
func (e *Event) MemberIDs() []int64 {
	var out []int64
	for _, p := range strings.Split(e.Members, ",") {
		if n, err := strconv.ParseInt(strings.TrimSpace(p), 10, 64); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// Occurrence is one showing of an event or a bill on the calendar.
type Occurrence struct {
	Key      string `json:"key"` // "e:<id>:<start>" or "b:<id>:<date>"
	Source   string `json:"source"`
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Start    string `json:"start"`
	End      string `json:"end"`
	AllDay   bool   `json:"all_day"`
	Location string `json:"location"`
	Kind     string `json:"kind"`
	Members  string `json:"members"`
	OwnerID  int64  `json:"owner_id"`
	Private  bool   `json:"private"`
	Color    string `json:"color"`
	Repeats  bool   `json:"repeats"`
	// bills only
	AmountCents int64 `json:"amount_cents,omitempty"`
	Paid        bool  `json:"paid,omitempty"`
	Overdue     bool  `json:"overdue,omitempty"`
}

const eventCols = `id,title,start_at,end_at,all_day,location,notes,kind,freq,interval,until,remind_minutes,members,owner_id,private,color`

func scanEvent(r interface{ Scan(...any) error }) (Event, error) {
	var e Event
	var ad, p int
	err := r.Scan(&e.ID, &e.Title, &e.Start, &e.End, &ad, &e.Location, &e.Notes, &e.Kind, &e.Freq, &e.Interval, &e.Until, &e.RemindMinutes, &e.Members, &e.OwnerID, &p, &e.Color)
	e.AllDay, e.Private = ad == 1, p == 1
	return e, err
}

// ListEvents returns visible events (uid 0 = all, for the reminder engine).
func (s *Store) ListEvents(uid int64) ([]Event, error) {
	q, args := `SELECT `+eventCols+` FROM events`, []any{}
	if uid != 0 {
		q += ` WHERE ` + visible
		args = append(args, uid)
	}
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetEvent loads one visible event.
func (s *Store) GetEvent(uid, id int64) (Event, error) {
	e, err := scanEvent(s.DB.QueryRow(`SELECT `+eventCols+` FROM events WHERE id=? AND `+visible, id, uid))
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNotFound
	}
	return e, err
}

func normEvent(e *Event) error {
	e.Title = strings.TrimSpace(e.Title)
	if e.Title == "" {
		return errors.New("title is required")
	}
	st, err := ParseLocal(e.Start)
	if err != nil {
		return errors.New("start must be a date or date and time")
	}
	if e.AllDay {
		e.Start = st.Format(DateFmt)
		if e.End != "" {
			if en, err := ParseLocal(e.End); err == nil && !en.Before(st) {
				e.End = en.Format(DateFmt)
			} else {
				e.End = ""
			}
		}
	} else {
		e.Start = st.Format(DateTimeFmt)
		if e.End != "" {
			en, err := ParseLocal(e.End)
			if err != nil || en.Before(st) {
				return errors.New("end must be after start")
			}
			e.End = en.Format(DateTimeFmt)
		}
	}
	if e.Freq == "" || e.Freq == "once" {
		e.Freq = "none"
	}
	if !Freqs[e.Freq] {
		return errors.New("bad repeat")
	}
	if e.Interval < 1 {
		e.Interval = 1
	}
	if e.Kind == "" {
		e.Kind = "appointment"
	}
	var mins []string
	for _, n := range ParseInts(e.RemindMinutes) {
		mins = append(mins, strconv.Itoa(n))
	}
	e.RemindMinutes = strings.Join(mins, ",")
	var mem []string
	for _, id := range e.MemberIDs() {
		mem = append(mem, strconv.FormatInt(id, 10))
	}
	e.Members = strings.Join(mem, ",")
	return nil
}

// SaveEvent inserts or updates an event.
func (s *Store) SaveEvent(uid int64, e *Event) error {
	if err := normEvent(e); err != nil {
		return err
	}
	if e.ID == 0 {
		res, err := s.DB.Exec(`INSERT INTO events(title,start_at,end_at,all_day,location,notes,kind,freq,interval,until,remind_minutes,members,owner_id,private,color) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			e.Title, e.Start, e.End, b2i(e.AllDay), e.Location, e.Notes, e.Kind, e.Freq, e.Interval, e.Until, e.RemindMinutes, e.Members, uid, b2i(e.Private), e.Color)
		if err == nil {
			e.ID, _ = res.LastInsertId()
			e.OwnerID = uid
		}
		return err
	}
	return mustChange(s.DB.Exec(`UPDATE events SET title=?,start_at=?,end_at=?,all_day=?,location=?,notes=?,kind=?,freq=?,interval=?,until=?,remind_minutes=?,members=?,private=?,color=? WHERE id=? AND `+visible,
		e.Title, e.Start, e.End, b2i(e.AllDay), e.Location, e.Notes, e.Kind, e.Freq, e.Interval, e.Until, e.RemindMinutes, e.Members, b2i(e.Private), e.Color, e.ID, uid))
}

// DeleteEvent removes an event.
func (s *Store) DeleteEvent(uid, id int64) error {
	return mustChange(s.DB.Exec(`DELETE FROM events WHERE id=? AND `+visible, id, uid))
}

// eventOccurrences expands one event in [from, to]. An occurrence that started before `from` but is still
// running is included (multi-day events).
func eventOccurrences(e Event, from, to time.Time) []Occurrence {
	st, err := ParseLocal(e.Start)
	if err != nil {
		return nil
	}
	var dur time.Duration
	if e.End != "" {
		if en, err := ParseLocal(e.End); err == nil {
			dur = en.Sub(st)
		}
	}
	look := from.Add(-dur)
	if e.AllDay {
		look = look.AddDate(0, 0, -1)
	}
	var out []Occurrence
	for _, t := range Occurrences(st, e.Freq, e.Interval, e.Until, look, to) {
		o := Occurrence{Source: "event", ID: e.ID, Title: e.Title, AllDay: e.AllDay, Location: e.Location, Kind: e.Kind, Members: e.Members,
			OwnerID: e.OwnerID, Private: e.Private, Color: e.Color, Repeats: e.Freq != "none"}
		if e.AllDay {
			o.Start = t.Format(DateFmt)
			if dur > 0 {
				o.End = t.Add(dur).Format(DateFmt)
			}
		} else {
			o.Start = t.Format(DateTimeFmt)
			if dur > 0 {
				o.End = t.Add(dur).Format(DateTimeFmt)
			}
		}
		endT := t.Add(dur)
		if e.AllDay {
			endT = endT.AddDate(0, 0, 1)
		}
		if endT.Before(from) {
			continue
		}
		o.Key = "e:" + strconv.FormatInt(e.ID, 10) + ":" + o.Start
		out = append(out, o)
	}
	return out
}

// Calendar returns visible events and bill due dates between from and to (dates, inclusive).
func (s *Store) Calendar(u *User, from, to time.Time, withBills bool) ([]Occurrence, error) {
	evs, err := s.ListEvents(u.ID)
	if err != nil {
		return nil, err
	}
	to = to.Add(24*time.Hour - time.Minute)
	out := []Occurrence{}
	for _, e := range evs {
		out = append(out, eventOccurrences(e, from, to)...)
	}
	if withBills && u.CanFinance() {
		dues, err := s.BillDues(u.ID, from, to)
		if err != nil {
			return nil, err
		}
		for _, d := range dues {
			out = append(out, Occurrence{Key: "b:" + strconv.FormatInt(d.BillID, 10) + ":" + d.Date, Source: "bill", ID: d.BillID,
				Title: d.Name, Start: d.Date, AllDay: true, Kind: "bill", AmountCents: d.AmountCents, Paid: d.Paid, Overdue: d.Overdue, Repeats: true})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out, nil
}
