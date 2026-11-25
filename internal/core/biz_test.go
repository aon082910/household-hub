package core

import (
	"strings"
	"testing"
)

func catID(s *Store, name string) *int64 {
	cats, _ := s.ListCategories()
	for _, c := range cats {
		if c.Name == name {
			id := c.ID
			return &id
		}
	}
	return nil
}

func TestSplitCountsPartsOnce(t *testing.T) {
	s, u, acc := setupFin(t)
	tx := Transaction{Date: "2026-11-10", AmountCents: -10000, AccountID: &acc, Payee: "Big Store", ExternalID: "S1", Source: "import"}
	s.SaveTransaction(u.ID, &tx)
	if err := s.SaveSplits(u.ID, tx.ID, []SplitPart{{AmountCents: -6000, CategoryID: catID(s, "Groceries")}, {AmountCents: -3000}}); err == nil {
		t.Error("parts that do not add up accepted")
	}
	err := s.SaveSplits(u.ID, tx.ID, []SplitPart{{AmountCents: -6000, CategoryID: catID(s, "Groceries")}, {AmountCents: -4000, CategoryID: catID(s, "Business: Supplies"), Business: true}})
	if err != nil {
		t.Fatal(err)
	}
	ms, _ := s.Summary(u.ID, "2026-11")
	if ms.ExpenseCents != 10000 {
		t.Errorf("spending %d (split counted twice?)", ms.ExpenseCents)
	}
	for _, c := range ms.Categories {
		if c.Name == "Groceries" && c.SpentCents != 6000 {
			t.Errorf("groceries %d", c.SpentCents)
		}
	}
	accs, _ := s.ListAccounts(u.ID)
	if accs[0].BalanceCents != -10000 {
		t.Errorf("balance %d", accs[0].BalanceCents)
	}
	txs, _ := s.ListTransactions(u.ID, TxFilter{})
	if len(txs) != 1 || !txs[0].Split {
		t.Errorf("list shows parts: %+v", txs)
	}
	// same charge imported again: still a duplicate of the parent
	again := Transaction{Date: "2026-11-10", AmountCents: -10000, AccountID: &acc, Payee: "Big Store", ExternalID: "S1", Source: "import"}
	if st, _ := s.AddImported(u.ID, &again); st != "duplicate" {
		t.Errorf("re-import %s", st)
	}
	rep, _ := s.BusinessTaxReport(u.ID, 2026)
	if rep.ExpenseCents != 4000 || rep.NoReceipt != 0 {
		t.Errorf("tax report from split %+v", rep)
	}
	// changing the total drops the parts
	tx.AmountCents = -12000
	s.SaveTransaction(u.ID, &tx)
	if parts, _ := s.GetSplits(u.ID, tx.ID); len(parts) != 0 {
		t.Error("stale parts kept after the total changed")
	}
}

func TestRulesAndBusinessAccount(t *testing.T) {
	s, u, _ := setupFin(t)
	biz := Account{Name: "Business Relay", Business: true}
	s.SaveAccount(u.ID, &biz)
	s.SaveCategoryRule(&CategoryRule{Contains: "canva|etsy fee", CategoryID: catID(s, "Business: Office & software"), Business: 1, Transfer: -1})
	s.SaveCategoryRule(&CategoryRule{Contains: "transfer to", Business: -1, Transfer: 1})
	p, _ := ParseStatement([]byte("Date,Description,Amount\n11/02/2026,CANVA PRO,-15.00\n11/03/2026,TRANSFER TO PAYPAL,-200.00\n11/04/2026,ETSY SALE,45.00\n"), nil)
	s.CommitImport(u.ID, biz.ID, p, false)
	txs, _ := s.ListTransactions(u.ID, TxFilter{})
	got := map[string]Transaction{}
	for _, x := range txs {
		got[x.Payee] = x
	}
	if c := got["CANVA PRO"]; c.CategoryID == nil || *c.CategoryID != *catID(s, "Business: Office & software") || !c.Business {
		t.Errorf("rule not applied: %+v", c)
	}
	if !got["TRANSFER TO PAYPAL"].Transfer || !got["ETSY SALE"].Business {
		t.Errorf("flags: %+v", got)
	}
	rep, _ := s.BusinessTaxReport(u.ID, 2026)
	if rep.IncomeCents != 4500 || rep.ExpenseCents != 1500 || rep.Uncategorised != 1 {
		t.Errorf("report %+v", rep)
	}
	csv, _ := s.TaxReportCSV(u.ID, 2026)
	if !strings.Contains(csv, "18 Office expense") || !strings.Contains(csv, "not tax advice") {
		t.Errorf("csv %s", csv)
	}
	// bulk edit: mark the sale personal
	n, _ := s.BulkEdit(u.ID, []int64{got["ETSY SALE"].ID}, BulkChange{Business: ptrBool(false)})
	if n != 1 {
		t.Error("bulk edit")
	}
	if rep, _ := s.BusinessTaxReport(u.ID, 2026); rep.IncomeCents != 0 {
		t.Error("bulk edit did not apply")
	}
}

func ptrBool(b bool) *bool { return &b }

func TestAttachments(t *testing.T) {
	s, u, acc := setupFin(t)
	other, _ := s.CreateUser("dad", "Dad", "password1", RoleAdult, "")
	priv := Transaction{Date: "2026-11-10", AmountCents: -500, AccountID: &acc, Payee: "Secret", Private: true}
	s.SaveTransaction(u.ID, &priv)
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...)
	a, err := s.AddAttachment(u.ID, "receipt", &priv.ID, nil, `..\..\evil<1>.png`, png)
	if err != nil || a.Mime != "image/png" || strings.ContainsAny(a.Name, `\/<>`) {
		t.Fatalf("add %+v %v", a, err)
	}
	if _, err := s.AddAttachment(u.ID, "receipt", &priv.ID, nil, "x.html", []byte("<html><script>alert(1)</script></html>")); err == nil {
		t.Error("HTML accepted")
	}
	if _, err := s.AddAttachment(u.ID, "receipt", &priv.ID, nil, "x.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`)); err == nil {
		t.Error("SVG accepted")
	}
	if _, _, err := s.GetAttachment(other.ID, a.ID); err == nil {
		t.Error("other user read a private receipt")
	}
	if _, full, err := s.GetAttachment(u.ID, a.ID); err != nil || !strings.HasPrefix(full, s.FilesDir()) {
		t.Errorf("owner read: %v %s", err, full)
	}
	txs, _ := s.ListTransactions(u.ID, TxFilter{})
	if txs[0].Receipts != 1 {
		t.Error("receipt count")
	}
	if err := s.DeleteAttachment(u.ID, a.ID); err != nil {
		t.Error(err)
	}
}
