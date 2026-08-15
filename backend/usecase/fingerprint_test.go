package usecase

import "testing"

// fpFS is a minimal WorkItemsFS for the ProgressFingerprint composition test: an in-memory path→content
// map. Only Exists/Read are exercised (ProgressFingerprint reads the progress + optional state artifact).
type fpFS struct{ files map[string]string }

func (f fpFS) Exists(p string) bool         { _, ok := f.files[p]; return ok }
func (f fpFS) Read(p string) (string, bool) { s, ok := f.files[p]; return s, ok }
func (f fpFS) MTimeMs(string) int64         { return 0 }
func (f fpFS) ListDir(string) []string      { return nil }
func (f fpFS) WalkMarkdown(string) []string { return nil }

type noCommits struct{}

func (noCommits) CommitExists(string, string) bool { return false }

func TestProgressFingerprint_Composition(t *testing.T) {
	svc := NewWorkItemsService(fpFS{files: map[string]string{
		"/repo/tasks/.build-foo-progress.md": "- [x] 1. Slice one\n- [x] 2. Slice two\n- [ ] 3. Slice three\n",
	}}, noCommits{})

	fp := svc.ProgressFingerprint("/repo", "/repo/tasks/prd-foo.md")
	if fp == nil || *fp != "2:none" {
		t.Fatalf("two completed slices, no manifest → want \"2:none\", got %v", fp)
	}

	// AC 2.3 — an absent scratchpad yields a nil (neutral) fingerprint, not a crash or a false value.
	if got := svc.ProgressFingerprint("/repo", "/repo/tasks/prd-bar.md"); got != nil {
		t.Fatalf("absent progress scratchpad must be neutral (nil), got %v", got)
	}
}
