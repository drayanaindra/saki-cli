package usecase

import (
	"strings"
	"testing"
)

// wiFS is a WorkItemsFS test double with configurable WalkMarkdown and ListDir.
// Separate from fpFS (fingerprint_test.go) because it needs a populated WalkMarkdown.
type wiFS struct {
	files map[string]string
	walk  []string
}

func (f wiFS) WalkMarkdown(string) []string { return f.walk }
func (f wiFS) Read(p string) (string, bool) { s, ok := f.files[p]; return s, ok }
func (f wiFS) Exists(p string) bool         { _, ok := f.files[p]; return ok }
func (f wiFS) MTimeMs(string) int64         { return 0 }
func (f wiFS) ListDir(dir string) []string {
	prefix := dir + "/"
	var out []string
	for p := range f.files {
		if strings.HasPrefix(p, prefix) && !strings.Contains(p[len(prefix):], "/") {
			out = append(out, p[len(prefix):])
		}
	}
	return out
}

// ── FindWorkItems ──────────────────────────────────────────────────────────────

func TestFindWorkItems_classifiesAndSorts(t *testing.T) {
	svc := NewWorkItemsService(wiFS{
		files: map[string]string{
			"/repo/prd-beta.md":  "- [ ] 1. Slice one\n",
			"/repo/prd-alpha.md": "- [x] 1. Slice one\n",
			"/repo/plan-foo.md":  "- [ ] 1. Step one\n",
		},
		walk: []string{"/repo/prd-beta.md", "/repo/prd-alpha.md", "/repo/plan-foo.md"},
	}, noCommits{})

	prds, plans := svc.FindWorkItems("/repo")
	if len(prds) != 2 {
		t.Fatalf("want 2 PRDs, got %d", len(prds))
	}
	if len(plans) != 1 {
		t.Fatalf("want 1 plan, got %d", len(plans))
	}
	// sorted: alpha < beta (LabelFor returns the full filename)
	if prds[0].Name != "prd-alpha.md" {
		t.Errorf("want first PRD = prd-alpha.md, got %s", prds[0].Name)
	}
	if prds[1].Name != "prd-beta.md" {
		t.Errorf("want second PRD = prd-beta.md, got %s", prds[1].Name)
	}
}

func TestFindWorkItems_empty(t *testing.T) {
	svc := NewWorkItemsService(wiFS{files: map[string]string{}, walk: nil}, noCommits{})
	prds, plans := svc.FindWorkItems("/repo")
	if len(prds) != 0 || len(plans) != 0 {
		t.Errorf("want empty results, got prds=%d plans=%d", len(prds), len(plans))
	}
}

func TestFindWorkItems_ignoresNonClassified(t *testing.T) {
	svc := NewWorkItemsService(wiFS{
		files: map[string]string{"/repo/README.md": "# readme"},
		walk:  []string{"/repo/README.md"},
	}, noCommits{})
	prds, plans := svc.FindWorkItems("/repo")
	if len(prds) != 0 || len(plans) != 0 {
		t.Errorf("README.md should not classify as PRD/plan")
	}
}

// ── buildItem with progress scratchpad ────────────────────────────────────────

func TestFindWorkItems_withProgressScratchpad(t *testing.T) {
	// prd-foo.md → slug "foo" → artifact at /repo/.build-foo-progress.md
	svc := NewWorkItemsService(wiFS{
		files: map[string]string{
			"/repo/prd-foo.md":             "- [ ] 1. Slice one\n",
			"/repo/.build-foo-progress.md": "- [x] 1. Slice one\n- [ ] 2. Slice two\n",
		},
		walk: []string{"/repo/prd-foo.md"},
	}, noCommits{})

	prds, _ := svc.FindWorkItems("/repo")
	if len(prds) != 1 {
		t.Fatalf("want 1 PRD, got %d", len(prds))
	}
	wi := prds[0]
	if wi.Done != 1 || wi.Total != 2 {
		t.Errorf("want Done=1 Total=2, got Done=%d Total=%d", wi.Done, wi.Total)
	}
	if !wi.Resumable {
		t.Error("want Resumable=true when progress exists")
	}
}

func TestFindWorkItems_fallbackToMarkdownCheckboxes(t *testing.T) {
	// No progress scratchpad → classify from the PRD's own checkboxes
	svc := NewWorkItemsService(wiFS{
		files: map[string]string{
			"/repo/prd-bar.md": "- [x] 1. Done\n- [ ] 2. Pending\n",
		},
		walk: []string{"/repo/prd-bar.md"},
	}, noCommits{})

	prds, _ := svc.FindWorkItems("/repo")
	if len(prds) != 1 {
		t.Fatalf("want 1 PRD")
	}
	wi := prds[0]
	if wi.Done != 1 || wi.Total != 2 {
		t.Errorf("fallback checkbox parse: want Done=1 Total=2, got %d %d", wi.Done, wi.Total)
	}
	if wi.Resumable {
		t.Error("want Resumable=false when no manifest/scratchpad")
	}
}

// ── planArtifactExists ────────────────────────────────────────────────────────

func TestPlanArtifactExists_namePattern(t *testing.T) {
	svc := NewWorkItemsService(wiFS{
		files: map[string]string{
			"/repo/tasks/add-auth-slice1-plan.md": "",
		},
		walk: nil,
	}, noCommits{})

	if !svc.planArtifactExists("/repo", "/repo/prd-auth.md", 1) {
		t.Error("plan file with 'slice1' in name should be found")
	}
	if svc.planArtifactExists("/repo", "/repo/prd-auth.md", 2) {
		t.Error("no slice2 plan file → should not be found")
	}
}

func TestPlanArtifactExists_directPlanSliceFile(t *testing.T) {
	// plan-slice3.md in prd dir
	svc := NewWorkItemsService(wiFS{
		files: map[string]string{
			"/repo/tasks/plan-slice3.md": "",
		},
		walk: nil,
	}, noCommits{})
	if !svc.planArtifactExists("/repo", "/repo/tasks/prd-foo.md", 3) {
		t.Error("plan-slice3.md should be found for slice 3")
	}
}

func TestPlanArtifactExists_notFound(t *testing.T) {
	svc := NewWorkItemsService(wiFS{files: map[string]string{}, walk: nil}, noCommits{})
	if svc.planArtifactExists("/repo", "/repo/prd-foo.md", 1) {
		t.Error("empty fs → should never find a plan artifact")
	}
}

// ── itoa ──────────────────────────────────────────────────────────────────────

func TestItoa(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{0, "0"}, {1, "1"}, {9, "9"}, {10, "10"}, {42, "42"}, {100, "100"},
		{-1, "-1"}, {-42, "-42"},
	}
	for _, c := range cases {
		if got := itoa(c.n); got != c.want {
			t.Errorf("itoa(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}
