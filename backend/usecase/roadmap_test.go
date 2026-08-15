package usecase

import (
	"testing"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

// fakeRoadmapFS is an in-memory RoadmapFS: files maps an absolute path → its content.
type fakeRoadmapFS struct{ files map[string]string }

func (f fakeRoadmapFS) Exists(p string) bool         { _, ok := f.files[p]; return ok }
func (f fakeRoadmapFS) Read(p string) (string, bool) { s, ok := f.files[p]; return s, ok }

// AC 1.3 · R6 (tests: 1.3): a malformed/empty roadmap.md degrades to a best-effort board with
// HTTP < 500 — never a crash, never a blank board on a partial parse.
func TestReadRoadmap_malformedDegrades(t *testing.T) {
	const cwd = "/repo"
	malformed := "# Roadmap: Studio\n\n### E1 · half a block with no status\nsome noise\n\n### F2 · Real\n**Status:** Planned\n"
	svc := NewRoadmapService(fakeRoadmapFS{files: map[string]string{
		"/repo/tasks/roadmap.md": malformed,
	}})
	status, body := svc.ReadRoadmap(cwd)
	if status >= 500 {
		t.Fatalf("status %d — must degrade below 500 on malformed input", status)
	}
	m, ok := body.(map[string]any)
	if !ok || m["found"] != true {
		t.Fatalf("want found:true best-effort body, got %#v", body)
	}
	// The status-less E1 block is skipped; the valid F2 block survives (degrade, not blank board).
	epics, _ := m["epics"].([]domain.RoadmapItem)
	if len(epics) != 1 || epics[0].ID != "F2" {
		t.Fatalf("want the one valid block (F2) to survive the partial parse, got %#v", epics)
	}
}

// AC 1.4 · R1 (read-only): a missing file degrades to found:false, never a 500 and never a write.
func TestReadRoadmap_missingFalse(t *testing.T) {
	svc := NewRoadmapService(fakeRoadmapFS{files: map[string]string{}})
	status, body := svc.ReadRoadmap("/repo")
	if status != 200 {
		t.Fatalf("status %d, want 200", status)
	}
	if m, _ := body.(map[string]any); m["found"] != false {
		t.Fatalf("want found:false, got %#v", body)
	}
}

// R3 containment parity: a bad/escaping cwd is 422 (never 500, never a read outside cwd).
func TestReadRoadmap_badCwd422(t *testing.T) {
	svc := NewRoadmapService(fakeRoadmapFS{files: map[string]string{}})
	if status, _ := svc.ReadRoadmap(""); status != 422 {
		t.Fatalf("empty cwd: status %d, want 422", status)
	}
}
