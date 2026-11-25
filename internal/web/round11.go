package web

import (
	"net/http"
	"os"
	"strings"

	"hhub/internal/core"
)

// routesRound11: receipt reader.
func (sv *Server) routesRound11() {
	fin := func(p string, h handler) { sv.mux.HandleFunc(p, sv.auth(h, "finance")) }

	fin("GET /api/receipts/status", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		return map[string]bool{"available": core.OCRAvailable()}, nil
	})
	// read a photo that is not saved yet (the page then makes the transaction and attaches the same photo)
	fin("POST /api/receipts/read", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		_, data, err := readUpload(r)
		if err != nil {
			return nil, err
		}
		mime := http.DetectContentType(data)
		if i := strings.Index(mime, ";"); i > 0 {
			mime = mime[:i]
		}
		if _, ok := core.AllowedUpload[mime]; !ok {
			return nil, bad("only photos and PDFs can be read")
		}
		text, err := core.ReadImageText(data, mime)
		if err != nil {
			return nil, err
		}
		return core.ParseReceipt(text), nil
	})
	// read a receipt already attached to a transaction
	fin("POST /api/files/{id}/read", func(w http.ResponseWriter, r *http.Request, u *core.User) (any, error) {
		a, full, err := sv.Store.GetAttachment(u.ID, pathID(r))
		if err != nil {
			return nil, err
		}
		data, err := os.ReadFile(full)
		if err != nil {
			return nil, err
		}
		text, err := core.ReadImageText(data, a.Mime)
		if err != nil {
			return nil, err
		}
		return core.ParseReceipt(text), nil
	})
}
