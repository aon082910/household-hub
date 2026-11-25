package core

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// CalendarFeed is an outside calendar (school, sports, work, Google/Outlook "secret address") read into the
// household calendar. It is one-way: nothing is written back.
type CalendarFeed struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	Color     string `json:"color"`
	Members   string `json:"members"`
	Enabled   bool   `json:"enabled"`
	LastFetch string `json:"last_fetch"`
	LastError string `json:"last_error"`
	Skipped   int    `json:"skipped"` // events with repeat rules this reader does not understand
	Items     int    `json:"items"`
}

// ListFeeds returns the calendar subscriptions.
func (s *Store) ListFeeds() ([]CalendarFeed, error) {
	rows, err := s.DB.Query(`SELECT id,name,url,color,members,enabled,last_fetch,last_error,skipped,(SELECT COUNT(*) FROM feed_items i WHERE i.feed_id=calendar_feeds.id) FROM calendar_feeds ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CalendarFeed{}
	for rows.Next() {
		var f CalendarFeed
		var en int
		rows.Scan(&f.ID, &f.Name, &f.URL, &f.Color, &f.Members, &en, &f.LastFetch, &f.LastError, &f.Skipped, &f.Items)
		f.Enabled = en == 1
		out = append(out, f)
	}
	return out, nil
}

func cleanFeedURL(u string) (string, error) {
	u = strings.TrimSpace(u)
	if strings.HasPrefix(strings.ToLower(u), "webcal://") {
		u = "https://" + u[9:]
	}
	p, err := url.Parse(u)
	if err != nil || (p.Scheme != "http" && p.Scheme != "https") || p.Host == "" {
		return "", errors.New("the address must start with https:// (or webcal://)")
	}
	return p.String(), nil
}

// SaveFeed adds or updates a subscription.
func (s *Store) SaveFeed(f *CalendarFeed) error {
	f.Name = strings.TrimSpace(f.Name)
	if f.Name == "" {
		return errors.New("name the calendar")
	}
	u, err := cleanFeedURL(f.URL)
	if err != nil {
		return err
	}
	f.URL = u
	if f.Color == "" {
		f.Color = "#8a63d2"
	}
	if f.ID == 0 {
		res, err := s.DB.Exec(`INSERT INTO calendar_feeds(name,url,color,members,enabled) VALUES(?,?,?,?,?)`, f.Name, f.URL, f.Color, f.Members, b2i(f.Enabled))
		if err == nil {
			f.ID, _ = res.LastInsertId()
		}
		return err
	}
	return mustChange(s.DB.Exec(`UPDATE calendar_feeds SET name=?,url=?,color=?,members=?,enabled=?,last_fetch='' WHERE id=?`, f.Name, f.URL, f.Color, f.Members, b2i(f.Enabled), f.ID))
}

// DeleteFeed removes a subscription and its events.
func (s *Store) DeleteFeed(id int64) error {
	return mustChange(s.DB.Exec(`DELETE FROM calendar_feeds WHERE id=?`, id))
}

// FeedItem is one imported occurrence.
type FeedItem struct {
	UID      string
	Start    time.Time
	End      time.Time
	AllDay   bool
	Title    string
	Location string
}

// FetchFeed downloads one calendar (20 s, 5 MB) and stores its occurrences from 30 days ago to 400 days ahead.
func (s *Store) FetchFeed(id int64, client *http.Client) (int, error) {
	var u string
	if err := s.DB.QueryRow(`SELECT url FROM calendar_feeds WHERE id=?`, id).Scan(&u); err != nil {
		return 0, ErrNotFound
	}
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	fail := func(err error) (int, error) {
		s.DB.Exec(`UPDATE calendar_feeds SET last_fetch=?, last_error=? WHERE id=?`, s.nowStr(), trunc(err.Error(), 300), id)
		return 0, err
	}
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("User-Agent", "HouseholdHub/1 (calendar reader)")
	resp, err := client.Do(req)
	if err != nil {
		return fail(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fail(fmt.Errorf("the calendar address answered HTTP %d", resp.StatusCode))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return fail(err)
	}
	now := s.Now()
	items, skipped, err := ParseICS(string(data), now.AddDate(0, 0, -30), now.AddDate(0, 0, 400))
	if err != nil {
		return fail(err)
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	tx.Exec(`DELETE FROM feed_items WHERE feed_id=?`, id)
	for _, it := range items {
		st, en := it.Start.In(time.Local).Format(DateTimeFmt), ""
		if it.AllDay {
			st = it.Start.Format(DateFmt)
			if !it.End.IsZero() && it.End.After(it.Start.AddDate(0, 0, 1)) {
				en = it.End.AddDate(0, 0, -1).Format(DateFmt) // DTEND is exclusive for all-day events
			}
		} else if !it.End.IsZero() && it.End.After(it.Start) {
			en = it.End.In(time.Local).Format(DateTimeFmt)
		}
		if _, err := tx.Exec(`INSERT INTO feed_items(feed_id,uid,start_at,end_at,all_day,title,location) VALUES(?,?,?,?,?,?,?)`, id, it.UID, st, en, b2i(it.AllDay), trunc(it.Title, 200), trunc(it.Location, 200)); err != nil {
			return 0, err
		}
	}
	tx.Exec(`UPDATE calendar_feeds SET last_fetch=?, last_error='', skipped=? WHERE id=?`, s.nowStr(), skipped, id)
	return len(items), tx.Commit()
}

// FetchDueFeeds refreshes every enabled feed older than an hour.
func (s *Store) FetchDueFeeds() {
	feeds, _ := s.ListFeeds()
	for _, f := range feeds {
		if !f.Enabled {
			continue
		}
		if t, err := time.Parse(time.RFC3339, f.LastFetch); err == nil && s.Now().Sub(t) < 55*time.Minute {
			continue
		}
		s.FetchFeed(f.ID, nil)
	}
}

// feedOccurrences returns imported items between two dates as calendar occurrences.
func (s *Store) feedOccurrences(from, to time.Time) []Occurrence {
	rows, err := s.DB.Query(`SELECT i.id,i.feed_id,i.start_at,i.end_at,i.all_day,i.title,i.location,f.color,f.members,f.name FROM feed_items i JOIN calendar_feeds f ON f.id=i.feed_id
		WHERE f.enabled=1 AND substr(i.start_at,1,10)<=? AND (CASE WHEN i.end_at<>'' THEN substr(i.end_at,1,10) ELSE substr(i.start_at,1,10) END)>=?`, to.Format(DateFmt), from.Format(DateFmt))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Occurrence
	for rows.Next() {
		var o Occurrence
		var fid int64
		var ad int
		var feed string
		rows.Scan(&o.ID, &fid, &o.Start, &o.End, &ad, &o.Title, &o.Location, &o.Color, &o.Members, &feed)
		o.AllDay, o.Source, o.Kind = ad == 1, "feed", feed
		o.Key = fmt.Sprintf("f:%d:%d", fid, o.ID)
		out = append(out, o)
	}
	return out
}

// ---------- the .ics reader ----------

type icsProp struct {
	name   string
	params map[string]string
	value  string
}

func unfoldICS(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if (strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t")) && len(lines) > 0 {
			lines[len(lines)-1] += l[1:]
			continue
		}
		lines = append(lines, strings.TrimRight(l, "\r"))
	}
	return lines
}

func parseProp(line string) icsProp {
	p := icsProp{params: map[string]string{}}
	colon := -1
	inQ := false
	for i, c := range line {
		if c == '"' {
			inQ = !inQ
		}
		if c == ':' && !inQ {
			colon = i
			break
		}
	}
	if colon < 0 {
		p.name = strings.ToUpper(line)
		return p
	}
	head := line[:colon]
	p.value = line[colon+1:]
	parts := strings.Split(head, ";")
	p.name = strings.ToUpper(parts[0])
	for _, kv := range parts[1:] {
		if k, v, ok := strings.Cut(kv, "="); ok {
			p.params[strings.ToUpper(k)] = strings.Trim(v, `"`)
		}
	}
	return p
}

func unescapeICS(s string) string {
	return strings.NewReplacer(`\n`, " ", `\N`, " ", `\,`, ",", `\;`, ";", `\\`, `\`).Replace(s)
}

// parseICSTime reads DATE, DATE-TIME (UTC "Z", with TZID, or floating local).
func parseICSTime(p icsProp) (time.Time, bool, error) {
	v := strings.TrimSpace(p.value)
	if p.params["VALUE"] == "DATE" || len(v) == 8 {
		t, err := time.ParseInLocation("20060102", v[:min(8, len(v))], time.Local)
		return t, true, err
	}
	if strings.HasSuffix(v, "Z") {
		t, err := time.Parse("20060102T150405Z", v)
		return t, false, err
	}
	loc := time.Local
	if tz := p.params["TZID"]; tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		} else if l, ok := windowsZones[tz]; ok {
			loc, _ = time.LoadLocation(l)
		}
	}
	t, err := time.ParseInLocation("20060102T150405", v, loc)
	return t, false, err
}

// a few Windows zone names Outlook writes
var windowsZones = map[string]string{
	"Eastern Standard Time": "America/New_York", "Central Standard Time": "America/Chicago",
	"Mountain Standard Time": "America/Denver", "Pacific Standard Time": "America/Los_Angeles",
	"US Mountain Standard Time": "America/Phoenix", "Alaskan Standard Time": "America/Anchorage",
	"Hawaiian Standard Time": "Pacific/Honolulu", "UTC": "UTC",
}

type icsEvent struct {
	uid, title, location, rrule string
	start, end                  time.Time
	allDay                      bool
	exdates                     map[string]bool
	recurrenceID                string
	cancelled                   bool
}

var weekdays = map[string]time.Weekday{"SU": time.Sunday, "MO": time.Monday, "TU": time.Tuesday, "WE": time.Wednesday, "TH": time.Thursday, "FR": time.Friday, "SA": time.Saturday}

// ParseICS reads an iCalendar file and returns the occurrences inside [from, to]. Repeats with FREQ
// DAILY/WEEKLY/MONTHLY/YEARLY, INTERVAL, COUNT, UNTIL, weekly BYDAY, EXDATE and moved single dates are
// understood; anything else is skipped and counted, never guessed.
func ParseICS(data string, from, to time.Time) ([]FeedItem, int, error) {
	if !strings.Contains(strings.ToUpper(data[:min(len(data), 2000)]), "BEGIN:VCALENDAR") {
		return nil, 0, errors.New("that address did not return a calendar (.ics)")
	}
	var events []*icsEvent
	var cur *icsEvent
	depth := 0
	for _, line := range unfoldICS(data) {
		p := parseProp(line)
		switch {
		case p.name == "BEGIN" && strings.EqualFold(p.value, "VEVENT"):
			cur, depth = &icsEvent{exdates: map[string]bool{}}, 0
			continue
		case p.name == "BEGIN" && cur != nil:
			depth++ // VALARM inside an event
			continue
		case p.name == "END" && cur != nil && depth > 0:
			depth--
			continue
		case p.name == "END" && strings.EqualFold(p.value, "VEVENT") && cur != nil:
			if !cur.start.IsZero() && !cur.cancelled {
				events = append(events, cur)
			}
			cur = nil
			continue
		}
		if cur == nil || depth > 0 {
			continue
		}
		switch p.name {
		case "UID":
			cur.uid = p.value
		case "SUMMARY":
			cur.title = unescapeICS(p.value)
		case "LOCATION":
			cur.location = unescapeICS(p.value)
		case "DTSTART":
			cur.start, cur.allDay, _ = parseICSTime(p)
		case "DTEND":
			cur.end, _, _ = parseICSTime(p)
		case "DURATION":
			if d, ok := parseDuration(p.value); ok && !cur.start.IsZero() {
				cur.end = cur.start.Add(d)
			}
		case "RRULE":
			cur.rrule = p.value
		case "EXDATE":
			for _, v := range strings.Split(p.value, ",") {
				q := p
				q.value = v
				if t, _, err := parseICSTime(q); err == nil {
					cur.exdates[t.UTC().Format(time.RFC3339)] = true
					cur.exdates[t.Format(DateFmt)+"|date"] = true
				}
			}
		case "RECURRENCE-ID":
			if t, _, err := parseICSTime(p); err == nil {
				cur.recurrenceID = t.UTC().Format(time.RFC3339)
			}
		case "STATUS":
			cur.cancelled = strings.EqualFold(p.value, "CANCELLED")
		}
	}
	// moved single dates replace that occurrence of the series
	moved := map[string]bool{}
	for _, e := range events {
		if e.recurrenceID != "" {
			moved[e.uid+"|"+e.recurrenceID] = true
		}
	}
	var out []FeedItem
	skipped := 0
	for _, e := range events {
		if e.title == "" {
			e.title = "(no title)"
		}
		dur := time.Duration(0)
		if !e.end.IsZero() {
			dur = e.end.Sub(e.start)
		}
		emit := func(t time.Time) {
			if moved[e.uid+"|"+t.UTC().Format(time.RFC3339)] && e.recurrenceID == "" {
				return
			}
			if e.exdates[t.UTC().Format(time.RFC3339)] || (e.allDay && e.exdates[t.Format(DateFmt)+"|date"]) {
				return
			}
			end := t.Add(dur)
			if end.Before(from) || t.After(to) {
				return
			}
			it := FeedItem{UID: e.uid, Start: t, AllDay: e.allDay, Title: e.title, Location: e.location}
			if dur > 0 {
				it.End = end
			}
			out = append(out, it)
		}
		if e.rrule == "" || e.recurrenceID != "" {
			emit(e.start)
			continue
		}
		occ, ok := expandRRule(e.start, e.rrule, to)
		if !ok {
			skipped++
			continue
		}
		for _, t := range occ {
			emit(t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	if len(out) > 5000 {
		out = out[:5000]
	}
	return out, skipped, nil
}

func parseDuration(v string) (time.Duration, bool) {
	v = strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(v)), "P")
	var d time.Duration
	num := ""
	inTime := false
	for _, c := range v {
		switch {
		case c >= '0' && c <= '9':
			num += string(c)
		case c == 'T':
			inTime = true
		default:
			n, err := strconv.Atoi(num)
			if err != nil {
				return 0, false
			}
			num = ""
			switch {
			case c == 'W':
				d += time.Duration(n) * 7 * 24 * time.Hour
			case c == 'D':
				d += time.Duration(n) * 24 * time.Hour
			case c == 'H' && inTime:
				d += time.Duration(n) * time.Hour
			case c == 'M' && inTime:
				d += time.Duration(n) * time.Minute
			case c == 'S' && inTime:
				d += time.Duration(n) * time.Second
			default:
				return 0, false
			}
		}
	}
	return d, true
}

// expandRRule handles the common rules. ok=false means the rule uses parts this reader does not support.
func expandRRule(start time.Time, rule string, to time.Time) ([]time.Time, bool) {
	parts := map[string]string{}
	for _, kv := range strings.Split(rule, ";") {
		if k, v, ok := strings.Cut(kv, "="); ok {
			parts[strings.ToUpper(k)] = strings.ToUpper(v)
		}
	}
	for k := range parts {
		switch k {
		case "FREQ", "INTERVAL", "COUNT", "UNTIL", "BYDAY", "WKST":
		default:
			return nil, false // BYMONTHDAY, BYSETPOS, BYMONTH ... not guessed
		}
	}
	freq := map[string]string{"DAILY": "daily", "WEEKLY": "weekly", "MONTHLY": "monthly", "YEARLY": "yearly"}[parts["FREQ"]]
	if freq == "" {
		return nil, false
	}
	interval := 1
	if v, err := strconv.Atoi(parts["INTERVAL"]); err == nil && v > 0 {
		interval = v
	}
	count := 0
	if v, err := strconv.Atoi(parts["COUNT"]); err == nil && v > 0 {
		count = v
	}
	last := to
	if u := parts["UNTIL"]; u != "" {
		var t time.Time
		var err error
		if len(u) == 8 {
			t, err = time.ParseInLocation("20060102", u, start.Location())
			t = t.Add(24*time.Hour - time.Second)
		} else if strings.HasSuffix(u, "Z") {
			t, err = time.Parse("20060102T150405Z", u)
		} else {
			t, err = time.ParseInLocation("20060102T150405", u, start.Location())
		}
		if err != nil {
			return nil, false
		}
		if t.Before(last) {
			last = t
		}
	}
	var days []time.Weekday
	if bd := parts["BYDAY"]; bd != "" {
		if freq != "weekly" {
			return nil, false // "2nd Tuesday" style rules are skipped
		}
		for _, d := range strings.Split(bd, ",") {
			w, ok := weekdays[d]
			if !ok {
				return nil, false
			}
			days = append(days, w)
		}
	}
	var out []time.Time
	n := 0
	if len(days) == 0 {
		for k := 0; k < 20000; k++ {
			t := nth(start, freq, interval, k)
			if t.After(last) || (count > 0 && n >= count) {
				break
			}
			out = append(out, t)
			n++
		}
		return out, true
	}
	// weekly with several days: walk week by week from the start's week
	weekStart := start.AddDate(0, 0, -int(start.Weekday()))
	for w := 0; w < 3000; w++ {
		base := weekStart.AddDate(0, 0, 7*w*interval)
		var week []time.Time
		for _, d := range days {
			t := time.Date(base.Year(), base.Month(), base.Day()+int(d), start.Hour(), start.Minute(), start.Second(), 0, start.Location())
			if !t.Before(start) {
				week = append(week, t)
			}
		}
		sort.Slice(week, func(i, j int) bool { return week[i].Before(week[j]) })
		for _, t := range week {
			if t.After(last) || (count > 0 && n >= count) {
				return out, true
			}
			out = append(out, t)
			n++
		}
		if base.After(last) {
			break
		}
	}
	return out, true
}
