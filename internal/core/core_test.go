package core

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func at(s string) time.Time {
	t, err := ParseLocal(s)
	if err != nil {
		panic(err)
	}
	return t
}

type fakeSender struct {
	mu    sync.Mutex
	sent  []string
	house []string
}

func (f *fakeSender) Deliver(u *User, m Message) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, u.Username+"|"+m.Title)
	return "fake: ok"
}
func (f *fakeSender) Household(m Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.house = append(f.house, m.Title)
	return nil
}

func TestMonthEndRecurrence(t *testing.T) {
	st := at("2026-01-31")
	got := Occurrences(st, "monthly", 1, "", at("2026-01-01"), at("2026-05-31"))
	want := []string{"2026-01-31", "2026-02-28", "2026-03-31", "2026-04-30", "2026-05-31"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i, g := range got {
		if g.Format(DateFmt) != want[i] {
			t.Errorf("%d: got %s want %s", i, g.Format(DateFmt), want[i])
		}
	}
	// leap year, yearly on Feb 29
	ly := Occurrences(at("2028-02-29"), "yearly", 1, "", at("2028-01-01"), at("2032-12-31"))
	if ly[1].Format(DateFmt) != "2029-02-28" || ly[4].Format(DateFmt) != "2032-02-29" {
		t.Errorf("leap: %v", ly)
	}
}

func TestRecurrenceIntervalUntilAndSkipAhead(t *testing.T) {
	st := at("2020-01-06T09:30") // Monday
	got := Occurrences(st, "weekly", 2, "2026-12-31", at("2026-11-01"), at("2026-11-30"))
	if len(got) != 2 {
		t.Fatalf("biweekly in Nov 2026: %v", got)
	}
	for _, g := range got {
		if g.Weekday() != time.Monday || g.Format("15:04") != "09:30" {
			t.Errorf("bad occurrence %v", g)
		}
	}
	if n := len(Occurrences(st, "daily", 1, "2020-01-10", at("2020-01-01"), at("2020-12-31"))); n != 5 {
		t.Errorf("until inclusive: got %d want 5", n)
	}
	if n := len(Occurrences(at("2026-03-03"), "none", 1, "", at("2026-03-01"), at("2026-03-31"))); n != 1 {
		t.Errorf("single: %d", n)
	}
}

func TestDSTKeepsWallClock(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tzdata")
	}
	st := time.Date(2026, 3, 7, 10, 0, 0, 0, ny) // day before US DST starts
	occ := Occurrences(st, "daily", 1, "", st, st.AddDate(0, 0, 3))
	for _, o := range occ {
		if o.Hour() != 10 {
			t.Errorf("DST moved the time: %v", o)
		}
	}
}

func TestCents(t *testing.T) {
	cases := map[string]int64{"12": 1200, "12.5": 1250, "$1,234.56": 123456, "-3.07": -307, "(4.00)": -400, ".99": 99, "": 0}
	for in, want := range cases {
		got, err := ParseCents(in)
		if err != nil || got != want {
			t.Errorf("%q: got %d %v want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"1.234", "abc", "1.2.3"} {
		if _, err := ParseCents(bad); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
	if FormatCents(-123456789) != "-1,234,567.89" || FormatCents(5) != "0.05" {
		t.Error(FormatCents(-123456789), FormatCents(5))
	}
}

func TestAuthAndSessions(t *testing.T) {
	s := newStore(t)
	if _, err := s.CreateUser("x", "", "longenough", RoleAdmin, ""); err == nil {
		t.Error("short username accepted")
	}
	if _, err := s.CreateUser("mom", "Mom", "short", RoleAdmin, ""); err == nil {
		t.Error("short password accepted")
	}
	mom, err := s.CreateUser("mom", "Mom", "password1", RoleAdmin, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser("MOM", "", "password1", RoleAdult, ""); err == nil {
		t.Error("duplicate username (case) accepted")
	}
	if _, err := s.Login("mom", "wrong-pass"); err != ErrBadLogin {
		t.Error("bad password accepted")
	}
	if _, err := s.Login("Mom", "password1"); err != nil {
		t.Error("login is case-insensitive on username", err)
	}
	tok, _ := s.NewSession(mom.ID, "web", "")
	if u, _, err := s.SessionUser(tok); err != nil || u.ID != mom.ID {
		t.Fatal("session lookup", err)
	}
	if err := s.SetRole(mom.ID, RoleAdult, true); err == nil {
		t.Error("last admin demoted")
	}
	s.EndSession(tok)
	if _, _, err := s.SessionUser(tok); err == nil {
		t.Error("ended session still works")
	}
	// expiry
	tok2, _ := s.NewSession(mom.ID, "web", "")
	s.Now = func() time.Time { return time.Now().AddDate(0, 0, 31) }
	if _, _, err := s.SessionUser(tok2); err == nil {
		t.Error("expired session works")
	}
}

func TestPrivacyAndBudget(t *testing.T) {
	s := newStore(t)
	mom, _ := s.CreateUser("mom", "Mom", "password1", RoleAdmin, "")
	dad, _ := s.CreateUser("dad", "Dad", "password1", RoleAdult, "")
	shared := Account{Name: "Joint", OpeningCents: 100000}
	secret := Account{Name: "Mom savings", Private: true, OpeningCents: 5000}
	s.SaveAccount(mom.ID, &shared)
	s.SaveAccount(mom.ID, &secret)
	accs, _ := s.ListAccounts(dad.ID)
	if len(accs) != 1 || accs[0].Name != "Joint" {
		t.Fatalf("dad sees %v", accs)
	}
	if err := s.SaveAccount(dad.ID, &Account{ID: secret.ID, Name: "hacked"}); err != ErrNotFound {
		t.Error("dad edited mom's private account", err)
	}
	cats, _ := s.ListCategories()
	var groc int64
	for _, c := range cats {
		if c.Name == "Groceries" {
			groc = c.ID
			c.BudgetCents = 60000
			s.SaveCategory(&c)
		}
	}
	for _, tx := range []Transaction{
		{Date: "2026-11-03", AmountCents: -12345, AccountID: &shared.ID, CategoryID: &groc, Payee: "Store"},
		{Date: "2026-11-10", AmountCents: -5000, AccountID: &shared.ID, CategoryID: &groc, Payee: "Store", Private: true},
		{Date: "2026-11-15", AmountCents: 250000, AccountID: &shared.ID, Payee: "Pay"},
		{Date: "2026-10-30", AmountCents: -999, AccountID: &shared.ID, CategoryID: &groc},
	} {
		tx := tx
		if err := s.SaveTransaction(mom.ID, &tx); err != nil {
			t.Fatal(err)
		}
	}
	ms, err := s.Summary(dad.ID, "2026-11")
	if err != nil {
		t.Fatal(err)
	}
	if ms.IncomeCents != 250000 || ms.ExpenseCents != 12345 {
		t.Errorf("dad summary income %d expense %d", ms.IncomeCents, ms.ExpenseCents)
	}
	msMom, _ := s.Summary(mom.ID, "2026-11")
	for _, c := range msMom.Categories {
		if c.ID == groc && (c.SpentCents != 17345 || c.BudgetCents != 60000) {
			t.Errorf("groceries spent %d budget %d", c.SpentCents, c.BudgetCents)
		}
	}
	accs, _ = s.ListAccounts(mom.ID)
	for _, a := range accs {
		if a.Name == "Joint" && a.BalanceCents != 100000-12345-5000+250000-999 {
			t.Errorf("balance %d", a.BalanceCents)
		}
	}
}

func TestBillPayAndDues(t *testing.T) {
	s := newStore(t)
	s.Now = func() time.Time { return at("2026-11-20T10:00") }
	mom, _ := s.CreateUser("mom", "Mom", "password1", RoleAdmin, "")
	b := Bill{Name: "Rent", AmountCents: 150000, StartDate: "2026-01-01", Freq: "monthly", RemindDays: "3,0"}
	if err := s.SaveBill(mom.ID, &b); err != nil {
		t.Fatal(err)
	}
	dues, _ := s.BillDues(mom.ID, at("2026-10-01"), at("2026-12-31"))
	if len(dues) != 3 || !dues[0].Overdue || !dues[1].Overdue || dues[2].Overdue {
		t.Fatalf("dues %+v", dues)
	}
	if err := s.PayBill(mom.ID, b.ID, "2026-11-01", "", 0, true); err != nil {
		t.Fatal(err)
	}
	txs, _ := s.ListTransactions(mom.ID, TxFilter{})
	if len(txs) != 1 || txs[0].AmountCents != -150000 {
		t.Fatalf("pay txs %+v", txs)
	}
	dues, _ = s.BillDues(mom.ID, at("2026-11-01"), at("2026-11-01"))
	if !dues[0].Paid || dues[0].Overdue {
		t.Error("not marked paid")
	}
	if err := s.UnpayBill(mom.ID, b.ID, "2026-11-01"); err != nil {
		t.Fatal(err)
	}
	if txs, _ := s.ListTransactions(mom.ID, TxFilter{}); len(txs) != 0 {
		t.Error("unpay left the transaction")
	}
}

func TestRemindersFireOnce(t *testing.T) {
	s := newStore(t)
	now := at("2026-11-25T13:10")
	s.Now = func() time.Time { return now }
	mom, _ := s.CreateUser("mom", "Mom", "password1", RoleAdmin, "")
	dad, _ := s.CreateUser("dad", "Dad", "password1", RoleAdult, "")
	kid, _ := s.CreateUser("kid", "Kid", "password1", RoleKid, "")
	// dentist at 14:00 for kid + mom, reminders 60 and 1440 minutes before
	e := Event{Title: "Dentist", Start: "2026-11-25T14:00", RemindMinutes: "60,1440", Members: "3,1", Location: "Main St"}
	if err := s.SaveEvent(mom.ID, &e); err != nil {
		t.Fatal(err)
	}
	// rent due in 3 days, reminders 3 and 0 days before at 09:00
	b := Bill{Name: "Rent", AmountCents: 150000, StartDate: "2026-11-28", Freq: "monthly", RemindDays: "3,0"}
	s.SaveBill(mom.ID, &b)
	// mom's private appointment
	p := Event{Title: "Secret", Start: "2026-11-25T13:30", RemindMinutes: "30", Private: true}
	s.SaveEvent(mom.ID, &p)

	f := &fakeSender{}
	n, err := s.RunReminders(now, f)
	if err != nil {
		t.Fatal(err)
	}
	// dentist 60-min (mom, kid) + rent 3-day (mom, dad; not kid) + secret (mom) = 5
	if n != 5 {
		t.Fatalf("first run sent %d: %v", n, f.sent)
	}
	for _, x := range f.sent {
		if strings.HasPrefix(x, "kid|Rent") || strings.HasPrefix(x, "dad|Secret") || strings.HasPrefix(x, "dad|Dentist") {
			t.Errorf("wrong person got %s", x)
		}
	}
	// a second tick, and a restart (new store on same file), must not resend
	n2, _ := s.RunReminders(now.Add(time.Minute), f)
	s2, _ := Open(s.Path)
	defer s2.Close()
	s2.Now = s.Now
	n3, _ := s2.RunReminders(now.Add(2*time.Minute), f)
	if n2 != 0 || n3 != 0 {
		t.Errorf("resent: %d %d", n2, n3)
	}
	if len(f.house) != 2 { // dentist + rent (not the private one)
		t.Errorf("household webhook got %v", f.house)
	}
	if c := s.UnreadCount(kid.ID); c != 1 {
		t.Errorf("kid unread %d", c)
	}
	_ = dad
	// pay rent, then on the due day no reminder
	s.PayBill(mom.ID, b.ID, "2026-11-28", "", 0, false)
	due := at("2026-11-28T09:05")
	s.Now = func() time.Time { return due }
	if n, _ := s.RunReminders(due, f); n != 0 {
		t.Errorf("paid bill reminded: %d", n)
	}
}

func TestOverdueAndAutopay(t *testing.T) {
	s := newStore(t)
	mom, _ := s.CreateUser("mom", "Mom", "password1", RoleAdmin, "")
	b := Bill{Name: "Phone", AmountCents: 8000, StartDate: "2026-11-24", Freq: "monthly", RemindDays: ""}
	s.SaveBill(mom.ID, &b)
	a := Bill{Name: "Netflix", AmountCents: 1599, StartDate: "2026-11-24", Freq: "monthly", Autopay: true, RemindDays: "1"}
	s.SaveBill(mom.ID, &a)
	now := at("2026-11-25T09:30")
	s.Now = func() time.Time { return now }
	f := &fakeSender{}
	n, _ := s.RunReminders(now, f)
	if n != 1 || !strings.Contains(f.sent[0], "OVERDUE: Phone") {
		t.Errorf("overdue: %d %v", n, f.sent)
	}
	dues, _ := s.BillDues(mom.ID, at("2026-11-24"), at("2026-11-24"))
	for _, d := range dues {
		if d.Name == "Netflix" && !d.Paid {
			t.Error("autopay not marked paid")
		}
	}
	txs, _ := s.ListTransactions(mom.ID, TxFilter{})
	if len(txs) != 1 || txs[0].AmountCents != -1599 {
		t.Errorf("autopay tx %+v", txs)
	}
	s.RunReminders(now.Add(time.Hour), f)
	if txs, _ := s.ListTransactions(mom.ID, TxFilter{}); len(txs) != 1 {
		t.Error("autopay recorded twice")
	}
}

func TestSettingsMask(t *testing.T) {
	s := newStore(t)
	s.SaveSettings(map[string]string{"smtp_pass": "hunter22", "household_name": "Smiths", "bogus": "x"})
	m := s.Settings(true)
	if m["smtp_pass"] != Mask || m["household_name"] != "Smiths" {
		t.Errorf("%v", m)
	}
	s.SaveSettings(map[string]string{"smtp_pass": Mask})
	if s.Setting("smtp_pass") != "hunter22" {
		t.Error("mask overwrote secret")
	}
}

func TestCalendarAndICS(t *testing.T) {
	s := newStore(t)
	s.Now = func() time.Time { return at("2026-11-20T10:00") }
	mom, _ := s.CreateUser("mom", "Mom", "password1", RoleAdmin, "")
	kid, _ := s.CreateUser("kid", "Kid", "password1", RoleKid, "")
	s.SaveEvent(mom.ID, &Event{Title: "Trip", Start: "2026-11-27", End: "2026-11-29", AllDay: true})
	s.SaveBill(mom.ID, &Bill{Name: "Water", AmountCents: 4500, StartDate: "2026-11-15", Freq: "monthly"})
	occ, _ := s.Calendar(mom, at("2026-11-28"), at("2026-11-28"), true)
	if len(occ) != 1 || occ[0].Title != "Trip" {
		t.Errorf("multi-day event on middle day: %+v", occ)
	}
	kidOcc, _ := s.Calendar(kid, at("2026-11-01"), at("2026-11-30"), true)
	for _, o := range kidOcc {
		if o.Source == "bill" {
			t.Error("kid sees a bill")
		}
	}
	ics, err := s.ICS(mom)
	if err != nil || !strings.Contains(ics, "SUMMARY:Bill: Water $45.00") || !strings.Contains(ics, "DTSTART;VALUE=DATE:20261127") {
		t.Errorf("ics: %v\n%s", err, ics)
	}
}
