package core

import (
	"testing"
	"time"
)

func TestWhoOwesWhom(t *testing.T) {
	s, mom, _ := setupFin(t)
	dad, _ := s.CreateUser("dad", "Dad", "password1", RoleAdult, "")
	kid, _ := s.CreateUser("kid", "Kid", "password1", RoleAdult, "")
	if sh := SplitEvenly(1000, []int64{1, 2, 3}); sh[0].ShareCents != 334 || sh[2].ShareCents != 333 {
		t.Errorf("split %+v", sh)
	}
	// mom pays 90 dinner for 3; dad pays 30 gas for mom and dad
	s.SaveSharedCost(mom.ID, &SharedCost{Date: "2026-11-20", Description: "Dinner", AmountCents: 9000, PaidBy: mom.ID, Shares: SplitEvenly(9000, []int64{mom.ID, dad.ID, kid.ID})})
	s.SaveSharedCost(dad.ID, &SharedCost{Date: "2026-11-21", Description: "Gas", AmountCents: 3000, PaidBy: dad.ID, Shares: SplitEvenly(3000, []int64{mom.ID, dad.ID})})
	if err := s.SaveSharedCost(mom.ID, &SharedCost{Date: "2026-11-21", Description: "Bad", AmountCents: 100, PaidBy: mom.ID, Shares: []Share{{mom.ID, 50}}}); err == nil {
		t.Error("shares not adding up accepted")
	}
	_, bal, pay, _ := s.SharedCosts()
	got := map[int64]int64{}
	for _, b := range bal {
		got[b.UserID] = b.Cents
	}
	// mom: +9000 -3000 -1500 = 4500; dad: +3000 -3000 -1500 = -1500; kid: -3000
	if got[mom.ID] != 4500 || got[dad.ID] != -1500 || got[kid.ID] != -3000 {
		t.Errorf("balances %v", got)
	}
	if len(pay) != 2 || pay[0].To != mom.ID {
		t.Errorf("payments %+v", pay)
	}
	// kid pays mom back
	s.SaveSharedCost(kid.ID, &SharedCost{Date: "2026-11-22", Description: "Paid back", Kind: "settle", AmountCents: 3000, PaidBy: kid.ID, Shares: []Share{{mom.ID, 3000}}})
	_, bal, _, _ = s.SharedCosts()
	for _, b := range bal {
		if b.UserID == kid.ID {
			t.Errorf("kid still owes %d", b.Cents)
		}
	}
}

func TestRolloverBudget(t *testing.T) {
	s, u, acc := setupFin(t)
	g := catID(s, "Groceries")
	c := Category{ID: *g, Name: "Groceries", Kind: "expense", BudgetCents: 50000}
	s.SaveCategory(&c)
	s.Now = func() time.Time { return at("2026-09-05T10:00") }
	s.SetRollover(*g, true) // from 2026-09
	for _, x := range []Transaction{{Date: "2026-09-10", AmountCents: -40000}, {Date: "2026-10-10", AmountCents: -55000}, {Date: "2026-11-10", AmountCents: -20000}} {
		x := x
		x.AccountID, x.CategoryID = &acc, g
		s.SaveTransaction(u.ID, &x)
	}
	ms, _ := s.Summary(u.ID, "2026-11")
	for _, cs := range ms.Categories {
		if cs.Name == "Groceries" {
			// carry = 2 months x 500 - (400 + 550) = 50; available = 500 + 50 - 200 = 350
			if cs.CarryCents != 5000 || cs.AvailableCents != 35000 {
				t.Errorf("rollover %+v", cs)
			}
		}
	}
}

func TestAnnualAndStatementCheck(t *testing.T) {
	s, u, acc := setupFin(t) // Nov 2026
	s.SaveAnnualCost(u.ID, &AnnualCost{Name: "Car registration", AmountCents: 12000, Month: 3})
	s.SaveAnnualCost(u.ID, &AnnualCost{Name: "Prime", AmountCents: 13900, Month: 12})
	list, per, _ := s.ListAnnualCosts(u.ID)
	if per != 2159 || list[0].Name != "Prime" || list[0].MonthsAway != 1 {
		t.Errorf("annual %d %+v", per, list)
	}
	for _, x := range []Transaction{{Date: "2026-11-01", AmountCents: 100000, Payee: "Pay"}, {Date: "2026-11-05", AmountCents: -2500, Payee: "Coffee Shop"}, {Date: "2026-11-06", AmountCents: -2500, Payee: "COFFEE SHOP"}} {
		x := x
		x.AccountID = &acc
		s.SaveTransaction(u.ID, &x)
	}
	c, err := s.CheckStatement(u.ID, acc, "2026-11-10", 97500, true)
	if err != nil {
		t.Fatal(err)
	}
	if c.AppCents != 95000 || c.DiffCents != 2500 || len(c.Duplicates) != 2 || len(c.MatchSingle) != 2 || len(c.LastChecks) != 1 {
		t.Errorf("check %+v", c)
	}
}
