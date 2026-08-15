package usecase

import (
	"errors"
	"testing"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

// fakeProjectFS records the call sequence and lets a test force a step to fail.
type fakeProjectFS struct {
	existing map[string]bool
	calls    []string
	failOn   string // method name that should return an error
}

func (f *fakeProjectFS) Exists(path string) bool { return f.existing[path] }

func (f *fakeProjectFS) MkdirAll(path string) error { return f.record("MkdirAll") }

func (f *fakeProjectFS) CopyTemplate(_ domain.TemplateKind, _ string) error {
	return f.record("CopyTemplate")
}

func (f *fakeProjectFS) StampPackageName(_, _ string) error { return f.record("StampPackageName") }

func (f *fakeProjectFS) GitInitCommit(_ string, _ domain.TemplateKind) error {
	return f.record("GitInitCommit")
}

func (f *fakeProjectFS) RemoveAll(_ string) error { return f.record("RemoveAll") }

func (f *fakeProjectFS) record(name string) error {
	f.calls = append(f.calls, name)
	if name == f.failOn {
		return errors.New("boom in " + name)
	}
	return nil
}

const home = "/home/me"

func TestCreate_happyPath(t *testing.T) {
	fs := &fakeProjectFS{existing: map[string]bool{}}
	path, err := NewNewProjectService(fs, home).Create("~/Projects", "app", domain.TemplateViteReactTS)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "/home/me/Projects/app" {
		t.Fatalf("path = %q", path)
	}
	want := []string{"MkdirAll", "CopyTemplate", "StampPackageName", "GitInitCommit"}
	if got := fs.calls; !equal(got, want) {
		t.Fatalf("call order = %v, want %v", got, want)
	}
}

func TestCreate_existsIs409NoMkdir(t *testing.T) {
	fs := &fakeProjectFS{existing: map[string]bool{"/home/me/Projects/app": true}}
	_, err := NewNewProjectService(fs, home).Create("~/Projects", "app", domain.TemplateEmpty)
	if err == nil || err.Code != domain.CodeExists {
		t.Fatalf("want EXISTS error, got %v", err)
	}
	if len(fs.calls) != 0 {
		t.Fatalf("existing target must not scaffold, calls = %v", fs.calls)
	}
}

func TestCreate_invalidNameIs400NoFS(t *testing.T) {
	fs := &fakeProjectFS{existing: map[string]bool{}}
	_, err := NewNewProjectService(fs, home).Create("~/Projects", "../escape", domain.TemplateEmpty)
	if err == nil || err.Code != domain.CodeInvalid {
		t.Fatalf("want INVALID, got %v", err)
	}
	if len(fs.calls) != 0 {
		t.Fatalf("invalid name must not touch fs, calls = %v", fs.calls)
	}
}

// The rollback invariant (AC 3.3): a failure at any post-mkdir step removes the half-built dir.
func TestCreate_rollbackOnScaffoldFailure(t *testing.T) {
	for _, failStep := range []string{"CopyTemplate", "StampPackageName", "GitInitCommit"} {
		fs := &fakeProjectFS{existing: map[string]bool{}, failOn: failStep}
		_, err := NewNewProjectService(fs, home).Create("~/Projects", "app", domain.TemplateNextJS)
		if err == nil || err.Code != "" {
			t.Fatalf("[%s] want a generic (500) error, got %v", failStep, err)
		}
		if !contains(fs.calls, "RemoveAll") {
			t.Fatalf("[%s] rollback RemoveAll not called; calls = %v", failStep, fs.calls)
		}
	}
}

// mkdir failure is OUTSIDE the rollback window (parity mkdir-before-try): no RemoveAll.
func TestCreate_mkdirFailureNoRollback(t *testing.T) {
	fs := &fakeProjectFS{existing: map[string]bool{}, failOn: "MkdirAll"}
	_, err := NewNewProjectService(fs, home).Create("~/Projects", "app", domain.TemplateEmpty)
	if err == nil || err.Code != "" {
		t.Fatalf("want generic 500, got %v", err)
	}
	if contains(fs.calls, "RemoveAll") {
		t.Fatalf("mkdir failure must not roll back, calls = %v", fs.calls)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
