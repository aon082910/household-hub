package core

import (
	"database/sql"
	"errors"
	"fmt"
	"html"
	"log"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Mailbox is an email account the reader checks (IMAP). Secret is write-only: the API never returns it.
type Mailbox struct {
	ID        int64  `json:"id"`
	Label     string `json:"label"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Username  string `json:"username"`
	Secret    string `json:"secret,omitempty"`
	HasSecret bool   `json:"has_secret"`
	AuthType  string `json:"auth_type"`
	Folder    string `json:"folder"`
	Enabled   bool   `json:"enabled"`
	Preview   bool   `json:"preview"` // only list what would happen, change nothing
	SinceDays int    `json:"since_days"`
	LastCheck string `json:"last_check"`
	LastError string `json:"last_error"`
}

// EmailRule says what an email means and where it goes.
type EmailRule struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	FromContains    string `json:"from_contains"`
	ToContains      string `json:"to_contains"`
	SubjectContains string `json:"subject_contains"`
	BodyContains    string `json:"body_contains"`
	Kind            string `json:"kind"` // paypal, transaction, bill_notice, bill_paid, ignore
	AccountID       *int64 `json:"account_id"`
	CategoryID      *int64 `json:"category_id"`
	BillID          *int64 `json:"bill_id"`
	Direction       string `json:"direction"` // auto, out, in
	Priority        int    `json:"priority"`
	Enabled         bool   `json:"enabled"`
}

// Email is one message as the parser sees it (no body is ever stored).
type Email struct {
	MessageID string
	From      string
	To        string
	Subject   string
	Date      time.Time
	Body      string // plain text (HTML already stripped)
}

// EmailItem is what the reader did with one email.
type EmailItem struct {
	ID            int64  `json:"id"`
	MailboxID     int64  `json:"mailbox_id"`
	Received      string `json:"received"`
	Sender        string `json:"sender"`
	Recipient     string `json:"recipient"`
	Subject       string `json:"subject"`
	RuleID        *int64 `json:"rule_id"`
	Kind          string `json:"kind"`
	AmountCents   int64  `json:"amount_cents"`
	Payee         string `json:"payee"`
	Ref           string `json:"ref"`
	DueDate       string `json:"due_date"`
	AccountID     *int64 `json:"account_id"`
	BillID        *int64 `json:"bill_id"`
	Status        string `json:"status"` // preview, review, added, matched, duplicate, applied, ignored
	TransactionID *int64 `json:"transaction_id"`
	Note          string `json:"note"`
}

var EmailKinds = map[string]bool{"paypal": true, "transaction": true, "bill_notice": true, "bill_paid": true, "ignore": true}

// ---------- mailboxes ----------

const mbCols = `id,label,host,port,username,secret<>'',auth_type,folder,enabled,preview,since_days,last_check,last_error`

func scanMailbox(r interface{ Scan(...any) error }) (Mailbox, error) {
	var m Mailbox
	var hs, en, pv int
	err := r.Scan(&m.ID, &m.Label, &m.Host, &m.Port, &m.Username, &hs, &m.AuthType, &m.Folder, &en, &pv, &m.SinceDays, &m.LastCheck, &m.LastError)
	m.HasSecret, m.Enabled, m.Preview = hs == 1, en == 1, pv == 1
	return m, err
}

// ListMailboxes returns mailboxes without secrets.
func (s *Store) ListMailboxes() ([]Mailbox, error) {
	rows, err := s.DB.Query(`SELECT ` + mbCols + ` FROM mailboxes ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Mailbox{}
	for rows.Next() {
		m, err := scanMailbox(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

func (s *Store) mailboxSecret(id int64) string {
	var sec string
	s.DB.QueryRow(`SELECT secret FROM mailboxes WHERE id=?`, id).Scan(&sec)
	return sec
}

// SaveMailbox adds or updates a mailbox. An empty or masked Secret keeps the stored one.
func (s *Store) SaveMailbox(m *Mailbox) error {
	m.Label, m.Host, m.Username = strings.TrimSpace(m.Label), strings.TrimSpace(m.Host), strings.TrimSpace(m.Username)
	if m.Host == "" || m.Username == "" {
		return errors.New("server and username are required")
	}
	if m.Label == "" {
		m.Label = m.Username
	}
	if m.Port == 0 {
		m.Port = 993
	}
	if m.Folder == "" {
		m.Folder = "INBOX"
	}
	if m.AuthType == "" {
		m.AuthType = "password"
	}
	if m.SinceDays <= 0 || m.SinceDays > 400 {
		m.SinceDays = 60
	}
	m.Secret = strings.ReplaceAll(m.Secret, " ", "") // Gmail shows app passwords in groups of 4
	if m.ID == 0 {
		if m.Secret == "" || m.Secret == Mask {
			return errors.New("the app password is required")
		}
		res, err := s.DB.Exec(`INSERT INTO mailboxes(label,host,port,username,secret,auth_type,folder,enabled,preview,since_days) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			m.Label, m.Host, m.Port, m.Username, m.Secret, m.AuthType, m.Folder, b2i(m.Enabled), b2i(m.Preview), m.SinceDays)
		if err == nil {
			m.ID, _ = res.LastInsertId()
		}
		m.Secret = ""
		return err
	}
	var old Mailbox
	s.DB.QueryRow(`SELECT host,username,folder FROM mailboxes WHERE id=?`, m.ID).Scan(&old.Host, &old.Username, &old.Folder)
	reset := ""
	if old.Host != m.Host || !strings.EqualFold(old.Username, m.Username) || old.Folder != m.Folder {
		reset = ", uidvalidity=0, last_uid=0" // another mailbox: start over
	}
	q := `UPDATE mailboxes SET label=?,host=?,port=?,username=?,auth_type=?,folder=?,enabled=?,preview=?,since_days=?` + reset
	args := []any{m.Label, m.Host, m.Port, m.Username, m.AuthType, m.Folder, b2i(m.Enabled), b2i(m.Preview), m.SinceDays}
	if m.Secret != "" && m.Secret != Mask {
		q += `,secret=?`
		args = append(args, m.Secret)
	}
	m.Secret = ""
	return mustChange(s.DB.Exec(q+` WHERE id=?`, append(args, m.ID)...))
}

// DeleteMailbox removes a mailbox (what it already added stays).
func (s *Store) DeleteMailbox(id int64) error {
	return mustChange(s.DB.Exec(`DELETE FROM mailboxes WHERE id=?`, id))
}

// ---------- rules ----------

const ruleCols = `id,name,from_contains,to_contains,subject_contains,body_contains,kind,account_id,category_id,bill_id,direction,priority,enabled`

// ListRules returns rules in the order they are tried.
func (s *Store) ListRules() ([]EmailRule, error) {
	rows, err := s.DB.Query(`SELECT ` + ruleCols + ` FROM email_rules ORDER BY priority, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EmailRule{}
	for rows.Next() {
		var r EmailRule
		var a, c, b sql.NullInt64
		var en int
		if err := rows.Scan(&r.ID, &r.Name, &r.FromContains, &r.ToContains, &r.SubjectContains, &r.BodyContains, &r.Kind, &a, &c, &b, &r.Direction, &r.Priority, &en); err != nil {
			return nil, err
		}
		r.AccountID, r.CategoryID, r.BillID, r.Enabled = idPtr(a), idPtr(c), idPtr(b), en == 1
		out = append(out, r)
	}
	return out, nil
}

// SaveRule adds or updates a rule.
func (s *Store) SaveRule(r *EmailRule) error {
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" {
		return errors.New("name the rule")
	}
	if !EmailKinds[r.Kind] {
		return errors.New("bad rule type")
	}
	if r.FromContains == "" && r.ToContains == "" && r.SubjectContains == "" && r.BodyContains == "" {
		return errors.New("fill in at least one 'contains' box")
	}
	if r.Direction == "" {
		r.Direction = "auto"
	}
	if r.Priority == 0 {
		r.Priority = 100
	}
	if r.ID == 0 {
		res, err := s.DB.Exec(`INSERT INTO email_rules(name,from_contains,to_contains,subject_contains,body_contains,kind,account_id,category_id,bill_id,direction,priority,enabled) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
			r.Name, r.FromContains, r.ToContains, r.SubjectContains, r.BodyContains, r.Kind, nullID(r.AccountID), nullID(r.CategoryID), nullID(r.BillID), r.Direction, r.Priority, b2i(r.Enabled))
		if err == nil {
			r.ID, _ = res.LastInsertId()
		}
		return err
	}
	return mustChange(s.DB.Exec(`UPDATE email_rules SET name=?,from_contains=?,to_contains=?,subject_contains=?,body_contains=?,kind=?,account_id=?,category_id=?,bill_id=?,direction=?,priority=?,enabled=? WHERE id=?`,
		r.Name, r.FromContains, r.ToContains, r.SubjectContains, r.BodyContains, r.Kind, nullID(r.AccountID), nullID(r.CategoryID), nullID(r.BillID), r.Direction, r.Priority, b2i(r.Enabled), r.ID))
}

// DeleteRule removes a rule.
func (s *Store) DeleteRule(id int64) error {
	return mustChange(s.DB.Exec(`DELETE FROM email_rules WHERE id=?`, id))
}

func containsAny(hay, needles string) bool {
	if strings.TrimSpace(needles) == "" {
		return true
	}
	h := strings.ToLower(hay)
	for _, n := range strings.Split(needles, "|") { // "a|b" = a or b
		if n = strings.ToLower(strings.TrimSpace(n)); n != "" && strings.Contains(h, n) {
			return true
		}
	}
	return false
}

func (r *EmailRule) matches(e *Email) bool {
	return r.Enabled && containsAny(e.From, r.FromContains) && containsAny(e.To, r.ToContains) &&
		containsAny(e.Subject, r.SubjectContains) && containsAny(e.Body, r.BodyContains)
}

// ---------- parsing ----------

var (
	reTags      = regexp.MustCompile(`(?is)<(script|style|head)[^>]*>.*?</(script|style|head)>`)
	reBreaks    = regexp.MustCompile(`(?i)<(br|/p|/div|/tr|/h[1-6]|/li|/table)[^>]*>`)
	reCells     = regexp.MustCompile(`(?i)</t[dh]>`)
	reAnyTag    = regexp.MustCompile(`(?s)<[^>]+>`)
	reSpaces    = regexp.MustCompile(`[ \t\x{00a0}]+`)
	reBlank     = regexp.MustCompile(`\n\s*\n+`)
	reMoney     = regexp.MustCompile(`(-)?\$\s?([0-9][0-9,]*\.[0-9]{2})`)
	reDueAmount = regexp.MustCompile(`(?i)(?:amount due|minimum (?:payment )?due|payment due|balance due|total due|statement balance|amount of|due)[^$\n]{0,40}\$\s?([0-9][0-9,]*\.[0-9]{2})`)
	reDueDate   = regexp.MustCompile(`(?i)(?:due|payment date|draft date|will be (?:charged|drafted|processed))(?: (?:on|by|date))?:?\s*((?:mon|tue|wed|thu|fri|sat|sun)[a-z]*,?\s+)?([A-Z][a-z]{2,8}\.? \d{1,2},? \d{4}|\d{1,2}/\d{1,2}/\d{2,4})`)
	reTxnID     = regexp.MustCompile(`(?i)transaction (?:id|number|#)[:\s#]*([A-Z0-9]{10,25})`)
	rePPOut     = []*regexp.Regexp{
		regexp.MustCompile(`(?i)receipt for your payment to (.+)`),
		regexp.MustCompile(`(?i)you (?:sent|paid) \$?[0-9,.]+(?: USD)? to (.+)`),
		regexp.MustCompile(`(?i)you(?:'ve| have)? (?:made|sent|authori[sz]ed) a payment (?:of \$?[0-9,.]+(?: USD)? )?to (.+)`),
		regexp.MustCompile(`(?i)your (?:automatic )?payment to (.+?)(?: (?:was|has|is) .*)?$`),
		regexp.MustCompile(`(?i)you(?:'ve| have)? (?:paid|sent) (.+)`),
	}
	rePPIn = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(?:you(?:'ve| have)? )?(?:received|got) (?:a payment|money|\$?[0-9,.]+(?: USD)?) from (.+)`),
		regexp.MustCompile(`(?i)(.+?) sent you \$?[0-9,.]+`),
		regexp.MustCompile(`(?i)refund from (.+)`),
	}
	reInWords   = regexp.MustCompile(`(?i)\b(received|got money|refund|sent you|deposit(ed)?|credited|incoming)\b`)
	rePaidWords = regexp.MustCompile(`(?i)(thank you for your payment|payment (?:received|confirmation|processed|successful|posted)|we received your payment|receipt|you paid|auto ?pay(?:ment)? (?:processed|complete)|has been paid|refill (?:was )?successful|successfully refilled)`)
	reDueWords  = regexp.MustCompile(`(?i)(bill is (?:ready|available)|amount due|payment (?:is )?due|due date|statement is ready|payment reminder|upcoming payment|will be (?:charged|drafted|processed)|plan (?:expires|ends)|service (?:expires|ends))`)
	reAtPayee   = regexp.MustCompile(`(?i)\b(?:at|to|from|with)\s+([A-Za-z0-9][A-Za-z0-9 &'.*#-]{1,40}?)(?:\s+(?:on|for|was|has|in|using|ending)\b|[.,\n]|$)`)
)

// HTMLToText turns an HTML email into readable lines.
func HTMLToText(h string) string {
	h = reTags.ReplaceAllString(h, " ")
	h = reBreaks.ReplaceAllString(h, "\n")
	h = reCells.ReplaceAllString(h, " ")
	h = reAnyTag.ReplaceAllString(h, " ")
	h = html.UnescapeString(h)
	h = reSpaces.ReplaceAllString(h, " ")
	lines := strings.Split(h, "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	return strings.TrimSpace(reBlank.ReplaceAllString(strings.Join(lines, "\n"), "\n"))
}

func firstMoney(s string) (int64, bool) {
	m := reMoney.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	c, err := ParseCents(m[2])
	return c, err == nil && c > 0
}

func cleanPayee(p string) string {
	p = strings.TrimSpace(strings.Trim(p, " .:!-\"'"))
	p = regexp.MustCompile(`(?i)\s*\$?[0-9,]+\.[0-9]{2}(?: USD)?.*$`).ReplaceAllString(p, "")
	p = regexp.MustCompile(`(?i)\s+(?:on|for)\s+(?:your|the|a)\b.*$`).ReplaceAllString(p, "")
	if len(p) > 60 {
		p = p[:60]
	}
	p = strings.TrimSpace(p)
	if regexp.MustCompile(`(?i)^(?:a |an |your )?(?:payment|money|purchase|order)s?$`).MatchString(p) {
		return "" // "You sent a payment": the name is in the body
	}
	return p
}

// EmailParse is what the parser read out of a message.
type EmailParse struct {
	Kind        string `json:"kind"`
	AmountCents int64  `json:"amount_cents"` // signed: negative = money out
	Payee       string `json:"payee"`
	Ref         string `json:"ref"`
	DueDate     string `json:"due_date"`
	BillID      *int64 `json:"bill_id"`
	BillName    string `json:"bill_name"`
	AccountID   *int64 `json:"account_id"`
	CategoryID  *int64 `json:"category_id"`
	RuleID      *int64 `json:"rule_id"`
	RuleName    string `json:"rule_name"`
	Confident   bool   `json:"confident"`
	Note        string `json:"note"`
}

func parseDueDate(s string) string {
	m := reDueDate.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	d := strings.ReplaceAll(strings.TrimSpace(m[2]), ".", "")
	for _, f := range []string{"January 2, 2006", "January 2 2006", "Jan 2, 2006", "Jan 2 2006", "1/2/2006", "01/02/2006", "1/2/06", "01/02/06"} {
		if t, err := time.Parse(f, d); err == nil {
			return t.Format(DateFmt)
		}
	}
	return ""
}

// ParseEmail decides what a message is. bills is used to recognise GEICO, Straight Talk and the like by name.
func ParseEmail(e *Email, rules []EmailRule, bills []Bill) *EmailParse {
	p := &EmailParse{}
	var rule *EmailRule
	for i := range rules {
		if rules[i].matches(e) {
			rule = &rules[i]
			break
		}
	}
	all := e.Subject + "\n" + e.Body
	if rule != nil {
		p.Kind, p.AccountID, p.CategoryID, p.BillID, p.RuleID, p.RuleName = rule.Kind, rule.AccountID, rule.CategoryID, rule.BillID, &rule.ID, rule.Name
	} else if containsAny(e.From, "paypal.com") {
		p.Kind = "paypal"
	}
	// a bill named in the sender or subject ("GEICO", "Straight Talk")
	if p.BillID == nil && p.Kind != "paypal" && p.Kind != "ignore" {
		for _, b := range bills {
			if b.Active && (billMatches(b.Name, e.From) || billMatches(b.Name, e.Subject)) {
				id := b.ID
				p.BillID, p.BillName = &id, b.Name
				break
			}
		}
	}
	if p.BillID != nil && p.BillName == "" {
		for _, b := range bills {
			if b.ID == *p.BillID {
				p.BillName = b.Name
			}
		}
	}
	if p.Kind == "" && p.BillID != nil {
		switch {
		case rePaidWords.MatchString(e.Subject):
			p.Kind = "bill_paid"
		case reDueWords.MatchString(all):
			p.Kind = "bill_notice"
		default:
			p.Kind = "bill_notice"
			p.Note = "not sure whether this is a bill or a receipt"
		}
	}
	if p.Kind == "" {
		return nil // not a money email
	}
	if p.Kind == "ignore" {
		return p
	}
	if m := reTxnID.FindStringSubmatch(all); m != nil {
		p.Ref = m[1]
	}
	dir := "out"
	if rule != nil && rule.Direction != "auto" {
		dir = rule.Direction
	} else if reInWords.MatchString(e.Subject) {
		dir = "in"
	}
	switch p.Kind {
	case "paypal":
		amt, ok := firstMoney(e.Subject)
		if !ok {
			amt, ok = firstMoney(e.Body)
		}
		pats := rePPOut
		if dir == "in" {
			pats = rePPIn
		}
		for _, re := range pats {
			if m := re.FindStringSubmatch(e.Subject); m != nil {
				p.Payee = cleanPayee(m[1])
				break
			}
		}
		if p.Payee == "" {
			for _, re := range pats {
				if m := re.FindStringSubmatch(e.Body); m != nil {
					p.Payee = cleanPayee(strings.SplitN(m[1], "\n", 2)[0])
					break
				}
			}
		}
		p.AmountCents = amt
		p.Confident = ok && p.Payee != "" && p.Ref != ""
		if !p.Confident {
			p.Note = strings.TrimSpace(p.Note + " PayPal email without a clear amount, payee or transaction ID")
		}
	case "transaction":
		amt, ok := firstMoney(e.Subject)
		if !ok {
			amt, ok = firstMoney(e.Body)
		}
		if m := reAtPayee.FindStringSubmatch(e.Subject); m != nil {
			p.Payee = cleanPayee(m[1])
		} else if m := reAtPayee.FindStringSubmatch(e.Body); m != nil {
			p.Payee = cleanPayee(m[1])
		}
		if p.Payee == "" && rule != nil {
			p.Payee = rule.Name
		}
		p.AmountCents = amt
		p.Confident = ok
	case "bill_notice":
		if m := reDueAmount.FindStringSubmatch(all); m != nil {
			p.AmountCents, _ = ParseCents(m[1])
		} else {
			p.AmountCents, _ = firstMoney(all)
		}
		p.DueDate = parseDueDate(all)
		p.Payee = p.BillName
		p.Confident = p.AmountCents > 0 && p.BillID != nil && p.Note == ""
	case "bill_paid":
		p.AmountCents, _ = firstMoney(all)
		p.Payee = p.BillName
		p.Confident = p.BillID != nil
	}
	if dir == "out" && (p.Kind == "paypal" || p.Kind == "transaction") {
		p.AmountCents = -p.AmountCents
	}
	return p
}

// ---------- processing ----------

func (s *Store) firstAdmin() int64 {
	var id int64
	s.DB.QueryRow(`SELECT id FROM users WHERE role='admin' AND active=1 ORDER BY id LIMIT 1`).Scan(&id)
	return id
}

// ProcessEmail records what one email means and, unless preview is on or the parse is unsure, acts on it.
// It returns the stored item, or nil when the email is not about money or was seen before.
func (s *Store) ProcessEmail(mailboxID int64, preview bool, e *Email, send Sender) (*EmailItem, error) {
	if e.MessageID == "" {
		e.MessageID = fmt.Sprintf("%s|%s|%s", e.From, e.Subject, e.Date.UTC().Format(time.RFC3339))
	}
	var n int
	s.DB.QueryRow(`SELECT COUNT(*) FROM email_items WHERE message_id=?`, e.MessageID).Scan(&n)
	if n > 0 {
		return nil, nil
	}
	rules, err := s.ListRules()
	if err != nil {
		return nil, err
	}
	bills, _ := s.ListBills(0)
	p := ParseEmail(e, rules, bills)
	if p == nil {
		return nil, nil
	}
	it := &EmailItem{MailboxID: mailboxID, Received: e.Date.Format(time.RFC3339), Sender: trunc(e.From, 200), Recipient: trunc(e.To, 200), Subject: trunc(e.Subject, 300),
		RuleID: p.RuleID, Kind: p.Kind, AmountCents: p.AmountCents, Payee: p.Payee, Ref: p.Ref, DueDate: p.DueDate, AccountID: p.AccountID, BillID: p.BillID, Note: p.Note}
	uid := s.firstAdmin()
	switch {
	case p.Kind == "ignore":
		it.Status = "ignored"
	case preview:
		it.Status = "preview"
	case !p.Confident:
		it.Status = "review"
	default:
		s.applyItem(uid, it, p.CategoryID, send)
	}
	res, err := s.DB.Exec(`INSERT OR IGNORE INTO email_items(mailbox_id,message_id,received,sender,recipient,subject,rule_id,kind,amount_cents,payee,ref,due_date,account_id,bill_id,status,transaction_id,note) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		mailboxID, e.MessageID, it.Received, it.Sender, it.Recipient, it.Subject, nullID(it.RuleID), it.Kind, it.AmountCents, it.Payee, it.Ref, it.DueDate, nullID(it.AccountID), nullID(it.BillID), it.Status, nullID(it.TransactionID), it.Note)
	if err != nil {
		return nil, err
	}
	it.ID, _ = res.LastInsertId()
	return it, nil
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// applyItem acts on a confident (or user-approved) item and sets its Status.
func (s *Store) applyItem(uid int64, it *EmailItem, categoryID *int64, send Sender) {
	date := it.Received
	if t, err := time.Parse(time.RFC3339, it.Received); err == nil {
		date = t.In(time.Local).Format(DateFmt)
	}
	switch it.Kind {
	case "paypal", "transaction":
		if it.AccountID == nil {
			it.Status, it.Note = "review", "which account? add a rule with the account (for PayPal: 'to contains' your PayPal email)"
			return
		}
		t := Transaction{Date: date, AmountCents: it.AmountCents, AccountID: it.AccountID, CategoryID: categoryID, Payee: it.Payee, ExternalID: it.Ref, Source: "email", BillID: it.BillID}
		if it.AmountCents == 0 {
			it.Status, it.Note = "review", "no amount found"
			return
		}
		st, err := s.AddImported(uid, &t)
		if err != nil {
			it.Status, it.Note = "review", err.Error()
			return
		}
		it.Status, it.TransactionID = st, &t.ID
	case "bill_notice":
		b, err := s.GetBill(uid, *it.BillID)
		if err != nil {
			it.Status = "review"
			return
		}
		if it.AmountCents > 0 && it.AmountCents != b.AmountCents {
			s.DB.Exec(`UPDATE bills SET amount_cents=? WHERE id=?`, it.AmountCents, b.ID)
			it.Note = strings.TrimSpace(fmt.Sprintf("amount changed from %s to %s", FormatCents(b.AmountCents), FormatCents(it.AmountCents)))
		}
		it.Status = "applied"
		if send != nil {
			users, _ := s.ListUsers()
			byID, active := map[int64]*User{}, []*User{}
			for _, u := range users {
				byID[u.ID] = u
				if u.Active {
					active = append(active, u)
				}
			}
			msg := Message{Title: fmt.Sprintf("%s bill: %s%s", b.Name, s.Setting("currency"), FormatCents(it.AmountCents))}
			if it.DueDate != "" {
				if d, err := ParseLocal(it.DueDate); err == nil {
					msg.Title += " due " + d.Format("Mon Jan 2")
				}
			}
			msg.Body = "From your email. " + it.Note
			for _, u := range billRecipients(b, byID, active) {
				s.notify(send, u, msg)
			}
		}
	case "bill_paid":
		b, err := s.GetBill(uid, *it.BillID)
		if err != nil {
			it.Status = "review"
			return
		}
		d, _ := ParseLocal(date)
		dues, _ := s.BillDues(uid, d.AddDate(0, 0, -10), d.AddDate(0, 0, 10))
		best, bestDist := "", 1e18
		for _, du := range dues {
			if du.BillID != b.ID || du.Paid {
				continue
			}
			dd, _ := ParseLocal(du.Date)
			if x := abs64f(dd.Sub(d).Hours()); x < bestDist {
				best, bestDist = du.Date, x
			}
		}
		if best == "" {
			it.Status, it.Note = "applied", "no unpaid due date near this payment"
			return
		}
		amt := it.AmountCents
		if amt == 0 {
			amt = b.AmountCents
		}
		// record=false: the money itself shows up from the bank or PayPal side, so it is not counted twice
		if err := s.payBill(uid, b.ID, best, date, amt, false, "", 0); err != nil {
			it.Status, it.Note = "review", err.Error()
			return
		}
		it.Status, it.Note = "applied", "marked the "+best+" due date paid"
	}
}

// ListEmailItems lists processed emails, newest first. status "" = all, "open" = review + preview.
func (s *Store) ListEmailItems(status string, limit int) ([]EmailItem, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	q := `SELECT id,COALESCE(mailbox_id,0),received,sender,recipient,subject,rule_id,kind,amount_cents,payee,ref,due_date,account_id,bill_id,status,transaction_id,note FROM email_items`
	args := []any{}
	switch status {
	case "":
	case "open":
		q += ` WHERE status IN ('review','preview')`
	default:
		q += ` WHERE status=?`
		args = append(args, status)
	}
	rows, err := s.DB.Query(q+` ORDER BY received DESC, id DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EmailItem{}
	for rows.Next() {
		var it EmailItem
		var r, a, b, t sql.NullInt64
		if err := rows.Scan(&it.ID, &it.MailboxID, &it.Received, &it.Sender, &it.Recipient, &it.Subject, &r, &it.Kind, &it.AmountCents, &it.Payee, &it.Ref, &it.DueDate, &a, &b, &it.Status, &t, &it.Note); err != nil {
			return nil, err
		}
		it.RuleID, it.AccountID, it.BillID, it.TransactionID = idPtr(r), idPtr(a), idPtr(b), idPtr(t)
		out = append(out, it)
	}
	return out, nil
}

// ResolveEmailItem applies (with the user's corrections) or ignores a review/preview item.
func (s *Store) ResolveEmailItem(uid, id int64, action string, fix EmailItem, categoryID *int64, send Sender) (*EmailItem, error) {
	items, err := s.ListEmailItems("", 1000)
	if err != nil {
		return nil, err
	}
	var it *EmailItem
	for i := range items {
		if items[i].ID == id {
			it = &items[i]
		}
	}
	if it == nil {
		return nil, ErrNotFound
	}
	if it.Status != "review" && it.Status != "preview" {
		return nil, errors.New("already handled")
	}
	if action == "ignore" {
		_, err := s.DB.Exec(`UPDATE email_items SET status='ignored' WHERE id=?`, id)
		it.Status = "ignored"
		return it, err
	}
	if fix.Kind != "" {
		if !EmailKinds[fix.Kind] {
			return nil, errors.New("bad type")
		}
		it.Kind = fix.Kind
	}
	if fix.AmountCents != 0 {
		it.AmountCents = fix.AmountCents
	}
	if strings.TrimSpace(fix.Payee) != "" {
		it.Payee = strings.TrimSpace(fix.Payee)
	}
	if fix.AccountID != nil {
		it.AccountID = fix.AccountID
	}
	if fix.BillID != nil {
		it.BillID = fix.BillID
	}
	if (it.Kind == "bill_notice" || it.Kind == "bill_paid") && it.BillID == nil {
		return nil, errors.New("pick the bill")
	}
	it.Note = ""
	s.applyItem(uid, it, categoryID, send)
	if it.Status == "review" {
		return it, errors.New(it.Note)
	}
	_, err = s.DB.Exec(`UPDATE email_items SET kind=?,amount_cents=?,payee=?,account_id=?,bill_id=?,status=?,transaction_id=?,note=? WHERE id=?`,
		it.Kind, it.AmountCents, it.Payee, nullID(it.AccountID), nullID(it.BillID), it.Status, nullID(it.TransactionID), it.Note, id)
	return it, err
}

// MailFetcher reads new messages from a mailbox (IMAP in production, a fake in tests).
type MailFetcher func(m Mailbox, secret string, uidValidity, lastUID uint32) (msgs []Email, newValidity, newLast uint32, err error)

// ScanMailbox checks one mailbox and processes its new money emails. It returns how many items it stored.
func (s *Store) ScanMailbox(id int64, fetch MailFetcher, send Sender) (int, error) {
	var m Mailbox
	var uv, lu int64
	row := s.DB.QueryRow(`SELECT `+mbCols+`,uidvalidity,last_uid FROM mailboxes WHERE id=?`, id)
	var hs, en, pv int
	if err := row.Scan(&m.ID, &m.Label, &m.Host, &m.Port, &m.Username, &hs, &m.AuthType, &m.Folder, &en, &pv, &m.SinceDays, &m.LastCheck, &m.LastError, &uv, &lu); err != nil {
		return 0, ErrNotFound
	}
	m.Enabled, m.Preview = en == 1, pv == 1
	msgs, nv, nl, err := fetch(m, s.mailboxSecret(id), uint32(uv), uint32(lu))
	now := s.nowStr()
	if err != nil {
		s.DB.Exec(`UPDATE mailboxes SET last_check=?, last_error=? WHERE id=?`, now, trunc(err.Error(), 300), id)
		return 0, err
	}
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].Date.Before(msgs[j].Date) })
	stored, review := 0, 0
	for i := range msgs {
		it, err := s.ProcessEmail(id, m.Preview, &msgs[i], send)
		if err != nil {
			log.Printf("email %q: %v", msgs[i].Subject, err)
			continue
		}
		if it != nil {
			stored++
			if it.Status == "review" {
				review++
			}
		}
	}
	s.DB.Exec(`UPDATE mailboxes SET last_check=?, last_error='', uidvalidity=?, last_uid=? WHERE id=?`, now, nv, nl, id)
	if review > 0 && send != nil {
		if u, err := s.GetUser(s.firstAdmin()); err == nil {
			s.notify(send, u, Message{Title: fmt.Sprintf("%d money email(s) need a look", review), Body: "Open Import > Email review in Household Hub."})
		}
	}
	return stored, nil
}

// ScanAll checks every enabled mailbox.
func (s *Store) ScanAll(fetch MailFetcher, send Sender) {
	mbs, err := s.ListMailboxes()
	if err != nil {
		return
	}
	for _, m := range mbs {
		if !m.Enabled {
			continue
		}
		if n, err := s.ScanMailbox(m.ID, fetch, send); err != nil {
			log.Printf("mailbox %s: %v", m.Label, err)
		} else if n > 0 {
			log.Printf("mailbox %s: %d money emails", m.Label, n)
		}
	}
}
