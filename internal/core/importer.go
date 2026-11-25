package core

import (
	"bytes"
	"crypto/sha1"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Mapping tells the CSV reader which column holds what (0-based column numbers, -1 = not in this file).
type Mapping struct {
	Date        int    `json:"date"`
	Amount      int    `json:"amount"` // signed amount (negative = money out)
	Debit       int    `json:"debit"`  // money out (positive numbers), when the bank splits columns
	Credit      int    `json:"credit"` // money in
	Payee       int    `json:"payee"`
	Description int    `json:"description"`
	ID          int    `json:"id"`     // transaction id / reference
	Status      int    `json:"status"` // PayPal: only "Completed" rows are imported
	Type        int    `json:"type"`   // PayPal: transfers and holds are recognised from this
	Fee         int    `json:"fee"`
	Invert      bool   `json:"invert"` // file shows spending as positive numbers
	DateFormat  string `json:"date_format"`
	Profile     string `json:"profile"` // "paypal" or "generic"
}

// ImportRow is one parsed line, before it is saved.
type ImportRow struct {
	Line        int    `json:"line"`
	Date        string `json:"date"`
	AmountCents int64  `json:"amount_cents"`
	Payee       string `json:"payee"`
	Notes       string `json:"notes"`
	ExternalID  string `json:"external_id"`
	Transfer    bool   `json:"transfer"`
	Skip        string `json:"skip,omitempty"`   // why the row is left out
	Status      string `json:"status,omitempty"` // new, duplicate, match (filled by preview)
}

// Parsed is a whole statement file.
type Parsed struct {
	Format  string      `json:"format"` // csv, ofx
	Headers []string    `json:"headers"`
	Mapping Mapping     `json:"mapping"`
	Rows    []ImportRow `json:"rows"`
}

var dateFormats = []string{"2006-01-02", "01/02/2006", "1/2/2006", "01/02/06", "1/2/06", "2006/01/02", "01-02-2006", "Jan 2, 2006", "January 2, 2006", "02 Jan 2006", "2 Jan 2006", "Jan 02, 2006", "20060102"}

func parseDate(s, layout string) (string, error) {
	s = strings.TrimSpace(s)
	if len(s) > 10 && (s[4] == '-' || s[4] == '/') { // 2026-11-25T10:00 or with time
		s = s[:10]
	}
	if layout != "" {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format(DateFmt), nil
		}
	}
	for _, f := range dateFormats {
		if t, err := time.Parse(f, s); err == nil {
			return t.Format(DateFmt), nil
		}
	}
	return "", fmt.Errorf("unknown date %q", s)
}

// ParseStatement reads a CSV, OFX or QFX file. m (optional) overrides the column guess.
func ParseStatement(data []byte, m *Mapping) (*Parsed, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if !utf8.Valid(data) { // Windows-1252 exports: keep ASCII, drop the rest
		data = bytes.Map(func(r rune) rune {
			if r == utf8.RuneError {
				return -1
			}
			return r
		}, data)
	}
	head := strings.ToUpper(string(data[:min(len(data), 2000)]))
	if strings.Contains(head, "<OFX") || strings.Contains(head, "OFXHEADER") {
		return parseOFX(string(data))
	}
	return parseCSV(data, m)
}

// ---------- OFX / QFX ----------

func ofxTag(block, tag string) string {
	re := regexp.MustCompile(`(?is)<` + tag + `>([^<\r\n]*)`)
	if m := re.FindStringSubmatch(block); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func parseOFX(s string) (*Parsed, error) {
	p := &Parsed{Format: "ofx"}
	// split by <STMTTRN> manually so blocks without closing tags (OFX 1.x) still work
	parts := strings.Split(s, "<STMTTRN>")
	if len(parts) < 2 {
		parts = regexp.MustCompile(`(?i)<stmttrn>`).Split(s, -1)
	}
	for i, blk := range parts[1:] {
		if j := strings.Index(strings.ToUpper(blk), "</STMTTRN>"); j >= 0 {
			blk = blk[:j]
		}
		r := ImportRow{Line: i + 1}
		d := ofxTag(blk, "DTPOSTED")
		if len(d) >= 8 {
			if t, err := time.Parse("20060102", d[:8]); err == nil {
				r.Date = t.Format(DateFmt)
			}
		}
		amt, err := ParseCents(ofxTag(blk, "TRNAMT"))
		if err != nil || r.Date == "" {
			r.Skip = "unreadable date or amount"
		}
		r.AmountCents = amt
		r.ExternalID = ofxTag(blk, "FITID")
		r.Payee = ofxTag(blk, "NAME")
		r.Notes = ofxTag(blk, "MEMO")
		if r.Payee == "" {
			r.Payee = r.Notes
		}
		if t := strings.ToUpper(ofxTag(blk, "TRNTYPE")); t == "XFER" {
			r.Transfer = true
		}
		p.Rows = append(p.Rows, r)
	}
	if len(p.Rows) == 0 {
		return nil, errors.New("no transactions found in the OFX/QFX file")
	}
	return p, nil
}

// ---------- CSV ----------

func findCol(h []string, names ...string) int {
	for _, n := range names { // exact first
		for i, c := range h {
			if c == n {
				return i
			}
		}
	}
	for _, n := range names {
		for i, c := range h {
			if strings.Contains(c, n) {
				return i
			}
		}
	}
	return -1
}

// GuessMapping picks columns from the header names. PayPal's download is recognised by its columns.
func GuessMapping(headers []string) Mapping {
	h := make([]string, len(headers))
	for i, x := range headers {
		h[i] = strings.ToLower(strings.TrimSpace(x))
	}
	m := Mapping{Date: -1, Amount: -1, Debit: -1, Credit: -1, Payee: -1, Description: -1, ID: -1, Status: -1, Type: -1, Fee: -1, Profile: "generic"}
	if findCol(h, "transaction id") >= 0 && (findCol(h, "gross") >= 0 || findCol(h, "net") >= 0) {
		m.Profile = "paypal"
		m.Date = findCol(h, "date")
		m.Amount = findCol(h, "net")
		if m.Amount < 0 {
			m.Amount = findCol(h, "gross")
		}
		m.Payee = findCol(h, "name")
		m.Description = findCol(h, "item title", "subject", "note")
		m.ID = findCol(h, "transaction id")
		m.Status = findCol(h, "status")
		m.Type = findCol(h, "type")
		return m
	}
	m.Date = findCol(h, "posted date", "transaction date", "date")
	m.Amount = findCol(h, "amount", "transaction amount")
	if m.Amount < 0 {
		m.Debit = findCol(h, "debit", "withdrawal", "money out", "outflow")
		m.Credit = findCol(h, "credit", "deposit", "money in", "inflow")
	}
	m.Payee = findCol(h, "payee", "counterparty", "merchant", "name", "description")
	m.Description = findCol(h, "memo", "reference", "description", "details")
	if m.Description == m.Payee {
		m.Description = -1
	}
	m.ID = findCol(h, "transaction id", "reference number", "reference id", "fitid", "id")
	m.Status = findCol(h, "status")
	m.Type = findCol(h, "type", "category")
	return m
}

func cell(rec []string, i int) string {
	if i < 0 || i >= len(rec) {
		return ""
	}
	return strings.TrimSpace(rec[i])
}

var paypalTransferTypes = regexp.MustCompile(`(?i)transfer|bank deposit|withdraw|card deposit|add funds|funding|general credit card deposit|currency conversion`)
var paypalSkipTypes = regexp.MustCompile(`(?i)authorization|hold|currency conversion|reversal of general account hold`)

func parseCSV(data []byte, m *Mapping) (*Parsed, error) {
	first := string(data[:min(len(data), 4000)])
	delim := ','
	if strings.Count(first, ";") > strings.Count(first, ",") {
		delim = ';'
	} else if strings.Count(first, "\t") > strings.Count(first, ",") {
		delim = '\t'
	}
	r := csv.NewReader(bytes.NewReader(data))
	r.Comma = delim
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	recs, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("not a readable CSV file: %w", err)
	}
	// the header is the first row that looks like column names (some banks put account info above it)
	hi := -1
	for i, rec := range recs {
		if i > 15 {
			break
		}
		j := strings.ToLower(strings.Join(rec, "|"))
		if strings.Contains(j, "date") && (strings.Contains(j, "amount") || strings.Contains(j, "debit") || strings.Contains(j, "gross") || strings.Contains(j, "withdraw")) {
			hi = i
			break
		}
	}
	if hi < 0 {
		return nil, errors.New("could not find a header row with a date and an amount column")
	}
	p := &Parsed{Format: "csv", Headers: recs[hi]}
	if m == nil || m.Date < 0 {
		g := GuessMapping(recs[hi])
		m = &g
	}
	p.Mapping = *m
	seen := map[string]int{}
	for n, rec := range recs[hi+1:] {
		if len(rec) == 0 || strings.TrimSpace(strings.Join(rec, "")) == "" {
			continue
		}
		row := ImportRow{Line: hi + n + 2, Payee: cell(rec, m.Payee), Notes: cell(rec, m.Description), ExternalID: cell(rec, m.ID)}
		d, err := parseDate(cell(rec, m.Date), m.DateFormat)
		if err != nil {
			row.Skip = err.Error()
		}
		row.Date = d
		var amt int64
		if m.Amount >= 0 {
			amt, err = ParseCents(cell(rec, m.Amount))
		} else {
			var dr, cr int64
			dr, err = ParseCents(cell(rec, m.Debit))
			if err == nil {
				cr, err = ParseCents(cell(rec, m.Credit))
			}
			amt = cr - abs(dr)
		}
		if err != nil && row.Skip == "" {
			row.Skip = "unreadable amount"
		}
		if m.Invert {
			amt = -amt
		}
		row.AmountCents = amt
		typ := cell(rec, m.Type)
		if m.Profile == "paypal" {
			if st := cell(rec, m.Status); st != "" && !strings.EqualFold(st, "Completed") {
				row.Skip = "status " + st
			}
			if paypalSkipTypes.MatchString(typ) {
				row.Skip = "PayPal " + typ
			} else if paypalTransferTypes.MatchString(typ) {
				row.Transfer = true
			}
			if row.Payee == "" {
				row.Payee = typ
			}
		} else if regexp.MustCompile(`(?i)transfer`).MatchString(typ) {
			row.Transfer = true
		}
		if row.Notes == row.Payee {
			row.Notes = ""
		}
		if row.AmountCents == 0 && row.Skip == "" {
			row.Skip = "zero amount"
		}
		if row.ExternalID == "" && row.Skip == "" {
			// no id in the file: a stable fingerprint, so importing the same file twice adds nothing
			k := row.Date + "|" + fmt.Sprint(row.AmountCents) + "|" + strings.ToLower(row.Payee) + "|" + strings.ToLower(row.Notes)
			seen[k]++
			h := sha1.Sum([]byte(k + "|" + fmt.Sprint(seen[k])))
			row.ExternalID = "h:" + hex.EncodeToString(h[:8])
		}
		p.Rows = append(p.Rows, row)
	}
	return p, nil
}

// ---------- saving ----------

// SaveMapping remembers the column mapping for an account.
func (s *Store) SaveMapping(accountID int64, m Mapping) {
	b, _ := json.Marshal(m)
	s.DB.Exec(`INSERT INTO import_profiles(account_id,mapping) VALUES(?,?) ON CONFLICT(account_id) DO UPDATE SET mapping=excluded.mapping`, accountID, string(b))
}

// LoadMapping returns the saved mapping for an account, or nil.
func (s *Store) LoadMapping(accountID int64) *Mapping {
	var js string
	if s.DB.QueryRow(`SELECT mapping FROM import_profiles WHERE account_id=?`, accountID).Scan(&js) != nil {
		return nil
	}
	var m Mapping
	if json.Unmarshal([]byte(js), &m) != nil {
		return nil
	}
	return &m
}

// findDuplicate looks for the same money movement already recorded: same external id, or (from another
// source) same account, same amount, date within 3 days and no id of its own yet.
func (s *Store) findDuplicate(accountID int64, t *Transaction) (id int64, exact bool) {
	if t.ExternalID != "" {
		if s.DB.QueryRow(`SELECT id FROM transactions WHERE account_id=? AND external_id=?`, accountID, t.ExternalID).Scan(&id) == nil {
			return id, true
		}
	}
	d, err := time.Parse(DateFmt, t.Date)
	if err != nil {
		return 0, false
	}
	err = s.DB.QueryRow(`SELECT id FROM transactions WHERE account_id=? AND amount_cents=? AND date>=? AND date<=? AND source<>? AND (external_id='' OR source IN ('manual','autopay') OR ?='email')
		ORDER BY abs(julianday(date)-julianday(?)) LIMIT 1`,
		accountID, t.AmountCents, d.AddDate(0, 0, -3).Format(DateFmt), d.AddDate(0, 0, 3).Format(DateFmt), t.Source, t.Source, t.Date).Scan(&id)
	if err != nil {
		return 0, false
	}
	return id, false
}

// AddImported saves one imported or emailed transaction unless it is already there. It returns "added",
// "duplicate" (same id already saved) or "matched" (an existing entry from another source was the same payment;
// it gets this id so later imports also see it).
func (s *Store) AddImported(uid int64, t *Transaction) (string, error) {
	if t.AccountID == nil {
		return "", errors.New("pick an account")
	}
	s.mu.Lock()
	id, exact := s.findDuplicate(*t.AccountID, t)
	if id != 0 {
		if !exact && t.ExternalID != "" {
			s.DB.Exec(`UPDATE transactions SET external_id=? WHERE id=? AND external_id=''`, t.ExternalID, id)
		}
		s.mu.Unlock()
		t.ID = id
		if exact {
			return "duplicate", nil
		}
		return "matched", nil
	}
	if t.CategoryID == nil && t.Payee != "" && !t.Transfer { // learn the category from the last entry with this payee
		var c sql.NullInt64
		s.DB.QueryRow(`SELECT category_id FROM transactions WHERE lower(payee)=lower(?) AND category_id IS NOT NULL ORDER BY date DESC LIMIT 1`, t.Payee).Scan(&c)
		t.CategoryID = idPtr(c)
	}
	err := s.SaveTransaction(uid, t)
	s.mu.Unlock()
	if err != nil {
		return "", err
	}
	if t.AmountCents < 0 && !t.Transfer {
		s.MatchBill(uid, t)
	}
	return "added", nil
}

func words(s string) []string {
	var out []string
	for _, w := range regexp.MustCompile(`[a-z0-9]+`).FindAllString(strings.ToLower(s), -1) {
		if len(w) >= 3 {
			out = append(out, w)
		}
	}
	return out
}

// billMatches: the bill's name words all appear in the payee or notes ("Straight Talk" in "STRAIGHTTALK*AIRTIME").
func billMatches(billName, text string) bool {
	t := strings.ToLower(text)
	squashed := strings.NewReplacer(" ", "", "*", "", "-", "", ".", "").Replace(t)
	ws := words(billName)
	if len(ws) == 0 {
		return false
	}
	all := true
	for _, w := range ws {
		if !strings.Contains(t, w) {
			all = false
		}
	}
	return all || strings.Contains(squashed, strings.Join(ws, ""))
}

// MatchBill marks the closest unpaid due date (within 7 days) of a bill whose name matches this charge as paid
// by it. It returns the bill id or 0.
func (s *Store) MatchBill(uid int64, t *Transaction) int64 {
	d, err := ParseLocal(t.Date)
	if err != nil {
		return 0
	}
	bills, _ := s.ListBills(uid)
	type cand struct {
		b    Bill
		occ  string
		dist float64
	}
	var cs []cand
	for _, b := range bills {
		if !b.Active || !(billMatches(b.Name, t.Payee) || billMatches(b.Name, t.Notes)) {
			continue
		}
		if t.BillID != nil && *t.BillID != b.ID {
			continue
		}
		dues, _ := s.BillDues(uid, d.AddDate(0, 0, -7), d.AddDate(0, 0, 7))
		for _, du := range dues {
			if du.BillID != b.ID || du.Paid {
				continue
			}
			dd, _ := ParseLocal(du.Date)
			cs = append(cs, cand{b, du.Date, abs64f(dd.Sub(d).Hours())})
		}
	}
	if len(cs) == 0 {
		return 0
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].dist < cs[j].dist })
	c := cs[0]
	if err := s.LinkBillPayment(uid, c.b.ID, c.occ, t.Date, abs(t.AmountCents), t.ID); err != nil {
		return 0
	}
	if c.b.AmountCents != abs(t.AmountCents) && c.b.Freq != "once" { // the bill's amount changed: use the new one from now on
		s.DB.Exec(`UPDATE bills SET amount_cents=? WHERE id=?`, abs(t.AmountCents), c.b.ID)
	}
	return c.b.ID
}

func abs64f(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// PreviewImport marks each row new / duplicate / match without saving anything.
func (s *Store) PreviewImport(accountID int64, p *Parsed) map[string]int {
	counts := map[string]int{}
	for i := range p.Rows {
		r := &p.Rows[i]
		if r.Skip != "" {
			r.Status = "skip"
			counts["skip"]++
			continue
		}
		t := Transaction{Date: r.Date, AmountCents: r.AmountCents, ExternalID: r.ExternalID, Source: "import"}
		id, exact := s.findDuplicate(accountID, &t)
		switch {
		case id != 0 && exact:
			r.Status = "duplicate"
		case id != 0:
			r.Status = "match"
		default:
			r.Status = "new"
		}
		counts[r.Status]++
	}
	return counts
}

// CommitImport saves the rows of a parsed file into an account.
func (s *Store) CommitImport(uid, accountID int64, p *Parsed, private bool) (map[string]int, error) {
	counts := map[string]int{}
	for _, r := range p.Rows {
		if r.Skip != "" {
			counts["skip"]++
			continue
		}
		acc := accountID
		t := Transaction{Date: r.Date, AmountCents: r.AmountCents, AccountID: &acc, Payee: r.Payee, Notes: r.Notes, ExternalID: r.ExternalID, Source: "import", Transfer: r.Transfer, Private: private}
		st, err := s.AddImported(uid, &t)
		if err != nil {
			return counts, fmt.Errorf("line %d: %w", r.Line, err)
		}
		counts[st]++
	}
	if p.Format == "csv" {
		s.SaveMapping(accountID, p.Mapping)
	}
	return counts, nil
}
