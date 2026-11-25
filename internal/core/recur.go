package core

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Date and date-time layouts used everywhere (local wall-clock time, no zone stored).
const (
	DateFmt     = "2006-01-02"
	DateTimeFmt = "2006-01-02T15:04"
)

// Frequencies a bill or event can repeat with. "none" (events) or "once" (bills) means a single date.
var Freqs = map[string]bool{"none": true, "once": true, "daily": true, "weekly": true, "monthly": true, "yearly": true}

// ParseLocal reads "2006-01-02" or "2006-01-02T15:04" as local time.
func ParseLocal(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if len(s) > 16 {
		s = s[:16]
	}
	if len(s) == 10 {
		return time.ParseInLocation(DateFmt, s, time.Local)
	}
	return time.ParseInLocation(DateTimeFmt, s, time.Local)
}

func daysIn(y int, m time.Month) int { return time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC).Day() }

// nth returns the k-th occurrence (k=0 is start). Monthly and yearly keep the start's day and clamp it to the
// month's last day (a bill due on the 31st falls on Feb 28/29, then back on Mar 31). Wall-clock time is kept
// across daylight-saving changes.
func nth(start time.Time, freq string, interval, k int) time.Time {
	switch freq {
	case "daily":
		return start.AddDate(0, 0, k*interval)
	case "weekly":
		return start.AddDate(0, 0, 7*k*interval)
	case "monthly", "yearly":
		months := k * interval
		if freq == "yearly" {
			months *= 12
		}
		total := int(start.Month()) - 1 + months
		y := start.Year() + total/12
		m := time.Month(total%12 + 1)
		d := start.Day()
		if dim := daysIn(y, m); d > dim {
			d = dim
		}
		return time.Date(y, m, d, start.Hour(), start.Minute(), 0, 0, start.Location())
	}
	return start
}

// Occurrences lists the repeats of a schedule that start within [from, to]. until ("2006-01-02", optional) is the
// last allowed date, inclusive.
func Occurrences(start time.Time, freq string, interval int, until string, from, to time.Time) []time.Time {
	if interval < 1 {
		interval = 1
	}
	var last time.Time
	if until != "" {
		if u, err := ParseLocal(until); err == nil {
			last = u.AddDate(0, 0, 1) // end of that day
		}
	}
	var out []time.Time
	if freq == "" || freq == "none" || freq == "once" || !Freqs[freq] {
		if !start.Before(from) && !start.After(to) && (last.IsZero() || start.Before(last)) {
			out = append(out, start)
		}
		return out
	}
	k := 0
	// skip ahead close to `from` so old daily schedules stay cheap
	if from.After(start) {
		days := from.Sub(start).Hours() / 24
		switch freq {
		case "daily":
			k = int(math.Floor(days/float64(interval))) - 2
		case "weekly":
			k = int(math.Floor(days/float64(7*interval))) - 2
		case "monthly":
			k = int(math.Floor(days/31/float64(interval))) - 2
		case "yearly":
			k = int(math.Floor(days/366/float64(interval))) - 2
		}
		if k < 0 {
			k = 0
		}
	}
	for n := 0; n < 20000; n, k = n+1, k+1 {
		t := nth(start, freq, interval, k)
		if t.After(to) || (!last.IsZero() && !t.Before(last)) {
			break
		}
		if !t.Before(from) {
			out = append(out, t)
		}
	}
	return out
}

// ParseCents reads "12", "12.5", "-1,234.56" or "$12.34" into cents.
func ParseCents(s string) (int64, error) {
	s = strings.TrimSpace(strings.NewReplacer("$", "", ",", "", " ", "").Replace(s))
	if s == "" {
		return 0, nil
	}
	neg := false
	if strings.HasPrefix(s, "-") {
		neg, s = true, s[1:]
	} else if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		neg, s = true, s[1:len(s)-1]
	}
	whole, frac, _ := strings.Cut(s, ".")
	if whole == "" {
		whole = "0"
	}
	if len(frac) > 2 {
		return 0, errors.New("at most 2 decimals")
	}
	for len(frac) < 2 {
		frac += "0"
	}
	w, err1 := strconv.ParseInt(whole, 10, 64)
	f, err2 := strconv.ParseInt(frac, 10, 64)
	if err1 != nil || err2 != nil || w < 0 || f < 0 {
		return 0, fmt.Errorf("not an amount: %q", s)
	}
	c := w*100 + f
	if neg {
		c = -c
	}
	return c, nil
}

// FormatCents prints cents as 1,234.56 with a leading minus when negative.
func FormatCents(c int64) string {
	neg := c < 0
	if neg {
		c = -c
	}
	w := strconv.FormatInt(c/100, 10)
	var b strings.Builder
	for i, r := range w {
		if i > 0 && (len(w)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	out := fmt.Sprintf("%s.%02d", b.String(), c%100)
	if neg {
		return "-" + out
	}
	return out
}

// ParseInts reads "60, 1440" into ints, skipping junk and negatives.
func ParseInts(s string) []int {
	var out []int
	for _, p := range strings.Split(s, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(p)); err == nil && n >= 0 {
			out = append(out, n)
		}
	}
	return out
}
