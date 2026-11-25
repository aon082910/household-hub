package core

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// All names, addresses and amounts below are made up.

func TestMigrationFromV1KeepsData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(migrations[0]); err != nil {
		t.Fatal(err)
	}
	db.Exec(`PRAGMA user_version = 1`)
	db.Exec(`INSERT INTO users(username,display_name,pass_hash,created) VALUES('a','A','x','2026-01-01')`)
	db.Exec(`INSERT INTO transactions(date,amount_cents,user_id,created) VALUES('2026-11-01',-500,1,'2026-11-01')`)
	db.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	txs, err := s.ListTransactions(1, TxFilter{})
	if err != nil || len(txs) != 1 || txs[0].Source != "manual" || txs[0].AmountCents != -500 {
		t.Fatalf("after migration: %v %+v", err, txs)
	}
}

func setupFin(t *testing.T) (*Store, *User, int64) {
	s := newStore(t)
	s.Now = func() time.Time { return at("2026-11-25T10:00") }
	u, _ := s.CreateUser("mom", "Mom", "password1", RoleAdmin, "")
	a := Account{Name: "Wallet A"}
	s.SaveAccount(u.ID, &a)
	return s, u, a.ID
}

const paypalCSV = "ï»¿\"Date\",\"Time\",\"TimeZone\",\"Name\",\"Type\",\"Status\",\"Currency\",\"Gross\",\"Fee\",\"Net\",\"From Email Address\",\"To Email Address\",\"Transaction ID\"\n" +
	"\"11/20/2026\",\"10:00:00\",\"EST\",\"Streamy Video\",\"PreApproved Payment Bill User Payment\",\"Completed\",\"USD\",\"-12.99\",\"0.00\",\"-12.99\",\"me@example.test\",\"billing@streamy.test\",\"1AA11111BB2222222\"\n" +
	"\"11/20/2026\",\"10:00:01\",\"EST\",\"\",\"General Card Deposit\",\"Completed\",\"USD\",\"12.99\",\"0.00\",\"12.99\",\"\",\"me@example.test\",\"3CC33333DD4444444\"\n" +
	"\"11/21/2026\",\"09:00:00\",\"EST\",\"Corner Shop\",\"General Authorization\",\"Pending\",\"USD\",\"-5.00\",\"0.00\",\"-5.00\",\"me@example.test\",\"shop@example.test\",\"5EE55555FF6666666\"\n" +
	"\"11/22/2026\",\"12:00:00\",\"EST\",\"Pat Friend\",\"General Payment\",\"Completed\",\"USD\",\"40.00\",\"0.00\",\"40.00\",\"pat@example.test\",\"me@example.test\",\"7GG77777HH8888888\"\n"

func TestPayPalCSV(t *testing.T) {
	s, u, acc := setupFin(t)
	p, err := ParseStatement([]byte(paypalCSV), nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Mapping.Profile != "paypal" || len(p.Rows) != 4 {
		t.Fatalf("mapping %+v rows %d", p.Mapping, len(p.Rows))
	}
	if p.Rows[2].Skip == "" || !p.Rows[1].Transfer || p.Rows[0].AmountCents != -1299 || p.Rows[0].Payee != "Streamy Video" {
		t.Errorf("rows %+v", p.Rows)
	}
	counts, err := s.CommitImport(u.ID, acc, p, false)
	if err != nil || counts["added"] != 3 || counts["skip"] != 1 {
		t.Fatalf("commit %v %v", counts, err)
	}
	// same file again: nothing new
	p2, _ := ParseStatement([]byte(paypalCSV), nil)
	if c := s.PreviewImport(acc, p2); c["duplicate"] != 3 {
		t.Errorf("preview again %v", c)
	}
	counts, _ = s.CommitImport(u.ID, acc, p2, false)
	if counts["added"] != 0 || counts["duplicate"] != 3 {
		t.Errorf("re-import %v", counts)
	}
	// the card deposit is a transfer: not income
	ms, _ := s.Summary(u.ID, "2026-11")
	if ms.IncomeCents != 4000 || ms.ExpenseCents != 1299 {
		t.Errorf("summary income %d expense %d", ms.IncomeCents, ms.ExpenseCents)
	}
	if m := s.LoadMapping(acc); m == nil || m.Profile != "paypal" {
		t.Error("mapping not saved")
	}
}

const bankCSV = "Account: Example Checking ****0000\nExported 11/25/2026\n\n" +
	"Posted Date,Description,Debit,Credit,Balance\n" +
	"11/18/2026,STRAIGHTTALK*AIRTIME 800-000-0000,45.00,,955.00\n" +
	"11/19/2026,PAYROLL DEPOSIT,,1200.00,2155.00\n" +
	"11/19/2026,COFFEE PLACE,4.50,,2150.50\n" +
	"11/19/2026,COFFEE PLACE,4.50,,2146.00\n"

func TestBankCSVHashAndBillMatch(t *testing.T) {
	s, u, acc := setupFin(t)
	b := Bill{Name: "Straight Talk", AmountCents: 3500, StartDate: "2026-10-20", Freq: "monthly", AccountID: &acc}
	s.SaveBill(u.ID, &b)
	p, err := ParseStatement([]byte(bankCSV), nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Mapping.Debit < 0 || p.Mapping.Credit < 0 || len(p.Rows) != 4 {
		t.Fatalf("mapping %+v rows %+v", p.Mapping, p.Rows)
	}
	if p.Rows[2].ExternalID == p.Rows[3].ExternalID {
		t.Error("two identical coffees got the same id")
	}
	c, err := s.CommitImport(u.ID, acc, p, false)
	if err != nil || c["added"] != 4 {
		t.Fatalf("commit %v %v", c, err)
	}
	dues, _ := s.BillDues(u.ID, at("2026-11-20"), at("2026-11-20"))
	if len(dues) != 1 || !dues[0].Paid || dues[0].PaidCents != 4500 {
		t.Fatalf("bill not matched: %+v", dues)
	}
	nb, _ := s.GetBill(u.ID, b.ID)
	if nb.AmountCents != 4500 {
		t.Errorf("bill amount not updated: %d", nb.AmountCents)
	}
	p2, _ := ParseStatement([]byte(bankCSV), nil)
	if c, _ := s.CommitImport(u.ID, acc, p2, false); c["added"] != 0 {
		t.Errorf("re-import added %v", c)
	}
	// un-paying keeps the imported charge
	s.UnpayBill(u.ID, b.ID, "2026-11-20")
	if txs, _ := s.ListTransactions(u.ID, TxFilter{}); len(txs) != 4 {
		t.Errorf("unpay deleted an imported charge: %d left", len(txs))
	}
}

const ofx = `OFXHEADER:100
DATA:OFXSGML
<OFX><BANKMSGSRSV1><STMTTRNRS><STMTRS><BANKTRANLIST>
<STMTTRN><TRNTYPE>DEBIT<DTPOSTED>20261115120000<TRNAMT>-23.45<FITID>F1001<NAME>GROCERY TOWN<MEMO>POS
<STMTTRN><TRNTYPE>XFER<DTPOSTED>20261116<TRNAMT>-100.00<FITID>F1002<NAME>TRANSFER TO PAYPAL
</BANKTRANLIST></STMTRS></STMTTRNRS></BANKMSGSRSV1></OFX>`

func TestOFX(t *testing.T) {
	p, err := ParseStatement([]byte(ofx), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Rows) != 2 || p.Rows[0].ExternalID != "F1001" || p.Rows[0].AmountCents != -2345 || p.Rows[0].Date != "2026-11-15" || !p.Rows[1].Transfer {
		t.Errorf("%+v", p.Rows)
	}
}

func TestAutopayThenImportIsOneCharge(t *testing.T) {
	s, u, acc := setupFin(t)
	b := Bill{Name: "Streamy", AmountCents: 1299, StartDate: "2026-11-20", Freq: "monthly", Autopay: true, AccountID: &acc}
	s.SaveBill(u.ID, &b)
	s.RunReminders(at("2026-11-21T09:00"), nil) // autopay records the expense
	tx := Transaction{Date: "2026-11-21", AmountCents: -1299, AccountID: &acc, Payee: "STREAMY.TEST", ExternalID: "X9", Source: "import"}
	st, err := s.AddImported(u.ID, &tx)
	if err != nil || st != "matched" {
		t.Fatalf("got %s %v", st, err)
	}
	if txs, _ := s.ListTransactions(u.ID, TxFilter{}); len(txs) != 1 {
		t.Errorf("double counted: %d", len(txs))
	}
}

func ppEmail(id, subject, body string) *Email {
	return &Email{MessageID: id, From: "PayPal <service@paypal.com>", To: "me.second@example.test", Subject: subject, Date: at("2026-11-24T15:00"), Body: body}
}

func TestEmailPayPalFlow(t *testing.T) {
	s, u, acc := setupFin(t)
	f := &fakeSender{}
	// no rule yet: no account known -> review
	it, err := s.ProcessEmail(1, false, ppEmail("<m1>", "Receipt for your payment to Streamy Video", "You sent $12.99 USD to Streamy Video\nTransaction ID: 1AA11111BB2222222"), f)
	if err != nil || it.Status != "review" || it.AmountCents != -1299 || it.Payee != "Streamy Video" {
		t.Fatalf("no-rule item %+v %v", it, err)
	}
	// rule maps this PayPal address to the account
	r := EmailRule{Name: "PayPal 2", FromContains: "paypal.com", ToContains: "me.second@example.test", Kind: "paypal", AccountID: &acc, Enabled: true}
	if err := s.SaveRule(&r); err != nil {
		t.Fatal(err)
	}
	it, _ = s.ProcessEmail(1, false, ppEmail("<m2>", "You sent a payment", "Hello,\nYou sent $25.00 USD to Pat Friend.\nTransaction ID: 9ZZ99999YY8888888"), f)
	if it.Status != "added" || it.AmountCents != -2500 || it.Payee != "Pat Friend" {
		t.Fatalf("rule item %+v", it)
	}
	// money in
	it, _ = s.ProcessEmail(1, false, ppEmail("<m3>", "You've got money", "Sam Seller sent you $30.00 USD\nTransaction ID: 4KK44444LL5555555"), f)
	if it.Status != "added" || it.AmountCents != 3000 {
		t.Fatalf("money in %+v", it)
	}
	// same message twice is ignored
	if again, _ := s.ProcessEmail(1, false, ppEmail("<m2>", "You sent a payment", "x"), f); again != nil {
		t.Error("message processed twice")
	}
	// later the PayPal CSV holds the same payment id: not added again
	csvRow := "\"Date\",\"Name\",\"Type\",\"Status\",\"Gross\",\"Net\",\"Transaction ID\"\n\"11/24/2026\",\"Pat Friend\",\"General Payment\",\"Completed\",\"-25.00\",\"-25.00\",\"9ZZ99999YY8888888\"\n"
	p, _ := ParseStatement([]byte(csvRow), nil)
	c, _ := s.CommitImport(u.ID, acc, p, false)
	if c["duplicate"] != 1 {
		t.Errorf("email+csv double counted: %v", c)
	}
	// resolve the review item by picking the account
	items, _ := s.ListEmailItems("open", 10)
	if len(items) != 1 {
		t.Fatalf("open items %+v", items)
	}
	res, err := s.ResolveEmailItem(u.ID, items[0].ID, "apply", EmailItem{AccountID: &acc}, nil, f)
	if err != nil || res.Status != "added" {
		t.Fatalf("resolve %+v %v", res, err)
	}
}

func TestEmailPreviewAndBills(t *testing.T) {
	s, u, acc := setupFin(t)
	f := &fakeSender{}
	g := Bill{Name: "GEICO", AmountCents: 13000, StartDate: "2026-12-05", Freq: "monthly", AccountID: &acc}
	s.SaveBill(u.ID, &g)
	notice := &Email{MessageID: "<g1>", From: "GEICO <billing@geico.example>", To: "bills@example.test", Subject: "Your GEICO bill is ready", Date: at("2026-11-24T08:00"),
		Body: HTMLToText("<html><body><p>Policy 0000</p><table><tr><td>Amount due:</td><td>$142.50</td></tr><tr><td>Due date:</td><td>December 5, 2026</td></tr></table></body></html>")}
	// preview: nothing changes
	it, _ := s.ProcessEmail(1, true, notice, f)
	if it.Status != "preview" || it.AmountCents != 14250 || it.DueDate != "2026-12-05" {
		t.Fatalf("preview %+v", it)
	}
	if b, _ := s.GetBill(u.ID, g.ID); b.AmountCents != 13000 {
		t.Error("preview changed the bill")
	}
	notice.MessageID = "<g2>"
	it, _ = s.ProcessEmail(1, false, notice, f)
	if it.Status != "applied" {
		t.Fatalf("notice %+v", it)
	}
	if b, _ := s.GetBill(u.ID, g.ID); b.AmountCents != 14250 {
		t.Errorf("bill amount %d", b.AmountCents)
	}
	if len(f.sent) == 0 || !strings.Contains(f.sent[len(f.sent)-1], "GEICO bill") {
		t.Errorf("no notice notification: %v", f.sent)
	}
	paid := &Email{MessageID: "<g3>", From: "GEICO <billing@geico.example>", Subject: "Thank you for your payment", Date: at("2026-12-04T09:00"), Body: "We received your payment of $142.50."}
	it, _ = s.ProcessEmail(1, false, paid, f)
	dues, _ := s.BillDues(u.ID, at("2026-12-05"), at("2026-12-05"))
	if it.Status != "applied" || !dues[0].Paid {
		t.Fatalf("paid %+v %+v", it, dues)
	}
	if txs, _ := s.ListTransactions(u.ID, TxFilter{}); len(txs) != 0 {
		t.Error("a payment email created a second expense")
	}
	// unrelated mail is not stored
	if x, _ := s.ProcessEmail(1, false, &Email{MessageID: "<n1>", From: "news@example.test", Subject: "Weekly news", Body: "$5.00 off"}, f); x != nil {
		t.Error("stored a non-money email")
	}
}

func TestMailboxSecretNeverReturned(t *testing.T) {
	s, _, _ := setupFin(t)
	m := Mailbox{Label: "Bills", Host: "imap.example.test", Username: "bills@example.test", Secret: "abcd efgh ijkl mnop", Enabled: true, Preview: true}
	if err := s.SaveMailbox(&m); err != nil {
		t.Fatal(err)
	}
	list, _ := s.ListMailboxes()
	b, _ := json.Marshal(list)
	if strings.Contains(string(b), "abcd") || !list[0].HasSecret {
		t.Errorf("secret leaked or missing: %s", b)
	}
	if s.mailboxSecret(m.ID) != "abcdefghijklmnop" {
		t.Error("spaces not removed from app password")
	}
	m.Secret = ""
	m.Label = "Bills 2"
	s.SaveMailbox(&m)
	if s.mailboxSecret(m.ID) != "abcdefghijklmnop" {
		t.Error("empty secret wiped the stored one")
	}
	// scanning with a fake mailbox, twice: second run stores nothing new
	calls := 0
	fake := func(mb Mailbox, secret string, uv, last uint32) ([]Email, uint32, uint32, error) {
		calls++
		if secret != "abcdefghijklmnop" {
			t.Error("wrong secret passed")
		}
		return []Email{*ppEmail("<s1>", "Receipt for your payment to Shop", "$3.00 USD\nTransaction ID: 2BB22222CC3333333")}, 7, 10, nil
	}
	n1, err := s.ScanMailbox(m.ID, fake, nil)
	n2, _ := s.ScanMailbox(m.ID, fake, nil)
	if err != nil || n1 != 1 || n2 != 0 || calls != 2 {
		t.Errorf("scan %d %d %v", n1, n2, err)
	}
}

func TestReadEmailHTMLOnly(t *testing.T) {
	raw := "From: PayPal <service@paypal.com>\r\nTo: me@example.test\r\nSubject: =?UTF-8?Q?Receipt_for_your_payment_to_Caf=C3=A9_Test?=\r\nMessage-ID: <x1@example.test>\r\nDate: Tue, 24 Nov 2026 10:00:00 -0500\r\nMIME-Version: 1.0\r\nContent-Type: text/html; charset=UTF-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n<html><body><p>You sent <b>$9.50&nbsp;USD</b> to Caf=C3=A9 Test</p><p>Transaction ID: 6MM66666NN7777777</p></body></html>\r\n"
	e, err := ReadEmail(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if e.Subject != "Receipt for your payment to Café Test" || !strings.Contains(e.Body, "$9.50 USD") || e.MessageID != "x1@example.test" {
		t.Errorf("%+v", e)
	}
	p := ParseEmail(e, nil, nil)
	if p == nil || p.AmountCents != -950 || p.Payee != "Café Test" || p.Ref != "6MM66666NN7777777" || !p.Confident {
		t.Errorf("parse %+v", p)
	}
}

func TestSeveralFoldersOneMessageOnce(t *testing.T) {
	s, _, acc := setupFin(t)
	m := Mailbox{Label: "Bills", Host: "imap.example.test", Username: "b@example.test", Secret: "pw", Folder: "INBOX,\n Bills , inbox", Enabled: true}
	if err := s.SaveMailbox(&m); err != nil {
		t.Fatal(err)
	}
	if got := Folders(m.Folder); len(got) != 2 || got[0] != "INBOX" || got[1] != "Bills" {
		t.Fatalf("folders %v", got)
	}
	r := EmailRule{Name: "PP", FromContains: "paypal.com", Kind: "paypal", AccountID: &acc, Enabled: true}
	s.SaveRule(&r)
	seen := map[string]uint32{}
	fake := func(mb Mailbox, secret string, uv, last uint32) ([]Email, uint32, uint32, error) {
		seen[mb.Folder] = last
		// the same receipt is in both folders (a Gmail label and the inbox)
		return []Email{*ppEmail("<dup1>", "Receipt for your payment to Shop", "$3.00 USD\nTransaction ID: 2BB22222CC3333333")}, 5, 42, nil
	}
	n, err := s.ScanMailbox(m.ID, fake, nil)
	if err != nil || n != 1 {
		t.Fatalf("stored %d %v", n, err)
	}
	s.ScanMailbox(m.ID, fake, nil)
	if seen["INBOX"] != 42 || seen["Bills"] != 42 {
		t.Errorf("per-folder position not kept: %v", seen)
	}
	if txs, _ := s.ListTransactions(1, TxFilter{}); len(txs) != 1 {
		t.Errorf("message counted %d times", len(txs))
	}
}

func TestWatchFolder(t *testing.T) {
	s, u, acc := setupFin(t)
	root := t.TempDir()
	t.Setenv("HHUB_WATCH_ROOT", root)
	dir := filepath.Join(root, "relay")
	os.MkdirAll(filepath.Join(dir, "2026"), 0o755)
	os.WriteFile(filepath.Join(dir, "2026", "nov.csv"), []byte("Date,Description,Amount\n11/20/2026,TEST SHOP,-9.99\n11/21/2026,PAY,100.00\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not a statement"), 0o644)
	if err := s.SaveWatchFolder(&WatchFolder{Path: filepath.Join(root, "..", "outside"), AccountID: acc, Enabled: true}); err == nil {
		t.Error("folder outside the root accepted")
	}
	w := WatchFolder{Path: "relay", AccountID: acc, Enabled: true}
	if err := s.SaveWatchFolder(&w); err != nil {
		t.Fatal(err)
	}
	if n, err := s.ScanWatchFolder(w.ID, nil); err != nil || n != 2 {
		t.Fatalf("first scan %d %v", n, err)
	}
	if n, _ := s.ScanWatchFolder(w.ID, nil); n != 0 {
		t.Errorf("same file imported again: %d", n)
	}
	// December's file has one row that was already in November's
	os.WriteFile(filepath.Join(dir, "dec.csv"), []byte("Date,Description,Amount\n11/21/2026,PAY,100.00\n12/01/2026,TEST SHOP,-5.00\n"), 0o644)
	if n, _ := s.ScanWatchFolder(w.ID, nil); n != 1 {
		t.Errorf("dec added %d", n)
	}
	if txs, _ := s.ListTransactions(u.ID, TxFilter{}); len(txs) != 3 {
		t.Errorf("transactions %d", len(txs))
	}
	if _, err := os.Stat(filepath.Join(dir, "2026", "nov.csv")); err != nil {
		t.Error("watch folder moved or deleted a file")
	}
}
