package core

import (
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// openAtVersion builds a database that stopped at migration n, with one user and one transaction.
func openAtVersion(t *testing.T, n int) string {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if _, err := db.Exec(migrations[i]); err != nil {
			t.Fatalf("migration %d: %v", i+1, err)
		}
	}
	db.Exec(`PRAGMA user_version = ` + strconv.Itoa(n))
	db.Exec(`INSERT INTO users(username,display_name,pass_hash,created) VALUES('a','A','x','2026-01-01')`)
	db.Exec(`INSERT INTO accounts(name,owner_id) VALUES('Old',1)`)
	db.Exec(`INSERT INTO transactions(date,amount_cents,account_id,user_id,created) VALUES('2026-11-01',-500,1,1,'2026-11-01')`)
	db.Close()
	return path
}

func TestEveryMigrationKeepsData(t *testing.T) {
	for v := 1; v < len(migrations); v++ {
		s, err := Open(openAtVersion(t, v))
		if err != nil {
			t.Fatalf("from v%d: %v", v, err)
		}
		accs, _ := s.ListAccounts(1)
		txs, _ := s.ListTransactions(1, TxFilter{})
		if len(accs) != 1 || accs[0].BalanceCents != -500 || len(txs) != 1 {
			t.Errorf("from v%d: accounts %+v txs %d", v, accs, len(txs))
		}
		s.Close()
	}
}

func addTx(s *Store, uid, acc int64, date string, cents int64, payee string) {
	a := acc
	t := Transaction{Date: date, AmountCents: cents, AccountID: &a, Payee: payee, Source: "import", ExternalID: "x" + date + payee}
	s.AddImported(uid, &t)
}

func TestSubscriptionsFound(t *testing.T) {
	s, u, acc := setupFin(t)
	for i, d := range []string{"2026-07-03", "2026-08-03", "2026-09-02", "2026-10-03", "2026-11-03"} {
		amt := int64(-1549)
		if i == 4 {
			amt = -1799
		}
		addTx(s, u.ID, acc, d, amt, "NETFLIX.COM 866-000-0000")
	}
	addTx(s, u.ID, acc, "2025-12-01", -9900, "Cloud Store yearly")
	addTx(s, u.ID, acc, "2026-11-30", -9900, "Cloud Store yearly") // 364 days later
	for _, d := range []string{"2026-08-01", "2026-08-09", "2026-10-20", "2026-11-02"} {
		addTx(s, u.ID, acc, d, -2000, "Corner Grocery")
	}
	tr := Transaction{Date: "2026-10-01", AmountCents: -5000, AccountID: &acc, Payee: "To savings", Transfer: true}
	s.SaveTransaction(u.ID, &tr)
	s.Now = func() time.Time { return at("2026-12-01T10:00") }
	subs, err := s.Subscriptions(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Subscription{}
	for _, x := range subs {
		got[x.Key] = x
	}
	nf, ok := got["netflix"]
	if !ok || nf.Period != "monthly" || nf.AmountCents != 1799 || nf.OldCents != 1549 || !nf.Active || nf.NextDate != "2026-12-03" {
		t.Errorf("netflix %+v (all %+v)", nf, subs)
	}
	if cs, ok := got["cloud store"]; !ok || cs.Period != "yearly" {
		t.Errorf("yearly not found: %+v", subs)
	}
	if _, ok := got["corner grocery"]; ok {
		t.Error("irregular shopping taken as a subscription")
	}
	b, err := s.SubscriptionToBill(u.ID, "netflix")
	if err != nil || b.Freq != "monthly" || b.AmountCents != 1799 || b.StartDate != "2026-12-03" {
		t.Fatalf("to bill %+v %v", b, err)
	}
	subs, _ = s.Subscriptions(u.ID)
	for _, x := range subs {
		if x.Key == "netflix" && x.BillID == nil {
			t.Error("subscription not linked to its new bill")
		}
	}
}

func TestForecastAndLowBalance(t *testing.T) {
	s, u, _ := setupFin(t) // Now = 2026-11-25
	low := int64(80000)
	chk := Account{Name: "Checking", Type: "checking", OpeningCents: 100000, LowBalance: &low}
	s.SaveAccount(u.ID, &chk)
	s.SaveBill(u.ID, &Bill{Name: "Rent", AmountCents: 30000, StartDate: "2026-11-30", Freq: "monthly", AccountID: &chk.ID})
	s.SaveBill(u.ID, &Bill{Name: "Phone", AmountCents: 5000, StartDate: "2026-12-01", Freq: "monthly"}) // no account
	s.SaveIncome(u.ID, &Income{Name: "VA payment", AmountCents: 50000, StartDate: "2026-12-01", Freq: "monthly", AccountID: &chk.ID})
	fc, notes, err := s.Forecast(u.ID, 30)
	if err != nil {
		t.Fatal(err)
	}
	var c AccountForecast
	for _, f := range fc {
		if f.AccountID == chk.ID {
			c = f
		}
	}
	// 1000 -300 (Nov 30) = 700; Dec 1: -50 first (650 = low point), then +500 = 1150
	if c.LowestCents != 65000 || c.LowestDate != "2026-12-01" || !c.Warning || c.SafeToSpend != 0 || c.EndCents != 115000 {
		t.Errorf("forecast %+v", c)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "Checking") {
		t.Errorf("unassigned note %v", notes)
	}
	// income stays out of reminders, autopay and summary
	f := &fakeSender{}
	if n, _ := s.RunReminders(at("2026-12-01T09:30"), f); n != 0 && strings.Contains(strings.Join(f.sent, ","), "VA payment") {
		t.Error("income reminded like a bill")
	}
	if ms, _ := s.Summary(u.ID, "2026-12"); ms.IncomeCents != 0 {
		t.Error("forecast income counted as real income")
	}
	// daily check warns once
	s.SaveSettings(map[string]string{"backup_keep": "0"})
	s.DailyChecks(at("2026-11-25T10:00"), f)
	s.DailyChecks(at("2026-11-25T11:00"), f)
	n := 0
	for _, x := range f.sent {
		if strings.Contains(x, "may drop") {
			n++
		}
	}
	if n != 1 { // one adult
		t.Errorf("low balance alerts %d: %v", n, f.sent)
	}
}

func TestBackupsKeepN(t *testing.T) {
	s, _, _ := setupFin(t)
	os.MkdirAll(filepath.Join(filepath.Dir(s.Path), "files", "receipts"), 0o755)
	os.WriteFile(filepath.Join(filepath.Dir(s.Path), "files", "receipts", "r1.png"), []byte("png"), 0o644)
	for _, d := range []string{"2026-11-20", "2026-11-21", "2026-11-22"} {
		day := d
		s.Now = func() time.Time { return at(day + "T03:30") }
		if _, err := s.RunBackup(2); err != nil {
			t.Fatal(err)
		}
	}
	list, _ := s.ListBackups()
	if len(list) != 2 || list[0].Name != "household-2026-11-22.db" {
		t.Errorf("backups %+v", list)
	}
	if _, err := os.Stat(filepath.Join(s.BackupDir(), "files", "receipts", "r1.png")); err != nil {
		t.Error("attachments not copied")
	}
	b, err := Open(filepath.Join(s.BackupDir(), list[0].Name))
	if err != nil || b.UserCount() != 1 {
		t.Errorf("backup not usable: %v", err)
	}
	if b != nil {
		b.Close()
	}
	// automatic: once per day
	s.Now = func() time.Time { return at("2026-11-23T05:00") }
	s.SaveSettings(map[string]string{"backup_keep": "5"})
	s.DailyChecks(at("2026-11-23T05:00"), nil)
	s.DailyChecks(at("2026-11-23T06:00"), nil)
	list, _ = s.ListBackups()
	if len(list) != 3 {
		t.Errorf("daily backup count %d", len(list))
	}
}

func TestMarkPaidButton(t *testing.T) {
	s, u, acc := setupFin(t)
	u.Channels, u.NtfyTopic = "inapp,ntfy", "test-topic"
	s.UpdateProfile(u)
	s.SaveSettings(map[string]string{"base_url": "http://192.0.2.10:8095"})
	b := Bill{Name: "Water", AmountCents: 4000, StartDate: "2026-11-28", Freq: "monthly", RemindDays: "3", AccountID: &acc}
	s.SaveBill(u.ID, &b)
	var got Message
	cap := &capSender{fn: func(m Message) { got = m }}
	s.RunReminders(at("2026-11-25T10:00"), cap)
	if len(got.Actions) != 1 || !strings.HasPrefix(got.Actions[0].URL, "http://192.0.2.10:8095/act/") {
		t.Fatalf("no Mark paid button: %+v", got)
	}
	tok := strings.TrimPrefix(got.Actions[0].URL, "http://192.0.2.10:8095/act/")
	msg, err := s.UseActionToken(tok)
	if err != nil || !strings.Contains(msg, "Water") {
		t.Fatalf("use %q %v", msg, err)
	}
	if msg, _ := s.UseActionToken(tok); msg != "Already done." {
		t.Errorf("second use %q", msg)
	}
	dues, _ := s.BillDues(u.ID, at("2026-11-28"), at("2026-11-28"))
	if !dues[0].Paid {
		t.Error("not paid")
	}
	if _, err := s.UseActionToken("nope"); err != ErrNotFound {
		t.Error("bad token accepted")
	}
	t2 := s.NewActionToken(b.ID, "2026-12-28", u.ID)
	s.Now = func() time.Time { return at("2027-02-01T10:00") }
	if _, err := s.UseActionToken(t2); err == nil {
		t.Error("expired token worked")
	}
}

type capSender struct{ fn func(Message) }

func (c *capSender) Deliver(u *User, m Message) string { c.fn(m); return "ok" }
func (c *capSender) Household(m Message) error         { return nil }

func TestPrivateAlertsStayPrivate(t *testing.T) {
	s, mom, _ := setupFin(t) // 2026-11-25
	dad, _ := s.CreateUser("dad", "Dad", "password1", RoleAdult, "")
	low := int64(50000)
	mine := Account{Name: "Mom savings", Type: "savings", OpeningCents: 60000, Private: true, LowBalance: &low}
	s.SaveAccount(mom.ID, &mine)
	s.SaveBill(mom.ID, &Bill{Name: "Gym", AmountCents: 20000, StartDate: "2026-11-28", Freq: "monthly", AccountID: &mine.ID, Private: true})
	for i, d := range []string{"2026-07-22", "2026-08-22", "2026-09-22", "2026-10-22", "2026-11-22"} {
		amt := int64(-999)
		if i == 4 {
			amt = -1299
		}
		tx := Transaction{Date: d, AmountCents: amt, AccountID: &mine.ID, Payee: "SECRET APP", Private: true}
		s.SaveTransaction(mom.ID, &tx)
	}
	f := &fakeSender{}
	s.SaveSettings(map[string]string{"backup_keep": "0"})
	s.DailyChecks(at("2026-11-25T10:00"), f)
	for _, x := range f.sent {
		if strings.HasPrefix(x, "dad|") {
			t.Errorf("dad was told about mom's private money: %s", x)
		}
	}
	got := strings.Join(f.sent, ";")
	if !strings.Contains(got, "mom|Mom savings may drop") || !strings.Contains(got, "mom|Price change: SECRET APP") {
		t.Errorf("mom not told: %s", got)
	}
	_ = dad
}

func TestOffsiteCopy(t *testing.T) {
	s, _, _ := setupFin(t)
	dst := t.TempDir()
	name, err := s.RunBackup(5)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CopyBackupOffsite(dst, name, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, name)); err != nil {
		t.Error("copy missing")
	}
	if err := s.CopyBackupOffsite(filepath.Join(filepath.Dir(s.Path), "backups"), name, 5); err == nil {
		t.Error("copy into the data folder allowed")
	}
	if err := s.CopyBackupOffsite(filepath.Join(dst, "nope"), name, 5); err == nil {
		t.Error("missing folder not reported")
	}
}
