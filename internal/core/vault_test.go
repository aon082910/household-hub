package core

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const sampleICS = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Test//EN\r\n" +
	"BEGIN:VEVENT\r\nUID:practice@test\r\nSUMMARY:Soccer practice\r\nDTSTART;TZID=America/Chicago:20261103T170000\r\nDTEND;TZID=America/Chicago:20261103T183000\r\n" +
	"RRULE:FREQ=WEEKLY;BYDAY=TU,TH;UNTIL=20261130T235959Z\r\nEXDATE;TZID=America/Chicago:20261126T170000\r\n" +
	"BEGIN:VALARM\r\nTRIGGER:-PT15M\r\nACTION:DISPLAY\r\nEND:VALARM\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:practice@test\r\nRECURRENCE-ID;TZID=America/Chicago:20261119T170000\r\nSUMMARY:Soccer (moved)\r\nDTSTART;TZID=America/Chicago:20261120T170000\r\nDURATION:PT1H\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:break@test\r\nSUMMARY:Thanksgiving\\, no school\r\nDTSTART;VALUE=DATE:20261126\r\nDTEND;VALUE=DATE:20261128\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:odd@test\r\nSUMMARY:Second Tuesday club\r\nDTSTART:20261110T230000Z\r\nRRULE:FREQ=MONTHLY;BYDAY=2TU\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:x@test\r\nSUMMARY:Cancelled game\r\nSTATUS:CANCELLED\r\nDTSTART:20261112T230000Z\r\nEND:VEVENT\r\n" +
	"END:VCALENDAR\r\n"

func TestParseICS(t *testing.T) {
	items, skipped, err := ParseICS(sampleICS, at("2026-11-01"), at("2026-12-31"))
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 1 {
		t.Errorf("skipped %d (the 2nd-Tuesday rule should be skipped, not guessed)", skipped)
	}
	var titles []string
	for _, it := range items {
		titles = append(titles, it.Start.In(time.UTC).Format("01-02 15:04")+" "+it.Title)
	}
	got := strings.Join(titles, " | ")
	// Tue/Thu Nov 3..26: 3,5,10,12,17,19(moved to 20),24,26(excluded) -> 7 practices incl. the moved one
	if strings.Count(got, "Soccer practice") != 6 || !strings.Contains(got, "11-20 23:00 Soccer (moved)") || strings.Contains(got, "Cancelled") {
		t.Errorf("items: %s", got)
	}
	if !strings.Contains(got, "Thanksgiving, no school") {
		t.Errorf("all-day missing: %s", got)
	}
}

func TestFeedIntoCalendar(t *testing.T) {
	s, u, _ := setupFin(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(sampleICS)) }))
	defer srv.Close()
	f := CalendarFeed{Name: "School", URL: srv.URL, Enabled: true}
	if err := s.SaveFeed(&f); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveFeed(&CalendarFeed{Name: "Bad", URL: "file:///etc/passwd"}); err == nil {
		t.Error("non-http feed accepted")
	}
	n, err := s.FetchFeed(f.ID, srv.Client())
	if err != nil || n < 7 {
		t.Fatalf("fetch %d %v", n, err)
	}
	occ, _ := s.Calendar(u, at("2026-11-26"), at("2026-11-27"), false)
	found := false
	for _, o := range occ {
		if o.Source == "feed" && o.Title == "Thanksgiving, no school" && o.End == "2026-11-27" {
			found = true
		}
	}
	if !found {
		t.Errorf("feed event not on calendar: %+v", occ)
	}
	ics, _ := s.ICS(u)
	if strings.Contains(ics, "Thanksgiving") {
		t.Error("imported events re-exported in the feed")
	}
}

func TestDocumentsAndDisplay(t *testing.T) {
	s, mom, _ := setupFin(t) // 2026-11-25
	dad, _ := s.CreateUser("dad", "Dad", "password1", RoleAdult, "")
	kid, _ := s.CreateUser("kid", "Kid", "password1", RoleKid, "")
	reg := Document{Title: "Car registration", Kind: "vehicle", Expires: "2026-12-25", RemindDays: "30,7", Private: false}
	if err := s.SaveDocument(mom, &reg); err != nil {
		t.Fatal(err)
	}
	pass := Document{Title: "Passport", Kind: "id", Private: true}
	s.SaveDocument(mom, &pass)
	if l, _ := s.ListDocuments(dad); len(l) != 1 || l[0].Title != "Car registration" || *l[0].DaysLeft != 30 {
		t.Errorf("dad sees %+v", l)
	}
	if l, _ := s.ListDocuments(kid); len(l) != 0 {
		t.Error("kid sees documents")
	}
	if err := s.SaveDocument(kid, &Document{Title: "x"}); err != ErrForbidden {
		t.Error("kid saved a document")
	}
	pdf := []byte("%PDF-1.4\n%test\n")
	a, err := s.AddAttachment(mom.ID, "document", nil, &pass.ID, "passport.pdf", pdf)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.GetAttachment(dad.ID, a.ID); err == nil {
		t.Error("dad opened a private document file")
	}
	if _, _, err := s.GetAttachment(mom.ID, a.ID); err != nil {
		t.Error("owner cannot open own document", err)
	}
	// expiry reminder: 30 days before, once, to adults (shared)
	f := &fakeSender{}
	s.DailyChecks(at("2026-11-25T10:00"), f)
	s.DailyChecks(at("2026-11-25T11:00"), f)
	n := 0
	for _, x := range f.sent {
		if strings.Contains(x, "Car registration expires") {
			n++
		}
	}
	if n != 2 { // mom + dad, once each
		t.Errorf("doc reminders %d: %v", n, f.sent)
	}
	// wall display: shared things only, no money
	s.SaveEvent(mom.ID, &Event{Title: "Family dinner", Start: "2026-11-25T18:00"})
	s.SaveEvent(mom.ID, &Event{Title: "Secret", Start: "2026-11-25T19:00", Private: true})
	l := List{Name: "Groceries"}
	s.SaveList(mom.ID, &l)
	s.AddItems(mom.ID, l.ID, "milk", nil, "")
	tok, _ := s.NewDisplayToken("Kitchen")
	if _, err := s.Display("wrong-token-wrong-token"); err == nil {
		t.Error("bad display token accepted")
	}
	d, err := s.Display(tok)
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, o := range d.Events {
		titles = append(titles, o.Title)
	}
	if strings.Contains(strings.Join(titles, ","), "Secret") || !strings.Contains(strings.Join(titles, ","), "Family dinner") || len(d.Lists) != 1 || len(d.Lists[0].Items) != 1 {
		t.Errorf("display %+v", d)
	}
}
