package web

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"hhub/internal/core"
)

type tc struct {
	t   *testing.T
	srv *httptest.Server
	c   *http.Client
}

func newTC(t *testing.T) (*tc, *core.Store) {
	s, err := core.Open(filepath.Join(t.TempDir(), "w.db"))
	if err != nil {
		t.Fatal(err)
	}
	sv := New(s, core.NewNotifier(s), "test")
	ts := httptest.NewServer(sv)
	t.Cleanup(func() { ts.Close(); s.Close() })
	return &tc{t: t, srv: ts, c: client()}, s
}

func client() *http.Client { j, _ := cookiejar.New(nil); return &http.Client{Jar: j} }

func (c *tc) do(cl *http.Client, method, path string, body any, hdr map[string]string) (int, map[string]any) {
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, c.srv.URL+path, rd)
	req.Header.Set("X-HHub", "1")
	for k, v := range hdr {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	resp, err := cl.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	var raw any
	json.NewDecoder(resp.Body).Decode(&raw)
	if mm, ok := raw.(map[string]any); ok {
		m = mm
	} else {
		m = map[string]any{"list": raw}
	}
	return resp.StatusCode, m
}

func TestSetupLoginRolesAndCSRF(t *testing.T) {
	c, s := newTC(t)
	code, st := c.do(c.c, "GET", "/api/state", nil, nil)
	if code != 200 || st["setup_needed"] != true {
		t.Fatalf("state %d %v", code, st)
	}
	if code, _ := c.do(c.c, "POST", "/api/setup", map[string]string{"Username": "mom", "DisplayName": "Mom", "Password": "password1", "HouseholdName": "Smiths"}, nil); code != 200 {
		t.Fatal("setup", code)
	}
	if code, _ := c.do(client(), "POST", "/api/setup", map[string]string{"Username": "evil", "Password": "password1"}, nil); code != 409 {
		t.Error("second setup allowed", code)
	}
	// cross-site write without the header is blocked
	if code, _ := c.do(c.c, "POST", "/api/accounts", map[string]any{"name": "x"}, map[string]string{"X-HHub": ""}); code != 403 {
		t.Error("write without X-HHub allowed", code)
	}
	if code, _ := c.do(c.c, "POST", "/api/accounts", map[string]any{"name": "x"}, map[string]string{"Origin": "http://evil.example"}); code != 403 {
		t.Error("foreign origin allowed", code)
	}
	if code, _ := c.do(c.c, "POST", "/api/accounts", map[string]any{"name": "Joint", "opening_cents": 1000}, nil); code != 200 {
		t.Error("account create", code)
	}
	// add a kid; kid cannot see money or admin pages
	if code, _ := c.do(c.c, "POST", "/api/users", map[string]string{"Username": "kid", "DisplayName": "Kid", "Password": "password1", "Role": "kid"}, nil); code != 200 {
		t.Fatal("add kid", code)
	}
	kid := client()
	if code, _ := c.do(kid, "POST", "/api/login", map[string]string{"Username": "kid", "Password": "password1"}, nil); code != 200 {
		t.Fatal("kid login", code)
	}
	for _, p := range []string{"/api/accounts", "/api/bills", "/api/settings", "/api/summary"} {
		if code, _ := c.do(kid, "GET", p, nil, nil); code != 403 {
			t.Errorf("kid got %s: %d", p, code)
		}
	}
	if code, _ := c.do(kid, "POST", "/api/events", map[string]any{"title": "Game", "start": "2026-12-01T18:00"}, nil); code != 200 {
		t.Error("kid cannot add event", code)
	}
	// device token: can read notifications, cannot touch money
	_, d := c.do(c.c, "POST", "/api/me/devices", map[string]string{"Label": "PC"}, nil)
	tok, _ := d["token"].(string)
	anon := &http.Client{}
	if code, _ := c.do(anon, "GET", "/api/notifications", nil, map[string]string{"Authorization": "Bearer " + tok}); code != 200 {
		t.Error("device read notifications", code)
	}
	if code, _ := c.do(anon, "GET", "/api/accounts", nil, map[string]string{"Authorization": "Bearer " + tok}); code != 403 {
		t.Error("device read accounts", code)
	}
	// wrong password rate limit
	bad := client()
	last := 0
	for i := 0; i < 10; i++ {
		last, _ = c.do(bad, "POST", "/api/login", map[string]string{"Username": "mom", "Password": "nope-nope"}, nil)
	}
	if last != 429 {
		t.Error("no rate limit", last)
	}
	// settings never return secrets
	s.SaveSettings(map[string]string{"smtp_pass": "secret-value"})
	_, set := c.do(c.c, "GET", "/api/settings", nil, nil)
	if set["smtp_pass"] != core.Mask {
		t.Error("secret leaked", set["smtp_pass"])
	}
	// mailbox secrets never come back, kids cannot see the email queue
	if code, _ := c.do(c.c, "POST", "/api/mailboxes", map[string]any{"label": "Bills", "host": "imap.example.test", "username": "b@example.test", "secret": "app-pass-1234", "enabled": false}, nil); code != 200 {
		t.Error("add mailbox", code)
	}
	req, _ := http.NewRequest("GET", c.srv.URL+"/api/mailboxes", nil)
	resp0, _ := c.c.Do(req)
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp0.Body)
	resp0.Body.Close()
	if strings.Contains(buf.String(), "app-pass") || !strings.Contains(buf.String(), `"has_secret":true`) {
		t.Error("mailbox secret leaked:", buf.String())
	}
	if code, _ := c.do(kid, "GET", "/api/email-items", nil, nil); code != 403 {
		t.Error("kid read email items", code)
	}
	// statement upload (multipart) needs the X-HHub header too
	var mp bytes.Buffer
	mw := multipart.NewWriter(&mp)
	mw.WriteField("account_id", "1")
	fw, _ := mw.CreateFormFile("file", "s.csv")
	fw.Write([]byte("Date,Description,Amount\n11/20/2026,TEST SHOP,-9.99\n"))
	mw.Close()
	for _, hdr := range []string{"1", ""} {
		req, _ := http.NewRequest("POST", c.srv.URL+"/api/import/preview", bytes.NewReader(mp.Bytes()))
		req.Header.Set("Content-Type", mw.FormDataContentType())
		if hdr != "" {
			req.Header.Set("X-HHub", hdr)
		}
		resp, _ := c.c.Do(req)
		want := 200
		if hdr == "" {
			want = 403
		}
		if resp.StatusCode != want {
			t.Errorf("import preview with header %q: %d", hdr, resp.StatusCode)
		}
		resp.Body.Close()
	}
	// ICS feed
	_, ics := c.do(c.c, "GET", "/api/me/ics", nil, nil)
	resp, _ := http.Get(c.srv.URL + ics["path"].(string))
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/calendar") {
		t.Error("ics", resp.StatusCode)
	}
	resp.Body.Close()
	if r, _ := http.Get(c.srv.URL + "/ics/0000000000000000000.ics"); r.StatusCode != 404 {
		t.Error("bad ics token served")
	}
}

func TestKidsCannotReachFiles(t *testing.T) {
	c, s := newTC(t)
	c.do(c.c, "POST", "/api/setup", map[string]string{"Username": "mom", "DisplayName": "Mom", "Password": "password1"}, nil)
	_, acc := c.do(c.c, "POST", "/api/accounts", map[string]any{"name": "A"}, nil)
	_, tx := c.do(c.c, "POST", "/api/transactions", map[string]any{"date": "2026-11-20", "amount_cents": -500, "account_id": acc["id"], "payee": "Shop"}, nil)
	txID := int64(tx["id"].(float64))
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)
	a, err := s.AddAttachment(1, "receipt", &txID, nil, "r.png", png)
	if err != nil {
		t.Fatal(err)
	}
	c.do(c.c, "POST", "/api/users", map[string]string{"Username": "kid", "DisplayName": "Kid", "Password": "password1", "Role": "kid"}, nil)
	kid := client()
	c.do(kid, "POST", "/api/login", map[string]string{"Username": "kid", "Password": "password1"}, nil)
	path := "/api/files/" + strconv.FormatInt(a.ID, 10)
	if code, _ := c.do(kid, "GET", path, nil, nil); code != 403 {
		t.Errorf("kid read a receipt: %d", code)
	}
	if code, _ := c.do(kid, "DELETE", path, nil, nil); code != 403 {
		t.Errorf("kid deleted a receipt: %d", code)
	}
}
