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

// F4 · P3 slice 3: adapter-level tests for POST /api/lock-prd. The usecase suite proves the lock
// logic; these lock the HTTP wiring — JSON body decode, the injected date reaching the marker, the
// graceful status codes, and (BLOCKER-1) that the route is OriginGuard-wrapped like the git-write bucket.

func buildLockHandler(fs fakeContentFS, w usecase.ContentWriter, g usecase.GitUserReader) Handler {
	st := newFakeStore()
	px := &fakeProxy{}
	rs := usecase.NewRunService(&fakeSpawner{}, &fakeJournal{writable: true}, st, fakeClock{}, fakeIDGen{})
	ss := usecase.NewStreamService(st, &fakeOutput{}, fakeClock{}, time.Millisecond)
	sk := usecase.NewStopService(st, &fakeKiller{})
	eng := newEngine(&fakeSpawner{}, &fakeJournal{writable: true}, st, &fakeKiller{})
	return NewHandler(usecase.NewBranchService(fakeBranchReader{}), rs, eng, usecase.NewListService(st, px, fakeFullOutput{}), ss, sk, px,
		gitWriteSvc(fakeGitWriter{}), emptyRoadmap(), emptyWorkitems(), emptyPrd(), lockSvc(fs, w, g), emptyBlockers(), emptySliceMeta(), emptyResolve(), emptyPlanTrack())
}

func postJSON(t *testing.T, url, jsonBody string) (int, map[string]any) {
	t.Helper()
	res, err := http.Post(url, "application/json", strings.NewReader(jsonBody))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var m map[string]any
	json.NewDecoder(res.Body).Decode(&m)
	return res.StatusCode, m
}

// AC 3.1: an unlocked PRD → {ok:true,locked:true}; the handler's injected date reaches the marker.
func TestLockRoute_fresh(t *testing.T) {
	fs := fakeContentFS{files: map[string]string{"/r/tasks/prd-x.md": "**Owner:** Alice · **Status:** Draft"}}
	w := &fakeContentWriter{writes: map[string]string{}}
	srv := httptest.NewServer(buildLockHandler(fs, w, fakeGitUser{}).Routes())
	defer srv.Close()

	status, m := postJSON(t, srv.URL+"/api/lock-prd", `{"cwd":"/r","path":"/r/tasks/prd-x.md"}`)
	if status != 200 || m["ok"] != true || m["locked"] != true {
		t.Fatalf("want 200 ok+locked, got %d %#v", status, m)
	}
	got := w.writes["/r/tasks/prd-x.md"]
	today := time.Now().UTC().Format("2006-01-02")
	if !strings.Contains(got, "<!-- prd-locked: @Alice · "+today) || !strings.Contains(got, "**Status:** Locked") {
		t.Errorf("marker/date/status not wired: %q", got)
	}
}

// AC 3.2 / R2: an already-locked PRD → {ok:true,alreadyLocked:true}, no write.
func TestLockRoute_idempotent(t *testing.T) {
	locked := "<!-- prd-locked: @a · 2026-01-01 · ui:none -->\n**Status:** Locked"
	fs := fakeContentFS{files: map[string]string{"/r/tasks/prd-x.md": locked}}
	w := &fakeContentWriter{writes: map[string]string{}}
	srv := httptest.NewServer(buildLockHandler(fs, w, fakeGitUser{}).Routes())
	defer srv.Close()

	status, m := postJSON(t, srv.URL+"/api/lock-prd", `{"cwd":"/r","path":"/r/tasks/prd-x.md"}`)
	if status != 200 || m["alreadyLocked"] != true {
		t.Fatalf("want alreadyLocked, got %d %#v", status, m)
	}
	if len(w.writes) != 0 {
		t.Errorf("R2: rewrote an already-locked PRD: %#v", w.writes)
	}
}

// AC 3.3 / R3: escaping + non-.md paths → 422 over HTTP.
func TestLockRoute_badPaths(t *testing.T) {
	srv := httptest.NewServer(buildLockHandler(fakeContentFS{}, &fakeContentWriter{}, fakeGitUser{}).Routes())
	defer srv.Close()
	for _, p := range []string{"../evil.md", "/r/foo.txt"} {
		if s, _ := postJSON(t, srv.URL+"/api/lock-prd", `{"cwd":"/r","path":"`+p+`"}`); s != 422 {
			t.Errorf("path=%q status %d, want 422", p, s)
		}
	}
}

// BLOCKER-1 fix: the write route IS OriginGuard-wrapped — a cross-origin POST is 403'd (parity with
// the TS global originGuard). Proves lock-prd is not reachable via localhost-CSRF / DNS-rebind.
func TestLockRoute_originGuarded(t *testing.T) {
	fs := fakeContentFS{files: map[string]string{"/r/tasks/prd-x.md": "**Status:** Draft"}}
	w := &fakeContentWriter{writes: map[string]string{}}
	srv := httptest.NewServer(buildLockHandler(fs, w, fakeGitUser{}).Routes())
	defer srv.Close()

	req, _ := http.NewRequest("POST", srv.URL+"/api/lock-prd", strings.NewReader(`{"cwd":"/r","path":"/r/tasks/prd-x.md"}`))
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
