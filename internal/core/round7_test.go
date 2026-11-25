package core

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestTrashRestore(t *testing.T) {
	s, u, acc := setupFin(t)
	tx := Transaction{Date: "2026-11-10", AmountCents: -10000, AccountID: &acc, Payee: "Big Store"}
	s.SaveTransaction(u.ID, &tx)
	s.SaveSplits(u.ID, tx.ID, []SplitPart{{AmountCents: -6000}, {AmountCents: -4000}})
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 16)...)
	a, _ := s.AddAttachment(u.ID, "receipt", &tx.ID, nil, "r.png", png)
	_, file, _ := s.GetAttachment(u.ID, a.ID)
	if err := s.DeleteTransaction(u.ID, tx.ID); err != nil {
		t.Fatal(err)
	}
	if txs, _ := s.ListTransactions(u.ID, TxFilter{}); len(txs) != 0 {
		t.Fatal("not deleted")
	}
	if _, err := os.Stat(file); err != nil {
		t.Error("receipt file removed before 30 days")
	}
	// a new transaction takes the old number, so the restore must renumber and keep the parts attached
	other := Transaction{Date: "2026-11-11", AmountCents: -100, AccountID: &acc, Payee: "Other"}
	s.SaveTransaction(u.ID, &other)
	items, _ := s.ListTrash(u)
	if len(items) != 1 || items[0].Kind != "transaction" {
		t.Fatalf("trash %+v", items)
	}
	if _, err := s.RestoreTrash(u, items[0].ID); err != nil {
		t.Fatal(err)
	}
	txs, _ := s.ListTransactions(u.ID, TxFilter{})
	var back Transaction
	for _, x := range txs {
		if x.Payee == "Big Store" {
			back = x
		}
	}
	if back.ID == 0 || !back.Split || back.Receipts != 1 {
		t.Fatalf("restored %+v", back)
	}
	if parts, _ := s.GetSplits(u.ID, back.ID); len(parts) != 2 {
		t.Errorf("parts %d", len(parts))
	}
	if ms, _ := s.Summary(u.ID, "2026-11"); ms.ExpenseCents != 10100 {
		t.Errorf("spending after restore %d", ms.ExpenseCents)
	}
	// purge after 30 days removes the file of a deleted receipt
	atts, _ := s.ListAttachments(u.ID, &back.ID, nil)
	s.DeleteAttachment(u.ID, atts[0].ID)
	s.Now = func() time.Time { return at("2027-01-05T10:00") }
	s.PurgeTrash()
	if _, err := os.Stat(file); err == nil {
		t.Error("purged receipt file still there")
	}
	kid, _ := s.CreateUser("kid", "Kid", "password1", RoleKid, "")
	if l, _ := s.ListTrash(kid); len(l) != 0 {
		t.Error("kid sees trash")
	}
}

func TestQuietHoursAndDigest(t *testing.T) {
	s, u, acc := setupFin(t)
	u.QuietStart, u.QuietEnd, u.DigestHour = 21, 7, 7
	s.UpdateProfile(u)
	u, _ = s.GetUser(u.ID)
	f := &fakeSender{}
	s.Now = func() time.Time { return at("2026-11-25T23:30") }
	s.notify(f, u, Message{Title: "late news"})
	if len(f.sent) != 0 || s.UnreadCount(u.ID) != 0 {
		t.Fatal("sent during quiet hours")
	}
	s.Now = func() time.Time { return at("2026-11-26T06:59") }
	s.FlushPending(f)
	if len(f.sent) != 0 {
		t.Error("sent before quiet hours ended")
	}
	s.Now = func() time.Time { return at("2026-11-26T07:00") }
	s.FlushPending(f)
	if len(f.sent) != 1 || s.UnreadCount(u.ID) != 1 {
		t.Errorf("not sent after quiet hours: %v", f.sent)
	}
	s.SaveBill(u.ID, &Bill{Name: "Water", AmountCents: 4500, StartDate: "2026-11-27", Freq: "monthly", AccountID: &acc})
	s.SaveEvent(u.ID, &Event{Title: "Dentist", Start: "2026-11-26T14:00"})
	s.Digests(at("2026-11-26T07:01"), f)
	s.Digests(at("2026-11-26T08:00"), f)
	n := 0
	for _, x := range f.sent {
		if strings.Contains(x, "Today:") {
			n++
		}
	}
	m := s.DigestFor(u, at("2026-11-26T07:01"))
	if n != 1 || !strings.Contains(m.Body, "Dentist") || !strings.Contains(m.Body, "Water") {
		t.Errorf("digest %d %q", n, m.Body)
	}
}

func TestSearch(t *testing.T) {
	s, mom, acc := setupFin(t)
	kid, _ := s.CreateUser("kid", "Kid", "password1", RoleKid, "")
	tx := Transaction{Date: "2026-11-10", AmountCents: -500, AccountID: &acc, Payee: "Pizza Place"}
	s.SaveTransaction(mom.ID, &tx)
	s.SaveEvent(mom.ID, &Event{Title: "Pizza party", Start: "2026-11-28"})
	s.SaveEvent(mom.ID, &Event{Title: "Pizza secret", Start: "2026-11-28", Private: true})
	hits, _ := s.Search(mom, "pizza")
	if len(hits) != 3 {
		t.Errorf("mom hits %+v", hits)
	}
	kh, _ := s.Search(kid, "pizza")
	if len(kh) != 1 || kh[0].Title != "Pizza party" {
		t.Errorf("kid hits %+v", kh)
	}
	if h, _ := s.Search(mom, "100%"); len(h) != 0 {
		t.Error("wildcard not escaped")
	}
}
