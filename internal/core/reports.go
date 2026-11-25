package core

import (
	"database/sql"
	"errors"
	"math"
	"sort"
	"strings"
	"time"
)

// ---------- savings goals ----------

// Goal is money set aside for something (car insurance, Christmas). Progress comes from a linked savings
// account's balance, or from amounts added by hand.
type Goal struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	TargetCents int64  `json:"target_cents"`
	TargetDate  string `json:"target_date"`
	AccountID   *int64 `json:"account_id"`
	SavedCents  int64  `json:"saved_cents"`
	OwnerID     int64  `json:"owner_id"`
	Private     bool   `json:"private"`
	Color       string `json:"color"`
	Done        bool   `json:"done"`
	// computed
	ProgressPct  int   `json:"progress_pct"`
	PerMonth     int64 `json:"per_month_cents"` // to reach the target by its date
	MonthsLeft   int   `json:"months_left"`
	OnTrack      bool  `json:"on_track"`
	CurrentCents int64 `json:"current_cents"`
}

// ListGoals returns visible goals with progress.
func (s *Store) ListGoals(uid int64) ([]Goal, error) {
	rows, err := s.DB.Query(`SELECT id,name,target_cents,target_date,account_id,saved_cents,owner_id,private,color,done FROM goals WHERE `+visible+` ORDER BY done, target_date, name`, uid)
	if err != nil {
		return nil, err
	}
	var out []Goal
	for rows.Next() {
		var g Goal
		var a sql.NullInt64
		var p, d int
		rows.Scan(&g.ID, &g.Name, &g.TargetCents, &g.TargetDate, &a, &g.SavedCents, &g.OwnerID, &p, &g.Color, &d)
		g.AccountID, g.Private, g.Done = idPtr(a), p == 1, d == 1
		out = append(out, g)
	}
	rows.Close()
	accs, _ := s.ListAccounts(uid)
	bal := map[int64]int64{}
	for _, a := range accs {
		bal[a.ID] = a.BalanceCents
	}
	now := s.Now()
	for i := range out {
		g := &out[i]
		g.CurrentCents = g.SavedCents
		if g.AccountID != nil {
			g.CurrentCents = bal[*g.AccountID]
		}
		if g.TargetCents > 0 {
			g.ProgressPct = int(math.Min(100, math.Max(0, float64(g.CurrentCents)*100/float64(g.TargetCents))))
		}
		left := g.TargetCents - g.CurrentCents
		if t, err := time.ParseInLocation(DateFmt, g.TargetDate, time.Local); err == nil {
			m := (t.Year()-now.Year())*12 + int(t.Month()) - int(now.Month())
			if t.Day() < now.Day() {
				m--
			}
			if m < 1 {
				m = 1
			}
			g.MonthsLeft = m
			if left > 0 {
				g.PerMonth = int64(math.Ceil(float64(left) / float64(m)))
			}
		}
		g.OnTrack = left <= 0 || g.TargetDate == ""
	}
	if out == nil {
		out = []Goal{}
	}
	return out, nil
}

// SaveGoal adds or updates a goal.
func (s *Store) SaveGoal(uid int64, g *Goal) error {
	g.Name = strings.TrimSpace(g.Name)
	if g.Name == "" || g.TargetCents <= 0 {
		return errors.New("name and a target amount are required")
	}
	if g.TargetDate != "" {
		if _, err := time.Parse(DateFmt, g.TargetDate); err != nil {
			return errors.New("target date must be YYYY-MM-DD")
		}
	}
	if g.ID == 0 {
		res, err := s.DB.Exec(`INSERT INTO goals(name,target_cents,target_date,account_id,saved_cents,owner_id,private,color,done) VALUES(?,?,?,?,?,?,?,?,?)`,
			g.Name, g.TargetCents, g.TargetDate, nullID(g.AccountID), g.SavedCents, uid, b2i(g.Private), g.Color, b2i(g.Done))
		if err == nil {
			g.ID, _ = res.LastInsertId()
		}
		return err
	}
	return mustChange(s.DB.Exec(`UPDATE goals SET name=?,target_cents=?,target_date=?,account_id=?,private=?,color=?,done=? WHERE id=? AND `+visible,
		g.Name, g.TargetCents, g.TargetDate, nullID(g.AccountID), b2i(g.Private), g.Color, b2i(g.Done), g.ID, uid))
}

// DeleteGoal removes a goal.
func (s *Store) DeleteGoal(uid, id int64) error {
	return mustChange(s.DB.Exec(`DELETE FROM goals WHERE id=? AND `+visible, id, uid))
}

// AddToGoal records money put aside (negative = taken out) for a goal without a linked account.
func (s *Store) AddToGoal(uid, id, cents int64, note string) error {
	if cents == 0 {
		return errors.New("amount is required")
	}
	if err := mustChange(s.DB.Exec(`UPDATE goals SET saved_cents=saved_cents+? WHERE id=? AND account_id IS NULL AND `+visible, cents, id, uid)); err != nil {
		return errors.New("this goal follows an account's balance; add money to that account instead")
	}
	_, err := s.DB.Exec(`INSERT INTO goal_moves(goal_id,date,cents,note,user_id) VALUES(?,?,?,?,?)`, id, s.Now().Format(DateFmt), cents, note, uid)
	return err
}

// ---------- reports ----------

// MonthRow is one month of the trend report.
type MonthRow struct {
	Month        string           `json:"month"`
	IncomeCents  int64            `json:"income_cents"`
	ExpenseCents int64            `json:"expense_cents"`
	ByCategory   map[string]int64 `json:"by_category"` // spending per category name
}

// Trends totals the last n months (split parts, no transfers).
func (s *Store) Trends(uid int64, months int) ([]MonthRow, error) {
	if months < 1 || months > 60 {
		months = 12
	}
	now := s.Now()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local).AddDate(0, -(months - 1), 0)
	rows, err := s.DB.Query(`SELECT substr(t.date,1,7), COALESCE(c.name,'(no category)'), t.amount_cents FROM transactions t LEFT JOIN categories c ON c.id=t.category_id
		WHERE t.date>=? AND t.transfer=0 AND t.split=0 AND (t.private=0 OR t.user_id=?)`, start.Format(DateFmt), uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	by := map[string]*MonthRow{}
	var out []MonthRow
	for i := 0; i < months; i++ {
		m := start.AddDate(0, i, 0).Format("2006-01")
		out = append(out, MonthRow{Month: m, ByCategory: map[string]int64{}})
	}
	for i := range out {
		by[out[i].Month] = &out[i]
	}
	for rows.Next() {
		var m, cat string
		var amt int64
		rows.Scan(&m, &cat, &amt)
		r := by[m]
		if r == nil {
			continue
		}
		if amt > 0 {
			r.IncomeCents += amt
		} else {
			r.ExpenseCents += -amt
			r.ByCategory[cat] += -amt
		}
	}
	return out, nil
}

// NetWorthPoint is the total of all visible accounts at a month's end.
type NetWorthPoint struct {
	Month       string `json:"month"`
	AssetsCents int64  `json:"assets_cents"`
	DebtCents   int64  `json:"debt_cents"`
	NetCents    int64  `json:"net_cents"`
}

// NetWorth returns month-end totals for the last n months.
func (s *Store) NetWorth(uid int64, months int) ([]NetWorthPoint, error) {
	if months < 1 || months > 120 {
		months = 24
	}
	accs, err := s.ListAccounts(uid)
	if err != nil {
		return nil, err
	}
	now := s.Now()
	first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local).AddDate(0, -(months - 1), 0)
	var out []NetWorthPoint
	for i := 0; i < months; i++ {
		mStart := first.AddDate(0, i, 0)
		end := mStart.AddDate(0, 1, -1)
		if end.After(now) {
			end = now
		}
		p := NetWorthPoint{Month: mStart.Format("2006-01")}
		for _, a := range accs {
			var sum int64
			s.DB.QueryRow(`SELECT COALESCE(SUM(amount_cents),0) FROM transactions WHERE account_id=? AND date<=?`, a.ID, end.Format(DateFmt)).Scan(&sum)
			b := a.OpeningCents + sum
			if b >= 0 {
				p.AssetsCents += b
			} else {
				p.DebtCents += -b
			}
		}
		p.NetCents = p.AssetsCents - p.DebtCents
		out = append(out, p)
	}
	return out, nil
}

// NamedTotal is a name with an amount and a count.
type NamedTotal struct {
	Name        string `json:"name"`
	AmountCents int64  `json:"amount_cents"`
	Count       int    `json:"count"`
}

// YearSummary is the "year in review".
type YearSummary struct {
	Year          int          `json:"year"`
	IncomeCents   int64        `json:"income_cents"`
	ExpenseCents  int64        `json:"expense_cents"`
	SavingsRate   int          `json:"savings_rate_pct"`
	MonthlyAvg    int64        `json:"monthly_avg_expense_cents"`
	TopCategories []NamedTotal `json:"top_categories"`
	TopPayees     []NamedTotal `json:"top_payees"`
	Biggest       []NamedTotal `json:"biggest"`
	Subscriptions int64        `json:"subscriptions_yearly_cents"`
}

// Year builds the year-in-review summary.
func (s *Store) Year(uid int64, year int) (*YearSummary, error) {
	if year < 2000 || year > 2100 {
		year = s.Now().Year()
	}
	from, to := time.Date(year, 1, 1, 0, 0, 0, 0, time.Local), time.Date(year, 12, 31, 0, 0, 0, 0, time.Local)
	rows, err := s.DB.Query(`SELECT COALESCE(c.name,'(no category)'), t.payee, t.amount_cents, t.date FROM transactions t LEFT JOIN categories c ON c.id=t.category_id
		WHERE t.date>=? AND t.date<=? AND t.transfer=0 AND t.split=0 AND (t.private=0 OR t.user_id=?)`, from.Format(DateFmt), to.Format(DateFmt), uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	y := &YearSummary{Year: year}
	cats, pays := map[string]*NamedTotal{}, map[string]*NamedTotal{}
	var big []NamedTotal
	for rows.Next() {
		var cat, payee, date string
		var amt int64
		rows.Scan(&cat, &payee, &amt, &date)
		if amt > 0 {
			y.IncomeCents += amt
			continue
		}
		y.ExpenseCents += -amt
		if cats[cat] == nil {
			cats[cat] = &NamedTotal{Name: cat}
		}
		cats[cat].AmountCents += -amt
		cats[cat].Count++
		k := subKey(payee)
		if k == "" {
			k = strings.ToLower(payee)
		}
		if pays[k] == nil {
			pays[k] = &NamedTotal{Name: payee}
		}
		pays[k].AmountCents += -amt
		pays[k].Count++
		big = append(big, NamedTotal{Name: payee + " (" + date + ")", AmountCents: -amt, Count: 1})
	}
	top := func(m map[string]*NamedTotal, n int) []NamedTotal {
		var l []NamedTotal
		for _, v := range m {
			l = append(l, *v)
		}
		sort.Slice(l, func(i, j int) bool { return l[i].AmountCents > l[j].AmountCents })
		if len(l) > n {
			l = l[:n]
		}
		if l == nil {
			l = []NamedTotal{}
		}
		return l
	}
	y.TopCategories, y.TopPayees = top(cats, 10), top(pays, 10)
	sort.Slice(big, func(i, j int) bool { return big[i].AmountCents > big[j].AmountCents })
	if len(big) > 5 {
		big = big[:5]
	}
	if big == nil {
		big = []NamedTotal{}
	}
	y.Biggest = big
	months := 12
	if year == s.Now().Year() {
		months = int(s.Now().Month())
	}
	y.MonthlyAvg = y.ExpenseCents / int64(months)
	if y.IncomeCents > 0 {
		y.SavingsRate = int((y.IncomeCents - y.ExpenseCents) * 100 / y.IncomeCents)
	}
	if subs, err := s.Subscriptions(uid); err == nil {
		for _, sub := range subs {
			if sub.Active && sub.Status != "ignored" {
				y.Subscriptions += sub.YearlyCost
			}
		}
	}
	return y, nil
}

// ---------- debt payoff ----------

// Debt is a card or loan in the payoff plan.
type Debt struct {
	AccountID    int64  `json:"account_id"`
	Name         string `json:"name"`
	BalanceCents int64  `json:"balance_cents"` // owed, positive
	APRBps       int64  `json:"apr_bps"`
	MinPayment   int64  `json:"min_payment_cents"`
	PaidOffMonth int    `json:"paid_off_month"` // months from now
	InterestPaid int64  `json:"interest_cents"`
}

// PayoffPlan is the result of one method.
type PayoffPlan struct {
	Method        string `json:"method"` // snowball (smallest balance first) or avalanche (highest rate first)
	Months        int    `json:"months"`
	TotalInterest int64  `json:"total_interest_cents"`
	Order         []Debt `json:"order"`
	Stuck         bool   `json:"stuck"` // payments do not cover the interest
}

// Debts lists cards and loans that are owed (negative balance).
func (s *Store) Debts(uid int64) ([]Debt, error) {
	accs, err := s.ListAccounts(uid)
	if err != nil {
		return nil, err
	}
	out := []Debt{}
	for _, a := range accs {
		if a.Archived || a.BalanceCents >= 0 || (a.Type != "credit" && a.Type != "loan") {
			continue
		}
		out = append(out, Debt{AccountID: a.ID, Name: a.Name, BalanceCents: -a.BalanceCents, APRBps: a.APRBps, MinPayment: a.MinPayment})
	}
	return out, nil
}

// Payoff simulates paying all debts with minimums plus `extra` per month, the extra (and every paid-off
// minimum) going to one debt at a time.
func Payoff(debts []Debt, extra int64, method string) PayoffPlan {
	ds := append([]Debt(nil), debts...)
	switch method {
	case "avalanche":
		sort.SliceStable(ds, func(i, j int) bool { return ds[i].APRBps > ds[j].APRBps })
	default:
		method = "snowball"
		sort.SliceStable(ds, func(i, j int) bool { return ds[i].BalanceCents < ds[j].BalanceCents })
	}
	plan := PayoffPlan{Method: method}
	bal := make([]float64, len(ds))
	var budget int64 = extra
	for i, d := range ds {
		bal[i] = float64(d.BalanceCents)
		budget += d.MinPayment
	}
	for m := 1; m <= 600; m++ {
		left := false
		for i := range bal {
			if bal[i] > 0.5 {
				left = true
			}
		}
		if !left {
			break
		}
		plan.Months = m
		pay := float64(budget)
		// interest first
		for i := range bal {
			if bal[i] <= 0.5 {
				continue
			}
			in := bal[i] * float64(ds[i].APRBps) / 10000 / 12
			bal[i] += in
			ds[i].InterestPaid += int64(math.Round(in))
		}
		// minimums
		for i := range bal {
			if bal[i] <= 0.5 {
				continue
			}
			p := math.Min(bal[i], float64(ds[i].MinPayment))
			bal[i] -= p
			pay -= p
		}
		// the rest to the first unpaid debt in order, then the next
		for i := range bal {
			if pay <= 0 {
				break
			}
			if bal[i] <= 0.5 {
				continue
			}
			p := math.Min(bal[i], pay)
			bal[i] -= p
			pay -= p
		}
		for i := range bal {
			if bal[i] <= 0.5 && ds[i].PaidOffMonth == 0 {
				ds[i].PaidOffMonth = m
				bal[i] = 0
			}
		}
		if m == 600 {
			plan.Stuck = true
		}
	}
	for _, d := range ds {
		plan.TotalInterest += d.InterestPaid
	}
	plan.Order = ds
	return plan
}
