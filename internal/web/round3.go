package web

import (
	"net/http"
	"strconv"

	"hhub/internal/core"
)

// routesRound3: goals, reports, debt payoff.
func (sv *Server) routesRound3() {
	fin := func(p string, h handler) { sv.mux.HandleFunc(p, sv.auth(h, "finance")) }

	fin("GET /api/goals", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return sv.Store.ListGoals(u.ID)
	})
	saveGoal := func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		var g core.Goal
		if err := readJSON(r, &g); err != nil {
			return nil, err
		}
		g.ID = pathID(r)
		return g, sv.Store.SaveGoal(u.ID, &g)
	}
	fin("POST /api/goals", saveGoal)
	fin("PUT /api/goals/{id}", saveGoal)
	fin("DELETE /api/goals/{id}", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return ok(), sv.Store.DeleteGoal(u.ID, pathID(r))
	})
	fin("POST /api/goals/{id}/add", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		var in struct {
			Cents int64  `json:"cents"`
			Note  string `json:"note"`
		}
		if err := readJSON(r, &in); err != nil {
			return nil, err
		}
		return ok(), sv.Store.AddToGoal(u.ID, pathID(r), in.Cents, in.Note)
	})

	fin("GET /api/reports/trends", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		n, _ := strconv.Atoi(r.URL.Query().Get("months"))
		return sv.Store.Trends(u.ID, n)
	})
	fin("GET /api/reports/networth", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		n, _ := strconv.Atoi(r.URL.Query().Get("months"))
		return sv.Store.NetWorth(u.ID, n)
	})
	fin("GET /api/reports/year", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		y, _ := strconv.Atoi(r.URL.Query().Get("year"))
		return sv.Store.Year(u.ID, y)
	})
	fin("GET /api/debts", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		debts, err := sv.Store.Debts(u.ID)
		if err != nil {
			return nil, err
		}
		extra, _ := core.ParseCents(r.URL.Query().Get("extra"))
		if extra < 0 {
			extra = 0
		}
		return map[string]any{"debts": debts, "snowball": core.Payoff(debts, extra, "snowball"), "avalanche": core.Payoff(debts, extra, "avalanche")}, nil
	})
}
