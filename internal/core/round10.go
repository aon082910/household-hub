package core

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// ---------- who owes whom ----------

// Share is one person's part of a shared cost.
type Share struct {
	UserID     int64 `json:"user_id"`
	ShareCents int64 `json:"share_cents"`
}

// SharedCost is something one person paid for several people (or a pay-back).
type SharedCost struct {
	ID          int64   `json:"id"`
	Date        string  `json:"date"`
	Description string  `json:"description"`
	AmountCents int64   `json:"amount_cents"`
	PaidBy      int64   `json:"paid_by"`
	Kind        string  `json:"kind"` // expense, settle
	Shares      []Share `json:"shares"`
}

// Balance is a member's running total: positive = others owe them.
type Balance struct {
	UserID int64 `json:"user_id"`
	Cents  int64 `json:"cents"`
}

// Payment is a suggested pay-back to settle up.
type Payment struct {
	From  int64 `json:"from"`
	To    int64 `json:"to"`
	Cents int64 `json:"cents"`
}

// SplitEvenly divides an amount between people; the first ones take the leftover cents.
func SplitEvenly(amount int64, users []int64) []Share {
	n := int64(len(users))
	if n == 0 {
		return nil
	}
	base, rest := amount/n, amount%n
	out := make([]Share, len(users))
	for i, u := range users {
		out[i] = Share{UserID: u, ShareCents: base}
		if int64(i) < rest {
			out[i].ShareCents++
		}
	}
	return out
}

// SaveSharedCost records a shared cost. The shares must add up to the amount.
func (s *Store) SaveSharedCost(by int64, c *SharedCost) error {
	c.Description = strings.TrimSpace(c.Description)
	if c.Description == "" || c.AmountCents <= 0 {
		return errors.New("what was it and how much?")
	}
	if _, err := time.Parse(DateFmt, c.Date); err != nil {
		return errors.New("date must be YYYY-MM-DD")
	}
	if c.Kind != "settle" {
		c.Kind = "expense"
	}
	var sum int64
	for _, sh := range c.Shares {
		if sh.ShareCents < 0 {
			return errors.New("shares cannot be negative")
		}
		sum += sh.ShareCents
	}
	if sum != c.AmountCents {
		return fmt.Errorf("the shares add up to %s, not %s", FormatCents(sum), FormatCents(c.AmountCents))
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if c.ID == 0 {
		res, err := tx.Exec(`INSERT INTO owes(date,description,amount_cents,paid_by,kind,created_by,created) VALUES(?,?,?,?,?,?,?)`, c.Date, c.Description, c.AmountCents, c.PaidBy, c.Kind, by, s.nowStr())
		if err != nil {
			return err
		}
		c.ID, _ = res.LastInsertId()
	} else {
		if _, err := tx.Exec(`UPDATE owes SET date=?,description=?,amount_cents=?,paid_by=?,kind=? WHERE id=?`, c.Date, c.Description, c.AmountCents, c.PaidBy, c.Kind, c.ID); err != nil {
			return err
		}
		tx.Exec(`DELETE FROM owes_shares WHERE owes_id=?`, c.ID)
	}
	for _, sh := range c.Shares {
		if sh.ShareCents == 0 {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO owes_shares(owes_id,user_id,share_cents) VALUES(?,?,?)`, c.ID, sh.UserID, sh.ShareCents); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeleteSharedCost removes one (kept 30 days in recently deleted).
func (s *Store) DeleteSharedCost(by, id int64) error {
	var d string
	if s.DB.QueryRow(`SELECT description FROM owes WHERE id=?`, id).Scan(&d) != nil {
		return ErrNotFound
	}
	return s.trashThen("shared cost", d, 0, false, by, []partQuery{{"owes", "id=?", []any{id}}, {"owes_shares", "owes_id=?", []any{id}}},
		func() error { return mustChange(s.DB.Exec(`DELETE FROM owes WHERE id=?`, id)) })
}

// SharedCosts lists recent shared costs, each person's balance and the fewest payments that settle everyone.
func (s *Store) SharedCosts() ([]SharedCost, []Balance, []Payment, error) {
	rows, err := s.DB.Query(`SELECT id,date,description,amount_cents,paid_by,kind FROM owes ORDER BY date DESC, id DESC LIMIT 300`)
	if err != nil {
		return nil, nil, nil, err
	}
	list := []SharedCost{}
	idx := map[int64]int{}
	for rows.Next() {
		var c SharedCost
		rows.Scan(&c.ID, &c.Date, &c.Description, &c.AmountCents, &c.PaidBy, &c.Kind)
		idx[c.ID] = len(list)
		list = append(list, c)
	}
	rows.Close()
	sr, err := s.DB.Query(`SELECT owes_id,user_id,share_cents FROM owes_shares`)
	if err != nil {
		return nil, nil, nil, err
	}
	for sr.Next() {
		var oid int64
		var sh Share
		sr.Scan(&oid, &sh.UserID, &sh.ShareCents)
		if i, ok := idx[oid]; ok {
			list[i].Shares = append(list[i].Shares, sh)
		}
	}
	sr.Close()
	// balances over everything (not only the listed 300)
	bal := map[int64]int64{}
	r1, _ := s.DB.Query(`SELECT paid_by, SUM(amount_cents) FROM owes GROUP BY paid_by`)
	for r1 != nil && r1.Next() {
		var u, c int64
		r1.Scan(&u, &c)
		bal[u] += c
	}
	if r1 != nil {
		r1.Close()
	}
	r2, _ := s.DB.Query(`SELECT user_id, SUM(share_cents) FROM owes_shares GROUP BY user_id`)
	for r2 != nil && r2.Next() {
		var u, c int64
		r2.Scan(&u, &c)
		bal[u] -= c
	}
	if r2 != nil {
		r2.Close()
	}
	var balances []Balance
	for u, c := range bal {
		if c != 0 {
			balances = append(balances, Balance{u, c})
		}
	}
	sort.Slice(balances, func(i, j int) bool { return balances[i].Cents > balances[j].Cents })
	return list, nonNil(balances), settle(balances), nil
}

func nonNil(b []Balance) []Balance {
	if b == nil {
		return []Balance{}
	}
	return b
}

// settle pairs the biggest debtor with the biggest creditor until everyone is even.
func settle(b []Balance) []Payment {
	var cred, debt []Balance
	for _, x := range b {
		if x.Cents > 0 {
			cred = append(cred, x)
		} else if x.Cents < 0 {
			debt = append(debt, Balance{x.UserID, -x.Cents})
		}
	}
	sort.Slice(cred, func(i, j int) bool { return cred[i].Cents > cred[j].Cents })
	sort.Slice(debt, func(i, j int) bool { return debt[i].Cents > debt[j].Cents })
	out := []Payment{}
	i, j := 0, 0
	for i < len(debt) && j < len(cred) {
		p := min(debt[i].Cents, cred[j].Cents)
		out = append(out, Payment{From: debt[i].UserID, To: cred[j].UserID, Cents: p})
		debt[i].Cents -= p
		cred[j].Cents -= p
		if debt[i].Cents == 0 {
			i++
		}
		if cred[j].Cents == 0 {
			j++
		}
	}
	return out
}

// ---------- rollover budgets ----------

// SetRollover turns carrying unspent budget into the next month on or off for a category (from this month on).
func (s *Store) SetRollover(catID int64, on bool) error {
	from := ""
	if on {
		from = s.Now().Format("2006-01")
	}
	return mustChange(s.DB.Exec(`UPDATE categories SET rollover=?, rollover_from=CASE WHEN ?=1 AND rollover_from<>'' THEN rollover_from ELSE ? END WHERE id=?`, b2i(on), b2i(on), from, catID))
}

// carryOver is the unspent (or overspent) budget carried into `month` for a rollover category.
func (s *Store) carryOver(uid int64, catID, budget int64, from, month string) int64 {
	if from == "" || from >= month {
		return 0
	}
	start, err := time.ParseInLocation("2006-01", from, time.Local)
	if err != nil {
		return 0
	}
	end, _ := time.ParseInLocation("2006-01", month, time.Local)
	var spent int64
	s.DB.QueryRow(`SELECT COALESCE(SUM(-amount_cents),0) FROM transactions WHERE category_id=? AND transfer=0 AND split=0 AND date>=? AND date<? AND (private=0 OR user_id=?)`,
		catID, start.Format(DateFmt), end.Format(DateFmt), uid).Scan(&spent)
	months := (end.Year()-start.Year())*12 + int(end.Month()) - int(start.Month())
	return budget*int64(months) - spent
}

// ---------- yearly costs ----------

// AnnualCost is a cost that comes once a year (car registration, Amazon Prime, property tax).
type AnnualCost struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	AmountCents int64  `json:"amount_cents"`
	Month       int    `json:"month"`
	CategoryID  *int64 `json:"category_id"`
	OwnerID     int64  `json:"owner_id"`
	Private     bool   `json:"private"`
	MonthsAway  int    `json:"months_away"`
}

// ListAnnualCosts returns yearly costs (soonest first) and how much to put aside each month for all of them.
func (s *Store) ListAnnualCosts(uid int64) ([]AnnualCost, int64, error) {
	rows, err := s.DB.Query(`SELECT id,name,amount_cents,month,category_id,owner_id,private FROM annual_costs WHERE `+visible, uid)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	now := int(s.Now().Month())
	out := []AnnualCost{}
	var total int64
	for rows.Next() {
		var a AnnualCost
		var c sql.NullInt64
		var p int
		rows.Scan(&a.ID, &a.Name, &a.AmountCents, &a.Month, &c, &a.OwnerID, &p)
		a.CategoryID, a.Private = idPtr(c), p == 1
		a.MonthsAway = (a.Month - now + 12) % 12
		total += a.AmountCents
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MonthsAway < out[j].MonthsAway })
	return out, int64(math.Ceil(float64(total) / 12)), nil
}

// SaveAnnualCost adds or updates a yearly cost.
func (s *Store) SaveAnnualCost(uid int64, a *AnnualCost) error {
	a.Name = strings.TrimSpace(a.Name)
	if a.Name == "" || a.AmountCents <= 0 || a.Month < 1 || a.Month > 12 {
		return errors.New("name, amount and month are required")
	}
	if a.ID == 0 {
		res, err := s.DB.Exec(`INSERT INTO annual_costs(name,amount_cents,month,category_id,owner_id,private) VALUES(?,?,?,?,?,?)`, a.Name, a.AmountCents, a.Month, nullID(a.CategoryID), uid, b2i(a.Private))
		if err == nil {
			a.ID, _ = res.LastInsertId()
		}
		return err
	}
	return mustChange(s.DB.Exec(`UPDATE annual_costs SET name=?,amount_cents=?,month=?,category_id=?,private=? WHERE id=? AND `+visible, a.Name, a.AmountCents, a.Month, nullID(a.CategoryID), b2i(a.Private), a.ID, uid))
}

// DeleteAnnualCost removes a yearly cost.
func (s *Store) DeleteAnnualCost(uid, id int64) error {
	return mustChange(s.DB.Exec(`DELETE FROM annual_costs WHERE id=? AND `+visible, id, uid))
}

// ---------- statement check ----------

// StatementCheck compares the app's balance on a date with the bank statement's closing balance.
type StatementCheck struct {
	AccountID   int64          `json:"account_id"`
	Date        string         `json:"date"`
	BankCents   int64          `json:"bank_cents"`
	AppCents    int64          `json:"app_cents"`
	DiffCents   int64          `json:"diff_cents"` // bank - app
	Duplicates  []Transaction  `json:"possible_duplicates"`
	MatchSingle []Transaction  `json:"could_explain"` // one transaction whose amount equals the difference
	LastChecks  []StatementLog `json:"last_checks"`
}

// StatementLog is a saved check.
type StatementLog struct {
	Date      string `json:"date"`
	BankCents int64  `json:"bank_cents"`
	AppCents  int64  `json:"app_cents"`
	Created   string `json:"created"`
}

// CheckStatement works out the difference and points at likely causes.
func (s *Store) CheckStatement(uid, accountID int64, date string, bankCents int64, save bool) (*StatementCheck, error) {
	if _, err := time.Parse(DateFmt, date); err != nil {
		return nil, errors.New("statement date must be YYYY-MM-DD")
	}
	var opening int64
	if s.DB.QueryRow(`SELECT opening_cents FROM accounts WHERE id=? AND `+visible, accountID, uid).Scan(&opening) != nil {
		return nil, ErrNotFound
	}
	var sum int64
	s.DB.QueryRow(`SELECT COALESCE(SUM(amount_cents),0) FROM transactions WHERE account_id=? AND date<=?`, accountID, date).Scan(&sum)
	c := &StatementCheck{AccountID: accountID, Date: date, BankCents: bankCents, AppCents: opening + sum}
	c.DiffCents = c.BankCents - c.AppCents
	d, _ := time.Parse(DateFmt, date)
	from := d.AddDate(0, 0, -45).Format(DateFmt)
	txs, _ := s.ListTransactions(uid, TxFilter{From: from, To: date, AccountID: accountID, Limit: 2000})
	c.Duplicates, c.MatchSingle = []Transaction{}, []Transaction{}
	for i := range txs {
		if c.DiffCents != 0 && (txs[i].AmountCents == -c.DiffCents || txs[i].AmountCents == c.DiffCents) {
			c.MatchSingle = append(c.MatchSingle, txs[i])
		}
		for j := i + 1; j < len(txs); j++ {
			a, b := txs[i], txs[j]
			if a.AmountCents != b.AmountCents {
				continue
			}
			da, _ := time.Parse(DateFmt, a.Date)
			db, _ := time.Parse(DateFmt, b.Date)
			if math.Abs(da.Sub(db).Hours()) <= 72 && subKey(a.Payee) == subKey(b.Payee) {
				c.Duplicates = append(c.Duplicates, a, b)
			}
		}
	}
	if save {
		s.DB.Exec(`INSERT INTO reconciliations(account_id,statement_date,statement_cents,app_cents,user_id,created) VALUES(?,?,?,?,?,?)`, accountID, date, bankCents, c.AppCents, uid, s.nowStr())
	}
	rows, err := s.DB.Query(`SELECT statement_date,statement_cents,app_cents,created FROM reconciliations WHERE account_id=? ORDER BY id DESC LIMIT 12`, accountID)
	if err == nil {
		for rows.Next() {
			var l StatementLog
			rows.Scan(&l.Date, &l.BankCents, &l.AppCents, &l.Created)
			c.LastChecks = append(c.LastChecks, l)
		}
		rows.Close()
	}
	if c.LastChecks == nil {
		c.LastChecks = []StatementLog{}
	}
	return c, nil
}
