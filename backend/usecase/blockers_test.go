package usecase

import (
	"testing"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

const blkPrd = `# PRD

## Rabbit Holes & Open Questions
**Open questions:**
- **Q1** before slice 2. Which fork?
`

const blkProgress = `# Build progress
- [ ] 2. Slice two — BLOCKED: needs a decision
- [ ] 3. Slice three — BLOCKED: gated to a later phase
`

func blkFS() fakePrdFS {
	return fakePrdFS{files: map[string]string{
		"/repo/tasks/prd-x.md":             blkPrd,
		"/repo/tasks/.build-x-progress.md": blkProgress,
	}}
}

func numQuestions(t *testing.T, v any) int {
	t.Helper()
	qs, ok := v.([]domain.OpenQuestion)
	if !ok {
		t.Fatalf("questions is not []domain.OpenQuestion: %#v", v)
	}
	return len(qs)
}

func TestReadBlockers_happy(t *testing.T) {
	status, b := NewBlockersService(blkFS()).ReadBlockers("/repo", "/repo/tasks/prd-x.md", "2")
	m := body(t, b)
	if status != 200 {
		t.Fatalf("status %d, want 200", status)
	}
	if m["n"] != 2 || m["reason"] != "needs a decision" || m["deferred"] != false {
		t.Fatalf("blockers = %#v, want n=2 reason='needs a decision' deferred=false", m)
	}
	if numQuestions(t, m["questions"]) != 1 {
		t.Fatalf("questions len = %d, want 1 (%#v)", numQuestions(t, m["questions"]), m["questions"])
	}
}

func TestReadBlockers_deferredTrue(t *testing.T) {
	// slice 3 is BLOCKED + "later" → deferred true.
	_, b := NewBlockersService(blkFS()).ReadBlockers("/repo", "/repo/tasks/prd-x.md", "3")
	if body(t, b)["deferred"] != true {
		t.Fatalf("slice 3 deferred = %#v, want true", body(t, b)["deferred"])
	}
}

func TestReadBlockers_noCwdOmitsProgress(t *testing.T) {
	// No cwd → progress omitted → no reason/deferred, but PRD questions still parse.
	status, b := NewBlockersService(blkFS()).ReadBlockers("", "/repo/tasks/prd-x.md", "2")
	m := body(t, b)
	if status != 200 || m["reason"] != nil || m["deferred"] != false {
		t.Fatalf("no-cwd = %#v, want 200 reason=nil deferred=false", m)
	}
	if numQuestions(t, m["questions"]) != 1 {
		t.Fatalf("questions should still parse from PRD: %#v", m["questions"])
	}
}

func TestReadBlockers_validation(t *testing.T) {
	svc := NewBlockersService(blkFS())
	if s, _ := svc.ReadBlockers("/repo", "/repo/tasks/missing.md", "2"); s != 404 {
		t.Errorf("missing PRD status %d, want 404", s)
	}
	if s, _ := svc.ReadBlockers("/repo", "/repo/tasks/prd-x.txt", "2"); s != 422 {
		t.Errorf("non-.md status %d, want 422", s)
	}
	if s, _ := svc.ReadBlockers("/repo", "/repo/tasks/prd-x.md", "abc"); s != 422 {
		t.Errorf("bad n status %d, want 422", s)
	}
}

func TestSliceMeta(t *testing.T) {
	svc := NewSliceMetaService(lockGitSlice{commit: "a1b2c3d feat: slice-2 board"})
	status, b := svc.SliceMeta("/repo", "2")
	if status != 200 || body(t, b)["commit"] != "a1b2c3d feat: slice-2 board" {
		t.Fatalf("slice-meta = %d %#v", status, body(t, b))
	}
	if _, b := NewSliceMetaService(lockGitSlice{}).SliceMeta("/repo", "9"); body(t, b)["commit"] != nil {
		t.Errorf("no-commit should be null, got %#v", body(t, b)["commit"])
	}
	if s, _ := svc.SliceMeta("", "2"); s != 422 {
		t.Errorf("missing cwd status %d, want 422", s)
	}
	if s, _ := svc.SliceMeta("/repo", ""); s != 422 {
		t.Errorf("missing n status %d, want 422", s)
	}
}

type lockGitSlice struct{ commit string }

func (g lockGitSlice) SliceCommit(_, _ string) string { return g.commit }
