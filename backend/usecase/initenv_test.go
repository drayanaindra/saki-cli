package usecase

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

// spyProvisioner records whether the adapter was reached at all. "Was the installer invoked?" is the
// assertion several criteria turn on (1.4 "no profile files are created", 1.5 "never invokes an
// adapter"), and a call counter proves it structurally instead of inferring it from the result.
type spyProvisioner struct {
	calls   int
	changed bool
	err     error
}

func (p *spyProvisioner) Provision(ProvisionRequest) (bool, error) {
	p.calls++
	return p.changed, p.err
}

// stubProofs replays a SEQUENCE of ProfileProof results, because the service now calls the proof
// twice on the provisioning path (once to test idempotency, once as the verdict) and the two calls
// must be able to disagree — that difference is exactly what "the proof decides, not the installer"
// means. The last entry repeats, so a single-element sequence behaves like a constant.
type stubProofs struct {
	binaryErr    error
	profileErr   []error
	binaryCalls  int
	profileCalls int
	calls        int
}

func (p *stubProofs) BinaryCheck(domain.RunEngine) error {
	p.binaryCalls++
	return p.binaryErr
}

func (p *stubProofs) ProfileProof(domain.RunEngine, *string) error {
	p.profileCalls++
	if len(p.profileErr) == 0 {
		return nil
	}
	i := p.calls
	p.calls++
	if i >= len(p.profileErr) {
		i = len(p.profileErr) - 1
	}
	return p.profileErr[i]
}

var errNotProvisioned = errors.New("codex profile does not resolve @saketek/saki-builder")

func TestInitEnvServiceClaudeAlreadyProvenSkipsAdapter(t *testing.T) {
	dir := t.TempDir()
	adapter := &spyProvisioner{changed: true}
	proofs := &stubProofs{}
	svc := NewInitEnvService(adapter, proofs)

	status, body := svc.Provision(ProvisionRequest{Cwd: dir, Engine: domain.EngineClaude})

	if status != 200 || body["status"] != string(domain.InitEnvStatusOK) || body["changed"] != false {
		t.Fatalf("status=%d body=%v", status, body)
	}
	if adapter.calls != 0 || proofs.binaryCalls != 0 || proofs.profileCalls != 1 {
		t.Fatalf("claude calls: adapter=%d binary=%d profile=%d", adapter.calls, proofs.binaryCalls, proofs.profileCalls)
	}
}

func TestInitEnvServiceAcceptsOMP(t *testing.T) {
	dir := t.TempDir()
	adapter := &spyProvisioner{changed: true}
	proofs := &stubProofs{}
	status, body := NewInitEnvService(adapter, proofs).Provision(ProvisionRequest{Cwd: dir, Engine: domain.EngineOMP})
	if status != 200 || body["status"] != string(domain.InitEnvStatusOK) || adapter.calls != 0 {
		t.Fatalf("a proven OMP profile must be accepted without provisioning: status=%d body=%v adapterCalls=%d", status, body, adapter.calls)
	}
}

func TestInitEnvServiceClaudeProvisionsThenProves(t *testing.T) {
	dir := t.TempDir()
	adapter := &spyProvisioner{changed: true}
	proofs := &stubProofs{profileErr: []error{errNotProvisioned, nil}}
	svc := NewInitEnvService(adapter, proofs)

	status, body := svc.Provision(ProvisionRequest{Cwd: dir, Engine: domain.EngineClaude})

	if status != 200 || body["status"] != string(domain.InitEnvStatusOK) || body["changed"] != true {
		t.Fatalf("status=%d body=%v", status, body)
	}
	if adapter.calls != 1 || proofs.binaryCalls != 0 || proofs.profileCalls != 2 {
		t.Fatalf("claude calls: adapter=%d binary=%d profile=%d", adapter.calls, proofs.binaryCalls, proofs.profileCalls)
	}
}

func TestInitEnvServiceClaudeInstallerExitZeroIsNotProof(t *testing.T) {
	dir := t.TempDir()
	adapter := &spyProvisioner{changed: true}
	proofs := &stubProofs{profileErr: []error{errNotProvisioned, errNotProvisioned}}
	svc := NewInitEnvService(adapter, proofs)

	status, body := svc.Provision(ProvisionRequest{Cwd: dir, Engine: domain.EngineClaude})

	if status != 200 || body["status"] != string(domain.InitEnvStatusFailed) {
		t.Fatalf("status=%d body=%v", status, body)
	}
	if body["fix"] != ClaudeInstallFix {
		t.Fatalf("fix=%v, want ClaudeInstallFix", body["fix"])
	}
}

func TestInitEnvServiceClaudeProofWinsOverInstallerError(t *testing.T) {
	dir := t.TempDir()
	adapter := &spyProvisioner{err: errors.New("already registered")}
	proofs := &stubProofs{profileErr: []error{errNotProvisioned, nil}}
	svc := NewInitEnvService(adapter, proofs)

	status, body := svc.Provision(ProvisionRequest{Cwd: dir, Engine: domain.EngineClaude})

	if status != 200 || body["status"] != string(domain.InitEnvStatusOK) {
		t.Fatalf("status=%d body=%v", status, body)
	}
}

func TestInitEnvServiceRejectsBadCwdBeforeAdapter(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	adapter := &spyProvisioner{}
	svc := NewInitEnvService(adapter, &stubProofs{})

	status, body := svc.Provision(ProvisionRequest{Cwd: dir, Engine: domain.EngineCodex})

	if status != 422 || adapter.calls != 0 || body["error"] == nil {
		t.Fatalf("status=%d calls=%d body=%v", status, adapter.calls, body)
	}
}

func TestInitEnvServiceRejectsRelativeProfileBeforeAdapter(t *testing.T) {
	dir := t.TempDir()
	relative := "../outside"
	adapter := &spyProvisioner{}
	svc := NewInitEnvService(adapter, &stubProofs{})

	status, body := svc.Provision(ProvisionRequest{Cwd: dir, Engine: domain.EngineCodex, Profile: &relative})

	if status != 422 || adapter.calls != 0 || body["error"] == nil {
		t.Fatalf("status=%d calls=%d body=%v", status, adapter.calls, body)
	}
}

// Criterion 1.3 / BR3: an already-provisioned profile is a no-op. The installer is not merely
// harmless on a repeat run — it is never invoked, so it cannot duplicate a plugin registration.
func TestInitEnvServiceAlreadyProvenSkipsAdapter(t *testing.T) {
	dir := t.TempDir()
	profile := filepath.Join(dir, "profile")
	adapter := &spyProvisioner{changed: true}
	svc := NewInitEnvService(adapter, &stubProofs{}) // proof passes on the first call

	status, body := svc.Provision(ProvisionRequest{Cwd: dir, Engine: domain.EngineCodex, Profile: &profile})

	if status != 200 || body["status"] != "ok" {
		t.Fatalf("status=%d body=%v", status, body)
	}
	if body["changed"] != false {
		t.Fatalf("changed=%v, want false on an already-provisioned profile", body["changed"])
	}
	if adapter.calls != 0 {
		t.Fatalf("adapter invoked %d times on an already-provisioned profile, want 0", adapter.calls)
	}
}

// 🔒 BR2, the strict direction — the rule this whole service exists to enforce. A child installer
// that exits 0 proves NOTHING: if the shared proof still fails, the result is `failed`.
func TestInitEnvServiceInstallerExitZeroIsNotProof(t *testing.T) {
	dir := t.TempDir()
	profile := filepath.Join(dir, "profile")
	adapter := &spyProvisioner{changed: true} // installer "succeeded"
	proofs := &stubProofs{profileErr: []error{errNotProvisioned, errNotProvisioned}}
	svc := NewInitEnvService(adapter, proofs)

	status, body := svc.Provision(ProvisionRequest{Cwd: dir, Engine: domain.EngineCodex, Profile: &profile})

	if status != 200 || body["status"] != "failed" {
		t.Fatalf("installer exit 0 with a failing proof must be failed; status=%d body=%v", status, body)
	}
	if reason, _ := body["reason"].(string); !strings.Contains(reason, errNotProvisioned.Error()) {
		t.Fatalf("reason=%q, want the PROOF's reason", reason)
	}
	if body["fix"] != CodexInstallFix {
		t.Fatalf("fix=%v, want CodexInstallFix", body["fix"])
	}
}

// 🔒 BR2, the permissive direction — and the real-world repeat-run path (criterion 1.3). A second
// `codex plugin marketplace add` can exit non-zero with "already added"; if the proof passes, the
// profile IS provisioned and the installer's complaint is not the verdict.
func TestInitEnvServiceProofWinsOverInstallerError(t *testing.T) {
	dir := t.TempDir()
	profile := filepath.Join(dir, "profile")
	adapter := &spyProvisioner{changed: false, err: errors.New("codex provisioning failed: marketplace already added")}
	// Fails the idempotency probe, passes the verdict call after the adapter ran.
	proofs := &stubProofs{profileErr: []error{errNotProvisioned, nil}}
	svc := NewInitEnvService(adapter, proofs)

	status, body := svc.Provision(ProvisionRequest{Cwd: dir, Engine: domain.EngineCodex, Profile: &profile})

	if status != 200 || body["status"] != "ok" {
		t.Fatalf("a passing proof must win over an installer error; status=%d body=%v", status, body)
	}
	if adapter.calls != 1 {
		t.Fatalf("adapter calls=%d, want exactly 1", adapter.calls)
	}
	if body["reason"] != "" {
		t.Fatalf("reason=%v, want empty on success", body["reason"])
	}
}

// The provisioning path proper: proof fails, the adapter runs and reports a real change, the second
// proof passes → ok + changed:true.
func TestInitEnvServiceProvisionsThenProves(t *testing.T) {
	dir := t.TempDir()
	profile := filepath.Join(dir, "profile")
	adapter := &spyProvisioner{changed: true}
	proofs := &stubProofs{profileErr: []error{errNotProvisioned, nil}}
	svc := NewInitEnvService(adapter, proofs)

	status, body := svc.Provision(ProvisionRequest{Cwd: dir, Engine: domain.EngineCodex, Profile: &profile})

	if status != 200 || body["status"] != "ok" || body["changed"] != true {
		t.Fatalf("status=%d body=%v", status, body)
	}
	if adapter.calls != 1 {
		t.Fatalf("adapter calls=%d, want exactly 1", adapter.calls)
	}
}

// Criterion 1.4: a missing binary exits non-zero with a remediation NAMING the binary, and nothing is
// written. The binary check runs before the adapter, so "no profile files are created" is structural
// (calls==0), not merely observed. The infra half of this claim — that the REAL provisioner also
// writes nothing when the binary is absent — is locked by
// TestEngineProvisionerMissingBinaryWritesNothing (backend/infra/initenv_test.go).
func TestInitEnvServiceMissingBinaryReportsFixAndWritesNothing(t *testing.T) {
	dir := t.TempDir()
	profile := filepath.Join(dir, "profile")
	adapter := &spyProvisioner{changed: true}
	proofs := &stubProofs{binaryErr: ErrBinaryNotFound}
	svc := NewInitEnvService(adapter, proofs)

	status, body := svc.Provision(ProvisionRequest{Cwd: dir, Engine: domain.EngineCodex, Profile: &profile})

	if status != 200 || body["status"] != "failed" {
		t.Fatalf("status=%d body=%v", status, body)
	}
	if adapter.calls != 0 {
		t.Fatalf("adapter invoked %d times with no binary, want 0", adapter.calls)
	}
	if body["fix"] != CodexInstallFix {
		t.Fatalf("fix=%v, want CodexInstallFix so the operator is told how to recover", body["fix"])
	}
	if _, err := os.Stat(profile); !os.IsNotExist(err) {
		t.Fatalf("profile dir %s exists after a failed setup; want nothing written", profile)
	}
}

// The result carries the same profile LABEL doctor reports (usecase.profileLabel), so `saki init-env`
// and `saki doctor` name the same profile — outcome 5.1's "setup and doctor agree".
func TestInitEnvServiceProfileLabelMatchesDoctor(t *testing.T) {
	dir := t.TempDir()
	svc := NewInitEnvService(&spyProvisioner{}, &stubProofs{})

	_, unpinned := svc.Provision(ProvisionRequest{Cwd: dir, Engine: domain.EngineCodex})
	if unpinned["profile"] != profileLabel(nil) {
		t.Fatalf("unpinned profile=%v, want %q", unpinned["profile"], profileLabel(nil))
	}

	profile := filepath.Join(dir, "profile")
	_, pinned := svc.Provision(ProvisionRequest{Cwd: dir, Engine: domain.EngineCodex, Profile: &profile})
	if pinned["profile"] != profile {
		t.Fatalf("pinned profile=%v, want %q", pinned["profile"], profile)
	}
}

// Slice 2 lands the opencode adapter. The service now provisions opencode like codex — the proof
// decides (BR2), the adapter runs only when the profile is not already proven, and an already-proven
// opencode profile is a `changed:false` no-op that never reaches the adapter (criterion 2.2/2.4).
func TestInitEnvServiceProvisionsOpencodeThenProves(t *testing.T) {
	dir := t.TempDir()
	adapter := &spyProvisioner{changed: true}
	proofs := &stubProofs{profileErr: []error{errNotProvisioned, nil}}
	svc := NewInitEnvService(adapter, proofs)

	status, body := svc.Provision(ProvisionRequest{Cwd: dir, Engine: domain.EngineOpencode})

	if status != 200 || body["status"] != "ok" {
		t.Fatalf("status=%d body=%v", status, body)
	}
	if body["changed"] != true {
		t.Fatalf("changed=%v, want true on a fresh opencode profile", body["changed"])
	}
	if adapter.calls != 1 {
		t.Fatalf("adapter invoked %d times, want 1", adapter.calls)
	}
	if fix, _ := body["fix"].(string); fix != "" {
		t.Fatalf("fix=%q on a success, want empty (succeed() clears it)", fix)
	}
}

// Criterion 2.2 / 2.4: an already-proven opencode profile is a no-op — the installer is never invoked,
// so it cannot duplicate a plugin entry, and `changed` stays false.
func TestInitEnvServiceOpencodeAlreadyProvenSkipsAdapter(t *testing.T) {
	dir := t.TempDir()
	adapter := &spyProvisioner{changed: true}
	svc := NewInitEnvService(adapter, &stubProofs{}) // proof passes on the first call

	status, body := svc.Provision(ProvisionRequest{Cwd: dir, Engine: domain.EngineOpencode})

	if status != 200 || body["status"] != "ok" {
		t.Fatalf("status=%d body=%v", status, body)
	}
	if body["changed"] != false {
		t.Fatalf("changed=%v, want false on an already-provisioned opencode profile", body["changed"])
	}
	if adapter.calls != 0 {
		t.Fatalf("adapter invoked %d times on an already-provisioned opencode profile, want 0", adapter.calls)
	}
}

// 🔒 BR3 under concurrency. Two callers can POST /api/init-env for the SAME profile at once;
// unserialized, their installer children interleave on one config.toml and the before/after
// fingerprints cross-observe, making both `changed` and "no duplicate registration" racy. Catching
// re-entry is the property that matters; `go test -race` then proves the gate itself is sound.
func TestInitEnvServiceSerializesProvisionsPerProfile(t *testing.T) {
	dir := t.TempDir()
	profile := filepath.Join(dir, "profile")
	adapter := &reentrancyProvisioner{}
	svc := NewInitEnvService(adapter, &stubProofs{profileErr: []error{errNotProvisioned}})

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			svc.Provision(ProvisionRequest{Cwd: dir, Engine: domain.EngineCodex, Profile: &profile})
		}()
	}
	wg.Wait()

	if peak := adapter.maxConcurrent.Load(); peak > 1 {
		t.Fatalf("%d concurrent provisions of one profile; want them serialized", peak)
	}
}

func TestProfileLockKeyCollapsesEquivalentOpencodeProfiles(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(home, ".config")
	if got, want := profileLockKey(domain.EngineOpencode, nil), profileLockKey(domain.EngineOpencode, &root); got != want {
		t.Fatalf("default and explicit opencode profiles use different lock keys: %q != %q", got, want)
	}
	if got := profileLockKey(domain.EngineCodex, &root); got == profileLockKey(domain.EngineOpencode, &root) {
		t.Fatal("codex and opencode profiles must not share a lock key")
	}
}

func TestProfileLockKeyUsesClaudeDefaultProfile(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(home, ".claude")
	if got, want := profileLockKey(domain.EngineClaude, nil), profileLockKey(domain.EngineClaude, &root); got != want {
		t.Fatalf("default and explicit Claude profiles use different lock keys: %q != %q", got, want)
	}
	if got := profileLockKey(domain.EngineClaude, nil); strings.Contains(got, filepath.Join(".config", "claude")) {
		t.Fatalf("Claude default lock key uses legacy profile root: %q", got)
	}
}

func TestProfileGateSerializesEquivalentOpencodeProfiles(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(home, ".config")
	g := newProfileGate()
	unlockDefault := g.lock(domain.EngineOpencode, nil)
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		close(started)
		unlockExplicit := g.lock(domain.EngineOpencode, &root)
		unlockExplicit()
		close(done)
	}()
	<-started
	time.Sleep(time.Millisecond)
	select {
	case <-done:
		t.Fatal("equivalent explicit opencode profile acquired its lock concurrently")
	case <-time.After(10 * time.Millisecond):
	}
	unlockDefault()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("explicit opencode lock did not acquire after default lock released")
	}
}

// reentrancyProvisioner records the HIGHEST number of Provision calls ever in flight at once.
type reentrancyProvisioner struct {
	inFlight      atomic.Int32
	maxConcurrent atomic.Int32
}

func (p *reentrancyProvisioner) Provision(ProvisionRequest) (bool, error) {
	n := p.inFlight.Add(1)
	for {
		peak := p.maxConcurrent.Load()
		if n <= peak || p.maxConcurrent.CompareAndSwap(peak, n) {
			break
		}
	}
	time.Sleep(time.Millisecond) // widen the window a real installer would occupy
	p.inFlight.Add(-1)
	return true, nil
}

// PRD §11 (installer drift): the codex command forms are an external contract kept in ONE mapping.
// This is the lock that stops what `saki init-env` RUNS and what `saki doctor` TELLS THE OPERATOR TO
// RUN from diverging — before F6 they were two independently hand-written copies of the same commands.
func TestCodexInstallFixIsRenderedFromTheProvisionArgv(t *testing.T) {
	lines := strings.Split(CodexInstallFix, "\n")
	if len(lines) != len(CodexProvisionArgv) {
		t.Fatalf("CodexInstallFix has %d lines, CodexProvisionArgv has %d vectors", len(lines), len(CodexProvisionArgv))
	}
	for i, vec := range CodexProvisionArgv {
		if want := strings.Join(vec, " "); lines[i] != want {
			t.Errorf("line %d = %q, want %q", i, lines[i], want)
		}
	}
	if len(CodexProvisionArgv) == 0 || CodexProvisionArgv[0][0] != "codex" {
		t.Fatalf("CodexProvisionArgv must invoke codex, got %v", CodexProvisionArgv)
	}
}

// Same drift-lock as the codex twin: OpencodeInstallFix is RENDERED from OpencodeProvisionArgv, so
// what `saki init-env --engine opencode` executes and what `saki doctor` tells the operator to run
// can never diverge (PRD §11). Also pins the safety-critical shape: the ONE vector must be
// `opencode plugin … --global` (profile-contained via pinned XDG_CONFIG_HOME), never an `npx …
// install --global` that writes outside the selected profile (PRD §9 rule 3).
func TestOpencodeInstallFixIsRenderedFromTheProvisionArgv(t *testing.T) {
	lines := strings.Split(OpencodeInstallFix, "\n")
	if len(lines) != len(OpencodeProvisionArgv) {
		t.Fatalf("OpencodeInstallFix has %d lines, OpencodeProvisionArgv has %d vectors", len(lines), len(OpencodeProvisionArgv))
	}
	for i, vec := range OpencodeProvisionArgv {
		if want := strings.Join(vec, " "); lines[i] != want {
			t.Errorf("line %d = %q, want %q", i, lines[i], want)
		}
	}
	if len(OpencodeProvisionArgv) != 1 {
		t.Fatalf("OpencodeProvisionArgv must be a single vector, got %d", len(OpencodeProvisionArgv))
	}
	vec := OpencodeProvisionArgv[0]
	if vec[0] != "opencode" || vec[len(vec)-1] != "--global" {
		t.Fatalf("opencode provisioning must be `opencode plugin … --global`, got %v", vec)
	}
}

// engineInstallFix must cover every provisionable engine.
func TestEngineInstallFixCoversProvisionableEngines(t *testing.T) {
	if got := engineInstallFix(domain.EngineCodex); got != CodexInstallFix {
		t.Fatalf("codex fix = %q, want CodexInstallFix", got)
	}
	if got := engineInstallFix(domain.EngineOpencode); got != OpencodeInstallFix {
		t.Fatalf("opencode fix = %q, want OpencodeInstallFix", got)
	}
	if got := engineInstallFix(domain.EngineClaude); got != ClaudeInstallFix {
		t.Fatalf("claude fix = %q, want ClaudeInstallFix", got)
	}
}

func TestClaudeInstallFixIsRenderedFromProvisionArgv(t *testing.T) {
	lines := strings.Split(ClaudeInstallFix, "\n")
	if len(lines) != len(ClaudeProvisionArgv) {
		t.Fatalf("ClaudeInstallFix has %d lines, want %d vectors", len(lines), len(ClaudeProvisionArgv))
	}
	for i, vec := range ClaudeProvisionArgv {
		if want := strings.Join(vec, " "); lines[i] != want {
			t.Errorf("line %d = %q, want %q", i, lines[i], want)
		}
	}
}
