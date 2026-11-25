package web

import (
	"io/fs"
	"net/http"

	"hhub/internal/core"
)

// routesRound6: calendar subscriptions, document vault, wall display.
func (sv *Server) routesRound6() {
	m := sv.mux
	fin := func(p string, h handler) { m.HandleFunc(p, sv.auth(h, "finance")) }
	adm := func(p string, h handler) { m.HandleFunc(p, sv.auth(h, "admin")) }

	adm("GET /api/feeds", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) { return sv.Store.ListFeeds() })
	saveFeed := func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		var f core.CalendarFeed
		if err := readJSON(r, &f); err != nil {
			return nil, err
		}
		f.ID = pathID(r)
		if err := sv.Store.SaveFeed(&f); err != nil {
			return nil, err
		}
		n, err := sv.Store.FetchFeed(f.ID, nil)
		if err != nil {
			return map[string]any{"feed": f, "error": err.Error()}, nil
		}
		return map[string]any{"feed": f, "items": n}, nil
	}
	adm("POST /api/feeds", saveFeed)
	adm("PUT /api/feeds/{id}", saveFeed)
	adm("DELETE /api/feeds/{id}", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return ok(), sv.Store.DeleteFeed(pathID(r))
	})
	adm("POST /api/feeds/{id}/fetch", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		n, err := sv.Store.FetchFeed(pathID(r), nil)
		return map[string]int{"items": n}, err
	})

	fin("GET /api/documents", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return sv.Store.ListDocuments(u)
	})
	saveDoc := func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		var d core.Document
		if err := readJSON(r, &d); err != nil {
			return nil, err
		}
		d.ID = pathID(r)
		return d, sv.Store.SaveDocument(u, &d)
	}
	fin("POST /api/documents", saveDoc)
	fin("PUT /api/documents/{id}", saveDoc)
	fin("DELETE /api/documents/{id}", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return ok(), sv.Store.DeleteDocument(u, pathID(r))
	})
	fin("GET /api/documents/{id}/files", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		id := pathID(r)
		return sv.Store.ListAttachments(u.ID, nil, &id)
	})
	fin("POST /api/documents/{id}/files", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		id := pathID(r)
		docs, err := sv.Store.ListDocuments(u)
		if err != nil {
			return nil, err
		}
		found := false
		for _, d := range docs {
			found = found || d.ID == id
		}
		if !found {
			return nil, core.ErrNotFound
		}
		name, data, err := readUpload(r)
		if err != nil {
			return nil, err
		}
		return sv.Store.AddAttachment(u.ID, "document", nil, &id, name, data)
	})

	adm("GET /api/displays", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return sv.Store.ListDisplayTokens()
	})
	adm("POST /api/displays", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		var in struct{ Label string }
		readJSON(r, &in)
		t, err := sv.Store.NewDisplayToken(in.Label)
		return map[string]string{"path": "/display/" + t}, err
	})
	adm("DELETE /api/displays/{id}", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return ok(), sv.Store.RevokeDisplayToken(r.PathValue("id"))
	})

	// the wall display itself: its own token, read-only, shared items only, never a sign-in
	sub, _ := fs.Sub(uiFS, "ui")
	m.HandleFunc("GET /display/{token}", func(w http.ResponseWriter, r *http.Request) {
		b, err := fs.ReadFile(sub, "display.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Write(b)
	})
	m.HandleFunc("GET /api/display/{token}", func(w http.ResponseWriter, r *http.Request) {
		d, err := sv.Store.Display(r.PathValue("token"))
		if err != nil {
			writeJSON(w, 404, map[string]string{"error": "this display link was removed"})
			return
		}
		writeJSON(w, 200, d)
	})
}
