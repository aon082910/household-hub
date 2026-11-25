package web

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"hhub/internal/core"
)

func TestTOTPVector(t *testing.T) {
	// RFC 6238 appendix B, SHA-1, T=59s: 94287082 -> last 6 digits
	if got := core.TOTPCode("GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ", time.Unix(59, 0)); got != "287082" {
		t.Errorf("got %s", got)
	}
	if got := core.TOTPCode("GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ", time.Unix(1111111109, 0)); got != "081804" {
		t.Errorf("got %s", got)
	}
}

func TestTwoStepLoginAndAudit(t *testing.T) {
	c, s := newTC(t)
	c.do(c.c, "POST", "/api/setup", map[string]string{"Username": "mom", "DisplayName": "Mom", "Password": "password1"}, nil)
	_, st := c.do(c.c, "POST", "/api/me/2fa/start", nil, nil)
	secret, _ := st["secret"].(string)
	if secret == "" || !strings.HasPrefix(st["qr"].(string), "data:image/png;base64,") {
		t.Fatalf("start %v", st)
	}
	if code, _ := c.do(c.c, "POST", "/api/me/2fa/enable", map[string]string{"Code": "000000"}, nil); code != 400 {
		t.Error("wrong code enabled two-step")
	}
	_, en := c.do(c.c, "POST", "/api/me/2fa/enable", map[string]string{"Code": core.TOTPCode(secret, time.Now())}, nil)
	codes, _ := en["recovery_codes"].([]any)
	if len(codes) != 8 {
		t.Fatalf("enable %v", en)
	}
	// password alone no longer signs in
	cl := client()
	_, r := c.do(cl, "POST", "/api/login", map[string]string{"Username": "mom", "Password": "password1"}, nil)
	if r["need_code"] != true {
		t.Fatalf("login %v", r)
	}
	if code, _ := c.do(cl, "GET", "/api/me", nil, nil); code != 401 {
		t.Error("signed in without the code")
	}
	ticket := r["ticket"].(string)
	if code, _ := c.do(cl, "POST", "/api/login/code", map[string]string{"Ticket": ticket, "Code": "123456"}, nil); code != 401 {
		t.Error("wrong code accepted")
	}
	if code, _ := c.do(cl, "POST", "/api/login/code", map[string]string{"Ticket": ticket, "Code": core.TOTPCode(secret, time.Now())}, nil); code != 200 {
		t.Fatal("right code refused")
	}
	if code, _ := c.do(cl, "GET", "/api/me", nil, nil); code != 200 {
		t.Error("not signed in after the code")
	}
	// recovery code works once
	rc := codes[0].(string)
	for i, want := range []int{200, 401} {
		cl2 := client()
		_, r2 := c.do(cl2, "POST", "/api/login", map[string]string{"Username": "mom", "Password": "password1"}, nil)
		if code, _ := c.do(cl2, "POST", "/api/login/code", map[string]string{"Ticket": r2["ticket"].(string), "Code": rc}, nil); code != want {
			t.Errorf("recovery use %d: %d", i+1, code)
		}
	}
	// change log has sign-ins and the 2fa change, never passwords
	_, au := c.do(c.c, "GET", "/api/audit", nil, nil)
	list, _ := au["list"].([]any)
	joined := ""
	for _, e := range list {
		joined += e.(map[string]any)["action"].(string) + "|" + e.(map[string]any)["item"].(string) + ";"
	}
	if !strings.Contains(joined, "signed in") || !strings.Contains(joined, "two-step sign-in turned on") || strings.Contains(joined, "password1") {
		t.Errorf("audit %s", joined)
	}
	_ = s
}

func TestForwardedForOnlyFromTrustedProxy(t *testing.T) {
	c, s := newTC(t)
	c.do(c.c, "POST", "/api/setup", map[string]string{"Username": "mom", "DisplayName": "Mom", "Password": "password1"}, nil)
	bad := func(xff string) int {
		code := 0
		for i := 0; i < 9; i++ {
			code, _ = c.do(client(), "POST", "/api/login", map[string]string{"Username": "mom", "Password": "nope-nope"}, map[string]string{"X-Forwarded-For": xff})
		}
		return code
	}
	// untrusted: a changing X-Forwarded-For does not dodge the lock
	if code := bad("203.0.113.9"); code != 429 {
		t.Errorf("spoofed header bypassed the limit: %d", code)
	}
	// trusted proxy: each real client is counted on its own
	s.SaveSettings(map[string]string{"trusted_proxies": "127.0.0.1"})
	sv := New(s, core.NewNotifier(s), "t")
	req, _ := http.NewRequest("GET", "/", nil)
	req.RemoteAddr = "127.0.0.1:5000"
	req.Header.Set("X-Forwarded-For", "198.51.100.7, 127.0.0.1")
	if ip := sv.clientIP(req); ip != "198.51.100.7" {
		t.Errorf("client ip %s", ip)
	}
	req.Header.Set("X-Forwarded-Proto", "https")
	if !sv.secureReq(req) {
		t.Error("https from trusted proxy not seen")
	}
	req.RemoteAddr = "192.0.2.50:5000"
	if sv.secureReq(req) || sv.clientIP(req) != "192.0.2.50" {
		t.Error("headers believed from an untrusted address")
	}
}
