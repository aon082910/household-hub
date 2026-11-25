package core

import (
	"testing"
	"time"
)

func TestGoals(t *testing.T) {
	s, u, _ := setupFin(t) // 2026-11-25
	sav := Account{Name: "Savings", Type: "savings", OpeningCents: 30000}
	s.SaveAccount(u.ID, &sav)
	g1 := Goal{Name: "Car insurance", TargetCents: 60000, TargetDate: "2027-05-25"}
	s.SaveGoal(u.ID, &g1)
	s.AddToGoal(u.ID, g1.ID, 12000, "")
	g2 := Goal{Name: "Emergency", TargetCents: 100000, AccountID: &sav.ID}
	s.SaveGoal(u.ID, &g2)
	if err := s.AddToGoal(u.ID, g2.ID, 100, ""); err == nil {
		t.Error("added by hand to an account-linked goal")
	}
	gs, _ := s.ListGoals(u.ID)
	by := map[string]Goal{}
	for _, g := range gs {
		by[g.Name] = g
	}
	if g := by["Car insurance"]; g.CurrentCents != 12000 || g.MonthsLeft != 6 || g.PerMonth != 8000 || g.ProgressPct != 20 {
		t.Errorf("car %+v", g)
	}
	if g := by["Emergency"]; g.CurrentCents != 30000 || g.ProgressPct != 30 {
		t.Errorf("emergency %+v", g)
	}
}

func TestTrendsNetWorthYear(t *testing.T) {
	s, u, acc := setupFin(t)
	card := Account{Name: "Card", Type: "credit", OpeningCents: -50000}
	s.SaveAccount(u.ID, &card)
	for _, x := range []Transaction{
		{Date: "2026-10-05", AmountCents: 300000, Payee: "Pay"},
		{Date: "2026-10-06", AmountCents: -20000, Payee: "Grocer", CategoryID: catID(s, "Groceries")},
		{Date: "2026-11-06", AmountCents: -25000, Payee: "Grocer", CategoryID: catID(s, "Groceries")},
		{Date: "2026-11-07", AmountCents: -100000, Payee: "To savings", Transfer: true},
	} {
		x := x
		x.AccountID = &acc
		s.SaveTransaction(u.ID, &x)
	}
	tr, _ := s.Trends(u.ID, 3)
	if len(tr) != 3 || tr[1].Month != "2026-10" || tr[1].IncomeCents != 300000 || tr[2].ExpenseCents != 25000 || tr[2].ByCategory["Groceries"] != 25000 {
		t.Errorf("trends %+v", tr)
	}
	nw, _ := s.NetWorth(u.ID, 2)
	// Oct end: wallet 2800, card -500 -> 2300; Nov: wallet 2800-250-1000=1550, card -500 -> 1050
	if nw[0].NetCents != 230000 || nw[1].NetCents != 105000 || nw[1].DebtCents != 50000 {
		t.Errorf("net worth %+v", nw)
	}
	y, _ := s.Year(u.ID, 2026)
	if y.IncomeCents != 300000 || y.ExpenseCents != 45000 || y.TopCategories[0].Name != "Groceries" || y.SavingsRate != 85 {
		t.Errorf("year %+v", y)
	}
}

func TestPayoff(t *testing.T) {
	debts := []Debt{
		{Name: "Store card", BalanceCents: 50000, APRBps: 2499, MinPayment: 2500},
		{Name: "Visa", BalanceCents: 300000, APRBps: 1999, MinPayment: 9000},
		{Name: "Car loan", BalanceCents: 800000, APRBps: 699, MinPayment: 25000},
	}
	sb := Payoff(debts, 20000, "snowball")
	av := Payoff(debts, 20000, "avalanche")
	if sb.Order[0].Name != "Store card" || av.Order[0].Name != "Store card" {
		t.Errorf("order %v / %v", sb.Order[0].Name, av.Order[0].Name)
	}
	if sb.Stuck || av.Stuck || sb.Months < 10 || sb.Months > 40 {
		t.Errorf("months %d %d", sb.Months, av.Months)
	}
	if av.TotalInterest > sb.TotalInterest {
		t.Errorf("avalanche should not cost more interest: %d vs %d", av.TotalInterest, sb.TotalInterest)
	}
	for _, d := range sb.Order {
		if d.PaidOffMonth == 0 {
			t.Errorf("%s never paid off", d.Name)
		}
	}
	stuck := Payoff([]Debt{{Name: "X", BalanceCents: 1000000, APRBps: 3000, MinPayment: 1000}}, 0, "snowball")
	if !stuck.Stuck {
		t.Error("payment below interest not flagged")
	}
	_ = time.Now
}
