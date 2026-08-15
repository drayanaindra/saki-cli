package adapter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drayanaindra/saki-cli/backend/domain"
	"github.com/drayanaindra/saki-cli/backend/infra"
	"github.com/drayanaindra/saki-cli/backend/usecase"
)

// --- fakes for the three ports ---

type fakeProfileScanner struct{ dirs []string }

func (f fakeProfileScanner) Home() (string, error)    { return "/home/me", nil }
func (f fakeProfileScanner) DirNames(string) []string { return f.dirs }

type fakeEnvStateFS struct{ dir bool }

func (fakeEnvStateFS) Home() string                        { return "/home/me" }
func (fakeEnvStateFS) ReadMarker(string) *domain.EnvMarker { return nil }
func (f fakeEnvStateFS) HasClaudeDir(string) bool          { return f.dir }
func (fakeEnvStateFS) HasClaudeMd(string) bool             { return false }

type fakeOSA struct{}

func (fakeOSA) ChooseFolder() (int, string, string) { return 0, "/x", "" }

func newResidualTestHandler(goos, projectHome string) ResidualHandler {
	return NewResidualHandler(
		usecase.NewProfilesService(fakeProfileScanner{dirs: []string{".claude-work"}}),
		usecase.NewEnvStateService(fakeEnvStateFS{dir: true}),
		usecase.NewPickFolderService(fakeOSA{}, goos),
		usecase.NewNewProjectService(infra.OSProjectFS{}, projectHome),
	)
}

func serveResidual(t *testing.T, goos, method, target, origin string) *httptest.ResponseRecorder {
	t.Helper()
	return serveResidualBody(t, goos, method, target, origin, "", t.TempDir())
}

func serveResidualBody(t *testing.T, goos, method, target, origin, body, projectHome string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	newResidualTestHandler(goos, projectHome).Register(mux)
	var reqBody *strings.Reader
	if body != "" {
		reqBody = strings.NewReader(body)
	} else {
		reqBody = strings.NewReader("")
	}
	req := httptest.NewRequest(method, target, reqBody)
	req.Host = "localhost:5180"
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestProfilesRoute(t *testing.T) {
	rec := serveResidual(t, "linux", "GET", "/api/profiles", "")
	if rec.Code != 200 {
		t.Fatalf("profiles status = %d, want 200", rec.Code)
	}
	var got []usecase.Profile
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("profiles body not a JSON array: %v", err)
	}
	if len(got) != 2 || got[0].Name != "default" || got[1].Name != "work" {
		t.Fatalf("unexpected profiles payload: %+v", got)
	}
}

func TestEnvStateRoute(t *testing.T) {
	ok := serveResidual(t, "linux", "GET", "/api/env-state?cwd=/repo", "")
	if ok.Code != 200 {
		t.Fatalf("env-state status = %d, want 200", ok.Code)
	}
	var body struct {
		State string `json:"state"`
	}
	_ = json.Unmarshal(ok.Body.Bytes(), &body)
	if body.State != "foreign" {
		t.Fatalf("env-state state = %q, want foreign", body.State)
	}

	missing := serveResidual(t, "linux", "GET", "/api/env-state", "")
	if missing.Code != 422 {
		t.Fatalf("env-state missing cwd status = %d, want 422", missing.Code)
	}
}

func TestPickFolderRoute_nonDarwin501(t *testing.T) {
	rec := serveResidual(t, "linux", "GET", "/api/pick-folder", "")
	if rec.Code != 501 {
		t.Fatalf("pick-folder non-darwin status = %d, want 501", rec.Code)
	}
}

func TestResidualRoutes_originGuarded(t *testing.T) {
	// A cross-origin request is 403'd on all routes (BR7 parity with the TS global originGuard).
	rec := serveResidual(t, "linux", "GET", "/api/profiles", "http://evil.com")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin GET status = %d, want 403", rec.Code)
	}
	post := serveResidualBody(t, "linux", "POST", "/api/new-project", "http://evil.com", `{"name":"x"}`, t.TempDir())
	if post.Code != http.StatusForbidden {
		t.Fatalf("cross-origin POST /api/new-project status = %d, want 403", post.Code)
	}
}

func TestNewProjectRoute_scaffolds200(t *testing.T) {
	base := t.TempDir()
	body := `{"base":"` + base + `","name":"myapp","template":"empty"}`
	rec := serveResidualBody(t, "linux", "POST", "/api/new-project", "", body, t.TempDir())
	if rec.Code != 200 {
		t.Fatalf("new-project status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Path != filepath.Join(base, "myapp") {
		t.Fatalf("path = %q", resp.Path)
	}
	if _, err := os.Stat(filepath.Join(resp.Path, ".git")); err != nil {
		t.Fatalf("scaffolded target is not a git repo: %v", err)
	}
}

func TestNewProjectRoute_existing409(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "dup"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"base":"` + base + `","name":"dup","template":"empty"}`
	rec := serveResidualBody(t, "linux", "POST", "/api/new-project", "", body, t.TempDir())
	if rec.Code != 409 {
		t.Fatalf("existing target status = %d, want 409", rec.Code)
	}
}

func TestNewProjectRoute_unknownTemplate400(t *testing.T) {
	body := `{"base":"/tmp","name":"x","template":"django"}`
	rec := serveResidualBody(t, "linux", "POST", "/api/new-project", "", body, t.TempDir())
	if rec.Code != 400 {
		t.Fatalf("unknown template status = %d, want 400", rec.Code)
	}
}

func TestNewProjectRoute_invalidName400(t *testing.T) {
	body := `{"base":"/tmp","name":"../escape","template":"empty"}`
	rec := serveResidualBody(t, "linux", "POST", "/api/new-project", "", body, t.TempDir())
	if rec.Code != 400 {
		t.Fatalf("traversal name status = %d, want 400", rec.Code)
	}
}

// A non-string template (e.g. 5) is NOT a valid kind → 400 (parity: TS `body.template ?? 'empty'`
// passes 5 through to isTemplateKind → 400), rather than silently defaulting to empty.
func TestNewProjectRoute_nonStringTemplate400(t *testing.T) {
	body := `{"base":"/tmp","name":"x","template":5}`
	rec := serveResidualBody(t, "linux", "POST", "/api/new-project", "", body, t.TempDir())
	if rec.Code != 400 {
		t.Fatalf("non-string template status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

// An absent template defaults to `empty` and scaffolds (parity: `?? 'empty'`).
func TestNewProjectRoute_absentTemplateDefaultsEmpty(t *testing.T) {
	base := t.TempDir()
	body := `{"base":"` + base + `","name":"noTmpl"}`
	rec := serveResidualBody(t, "linux", "POST", "/api/new-project", "", body, t.TempDir())
	if rec.Code != 200 {
		t.Fatalf("absent template status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
}
