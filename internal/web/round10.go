package web

import (
	"net/http"

	"hhub/internal/core"
)

// routesRound10: shared costs, rollover budgets, yearly costs, statement check.
func (sv *Server) routesRound10() {
	fin := func(p string, h handler) { sv.mux.HandleFunc(p, sv.auth(h, "finance")) }

	fin("GET /api/shared", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		list, bal, pay, err := sv.Store.SharedCosts()
		return map[string]any{"costs": list, "balances": bal, "payments": pay}, err
	})
	saveShared := func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		var c core.SharedCost
		if err := readJSON(r, &c); err != nil {
			return nil, err
		}
		c.ID = pathID(r)
		return c, sv.Store.SaveSharedCost(u.ID, &c)
	}
	fin("POST /api/shared", saveShared)
	fin("PUT /api/shared/{id}", saveShared)
	fin("DELETE /api/shared/{id}", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return ok(), sv.Store.DeleteSharedCost(u.ID, pathID(r))
	})
	fin("PUT /api/categories/{id}/rollover", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		var in struct {
			On bool `json:"on"`
		}
		if err := readJSON(r, &in); err != nil {
			return nil, err
		}
		return ok(), sv.Store.SetRollover(pathID(r), in.On)
	})
	fin("GET /api/annual", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		list, per, err := sv.Store.ListAnnualCosts(u.ID)
		return map[string]any{"costs": list, "per_month_cents": per}, err
	})
	saveAnnual := func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		var a core.AnnualCost
		if err := readJSON(r, &a); err != nil {
			return nil, err
		}
		a.ID = pathID(r)
		return a, sv.Store.SaveAnnualCost(u.ID, &a)
	}
	fin("POST /api/annual", saveAnnual)
	fin("PUT /api/annual/{id}", saveAnnual)
	fin("DELETE /api/annual/{id}", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return ok(), sv.Store.DeleteAnnualCost(u.ID, pathID(r))
	})
	fin("POST /api/accounts/{id}/check", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		var in struct {
			Date      string `json:"date"`
			BankCents int64  `json:"bank_cents"`
			Save      bool   `json:"save"`
		}
		if err := readJSON(r, &in); err != nil {
			return nil, err
		}
		return sv.Store.CheckStatement(u.ID, pathID(r), in.Date, in.BankCents, in.Save)
	})
}
