package core

import (
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"
)

// visible is the SQL filter for rows a user may see: shared rows plus their own private ones.
const visible = `(private=0 OR owner_id=?)`

// Account is a bank account, card or cash pot.
type Account struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	OpeningCents int64  `json:"opening_cents"`
	BalanceCents int64  `json:"balance_cents"`
	OwnerID      int64  `json:"owner_id"`
	Private      bool   `json:"private"`
	Archived     bool   `json:"archived"`
	Notes        string `json:"notes"`
}

// Category groups transactions; expense categories can carry a monthly budget.
type Category struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	BudgetCents int64  `json:"budget_cents"`
	Color       string `json:"color"`
}

// Transaction is money in (positive) or out (negative).
type Transaction struct {
	ID          int64  `json:"id"`
	Date        string `json:"date"`
	AmountCents int64  `json:"amount_cents"`
	AccountID   *int64 `json:"account_id"`
	CategoryID  *int64 `json:"category_id"`
	Payee       string `json:"payee"`
	Notes       string `json:"notes"`
	UserID      int64  `json:"user_id"`
	Private     bool   `json:"private"`
	BillID      *int64 `json:"bill_id"`
}

// Bill is a recurring (or one-time) payment with a due date.
type Bill struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	AmountCents int64  `json:"amount_cents"`
	CategoryID  *int64 `json:"category_id"`
	AccountID   *int64 `json:"account_id"`
	StartDate   string `json:"start_date"`
	Freq        string `json:"freq"`
	Interval    int    `json:"interval"`
	Until       string `json:"until"`
	Autopay     bool   `json:"autopay"`
	RemindDays  string `json:"remind_days"`
	AssignedTo  *int64 `json:"assigned_to"`
	OwnerID     int64  `json:"owner_id"`
	Private     bool   `json:"private"`
	URL         string `json:"url"`
	Notes       string `json:"notes"`
	Active      bool   `json:"active"`
}

// BillDue is one due date of a bill.
type BillDue struct {
	BillID      int64  `json:"bill_id"`
	Name        string `json:"name"`
	Date        string `json:"date"`
	AmountCents int64  `json:"amount_cents"`
	Paid        bool   `json:"paid"`
	PaidCents   int64  `json:"paid_cents"`
	PaidDate    string `json:"paid_date"`
	Autopay     bool   `json:"autopay"`
	AssignedTo  *int64 `json:"assigned_to"`
	Overdue     bool   `json:"overdue"`
	URL         string `json:"url"`
}

func nullID(p *int64) any {
	if p == nil || *p == 0 {
		return nil
	}
	return *p
}

func idPtr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

// ---------- accounts ----------

// ListAccounts returns visible accounts with their current balance.
func (s *Store) ListAccounts(uid int64) ([]Account, error) {
	rows, err := s.DB.Query(`SELECT a.id,a.name,a.type,a.opening_cents,a.owner_id,a.private,a.archived,a.notes,
		a.opening_cents + COALESCE((SELECT SUM(amount_cents) FROM transactions t WHERE t.account_id=a.id),0)
		FROM accounts a WHERE (a.private=0 OR a.owner_id=?) ORDER BY a.archived, a.name`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Account{}
	for rows.Next() {
		var a Account
		var p, ar int
		if err := rows.Scan(&a.ID, &a.Name, &a.Type, &a.OpeningCents, &a.OwnerID, &p, &ar, &a.Notes, &a.BalanceCents); err != nil {
			return nil, err
		}
		a.Private, a.Archived = p == 1, ar == 1
		out = append(out, a)
	}
	return out, rows.Err()
}

// SaveAccount inserts (ID 0) or updates an account. Only the owner may change a private one.
func (s *Store) SaveAccount(uid int64, a *Account) error {
	a.Name = strings.TrimSpace(a.Name)
	if a.Name == "" {
		return errors.New("name is required")
	}
	if a.ID == 0 {
		res, err := s.DB.Exec(`INSERT INTO accounts(name,type,opening_cents,owner_id,private,archived,notes) VALUES(?,?,?,?,?,?,?)`,
			a.Name, a.Type, a.OpeningCents, uid, b2i(a.Private), b2i(a.Archived), a.Notes)
		if err == nil {
			a.ID, _ = res.LastInsertId()
		}
		return err
	}
	res, err := s.DB.Exec(`UPDATE accounts SET name=?,type=?,opening_cents=?,private=?,archived=?,notes=? WHERE id=? AND `+visible,
		a.Name, a.Type, a.OpeningCents, b2i(a.Private), b2i(a.Archived), a.Notes, a.ID, uid)
	return mustChange(res, err)
}

// DeleteAccount removes an account; its transactions stay with no account.
func (s *Store) DeleteAccount(uid, id int64) error {
	return mustChange(s.DB.Exec(`DELETE FROM accounts WHERE id=? AND `+visible, id, uid))
}

func mustChange(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------- categories ----------

// ListCategories returns every category.
func (s *Store) ListCategories() ([]Category, error) {
	rows, err := s.DB.Query(`SELECT id,name,kind,budget_cents,color FROM categories ORDER BY kind DESC, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Category{}
	for rows.Next() {
		var c Category
		rows.Scan(&c.ID, &c.Name, &c.Kind, &c.BudgetCents, &c.Color)
		out = append(out, c)
	}
	return out, rows.Err()
}

// SaveCategory inserts or updates a category.
func (s *Store) SaveCategory(c *Category) error {
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" {
		return errors.New("name is required")
	}
	if c.Kind != "income" {
		c.Kind = "expense"
	}
	if c.ID == 0 {
		res, err := s.DB.Exec(`INSERT INTO categories(name,kind,budget_cents,color) VALUES(?,?,?,?)`, c.Name, c.Kind, c.BudgetCents, c.Color)
		if err != nil && strings.Contains(err.Error(), "UNIQUE") {
			return errors.New("that category exists")
		}
		if err == nil {
			c.ID, _ = res.LastInsertId()
		}
		return err
	}
	return mustChange(s.DB.Exec(`UPDATE categories SET name=?,kind=?,budget_cents=?,color=? WHERE id=?`, c.Name, c.Kind, c.BudgetCents, c.Color, c.ID))
}

// DeleteCategory removes a category; transactions keep no category.
func (s *Store) DeleteCategory(id int64) error {
	return mustChange(s.DB.Exec(`DELETE FROM categories WHERE id=?`, id))
}

// ---------- transactions ----------

// TxFilter narrows ListTransactions.
type TxFilter struct {
	From, To   string // dates, inclusive
	AccountID  int64
	CategoryID int64
	Query      string
	Limit      int
}

// ListTransactions returns visible transactions, newest first.
func (s *Store) ListTransactions(uid int64, f TxFilter) ([]Transaction, error) {
	q := `SELECT id,date,amount_cents,account_id,category_id,payee,notes,user_id,private,bill_id FROM transactions WHERE (private=0 OR user_id=?)`
	args := []any{uid}
	if f.From != "" {
		q += ` AND date>=?`
		args = append(args, f.From)
	}
	if f.To != "" {
		q += ` AND date<=?`
		args = append(args, f.To)
	}
	if f.AccountID != 0 {
		q += ` AND account_id=?`
		args = append(args, f.AccountID)
	}
	if f.CategoryID != 0 {
		q += ` AND category_id=?`
		args = append(args, f.CategoryID)
	}
	if f.Query != "" {
		q += ` AND (payee LIKE ? OR notes LIKE ?)`
		like := "%" + f.Query + "%"
		args = append(args, like, like)
	}
	if f.Limit <= 0 || f.Limit > 5000 {
		f.Limit = 1000
	}
	q += ` ORDER BY date DESC, id DESC LIMIT ?`
	args = append(args, f.Limit)
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Transaction{}
	for rows.Next() {
		var t Transaction
		var acc, cat, bill sql.NullInt64
		var p int
		if err := rows.Scan(&t.ID, &t.Date, &t.AmountCents, &acc, &cat, &t.Payee, &t.Notes, &t.UserID, &p, &bill); err != nil {
			return nil, err
		}
		t.AccountID, t.CategoryID, t.BillID, t.Private = idPtr(acc), idPtr(cat), idPtr(bill), p == 1
		out = append(out, t)
	}
	return out, rows.Err()
}

// SaveTransaction inserts or updates a transaction (private ones only by their creator).
func (s *Store) SaveTransaction(uid int64, t *Transaction) error {
	if _, err := time.Parse(DateFmt, t.Date); err != nil {
		return errors.New("date must be YYYY-MM-DD")
	}
	if t.AmountCents == 0 {
		return errors.New("amount is required")
	}
	if t.ID == 0 {
		res, err := s.DB.Exec(`INSERT INTO transactions(date,amount_cents,account_id,category_id,payee,notes,user_id,private,bill_id,created) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			t.Date, t.AmountCents, nullID(t.AccountID), nullID(t.CategoryID), strings.TrimSpace(t.Payee), t.Notes, uid, b2i(t.Private), nullID(t.BillID), s.nowStr())
		if err == nil {
			t.ID, _ = res.LastInsertId()
			t.UserID = uid
		}
		return err
	}
	return mustChange(s.DB.Exec(`UPDATE transactions SET date=?,amount_cents=?,account_id=?,category_id=?,payee=?,notes=?,private=? WHERE id=? AND (private=0 OR user_id=?)`,
		t.Date, t.AmountCents, nullID(t.AccountID), nullID(t.CategoryID), strings.TrimSpace(t.Payee), t.Notes, b2i(t.Private), t.ID, uid))
}

// DeleteTransaction removes a transaction (and un-pays a bill it paid).
func (s *Store) DeleteTransaction(uid, id int64) error {
	if err := mustChange(s.DB.Exec(`DELETE FROM transactions WHERE id=? AND (private=0 OR user_id=?)`, id, uid)); err != nil {
		return err
	}
	s.DB.Exec(`UPDATE bill_payments SET transaction_id=NULL WHERE transaction_id=?`, id)
	return nil
}

// ---------- bills ----------

const billCols = `id,name,amount_cents,category_id,account_id,start_date,freq,interval,until,autopay,remind_days,assigned_to,owner_id,private,url,notes,active`

func scanBill(r interface{ Scan(...any) error }) (Bill, error) {
	var b Bill
	var cat, acc, asg sql.NullInt64
	var ap, p, act int
	err := r.Scan(&b.ID, &b.Name, &b.AmountCents, &cat, &acc, &b.StartDate, &b.Freq, &b.Interval, &b.Until, &ap, &b.RemindDays, &asg, &b.OwnerID, &p, &b.URL, &b.Notes, &act)
	b.CategoryID, b.AccountID, b.AssignedTo = idPtr(cat), idPtr(acc), idPtr(asg)
	b.Autopay, b.Private, b.Active = ap == 1, p == 1, act == 1
	return b, err
}

// ListBills returns visible bills (uid 0 = every bill, for the reminder engine).
func (s *Store) ListBills(uid int64) ([]Bill, error) {
	q, args := `SELECT `+billCols+` FROM bills`, []any{}
	if uid != 0 {
		q += ` WHERE ` + visible
		args = append(args, uid)
	}
	rows, err := s.DB.Query(q+` ORDER BY name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Bill{}
	for rows.Next() {
		b, err := scanBill(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// GetBill loads one visible bill.
func (s *Store) GetBill(uid, id int64) (Bill, error) {
	b, err := scanBill(s.DB.QueryRow(`SELECT `+billCols+` FROM bills WHERE id=? AND `+visible, id, uid))
	if errors.Is(err, sql.ErrNoRows) {
		return b, ErrNotFound
	}
	return b, err
}

// SaveBill inserts or updates a bill.
func (s *Store) SaveBill(uid int64, b *Bill) error {
	b.Name = strings.TrimSpace(b.Name)
	if b.Name == "" {
		return errors.New("name is required")
	}
	if _, err := time.Parse(DateFmt, b.StartDate); err != nil {
		return errors.New("first due date must be YYYY-MM-DD")
	}
	if b.Freq == "none" {
		b.Freq = "once"
	}
	if !Freqs[b.Freq] {
		return errors.New("bad repeat")
	}
	if b.Interval < 1 {
		b.Interval = 1
	}
	if b.ID == 0 {
		res, err := s.DB.Exec(`INSERT INTO bills(name,amount_cents,category_id,account_id,start_date,freq,interval,until,autopay,remind_days,assigned_to,owner_id,private,url,notes,active) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,1)`,
			b.Name, b.AmountCents, nullID(b.CategoryID), nullID(b.AccountID), b.StartDate, b.Freq, b.Interval, b.Until, b.b2iAutopay(), b.RemindDays, nullID(b.AssignedTo), uid, b2i(b.Private), b.URL, b.Notes)
		if err == nil {
			b.ID, _ = res.LastInsertId()
			b.OwnerID, b.Active = uid, true
		}
		return err
	}
	return mustChange(s.DB.Exec(`UPDATE bills SET name=?,amount_cents=?,category_id=?,account_id=?,start_date=?,freq=?,interval=?,until=?,autopay=?,remind_days=?,assigned_to=?,private=?,url=?,notes=?,active=? WHERE id=? AND `+visible,
		b.Name, b.AmountCents, nullID(b.CategoryID), nullID(b.AccountID), b.StartDate, b.Freq, b.Interval, b.Until, b.b2iAutopay(), b.RemindDays, nullID(b.AssignedTo), b2i(b.Private), b.URL, b.Notes, b2i(b.Active), b.ID, uid))
}

func (b *Bill) b2iAutopay() int { return b2i(b.Autopay) }

// DeleteBill removes a bill and its payment history (transactions stay).
func (s *Store) DeleteBill(uid, id int64) error {
	return mustChange(s.DB.Exec(`DELETE FROM bills WHERE id=? AND `+visible, id, uid))
}

// BillDues expands bills into due dates within [from, to] and marks paid / overdue ones.
func (s *Store) BillDues(uid int64, from, to time.Time) ([]BillDue, error) {
	bills, err := s.ListBills(uid)
	if err != nil {
		return nil, err
	}
	paid := map[int64]map[string][2]any{}
	rows, err := s.DB.Query(`SELECT bill_id,occ_date,paid_date,amount_cents FROM bill_payments WHERE occ_date>=? AND occ_date<=?`, from.Format(DateFmt), to.Format(DateFmt))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, amt int64
		var occ, pd string
		rows.Scan(&id, &occ, &pd, &amt)
		if paid[id] == nil {
			paid[id] = map[string][2]any{}
		}
		paid[id][occ] = [2]any{pd, amt}
	}
	rows.Close()
	today := s.Now().Format(DateFmt)
	out := []BillDue{}
	for _, b := range bills {
		if !b.Active {
			continue
		}
		st, err := ParseLocal(b.StartDate)
		if err != nil {
			continue
		}
		for _, t := range Occurrences(st, b.Freq, b.Interval, b.Until, from, to) {
			d := BillDue{BillID: b.ID, Name: b.Name, Date: t.Format(DateFmt), AmountCents: b.AmountCents, Autopay: b.Autopay, AssignedTo: b.AssignedTo, URL: b.URL}
			if p, ok := paid[b.ID][d.Date]; ok {
				d.Paid, d.PaidDate, d.PaidCents = true, p[0].(string), p[1].(int64)
			}
			d.Overdue = !d.Paid && d.Date < today
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Date != out[j].Date {
			return out[i].Date < out[j].Date
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// PayBill marks one due date paid. With record=true it also adds the expense transaction.
func (s *Store) PayBill(uid, billID int64, occ, paidDate string, amount int64, record bool) error {
	b, err := s.GetBill(uid, billID)
	if err != nil {
		return err
	}
	if paidDate == "" {
		paidDate = s.Now().Format(DateFmt)
	}
	if amount == 0 {
		amount = b.AmountCents
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var txID any
	if record && amount != 0 {
		t := Transaction{Date: paidDate, AmountCents: -abs(amount), AccountID: b.AccountID, CategoryID: b.CategoryID, Payee: b.Name, Notes: "Bill due " + occ, Private: b.Private, BillID: &b.ID}
		if err := s.SaveTransaction(uid, &t); err != nil {
			return err
		}
		txID = t.ID
	}
	_, err = s.DB.Exec(`INSERT INTO bill_payments(bill_id,occ_date,paid_date,amount_cents,user_id,transaction_id) VALUES(?,?,?,?,?,?)
		ON CONFLICT(bill_id,occ_date) DO UPDATE SET paid_date=excluded.paid_date, amount_cents=excluded.amount_cents`, b.ID, occ, paidDate, abs(amount), uid, txID)
	return err
}

// UnpayBill clears a payment and deletes the transaction it created.
func (s *Store) UnpayBill(uid, billID int64, occ string) error {
	if _, err := s.GetBill(uid, billID); err != nil {
		return err
	}
	var tx sql.NullInt64
	s.DB.QueryRow(`SELECT transaction_id FROM bill_payments WHERE bill_id=? AND occ_date=?`, billID, occ).Scan(&tx)
	if tx.Valid {
		s.DB.Exec(`DELETE FROM transactions WHERE id=?`, tx.Int64)
	}
	return mustChange(s.DB.Exec(`DELETE FROM bill_payments WHERE bill_id=? AND occ_date=?`, billID, occ))
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// ---------- summary ----------

// CategorySpend is one row of the monthly budget view.
type CategorySpend struct {
	Category
	SpentCents int64 `json:"spent_cents"`
}

// MonthSummary is the money part of the dashboard.
type MonthSummary struct {
	Month         string          `json:"month"`
	IncomeCents   int64           `json:"income_cents"`
	ExpenseCents  int64           `json:"expense_cents"`
	BudgetCents   int64           `json:"budget_cents"`
	BudgetSpent   int64           `json:"budget_spent_cents"` // spending in categories that have a budget
	BudgetLeft    int64           `json:"budget_left_cents"`  // sum of (budget - spent) over budgeted categories
	Categories    []CategorySpend `json:"categories"`
	Uncategorised int64           `json:"uncategorised_cents"`
}

// Summary totals a month ("2006-01") of visible transactions per category.
func (s *Store) Summary(uid int64, month string) (MonthSummary, error) {
	ms := MonthSummary{Month: month, Categories: []CategorySpend{}}
	start, err := time.ParseInLocation("2006-01", month, time.Local)
	if err != nil {
		return ms, errors.New("month must be YYYY-MM")
	}
	end := start.AddDate(0, 1, -1)
	cats, err := s.ListCategories()
	if err != nil {
		return ms, err
	}
	spent := map[int64]int64{}
	rows, err := s.DB.Query(`SELECT category_id, SUM(amount_cents) FROM transactions WHERE date>=? AND date<=? AND (private=0 OR user_id=?) GROUP BY category_id`,
		start.Format(DateFmt), end.Format(DateFmt), uid)
	if err != nil {
		return ms, err
	}
	for rows.Next() {
		var c sql.NullInt64
		var sum int64
		rows.Scan(&c, &sum)
		if c.Valid {
			spent[c.Int64] = sum
		} else {
			ms.Uncategorised = sum
		}
	}
	rows.Close()
	s.DB.QueryRow(`SELECT COALESCE(SUM(CASE WHEN amount_cents>0 THEN amount_cents END),0), COALESCE(SUM(CASE WHEN amount_cents<0 THEN -amount_cents END),0)
		FROM transactions WHERE date>=? AND date<=? AND (private=0 OR user_id=?)`, start.Format(DateFmt), end.Format(DateFmt), uid).Scan(&ms.IncomeCents, &ms.ExpenseCents)
	for _, c := range cats {
		cs := CategorySpend{Category: c, SpentCents: spent[c.ID]}
		if c.Kind == "expense" {
			cs.SpentCents = -cs.SpentCents // show spending as a positive number
			if c.BudgetCents > 0 {
				ms.BudgetCents += c.BudgetCents
				ms.BudgetSpent += cs.SpentCents
			}
		}
		ms.Categories = append(ms.Categories, cs)
	}
	ms.BudgetLeft = ms.BudgetCents - ms.BudgetSpent
	return ms, nil
}
