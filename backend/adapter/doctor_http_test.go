package adapter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/drayanaindra/saki-cli/backend/domain"
	"github.com/drayanaindra/saki-cli/backend/infra"
	"github.com/drayanaindra/saki-cli/backend/usecase"
)

type fakeEngineProofs struct {
	err           map[domain.RunEngine]error
	lastConfigDir *string
	profileCalls  int
}

func (f *fakeEngineProofs) BinaryCheck(domain.RunEngine) error { return nil }
func (f *fakeEngineProofs) ProfileProof(engine domain.RunEngine, configDir *string) error {
	f.lastConfigDir = configDir
	f.profileCalls++
	return f.err[engine]
}

func doctorHandlerFor(f usecase.EngineProofs) Handler {
	return Handler{doctor: usecase.NewDoctorService(f)}
}

func assertDoctorEngines(t *testing.T, engines []domain.EngineReport) {
	t.Helper()
	if len(engines) != 4 {
		t.Fatalf("engines = %+v, want 4 entries", engines)
	}
	want := []string{"codex", "opencode", "omp", "claude"}
	for i, engine := range engines {
		if engine.Engine != want[i] {
			t.Errorf("engines[%d].Engine = %q, want %q", i, engine.Engine, want[i])
		}
	}
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
	assertDoctorEngines(t, body.Engines)
	if f.profileCalls != 4 {
		t.Fatalf("profile calls = %d, want 4", f.profileCalls)
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

func TestDoctorHandler_RealWiring(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	profileDir := t.TempDir()

	h := NewHandler(
		usecase.NewBranchService(fakeBranchReader{}), usecase.RunService{}, nil,
		usecase.ListService{}, usecase.StreamService{}, usecase.StopService{}, &fakeProxy{},
		gitWriteSvc(fakeGitWriter{}), emptyRoadmap(), emptyWorkitems(), emptyPrd(), emptyLock(),
		emptyBlockers(), emptySliceMeta(), emptyResolve(), emptyPlanTrack(),
		usecase.NewDoctorService(infra.EngineProofChecker{}),
		usecase.NewInitEnvService(infra.EngineProvisioner{}, infra.EngineProofChecker{}),
	)
	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	res, err := http.Get(srv.URL + "/api/doctor?profile=" + url.QueryEscape(profileDir))
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
	assertDoctorEngines(t, body.Engines)
	if body.Engines[0].Status != "failed" || body.Engines[1].Status != "failed" || body.Engines[2].Status != "failed" || body.Engines[3].Status != "failed" {
		t.Fatalf("engines = %+v, want all four reports failed for missing profiles/binaries", body.Engines)
	}
}

func TestDoctorHandler_RejectsNonLoopbackHost(t *testing.T) {
	f := &fakeEngineProofs{}
	mux := doctorHandlerFor(f).Routes()
	req := httptest.NewRequest(http.MethodGet, "/api/doctor", nil)
	req.Host = "evil.com"
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("doctor route want 403 on non-loopback host, got %d", rec.Code)
	}
	if f.profileCalls != 0 {
		t.Fatalf("profile calls = %d, want 0 for rejected request", f.profileCalls)
	}
}
