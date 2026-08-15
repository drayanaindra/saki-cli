package infra

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWalkShots verifies the NEW depth-4 / .png / no-ignore-list screenshot walk (F5 · slice 2):
// it collects .png under the three SHOT_DIRS at depth ≤4, excludes a depth-5 file, ignores non-.png
// and non-SHOT dirs, and does NOT inherit WalkMarkdown's ignore-dir/.md semantics.
func TestWalkShots(t *testing.T) {
	root := t.TempDir()
	mk := func(rel string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// depth ≤4 under SHOT_DIRS → collected
	mk("test-results/a.png")               // depth 0
	mk("playwright-report/x/y/z/deep.png") // depth 3
	mk("screenshots/a/b/c/d/edge.png")     // depth 4 (still collected)
	// excluded
	mk("test-results/a/b/c/d/e/too-deep.png") // depth 5 → excluded
	mk("test-results/note.txt")               // non-.png → excluded
	mk("docs/outside.png")                    // outside SHOT_DIRS → excluded

	got := ContentFS{}.WalkShots(root)
	names := map[string]bool{}
	for _, s := range got {
		rel, _ := filepath.Rel(root, s.Path)
		names[rel] = true
	}
	wantIn := []string{
		filepath.Join("test-results", "a.png"),
		filepath.Join("playwright-report", "x", "y", "z", "deep.png"),
		filepath.Join("screenshots", "a", "b", "c", "d", "edge.png"),
	}
	for _, w := range wantIn {
		if !names[w] {
			t.Fatalf("expected %q collected, got %v", w, names)
		}
	}
	wantOut := []string{
		filepath.Join("test-results", "a", "b", "c", "d", "e", "too-deep.png"),
		filepath.Join("test-results", "note.txt"),
		filepath.Join("docs", "outside.png"),
	}
	for _, w := range wantOut {
		if names[w] {
			t.Fatalf("expected %q excluded, got %v", w, names)
		}
	}
	if len(got) != len(wantIn) {
		t.Fatalf("count = %d, want %d (%v)", len(got), len(wantIn), names)
	}
}
