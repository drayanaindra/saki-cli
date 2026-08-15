package adapter

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/drayanaindra/saki-cli/backend/usecase"
)

// F4 · P3 slice 6: adapter-level tests for the three plan-track WRITE routes — POST
// /api/roadmap/plan-start | plan-ship | plan-attach. The usecase suite (usecase/plantrack_test.go)
// proves the decision table (flip / no-op / R3 422 / R6 graceful); these lock the HTTP wiring — the
// {cwd,id,planFile} decode, the injected date reaching the marker, and (security 5.5 / BR7) that all
// three writes are OriginGuard-wrapped like the lock-prd + resolve-blocker bucket.

const planTrackRoadmap = `# Roadmap: Test

### I1 · An improvement
**Type:** Improvement · **Track:** Plan · **Status:** Planned · **Owner:** unassigned · **Updated:** 2026-01-01
**What:** do a thing.
**Child plan:** —
`

func buildPlanTrackHandler(fs fakeContentFS, w usecase.ContentWriter) Handler {
	st := newFakeStore()
	px := &fakeProxy{}
	rs := usecase.NewRunService(&fakeSpawner{}, &fakeJournal{writable: true}, st, fakeClock{}, fakeIDGen{})
	ss := usecase.NewStreamService(st, &fakeOutput{}, fakeClock{}, time.Millisecond)
	sk := usecase.NewStopService(st, &fakeKiller{})
	eng := newEngine(&fakeSpawner{}, &fakeJournal{writable: true}, st, &fakeKiller{})
	return NewHandler(usecase.NewBranchService(fakeBranchReader{}), rs, eng, usecase.NewListService(st, px, fakeFullOutput{}), ss, sk, px,
		gitWriteSvc(fakeGitWriter{}), emptyRoadmap(), emptyWorkitems(), emptyPrd(), emptyLock(),
		emptyBlockers(), emptySliceMeta(), emptyResolve(), planTrackSvc(fs, w), emptyDoctor())
}

func planFSFor(content string) fakeContentFS {
	return fakeContentFS{files: map[string]string{"/repo/tasks/roadmap.md": content}}
}

// AC 6.1: POST /api/roadmap/plan-start flips a Planned Plan-track item → In-progress, {ok,changed:true},
// and the transition lands in the write (the handler injects today's date at the seam).
func TestPlanStartRoute_flip(t *testing.T) {
	fs := planFSFor(planTrackRoadmap)
	w := &fakeContentWriter{writes: map[string]string{}}
	srv := httptest.NewServer(buildPlanTrackHandler(fs, w).Routes())
	defer srv.Close()

	status, m := postJSON(t, srv.URL+"/api/roadmap/plan-start", `{"cwd":"/repo","id":"I1"}`)
	if status != 200 || m["ok"] != true || m["changed"] != true {
		t.Fatalf("want 200 ok+changed, got %d %#v", status, m)
	}
	if !strings.Contains(w.writes["/repo/tasks/roadmap.md"], "**Status:** In-progress · **Owner:** unassigned") {
		t.Errorf("transition not written: %q", w.writes["/repo/tasks/roadmap.md"])
	}
}

// AC 6.2 / R4: a PRD-track / unknown id → changed:false, roadmap.md byte-identical (no write).
func TestPlanStartRoute_noOpNoWrite(t *testing.T) {
	fs := planFSFor(planTrackRoadmap)
	w := &fakeContentWriter{writes: map[string]string{}}
	srv := httptest.NewServer(buildPlanTrackHandler(fs, w).Routes())
	defer srv.Close()

	status, m := postJSON(t, srv.URL+"/api/roadmap/plan-start", `{"cwd":"/repo","id":"F9"}`)
	if status != 200 || m["changed"] != false {
		t.Fatalf("want 200 changed:false, got %d %#v", status, m)
	}
	if len(w.writes) != 0 {
		t.Errorf("R4: a no-op transition must not write: %#v", w.writes)
	}
}

// AC 6.3: plan-ship advances In-progress → Shipped over HTTP.
func TestPlanShipRoute_flip(t *testing.T) {
	md := strings.Replace(planTrackRoadmap, "**Status:** Planned", "**Status:** In-progress", 1)
	fs := planFSFor(md)
	w := &fakeContentWriter{writes: map[string]string{}}
	srv := httptest.NewServer(buildPlanTrackHandler(fs, w).Routes())
	defer srv.Close()

	status, m := postJSON(t, srv.URL+"/api/roadmap/plan-ship", `{"cwd":"/repo","id":"I1"}`)
	if status != 200 || m["changed"] != true {
		t.Fatalf("want ship changed:true, got %d %#v", status, m)
	}
	if !strings.Contains(w.writes["/repo/tasks/roadmap.md"], "**Status:** Shipped") {
		t.Errorf("not shipped: %q", w.writes["/repo/tasks/roadmap.md"])
	}
}

// AC 6.3: plan-attach records **Child plan:**; a bad planFile → 422, nothing written.
func TestPlanAttachRoute_recordsAndGuards(t *testing.T) {
	fs := planFSFor(planTrackRoadmap)
	w := &fakeContentWriter{writes: map[string]string{}}
	srv := httptest.NewServer(buildPlanTrackHandler(fs, w).Routes())
	defer srv.Close()

	status, m := postJSON(t, srv.URL+"/api/roadmap/plan-attach", `{"cwd":"/repo","id":"I1","planFile":"i1-plan.md"}`)
	if status != 200 || m["changed"] != true {
		t.Fatalf("want attach changed:true, got %d %#v", status, m)
	}
	if !strings.Contains(w.writes["/repo/tasks/roadmap.md"], "**Child plan:** i1-plan.md") {
		t.Errorf("child plan not recorded: %q", w.writes["/repo/tasks/roadmap.md"])
	}
	// A multiline planFile is rejected at 422 before any write (R3 guard).
	if s, _ := postJSON(t, srv.URL+"/api/roadmap/plan-attach", `{"cwd":"/repo","id":"I1","planFile":"a\nb.md"}`); s != 422 {
		t.Errorf("bad planFile status %d, want 422", s)
	}
}

// AC 6.3 / R3: a missing/escaping cwd or malformed id → 422 (never 500), nothing written.
func TestPlanStartRoute_badCwdOrId422(t *testing.T) {
	fs := planFSFor(planTrackRoadmap)
	w := &fakeContentWriter{writes: map[string]string{}}
	srv := httptest.NewServer(buildPlanTrackHandler(fs, w).Routes())
	defer srv.Close()

	if s, _ := postJSON(t, srv.URL+"/api/roadmap/plan-start", `{"id":"I1"}`); s != 422 {
		t.Errorf("missing cwd status %d, want 422", s)
	}
	if s, _ := postJSON(t, srv.URL+"/api/roadmap/plan-start", `{"cwd":"/repo","id":"nope"}`); s != 422 {
		t.Errorf("bad id status %d, want 422", s)
	}
	if len(w.writes) != 0 {
		t.Errorf("R3: nothing must be written on a 422: %#v", w.writes)
	}
}

// security 5.5 / BR7: all three writes are OriginGuard-wrapped — a cross-origin POST is 403'd and
// mutates nothing (TS global app.use(originGuard) parity).
func TestPlanTrackRoutes_originGuarded(t *testing.T) {
	for _, path := range []string{"/api/roadmap/plan-start", "/api/roadmap/plan-ship", "/api/roadmap/plan-attach"} {
		fs := planFSFor(planTrackRoadmap)
		w := &fakeContentWriter{writes: map[string]string{}}
		srv := httptest.NewServer(buildPlanTrackHandler(fs, w).Routes())

		req, _ := http.NewRequest("POST", srv.URL+path, strings.NewReader(`{"cwd":"/repo","id":"I1","planFile":"i1-plan.md"}`))
		req.Header.Set("Origin", "http://evil.com")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != 403 {
			t.Errorf("%s cross-origin status %d, want 403 (OriginGuard)", path, res.StatusCode)
		}
		res.Body.Close()
		if len(w.writes) != 0 {
			t.Errorf("%s: a cross-origin request mutated a file: %#v", path, w.writes)
		}
		srv.Close()
	}
}
