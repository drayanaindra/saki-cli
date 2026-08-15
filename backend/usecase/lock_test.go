package usecase

import (
	"errors"
	"strings"
	"testing"
)

// lockWriter records writes (or returns err to exercise the R6 graceful branch).
type lockWriter struct {
	writes map[string]string
	err    error
}

func (w *lockWriter) WriteFile(path, content string) error {
	if w.err != nil {
		return w.err
	}
	w.writes[path] = content
	return nil
}

type lockGitUser struct{ name string }

func (g lockGitUser) UserName(string) string { return g.name }

const prdPath = "/repo/tasks/prd-x.md"

func newLockWriter() *lockWriter { return &lockWriter{writes: map[string]string{}} }

// AC 3.1: an unlocked PRD → single marker + Status→Locked, {ok:true,locked:true,path}, one write.
func TestLock_fresh(t *testing.T) {
	fs := fakePrdFS{files: map[string]string{prdPath: "# PRD\n**Owner:** unassigned · **Status:** Draft"}}
	w := newLockWriter()
	status, b := NewLockService(fs, w, lockGitUser{name: "Bob"}).Lock("/repo", prdPath, "2026-07-15")
	m := body(t, b)
	if status != 200 || m["ok"] != true || m["locked"] != true || m["path"] != prdPath {
		t.Fatalf("want 200 ok+locked+path, got %d %#v", status, m)
	}
	got := w.writes[prdPath]
	if !strings.HasPrefix(got, "<!-- prd-locked: @Bob · 2026-07-15 · ui:none -->\n") {
		t.Errorf("marker missing/mismatched: %q", got)
	}
	if !strings.Contains(got, "**Status:** Locked") {
		t.Errorf("Status not flipped: %q", got)
	}
}

// AC 3.2 / R2: an already-locked PRD → {ok:true,alreadyLocked:true}, NO write.
func TestLock_alreadyLocked(t *testing.T) {
	locked := "<!-- prd-locked: @a · 2026-01-01 · ui:none -->\n# PRD\n**Status:** Locked"
	fs := fakePrdFS{files: map[string]string{prdPath: locked}}
	w := newLockWriter()
	status, b := NewLockService(fs, w, lockGitUser{}).Lock("/repo", prdPath, "2026-07-15")
	m := body(t, b)
	if status != 200 || m["ok"] != true || m["alreadyLocked"] != true {
		t.Fatalf("want alreadyLocked, got %d %#v", status, m)
	}
	if len(w.writes) != 0 {
		t.Errorf("R2 violated: an already-locked PRD was rewritten: %#v", w.writes)
	}
}

// AC 3.3 / R3: a cwd-escaping path → 422, nothing written.
func TestLock_containment422(t *testing.T) {
	fs := fakePrdFS{files: map[string]string{prdPath: "x"}}
	w := newLockWriter()
	status, _ := NewLockService(fs, w, lockGitUser{}).Lock("/repo", "../evil.md", "2026-07-15")
	if status != 422 {
		t.Fatalf("status %d, want 422", status)
	}
	if len(w.writes) != 0 {
		t.Errorf("R3 violated: a write escaped on a bad path: %#v", w.writes)
	}
}

// R6: exists passes but the read fails (race/perm) → {ok:false,error}, HTTP 200, NEVER a marker onto "".
func TestLock_readErrGraceful(t *testing.T) {
	// path is in dirs (Exists=true) but not files (Read=false).
	fs := fakePrdFS{dirs: map[string][]string{prdPath: {}}}
	w := newLockWriter()
	status, b := NewLockService(fs, w, lockGitUser{}).Lock("/repo", prdPath, "2026-07-15")
	m := body(t, b)
	if status != 200 || m["ok"] != false || m["error"] != "read failed" {
		t.Fatalf("want 200 ok:false read-failed, got %d %#v", status, m)
	}
	if len(w.writes) != 0 {
		t.Errorf("clobber guard failed: wrote onto a failed read: %#v", w.writes)
	}
}

// AC 3.4 / R6: an unwritable target → {ok:false,error} at HTTP 200, never a 500.
func TestLock_writeErrGraceful(t *testing.T) {
	fs := fakePrdFS{files: map[string]string{prdPath: "**Status:** Draft"}}
	w := &lockWriter{writes: map[string]string{}, err: errors.New("disk full")}
	status, b := NewLockService(fs, w, lockGitUser{}).Lock("/repo", prdPath, "2026-07-15")
	m := body(t, b)
	if status != 200 || m["ok"] != false || m["error"] != "disk full" {
		t.Fatalf("want 200 ok:false disk-full, got %d %#v", status, m)
	}
}

// AC 3.1: the marker's ui: reflects a proto gallery on disk (findProtoPreview reuse).
func TestLock_uiFromProto(t *testing.T) {
	fs := fakePrdFS{files: map[string]string{
		prdPath:                            "**Owner:** Alice · **Status:** Draft",
		"/repo/tasks/proto-x/preview.html": "<html>",
	}}
	w := newLockWriter()
	NewLockService(fs, w, lockGitUser{}).Lock("/repo", prdPath, "2026-07-15")
	got := w.writes[prdPath]
	if !strings.HasPrefix(got, "<!-- prd-locked: @Alice · 2026-07-15 · ui:tasks/proto-x/ -->\n") {
		t.Errorf("ui: not derived from proto preview: %q", got)
	}
}
