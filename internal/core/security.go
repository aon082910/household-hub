package core

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/png"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	"rsc.io/qr"
)

// ---------- two-step sign-in (TOTP, RFC 6238: 6 digits, 30 seconds, SHA-1, as every authenticator app uses) ----------

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// TOTPCode returns the code for a secret at a time.
func TOTPCode(secret string, t time.Time) string {
	key, err := b32.DecodeString(strings.ToUpper(strings.ReplaceAll(secret, " ", "")))
	if err != nil {
		return ""
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(t.Unix()/30))
	m := hmac.New(sha1.New, key)
	m.Write(msg[:])
	h := m.Sum(nil)
	o := h[len(h)-1] & 0x0f
	v := (binary.BigEndian.Uint32(h[o:o+4]) & 0x7fffffff) % 1000000
	return fmt.Sprintf("%06d", v)
}

// TOTPValid accepts the code of now and of one step before or after (clock drift).
func TOTPValid(secret, code string, now time.Time) bool {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != 6 {
		return false
	}
	for _, d := range []time.Duration{0, -30 * time.Second, 30 * time.Second} {
		if subtle.ConstantTimeCompare([]byte(TOTPCode(secret, now.Add(d))), []byte(code)) == 1 {
			return true
		}
	}
	return false
}

// TwoStepSetup is what the user scans or types into an authenticator app.
type TwoStepSetup struct {
	Secret string `json:"secret"`
	URI    string `json:"uri"`
	QR     string `json:"qr"` // data: URL of a PNG
}

// StartTwoStep makes a new secret (not active until confirmed with a code).
func (s *Store) StartTwoStep(u *User) (*TwoStepSetup, error) {
	raw := make([]byte, 20)
	rand.Read(raw)
	sec := b32.EncodeToString(raw)
	if _, err := s.DB.Exec(`UPDATE users SET totp_secret=? WHERE id=? AND totp_on=0`, sec, u.ID); err != nil {
		return nil, err
	}
	issuer := s.Setting("household_name")
	if issuer == "" {
		issuer = "Household Hub"
	}
	label := url.PathEscape(issuer + ":" + u.Username)
	uri := "otpauth://totp/" + label + "?secret=" + sec + "&issuer=" + url.QueryEscape(issuer) + "&period=30&digits=6"
	out := &TwoStepSetup{Secret: sec, URI: uri}
	if code, err := qr.Encode(uri, qr.M); err == nil {
		// draw the modules ourselves: 6 px each, 4-module white border
		const px, quiet = 6, 4
		side := (code.Size + 2*quiet) * px
		img := image.NewGray(image.Rect(0, 0, side, side))
		for i := range img.Pix {
			img.Pix[i] = 255
		}
		for y := 0; y < code.Size; y++ {
			for x := 0; x < code.Size; x++ {
				if !code.Black(x, y) {
					continue
				}
				for dy := 0; dy < px; dy++ {
					for dx := 0; dx < px; dx++ {
						img.Pix[((y+quiet)*px+dy)*img.Stride+(x+quiet)*px+dx] = 0
					}
				}
			}
		}
		var b bytes.Buffer
		if png.Encode(&b, img) == nil {
			out.QR = "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
		}
	}
	return out, nil
}

// EnableTwoStep turns two-step sign-in on after a correct code and returns 8 one-time recovery codes.
func (s *Store) EnableTwoStep(u *User, code string) ([]string, error) {
	var sec string
	var on int
	s.DB.QueryRow(`SELECT totp_secret,totp_on FROM users WHERE id=?`, u.ID).Scan(&sec, &on)
	if on == 1 {
		return nil, errors.New("already on")
	}
	if sec == "" || !TOTPValid(sec, code, s.Now()) {
		return nil, errors.New("that code is not right; check the time on your phone and try the newest code")
	}
	codes, hashes := make([]string, 8), make([]string, 8)
	for i := range codes {
		c := strings.ToUpper(randToken(5)) // 10 hex chars
		codes[i] = c[:5] + "-" + c[5:]
		h, _ := bcrypt.GenerateFromPassword([]byte(codes[i]), bcrypt.MinCost)
		hashes[i] = string(h)
	}
	_, err := s.DB.Exec(`UPDATE users SET totp_on=1, recovery=? WHERE id=?`, strings.Join(hashes, " "), u.ID)
	s.Audit(u.ID, "two-step sign-in turned on", "", "")
	return codes, err
}

// DisableTwoStep turns it off (the user with their password, or an admin resetting someone).
func (s *Store) DisableTwoStep(userID, byID int64) error {
	_, err := s.DB.Exec(`UPDATE users SET totp_on=0, totp_secret='', recovery='' WHERE id=?`, userID)
	s.Audit(byID, "two-step sign-in turned off", fmt.Sprint("user ", userID), "")
	return err
}

// TwoStepOn reports whether a user must give a code.
func (s *Store) TwoStepOn(userID int64) bool {
	var on int
	s.DB.QueryRow(`SELECT totp_on FROM users WHERE id=?`, userID).Scan(&on)
	return on == 1
}

// CheckSecondStep accepts an authenticator code or an unused recovery code (which is then used up).
func (s *Store) CheckSecondStep(userID int64, code string) bool {
	var sec, rec string
	s.DB.QueryRow(`SELECT totp_secret,recovery FROM users WHERE id=? AND totp_on=1`, userID).Scan(&sec, &rec)
	if sec == "" {
		return false
	}
	if TOTPValid(sec, code, s.Now()) {
		return true
	}
	code = strings.ToUpper(strings.TrimSpace(code))
	if len(code) == 10 {
		code = code[:5] + "-" + code[5:]
	}
	hs := strings.Fields(rec)
	for i, h := range hs {
		if bcrypt.CompareHashAndPassword([]byte(h), []byte(code)) == nil {
			hs = append(hs[:i], hs[i+1:]...)
			s.DB.Exec(`UPDATE users SET recovery=? WHERE id=?`, strings.Join(hs, " "), userID)
			s.Audit(userID, "signed in with a recovery code", fmt.Sprintf("%d left", len(hs)), "")
			return true
		}
	}
	return false
}

// LoginTickets holds the "password was right, now the code" step (5 minutes, 5 tries).
type LoginTickets struct {
	mu sync.Mutex
	m  map[string]*ticket
}

type ticket struct {
	user    int64
	expires time.Time
	tries   int
}

// NewLoginTickets makes an empty ticket store.
func NewLoginTickets() *LoginTickets { return &LoginTickets{m: map[string]*ticket{}} }

// Issue gives a ticket for a user whose password was right.
func (lt *LoginTickets) Issue(user int64) string {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	for k, t := range lt.m {
		if time.Now().After(t.expires) {
			delete(lt.m, k)
		}
	}
	k := randToken(24)
	lt.m[k] = &ticket{user: user, expires: time.Now().Add(5 * time.Minute)}
	return k
}

// Use checks a code for a ticket. ok=true ends the ticket; after 5 wrong codes the ticket is gone.
func (lt *LoginTickets) Use(k string, check func(user int64) bool) (int64, bool) {
	lt.mu.Lock()
	t := lt.m[k]
	if t == nil || time.Now().After(t.expires) {
		delete(lt.m, k)
		lt.mu.Unlock()
		return 0, false
	}
	t.tries++
	if t.tries > 5 {
		delete(lt.m, k)
		lt.mu.Unlock()
		return 0, false
	}
	user := t.user
	lt.mu.Unlock()
	if check(user) {
		lt.mu.Lock()
		delete(lt.m, k)
		lt.mu.Unlock()
		return user, true
	}
	return 0, false
}

// ---------- change log ----------

// AuditEntry is one line of the change log (who did what, never the data itself).
type AuditEntry struct {
	ID     int64  `json:"id"`
	At     string `json:"at"`
	UserID *int64 `json:"user_id"`
	Action string `json:"action"`
	Item   string `json:"item"`
	IP     string `json:"ip"`
}

// Audit records an action. userID 0 = unknown (a failed sign-in).
func (s *Store) Audit(userID int64, action, item, ip string) {
	var u any
	if userID != 0 {
		u = userID
	}
	s.DB.Exec(`INSERT INTO audit_log(at,user_id,action,item,ip) VALUES(?,?,?,?,?)`, s.nowStr(), u, trunc(action, 120), trunc(item, 200), trunc(ip, 60))
}

// ListAudit returns the newest entries (before = id for paging, 0 = newest).
func (s *Store) ListAudit(before int64, limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	q, args := `SELECT id,at,user_id,action,item,ip FROM audit_log`, []any{}
	if before > 0 {
		q += ` WHERE id<?`
		args = append(args, before)
	}
	rows, err := s.DB.Query(q+` ORDER BY id DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		var u nullInt
		rows.Scan(&e.ID, &e.At, &u, &e.Action, &e.Item, &e.IP)
		e.UserID = u.ptr()
		out = append(out, e)
	}
	return out, nil
}

type nullInt struct {
	v  int64
	ok bool
}

func (n *nullInt) Scan(src any) error {
	switch x := src.(type) {
	case int64:
		n.v, n.ok = x, true
	case nil:
		n.ok = false
	}
	return nil
}

func (n nullInt) ptr() *int64 {
	if !n.ok {
		return nil
	}
	v := n.v
	return &v
}

// ---------- reverse proxy ----------

// TrustedProxy reports whether ip is one of the configured proxies ("trusted_proxies": IPs or CIDRs, comma
// separated). Only then are X-Forwarded-For and X-Forwarded-Proto believed.
func (s *Store) TrustedProxy(ip string) bool {
	p := net.ParseIP(ip)
	if p == nil {
		return false
	}
	for _, x := range strings.Split(s.Setting("trusted_proxies"), ",") {
		x = strings.TrimSpace(x)
		if x == "" {
			continue
		}
		if strings.Contains(x, "/") {
			if _, n, err := net.ParseCIDR(x); err == nil && n.Contains(p) {
				return true
			}
		} else if q := net.ParseIP(x); q != nil && q.Equal(p) {
			return true
		}
	}
	return false
}
