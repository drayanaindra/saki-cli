package adapter

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/drayanaindra/saki-cli/backend/domain"
	"github.com/drayanaindra/saki-cli/backend/usecase"
)

// ResidualHandler serves the residual LOCAL read routes ported from apps/server in F7 · E14·P6
// Slice 2: GET /api/profiles, /api/env-state, /api/pick-folder. Like ProtoHandler/ScreenshotHandler
// it mounts as a SUB-handler via Register (called from cmd) so the 16-arg NewHandler signature is
// untouched. All three are OriginGuard-wrapped — the TS local backend applies a GLOBAL originGuard
// (index.ts:53) over every local route, so faithful parity guards these reads too (BR7).
type ResidualHandler struct {
	profiles   usecase.ProfilesService
	env        usecase.EnvStateService
	pick       usecase.PickFolderService
	newProject usecase.NewProjectService
}

// NewResidualHandler wires the residual-route usecases into the sub-handler.
func NewResidualHandler(profiles usecase.ProfilesService, env usecase.EnvStateService, pick usecase.PickFolderService, newProject usecase.NewProjectService) ResidualHandler {
	return ResidualHandler{profiles: profiles, env: env, pick: pick, newProject: newProject}
}

// Register mounts the residual routes onto mux, each OriginGuard-wrapped.
func (h ResidualHandler) Register(mux *http.ServeMux) {
	mux.Handle("GET /api/profiles", OriginGuard(http.HandlerFunc(h.profilesHandler)))
	mux.Handle("GET /api/env-state", OriginGuard(http.HandlerFunc(h.envStateHandler)))
	mux.Handle("GET /api/pick-folder", OriginGuard(http.HandlerFunc(h.pickFolderHandler)))
	mux.Handle("POST /api/new-project", OriginGuard(http.HandlerFunc(h.newProjectHandler))) // F7 slice 3
}

// profilesHandler mirrors apps/server GET /api/profiles (index.ts:337): 200 with the profile list
// (always ≥ the default entry — never an error, parity index.ts:320).
func (h ResidualHandler) profilesHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.profiles.List())
}

// envStateHandler mirrors apps/server GET /api/env-state (index.ts:879): 422 on a missing cwd, else
// 200 {state: none|foreign|mine}.
func (h ResidualHandler) envStateHandler(w http.ResponseWriter, r *http.Request) {
	cwd := r.URL.Query().Get("cwd")
	if cwd == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "cwd is required"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"state": h.env.Classify(cwd)})
}

// pickFolderHandler mirrors apps/server GET /api/pick-folder (index.ts:836): the service returns the
// (status, body) verbatim — 501 off darwin, else 200 {path}/{canceled} or 500 {error}.
func (h ResidualHandler) pickFolderHandler(w http.ResponseWriter, _ *http.Request) {
	status, body := h.pick.Pick()
	writeJSON(w, status, body)
}

// newProjectHandler mirrors apps/server POST /api/new-project (index.ts:858): scaffold a brand-new
// project. Defaults: base `~/Projects`, template `empty` (parity index.ts:860-862). An unknown template
// is 400 BEFORE any filesystem work; then Create's error Code maps INVALID→400, EXISTS→409, else→500,
// success→200 {path}.
func (h ResidualHandler) newProjectHandler(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, maxBody))
	var req struct {
		Base     string          `json:"base"`
		Name     string          `json:"name"`
		Template json.RawMessage `json:"template"`
	}
	_ = json.Unmarshal(body, &req) // non-string fields leave zero values → defaults / validation below
	// base default fires when base is absent/blank (parity: `body.base.trim() ? body.base : '~/Projects'`).
	base := req.Base
	if strings.TrimSpace(base) == "" {
		base = "~/Projects"
	}
	// template default fires ONLY when the key is absent or null (parity: `body.template ?? 'empty'`).
	// A present-but-non-default value — an explicit "" OR a non-string like 5 — stays as-is and falls to
	// the unknown-template 400 below (parity: TS passes `5` straight to isTemplateKind → 400 "unknown
	// template: 5"), rather than silently defaulting to empty.
	template := templateFromBody(req.Template)
	if !domain.IsTemplateKind(template) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "unknown template: " + template})
		return
	}
	path, perr := h.newProject.Create(base, req.Name, domain.TemplateKind(template))
	if perr != nil {
		writeJSON(w, projectErrorStatus(perr.Code), map[string]any{"error": perr.Msg})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": path})
}

// templateFromBody resolves the request's `template` field to its string form for the allowlist check
// (parity `body.template ?? 'empty'`): absent (nil) or JSON null → "empty"; a JSON string → its value;
// any other JSON type (number/object/…) → its raw text, which won't match the allowlist → 400.
func templateFromBody(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return string(domain.TemplateEmpty)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

// projectErrorStatus maps a domain.ProjectError code to its HTTP status (parity index.ts:871-873).
func projectErrorStatus(code string) int {
	switch code {
	case domain.CodeInvalid:
		return http.StatusBadRequest
	case domain.CodeExists:
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}
