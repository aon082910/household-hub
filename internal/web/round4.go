package web

import (
	"net/http"
	"strconv"
	"time"

	"hhub/internal/core"
)

// routesRound4: shared lists, chores, allowance, meal plan. Kids can use these (with limits in core).
func (sv *Server) routesRound4() {
	user := func(p string, h handler) { sv.mux.HandleFunc(p, sv.auth(h, "")) }
	fin := func(p string, h handler) { sv.mux.HandleFunc(p, sv.auth(h, "finance")) }

	user("GET /api/lists", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) { return sv.Store.ListLists(u.ID) })
	user("GET /api/lists/{id}", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return sv.Store.GetList(u.ID, pathID(r))
	})
	saveList := func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		var l core.List
		if err := readJSON(r, &l); err != nil {
			return nil, err
		}
		l.ID = pathID(r)
		return l, sv.Store.SaveList(u.ID, &l)
	}
	user("POST /api/lists", saveList)
	user("PUT /api/lists/{id}", saveList)
	user("DELETE /api/lists/{id}", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return ok(), sv.Store.DeleteList(u.ID, pathID(r))
	})
	user("POST /api/lists/{id}/items", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		var in struct {
			Text       string `json:"text"`
			AssignedTo *int64 `json:"assigned_to"`
			DueDate    string `json:"due_date"`
		}
		if err := readJSON(r, &in); err != nil {
			return nil, err
		}
		return sv.Store.AddItems(u.ID, pathID(r), in.Text, in.AssignedTo, in.DueDate)
	})
	user("POST /api/lists/{id}/clear", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return ok(), sv.Store.ClearDone(u.ID, pathID(r))
	})
	user("PUT /api/items/{id}", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		var in struct {
			Done bool `json:"done"`
		}
		if err := readJSON(r, &in); err != nil {
			return nil, err
		}
		return ok(), sv.Store.SetItemDone(u.ID, pathID(r), in.Done)
	})
	user("DELETE /api/items/{id}", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return ok(), sv.Store.DeleteItem(u.ID, pathID(r))
	})

	user("GET /api/chores", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) { return sv.Store.ListChores() })
	user("GET /api/chores/due", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		now := sv.Store.Now()
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
		days, _ := strconv.Atoi(r.URL.Query().Get("days"))
		if days < 1 || days > 31 {
			days = 7
		}
		return sv.Store.ChoresDue(today, today.AddDate(0, 0, days-1))
	})
	saveChore := func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		var c core.Chore
		if err := readJSON(r, &c); err != nil {
			return nil, err
		}
		c.ID = pathID(r)
		if r.Method == "POST" {
			c.Active = true
		}
		return c, sv.Store.SaveChore(u.ID, &c)
	}
	fin("POST /api/chores", saveChore)
	fin("PUT /api/chores/{id}", saveChore)
	fin("DELETE /api/chores/{id}", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return ok(), sv.Store.DeleteChore(pathID(r))
	})
	user("POST /api/chores/{id}/done", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		var in struct {
			Date string `json:"date"`
		}
		if err := readJSON(r, &in); err != nil {
			return nil, err
		}
		return ok(), sv.Store.MarkChoreDone(u, pathID(r), in.Date)
	})
	user("POST /api/chore-done/{id}/decide", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		var in struct {
			Approve bool `json:"approve"`
		}
		if err := readJSON(r, &in); err != nil {
			return nil, err
		}
		return ok(), sv.Store.DecideChore(u, pathID(r), in.Approve)
	})
	user("DELETE /api/chore-done/{id}", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return ok(), sv.Store.UndoChore(u, pathID(r))
	})

	user("GET /api/allowance/{id}", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		bal, list, err := sv.Store.Allowance(u, pathID(r))
		if err != nil {
			return nil, err
		}
		return map[string]any{"balance_cents": bal, "entries": list}, nil
	})
	fin("POST /api/allowance/{id}", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		var in struct {
			Cents int64  `json:"cents"`
			Note  string `json:"note"`
		}
		if err := readJSON(r, &in); err != nil {
			return nil, err
		}
		return ok(), sv.Store.AddAllowance(u, pathID(r), in.Cents, in.Note)
	})

	user("GET /api/meals", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return sv.Store.Meals(r.URL.Query().Get("from"), r.URL.Query().Get("to"))
	})
	saveMeal := func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		var m core.Meal
		if err := readJSON(r, &m); err != nil {
			return nil, err
		}
		m.ID = pathID(r)
		return m, sv.Store.SaveMeal(u.ID, &m)
	}
	user("POST /api/meals", saveMeal)
	user("PUT /api/meals/{id}", saveMeal)
	user("DELETE /api/meals/{id}", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return ok(), sv.Store.DeleteMeal(pathID(r))
	})
	user("POST /api/meals/to-list", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		var in struct {
			ListID int64  `json:"list_id"`
			From   string `json:"from"`
			To     string `json:"to"`
		}
		if err := readJSON(r, &in); err != nil {
			return nil, err
		}
		n, err := sv.Store.MealsToList(u.ID, in.ListID, in.From, in.To)
		return map[string]int{"added": n}, err
	})
}
