package web

import (
	"io"
	"net/http"
	"net/url"
	"strconv"

	"hhub/internal/core"
)

// routesRound2: category rules, splits, bulk edit, receipts, business tax report.
func (sv *Server) routesRound2() {
	m := sv.mux
	user := func(p string, h handler) { m.HandleFunc(p, sv.auth(h, "")) }
	fin := func(p string, h handler) { m.HandleFunc(p, sv.auth(h, "finance")) }

	fin("GET /api/category-rules", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return sv.Store.ListCategoryRules()
	})
	saveRule := func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		var x core.CategoryRule
		if err := readJSON(r, &x); err != nil {
			return nil, err
		}
		x.ID = pathID(r)
		return x, sv.Store.SaveCategoryRule(&x)
	}
	fin("POST /api/category-rules", saveRule)
	fin("PUT /api/category-rules/{id}", saveRule)
	fin("DELETE /api/category-rules/{id}", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return ok(), sv.Store.DeleteCategoryRule(pathID(r))
	})
	fin("POST /api/category-rules/apply", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return map[string]int{"changed": sv.Store.ApplyRulesToExisting(u.ID)}, nil
	})

	fin("GET /api/transactions/{id}/splits", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return sv.Store.GetSplits(u.ID, pathID(r))
	})
	fin("PUT /api/transactions/{id}/splits", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		var in struct {
			Parts []core.SplitPart `json:"parts"`
		}
		if err := readJSON(r, &in); err != nil {
			return nil, err
		}
		return ok(), sv.Store.SaveSplits(u.ID, pathID(r), in.Parts)
	})
	fin("POST /api/transactions/bulk", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		var in struct {
			IDs    []int64         `json:"ids"`
			Change core.BulkChange `json:"change"`
		}
		if err := readJSON(r, &in); err != nil {
			return nil, err
		}
		n, err := sv.Store.BulkEdit(u.ID, in.IDs, in.Change)
		return map[string]int{"changed": n}, err
	})

	fin("GET /api/transactions/{id}/receipts", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		id := pathID(r)
		return sv.Store.ListAttachments(u.ID, &id, nil)
	})
	fin("POST /api/transactions/{id}/receipts", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		id := pathID(r)
		name, data, err := readUpload(r)
		if err != nil {
			return nil, err
		}
		return sv.Store.AddAttachment(u.ID, "receipt", &id, nil, name, data)
	})
	user("GET /api/files/{id}", sv.serveFile)
	user("DELETE /api/files/{id}", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return ok(), sv.Store.DeleteAttachment(u.ID, pathID(r))
	})

	fin("GET /api/tax/report", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		y, _ := strconv.Atoi(r.URL.Query().Get("year"))
		return sv.Store.BusinessTaxReport(u.ID, y)
	})
	fin("GET /api/tax/report.csv", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		y, _ := strconv.Atoi(r.URL.Query().Get("year"))
		body, err := sv.Store.TaxReportCSV(u.ID, y)
		if err != nil {
			return nil, err
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="business-summary-`+strconv.Itoa(y)+`.csv"`)
		io.WriteString(w, body)
		return nil, nil
	})
}

// readUpload reads the "file" field of a multipart upload (size-limited).
func readUpload(r *http.Request) (string, []byte, error) {
	r.Body = http.MaxBytesReader(nil, r.Body, core.MaxUpload+1<<20)
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		return "", nil, bad("upload failed (max 15 MB)")
	}
	f, h, err := r.FormFile("file")
	if err != nil {
		return "", nil, bad("choose a file")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, core.MaxUpload+1))
	return h.Filename, data, err
}

// serveFile sends an attachment the user may see. Only photos and PDFs exist (checked at upload); they are
// sent with their own type, nosniff and a sandbox policy, so a file can never run as a web page.
func (sv *Server) serveFile(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
	a, full, err := sv.Store.GetAttachment(u.ID, pathID(r))
	if err != nil {
		return nil, err
	}
	disp := "inline"
	if r.URL.Query().Get("download") == "1" {
		disp = "attachment"
	}
	w.Header().Set("Content-Type", a.Mime)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self'; style-src 'unsafe-inline'")
	w.Header().Set("Content-Disposition", disp+"; filename*=UTF-8''"+url.PathEscape(a.Name))
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeFile(w, r, full)
	return nil, nil
}
