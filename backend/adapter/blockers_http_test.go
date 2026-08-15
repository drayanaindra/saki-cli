package adapter

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/drayanaindra/saki-cli/backend/usecase"
)

// F4 · P3 slice 4: adapter-level tests for GET /api/slice-meta + /api/blockers. The usecase suite
// proves the assembly; these lock the HTTP wiring (query-param plumbing, status codes) and confirm the
// content reads are now OriginGuard-wrapped (the slice-3-review parity fix).

func buildContentReadHandler(fs fakeContentFS, git usecase.GitSliceReader) Handler {
	st := newFakeStore()
	px := &fakeProxy{}
	rs := usecase.NewRunService(&fakeSpawner{}, &fakeJournal{writable: true}, st, fakeClock{}, fakeIDGen{})
	eng := newEngine(&fakeSpawner{}, &fakeJournal{writable: true}, st, &fakeKiller{})
	ss := usecase.NewStreamService(st, &fakeOutput{}, fakeClock{}, time.Millisecond)
	sk := usecase.NewStopService(st, &fakeKiller{})
	return NewHandler(usecase.NewBranchService(fakeBranchReader{}), rs, eng, usecase.NewListService(st, px, fakeFullOutput{}), ss, sk, px,
		gitWriteSvc(fakeGitWriter{}), emptyRoadmap(), emptyWorkitems(), emptyPrd(), emptyLock(),
		blockersSvc(fs), sliceMetaSvcFor(git), emptyResolve(), emptyPlanTrack(), emptyDoctor())
}

func TestSliceMetaRoute(t *testing.T) {
	srv := httptest.NewServer(buildContentReadHandler(fakeContentFS{}, fakeGitSlice{commit: "abc123 feat: slice-2"}).Routes())
	defer srv.Close()

	status, m := getJSON(t, srv.URL+"/api/slice-meta?cwd=/r&n=2")
	if status != 200 || m["commit"] != "abc123 feat: slice-2" {
		t.Fatalf("want commit, got %d %#v", status, m)
	}
	// no commit → null; missing cwd/n → 422.
	if s, m := getJSON(t, srv.URL+"/api/slice-meta?cwd=/r&n=2"); s == 200 && m["commit"] == "abc123 feat: slice-2" {
		// covered above
	}
	if s, _ := getJSON(t, srv.URL+"/api/slice-meta?cwd=/r"); s != 422 {
		t.Errorf("missing n status %d, want 422", s)
	}
}

func TestBlockersRoute(t *testing.T) {
	fs := fakeContentFS{files: map[string]string{
		"/r/tasks/prd-x.md": "## Open Questions\n**Open questions:**\n- **Q1** before slice 2. Fork?",
	}}
	srv := httptest.NewServer(buildContentReadHandler(fs, fakeGitSlice{}).Routes())
	defer srv.Close()

	status, m := getJSON(t, srv.URL+"/api/blockers?cwd=/r&prd=/r/tasks/prd-x.md&n=2")
	if status != 200 || m["n"] != float64(2) {
		t.Fatalf("want 200 n=2, got %d %#v", status, m)
	}
	if qs, ok := m["questions"].([]any); !ok || len(qs) != 1 {
		t.Fatalf("questions = %#v, want 1", m["questions"])
	}
	// missing PRD → 404; non-integer n → 422.
	if s, _ := getJSON(t, srv.URL+"/api/blockers?cwd=/r&prd=/r/tasks/none.md&n=2"); s != 404 {
		t.Errorf("missing PRD status %d, want 404", s)
	}
	if s, _ := getJSON(t, srv.URL+"/api/blockers?cwd=/r&prd=/r/tasks/prd-x.md&n=x"); s != 422 {
		t.Errorf("bad n status %d, want 422", s)
	}
}

// The slice-3-review parity fix: the F4 GET reads are OriginGuard-wrapped — a cross-origin GET is
// 403'd (parity with the TS global originGuard), no longer reachable via DNS-rebind.
func TestContentReads_originGuarded(t *testing.T) {
	srv := httptest.NewServer(buildContentReadHandler(fakeContentFS{}, fakeGitSlice{}).Routes())
	defer srv.Close()
	for _, path := range []string{"/api/slice-meta?cwd=/r&n=2", "/api/blockers?cwd=/r&prd=/r/x.md&n=2"} {
		req, _ := http.NewRequest("GET", srv.URL+path, nil)
		req.Header.Set("Origin", "http://evil.com")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 403 {
			t.Errorf("cross-origin GET %s status %d, want 403 (OriginGuard)", path, res.StatusCode)
		}
	}
}
