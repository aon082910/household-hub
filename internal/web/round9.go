package web

import (
	"net/http"
	"strconv"

	"hhub/internal/core"
)

// routesRound9: mileage log, estimated taxes.
func (sv *Server) routesRound9() {
	fin := func(p string, h handler) { sv.mux.HandleFunc(p, sv.auth(h, "finance")) }

	fin("GET /api/trips", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		y, _ := strconv.Atoi(r.URL.Query().Get("year"))
		if y == 0 {
			y = sv.Store.Now().Year()
		}
		trips, err := sv.Store.ListTrips(y)
		miles, cents := sv.Store.MileageTotal(y, "")
		return map[string]any{"trips": trips, "miles": miles, "deduction_cents": cents, "rate": sv.Store.Setting("mileage_rate")}, err
	})
	saveTrip := func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		var t core.Trip
		if err := readJSON(r, &t); err != nil {
			return nil, err
		}
		t.ID = pathID(r)
		return t, sv.Store.SaveTrip(u.ID, &t)
	}
	fin("POST /api/trips", saveTrip)
	fin("PUT /api/trips/{id}", saveTrip)
	fin("DELETE /api/trips/{id}", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return ok(), sv.Store.DeleteTrip(u.ID, pathID(r))
	})
	fin("GET /api/tax/estimated", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		y, _ := strconv.Atoi(r.URL.Query().Get("year"))
		return sv.Store.EstimatedTaxPlan(u.ID, y)
	})
	fin("PUT /api/tax/settings", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		var in struct {
			Rate        string `json:"est_tax_rate"`
			MileageRate string `json:"mileage_rate"`
			Reminders   string `json:"est_tax_reminders"`
		}
		if err := readJSON(r, &in); err != nil {
			return nil, err
		}
		if v, err := strconv.ParseFloat(in.Rate, 64); err != nil || v <= 0 || v > 80 {
			return nil, bad("set-aside percent must be 1 to 80")
		}
		if v, err := strconv.ParseFloat(in.MileageRate, 64); err != nil || v <= 0 || v > 5 {
			return nil, bad("mileage rate must be dollars per mile, like 0.70")
		}
		if in.Reminders != "0" {
			in.Reminders = "1"
		}
		return ok(), sv.Store.SaveSettings(map[string]string{"est_tax_rate": in.Rate, "mileage_rate": in.MileageRate, "est_tax_reminders": in.Reminders})
	})
}
