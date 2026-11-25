package core

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// ---------- mileage log ----------

// Trip is one business drive.
type Trip struct {
	ID      int64   `json:"id"`
	Date    string  `json:"date"`
	From    string  `json:"from"`
	To      string  `json:"to"`
	Purpose string  `json:"purpose"`
	Miles   float64 `json:"miles"`
	Vehicle string  `json:"vehicle"`
	UserID  int64   `json:"user_id"`
}

// ListTrips returns a year's trips, newest first.
func (s *Store) ListTrips(year int) ([]Trip, error) {
	rows, err := s.DB.Query(`SELECT id,date,from_place,to_place,purpose,miles_x10,vehicle,user_id FROM trips WHERE substr(date,1,4)=? ORDER BY date DESC, id DESC`, strconv.Itoa(year))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Trip{}
	for rows.Next() {
		var t Trip
		var m int64
		rows.Scan(&t.ID, &t.Date, &t.From, &t.To, &t.Purpose, &m, &t.Vehicle, &t.UserID)
		t.Miles = float64(m) / 10
		out = append(out, t)
	}
	return out, nil
}

// SaveTrip adds or updates a trip (the IRS wants date, miles and business purpose).
func (s *Store) SaveTrip(uid int64, t *Trip) error {
	if _, err := time.Parse(DateFmt, t.Date); err != nil {
		return errors.New("date must be YYYY-MM-DD")
	}
	t.Purpose = strings.TrimSpace(t.Purpose)
	if t.Purpose == "" {
		return errors.New("write the business purpose (the IRS asks for it)")
	}
	if t.Miles <= 0 || t.Miles > 5000 {
		return errors.New("miles must be between 0 and 5000")
	}
	m := int64(math.Round(t.Miles * 10))
	if t.ID == 0 {
		res, err := s.DB.Exec(`INSERT INTO trips(date,from_place,to_place,purpose,miles_x10,vehicle,user_id,created) VALUES(?,?,?,?,?,?,?,?)`, t.Date, t.From, t.To, t.Purpose, m, t.Vehicle, uid, s.nowStr())
		if err == nil {
			t.ID, _ = res.LastInsertId()
			t.UserID = uid
		}
		return err
	}
	return mustChange(s.DB.Exec(`UPDATE trips SET date=?,from_place=?,to_place=?,purpose=?,miles_x10=?,vehicle=? WHERE id=?`, t.Date, t.From, t.To, t.Purpose, m, t.Vehicle, t.ID))
}

// DeleteTrip removes a trip (kept 30 days in recently deleted).
func (s *Store) DeleteTrip(by, id int64) error {
	var d, p string
	if s.DB.QueryRow(`SELECT date,purpose FROM trips WHERE id=?`, id).Scan(&d, &p) != nil {
		return ErrNotFound
	}
	return s.trashThen("trip", d+" "+p, 0, false, by, []partQuery{{"trips", "id=?", []any{id}}},
		func() error { return mustChange(s.DB.Exec(`DELETE FROM trips WHERE id=?`, id)) })
}

func (s *Store) mileageRate() float64 {
	r, err := strconv.ParseFloat(strings.TrimPrefix(s.Setting("mileage_rate"), "$"), 64)
	if err != nil || r <= 0 || r > 5 {
		return 0.70
	}
	return r
}

// MileageTotal returns a year's business miles and their deduction in cents (miles x the standard rate).
func (s *Store) MileageTotal(year int, upto string) (float64, int64) {
	var m10 int64
	q, args := `SELECT COALESCE(SUM(miles_x10),0) FROM trips WHERE substr(date,1,4)=?`, []any{strconv.Itoa(year)}
	if upto != "" {
		q += ` AND date<=?`
		args = append(args, upto)
	}
	s.DB.QueryRow(q, args...).Scan(&m10)
	miles := float64(m10) / 10
	return miles, int64(math.Round(miles * s.mileageRate() * 100))
}

// ---------- estimated taxes ----------

// TaxQuarter is one estimated-payment period.
type TaxQuarter struct {
	Name           string `json:"name"`
	Through        string `json:"through"` // income counted up to this date
	Due            string `json:"due"`
	ProfitCents    int64  `json:"profit_cents"` // business profit from Jan 1 to Through
	SetAsideCents  int64  `json:"set_aside_cents"`
	PaidCents      int64  `json:"paid_cents"` // estimated payments made by the due date
	SuggestedCents int64  `json:"suggested_cents"`
	Past           bool   `json:"past"`
}

// EstimatedTaxes is the year's plan.
type EstimatedTaxes struct {
	Year         int          `json:"year"`
	RatePct      float64      `json:"rate_pct"`
	ProfitCents  int64        `json:"profit_cents"`
	Miles        float64      `json:"miles"`
	MileageCents int64        `json:"mileage_cents"`
	SetAside     int64        `json:"set_aside_cents"`
	PaidCents    int64        `json:"paid_cents"`
	Quarters     []TaxQuarter `json:"quarters"`
}

// nextBusinessDay moves a weekend due date to Monday.
func nextBusinessDay(t time.Time) time.Time {
	for t.Weekday() == time.Saturday || t.Weekday() == time.Sunday {
		t = t.AddDate(0, 0, 1)
	}
	return t
}

// taxQuarters are the IRS estimated-tax periods and due dates for a year.
func taxQuarters(y int) [][3]time.Time {
	d := func(yy int, m time.Month, dd int) time.Time { return time.Date(yy, m, dd, 0, 0, 0, 0, time.Local) }
	return [][3]time.Time{
		{d(y, 1, 1), d(y, 3, 31), nextBusinessDay(d(y, 4, 15))},
		{d(y, 4, 1), d(y, 5, 31), nextBusinessDay(d(y, 6, 15))},
		{d(y, 6, 1), d(y, 8, 31), nextBusinessDay(d(y, 9, 15))},
		{d(y, 9, 1), d(y, 12, 31), nextBusinessDay(d(y+1, 1, 15))},
	}
}

// businessProfit is business income minus business expenses (and mileage) from Jan 1 to `to`.
func (s *Store) businessProfit(uid int64, year int, to string) int64 {
	var inc, exp int64
	s.DB.QueryRow(`SELECT COALESCE(SUM(CASE WHEN amount_cents>0 THEN amount_cents END),0), COALESCE(SUM(CASE WHEN amount_cents<0 THEN -amount_cents END),0)
		FROM transactions WHERE business=1 AND transfer=0 AND split=0 AND date>=? AND date<=? AND (private=0 OR user_id=?)`,
		fmt.Sprintf("%d-01-01", year), to, uid).Scan(&inc, &exp)
	_, mileage := s.MileageTotal(year, to)
	return inc - exp - mileage
}

// EstimatedTaxPlan suggests how much to set aside and pay each quarter. It is a simple percentage of business
// profit (the user sets the percent), not a tax calculation.
func (s *Store) EstimatedTaxPlan(uid int64, year int) (*EstimatedTaxes, error) {
	if year < 2000 || year > 2100 {
		year = s.Now().Year()
	}
	rate, _ := strconv.ParseFloat(s.Setting("est_tax_rate"), 64)
	if rate <= 0 || rate > 80 {
		rate = 25
	}
	now := s.Now()
	e := &EstimatedTaxes{Year: year, RatePct: rate}
	end := fmt.Sprintf("%d-12-31", year)
	if now.Year() == year {
		end = now.Format(DateFmt)
	}
	e.ProfitCents = s.businessProfit(uid, year, end)
	e.Miles, e.MileageCents = s.MileageTotal(year, end)
	if e.ProfitCents > 0 {
		e.SetAside = int64(math.Round(float64(e.ProfitCents) * rate / 100))
	}
	paidBy := func(to string) int64 {
		var p int64
		s.DB.QueryRow(`SELECT COALESCE(SUM(-t.amount_cents),0) FROM transactions t JOIN categories c ON c.id=t.category_id
			WHERE c.name='Estimated tax payments' AND t.amount_cents<0 AND t.split=0 AND t.date>=? AND t.date<=? AND (t.private=0 OR t.user_id=?)`,
			fmt.Sprintf("%d-01-15", year), to, uid).Scan(&p)
		return p
	}
	e.PaidCents = paidBy(fmt.Sprintf("%d-01-31", year+1))
	for i, q := range taxQuarters(year) {
		through := q[1].Format(DateFmt)
		if through > end {
			through = end
		}
		tq := TaxQuarter{Name: fmt.Sprintf("Q%d", i+1), Through: q[1].Format(DateFmt), Due: q[2].Format(DateFmt), Past: now.After(q[2].AddDate(0, 0, 1))}
		if q[0].Format(DateFmt) <= end {
			tq.ProfitCents = s.businessProfit(uid, year, through)
		}
		if tq.ProfitCents > 0 {
			tq.SetAsideCents = int64(math.Round(float64(tq.ProfitCents) * rate / 100))
		}
		tq.PaidCents = paidBy(tq.Due)
		if d := tq.SetAsideCents - tq.PaidCents; d > 0 {
			tq.SuggestedCents = d
		}
		e.Quarters = append(e.Quarters, tq)
	}
	return e, nil
}

// estTaxReminders reminds admins 7 days and 1 day before each estimated-tax due date when business money exists.
func (s *Store) estTaxReminders(now time.Time, send Sender, cfg map[string]string) {
	if cfg["est_tax_reminders"] != "1" {
		return
	}
	hour, _ := strconv.Atoi(cfg["bill_remind_hour"])
	for _, y := range []int{now.Year() - 1, now.Year()} {
		for i, q := range taxQuarters(y) {
			for _, days := range []int{7, 1} {
				d := q[2].AddDate(0, 0, -days)
				fire := time.Date(d.Year(), d.Month(), d.Day(), hour, 0, 0, 0, time.Local)
				if now.Before(fire) || now.Sub(fire) > 20*time.Hour {
					continue
				}
				plan, _ := s.EstimatedTaxPlan(s.firstAdmin(), y)
				if plan == nil || plan.Quarters[i].SuggestedCents <= 0 {
					continue
				}
				if !s.claim("esttax", int64(y*10+i+1), q[2].Format(DateFmt), strconv.Itoa(days), 0) {
					continue
				}
				cur := cfg["currency"]
				m := Message{Title: fmt.Sprintf("Estimated tax Q%d due %s", i+1, whenText(now, q[2], true)),
					Body: fmt.Sprintf("Suggested payment about %s%s (%v%% of business profit, minus what you paid). Pay at irs.gov/payments; check the amount with your tax preparer.", cur, FormatCents(plan.Quarters[i].SuggestedCents), plan.RatePct)}
				users, _ := s.ListUsers()
				for _, u := range users {
					if u.Active && u.Role == RoleAdmin {
						s.notify(send, u, m)
					}
				}
			}
		}
	}
}
