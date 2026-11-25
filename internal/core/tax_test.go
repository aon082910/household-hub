package core

import (
	"strings"
	"testing"
)

// made-up exports shaped like Etsy's and Stripe's
const etsyCSV = `Date,Type,Title,Info,Currency,Amount,"Fees & Taxes",Net,"Tax Details"
"November 20, 2026",Sale,"Payment for Order #1001","Order #1001",USD,$12.00,-$1.08,$10.92,--
"November 20, 2026",Fee,"Listing fee","Listing #55",USD,--,-$0.20,-$0.20,--
"November 21, 2026",Marketing,"Etsy Ads","Ads",USD,--,-$1.50,-$1.50,--
"November 22, 2026",Deposit,"$50.00 sent to your bank account",,USD,--,--,-$50.00,--
`

const stripeCSV = `id,Type,Source,Amount,Fee,Net,Currency,"Created (UTC)",Description
txn_1,charge,ch_1,25.00,1.03,23.97,usd,2026-11-18 14:00,Template pack
txn_2,payout,po_1,-23.97,0.00,-23.97,usd,2026-11-20 09:00,STRIPE PAYOUT
`

func TestPlatformImports(t *testing.T) {
	s, u, _ := setupFin(t)
	etsy := Account{Name: "Etsy balance", Type: "other"}
	s.SaveAccount(u.ID, &etsy)
	p, err := ParseStatement([]byte(etsyCSV), nil)
	if err != nil || p.Mapping.Profile != "platform" {
		t.Fatalf("etsy %v %+v", err, p)
	}
	c, err := s.CommitImport(u.ID, etsy.ID, p, false)
	if err != nil || c["added"] != 5 { // sale + its fee + listing fee + ads + deposit
		t.Fatalf("etsy commit %v %v %+v", c, err, p.Rows)
	}
	rep, _ := s.BusinessTaxReport(u.ID, 2026)
	if rep.IncomeCents != 1200 || rep.ExpenseCents != 108+20+150 {
		t.Errorf("etsy report %+v", rep)
	}
	lines := map[string]int64{}
	for _, l := range rep.Lines {
		lines[l.Category] += l.AmountCents
	}
	if lines["Business: Sales"] != 1200 || lines["Business: Advertising"] != 150 || lines["Business: Bank & payment fees"] != 128 {
		t.Errorf("etsy lines %v", lines)
	}
	if p2, _ := ParseStatement([]byte(etsyCSV), nil); true {
		if c2, _ := s.CommitImport(u.ID, etsy.ID, p2, false); c2["added"] != 0 {
			t.Errorf("etsy re-import %v", c2)
		}
	}
	st := Account{Name: "Stripe", Type: "other"}
	s.SaveAccount(u.ID, &st)
	ps, err := ParseStatement([]byte(stripeCSV), nil)
	if err != nil || ps.Mapping.Profile != "platform" || len(ps.Rows) != 3 {
		t.Fatalf("stripe %v %+v", err, ps)
	}
	if ps.Rows[0].ExternalID != "txn_1" || ps.Rows[1].ExternalID != "txn_1:fee" || ps.Rows[1].AmountCents != -103 || !ps.Rows[2].Transfer {
		t.Errorf("stripe rows %+v", ps.Rows)
	}
}

func TestMileageAndEstimatedTax(t *testing.T) {
	s, u, acc := setupFin(t) // 2026-11-25
	s.SaveSettings(map[string]string{"est_tax_rate": "25", "mileage_rate": "0.70"})
	if err := s.SaveTrip(u.ID, &Trip{Date: "2026-03-10", Miles: 100, Purpose: ""}); err == nil {
		t.Error("trip without purpose accepted")
	}
	s.SaveTrip(u.ID, &Trip{Date: "2026-03-10", Miles: 100, Purpose: "Post office: ship orders"})
	for _, x := range []Transaction{
		{Date: "2026-02-10", AmountCents: 400000, Payee: "Sales", Business: true},
		{Date: "2026-02-11", AmountCents: -100000, Payee: "Supplies", Business: true},
		{Date: "2026-07-01", AmountCents: 200000, Payee: "Sales", Business: true},
		{Date: "2026-04-14", AmountCents: -50000, Payee: "IRS", CategoryID: catID(s, "Estimated tax payments")},
	} {
		x := x
		x.AccountID = &acc
		s.SaveTransaction(u.ID, &x)
	}
	miles, cents := s.MileageTotal(2026, "")
	if miles != 100 || cents != 7000 {
		t.Errorf("mileage %v %d", miles, cents)
	}
	e, _ := s.EstimatedTaxPlan(u.ID, 2026)
	// profit = 4000 - 1000 - 70 + 2000 = 4930; 25% = 1232.50
	if e.ProfitCents != 493000 || e.SetAside != 123250 || e.PaidCents != 50000 {
		t.Errorf("plan %+v", e)
	}
	q1 := e.Quarters[0] // through Mar 31: 4000-1000-70 = 2930 -> 732.50, paid 500 by Apr 15
	if q1.ProfitCents != 293000 || q1.SuggestedCents != 23250 || q1.Due != "2026-04-15" || !q1.Past {
		t.Errorf("q1 %+v", q1)
	}
	if e.Quarters[3].Due != "2027-01-15" {
		t.Errorf("q4 due %s", e.Quarters[3].Due)
	}
	rep, _ := s.BusinessTaxReport(u.ID, 2026)
	found := false
	for _, l := range rep.Lines {
		found = found || strings.HasPrefix(l.Line, "9 Car") && l.AmountCents == 7000
	}
	if !found {
		t.Error("mileage not on the tax report")
	}
	// reminder 7 days before Jan 15 2027 for Q4
	f := &fakeSender{}
	s.SaveSettings(map[string]string{"backup_keep": "0"})
	s.estTaxReminders(at("2027-01-08T10:00"), f, s.Settings(false))
	if len(f.sent) != 1 || !strings.Contains(f.sent[0], "Estimated tax Q4") {
		t.Errorf("reminder %v", f.sent)
	}
}
