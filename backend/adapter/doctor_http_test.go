package adapter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/drayanaindra/saki-cli/backend/domain"
	"github.com/drayanaindra/saki-cli/backend/infra"
	"github.com/drayanaindra/saki-cli/backend/usecase"
)

// fakeEngineProofs is the adapter-layer twin of usecase's own unexported fake — needed here because
// usecase.DoctorService is a concrete struct (not an interface), so testing the HTTP handler in
// isolation means injecting a fake EngineProofs, not a fake DoctorService.
type fakeEngineProofs struct {
	err           map[domain.RunEngine]error
	lastConfigDir *string
}

func (f *fakeEngineProofs) BinaryCheck(domain.RunEngine) error { return nil }
func (f *fakeEngineProofs) ProfileProof(engine domain.RunEngine, configDir *string) error {
	f.lastConfigDir = configDir
	return f.err[engine]
}

func doctorHandlerFor(f usecase.EngineProofs) Handler {
	return Handler{doctor: usecase.NewDoctorService(f)}
}

func TestDoctorHandler_ReturnsEngines(t *testing.T) {
	f := &fakeEngineProofs{}
	srv := httptest.NewServer(doctorHandlerFor(f).Routes())
	defer srv.Close()

	res, err := http.Get(srv.URL + "/api/doctor")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	var body struct {
		Engines []domain.EngineReport `json:"engines"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Engines) != 2 {
		t.Fatalf("engines = %+v, want 2 entries", body.Engines)
	}

	res2, err := http.Get(srv.URL + "/api/doctor?profile=/tmp/x")
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close()
	if f.lastConfigDir == nil || *f.lastConfigDir != "/tmp/x" {
		t.Fatalf("configDir threaded to ProfileProof = %v, want \"/tmp/x\"", f.lastConfigDir)
	}
}

// F2 slice 1 step 7's own gate (QA-mandated): proves main.go's ACTUAL production construction —
// NewHandler(..., usecase.NewDoctorService(infra.EngineProofChecker{})) — actually works end to end.
// Nothing else in this suite goes through the production constructor; the other doctor tests and every
// unrelated NewHandler call site inject a fake or the zero-value DoctorService. A DoctorService{}
// zero-value accidentally pasted into main.go would compile fine everywhere else and panic on the
// first real `saki doctor` call (a nil EngineProofs) — this is the one test that would catch it.
func TestDoctorHandler_RealWiring(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // neither codex nor opencode on PATH -> a real, deterministic failure

	h := NewHandler(
		usecase.NewBranchService(fakeBranchReader{}), usecase.RunService{}, nil,
		usecase.ListService{}, usecase.StreamService{}, usecase.StopService{}, &fakeProxy{},
		gitWriteSvc(fakeGitWriter{}), emptyRoadmap(), emptyWorkitems(), emptyPrd(), emptyLock(),
		emptyBlockers(), emptySliceMeta(), emptyResolve(), emptyPlanTrack(),
		usecase.NewDoctorService(infra.EngineProofChecker{}),
	)
	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	res, err := http.Get(srv.URL + "/api/doctor")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (a nil EngineProofs would panic, not fail cleanly)", res.StatusCode)
	}
	var body struct {
		Engines []domain.EngineReport `json:"engines"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Engines) != 2 || body.Engines[0].Status != "failed" {
		t.Fatalf("engines = %+v, want 2 entries with codex failed (binary missing)", body.Engines)
	}
}
