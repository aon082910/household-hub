package core

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ---------- recurring income (paychecks, VA payments) ----------

// Income is money that arrives on a schedule. It only feeds the forecast; nothing is recorded by itself.
type Income struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	AmountCents int64  `json:"amount_cents"`
	AccountID   *int64 `json:"account_id"`
	StartDate   string `json:"start_date"`
	Freq        string `json:"freq"`
	Interval    int    `json:"interval"`
	Until       string `json:"until"`
	OwnerID     int64  `json:"owner_id"`
	Private     bool   `json:"private"`
	Active      bool   `json:"active"`
}

// ListIncomes returns visible income schedules.
func (s *Store) ListIncomes(uid int64) ([]Income, error) {
	rows, err := s.DB.Query(`SELECT id,name,amount_cents,account_id,start_date,freq,interval,until,owner_id,private,active FROM incomes WHERE `+visible+` ORDER BY name`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Income{}
	for rows.Next() {
		var in Income
		var a sql.NullInt64
		var p, act int
		rows.Scan(&in.ID, &in.Name, &in.AmountCents, &a, &in.StartDate, &in.Freq, &in.Interval, &in.Until, &in.OwnerID, &p, &act)
		in.AccountID, in.Private, in.Active = idPtr(a), p == 1, act == 1
		out = append(out, in)
	}
	return out, nil
}

// SaveIncome adds or updates an income schedule.
func (s *Store) SaveIncome(uid int64, in *Income) error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || in.AmountCents <= 0 {
		return errors.New("name and a positive amount are required")
	}
	if _, err := time.Parse(DateFmt, in.StartDate); err != nil {
		return errors.New("next pay date must be YYYY-MM-DD")
	}
	if in.Freq == "none" {
		in.Freq = "once"
	}
	if !Freqs[in.Freq] {
		return errors.New("bad repeat")
	}
	if in.Interval < 1 {
		in.Interval = 1
	}
	if in.ID == 0 {
		res, err := s.DB.Exec(`INSERT INTO incomes(name,amount_cents,account_id,start_date,freq,interval,until,owner_id,private,active) VALUES(?,?,?,?,?,?,?,?,?,1)`,
			in.Name, in.AmountCents, nullID(in.AccountID), in.StartDate, in.Freq, in.Interval, in.Until, uid, b2i(in.Private))
		if err == nil {
			in.ID, _ = res.LastInsertId()
			in.Active = true
		}
		return err
	}
	return mustChange(s.DB.Exec(`UPDATE incomes SET name=?,amount_cents=?,account_id=?,start_date=?,freq=?,interval=?,until=?,private=?,active=? WHERE id=? AND `+visible,
		in.Name, in.AmountCents, nullID(in.AccountID), in.StartDate, in.Freq, in.Interval, in.Until, b2i(in.Private), b2i(in.Active), in.ID, uid))
}

// DeleteIncome removes an income schedule.
func (s *Store) DeleteIncome(uid, id int64) error {
	return mustChange(s.DB.Exec(`DELETE FROM incomes WHERE id=? AND `+visible, id, uid))
}

// ---------- subscriptions found in the transactions ----------

// Subscription is a charge that repeats on a schedule.
type Subscription struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Period      string `json:"period"` // weekly, biweekly, monthly, quarterly, yearly
	Days        int    `json:"days"`
	AmountCents int64  `json:"amount_cents"` // last charge, positive
	MonthlyCost int64  `json:"monthly_cents"`
	YearlyCost  int64  `json:"yearly_cents"`
	LastDate    string `json:"last_date"`
	NextDate    string `json:"next_date"`
	Count       int    `json:"count"`
	AccountID   *int64 `json:"account_id"`
	CategoryID  *int64 `json:"category_id"`
	Active      bool   `json:"active"`
	OldCents    int64  `json:"old_cents,omitempty"` // previous price when it changed
	ChangedOn   string `json:"changed_on,omitempty"`
	BillID      *int64 `json:"bill_id"`
	Status      string `json:"status"` // "", ignored
}

var reSubNoise = regexp.MustCompile(`[^a-z ]+`)

// subKey turns "NETFLIX.COM 866-579-7172 CA" and "Netflix" into the same key.
func subKey(payee string) string {
	p := strings.ToLower(payee)
	p = strings.NewReplacer("*", " ", ".com", " ", ".", " ", "#", " ", "-", " ", "_", " ").Replace(p)
	p = reSubNoise.ReplaceAllString(p, " ")
	f := strings.Fields(p)
	var keep []string
	for _, w := range f {
		if len(w) < 2 || w == "www" || w == "pos" || w == "debit" || w == "purchase" || w == "ach" || w == "recurring" || w == "payment" {
			continue
		}
		keep = append(keep, w)
		if len(keep) == 2 {
			break
		}
	}
	return strings.Join(keep, " ")
}

var periods = []struct {
	name     string
	days     int
	min, max float64
	minCount int
}{
	{"weekly", 7, 5, 9, 3},
	{"biweekly", 14, 12, 16, 3},
	{"monthly", 30, 26, 35, 3},
	{"quarterly", 91, 80, 100, 2},
	{"yearly", 365, 350, 380, 2},
}

func median(v []float64) float64 {
	c := append([]float64(nil), v...)
	sort.Float64s(c)
	n := len(c)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return c[n/2]
	}
	return (c[n/2-1] + c[n/2]) / 2
}

// Subscriptions finds repeating charges in the last 400 days of visible spending.
func (s *Store) Subscriptions(uid int64) ([]Subscription, error) {
	now := s.Now()
	txs, err := s.ListTransactions(uid, TxFilter{From: now.AddDate(0, 0, -400).Format(DateFmt), Limit: 5000})
	if err != nil {
		return nil, err
	}
	groups := map[string][]Transaction{}
	names := map[string]string{}
	for _, t := range txs {
		if t.AmountCents >= 0 || t.Transfer {
			continue
		}
		k := subKey(t.Payee)
		if k == "" {
			continue
		}
		groups[k] = append(groups[k], t)
		if names[k] == "" {
			names[k] = t.Payee
		}
	}
	state := map[string]string{}
	stBill := map[string]*int64{}
	rows, _ := s.DB.Query(`SELECT key,status,bill_id FROM subscription_state`)
	if rows != nil {
		for rows.Next() {
			var k, st string
			var b sql.NullInt64
			rows.Scan(&k, &st, &b)
			state[k], stBill[k] = st, idPtr(b)
		}
		rows.Close()
	}
	bills, _ := s.ListBills(uid)
	var out []Subscription
	for k, list := range groups {
		sort.Slice(list, func(i, j int) bool { return list[i].Date < list[j].Date })
		// one charge per day at most (a refund-and-recharge counts once)
		var dates []time.Time
		var amts []int64
		for _, t := range list {
			d, _ := time.Parse(DateFmt, t.Date)
			if len(dates) > 0 && d.Equal(dates[len(dates)-1]) {
				continue
			}
			dates, amts = append(dates, d), append(amts, -t.AmountCents)
		}
		if len(dates) < 2 {
			continue
		}
		var gaps []float64
		for i := 1; i < len(dates); i++ {
			gaps = append(gaps, dates[i].Sub(dates[i-1]).Hours()/24)
		}
		med := median(gaps)
		for _, p := range periods {
			if med < p.min || med > p.max || len(dates) < p.minCount {
				continue
			}
			regular := 0
			for _, g := range gaps {
				if g >= p.min && g <= p.max {
					regular++
				}
			}
			if float64(regular) < math.Ceil(float64(len(gaps))*0.7) {
				break
			}
			last := dates[len(dates)-1]
			sub := Subscription{Key: k, Name: names[k], Period: p.name, Days: p.days, AmountCents: amts[len(amts)-1], LastDate: last.Format(DateFmt),
				NextDate: last.AddDate(0, 0, p.days).Format(DateFmt), Count: len(dates), Status: state[k], BillID: stBill[k]}
			if p.name == "monthly" {
				sub.NextDate = last.AddDate(0, 1, 0).Format(DateFmt)
			} else if p.name == "yearly" {
				sub.NextDate = last.AddDate(1, 0, 0).Format(DateFmt)
			}
			sub.Active = now.Sub(last).Hours()/24 <= float64(p.days)*1.6
			sub.MonthlyCost = int64(math.Round(float64(sub.AmountCents) * 30.44 / float64(p.days)))
			sub.YearlyCost = int64(math.Round(float64(sub.AmountCents) * 365 / float64(p.days)))
			prev := amts[len(amts)-2]
			if diff := sub.AmountCents - prev; diff != 0 && abs(diff) >= 50 && float64(abs(diff)) >= float64(prev)*0.01 {
				sub.OldCents, sub.ChangedOn = prev, sub.LastDate
			}
			lt := list[len(list)-1]
			sub.AccountID, sub.CategoryID = lt.AccountID, lt.CategoryID
			if sub.BillID == nil {
				for _, b := range bills {
					if billMatches(b.Name, sub.Name) {
						id := b.ID
						sub.BillID = &id
						break
					}
				}
			}
			out = append(out, sub)
			break
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Active != out[j].Active {
			return out[i].Active
		}
		return out[i].MonthlyCost > out[j].MonthlyCost
	})
	if out == nil {
		out = []Subscription{}
	}
	return out, nil
}

// SetSubscription marks a found subscription ignored ("ignored"), back to normal (""), or links it to a bill.
func (s *Store) SetSubscription(key, status string, billID *int64) error {
	if status == "" && billID == nil {
		_, err := s.DB.Exec(`DELETE FROM subscription_state WHERE key=?`, key)
		return err
	}
	_, err := s.DB.Exec(`INSERT INTO subscription_state(key,status,bill_id) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET status=excluded.status, bill_id=excluded.bill_id`, key, status, nullID(billID))
	return err
}

// SubscriptionToBill makes a bill from a found subscription (amount, next date, schedule, account, category).
func (s *Store) SubscriptionToBill(uid int64, key string) (*Bill, error) {
	subs, err := s.Subscriptions(uid)
	if err != nil {
		return nil, err
	}
	for _, sub := range subs {
		if sub.Key != key {
			continue
		}
		b := Bill{Name: sub.Name, AmountCents: sub.AmountCents, StartDate: sub.NextDate, Freq: "monthly", Interval: 1, RemindDays: "1",
			AccountID: sub.AccountID, CategoryID: sub.CategoryID, Autopay: true, Notes: "Made from a found subscription"}
		switch sub.Period {
		case "weekly":
			b.Freq = "weekly"
		case "biweekly":
			b.Freq, b.Interval = "weekly", 2
		case "quarterly":
			b.Interval = 3
		case "yearly":
			b.Freq = "yearly"
		}
		if err := s.SaveBill(uid, &b); err != nil {
			return nil, err
		}
		s.SetSubscription(key, "bill", &b.ID)
		return &b, nil
	}
	return nil, ErrNotFound
}

// ---------- cash-flow forecast ----------

// ForecastEvent is one expected money movement.
type ForecastEvent struct {
	Date         string `json:"date"`
	Name         string `json:"name"`
	Kind         string `json:"kind"` // bill, income, subscription
	AmountCents  int64  `json:"amount_cents"`
	BalanceCents int64  `json:"balance_cents"`
}

// ForecastPoint is the balance at the end of a day.
type ForecastPoint struct {
	Date         string `json:"date"`
	BalanceCents int64  `json:"balance_cents"`
}

// AccountForecast is one account's expected path.
type AccountForecast struct {
	AccountID   int64           `json:"account_id"`
	Name        string          `json:"name"`
	StartCents  int64           `json:"start_cents"`
	LowestCents int64           `json:"lowest_cents"`
	LowestDate  string          `json:"lowest_date"`
	EndCents    int64           `json:"end_cents"`
	LowBalance  *int64          `json:"low_balance_cents"`
	SafeToSpend int64           `json:"safe_to_spend_cents"`
	Warning     bool            `json:"warning"`
	Events      []ForecastEvent `json:"events"`
	Points      []ForecastPoint `json:"points"`
}

// Forecast projects every visible, open account `days` ahead from today using unpaid bills, income schedules
// and found subscriptions that are not bills yet. Bills and income without an account go to defaultAcc (the first
// checking account when 0).
func (s *Store) Forecast(uid int64, days int) ([]AccountForecast, []string, error) {
	if days <= 0 || days > 370 {
		days = 60
	}
	now := s.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	end := today.AddDate(0, 0, days)
	accs, err := s.ListAccounts(uid)
	if err != nil {
		return nil, nil, err
	}
	var notes []string
	byID := map[int64]*AccountForecast{}
	var order []int64
	var defAcc int64
	if v, _ := strconv.ParseInt(s.Setting("forecast_default_account"), 10, 64); v != 0 {
		defAcc = v
	}
	for _, a := range accs {
		if a.Archived {
			continue
		}
		f := &AccountForecast{AccountID: a.ID, Name: a.Name, StartCents: a.BalanceCents, LowBalance: a.LowBalance}
		byID[a.ID] = f
		order = append(order, a.ID)
		if defAcc == 0 && a.Type == "checking" {
			defAcc = a.ID
		}
	}
	if defAcc == 0 && len(order) > 0 {
		defAcc = order[0]
	}
	if byID[defAcc] == nil && len(order) > 0 {
		defAcc = order[0]
	}
	add := func(acc *int64, ev ForecastEvent) {
		id := defAcc
		if acc != nil && byID[*acc] != nil {
			id = *acc
		}
		if f := byID[id]; f != nil {
			f.Events = append(f.Events, ev)
		}
	}
	unassigned := 0
	dues, err := s.BillDues(uid, today.AddDate(0, 0, -45), end)
	if err != nil {
		return nil, nil, err
	}
	bills, _ := s.ListBills(uid)
	billAcc := map[int64]*int64{}
	for _, b := range bills {
		billAcc[b.ID] = b.AccountID
	}
	for _, d := range dues {
		if d.Paid {
			continue
		}
		date := d.Date
		if d.Overdue { // still owed: expect it today
			date = today.Format(DateFmt)
		}
		if billAcc[d.BillID] == nil {
			unassigned++
		}
		add(billAcc[d.BillID], ForecastEvent{Date: date, Name: d.Name, Kind: "bill", AmountCents: -d.AmountCents})
	}
	incs, _ := s.ListIncomes(uid)
	for _, in := range incs {
		if !in.Active {
			continue
		}
		st, err := ParseLocal(in.StartDate)
		if err != nil {
			continue
		}
		for _, t := range Occurrences(st, in.Freq, in.Interval, in.Until, today, end) {
			add(in.AccountID, ForecastEvent{Date: t.Format(DateFmt), Name: in.Name, Kind: "income", AmountCents: in.AmountCents})
		}
	}
	subs, _ := s.Subscriptions(uid)
	for _, sub := range subs {
		if !sub.Active || sub.BillID != nil || sub.Status == "ignored" {
			continue
		}
		d, _ := time.ParseInLocation(DateFmt, sub.NextDate, time.Local)
		for i := 0; !d.After(end) && i < 60; i++ {
			if !d.Before(today) {
				add(sub.AccountID, ForecastEvent{Date: d.Format(DateFmt), Name: sub.Name, Kind: "subscription", AmountCents: -sub.AmountCents})
			}
			switch sub.Period {
			case "monthly":
				d = d.AddDate(0, 1, 0)
			case "yearly":
				d = d.AddDate(1, 0, 0)
			default:
				d = d.AddDate(0, 0, sub.Days)
			}
		}
	}
	if unassigned > 0 && defAcc != 0 {
		notes = append(notes, fmt.Sprintf("%d bill due date(s) have no account; they are counted in %s.", unassigned, byID[defAcc].Name))
	}
	out := []AccountForecast{}
	for _, id := range order {
		f := byID[id]
		// same day: money out before money in, so the low point is not hidden by a same-day deposit
		sort.SliceStable(f.Events, func(i, j int) bool {
			if f.Events[i].Date != f.Events[j].Date {
				return f.Events[i].Date < f.Events[j].Date
			}
			return f.Events[i].AmountCents < f.Events[j].AmountCents
		})
		bal := f.StartCents
		f.LowestCents, f.LowestDate = bal, today.Format(DateFmt)
		ei := 0
		for d := today; !d.After(end); d = d.AddDate(0, 0, 1) {
			ds := d.Format(DateFmt)
			for ei < len(f.Events) && f.Events[ei].Date <= ds {
				bal += f.Events[ei].AmountCents
				f.Events[ei].BalanceCents = bal
				ei++
				if bal < f.LowestCents {
					f.LowestCents, f.LowestDate = bal, ds
				}
			}
			f.Points = append(f.Points, ForecastPoint{Date: ds, BalanceCents: bal})
			if bal < f.LowestCents {
				f.LowestCents, f.LowestDate = bal, ds
			}
		}
		f.EndCents = bal
		floor := int64(0)
		if f.LowBalance != nil {
			floor = *f.LowBalance
		}
		f.SafeToSpend = f.LowestCents - floor
		if f.SafeToSpend < 0 {
			f.SafeToSpend = 0
		}
		f.Warning = f.LowBalance != nil && f.LowestCents < *f.LowBalance
		if f.Events == nil {
			f.Events = []ForecastEvent{}
		}
		out = append(out, *f)
	}
	return out, notes, nil
}

// ---------- backups ----------

// BackupDir is where automatic backups go (next to the database).
func (s *Store) BackupDir() string { return filepath.Join(filepath.Dir(s.Path), "backups") }

// BackupFile is one saved backup.
type BackupFile struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	Saved string `json:"saved"`
}

// RunBackup writes household-YYYY-MM-DD.db (a consistent copy) and keeps the newest `keep` copies.
func (s *Store) RunBackup(keep int) (string, error) {
	dir := s.BackupDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := "household-" + s.Now().Format("2006-01-02") + ".db"
	dst := filepath.Join(dir, name)
	tmp := dst + ".tmp"
	os.Remove(tmp)
	if err := s.Backup(tmp); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(dst)
		if err := os.Rename(tmp, dst); err != nil {
			return "", err
		}
	}
	if keep < 1 {
		keep = 30
	}
	if err := copyFilesDir(filepath.Join(filepath.Dir(s.Path), "files"), filepath.Join(dir, "files"), keep); err != nil {
		return name, fmt.Errorf("database saved, attachments not: %w", err)
	}
	list, _ := s.ListBackups()
	for i := keep; i < len(list); i++ {
		os.Remove(filepath.Join(dir, list[i].Name))
	}
	return name, nil
}

// copyFilesDir mirrors new attachment files (receipts, documents) into the backup folder. Files never change
// after upload, so only missing ones are copied. A file deleted in the app is removed from the backup copy once
// it is older than the keep window (older database backups may still point at it until then).
func copyFilesDir(src, dst string, keepDays int) error {
	if _, err := os.Stat(src); err != nil {
		return nil
	}
	cut := time.Now().AddDate(0, 0, -keepDays)
	filepath.WalkDir(dst, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dst, p)
		if _, err := os.Stat(filepath.Join(src, rel)); os.IsNotExist(err) {
			if info, err := d.Info(); err == nil && info.ModTime().Before(cut) {
				os.Remove(p)
			}
		}
		return nil
	})
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		out := filepath.Join(dst, rel)
		if _, err := os.Stat(out); err == nil {
			return nil
		}
		os.MkdirAll(filepath.Dir(out), 0o755)
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(out, b, 0o644)
	})
}

// ListBackups lists automatic backups, newest first.
func (s *Store) ListBackups() ([]BackupFile, error) {
	ents, err := os.ReadDir(s.BackupDir())
	out := []BackupFile{}
	if err != nil {
		return out, nil
	}
	for _, e := range ents {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "household-") || !strings.HasSuffix(e.Name(), ".db") {
			continue
		}
		info, _ := e.Info()
		out = append(out, BackupFile{Name: e.Name(), Size: info.Size(), Saved: info.ModTime().Format(time.RFC3339)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name > out[j].Name })
	return out, nil
}

// ---------- one-tap actions from phone notifications ----------

// NewActionToken makes a single-use link that marks one bill due date paid. It carries no login.
func (s *Store) NewActionToken(billID int64, occ string, userID int64) string {
	t := randToken(24)
	exp := s.Now().AddDate(0, 0, 21).Format(time.RFC3339)
	s.DB.Exec(`INSERT INTO action_tokens(token_hash,kind,bill_id,occ,user_id,expires) VALUES(?,?,?,?,?,?)`, hashToken(t), "pay", billID, occ, userID, exp)
	return t
}

// UseActionToken runs a one-tap action. A used token answers "already done" and changes nothing.
func (s *Store) UseActionToken(t string) (string, error) {
	var kind, occ, exp, used string
	var bill, uid int64
	err := s.DB.QueryRow(`SELECT kind,bill_id,occ,user_id,expires,used FROM action_tokens WHERE token_hash=?`, hashToken(t)).Scan(&kind, &bill, &occ, &uid, &exp, &used)
	if err != nil {
		return "", ErrNotFound
	}
	if used != "" {
		return "Already done.", nil
	}
	if e, _ := time.Parse(time.RFC3339, exp); s.Now().After(e) {
		return "", errors.New("this link has expired; mark it paid in the app")
	}
	u, err := s.GetUser(uid)
	if err != nil || !u.Active || !u.CanFinance() {
		return "", ErrForbidden
	}
	b, err := s.GetBill(uid, bill)
	if err != nil {
		return "", err
	}
	if err := s.PayBill(uid, bill, occ, "", 0, true); err != nil {
		return "", err
	}
	s.DB.Exec(`UPDATE action_tokens SET used=? WHERE token_hash=?`, s.nowStr(), hashToken(t))
	s.DB.Exec(`DELETE FROM action_tokens WHERE expires<?`, s.nowStr())
	return fmt.Sprintf("%s (due %s) marked paid.", b.Name, occ), nil
}

// ---------- daily checks: backups, subscription price changes, low balance ----------

// DailyChecks runs once per day after the configured hour (called by the reminder engine every minute).
func (s *Store) DailyChecks(now time.Time, send Sender) {
	cfg := s.Settings(false)
	hour, _ := strconv.Atoi(cfg["backup_hour"])
	day := now.Format(DateFmt)
	if now.Hour() >= hour && cfg["backup_keep"] != "0" && s.claim("backup", 0, day, "", 0) {
		keep, _ := strconv.Atoi(cfg["backup_keep"])
		if name, err := s.RunBackup(keep); err != nil {
			s.alertAdmins(send, Message{Title: "Household Hub backup failed", Body: err.Error()})
		} else {
			s.SaveSettings(map[string]string{"last_backup": name})
		}
	}
	bh, _ := strconv.Atoi(cfg["bill_remind_hour"])
	if now.Hour() < bh || send == nil {
		return
	}
	s.docReminders(now, send, cfg)
	// the heavier checks below run once an hour, not every minute
	if !s.claim("moneyscan", 0, now.Format("2006-01-02T15"), "", 0) {
		return
	}
	cur := cfg["currency"]
	users, _ := s.ListUsers()
	for _, u := range users {
		if !u.Active || !u.CanFinance() {
			continue
		}
		// each adult is told only about what they can see (their own private accounts stay theirs)
		subs, _ := s.Subscriptions(u.ID)
		for _, sub := range subs {
			if sub.OldCents == 0 || sub.Status == "ignored" {
				continue
			}
			if d, err := time.Parse(DateFmt, sub.ChangedOn); err != nil || now.Sub(d) > 5*24*time.Hour {
				continue
			}
			if s.claim("subprice", 0, sub.Key+"|"+sub.ChangedOn, "", u.ID) {
				s.notify(send, u, Message{Title: fmt.Sprintf("Price change: %s %s%s → %s%s", sub.Name, cur, FormatCents(sub.OldCents), cur, FormatCents(sub.AmountCents)),
					Body: fmt.Sprintf("About %s%s a year now.", cur, FormatCents(sub.YearlyCost))})
			}
		}
		fc, _, _ := s.Forecast(u.ID, 14)
		for _, f := range fc {
			if !f.Warning {
				continue
			}
			if s.claim("lowbal", f.AccountID, f.LowestDate, "", u.ID) {
				d, _ := ParseLocal(f.LowestDate)
				s.notify(send, u, Message{Title: fmt.Sprintf("%s may drop to %s%s on %s", f.Name, cur, FormatCents(f.LowestCents), d.Format("Mon Jan 2")),
					Body: fmt.Sprintf("Your warning level is %s%s. See Plan > Forecast.", cur, FormatCents(*f.LowBalance))})
			}
		}
	}
}

func (s *Store) alertAdults(send Sender, m Message) {
	users, _ := s.ListUsers()
	for _, u := range users {
		if u.Active && u.CanFinance() {
			s.notify(send, u, m)
		}
	}
}

func (s *Store) alertAdmins(send Sender, m Message) {
	if send == nil {
		return
	}
	users, _ := s.ListUsers()
	for _, u := range users {
		if u.Active && u.Role == RoleAdmin {
			s.notify(send, u, m)
		}
	}
}
