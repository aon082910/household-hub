package core

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ---------- category rules ----------

// CategoryRule sets the category (and business / transfer flags) of new imported or emailed transactions whose
// payee or notes contain a word. Rules run before the "same payee as last time" guess.
type CategoryRule struct {
	ID         int64  `json:"id"`
	Contains   string `json:"contains"` // "a|b" = a or b
	CategoryID *int64 `json:"category_id"`
	Business   int    `json:"business"` // -1 leave, 0 personal, 1 business
	Transfer   int    `json:"transfer"` // -1 leave, 0 no, 1 transfer
	Priority   int    `json:"priority"`
}

// ListCategoryRules returns rules in the order they are tried.
func (s *Store) ListCategoryRules() ([]CategoryRule, error) {
	rows, err := s.DB.Query(`SELECT id,contains,category_id,business,transfer,priority FROM category_rules ORDER BY priority, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CategoryRule{}
	for rows.Next() {
		var r CategoryRule
		var c sql.NullInt64
		rows.Scan(&r.ID, &r.Contains, &c, &r.Business, &r.Transfer, &r.Priority)
		r.CategoryID = idPtr(c)
		out = append(out, r)
	}
	return out, nil
}

// SaveCategoryRule adds or updates a rule.
func (s *Store) SaveCategoryRule(r *CategoryRule) error {
	r.Contains = strings.TrimSpace(r.Contains)
	if r.Contains == "" {
		return errors.New("type the words to look for")
	}
	if r.CategoryID == nil && r.Business < 0 && r.Transfer < 0 {
		return errors.New("the rule changes nothing")
	}
	if r.Priority == 0 {
		r.Priority = 100
	}
	if r.ID == 0 {
		res, err := s.DB.Exec(`INSERT INTO category_rules(contains,category_id,business,transfer,priority) VALUES(?,?,?,?,?)`, r.Contains, nullID(r.CategoryID), r.Business, r.Transfer, r.Priority)
		if err == nil {
			r.ID, _ = res.LastInsertId()
		}
		return err
	}
	return mustChange(s.DB.Exec(`UPDATE category_rules SET contains=?,category_id=?,business=?,transfer=?,priority=? WHERE id=?`, r.Contains, nullID(r.CategoryID), r.Business, r.Transfer, r.Priority, r.ID))
}

// DeleteCategoryRule removes a rule.
func (s *Store) DeleteCategoryRule(id int64) error {
	return mustChange(s.DB.Exec(`DELETE FROM category_rules WHERE id=?`, id))
}

// applyRules fills the category and flags of a new transaction from the first matching rule. It returns true
// when a rule set the category.
func (s *Store) applyRules(t *Transaction) bool {
	rules, _ := s.ListCategoryRules()
	text := t.Payee + " " + t.Notes
	for _, r := range rules {
		if !containsAny(text, r.Contains) {
			continue
		}
		if r.Business >= 0 {
			t.Business = r.Business == 1
		}
		if r.Transfer >= 0 {
			t.Transfer = r.Transfer == 1
		}
		if r.CategoryID != nil && t.CategoryID == nil {
			t.CategoryID = r.CategoryID
			return true
		}
		return false
	}
	return false
}

// ApplyRulesToExisting runs the rules over visible transactions that have no category yet. It returns how many
// changed.
func (s *Store) ApplyRulesToExisting(uid int64) int {
	txs, _ := s.ListTransactions(uid, TxFilter{Limit: 5000})
	n := 0
	for _, t := range txs {
		if t.CategoryID != nil || t.Split {
			continue
		}
		before := t
		if s.applyRules(&t) || before.Business != t.Business || before.Transfer != t.Transfer {
			if s.SaveTransaction(uid, &t) == nil {
				n++
			}
		}
	}
	return n
}

// ---------- splits ----------

// SplitPart is one piece of a split transaction.
type SplitPart struct {
	AmountCents int64  `json:"amount_cents"`
	CategoryID  *int64 `json:"category_id"`
	Notes       string `json:"notes"`
	Business    bool   `json:"business"`
}

// GetSplits returns the parts of a transaction (empty when it is not split).
func (s *Store) GetSplits(uid, id int64) ([]SplitPart, error) {
	if !s.canSeeTx(uid, id) {
		return nil, ErrNotFound
	}
	rows, err := s.DB.Query(`SELECT amount_cents,category_id,notes,business FROM transactions WHERE parent_id=? ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SplitPart{}
	for rows.Next() {
		var p SplitPart
		var c sql.NullInt64
		var b int
		rows.Scan(&p.AmountCents, &c, &p.Notes, &b)
		p.CategoryID, p.Business = idPtr(c), b == 1
		out = append(out, p)
	}
	return out, nil
}

func (s *Store) canSeeTx(uid, id int64) bool {
	var n int
	s.DB.QueryRow(`SELECT COUNT(*) FROM transactions WHERE id=? AND (private=0 OR user_id=?)`, id, uid).Scan(&n)
	return n == 1
}

// SaveSplits replaces the parts of a transaction. The parts must add up to its amount; no parts removes the
// split. Parts have no account, so account balances and duplicate checks keep using the whole charge.
func (s *Store) SaveSplits(uid, id int64, parts []SplitPart) error {
	var amount int64
	var date, payee string
	var priv, tr, par int
	err := s.DB.QueryRow(`SELECT amount_cents,date,payee,private,transfer,parent_id IS NOT NULL FROM transactions WHERE id=? AND (private=0 OR user_id=?)`, id, uid).Scan(&amount, &date, &payee, &priv, &tr, &par)
	if err != nil {
		return ErrNotFound
	}
	if par == 1 {
		return errors.New("a part cannot be split again")
	}
	if len(parts) == 1 {
		return errors.New("a split needs at least two parts")
	}
	var sum int64
	for _, p := range parts {
		if p.AmountCents == 0 {
			return errors.New("every part needs an amount")
		}
		sum += p.AmountCents
	}
	if len(parts) > 0 && sum != amount {
		return fmt.Errorf("the parts add up to %s but the charge is %s", FormatCents(sum), FormatCents(amount))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	tx.Exec(`DELETE FROM transactions WHERE parent_id=?`, id)
	for _, p := range parts {
		if _, err := tx.Exec(`INSERT INTO transactions(date,amount_cents,account_id,category_id,payee,notes,user_id,private,created,source,transfer,business,parent_id) VALUES(?,?,NULL,?,?,?,?,?,?,'split',?,?,?)`,
			date, p.AmountCents, nullID(p.CategoryID), payee, p.Notes, uid, priv, s.nowStr(), tr, b2i(p.Business), id); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE transactions SET split=? WHERE id=?`, b2i(len(parts) > 0), id); err != nil {
		return err
	}
	return tx.Commit()
}

// ---------- bulk edit ----------

// BulkChange is what a bulk edit sets (nil = leave as is).
type BulkChange struct {
	CategoryID *int64 `json:"category_id"`
	ClearCat   bool   `json:"clear_category"`
	Business   *bool  `json:"business"`
	Transfer   *bool  `json:"transfer"`
	Private    *bool  `json:"private"`
}

// BulkEdit changes many visible transactions at once (split totals keep their parts' categories).
func (s *Store) BulkEdit(uid int64, ids []int64, c BulkChange) (int, error) {
	if len(ids) == 0 || len(ids) > 2000 {
		return 0, errors.New("pick 1 to 2000 transactions")
	}
	n := 0
	for _, id := range ids {
		q, args := `UPDATE transactions SET id=id`, []any{}
		if c.CategoryID != nil {
			q += `, category_id=CASE WHEN split=1 THEN category_id ELSE ? END`
			args = append(args, *c.CategoryID)
		} else if c.ClearCat {
			q += `, category_id=NULL`
		}
		if c.Business != nil {
			q += `, business=?`
			args = append(args, b2i(*c.Business))
		}
		if c.Transfer != nil {
			q += `, transfer=?`
			args = append(args, b2i(*c.Transfer))
		}
		if c.Private != nil {
			q += `, private=CASE WHEN user_id=? THEN ? ELSE private END`
			args = append(args, uid, b2i(*c.Private))
		}
		args = append(args, id, uid)
		res, err := s.DB.Exec(q+` WHERE id=? AND parent_id IS NULL AND (private=0 OR user_id=?)`, args...)
		if err != nil {
			return n, err
		}
		if k, _ := res.RowsAffected(); k > 0 {
			n++
			if c.Transfer != nil || c.Private != nil {
				s.DB.Exec(`UPDATE transactions SET transfer=(SELECT transfer FROM transactions p WHERE p.id=?), private=(SELECT private FROM transactions p WHERE p.id=?) WHERE parent_id=?`, id, id, id)
			}
		}
	}
	return n, nil
}

// ---------- attachments (receipts now, documents later) ----------

// Attachment is an uploaded receipt or document file.
type Attachment struct {
	ID            int64  `json:"id"`
	Kind          string `json:"kind"`
	TransactionID *int64 `json:"transaction_id"`
	DocumentID    *int64 `json:"document_id"`
	Name          string `json:"name"`
	Mime          string `json:"mime"`
	Size          int64  `json:"size"`
	Created       string `json:"created"`
	path          string
}

// AllowedUpload names the file types people can attach: photos and PDFs only.
var AllowedUpload = map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/gif": ".gif", "image/webp": ".webp", "application/pdf": ".pdf"}

// MaxUpload is the largest attachment.
const MaxUpload = 15 << 20

// FilesDir is where attachments are stored (inside the data folder, so backups copy them).
func (s *Store) FilesDir() string { return filepath.Join(filepath.Dir(s.Path), "files") }

func cleanName(n string) string {
	n = filepath.Base(strings.ReplaceAll(n, "\\", "/"))
	n = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`"<>|:*?`, r) {
			return -1
		}
		return r
	}, n)
	if len(n) > 120 {
		n = n[len(n)-120:]
	}
	if n == "" || n == "." {
		n = "file"
	}
	return n
}

// AddAttachment checks and stores a file. kind is "receipt" (txID set) or "document" (docID set).
func (s *Store) AddAttachment(uid int64, kind string, txID, docID *int64, name string, data []byte) (*Attachment, error) {
	if len(data) == 0 || len(data) > MaxUpload {
		return nil, fmt.Errorf("files must be 1 byte to %d MB", MaxUpload>>20)
	}
	mime := http.DetectContentType(data)
	if i := strings.Index(mime, ";"); i > 0 {
		mime = mime[:i]
	}
	ext, ok := AllowedUpload[mime]
	if !ok {
		return nil, errors.New("only photos (JPG, PNG, GIF, WebP) and PDF files can be attached")
	}
	if kind == "receipt" && (txID == nil || !s.canSeeTx(uid, *txID)) {
		return nil, ErrNotFound
	}
	dir := filepath.Join(s.FilesDir(), kind+"s")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	rel := filepath.Join(kind+"s", randToken(16)+ext)
	if err := os.WriteFile(filepath.Join(s.FilesDir(), rel), data, 0o644); err != nil {
		return nil, err
	}
	a := &Attachment{Kind: kind, TransactionID: txID, DocumentID: docID, Name: cleanName(name), Mime: mime, Size: int64(len(data)), Created: s.nowStr()}
	res, err := s.DB.Exec(`INSERT INTO attachments(kind,owner_id,transaction_id,document_id,name,mime,size,path,created) VALUES(?,?,?,?,?,?,?,?,?)`,
		kind, uid, nullID(txID), nullID(docID), a.Name, mime, a.Size, filepath.ToSlash(rel), a.Created)
	if err != nil {
		os.Remove(filepath.Join(s.FilesDir(), rel))
		return nil, err
	}
	a.ID, _ = res.LastInsertId()
	return a, nil
}

// ListAttachments lists the files of a transaction or document the user can see.
func (s *Store) ListAttachments(uid int64, txID, docID *int64) ([]Attachment, error) {
	if txID != nil && !s.canSeeTx(uid, *txID) {
		return nil, ErrNotFound
	}
	if docID != nil && !s.canSeeDoc(uid, *docID) {
		return nil, ErrNotFound
	}
	rows, err := s.DB.Query(`SELECT id,kind,transaction_id,document_id,name,mime,size,created FROM attachments WHERE (transaction_id=? OR document_id=?) ORDER BY id`, nullID(txID), nullID(docID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Attachment{}
	for rows.Next() {
		var a Attachment
		var t, d sql.NullInt64
		rows.Scan(&a.ID, &a.Kind, &t, &d, &a.Name, &a.Mime, &a.Size, &a.Created)
		a.TransactionID, a.DocumentID = idPtr(t), idPtr(d)
		out = append(out, a)
	}
	return out, nil
}

// canSeeDoc is replaced when the document vault exists; until then no document is visible.
var canSeeDocHook func(s *Store, uid, id int64) bool

func (s *Store) canSeeDoc(uid, id int64) bool {
	if canSeeDocHook == nil {
		return false
	}
	return canSeeDocHook(s, uid, id)
}

// GetAttachment returns a file the user may see, with its full path.
func (s *Store) GetAttachment(uid, id int64) (*Attachment, string, error) {
	var a Attachment
	var t, d sql.NullInt64
	var rel string
	err := s.DB.QueryRow(`SELECT id,kind,transaction_id,document_id,name,mime,size,created,path FROM attachments WHERE id=?`, id).Scan(&a.ID, &a.Kind, &t, &d, &a.Name, &a.Mime, &a.Size, &a.Created, &rel)
	if err != nil {
		return nil, "", ErrNotFound
	}
	a.TransactionID, a.DocumentID = idPtr(t), idPtr(d)
	if (a.TransactionID != nil && !s.canSeeTx(uid, *a.TransactionID)) || (a.DocumentID != nil && !s.canSeeDoc(uid, *a.DocumentID)) || (a.TransactionID == nil && a.DocumentID == nil) {
		return nil, "", ErrNotFound
	}
	full := filepath.Join(s.FilesDir(), filepath.FromSlash(rel))
	if !strings.HasPrefix(full, s.FilesDir()) {
		return nil, "", ErrNotFound
	}
	return &a, full, nil
}

// DeleteAttachment removes a file.
// The file stays on disk for 30 days so it can be restored from "recently deleted".
func (s *Store) DeleteAttachment(uid, id int64) error {
	a, _, err := s.GetAttachment(uid, id)
	if err != nil {
		return err
	}
	return s.trashThen("file", a.Name, uid, true, uid, []partQuery{{"attachments", "id=?", []any{id}}},
		func() error { return mustChange(s.DB.Exec(`DELETE FROM attachments WHERE id=?`, id)) })
}

// ---------- business tax report ----------

// TaxLine is one row of the yearly report.
type TaxLine struct {
	Line        string `json:"line"`
	Category    string `json:"category"`
	Kind        string `json:"kind"`
	AmountCents int64  `json:"amount_cents"` // positive
	Count       int    `json:"count"`
}

// TaxReport totals a year's business transactions per category and Schedule C line. It is a summary to give
// a tax preparer, not tax advice.
type TaxReport struct {
	Year          int       `json:"year"`
	IncomeCents   int64     `json:"income_cents"`
	ExpenseCents  int64     `json:"expense_cents"`
	Lines         []TaxLine `json:"lines"`
	Uncategorised int       `json:"uncategorised"`
	NoReceipt     int       `json:"no_receipt"` // business expenses over $75 without a receipt
}

// BusinessTaxReport builds the yearly business summary.
func (s *Store) BusinessTaxReport(uid int64, year int) (*TaxReport, error) {
	if year < 2000 || year > 2100 {
		year = time.Now().Year()
	}
	from, to := fmt.Sprintf("%d-01-01", year), fmt.Sprintf("%d-12-31", year)
	rows, err := s.DB.Query(`SELECT COALESCE(c.name,''), COALESCE(c.tax_line,''), COALESCE(c.kind,''), t.amount_cents,
		(SELECT COUNT(*) FROM attachments a WHERE a.transaction_id=COALESCE(t.parent_id,t.id))
		FROM transactions t LEFT JOIN categories c ON c.id=t.category_id
		WHERE t.business=1 AND t.transfer=0 AND t.split=0 AND t.date>=? AND t.date<=? AND (t.private=0 OR t.user_id=?)`, from, to, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	r := &TaxReport{Year: year, Lines: []TaxLine{}}
	byKey := map[string]*TaxLine{}
	for rows.Next() {
		var cat, line, kind string
		var amt int64
		var rec int
		rows.Scan(&cat, &line, &kind, &amt, &rec)
		if cat == "" {
			r.Uncategorised++
			cat = "(no category)"
		}
		if line == "" {
			line = "(no tax line)"
		}
		if amt > 0 {
			r.IncomeCents += amt
			kind = "income"
		} else {
			r.ExpenseCents += -amt
			kind = "expense"
			if -amt >= 7500 && rec == 0 {
				r.NoReceipt++
			}
		}
		k := line + "|" + cat + "|" + kind
		if byKey[k] == nil {
			byKey[k] = &TaxLine{Line: line, Category: cat, Kind: kind}
		}
		byKey[k].AmountCents += abs(amt)
		byKey[k].Count++
	}
	for _, l := range byKey {
		r.Lines = append(r.Lines, *l)
	}
	sort.Slice(r.Lines, func(i, j int) bool {
		if r.Lines[i].Kind != r.Lines[j].Kind {
			return r.Lines[i].Kind == "income"
		}
		if r.Lines[i].Line != r.Lines[j].Line {
			return r.Lines[i].Line < r.Lines[j].Line
		}
		return r.Lines[i].Category < r.Lines[j].Category
	})
	return r, nil
}

// TaxReportCSV is the report as CSV (for a tax preparer).
func (s *Store) TaxReportCSV(uid int64, year int) (string, error) {
	r, err := s.BusinessTaxReport(uid, year)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Business summary %d (not tax advice; check with your tax preparer)\r\n", r.Year)
	b.WriteString("type,schedule_c_line,category,amount,transactions\r\n")
	for _, l := range r.Lines {
		fmt.Fprintf(&b, "%s,%s,%s,%s,%d\r\n", l.Kind, csvCell(l.Line), csvCell(l.Category), strings.ReplaceAll(FormatCents(l.AmountCents), ",", ""), l.Count)
	}
	fmt.Fprintf(&b, "total,,income,%s,\r\ntotal,,expenses,%s,\r\n", strings.ReplaceAll(FormatCents(r.IncomeCents), ",", ""), strings.ReplaceAll(FormatCents(r.ExpenseCents), ",", ""))
	return b.String(), nil
}
