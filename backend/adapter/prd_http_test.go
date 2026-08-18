package adapter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/drayanaindra/saki-cli/backend/usecase"
)

// F4 · P3 slice 2: adapter-level tests for the three PRD/review read routes. The usecase suite
// (usecase/prd_test.go) proves the read logic; these lock the HTTP wiring the usecase tests can't
// see — that prdHandler passes the query params to ReadPrd(cwd, path) in the right ORDER, that the
// .md gate surfaces as a 422 over HTTP, and that Routes() registers exactly these three and no more.

// buildPrdHandler wires a Handler whose PrdService reads from the given in-memory content fs; every
// other service is empty (these tests only exercise GET /api/prd|review|review-state).
func buildPrdHandler(fs fakeContentFS) Handler {
	st := newFakeStore()
	px := &fakeProxy{}
	rs := usecase.NewRunService(&fakeSpawner{}, &fakeJournal{writable: true}, st, fakeClock{}, fakeIDGen{})
	ss := usecase.NewStreamService(st, &fakeOutput{}, fakeClock{}, time.Millisecond)
	sk := usecase.NewStopService(st, &fakeKiller{})
	eng := newEngine(&fakeSpawner{}, &fakeJournal{writable: true}, st, &fakeKiller{})
	return NewHandler(usecase.NewBranchService(fakeBranchReader{}), rs, eng, usecase.NewListService(st, px, fakeFullOutput{}), ss, sk, px,
		gitWriteSvc(fakeGitWriter{}), emptyRoadmap(), emptyWorkitems(), prdSvc(fs), emptyLock(), emptyBlockers(), emptySliceMeta(), emptyResolve(), emptyPlanTrack(), emptyDoctor(), emptyInitEnv())
}

func getJSON(t *testing.T, url string) (int, map[string]any) {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var m map[string]any
	json.NewDecoder(res.Body).Decode(&m)
	return res.StatusCode, m
}

// AC 2.1: GET /api/prd?path — the handler must pass cwd + path in the right order. A swapped
// ReadPrd(path, cwd) would take cwd="/r/tasks/prd-foo.md" and path="/r" (no .md) → 422, so a 200 with
// the file's score proves the plumbing.
func TestPrdRoute_path(t *testing.T) {
	fs := fakeContentFS{files: map[string]string{"/r/tasks/prd-foo.md": "# PRD\nQuality Gate: 88/100\nbody"}}
	srv := httptest.NewServer(buildPrdHandler(fs).Routes())
	defer srv.Close()

	status, m := getJSON(t, srv.URL+"/api/prd?cwd=/r&path=/r/tasks/prd-foo.md")
	if status != 200 || m["found"] != true || m["path"] != "/r/tasks/prd-foo.md" {
		t.Fatalf("want 200 found path, got %d %#v", status, m)
	}
	if m["score"] != "88/100" {
		t.Fatalf("score = %#v, want 88/100", m["score"])
	}
}

// AC 2.1/validation: a non-.md path 422s over HTTP.
func TestPrdRoute_pathNotMd(t *testing.T) {
	srv := httptest.NewServer(buildPrdHandler(fakeContentFS{}).Routes())
	defer srv.Close()
	if status, _ := getJSON(t, srv.URL+"/api/prd?path=/r/foo.txt"); status != 422 {
		t.Fatalf("status %d, want 422", status)
	}
}

// AC 2.2: GET /api/prd?cwd — the cwd param reaches ReadPrd and drives latest-PRD discovery (newest by
// mtime); neither param → 422. (The cwd-with-none → found:false path is covered at usecase level in
// TestReadPrd_cwdNone; the in-memory fake's WalkMarkdown ignores its dir arg, so it can't scope here.)
func TestPrdRoute_cwd(t *testing.T) {
	fs := fakeContentFS{
		files: map[string]string{
			"/r/tasks/prd-old.md": "Quality Gate: 10/100 old",
			"/r/tasks/prd-new.md": "Quality Gate: 99/100 new",
		},
		mtimes: map[string]int64{"/r/tasks/prd-old.md": 1000, "/r/tasks/prd-new.md": 2000},
		walk:   []string{"/r/tasks/prd-old.md", "/r/tasks/prd-new.md"},
	}
	srv := httptest.NewServer(buildPrdHandler(fs).Routes())
	defer srv.Close()

	status, m := getJSON(t, srv.URL+"/api/prd?cwd=/r")
	if status != 200 || m["found"] != true || m["path"] != "/r/tasks/prd-new.md" {
		t.Fatalf("want newest prd-new.md, got %d %#v", status, m)
	}
	if s, _ := getJSON(t, srv.URL+"/api/prd"); s != 422 {
		t.Fatalf("no-args: status %d, want 422", s)
	}
}

// AC 2.3: GET /api/review — found companion, missing companion → found:false, missing/non-.md → 422.
func TestReviewRoute(t *testing.T) {
	fs := fakeContentFS{files: map[string]string{"/r/tasks/prd-foo-review.md": "review text"}}
	srv := httptest.NewServer(buildPrdHandler(fs).Routes())
	defer srv.Close()

	status, m := getJSON(t, srv.URL+"/api/review?path=/r/tasks/prd-foo.md")
	if status != 200 || m["found"] != true || m["content"] != "review text" {
		t.Fatalf("want found review, got %d %#v", status, m)
	}
	if s, m := getJSON(t, srv.URL+"/api/review?path=/r/tasks/prd-none.md"); s != 200 || m["found"] != false {
		t.Fatalf("missing companion: want found:false, got %d %#v", s, m)
	}
	if s, _ := getJSON(t, srv.URL+"/api/review"); s != 422 {
		t.Fatalf("no-path: status %d, want 422", s)
	}
	if s, _ := getJSON(t, srv.URL+"/api/review?path=/r/foo.txt"); s != 422 {
		t.Fatalf("non-.md: status %d, want 422", s)
	}
}

// AC 2.4: GET /api/review-state — the handler must pass path + cwd in the right order. The state file
// path is <cwd>/tasks/.prd-review-<slug>-state.json; a swap would look under the wrong root.
func TestReviewStateRoute(t *testing.T) {
	fs := fakeContentFS{files: map[string]string{"/r/tasks/.prd-review-foo-state.json": "{\"v\":1}"}}
	srv := httptest.NewServer(buildPrdHandler(fs).Routes())
	defer srv.Close()

	status, m := getJSON(t, srv.URL+"/api/review-state?cwd=/r&path=/r/tasks/prd-foo.md")
	if status != 200 || m["found"] != true || m["content"] != "{\"v\":1}" {
		t.Fatalf("want found state, got %d %#v", status, m)
	}
	if s, m := getJSON(t, srv.URL+"/api/review-state?cwd=/r&path=/r/tasks/prd-none.md"); s != 200 || m["found"] != false {
		t.Fatalf("missing state: want found:false, got %d %#v", s, m)
	}
	if s, _ := getJSON(t, srv.URL+"/api/review-state?path=/r/foo.txt"); s != 422 {
		t.Fatalf("non-.md: status %d, want 422", s)
	}
}

// Coexistence: as of slice 6 the plan-track writers COMPLETE the content bucket — POST
// /api/roadmap/plan-start is now Go-owned, so the mux answers it (422 on the empty-cwd body, NOT a 404
// fall-through to apps/server). This is the inverse of the earlier slices' "un-ported → 404" guard: it
// now confirms nothing in the F4 content bucket is left un-mounted (5.1 = 12/12).
func TestPrdRoutes_coexistence(t *testing.T) {
	srv := httptest.NewServer(buildPrdHandler(fakeContentFS{}).Routes())
	defer srv.Close()
	res, err := http.Post(srv.URL+"/api/roadmap/plan-start", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode == 404 {
		t.Fatalf("/api/roadmap/plan-start 404s — slice 6 should own it (bucket incomplete)")
	}
}
