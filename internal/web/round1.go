package web

import (
	"html"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"hhub/internal/core"
)

var backupName = regexp.MustCompile(`^household-\d{4}-\d{2}-\d{2}\.db$`)

// routesRound1: income, subscriptions, forecast, backups, one-tap actions.
func (sv *Server) routesRound1() {
	m := sv.mux
	fin := func(p string, h handler) { m.HandleFunc(p, sv.auth(h, "finance")) }
	adm := func(p string, h handler) { m.HandleFunc(p, sv.auth(h, "admin")) }

	fin("GET /api/incomes", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return sv.Store.ListIncomes(u.ID)
	})
	saveIncome := func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		var in core.Income
		if err := readJSON(r, &in); err != nil {
			return nil, err
		}
		in.ID = pathID(r)
		if r.Method == "POST" {
			in.Active = true
		}
		return in, sv.Store.SaveIncome(u.ID, &in)
	}
	fin("POST /api/incomes", saveIncome)
	fin("PUT /api/incomes/{id}", saveIncome)
	fin("DELETE /api/incomes/{id}", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return ok(), sv.Store.DeleteIncome(u.ID, pathID(r))
	})

	fin("GET /api/subscriptions", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return sv.Store.Subscriptions(u.ID)
	})
	fin("POST /api/subscriptions/state", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		var in struct {
			Key    string `json:"key"`
			Status string `json:"status"`
		}
		if err := readJSON(r, &in); err != nil {
			return nil, err
		}
		if in.Status != "" && in.Status != "ignored" {
			return nil, bad("status must be empty or ignored")
		}
		return ok(), sv.Store.SetSubscription(in.Key, in.Status, nil)
	})
	fin("POST /api/subscriptions/bill", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		var in struct {
			Key string `json:"key"`
		}
		if err := readJSON(r, &in); err != nil {
			return nil, err
		}
		return sv.Store.SubscriptionToBill(u.ID, in.Key)
	})

	fin("GET /api/forecast", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		days, _ := strconv.Atoi(r.URL.Query().Get("days"))
		fc, notes, err := sv.Store.Forecast(u.ID, days)
		if err != nil {
			return nil, err
		}
		if notes == nil {
			notes = []string{}
		}
		return map[string]any{"accounts": fc, "notes": notes}, nil
	})

	adm("GET /api/backups", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		list, err := sv.Store.ListBackups()
		set := sv.Store.Settings(true)
		return map[string]any{"backups": list, "dir": sv.Store.BackupDir(), "keep": set["backup_keep"], "hour": set["backup_hour"],
			"copy_dir": set["backup_copy_dir"], "last_copy": set["last_backup_copy"]}, err
	})
	adm("POST /api/backups/run", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		keep, _ := strconv.Atoi(sv.Store.Setting("backup_keep"))
		name, err := sv.Store.RunBackup(keep)
		return map[string]string{"name": name}, err
	})
	adm("GET /api/backups/{name}", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		name := r.PathValue("name")
		if !backupName.MatchString(name) {
			return nil, core.ErrNotFound
		}
		p := filepath.Join(sv.Store.BackupDir(), name)
		if _, err := os.Stat(p); err != nil {
			return nil, core.ErrNotFound
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
		http.ServeFile(w, r, p)
		return nil, nil
	})

	// one-tap "Mark paid" from a phone notification: the token is the only key (no login, no cookie)
	m.HandleFunc("GET /act/{token}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		tok := html.EscapeString(r.PathValue("token"))
		w.Write([]byte(`<!doctype html><meta name="viewport" content="width=device-width"><title>Household Hub</title>
<body style="font:17px system-ui;margin:2rem"><form method="post" action="/act/` + tok + `"><p>Mark this bill paid?</p><button style="font-size:1.1rem;padding:.6rem 1.2rem">Mark paid</button></form></body>`))
	})
	m.HandleFunc("POST /act/{token}", func(w http.ResponseWriter, r *http.Request) {
		msg, err := sv.Store.UseActionToken(r.PathValue("token"))
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if err != nil {
			code := 400
			if err == core.ErrNotFound {
				code = 404
				err = errNotValid
			}
			w.WriteHeader(code)
			w.Write([]byte(err.Error()))
			return
		}
		w.Write([]byte(msg))
	})
}

var errNotValid = httpErr{404, "this link is not valid"}
