package adapter

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/drayanaindra/saki-cli/backend/domain"
	"github.com/drayanaindra/saki-cli/backend/infra"
)

// I8 — the STANDALONE backend's four coexistence routes, exercised through the REAL mux with the REAL
// infra.NullProxy (not a fake). A fake proxy would only prove the handler calls it; these prove what a
// client actually receives when there is no apps/server to forward to.
//
// This is the seam the unit tests could not reach: infra/nullproxy_test.go pins the proxy's own
// behavior, but only the mux proves the STATUS survives the handler (e.g. that the SSE handler has not
// already written a 200 before delegating).

func TestRoutes_NullProxy_UnownedEventsIs404(t *testing.T) {
	srv := httptest.NewServer(buildStreamHandler(newFakeStore(), &fakeOutput{}, &infra.NullProxy{}).Routes())
	defer srv.Close()

	res, err := http.Get(srv.URL + "/events/a-run-this-backend-never-owned")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 — standalone has no upstream to tail an un-owned run from", res.StatusCode)
	}
	// Guards the real failure mode: a proxy that held the connection open would leave the client
	// hanging on an EventSource forever. Reading to EOF proves the response is complete and closed.
	if _, err := io.ReadAll(res.Body); err != nil {
		t.Fatalf("body did not terminate — the SSE connection was left open: %v", err)
	}
}

func TestRoutes_NullProxy_ForwardRunsIs501(t *testing.T) {
	srv := httptest.NewServer(buildHandler(nil, &fakeSpawner{}, &fakeJournal{writable: true}, newFakeStore(), &infra.NullProxy{}).Routes())
	defer srv.Close()

	res := post(t, srv, "/api/runs", `{}`)
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501 — the hosted POST /api/runs has no upstream standalone", res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	if !json.Valid(body) {
		t.Fatalf("body %q is not JSON — the adapter relays the proxy's body verbatim, so it must be", body)
	}
}

// The un-owned STOP path forwards too (http.go:357-358), so standalone it answers 501 — NOT the 404 the
// route's doc-comment implies for an unknown run. Missed in the plan's first draft; pinned here so the
// documented behavior and the real behavior cannot drift apart again.
func TestRoutes_NullProxy_UnownedStopIs501(t *testing.T) {
	srv := httptest.NewServer(buildStopHandler(newFakeStore(), &fakeKiller{}, &infra.NullProxy{}).Routes())
	defer srv.Close()

	res := post(t, srv, "/api/run/a-run-this-backend-never-owned/stop", `{}`)
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501 (un-owned stop forwards; standalone has no upstream)", res.StatusCode)
	}
}

// The run list must still answer 200 and return Go's OWN runs. NullProxy.GetRuns returns (nil, nil), so
// ListService walks an empty upstream list rather than taking its degrade branch — the union simply has
// nothing to union.
//
// The store is SEEDED on purpose. An empty store would make this test pass even if List() dropped Go's
// runs entirely, and equally if GetRuns had returned an error (the degrade branch yields the same empty
// body) — so it would assert almost nothing. Seeding makes the local run the actual subject: it must
// survive a union whose upstream half is absent.
func TestRoutes_NullProxy_RunsListIsGoOnly(t *testing.T) {
	st := newFakeStore()
	st.Put(domain.Run{ID: "go-owned-1", Status: "running", StartedAt: 10})
	srv := httptest.NewServer(buildHandler(nil, &fakeSpawner{}, &fakeJournal{writable: true}, st, &infra.NullProxy{}).Routes())
	defer srv.Close()

	res, err := http.Get(srv.URL + "/api/runs")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 — an absent upstream is 'no upstream runs', not a failure", res.StatusCode)
	}
	var runs []map[string]any
	if err := json.NewDecoder(res.Body).Decode(&runs); err != nil {
		t.Fatalf("run list is not a JSON array: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs = %v, want exactly the 1 Go-owned run — standalone must not drop local runs", runs)
	}
	if id, _ := runs[0]["id"].(string); id != "go-owned-1" {
		t.Fatalf("run id = %q, want go-owned-1 — the local run must survive the empty union", id)
	}
}
