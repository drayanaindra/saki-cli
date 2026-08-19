package adapter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/drayanaindra/saki-cli/backend/infra"
	"github.com/drayanaindra/saki-cli/backend/usecase"
)

// countingProvisioner is the whole point of this file: criterion 1.5 does not merely require the
// right status code, it requires that NO adapter was invoked. A rejected request that still spawned
// `codex plugin add` would return a correct-looking 422 while having already mutated a profile, so
// the call count is the assertion and the status is the corroboration.
type countingProvisioner struct{ calls int }

func (p *countingProvisioner) Provision(usecase.ProvisionRequest) (bool, error) {
	p.calls++
	return true, nil
}

func initEnvHandlerFor(p usecase.EngineProvisioner) Handler {
	return Handler{initEnv: usecase.NewInitEnvService(p, &fakeEngineProofs{})}
}

func postInitEnv(t *testing.T, mux *http.ServeMux, body string, host string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/init-env", strings.NewReader(body))
	// httptest defaults Host to example.com, which OriginGuard correctly 403s — so every case that
	// is NOT testing the guard must opt in to loopback explicitly.
	req.Host = "127.0.0.1:8788"
	if host != "" {
		req.Host = host
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// 🔒 Loopback-only. The route is mounted OriginGuard-wrapped (http.go), so a DNS-rebind or
// cross-origin call is refused before the handler body — and therefore before any child process.
// This is the init-env twin of TestDoctorHandler_RejectsNonLoopbackHost.
func TestInitEnvHandlerRejectsNonLoopbackHost(t *testing.T) {
	spy := &countingProvisioner{}
	rec := postInitEnv(t, initEnvHandlerFor(spy).Routes(), `{"cwd":"/tmp","engine":"codex"}`, "evil.com")

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 on a non-loopback host", rec.Code)
	}
	if spy.calls != 0 {
		t.Fatalf("a cross-origin request reached the provisioner %d times, want 0", spy.calls)
	}
}

// The 422 family — each rejected before the adapter. Table-driven because the invariant under test is
// identical across them: bad input never reaches a child process.
func TestInitEnvHandlerRejectsBadInputWithoutInvokingAnAdapter(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"unknown engine", `{"cwd":"/tmp","engine":"nope"}`},
		{"empty engine", `{"cwd":"/tmp","engine":""}`},
		{"relative escaping profile", `{"cwd":"/tmp","engine":"codex","profile":"../outside"}`},
		{"relative profile", `{"cwd":"/tmp","engine":"codex","profile":"relative/dir"}`},
		{"relative cwd", `{"cwd":"relative","engine":"codex"}`},
		{"nonexistent cwd", `{"cwd":"/definitely/not/here","engine":"codex"}`},
		{"malformed body", `{"cwd":`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spy := &countingProvisioner{}
			rec := postInitEnv(t, initEnvHandlerFor(spy).Routes(), tc.body, "")

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422", rec.Code)
			}
			if spy.calls != 0 {
				t.Fatalf("provisioner invoked %d times on invalid input, want 0", spy.calls)
			}
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("422 body is not JSON: %v", err)
			}
			if body["error"] == nil {
				t.Fatalf("422 body has no error field: %s", rec.Body.String())
			}
		})
	}
}

// Slice 2 lands the opencode adapter. With the fake proofs passing, a well-formed opencode request
// short-circuits at the proof (already-proven profile → `ok`, `changed:false`) WITHOUT invoking the
// adapter — the structural idempotency of criterion 2.2/2.4. The request is well-formed, so the status
// is a normal 200 verdict body, not a 422.
func TestInitEnvHandlerClaudeIsNotVerifiedWithoutInvokingAnAdapter(t *testing.T) {
	spy := &countingProvisioner{}
	rec := postInitEnv(t, initEnvHandlerFor(spy).Routes(), `{"cwd":"/tmp","engine":"claude"}`, "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "not_verified" || body["changed"] != false || body["fix"] != "" {
		t.Fatalf("body = %v, want not_verified/changed:false/fix:\"\"", body)
	}
	if body["reason"] != usecase.ErrInitEnvUnsupported.Error()+" (claude requires F4's installed + enabled plugin proof)" {
		t.Fatalf("reason = %v, want explicit F4 dependency", body["reason"])
	}
	if spy.calls != 0 {
		t.Fatalf("claude reached the provisioner %d times, want 0", spy.calls)
	}
}

func TestInitEnvHandlerOpencodeShortCircuitsAtProof(t *testing.T) {
	spy := &countingProvisioner{}
	rec := postInitEnv(t, initEnvHandlerFor(spy).Routes(), `{"cwd":"/tmp","engine":"opencode"}`, "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (a well-formed opencode request)", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" {
		t.Fatalf("status = %v, want ok on an already-proven opencode profile", body["status"])
	}
	if body["changed"] != false {
		t.Fatalf("changed = %v, want false (adapter not invoked)", body["changed"])
	}
	if spy.calls != 0 {
		t.Fatalf("opencode reached the provisioner %d times on an already-proven profile, want 0", spy.calls)
	}
}

// The production wiring itself — the one test that would catch a zero-value InitEnvService reaching
// main.go. POST /api/init-env is registered unconditionally, so nil ports would nil-panic on the
// first real call rather than failing cleanly. Exactly TestDoctorHandler_RealWiring's rationale.
func TestInitEnvHandlerRealWiring(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no codex on PATH -> a deterministic, real failure

	h := NewHandler(
		usecase.NewBranchService(fakeBranchReader{}), usecase.RunService{}, nil,
		usecase.ListService{}, usecase.StreamService{}, usecase.StopService{}, &fakeProxy{},
		gitWriteSvc(fakeGitWriter{}), emptyRoadmap(), emptyWorkitems(), emptyPrd(), emptyLock(),
		emptyBlockers(), emptySliceMeta(), emptyResolve(), emptyPlanTrack(), emptyDoctor(),
		usecase.NewInitEnvService(infra.EngineProvisioner{}, infra.EngineProofChecker{}),
	)
	rec := postInitEnv(t, h.Routes(), `{"cwd":"/tmp","engine":"codex"}`, "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (nil ports would panic, not fail cleanly)", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "failed" {
		t.Fatalf("status = %v, want failed with codex absent", body["status"])
	}
	// Every failure carries remediation — an agent must never get a bare failure it cannot act on.
	if fix, _ := body["fix"].(string); !strings.Contains(fix, "codex plugin") {
		t.Fatalf("fix = %q, want the codex remediation", fix)
	}
}

// The success body's shape is the CLI's contract (src/types.ts InitEnvResult). Locked here because a
// missing or retyped field silently breaks every agent branching on --json.
func TestInitEnvHandlerSuccessBodyCarriesTheDocumentedShape(t *testing.T) {
	spy := &countingProvisioner{}
	rec := postInitEnv(t, initEnvHandlerFor(spy).Routes(), `{"cwd":"/tmp","engine":"codex"}`, "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Engine  string `json:"engine"`
		Profile string `json:"profile"`
		Changed bool   `json:"changed"`
		Status  string `json:"status"`
		Reason  string `json:"reason"`
		Fix     string `json:"fix"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body does not decode into InitEnvResult's shape: %v", err)
	}
	// fakeEngineProofs passes every proof, so the profile is already provable: the adapter must be
	// skipped entirely (BR3 idempotency) and the verdict must be ok with fix cleared.
	if body.Engine != "codex" || body.Status != "ok" || body.Changed || body.Fix != "" {
		t.Fatalf("body = %+v, want codex/ok/changed:false/fix:\"\"", body)
	}
	if body.Profile != "default" {
		t.Fatalf("profile = %q, want doctor's own label for an unpinned profile", body.Profile)
	}
	if spy.calls != 0 {
		t.Fatalf("an already-provable profile reached the provisioner %d times, want 0", spy.calls)
	}
}
