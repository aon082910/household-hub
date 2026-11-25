// Package web is the HTTP API and the embedded web app of Household Hub.
package web

import (
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"hhub/internal/core"
)

//go:embed ui
var uiFS embed.FS

const cookieName = "hhub_session"

// Server serves the API and the web app.
type Server struct {
	Store    *core.Store
	Notifier *core.Notifier
	Version  string
	Fetch    core.MailFetcher // reads mailboxes (IMAP; tests swap it)
	mux      *http.ServeMux

	failMu  sync.Mutex
	fails   map[string][]time.Time
	tickets *core.LoginTickets
}

// New builds the server and its routes.
func New(s *core.Store, n *core.Notifier, version string) *Server {
	sv := &Server{Store: s, Notifier: n, Version: version, Fetch: core.IMAPFetch, mux: http.NewServeMux(), fails: map[string][]time.Time{}, tickets: core.NewLoginTickets()}
	sv.routes()
	sv.routesSecurity()
	sv.routesRound1()
	sv.routesRound2()
	sv.routesRound3()
	sv.routesRound4()
	sv.routesRound6()
	sv.routesRound7()
	sv.routesRound9()
	sv.routesRound10()
	sv.routesRound11()
	return sv
}

type ctxUser struct {
	u    *core.User
	kind string
}

type handler func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error)

// httpErr carries an HTTP status.
type httpErr struct {
	code int
	msg  string
}

func (e httpErr) Error() string { return e.msg }

func bad(msg string) error { return httpErr{400, msg} }

var errForbidden = httpErr{403, "not allowed"}

func (sv *Server) routes() {
	m := sv.mux
	sub, _ := fs.Sub(uiFS, "ui")
	files := http.FileServer(http.FS(sub))
	m.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" || r.URL.Path == "/sw.js" {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
	m.HandleFunc("GET /api/ping", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true, "app": "household-hub", "version": sv.Version})
	})
	m.HandleFunc("GET /api/state", sv.state)
	m.HandleFunc("POST /api/setup", sv.setup)
	m.HandleFunc("POST /api/login", sv.login)
	m.HandleFunc("POST /api/logout", sv.logout)
	m.HandleFunc("GET /ics/{token}", sv.ics)

	user := func(p string, h handler) { m.HandleFunc(p, sv.auth(h, "")) }
	fin := func(p string, h handler) { m.HandleFunc(p, sv.auth(h, "finance")) }
	adm := func(p string, h handler) { m.HandleFunc(p, sv.auth(h, "admin")) }

	// me
	user("GET /api/me", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) { return u, nil })
	user("PUT /api/me", sv.saveMe)
	user("POST /api/me/password", sv.myPassword)
	user("POST /api/me/test-notify", sv.testNotify)
	user("GET /api/me/devices", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return sv.Store.ListDevices(u.ID)
	})
	user("POST /api/me/devices", sv.newDevice)
	user("DELETE /api/me/devices/{id}", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return ok(), sv.Store.RevokeDevice(u.ID, r.PathValue("id"))
	})
	user("GET /api/me/ics", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return map[string]string{"path": "/ics/" + sv.Store.ICSToken(u.ID, false) + ".ics"}, nil
	})
	user("POST /api/me/ics/rotate", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return map[string]string{"path": "/ics/" + sv.Store.ICSToken(u.ID, true) + ".ics"}, nil
	})

	// members
	user("GET /api/users", sv.listUsers)
	adm("POST /api/users", sv.addUser)
	adm("PUT /api/users/{id}", sv.editUser)
	adm("GET /api/settings", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return sv.Store.Settings(true), nil
	})
	adm("PUT /api/settings", sv.saveSettings)
	adm("GET /api/backup", sv.backup)

	// dashboard / notifications / calendar
	user("GET /api/dashboard", sv.dashboard)
	user("GET /api/notifications", sv.notifications)
	user("POST /api/notifications/read", sv.markRead)
	user("POST /api/notifications/clear", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return ok(), sv.Store.ClearNotifications(u.ID)
	})
	user("GET /api/calendar", sv.calendar)
	user("GET /api/events/{id}", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return sv.Store.GetEvent(u.ID, pathID(r))
	})
	user("POST /api/events", sv.saveEvent)
	user("PUT /api/events/{id}", sv.saveEvent)
	user("DELETE /api/events/{id}", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return ok(), sv.Store.DeleteEvent(u.ID, pathID(r))
	})

	// money
	fin("GET /api/summary", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		month := r.URL.Query().Get("month")
		if month == "" {
			month = sv.Store.Now().Format("2006-01")
		}
		return sv.Store.Summary(u.ID, month)
	})
	fin("GET /api/accounts", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return sv.Store.ListAccounts(u.ID)
	})
	fin("POST /api/accounts", sv.saveAccount)
	fin("PUT /api/accounts/{id}", sv.saveAccount)
	fin("DELETE /api/accounts/{id}", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return ok(), sv.Store.DeleteAccount(u.ID, pathID(r))
	})
	fin("GET /api/categories", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return sv.Store.ListCategories()
	})
	fin("POST /api/categories", sv.saveCategory)
	fin("PUT /api/categories/{id}", sv.saveCategory)
	fin("DELETE /api/categories/{id}", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return ok(), sv.Store.DeleteCategory(pathID(r))
	})
	fin("GET /api/transactions", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return sv.Store.ListTransactions(u.ID, txFilter(r))
	})
	fin("GET /api/transactions.csv", sv.exportCSV)
	fin("POST /api/transactions", sv.saveTx)
	fin("PUT /api/transactions/{id}", sv.saveTx)
	fin("DELETE /api/transactions/{id}", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return ok(), sv.Store.DeleteTransaction(u.ID, pathID(r))
	})
	fin("GET /api/bills", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return sv.Store.ListBills(u.ID)
	})
	fin("GET /api/bills/dues", sv.billDues)
	fin("POST /api/bills", sv.saveBill)
	fin("PUT /api/bills/{id}", sv.saveBill)
	fin("DELETE /api/bills/{id}", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return ok(), sv.Store.DeleteBill(u.ID, pathID(r))
	})
	fin("POST /api/bills/{id}/pay", sv.payBill)
	// statement import and email reader
	fin("POST /api/import/preview", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return sv.importFile(r, u, false)
	})
	fin("POST /api/import/commit", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return sv.importFile(r, u, true)
	})
	fin("GET /api/email-items", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return sv.Store.ListEmailItems(r.URL.Query().Get("status"), 300)
	})
	fin("POST /api/email-items/{id}", sv.resolveEmail)
	adm("GET /api/mailboxes", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return sv.Store.ListMailboxes()
	})
	adm("POST /api/mailboxes", sv.saveMailbox)
	adm("PUT /api/mailboxes/{id}", sv.saveMailbox)
	adm("DELETE /api/mailboxes/{id}", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return ok(), sv.Store.DeleteMailbox(pathID(r))
	})
	adm("POST /api/mailboxes/{id}/scan", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		n, err := sv.Store.ScanMailbox(pathID(r), sv.Fetch, sv.Notifier)
		if err != nil {
			return nil, err
		}
		return map[string]int{"stored": n}, nil
	})
	adm("GET /api/mailboxes/{id}/folders", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return sv.Store.ListFolders(pathID(r))
	})
	adm("GET /api/watch-folders", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		ws, err := sv.Store.ListWatchFolders()
		return map[string]any{"folders": ws, "root": core.WatchRoot()}, err
	})
	adm("POST /api/watch-folders", sv.saveWatch)
	adm("PUT /api/watch-folders/{id}", sv.saveWatch)
	adm("DELETE /api/watch-folders/{id}", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return ok(), sv.Store.DeleteWatchFolder(pathID(r))
	})
	adm("POST /api/watch-folders/{id}/scan", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		n, err := sv.Store.ScanWatchFolder(pathID(r), sv.Notifier)
		if err != nil {
			return nil, err
		}
		return map[string]int{"added": n}, nil
	})
	adm("GET /api/watch-folders/{id}/files", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return sv.Store.ListWatchedFiles(pathID(r))
	})
	adm("GET /api/email-rules", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) { return sv.Store.ListRules() })
	adm("POST /api/email-rules", sv.saveRule)
	adm("PUT /api/email-rules/{id}", sv.saveRule)
	adm("DELETE /api/email-rules/{id}", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return ok(), sv.Store.DeleteRule(pathID(r))
	})
	adm("POST /api/email-rules/test", sv.testRule)
	fin("POST /api/bills/{id}/unpay", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		var in struct{ Occ string }
		if err := readJSON(r, &in); err != nil {
			return nil, err
		}
		return ok(), sv.Store.UnpayBill(u.ID, pathID(r), in.Occ)
	})
}

// ServeHTTP adds security headers and logs panics.
func (sv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "same-origin")
	h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("panic %s %s: %v", r.Method, r.URL.Path, rec)
			writeJSON(w, 500, map[string]string{"error": "internal error"})
		}
	}()
	sv.mux.ServeHTTP(w, r)
}

func ok() map[string]bool { return map[string]bool{"ok": true} }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		return bad("bad request body: " + err.Error())
	}
	return nil
}

func pathID(r *http.Request) int64 {
	n, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return n
}

func bearer(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func (sv *Server) currentUser(r *http.Request) (*core.User, string, bool) {
	if t := bearer(r); t != "" {
		u, kind, err := sv.Store.SessionUser(t)
		return u, kind, err == nil
	}
	c, err := r.Cookie(cookieName)
	if err != nil {
		return nil, "", false
	}
	u, kind, err := sv.Store.SessionUser(c.Value)
	return u, kind, err == nil
}

// sameOrigin blocks cross-site writes: cookie requests that change data must carry the X-HHub header (which a
// foreign page cannot add without a CORS preflight) and, when the browser sends Origin, it must match the host.
func sameOrigin(r *http.Request) bool {
	if r.Method == "GET" || r.Method == "HEAD" {
		return true
	}
	if r.Header.Get("X-HHub") != "1" {
		return false
	}
	if o := r.Header.Get("Origin"); o != "" && o != "null" {
		u, err := url.Parse(o)
		if err != nil || !strings.EqualFold(u.Host, r.Host) {
			return false
		}
	}
	return true
}

func (sv *Server) auth(h handler, need string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, kind, ok := sv.currentUser(r)
		if !ok {
			writeJSON(w, 401, map[string]string{"error": "sign in first"})
			return
		}
		if bearer(r) == "" && !sameOrigin(r) {
			writeJSON(w, 403, map[string]string{"error": "cross-site request blocked"})
			return
		}
		// device tokens (Windows notifier) may only read and acknowledge notifications
		if kind == "device" && !(strings.HasPrefix(r.URL.Path, "/api/notifications") || r.URL.Path == "/api/me") {
			writeJSON(w, 403, map[string]string{"error": "device token cannot do that"})
			return
		}
		if (need == "finance" && !u.CanFinance()) || (need == "admin" && u.Role != core.RoleAdmin) {
			writeJSON(w, 403, map[string]string{"error": "not allowed for your role"})
			return
		}
		out, err := h(w, r, u)
		if err == nil && r.Method != "GET" && r.Method != "HEAD" && kind != "device" {
			// change log: who did what to which item, never the data sent
			item := r.PathValue("id")
			if item == "" {
				item = r.PathValue("name")
			}
			sv.Store.Audit(u.ID, r.Pattern, item, sv.clientIP(r))
		}
		if err != nil {
			var he httpErr
			switch {
			case errors.As(err, &he):
				writeJSON(w, he.code, map[string]string{"error": he.msg})
			case errors.Is(err, core.ErrNotFound):
				writeJSON(w, 404, map[string]string{"error": "not found (or private)"})
			default:
				writeJSON(w, 400, map[string]string{"error": err.Error()})
			}
			return
		}
		if out == nil { // handler wrote the response itself
			return
		}
		writeJSON(w, 200, out)
	}
}

func remoteIP(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

// clientIP is the visitor's address. Behind a trusted reverse proxy it is the last address in X-Forwarded-For
// that is not itself a trusted proxy; otherwise the connection's own address (a forged header is ignored).
func (sv *Server) clientIP(r *http.Request) string {
	ip := remoteIP(r)
	if !sv.Store.TrustedProxy(ip) {
		return ip
	}
	parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(parts) - 1; i >= 0; i-- {
		p := strings.TrimSpace(parts[i])
		if p != "" && !sv.Store.TrustedProxy(p) && net.ParseIP(p) != nil {
			return p
		}
	}
	return ip
}

func (sv *Server) tooManyFails(ip string) bool {
	sv.failMu.Lock()
	defer sv.failMu.Unlock()
	cut := time.Now().Add(-10 * time.Minute)
	var keep []time.Time
	for _, t := range sv.fails[ip] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	sv.fails[ip] = keep
	return len(keep) >= 8
}

func (sv *Server) addFail(ip string) {
	sv.failMu.Lock()
	sv.fails[ip] = append(sv.fails[ip], time.Now())
	sv.failMu.Unlock()
}

func (sv *Server) secureReq(r *http.Request) bool {
	return r.TLS != nil || (sv.Store.TrustedProxy(remoteIP(r)) && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"))
}

func (sv *Server) setCookie(w http.ResponseWriter, r *http.Request, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: sv.secureReq(r), MaxAge: maxAge})
}

func (sv *Server) publicSettings() map[string]string {
	all := sv.Store.Settings(true)
	return map[string]string{"household_name": all["household_name"], "currency": all["currency"], "week_start": all["week_start"]}
}

func (sv *Server) state(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"setup_needed": sv.Store.UserCount() == 0, "settings": sv.publicSettings(), "version": sv.Version}
	if u, _, ok := sv.currentUser(r); ok {
		out["user"] = u
		out["unread"] = sv.Store.UnreadCount(u.ID)
		out["latest_notification"] = sv.Store.LatestNotificationID(u.ID)
	}
	writeJSON(w, 200, out)
}

func (sv *Server) setup(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, 403, map[string]string{"error": "cross-site request blocked"})
		return
	}
	var in struct{ Username, DisplayName, Password, HouseholdName string }
	if err := readJSON(r, &in); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	if sv.Store.UserCount() != 0 {
		writeJSON(w, 409, map[string]string{"error": "already set up"})
		return
	}
	u, err := sv.Store.CreateUser(in.Username, in.DisplayName, in.Password, core.RoleAdmin, "#4f7cff")
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	if in.HouseholdName != "" {
		sv.Store.SaveSettings(map[string]string{"household_name": in.HouseholdName})
	}
	tok, _ := sv.Store.NewSession(u.ID, "web", r.UserAgent())
	sv.setCookie(w, r, tok, 30*86400)
	writeJSON(w, 200, map[string]any{"user": u})
}

func (sv *Server) login(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, 403, map[string]string{"error": "cross-site request blocked"})
		return
	}
	ip := sv.clientIP(r)
	if sv.tooManyFails(ip) {
		writeJSON(w, 429, map[string]string{"error": "too many failed sign-ins; wait 10 minutes"})
		return
	}
	var in struct{ Username, Password string }
	if err := readJSON(r, &in); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	u, err := sv.Store.Login(in.Username, in.Password)
	if err != nil {
		sv.addFail(ip)
		sv.Store.Audit(0, "failed sign-in", trimName(in.Username), ip)
		writeJSON(w, 401, map[string]string{"error": err.Error()})
		return
	}
	if sv.Store.TwoStepOn(u.ID) {
		writeJSON(w, 200, map[string]any{"need_code": true, "ticket": sv.tickets.Issue(u.ID)})
		return
	}
	sv.finishLogin(w, r, u, ip)
}

func trimName(s string) string {
	if len(s) > 40 {
		return s[:40]
	}
	return s
}

func (sv *Server) finishLogin(w http.ResponseWriter, r *http.Request, u *core.User, ip string) {
	tok, _ := sv.Store.NewSession(u.ID, "web", r.UserAgent())
	sv.setCookie(w, r, tok, 30*86400)
	sv.Store.Audit(u.ID, "signed in", "", ip)
	writeJSON(w, 200, map[string]any{"user": u})
}

// loginCode is the second step: the authenticator (or a recovery) code for a ticket from login.
func (sv *Server) loginCode(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, 403, map[string]string{"error": "cross-site request blocked"})
		return
	}
	ip := sv.clientIP(r)
	if sv.tooManyFails(ip) {
		writeJSON(w, 429, map[string]string{"error": "too many failed sign-ins; wait 10 minutes"})
		return
	}
	var in struct{ Ticket, Code string }
	if err := readJSON(r, &in); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	uid, ok := sv.tickets.Use(in.Ticket, func(user int64) bool { return sv.Store.CheckSecondStep(user, in.Code) })
	if !ok {
		sv.addFail(ip)
		sv.Store.Audit(0, "wrong sign-in code", "", ip)
		writeJSON(w, 401, map[string]string{"error": "wrong code (or it took too long: sign in again)"})
		return
	}
	u, err := sv.Store.GetUser(uid)
	if err != nil || !u.Active {
		writeJSON(w, 401, map[string]string{"error": "sign in again"})
		return
	}
	sv.finishLogin(w, r, u, ip)
}

func (sv *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		sv.Store.EndSession(c.Value)
	}
	sv.setCookie(w, r, "", -1)
	writeJSON(w, 200, ok())
}

func (sv *Server) ics(w http.ResponseWriter, r *http.Request) {
	tok := strings.TrimSuffix(r.PathValue("token"), ".ics")
	u, err := sv.Store.UserByICSToken(tok)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	body, err := sv.Store.ICS(u)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	io.WriteString(w, body)
}

// ---------- me ----------

func (sv *Server) saveMe(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
	var in core.User
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	in.ID = u.ID
	var ch []string
	for _, c := range core.Channels {
		if in.HasChannel(c) {
			ch = append(ch, c)
		}
	}
	in.Channels = strings.Join(ch, ",")
	if in.WebhookURL != "" && !strings.HasPrefix(in.WebhookURL, "https://") && !strings.HasPrefix(in.WebhookURL, "http://") {
		return nil, bad("webhook must start with https://")
	}
	if err := sv.Store.UpdateProfile(&in); err != nil {
		return nil, err
	}
	return sv.Store.GetUser(u.ID)
}

func (sv *Server) myPassword(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
	var in struct{ Current, New string }
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	if !sv.Store.CheckPassword(u.ID, in.Current) {
		return nil, bad("current password is wrong")
	}
	return ok(), sv.Store.SetPassword(u.ID, in.New)
}

func (sv *Server) testNotify(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
	m := core.Message{Title: "Test from Household Hub", Body: "If you can read this, " + u.DisplayName + "'s notifications work."}
	sv.Store.AddNotification(u.ID, m)
	res := sv.Notifier.Deliver(u, m)
	if res == "" {
		res = "bell only (no outside channel switched on)"
	}
	return map[string]string{"result": res}, nil
}

func (sv *Server) newDevice(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
	var in struct{ Label string }
	readJSON(r, &in)
	if in.Label == "" {
		in.Label = "Windows PC"
	}
	tok, err := sv.Store.NewSession(u.ID, "device", in.Label)
	if err != nil {
		return nil, err
	}
	return map[string]any{"token": tok, "latest": sv.Store.LatestNotificationID(u.ID)}, nil
}

// ---------- members / settings ----------

type member struct {
	ID          int64  `json:"id"`
	DisplayName string `json:"display_name"`
	Color       string `json:"color"`
	Role        string `json:"role"`
	Active      bool   `json:"active"`
	Username    string `json:"username,omitempty"`
}

func (sv *Server) listUsers(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
	all, err := sv.Store.ListUsers()
	if err != nil {
		return nil, err
	}
	out := []member{}
	for _, x := range all {
		m := member{ID: x.ID, DisplayName: x.DisplayName, Color: x.Color, Role: x.Role, Active: x.Active}
		if u.Role == core.RoleAdmin {
			m.Username = x.Username
		}
		out = append(out, m)
	}
	return out, nil
}

func (sv *Server) addUser(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
	var in struct{ Username, DisplayName, Password, Role, Color string }
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	return sv.Store.CreateUser(in.Username, in.DisplayName, in.Password, in.Role, in.Color)
}

func (sv *Server) editUser(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
	var in struct {
		Role     string
		Active   bool
		Password string
		Reset2FA bool
	}
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	id := pathID(r)
	if err := sv.Store.SetRole(id, in.Role, in.Active); err != nil {
		return nil, err
	}
	if in.Reset2FA {
		sv.Store.DisableTwoStep(id, u.ID)
	}
	if in.Password != "" {
		if err := sv.Store.SetPassword(id, in.Password); err != nil {
			return nil, err
		}
	}
	return ok(), nil
}

func (sv *Server) saveSettings(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
	in := map[string]string{}
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	if h, ok := in["bill_remind_hour"]; ok {
		if n, err := strconv.Atoi(h); err != nil || n < 0 || n > 23 {
			return nil, bad("reminder hour must be 0-23")
		}
	}
	if err := sv.Store.SaveSettings(in); err != nil {
		return nil, err
	}
	return sv.Store.Settings(true), nil
}

func (sv *Server) backup(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
	tmp := filepath.Join(os.TempDir(), "hhub-backup-"+strconv.FormatInt(time.Now().UnixNano(), 10)+".db")
	defer os.Remove(tmp)
	if err := sv.Store.Backup(tmp); err != nil {
		return nil, err
	}
	f, err := os.Open(tmp)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="household-hub-`+time.Now().Format("2006-01-02")+`.db"`)
	io.Copy(w, f)
	return nil, nil
}

// ---------- dashboard / notifications / calendar ----------

func (sv *Server) dashboard(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
	now := sv.Store.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	out := map[string]any{"unread": sv.Store.UnreadCount(u.ID), "today": today.Format(core.DateFmt)}
	occ, err := sv.Store.Calendar(u, today, today.AddDate(0, 0, 7), false)
	if err != nil {
		return nil, err
	}
	out["events"] = occ
	if u.CanFinance() {
		sum, err := sv.Store.Summary(u.ID, now.Format("2006-01"))
		if err != nil {
			return nil, err
		}
		out["summary"] = sum
		dues, err := sv.Store.BillDues(u.ID, today.AddDate(0, 0, -60), today.AddDate(0, 0, 30))
		if err != nil {
			return nil, err
		}
		upcoming := []core.BillDue{}
		for _, d := range dues {
			if !d.Paid && (d.Overdue || d.Date >= today.Format(core.DateFmt)) {
				upcoming = append(upcoming, d)
			}
		}
		out["bills"] = upcoming
		accs, err := sv.Store.ListAccounts(u.ID)
		if err != nil {
			return nil, err
		}
		out["accounts"] = accs
	}
	return out, nil
}

func (sv *Server) notifications(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := sv.Store.ListNotifications(u.ID, after, limit)
	if err != nil {
		return nil, err
	}
	return map[string]any{"items": list, "unread": sv.Store.UnreadCount(u.ID)}, nil
}

func (sv *Server) markRead(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
	var in struct{ ID int64 }
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	return ok(), sv.Store.MarkRead(u.ID, in.ID)
}

func dateParam(r *http.Request, k string, def time.Time) (time.Time, error) {
	v := r.URL.Query().Get(k)
	if v == "" {
		return def, nil
	}
	t, err := time.ParseInLocation(core.DateFmt, v, time.Local)
	if err != nil {
		return t, bad(k + " must be YYYY-MM-DD")
	}
	return t, nil
}

func (sv *Server) calendar(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
	now := sv.Store.Now()
	from, err := dateParam(r, "from", time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local))
	if err != nil {
		return nil, err
	}
	to, err := dateParam(r, "to", from.AddDate(0, 1, -1))
	if err != nil {
		return nil, err
	}
	if to.Sub(from) > 400*24*time.Hour || to.Before(from) {
		return nil, bad("range too long")
	}
	return sv.Store.Calendar(u, from, to, r.URL.Query().Get("bills") != "0")
}

func (sv *Server) saveEvent(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
	var e core.Event
	if err := readJSON(r, &e); err != nil {
		return nil, err
	}
	e.ID = pathID(r)
	if err := sv.Store.SaveEvent(u.ID, &e); err != nil {
		return nil, err
	}
	return e, nil
}

// ---------- money ----------

func (sv *Server) saveAccount(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
	var a core.Account
	if err := readJSON(r, &a); err != nil {
		return nil, err
	}
	a.ID = pathID(r)
	if err := sv.Store.SaveAccount(u.ID, &a); err != nil {
		return nil, err
	}
	return a, nil
}

func (sv *Server) saveCategory(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
	var c core.Category
	if err := readJSON(r, &c); err != nil {
		return nil, err
	}
	c.ID = pathID(r)
	if err := sv.Store.SaveCategory(&c); err != nil {
		return nil, err
	}
	return c, nil
}

func txFilter(r *http.Request) core.TxFilter {
	q := r.URL.Query()
	f := core.TxFilter{From: q.Get("from"), To: q.Get("to"), Query: q.Get("q")}
	f.AccountID, _ = strconv.ParseInt(q.Get("account"), 10, 64)
	f.CategoryID, _ = strconv.ParseInt(q.Get("category"), 10, 64)
	f.Limit, _ = strconv.Atoi(q.Get("limit"))
	return f
}

func (sv *Server) exportCSV(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
	body, err := sv.Store.TransactionsCSV(u.ID, txFilter(r))
	if err != nil {
		return nil, err
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="transactions.csv"`)
	io.WriteString(w, body)
	return nil, nil
}

func (sv *Server) saveTx(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
	var t core.Transaction
	if err := readJSON(r, &t); err != nil {
		return nil, err
	}
	t.ID = pathID(r)
	if err := sv.Store.SaveTransaction(u.ID, &t); err != nil {
		return nil, err
	}
	return t, nil
}

func (sv *Server) saveBill(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
	var b core.Bill
	if err := readJSON(r, &b); err != nil {
		return nil, err
	}
	b.ID = pathID(r)
	if r.Method == "POST" {
		b.Active = true
	}
	if err := sv.Store.SaveBill(u.ID, &b); err != nil {
		return nil, err
	}
	return b, nil
}

func (sv *Server) billDues(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
	now := sv.Store.Now()
	from, err := dateParam(r, "from", now.AddDate(0, -1, 0))
	if err != nil {
		return nil, err
	}
	to, err := dateParam(r, "to", now.AddDate(0, 2, 0))
	if err != nil {
		return nil, err
	}
	if to.Sub(from) > 800*24*time.Hour {
		return nil, bad("range too long")
	}
	return sv.Store.BillDues(u.ID, from, to)
}

func (sv *Server) payBill(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
	var in struct {
		Occ         string `json:"occ"`
		PaidDate    string `json:"paid_date"`
		AmountCents int64  `json:"amount_cents"`
		Record      bool   `json:"record"`
	}
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	if _, err := time.Parse(core.DateFmt, in.Occ); err != nil {
		return nil, bad("occ must be YYYY-MM-DD")
	}
	return ok(), sv.Store.PayBill(u.ID, pathID(r), in.Occ, in.PaidDate, in.AmountCents, in.Record)
}

// ---------- import / email ----------

func (sv *Server) importFile(r *http.Request, u *core.User, commit bool) (any, error) {
	r.Body = http.MaxBytesReader(nil, r.Body, 21<<20)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		return nil, bad("upload failed (max 20 MB): " + err.Error())
	}
	acc, _ := strconv.ParseInt(r.FormValue("account_id"), 10, 64)
	accs, err := sv.Store.ListAccounts(u.ID)
	if err != nil {
		return nil, err
	}
	found := false
	for _, a := range accs {
		found = found || a.ID == acc
	}
	if !found {
		return nil, bad("pick an account")
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		return nil, bad("choose a file")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 20<<20))
	if err != nil {
		return nil, err
	}
	var m *core.Mapping
	if js := r.FormValue("mapping"); js != "" {
		m = &core.Mapping{}
		if err := json.Unmarshal([]byte(js), m); err != nil {
			return nil, bad("bad column mapping")
		}
	} else {
		m = sv.Store.LoadMapping(acc)
	}
	p, err := core.ParseStatement(data, m)
	if err != nil && m != nil { // a saved mapping from another bank's file: guess again
		p, err = core.ParseStatement(data, nil)
	}
	if err != nil {
		return nil, bad(err.Error())
	}
	if commit {
		counts, err := sv.Store.CommitImport(u.ID, acc, p, r.FormValue("private") == "1")
		if err != nil {
			return nil, err
		}
		return map[string]any{"counts": counts}, nil
	}
	counts := sv.Store.PreviewImport(acc, p)
	rows := p.Rows
	if len(rows) > 1500 {
		rows = rows[:1500]
	}
	return map[string]any{"format": p.Format, "headers": p.Headers, "mapping": p.Mapping, "rows": rows, "counts": counts, "total": len(p.Rows)}, nil
}

func (sv *Server) resolveEmail(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
	var in struct {
		Action      string `json:"action"`
		Kind        string `json:"kind"`
		AmountCents int64  `json:"amount_cents"`
		Payee       string `json:"payee"`
		AccountID   *int64 `json:"account_id"`
		BillID      *int64 `json:"bill_id"`
		CategoryID  *int64 `json:"category_id"`
	}
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	return sv.Store.ResolveEmailItem(u.ID, pathID(r), in.Action, core.EmailItem{Kind: in.Kind, AmountCents: in.AmountCents, Payee: in.Payee, AccountID: in.AccountID, BillID: in.BillID}, in.CategoryID, sv.Notifier)
}

func (sv *Server) saveMailbox(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
	var m core.Mailbox
	if err := readJSON(r, &m); err != nil {
		return nil, err
	}
	m.ID = pathID(r)
	if err := sv.Store.SaveMailbox(&m); err != nil {
		return nil, err
	}
	m.Secret = ""
	return m, nil
}

func (sv *Server) saveRule(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
	var x core.EmailRule
	if err := readJSON(r, &x); err != nil {
		return nil, err
	}
	x.ID = pathID(r)
	if err := sv.Store.SaveRule(&x); err != nil {
		return nil, err
	}
	return x, nil
}

func (sv *Server) testRule(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
	var in struct{ From, To, Subject, Body string }
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	body := in.Body
	if strings.Contains(body, "</") {
		body = core.HTMLToText(body)
	}
	rules, err := sv.Store.ListRules()
	if err != nil {
		return nil, err
	}
	bills, _ := sv.Store.ListBills(0)
	p := core.ParseEmail(&core.Email{From: in.From, To: in.To, Subject: in.Subject, Body: body}, rules, bills)
	if p == nil {
		return map[string]any{"result": "not a money email: no rule, PayPal sender or bill name matched"}, nil
	}
	return p, nil
}

func (sv *Server) saveWatch(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
	var x core.WatchFolder
	if err := readJSON(r, &x); err != nil {
		return nil, err
	}
	x.ID = pathID(r)
	if err := sv.Store.SaveWatchFolder(&x); err != nil {
		return nil, err
	}
	return x, nil
}
