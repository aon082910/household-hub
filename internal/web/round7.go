package web

import (
	"net/http"

	"hhub/internal/core"
)

// routesRound7: search, recently deleted, daily summary preview.
func (sv *Server) routesRound7() {
	user := func(p string, h handler) { sv.mux.HandleFunc(p, sv.auth(h, "")) }
	fin := func(p string, h handler) { sv.mux.HandleFunc(p, sv.auth(h, "finance")) }

	user("GET /api/search", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return sv.Store.Search(u, r.URL.Query().Get("q"))
	})
	user("GET /api/me/digest", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return sv.Store.DigestFor(u, sv.Store.Now()), nil
	})
	fin("GET /api/trash", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) { return sv.Store.ListTrash(u) })
	fin("POST /api/trash/{id}/restore", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		kind, err := sv.Store.RestoreTrash(u, pathID(r))
		return map[string]string{"kind": kind}, err
	})
}
