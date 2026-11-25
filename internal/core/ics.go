package core

import (
	"fmt"
	"strings"
	"time"
)

func icsEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\r\n", `\n`, "\n", `\n`).Replace(s)
}

// fold splits long lines as RFC 5545 asks (75 octets).
func fold(line string) string {
	if len(line) <= 75 {
		return line + "\r\n"
	}
	var b strings.Builder
	for len(line) > 75 {
		cut := 75
		for cut > 0 && (line[cut]&0xC0) == 0x80 { // do not split a UTF-8 character
			cut--
		}
		b.WriteString(line[:cut] + "\r\n ")
		line = line[cut:]
	}
	b.WriteString(line + "\r\n")
	return b.String()
}

// ICS builds a read-only calendar feed (events and bill due dates, 3 months back to 18 months ahead) that phones
// and Outlook can subscribe to.
func (s *Store) ICS(u *User) (string, error) {
	now := s.Now()
	occ, err := s.Calendar(u, now.AddDate(0, -3, 0), now.AddDate(0, 18, 0), true)
	if err != nil {
		return "", err
	}
	name := s.Setting("household_name")
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Household Hub//EN\r\nCALSCALE:GREGORIAN\r\n")
	b.WriteString(fold("X-WR-CALNAME:" + icsEscape(name)))
	stamp := now.UTC().Format("20060102T150405Z")
	for _, o := range occ {
		b.WriteString("BEGIN:VEVENT\r\n")
		b.WriteString(fold("UID:" + strings.ReplaceAll(o.Key, ":", "-") + "@household-hub"))
		b.WriteString("DTSTAMP:" + stamp + "\r\n")
		title := o.Title
		if o.Source == "bill" {
			title = "Bill: " + o.Title
			if o.AmountCents != 0 {
				title += " " + s.Setting("currency") + FormatCents(o.AmountCents)
			}
			if o.Paid {
				title += " (paid)"
			}
		}
		if o.AllDay {
			st, _ := ParseLocal(o.Start)
			en := st.AddDate(0, 0, 1)
			if o.End != "" {
				if e, err := ParseLocal(o.End); err == nil {
					en = e.AddDate(0, 0, 1)
				}
			}
			b.WriteString("DTSTART;VALUE=DATE:" + st.Format("20060102") + "\r\nDTEND;VALUE=DATE:" + en.Format("20060102") + "\r\n")
		} else {
			st, _ := ParseLocal(o.Start)
			en := st.Add(time.Hour)
			if o.End != "" {
				if e, err := ParseLocal(o.End); err == nil {
					en = e
				}
			}
			b.WriteString("DTSTART:" + st.UTC().Format("20060102T150405Z") + "\r\nDTEND:" + en.UTC().Format("20060102T150405Z") + "\r\n")
		}
		b.WriteString(fold("SUMMARY:" + icsEscape(title)))
		if o.Location != "" {
			b.WriteString(fold("LOCATION:" + icsEscape(o.Location)))
		}
		b.WriteString("END:VEVENT\r\n")
	}
	b.WriteString("END:VCALENDAR\r\n")
	return b.String(), nil
}

// CSV quoting helper used by exports.
func csvCell(s string) string {
	if strings.ContainsAny(s, "\",\r\n") {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	// keep spreadsheet apps from running a cell as a formula
	if s != "" && strings.ContainsRune("=+-@", rune(s[0])) {
		if _, err := ParseCents(s); err != nil {
			return "'" + s
		}
	}
	return s
}

// TransactionsCSV exports visible transactions.
func (s *Store) TransactionsCSV(uid int64, f TxFilter) (string, error) {
	f.Limit = 5000
	txs, err := s.ListTransactions(uid, f)
	if err != nil {
		return "", err
	}
	cats, _ := s.ListCategories()
	accs, _ := s.ListAccounts(uid)
	cn, an := map[int64]string{}, map[int64]string{}
	for _, c := range cats {
		cn[c.ID] = c.Name
	}
	for _, a := range accs {
		an[a.ID] = a.Name
	}
	var b strings.Builder
	b.WriteString("date,amount,account,category,payee,notes\r\n")
	for _, t := range txs {
		acc, cat := "", ""
		if t.AccountID != nil {
			acc = an[*t.AccountID]
		}
		if t.CategoryID != nil {
			cat = cn[*t.CategoryID]
		}
		fmt.Fprintf(&b, "%s,%s,%s,%s,%s,%s\r\n", t.Date, strings.ReplaceAll(FormatCents(t.AmountCents), ",", ""), csvCell(acc), csvCell(cat), csvCell(t.Payee), csvCell(t.Notes))
	}
	return b.String(), nil
}
