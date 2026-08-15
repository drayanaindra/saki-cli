package adapter

import (
	"net/http"

	"github.com/drayanaindra/saki-cli/backend/usecase"
)

// ScreenshotHandler serves the two Go-owned screenshot routes (F5 · E14·P4 slice 2). Like ProtoHandler
// it mounts as a SUB-handler onto the main mux via Register (called from cmd) — avoiding a NewHandler
// arg churn across all its call sites (PRD §8 Assumes).
type ScreenshotHandler struct {
	svc usecase.ScreenshotService
}

// NewScreenshotHandler wires the screenshot usecase into the sub-handler.
func NewScreenshotHandler(svc usecase.ScreenshotService) ScreenshotHandler {
	return ScreenshotHandler{svc: svc}
}

// Register mounts the two screenshot routes onto mux, both OriginGuard-wrapped (🔒 BR5 — read-parity
// with the F4 content routes: a cross-origin request is 403'd, never served).
func (s ScreenshotHandler) Register(mux *http.ServeMux) {
	mux.Handle("GET /api/screenshots", OriginGuard(http.HandlerFunc(s.list)))
	mux.Handle("GET /api/screenshot", OriginGuard(http.HandlerFunc(s.serve)))
}

// list mirrors apps/server GET /api/screenshots (index.ts:523): a missing cwd → 422 with an EMPTY
// body (parity with res.status(422).end()); else 200 `{"shots":[...]}` newest-first repo-relative rels.
func (s ScreenshotHandler) list(w http.ResponseWriter, r *http.Request) {
	cwd := r.URL.Query().Get("cwd")
	if cwd == "" {
		w.WriteHeader(http.StatusUnprocessableEntity) // empty body, matching the TS .end()
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"shots": s.svc.List(cwd)})
}

// serve mirrors apps/server GET /api/screenshot (index.ts:531): resolve (cwd, rel); on rejection write
// the status with an EMPTY body (parity with res.status(r.status).end() — 422 bad/missing-param/escape,
// 404 missing), NOT proto's JSON error; on success stream the bytes with Content-Type image/png.
func (s ScreenshotHandler) serve(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	res := s.svc.Resolve(q.Get("cwd"), q.Get("rel"))
	if res.Status != http.StatusOK {
		w.WriteHeader(res.Status) // empty body, matching the TS .end()
		return
	}
	defer res.Content.Close()
	w.Header().Set("Content-Type", "image/png")
	http.ServeContent(w, r, res.Path, res.ModTime, res.Content)
}
