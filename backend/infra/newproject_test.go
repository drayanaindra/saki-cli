package infra

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drayanaindra/saki-cli/backend/domain"
	"github.com/drayanaindra/saki-cli/backend/usecase"
)

func TestCopyTemplate_writesFilesIncludingDotfiles(t *testing.T) {
	dst := t.TempDir()
	if err := (OSProjectFS{}).CopyTemplate(domain.TemplateViteReactTS, dst); err != nil {
		t.Fatalf("CopyTemplate: %v", err)
	}
	// The `all:` embed prefix must have carried the dotfile; a bare //go:embed would silently skip it.
	for _, rel := range []string{".gitignore", "package.json", "index.html", "src/App.tsx", "src/main.tsx", "tsconfig.json"} {
		if _, err := os.Stat(filepath.Join(dst, rel)); err != nil {
			t.Fatalf("expected template file %q: %v", rel, err)
		}
	}
}

func TestStampPackageName(t *testing.T) {
	dst := t.TempDir()
	if err := (OSProjectFS{}).CopyTemplate(domain.TemplateNextJS, dst); err != nil {
		t.Fatal(err)
	}
	if err := (OSProjectFS{}).StampPackageName(dst, "  my-app  "); err != nil {
		t.Fatalf("StampPackageName: %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(dst, "package.json"))
	var pkg map[string]any
	if err := json.Unmarshal(raw, &pkg); err != nil {
		t.Fatal(err)
	}
	if pkg["name"] != "my-app" {
		t.Fatalf("name = %v, want my-app (trimmed)", pkg["name"])
	}
	if !strings.HasSuffix(string(raw), "\n") {
		t.Fatalf("package.json must end with a trailing newline")
	}
}

// The stamp must preserve the template's exact key order + not HTML-escape `&` (parity with the TS
// JSON.parse→set→stringify) — a map round-trip would sort keys and emit `&`.
// ampAmp is the two-ampersand shell operator, written via unicode escapes so the literal characters
// stay unambiguous in source. & is the ampersand rune.
const ampAmp = "&&"

func TestStampPackageName_preservesOrderAndDoesNotEscape(t *testing.T) {
	dst := t.TempDir()
	if err := (OSProjectFS{}).CopyTemplate(domain.TemplateViteReactTS, dst); err != nil {
		t.Fatal(err)
	}
	if err := (OSProjectFS{}).StampPackageName(dst, "myapp"); err != nil {
		t.Fatal(err)
	}
	got := string(mustRead(t, filepath.Join(dst, "package.json")))
	if !strings.Contains(got, `"name": "myapp"`) {
		t.Fatalf("name not stamped: %s", got)
	}
	// Go's json HTML-escaper would emit the literal 6-char sequence backslash-u-0-0-2-6 for an
	// ampersand; the faithful textual-splice stamp must leave the ampersands verbatim instead.
	if strings.Contains(got, "\\u0026") {
		t.Fatalf("ampersand was HTML-escaped to \\u0026 (map round-trip regression): %s", got)
	}
	if !strings.Contains(got, "tsc --noEmit "+ampAmp+" vite build") {
		t.Fatalf("build script ampersands not preserved verbatim: %s", got)
	}
	// name must stay the FIRST key (template order), not demoted below dependencies (map-sort regression).
	if strings.Index(got, `"name"`) > strings.Index(got, `"dependencies"`) {
		t.Fatalf("key order not preserved (name after dependencies): %s", got)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestStampPackageName_noopWhenAbsent(t *testing.T) {
	dst := t.TempDir()
	if err := (OSProjectFS{}).CopyTemplate(domain.TemplateEmpty, dst); err != nil {
		t.Fatal(err)
	}
	// `empty` ships no package.json — stamping must be a graceful no-op.
	if err := (OSProjectFS{}).StampPackageName(dst, "x"); err != nil {
		t.Fatalf("StampPackageName should no-op when package.json is absent, got %v", err)
	}
}

// End-to-end through the real service: scaffold under a temp base, assert a git repo with exactly one
// initial commit (AC 3.1).
func TestNewProjectService_scaffoldsGitRepoWithOneCommit(t *testing.T) {
	base := t.TempDir()
	svc := usecase.NewNewProjectService(OSProjectFS{}, t.TempDir())
	path, perr := svc.Create(base, "app", domain.TemplateEmpty)
	if perr != nil {
		t.Fatalf("Create: %v", perr)
	}
	if path != filepath.Join(base, "app") {
		t.Fatalf("path = %q", path)
	}
	if _, err := os.Stat(filepath.Join(path, "README.md")); err != nil {
		t.Fatalf("scaffold missing README.md: %v", err)
	}
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		t.Fatalf("target is not a git repo: %v", err)
	}
	out, err := exec.Command("git", "-C", path, "rev-list", "--count", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-list: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "1" {
		t.Fatalf("commit count = %q, want exactly 1", got)
	}
}

// AC 3.2 through the real service: an already-existing target is 409 and creates nothing new.
func TestNewProjectService_existingTargetIs409(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, perr := usecase.NewNewProjectService(OSProjectFS{}, t.TempDir()).Create(base, "app", domain.TemplateEmpty)
	if perr == nil || perr.Code != domain.CodeExists {
		t.Fatalf("want EXISTS, got %v", perr)
	}
}
