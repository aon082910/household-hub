package core

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
)

// Notification is a message in a user's bell list.
type Notification struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	Link     string `json:"link"`
	Created  string `json:"created"`
	Read     bool   `json:"read"`
	Delivery string `json:"delivery"`
}

// AddNotification stores a bell message for a user.
func (s *Store) AddNotification(uid int64, m Message) (int64, error) {
	res, err := s.DB.Exec(`INSERT INTO notifications(user_id,title,body,link,created) VALUES(?,?,?,?,?)`, uid, m.Title, m.Body, m.Link, s.nowStr())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListNotifications returns a user's newest messages (afterID > 0: only newer ones, oldest first).
func (s *Store) ListNotifications(uid, afterID int64, limit int) ([]Notification, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT id,title,body,link,created,read,delivery FROM notifications WHERE user_id=? ORDER BY id DESC LIMIT ?`
	args := []any{uid, limit}
	if afterID > 0 {
		q = `SELECT id,title,body,link,created,read,delivery FROM notifications WHERE user_id=? AND id>? ORDER BY id LIMIT ?`
		args = []any{uid, afterID, limit}
	}
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Notification{}
	for rows.Next() {
		var n Notification
		var r int
		rows.Scan(&n.ID, &n.Title, &n.Body, &n.Link, &n.Created, &r, &n.Delivery)
		n.Read = r == 1
		out = append(out, n)
	}
	return out, rows.Err()
}

// UnreadCount counts unread bell messages.
func (s *Store) UnreadCount(uid int64) (n int) {
	s.DB.QueryRow(`SELECT COUNT(*) FROM notifications WHERE user_id=? AND read=0`, uid).Scan(&n)
	return
}

// LatestNotificationID returns the newest id for a user (0 if none).
func (s *Store) LatestNotificationID(uid int64) (n int64) {
	s.DB.QueryRow(`SELECT COALESCE(MAX(id),0) FROM notifications WHERE user_id=?`, uid).Scan(&n)
	return
}

// MarkRead marks one (id>0) or all (id=0) messages read.
func (s *Store) MarkRead(uid, id int64) error {
	if id == 0 {
		_, err := s.DB.Exec(`UPDATE notifications SET read=1 WHERE user_id=?`, uid)
		return err
	}
	_, err := s.DB.Exec(`UPDATE notifications SET read=1 WHERE user_id=? AND id=?`, uid, id)
	return err
}

// ClearNotifications deletes a user's read messages.
func (s *Store) ClearNotifications(uid int64) error {
	_, err := s.DB.Exec(`DELETE FROM notifications WHERE user_id=? AND read=1`, uid)
	return err
}

// Sender is what the reminder engine uses to reach people (a *Notifier in production, a fake in tests).
type Sender interface {
	Deliver(u *User, m Message) string
	Household(m Message) error
}

// claim records that a reminder went out. It returns false when it was already sent, so a reminder is never
// sent twice, even after a restart or when two ticks overlap.
func (s *Store) claim(kind string, id int64, occ, lead string, uid int64) bool {
	res, err := s.DB.Exec(`INSERT OR IGNORE INTO sent_log(kind,item_id,occ,lead,user_id,sent) VALUES(?,?,?,?,?,?)`, kind, id, occ, lead, uid, s.nowStr())
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n == 1
}

// inQuiet reports whether t is inside the user's quiet hours, and when they end.
func inQuiet(u *User, t time.Time) (bool, time.Time) {
	a, b := u.QuietStart, u.QuietEnd
	if a < 0 || b < 0 || a == b {
		return false, t
	}
	h := t.Hour()
	in := (a < b && h >= a && h < b) || (a > b && (h >= a || h < b))
	if !in {
		return false, t
	}
	end := time.Date(t.Year(), t.Month(), t.Day(), b, 0, 0, 0, t.Location())
	if !end.After(t) {
		end = end.AddDate(0, 0, 1)
	}
	return true, end
}

// notify stores the bell message and sends it out on the user's channels. During the user's quiet hours the
// whole message waits and goes out when they end.
func (s *Store) notify(send Sender, u *User, m Message) {
	if q, until := inQuiet(u, s.Now()); q {
		b, _ := json.Marshal(m)
		s.DB.Exec(`INSERT INTO pending_sends(user_id,message,due) VALUES(?,?,?)`, u.ID, string(b), until.Format(time.RFC3339))
		return
	}
	s.deliverNow(send, u, m)
}

// FlushPending sends messages held back by quiet hours once they are due.
func (s *Store) FlushPending(send Sender) {
	rows, err := s.DB.Query(`SELECT id,user_id,message FROM pending_sends WHERE due<=? ORDER BY id`, s.nowStr())
	if err != nil {
		return
	}
	type p struct {
		id, uid int64
		msg     string
	}
	var list []p
	for rows.Next() {
		var x p
		rows.Scan(&x.id, &x.uid, &x.msg)
		list = append(list, x)
	}
	rows.Close()
	for _, x := range list {
		s.DB.Exec(`DELETE FROM pending_sends WHERE id=?`, x.id)
		u, err := s.GetUser(x.uid)
		if err != nil || !u.Active {
			continue
		}
		var m Message
		if json.Unmarshal([]byte(x.msg), &m) == nil {
			s.deliverNow(send, u, m)
		}
	}
}

func (s *Store) deliverNow(send Sender, u *User, m Message) {
	id, err := s.AddNotification(u.ID, m)
	if err != nil {
		log.Printf("reminder for %s: %v", u.Username, err)
		return
	}
	if send == nil {
		return
	}
	if d := send.Deliver(u, m); d != "" {
		s.DB.Exec(`UPDATE notifications SET delivery=? WHERE id=?`, d, id)
	}
}

func hm(t time.Time) string { return t.Format("3:04 PM") }

func whenText(now, t time.Time, allDay bool) string {
	d0 := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	d1 := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, now.Location())
	days := int(d1.Sub(d0).Hours()/24 + 0.5)
	var day string
	switch {
	case days == 0:
		day = "today"
	case days == 1:
		day = "tomorrow"
	case days == -1:
		day = "yesterday"
	case days > 1 && days < 7:
		day = t.Format("Monday")
	default:
		day = t.Format("Mon Jan 2")
	}
	if allDay {
		return day
	}
	return day + " at " + hm(t)
}

const (
	eventGrace = 6 * time.Hour  // a reminder missed by less than this (server was off) still goes out
	billGrace  = 20 * time.Hour // bill reminders fire once on the day they are due to fire
)

// RunReminders sends every reminder due at `now`. It returns how many messages went out.
func (s *Store) RunReminders(now time.Time, send Sender) (int, error) {
	users, err := s.ListUsers()
	if err != nil {
		return 0, err
	}
	byID := map[int64]*User{}
	var active []*User
	for _, u := range users {
		byID[u.ID] = u
		if u.Active {
			active = append(active, u)
		}
	}
	cfg := s.Settings(false)
	billHour, _ := strconv.Atoi(cfg["bill_remind_hour"])
	allDayHour, _ := strconv.Atoi(cfg["allday_remind_hour"])
	base := strings.TrimRight(cfg["base_url"], "/")
	sent := 0

	// ---- events ----
	evs, err := s.ListEvents(0)
	if err != nil {
		return 0, err
	}
	for _, e := range evs {
		leads := ParseInts(e.RemindMinutes)
		if len(leads) == 0 {
			continue
		}
		maxLead := 0
		for _, l := range leads {
			if l > maxLead {
				maxLead = l
			}
		}
		from := now.Add(-eventGrace)
		to := now.Add(time.Duration(maxLead)*time.Minute + 24*time.Hour)
		st, err := ParseLocal(e.Start)
		if err != nil {
			continue
		}
		for _, t := range Occurrences(st, e.Freq, e.Interval, e.Until, from, to) {
			at := t
			if e.AllDay {
				at = time.Date(t.Year(), t.Month(), t.Day(), allDayHour, 0, 0, 0, t.Location())
			}
			if at.Before(now.Add(-15 * time.Minute)) {
				continue // already started
			}
			occ := t.Format(DateTimeFmt)
			for _, lead := range leads {
				fire := at.Add(-time.Duration(lead) * time.Minute)
				if fire.After(now) || now.Sub(fire) > eventGrace {
					continue
				}
				m := Message{Title: e.Title + " " + whenText(now, t, e.AllDay)}
				var parts []string
				if e.Location != "" {
					parts = append(parts, e.Location)
				}
				if e.Notes != "" {
					parts = append(parts, e.Notes)
				}
				m.Body = strings.Join(parts, " - ")
				if base != "" {
					m.Link = base + "/#calendar?d=" + t.Format(DateFmt)
				}
				for _, u := range eventRecipients(e, byID, active) {
					if s.claim("event", e.ID, occ, strconv.Itoa(lead), u.ID) {
						s.notify(send, u, m)
						sent++
					}
				}
				if !e.Private && send != nil && s.claim("event", e.ID, occ, strconv.Itoa(lead), 0) {
					send.Household(m)
				}
			}
		}
	}

	// ---- bills ----
	bills, err := s.ListBills(0)
	if err != nil {
		return sent, err
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	for _, b := range bills {
		if !b.Active {
			continue
		}
		days := ParseInts(b.RemindDays)
		maxDays := 0
		for _, d := range days {
			if d > maxDays {
				maxDays = d
			}
		}
		st, err := ParseLocal(b.StartDate)
		if err != nil {
			continue
		}
		occs := Occurrences(st, b.Freq, b.Interval, b.Until, today.AddDate(0, 0, -8), today.AddDate(0, 0, maxDays+1))
		for _, due := range occs {
			occ := due.Format(DateFmt)
			var paid int
			s.DB.QueryRow(`SELECT COUNT(*) FROM bill_payments WHERE bill_id=? AND occ_date=?`, b.ID, occ).Scan(&paid)
			if paid > 0 {
				continue
			}
			// autopay: mark it paid (and record the expense) once the due day has come
			if b.Autopay && !due.After(today) {
				if s.claim("autopay", b.ID, occ, "", 0) {
					if err := s.payBill(b.OwnerID, b.ID, occ, occ, b.AmountCents, true, "autopay", 0); err != nil {
						log.Printf("autopay %s %s: %v", b.Name, occ, err)
					}
				}
				continue
			}
			amt := ""
			if b.AmountCents != 0 {
				amt = " " + cfg["currency"] + FormatCents(b.AmountCents)
			}
			type step struct {
				lead string
				fire time.Time
				msg  Message
			}
			var steps []step
			for _, d := range days {
				fire := time.Date(due.Year(), due.Month(), due.Day()-d, billHour, 0, 0, 0, due.Location())
				verb := "is due"
				if b.Autopay {
					verb = "auto-pays"
				}
				steps = append(steps, step{strconv.Itoa(d), fire, Message{Title: fmt.Sprintf("%s%s %s %s", b.Name, amt, verb, whenText(now, due, true))}})
			}
			if cfg["overdue_reminders"] == "1" && !b.Autopay {
				fire := time.Date(due.Year(), due.Month(), due.Day()+1, billHour, 0, 0, 0, due.Location())
				steps = append(steps, step{"overdue", fire, Message{Title: fmt.Sprintf("OVERDUE: %s%s was due %s", b.Name, amt, whenText(now, due, true))}})
			}
			for _, stp := range steps {
				if stp.fire.After(now) || now.Sub(stp.fire) > billGrace {
					continue
				}
				m := stp.msg
				m.Body = b.Notes
				if b.URL != "" {
					m.Body = strings.TrimSpace(m.Body + " Pay: " + b.URL)
				}
				if base != "" {
					m.Link = base + "/#bills"
				}
				for _, u := range billRecipients(b, byID, active) {
					if s.claim("bill", b.ID, occ, stp.lead, u.ID) {
						um := m
						if base != "" && u.HasChannel("ntfy") && u.NtfyTopic != "" && u.CanFinance() {
							um.Actions = []Action{{Label: "Mark paid", URL: base + "/act/" + s.NewActionToken(b.ID, occ, u.ID)}}
						}
						s.notify(send, u, um)
						sent++
					}
				}
				if !b.Private && send != nil && s.claim("bill", b.ID, occ, stp.lead, 0) {
					send.Household(m)
				}
			}
		}
	}
	return sent, nil
}

// eventRecipients: the chosen members, else everyone who can see the event.
func eventRecipients(e Event, byID map[int64]*User, active []*User) []*User {
	if e.Private {
		if u := byID[e.OwnerID]; u != nil && u.Active {
			return []*User{u}
		}
		return nil
	}
	var out []*User
	if ids := e.MemberIDs(); len(ids) > 0 {
		for _, id := range ids {
			if u := byID[id]; u != nil && u.Active {
				out = append(out, u)
			}
		}
		return out
	}
	return active
}

// billRecipients: the assigned person, else the owner of a private bill, else every adult.
func billRecipients(b Bill, byID map[int64]*User, active []*User) []*User {
	if b.Private {
		if u := byID[b.OwnerID]; u != nil && u.Active {
			return []*User{u}
		}
		return nil
	}
	if b.AssignedTo != nil {
		if u := byID[*b.AssignedTo]; u != nil && u.Active {
			return []*User{u}
		}
	}
	var out []*User
	for _, u := range active {
		if u.CanFinance() {
			out = append(out, u)
		}
	}
	return out
}

// RunEngine checks reminders every minute until ctx ends. It also trims old sent-log rows and notifications.
func (s *Store) RunEngine(ctx context.Context, send Sender) {
	tick := func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("reminder engine panic: %v", r)
			}
		}()
		s.DailyChecks(s.Now(), send)
		s.Digests(s.Now(), send)
		s.FlushPending(send)
		s.PurgeTrash()
		if n, err := s.RunReminders(s.Now(), send); err != nil {
			log.Printf("reminders: %v", err)
		} else if n > 0 {
			log.Printf("reminders: %d sent", n)
		}
	}
	tick()
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	last := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			tick()
			if time.Since(last) > 24*time.Hour {
				last = time.Now()
				cut := s.Now().AddDate(0, 0, -90).Format(time.RFC3339)
				s.DB.Exec(`DELETE FROM sent_log WHERE sent<?`, cut)
				s.DB.Exec(`DELETE FROM notifications WHERE read=1 AND created<?`, cut)
				s.DB.Exec(`DELETE FROM sessions WHERE expires<?`, s.nowStr())
				s.DB.Exec(`DELETE FROM audit_log WHERE at<?`, s.Now().AddDate(-1, 0, 0).Format(time.RFC3339))
			}
		}
	}
}
