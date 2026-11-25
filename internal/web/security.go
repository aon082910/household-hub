package web

import (
	"net/http"
	"strconv"

	"hhub/internal/core"
)

// routesSecurity: two-step sign-in and the change log.
func (sv *Server) routesSecurity() {
	m := sv.mux
	user := func(p string, h handler) { m.HandleFunc(p, sv.auth(h, "")) }
	adm := func(p string, h handler) { m.HandleFunc(p, sv.auth(h, "admin")) }

	m.HandleFunc("POST /api/login/code", sv.loginCode)

	user("GET /api/me/2fa", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return map[string]bool{"on": sv.Store.TwoStepOn(u.ID)}, nil
	})
	user("POST /api/me/2fa/start", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		if sv.Store.TwoStepOn(u.ID) {
			return nil, bad("two-step sign-in is already on")
		}
		return sv.Store.StartTwoStep(u)
	})
	user("POST /api/me/2fa/enable", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		var in struct{ Code string }
		if err := readJSON(r, &in); err != nil {
			return nil, err
		}
		codes, err := sv.Store.EnableTwoStep(u, in.Code)
		if err != nil {
			return nil, err
		}
		return map[string]any{"recovery_codes": codes}, nil
	})
	user("POST /api/me/2fa/disable", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		var in struct{ Password string }
		if err := readJSON(r, &in); err != nil {
			return nil, err
		}
		if !sv.Store.CheckPassword(u.ID, in.Password) {
			return nil, bad("password is wrong")
		}
		return ok(), sv.Store.DisableTwoStep(u.ID, u.ID)
	})

	adm("GET /api/audit", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
		return sv.Store.ListAudit(before, 200)
	})
}
