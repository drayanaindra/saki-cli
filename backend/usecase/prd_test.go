package usecase

import "testing"

// fakePrdFS is an in-memory WorkItemsFS for the PRD read paths.
//
//	files:  path → content (Read + Exists)
//	mtimes: path → mtime ms
//	walk:   WalkMarkdown result
//	dirs:   dir → ListDir base names (a listed dir also reports Exists=true)
type fakePrdFS struct {
	files  map[string]string
	mtimes map[string]int64
	walk   []string
	dirs   map[string][]string
}

func (f fakePrdFS) WalkMarkdown(string) []string { return f.walk }
func (f fakePrdFS) Read(p string) (string, bool) { s, ok := f.files[p]; return s, ok }
func (f fakePrdFS) Exists(p string) bool {
	if _, ok := f.files[p]; ok {
		return true
	}
	_, ok := f.dirs[p]
	return ok
}
func (f fakePrdFS) MTimeMs(p string) int64    { return f.mtimes[p] }
func (f fakePrdFS) ListDir(d string) []string { return f.dirs[d] }

func body(t *testing.T, b any) map[string]any {
	t.Helper()
	m, ok := b.(map[string]any)
	if !ok {
		t.Fatalf("body is not a map: %#v", b)
	}
	return m
}

// AC 2.1: GET /api/prd?path — .md read → {found,path,score,content,protoPreviewFile}.
func TestReadPrd_path(t *testing.T) {
	fs := fakePrdFS{files: map[string]string{
		"/repo/tasks/prd-foo.md": "# PRD\nQuality Gate: 88/100\nbody",
	}}
	svc := NewPrdService(fs)
	status, b := svc.ReadPrd("/repo", "/repo/tasks/prd-foo.md")
	if status != 200 {
		t.Fatalf("status %d, want 200", status)
	}
	m := body(t, b)
	if m["found"] != true || m["path"] != "/repo/tasks/prd-foo.md" {
		t.Fatalf("want found+path, got %#v", m)
	}
	if m["score"] != "88/100" {
		t.Fatalf("score = %#v, want 88/100", m["score"])
	}
	if m["content"] != "# PRD\nQuality Gate: 88/100\nbody" {
		t.Fatalf("content mismatch: %#v", m["content"])
	}
	// No proto gallery on disk → null.
	if m["protoPreviewFile"] != nil {
		t.Fatalf("protoPreviewFile = %#v, want nil", m["protoPreviewFile"])
	}
}

// AC 2.1: no score line → score is JSON null.
func TestReadPrd_pathNoScore(t *testing.T) {
	fs := fakePrdFS{files: map[string]string{"/repo/tasks/prd-x.md": "no score"}}
	status, b := NewPrdService(fs).ReadPrd("/repo", "/repo/tasks/prd-x.md")
	m := body(t, b)
	if status != 200 || m["score"] != nil {
		t.Fatalf("want 200 + null score, got %d %#v", status, m["score"])
	}
}

// AC 2.1/validation: non-.md path → 422.
func TestReadPrd_pathNotMd(t *testing.T) {
	status, b := NewPrdService(fakePrdFS{}).ReadPrd("/repo", "/repo/foo.txt")
	if status != 422 {
		t.Fatalf("status %d, want 422", status)
	}
	if body(t, b)["error"] == nil {
		t.Fatalf("want error body")
	}
}

// AC 2.1/R6: unreadable path → {found:false}, never 500.
func TestReadPrd_pathUnreadable(t *testing.T) {
	status, b := NewPrdService(fakePrdFS{}).ReadPrd("/repo", "/repo/missing.md")
	if status != 200 || body(t, b)["found"] != false {
		t.Fatalf("want 200 found:false, got %d %#v", status, b)
	}
}

// AC 2.2: GET /api/prd?cwd — newest PRD by mtime (findLatestPrd parity).
func TestReadPrd_cwdLatest(t *testing.T) {
	fs := fakePrdFS{
		files: map[string]string{
			"/repo/tasks/prd-old.md": "Quality Gate: 10/100 old",
			"/repo/tasks/prd-new.md": "Quality Gate: 99/100 new",
			"/repo/tasks/plan.md":    "a plan, not a prd",
		},
		mtimes: map[string]int64{
			"/repo/tasks/prd-old.md": 1000,
			"/repo/tasks/prd-new.md": 2000,
			"/repo/tasks/plan.md":    9999,
		},
		walk: []string{"/repo/tasks/prd-old.md", "/repo/tasks/prd-new.md", "/repo/tasks/plan.md"},
	}
	status, b := NewPrdService(fs).ReadPrd("/repo", "")
	m := body(t, b)
	if status != 200 || m["found"] != true || m["path"] != "/repo/tasks/prd-new.md" {
		t.Fatalf("want newest prd-new.md, got %d %#v", status, m)
	}
	if m["score"] != "99/100" {
		t.Fatalf("score = %#v, want 99/100", m["score"])
	}
}

// AC 2.2: cwd with no PRD → {found:false}.
func TestReadPrd_cwdNone(t *testing.T) {
	fs := fakePrdFS{
		files: map[string]string{"/repo/tasks/plan.md": "just a plan"},
		walk:  []string{"/repo/tasks/plan.md"},
	}
	status, b := NewPrdService(fs).ReadPrd("/repo", "")
	if status != 200 || body(t, b)["found"] != false {
		t.Fatalf("want 200 found:false, got %d %#v", status, b)
	}
}

// AC 2.2/validation: neither path nor cwd → 422 "cwd or path is required".
func TestReadPrd_noArgs(t *testing.T) {
	status, _ := NewPrdService(fakePrdFS{}).ReadPrd("", "")
	if status != 422 {
		t.Fatalf("status %d, want 422", status)
	}
}

// AC 2.3: GET /api/review — companion content / found:false / 422s.
func TestReadReview(t *testing.T) {
	fs := fakePrdFS{files: map[string]string{
		"/repo/tasks/prd-foo-review.md": "verdict: SHIP",
	}}
	svc := NewPrdService(fs)

	status, b := svc.ReadReview("/repo/tasks/prd-foo.md")
	m := body(t, b)
	if status != 200 || m["found"] != true || m["path"] != "/repo/tasks/prd-foo-review.md" || m["content"] != "verdict: SHIP" {
		t.Fatalf("want found companion, got %d %#v", status, m)
	}

	if status, b := svc.ReadReview("/repo/tasks/prd-none.md"); status != 200 || body(t, b)["found"] != false {
		t.Fatalf("missing companion: want 200 found:false, got %d %#v", status, b)
	}
	if status, _ := svc.ReadReview(""); status != 422 {
		t.Fatalf("empty path: want 422, got %d", status)
	}
	if status, _ := svc.ReadReview("/repo/tasks/foo.txt"); status != 422 {
		t.Fatalf("non-.md: want 422, got %d", status)
	}
}

// AC 2.4: GET /api/review-state — state content / found:false / 422s.
func TestReadReviewState(t *testing.T) {
	fs := fakePrdFS{files: map[string]string{
		"/repo/tasks/.prd-review-foo-state.json": `{"round":2}`,
	}}
	svc := NewPrdService(fs)

	status, b := svc.ReadReviewState("/repo/tasks/prd-foo.md", "/repo")
	m := body(t, b)
	if status != 200 || m["found"] != true || m["path"] != "/repo/tasks/.prd-review-foo-state.json" || m["content"] != `{"round":2}` {
		t.Fatalf("want found state, got %d %#v", status, m)
	}

	if status, b := svc.ReadReviewState("/repo/tasks/prd-none.md", "/repo"); status != 200 || body(t, b)["found"] != false {
		t.Fatalf("missing state: want 200 found:false, got %d %#v", status, b)
	}
	if status, _ := svc.ReadReviewState("", "/repo"); status != 422 {
		t.Fatalf("empty path: want 422, got %d", status)
	}
	if status, _ := svc.ReadReviewState("/repo/tasks/foo.txt", "/repo"); status != 422 {
		t.Fatalf("non-.md: want 422, got %d", status)
	}
}

// AC 2.1: findProtoPreview — layout 1 (prd-<slug>.md sibling gallery).
func TestFindProtoPreview_layout1(t *testing.T) {
	fs := fakePrdFS{
		files: map[string]string{
			"/repo/tasks/prd-foo.md":             "prd",
			"/repo/tasks/proto-foo/preview.html": "<html>",
		},
	}
	status, b := NewPrdService(fs).ReadPrd("/repo", "/repo/tasks/prd-foo.md")
	m := body(t, b)
	if status != 200 || m["protoPreviewFile"] != "tasks/proto-foo/preview.html" {
		t.Fatalf("layout1 proto = %#v, want tasks/proto-foo/preview.html", m["protoPreviewFile"])
	}
}

// AC 2.1: findProtoPreview — layout 2 (docs/prd/<cat>/<slug>/prd.md, exact-slug probe).
func TestFindProtoPreview_layout2Slug(t *testing.T) {
	fs := fakePrdFS{
		files: map[string]string{
			"/repo/docs/prd/cat/myslug/prd.md":      "prd",
			"/repo/tasks/proto-myslug/preview.html": "<html>",
		},
		dirs: map[string][]string{
			"/repo": {"docs", "tasks"},
		},
	}
	status, b := NewPrdService(fs).ReadPrd("/repo", "/repo/docs/prd/cat/myslug/prd.md")
	m := body(t, b)
	if status != 200 || m["protoPreviewFile"] != "tasks/proto-myslug/preview.html" {
		t.Fatalf("layout2 slug proto = %#v", m["protoPreviewFile"])
	}
}

// AC 2.1: findProtoPreview — layout 2 index.md scan (proto slug differs from dir name).
func TestFindProtoPreview_layout2IndexScan(t *testing.T) {
	fs := fakePrdFS{
		files: map[string]string{
			"/repo/docs/prd/cat/myslug/prd.md":       "prd",
			"/repo/tasks/proto-shorter/index.md":     "source: docs/prd/cat/myslug/prd.md",
			"/repo/tasks/proto-shorter/preview.html": "<html>",
		},
		dirs: map[string][]string{
			"/repo":       {"docs", "tasks"},
			"/repo/tasks": {"proto-shorter"},
		},
	}
	status, b := NewPrdService(fs).ReadPrd("/repo", "/repo/docs/prd/cat/myslug/prd.md")
	m := body(t, b)
	if status != 200 || m["protoPreviewFile"] != "tasks/proto-shorter/preview.html" {
		t.Fatalf("layout2 index-scan proto = %#v", m["protoPreviewFile"])
	}
}

// AC 2.1/R6: findProtoPreview — no gallery → null.
func TestFindProtoPreview_none(t *testing.T) {
	fs := fakePrdFS{files: map[string]string{"/repo/tasks/prd-foo.md": "prd"}}
	_, b := NewPrdService(fs).ReadPrd("/repo", "/repo/tasks/prd-foo.md")
	if body(t, b)["protoPreviewFile"] != nil {
		t.Fatalf("want nil proto")
	}
}
