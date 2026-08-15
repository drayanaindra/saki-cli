package adapter

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/drayanaindra/saki-cli/backend/infra"
	"github.com/drayanaindra/saki-cli/backend/usecase"
)

// F5 · E14·P4 slice 2: adapter-level tests for GET /api/screenshots (list) + GET /api/screenshot
// (serve). The domain/usecase/infra suites prove the pure resolve/walk; these lock the HTTP wiring —
// the {"shots":[…]} JSON shape, byte-identical png serving with image/png, EMPTY-body rejections
// (parity with the TS .end(), distinct from proto's JSON error), and the OriginGuard (🔒 BR5).

// shotServer stands up a real ContentFS-backed screenshot sub-handler over a temp repo holding
// screenshots/shot.png. Returns the server + the repo cwd.
func shotServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	cwd := t.TempDir()
	dir := filepath.Join(cwd, "screenshots")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "shot.png"), pngBody, 0o644); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	NewScreenshotHandler(usecase.NewScreenshotService(infra.ContentFS{})).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, cwd
}

// AC 2.1: list returns a {"shots":[…]} JSON object (NOT a bare array) of repo-relative rels.
func TestScreenshotsRoute_listShape(t *testing.T) {
	srv, cwd := shotServer(t)
	res := get(t, srv.URL+"/api/screenshots?cwd="+cwd, "")
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	var body struct {
		Shots []string `json:"shots"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Shots) != 1 || body.Shots[0] != filepath.Join("screenshots", "shot.png") {
		t.Fatalf("shots = %v, want [screenshots/shot.png]", body.Shots)
	}
}

// AC 2.1: no .png under any SHOT_DIR → {"shots":[]} with 200 (not 404/500).
func TestScreenshotsRoute_empty(t *testing.T) {
	srv := func() *httptest.Server {
		mux := http.NewServeMux()
		NewScreenshotHandler(usecase.NewScreenshotService(infra.ContentFS{})).Register(mux)
		s := httptest.NewServer(mux)
		t.Cleanup(s.Close)
		return s
	}()
	res := get(t, srv.URL+"/api/screenshots?cwd="+t.TempDir(), "")
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	b, _ := io.ReadAll(res.Body)
	if !bytes.Contains(b, []byte(`"shots":[]`)) {
		t.Fatalf("body = %s, want {\"shots\":[]}", b)
	}
}

// AC 2.3(b): a missing cwd → 422 with EMPTY body.
func TestScreenshotsRoute_missingCwd_422empty(t *testing.T) {
	srv, _ := shotServer(t)
	res := get(t, srv.URL+"/api/screenshots", "")
	defer res.Body.Close()
	if res.StatusCode != 422 {
		t.Fatalf("status = %d, want 422", res.StatusCode)
	}
	if b, _ := io.ReadAll(res.Body); len(b) != 0 {
		t.Fatalf("expected empty body, got %s", b)
	}
}

// AC 2.2: serve — 200 byte-identical .png with Content-Type image/png.
func TestScreenshotRoute_serves_png(t *testing.T) {
	srv, cwd := shotServer(t)
	res := get(t, srv.URL+"/api/screenshot?cwd="+cwd+"&rel=screenshots/shot.png", "")
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); ct != "image/png" {
		t.Fatalf("Content-Type = %q, want image/png", ct)
	}
	if b, _ := io.ReadAll(res.Body); !bytes.Equal(b, pngBody) {
		t.Fatalf("png body not byte-identical: %v", b)
	}
}

// AC 2.3(a): a non-.png rel → 422 EMPTY body, no bytes.
func TestScreenshotRoute_nonPng_422empty(t *testing.T) {
	srv, cwd := shotServer(t)
	res := get(t, srv.URL+"/api/screenshot?cwd="+cwd+"&rel=screenshots/shot.jpg", "")
	defer res.Body.Close()
	if res.StatusCode != 422 {
		t.Fatalf("status = %d, want 422", res.StatusCode)
	}
	if b, _ := io.ReadAll(res.Body); len(b) != 0 {
		t.Fatalf("rejected request must have empty body, got %s", b)
	}
}

// AC 2.3(b): a missing rel → 422 EMPTY body.
func TestScreenshotRoute_missingRel_422(t *testing.T) {
	srv, cwd := shotServer(t)
	res := get(t, srv.URL+"/api/screenshot?cwd="+cwd, "")
	defer res.Body.Close()
	if res.StatusCode != 422 {
		t.Fatalf("status = %d, want 422", res.StatusCode)
	}
}

// BR3: a well-formed-but-missing screenshot → 404 (not 200/500).
func TestScreenshotRoute_missing_404(t *testing.T) {
	srv, cwd := shotServer(t)
	res := get(t, srv.URL+"/api/screenshot?cwd="+cwd+"&rel=screenshots/nope.png", "")
	defer res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatalf("status = %d, want 404", res.StatusCode)
	}
}

// AC 2.3(c): a cross-origin Origin header → 403 (🔒 BR5, OriginGuard) on BOTH routes, never served.
func TestScreenshotRoutes_crossOrigin_403(t *testing.T) {
	srv, cwd := shotServer(t)
	for _, url := range []string{
		srv.URL + "/api/screenshots?cwd=" + cwd,
		srv.URL + "/api/screenshot?cwd=" + cwd + "&rel=screenshots/shot.png",
	} {
		res := get(t, url, "http://evil.example.com")
		if res.StatusCode != 403 {
			res.Body.Close()
			t.Fatalf("cross-origin %s status = %d, want 403", url, res.StatusCode)
		}
		if b, _ := io.ReadAll(res.Body); bytes.Equal(b, pngBody) {
			res.Body.Close()
			t.Fatalf("cross-origin request must not emit file bytes")
		}
		res.Body.Close()
	}
}
