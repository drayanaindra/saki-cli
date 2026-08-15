package adapter

import (
	"encoding/base64"
	"mime"
	"net/http"
	"path/filepath"

	"github.com/drayanaindra/saki-cli/backend/usecase"
)

// ProtoHandler serves the Go-owned proto asset route (F5 · E14·P4). It is mounted as a SUB-handler
// onto the main mux via Register (called from cmd) rather than threaded through NewHandler — a 16th
// service arg would churn all 9 NewHandler call sites for no gain, so the PRD (§8 Assumes) sanctions
// "a small proto sub-handler to avoid re-touching the signature".
type ProtoHandler struct {
	asset usecase.ProtoAssetService
}

// NewProtoHandler wires the proto-asset usecase into the sub-handler.
func NewProtoHandler(asset usecase.ProtoAssetService) ProtoHandler {
	return ProtoHandler{asset: asset}
}

// Register mounts the proto serve route(s) onto mux, OriginGuard-wrapped (🔒 BR5 — read-parity with
// the F4 content routes: a cross-origin request is 403'd, never served).
func (p ProtoHandler) Register(mux *http.ServeMux) {
	// Go 1.22 {dir}/{rest...} wildcard reproduces the TS :dir/* splat (index.ts:544): {dir} is the
	// base64url cwd, {rest...} the rel — path-based (NOT ?cwd=&rel=) so the gallery's relative
	// <img src="shot.png"> references resolve as sibling requests to this same route.
	mux.Handle("GET /api/proto/{dir}/{rest...}", OriginGuard(http.HandlerFunc(p.protoAsset)))
}

// protoAsset mirrors apps/server GET /api/proto/:dir/*: base64url-decode {dir} → cwd (422 on a bad
// encoding), take {rest...} as the rel, resolve+serve via the usecase. 422 on a bad/escaping rel
// (🔒 BR1), 404 on a well-formed-but-missing file (BR3), else the bytes with an extension-correct
// Content-Type (mime.TypeByExtension). A raw `..` in the URL is pre-empted non-200 by ServeMux
// cleanPath before reaching here.
func (p ProtoHandler) protoAsset(w http.ResponseWriter, r *http.Request) {
	cwd, ok := decodeProtoDir(r.PathValue("dir"))
	if !ok {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "bad dir encoding"})
		return
	}
	res := p.asset.Resolve(cwd, r.PathValue("rest"))
	if res.Status != http.StatusOK {
		writeJSON(w, res.Status, map[string]any{"error": "proto asset not served"})
		return
	}
	defer res.Content.Close()
	ct := mime.TypeByExtension(filepath.Ext(res.Path))
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	http.ServeContent(w, r, res.Path, res.ModTime, res.Content)
}

// decodeProtoDir decodes the base64url {dir} segment → the repo cwd. The frontend encodes it with
// btoa+url-swap+strip-padding (api.ts:614) → base64url WITHOUT padding, so RawURLEncoding is the
// primary decoder; URLEncoding is a padded-input fallback (parity with Node's padding-tolerant
// Buffer.from base64). An empty segment or an undecodable value → not ok (→ 422).
func decodeProtoDir(dir string) (string, bool) {
	if dir == "" {
		return "", false
	}
	if b, err := base64.RawURLEncoding.DecodeString(dir); err == nil {
		return string(b), true
	}
	if b, err := base64.URLEncoding.DecodeString(dir); err == nil {
		return string(b), true
	}
	return "", false
}
