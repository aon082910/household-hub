package core

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Roles. A kid sees the calendar and their own notifications, never the finances.
const (
	RoleAdmin = "admin"
	RoleAdult = "adult"
	RoleKid   = "kid"
)

// User is a household member.
type User struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Color       string `json:"color"`
	Role        string `json:"role"`
	Email       string `json:"email"`
	Phone       string `json:"phone"`
	SMSGateway  string `json:"sms_gateway"`
	NtfyTopic   string `json:"ntfy_topic"`
	WebhookURL  string `json:"webhook_url"`
	Channels    string `json:"channels"`
	Active      bool   `json:"active"`
}

// HasChannel reports whether the user turned on a delivery channel.
func (u *User) HasChannel(c string) bool {
	for _, x := range strings.Split(u.Channels, ",") {
		if strings.TrimSpace(x) == c {
			return true
		}
	}
	return false
}

// CanFinance reports whether the user may see money pages.
func (u *User) CanFinance() bool { return u.Role == RoleAdmin || u.Role == RoleAdult }

var (
	ErrNotFound  = errors.New("not found")
	ErrBadLogin  = errors.New("wrong username or password")
	ErrForbidden = errors.New("not allowed")
	usernameRe   = regexp.MustCompile(`^[A-Za-z0-9._-]{2,32}$`)
	dummyHash, _ = bcrypt.GenerateFromPassword([]byte("no-such-user"), bcrypt.DefaultCost)
)

const userCols = `id,username,display_name,color,role,email,phone,sms_gateway,ntfy_topic,webhook_url,channels,active`

func scanUser(r interface{ Scan(...any) error }) (*User, error) {
	u := &User{}
	var act int
	err := r.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Color, &u.Role, &u.Email, &u.Phone, &u.SMSGateway, &u.NtfyTopic, &u.WebhookURL, &u.Channels, &act)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	u.Active = act == 1
	return u, err
}

// UserCount returns how many users exist (0 = first-run setup needed).
func (s *Store) UserCount() int {
	var n int
	s.DB.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n
}

// CreateUser adds a member. Password must be at least 8 characters.
func (s *Store) CreateUser(username, display, password, role, color string) (*User, error) {
	username = strings.TrimSpace(username)
	if !usernameRe.MatchString(username) {
		return nil, errors.New("username: 2-32 letters, digits, dot, dash or underscore")
	}
	if len(password) < 8 {
		return nil, errors.New("password must be at least 8 characters")
	}
	if role != RoleAdmin && role != RoleAdult && role != RoleKid {
		role = RoleAdult
	}
	if strings.TrimSpace(display) == "" {
		display = username
	}
	if color == "" {
		color = "#4f7cff"
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	res, err := s.DB.Exec(`INSERT INTO users(username,display_name,color,role,pass_hash,ics_token,created) VALUES(?,?,?,?,?,?,?)`,
		username, display, color, role, string(h), randToken(16), s.nowStr())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, errors.New("that username is taken")
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetUser(id)
}

// GetUser loads one user.
func (s *Store) GetUser(id int64) (*User, error) {
	return scanUser(s.DB.QueryRow(`SELECT `+userCols+` FROM users WHERE id=?`, id))
}

// ListUsers returns every member.
func (s *Store) ListUsers() ([]*User, error) {
	rows, err := s.DB.Query(`SELECT ` + userCols + ` FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UpdateProfile saves a user's own editable fields.
func (s *Store) UpdateProfile(u *User) error {
	if strings.TrimSpace(u.DisplayName) == "" {
		return errors.New("name is required")
	}
	_, err := s.DB.Exec(`UPDATE users SET display_name=?,color=?,email=?,phone=?,sms_gateway=?,ntfy_topic=?,webhook_url=?,channels=? WHERE id=?`,
		strings.TrimSpace(u.DisplayName), u.Color, strings.TrimSpace(u.Email), strings.TrimSpace(u.Phone), strings.TrimSpace(u.SMSGateway),
		strings.TrimSpace(u.NtfyTopic), strings.TrimSpace(u.WebhookURL), u.Channels, u.ID)
	return err
}

// SetRole changes role/active (admin only). The last active admin cannot be demoted or disabled.
func (s *Store) SetRole(id int64, role string, active bool) error {
	if role != RoleAdmin && role != RoleAdult && role != RoleKid {
		return errors.New("bad role")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if role != RoleAdmin || !active {
		var n int
		s.DB.QueryRow(`SELECT COUNT(*) FROM users WHERE role='admin' AND active=1 AND id<>?`, id).Scan(&n)
		if n == 0 {
			return errors.New("keep at least one active admin")
		}
	}
	_, err := s.DB.Exec(`UPDATE users SET role=?, active=? WHERE id=?`, role, b2i(active), id)
	if err == nil && !active {
		s.DB.Exec(`DELETE FROM sessions WHERE user_id=?`, id)
	}
	return err
}

// SetPassword replaces a password (existing sessions stay signed in).
func (s *Store) SetPassword(id int64, password string) error {
	if len(password) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(`UPDATE users SET pass_hash=? WHERE id=?`, string(h), id)
	return err
}

// CheckPassword verifies a user's current password.
func (s *Store) CheckPassword(id int64, password string) bool {
	var h string
	if s.DB.QueryRow(`SELECT pass_hash FROM users WHERE id=?`, id).Scan(&h) != nil {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(h), []byte(password)) == nil
}

// Login checks credentials and returns the user.
func (s *Store) Login(username, password string) (*User, error) {
	var id int64
	var h string
	var act int
	err := s.DB.QueryRow(`SELECT id, pass_hash, active FROM users WHERE username=?`, strings.TrimSpace(username)).Scan(&id, &h, &act)
	if err != nil {
		bcrypt.CompareHashAndPassword(dummyHash, []byte(password)) // same timing as a real user
		return nil, ErrBadLogin
	}
	if act != 1 || bcrypt.CompareHashAndPassword([]byte(h), []byte(password)) != nil {
		return nil, ErrBadLogin
	}
	return s.GetUser(id)
}

func randToken(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

// NewSession creates a login token. kind is "web" (cookie, 30 days) or "device" (Windows notifier, 1 year).
func (s *Store) NewSession(userID int64, kind, label string) (string, error) {
	t := randToken(32)
	life := 30 * 24 * time.Hour
	if kind == "device" {
		life = 365 * 24 * time.Hour
	}
	now := s.Now()
	_, err := s.DB.Exec(`INSERT INTO sessions(token_hash,user_id,kind,label,created,expires,last_seen) VALUES(?,?,?,?,?,?,?)`,
		hashToken(t), userID, kind, label, now.Format(time.RFC3339), now.Add(life).Format(time.RFC3339), now.Format(time.RFC3339))
	return t, err
}

// SessionUser resolves a token to its active user.
func (s *Store) SessionUser(token string) (*User, string, error) {
	if token == "" {
		return nil, "", ErrNotFound
	}
	var uid int64
	var kind, exp string
	err := s.DB.QueryRow(`SELECT user_id, kind, expires FROM sessions WHERE token_hash=?`, hashToken(token)).Scan(&uid, &kind, &exp)
	if err != nil {
		return nil, "", ErrNotFound
	}
	if t, _ := time.Parse(time.RFC3339, exp); s.Now().After(t) {
		s.DB.Exec(`DELETE FROM sessions WHERE token_hash=?`, hashToken(token))
		return nil, "", ErrNotFound
	}
	u, err := s.GetUser(uid)
	if err != nil || !u.Active {
		return nil, "", ErrNotFound
	}
	s.DB.Exec(`UPDATE sessions SET last_seen=? WHERE token_hash=?`, s.nowStr(), hashToken(token))
	return u, kind, nil
}

// EndSession deletes one token.
func (s *Store) EndSession(token string) { s.DB.Exec(`DELETE FROM sessions WHERE token_hash=?`, hashToken(token)) }

// Device is a registered notifier (a Windows PC) shown so the user can revoke it.
type Device struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Created  string `json:"created"`
	LastSeen string `json:"last_seen"`
}

// ListDevices lists a user's device tokens (id = first 12 chars of the hash).
func (s *Store) ListDevices(userID int64) ([]Device, error) {
	rows, err := s.DB.Query(`SELECT token_hash,label,created,last_seen FROM sessions WHERE user_id=? AND kind='device' ORDER BY created`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		var d Device
		var h string
		rows.Scan(&h, &d.Label, &d.Created, &d.LastSeen)
		d.ID = h[:12]
		out = append(out, d)
	}
	return out, nil
}

// RevokeDevice removes a device token by its short id.
func (s *Store) RevokeDevice(userID int64, id string) error {
	_, err := s.DB.Exec(`DELETE FROM sessions WHERE user_id=? AND kind='device' AND substr(token_hash,1,12)=?`, userID, id)
	return err
}

// ICSToken returns (and optionally rotates) the user's private calendar-feed token.
func (s *Store) ICSToken(userID int64, rotate bool) string {
	if rotate {
		s.DB.Exec(`UPDATE users SET ics_token=? WHERE id=?`, randToken(16), userID)
	}
	var t string
	s.DB.QueryRow(`SELECT ics_token FROM users WHERE id=?`, userID).Scan(&t)
	return t
}

// UserByICSToken resolves a feed token.
func (s *Store) UserByICSToken(t string) (*User, error) {
	if len(t) < 16 {
		return nil, ErrNotFound
	}
	u, err := scanUser(s.DB.QueryRow(`SELECT `+userCols+` FROM users WHERE ics_token=?`, t))
	if err != nil || !u.Active {
		return nil, ErrNotFound
	}
	return u, nil
}
