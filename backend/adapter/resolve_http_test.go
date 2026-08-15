package adapter

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/drayanaindra/saki-cli/backend/usecase"
)

// F4 · P3 slice 5: adapter-level tests for POST /api/resolve-blocker. The usecase suite proves the
// decision table; these lock the HTTP wiring — the {prdPath, decisions[]} decode, the status codes,
// and that the write route is OriginGuard-wrapped like lock-prd (BR7 — TS global-guard parity).

func buildResolveHandler(fs fakeContentFS, w usecase.ContentWriter) Handler {
	st := newFakeStore()
	px := &fakeProxy{}
	rs := usecase.NewRunService(&fakeSpawner{}, &fakeJournal{writable: true}, st, fakeClock{}, fakeIDGen{})
	ss := usecase.NewStreamService(st, &fakeOutput{}, fakeClock{}, time.Millisecond)
	sk := usecase.NewStopService(st, &fakeKiller{})
	eng := newEngine(&fakeSpawner{}, &fakeJournal{writable: true}, st, &fakeKiller{})
	return NewHandler(usecase.NewBranchService(fakeBranchReader{}), rs, eng, usecase.NewListService(st, px, fakeFullOutput{}), ss, sk, px,
		gitWriteSvc(fakeGitWriter{}), emptyRoadmap(), emptyWorkitems(), emptyPrd(), emptyLock(),
		emptyBlockers(), emptySliceMeta(), resolveSvc(fs, w), emptyPlanTrack(), emptyDoctor())
}

// AC 5.1c: a matching open-question bullet → prefixed ✅ RESOLVED, {resolved:1}, and the write lands.
func TestResolveRoute_match(t *testing.T) {
	md := "## Rabbit Holes & Open Questions\n**Open questions:**\n- **Q1** before slice 2. Fork?\n"
	fs := fakeContentFS{files: map[string]string{"/r/prd.md": md}}
	w := &fakeContentWriter{writes: map[string]string{}}
	srv := httptest.NewServer(buildResolveHandler(fs, w).Routes())
	defer srv.Close()

	status, m := postJSON(t, srv.URL+"/api/resolve-blocker", `{"prdPath":"/r/prd.md","decisions":[{"question":"Q1","decision":"pick A"}]}`)
	if status != 200 || m["resolved"] != float64(1) {
		t.Fatalf("want 200 resolved:1, got %d %#v", status, m)
	}
	if !strings.Contains(w.writes["/r/prd.md"], "✅ RESOLVED — pick A — **Q1**") {
		t.Errorf("bullet not written: %q", w.writes["/r/prd.md"])
	}
}

// AC 5.3c / R5: an already-recorded decision → {resolved:0} HTTP 200 (idempotent, not 422), no write.
func TestResolveRoute_idempotent(t *testing.T) {
	md := "## Rabbit Holes & Open Questions\n**Open questions:**\n- ✅ RESOLVED — chose B — New fork\n"
	fs := fakeContentFS{files: map[string]string{"/r/prd.md": md}}
	w := &fakeContentWriter{writes: map[string]string{}}
	srv := httptest.NewServer(buildResolveHandler(fs, w).Routes())
	defer srv.Close()

	status, m := postJSON(t, srv.URL+"/api/resolve-blocker", `{"prdPath":"/r/prd.md","decisions":[{"question":"New fork","decision":"chose B"}]}`)
	if status != 200 || m["resolved"] != float64(0) {
		t.Fatalf("want 200 resolved:0, got %d %#v", status, m)
	}
	if len(w.writes) != 0 {
		t.Errorf("R5: an idempotent no-op wrote: %#v", w.writes)
	}
}

// AC 5.4c: an all-invalid decisions[] → 422 "no matching unresolved questions".
func TestResolveRoute_allInvalid422(t *testing.T) {
	fs := fakeContentFS{files: map[string]string{"/r/prd.md": "# PRD"}}
	srv := httptest.NewServer(buildResolveHandler(fs, &fakeContentWriter{}).Routes())
	defer srv.Close()

	if s, _ := postJSON(t, srv.URL+"/api/resolve-blocker", `{"prdPath":"/r/prd.md","decisions":[{"question":"","decision":"d"}]}`); s != 422 {
		t.Errorf("all-invalid status %d, want 422", s)
	}
	// non-.md prdPath and empty decisions are also 422.
	if s, _ := postJSON(t, srv.URL+"/api/resolve-blocker", `{"prdPath":"/r/prd.txt","decisions":[{"question":"q","decision":"d"}]}`); s != 422 {
		t.Errorf("non-.md path status %d, want 422", s)
	}
	if s, _ := postJSON(t, srv.URL+"/api/resolve-blocker", `{"prdPath":"/r/prd.md","decisions":[]}`); s != 422 {
		t.Errorf("empty decisions status %d, want 422", s)
	}
}

// A missing PRD → 404.
func TestResolveRoute_missing404(t *testing.T) {
	srv := httptest.NewServer(buildResolveHandler(fakeContentFS{}, &fakeContentWriter{}).Routes())
	defer srv.Close()
	if s, _ := postJSON(t, srv.URL+"/api/resolve-blocker", `{"prdPath":"/r/gone.md","decisions":[{"question":"q","decision":"d"}]}`); s != 404 {
		t.Errorf("missing PRD status %d, want 404", s)
	}
}

// The write route IS OriginGuard-wrapped — a cross-origin POST is 403'd and mutates nothing.
func TestResolveRoute_originGuarded(t *testing.T) {
	md := "## Rabbit Holes & Open Questions\n**Open questions:**\n- **Q1** a fork\n"
	fs := fakeContentFS{files: map[string]string{"/r/prd.md": md}}
	w := &fakeContentWriter{writes: map[string]string{}}
	srv := httptest.NewServer(buildResolveHandler(fs, w).Routes())
	defer srv.Close()

	req, _ := http.NewRequest("POST", srv.URL+"/api/resolve-blocker", strings.NewReader(`{"prdPath":"/r/prd.md","decisions":[{"question":"Q1","decision":"x"}]}`))
	req.Header.Set("Origin", "http://evil.com")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatalf("cross-origin POST status %d, want 403 (OriginGuard)", res.StatusCode)
	}
	if len(w.writes) != 0 {
		t.Errorf("a cross-origin request mutated a file: %#v", w.writes)
	}
}
