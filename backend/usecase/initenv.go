package usecase

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

var ErrInitEnvUnsupported = errors.New("engine provisioning is not verified for this engine")

// unsupportedReason explains, per engine, WHY provisioning is refused — so an agent can tell
// "retry later" from "this will never work until another feature ships" and stop instead of looping.
var unsupportedReason = map[domain.RunEngine]string{
	domain.EngineClaude:   " (claude requires F4's installed + enabled plugin proof)",
	domain.EngineOpencode: " (opencode lands in F6 slice 2)",
}

// ProvisionRequest is the validated command. Profile is the engine-profile ROOT (the same input
// doctor and a run's --profile take), nil meaning the engine's own default.
type ProvisionRequest struct {
	Cwd     string
	Engine  domain.RunEngine
	Profile *string
}

type EngineProvisioner interface {
	Provision(ProvisionRequest) (changed bool, err error)
}

type InitEnvService struct {
	provisioner EngineProvisioner
	proofs      EngineProofs
	// gate serializes provisioning per resolved profile. Two agents (or an agent and the operator)
	// can hit this route concurrently for one profile; without it their `codex plugin add` children
	// interleave on a single config.toml and the before/after fingerprints cross-observe, making both
	// `changed` and criterion 1.3's "no duplicate registration" race. Same class as
	// BuildEngineService.idemMu — a mutating path needs a dedupe gate, never "hardening for later".
	gate *profileGate
}

func NewInitEnvService(provisioner EngineProvisioner, proofs EngineProofs) InitEnvService {
	return InitEnvService{provisioner: provisioner, proofs: proofs, gate: newProfileGate()}
}

// Provision mutates only the selected engine profile, then proves the exact profile a spawn would use.
//
// 🔒 BR2 — THE SHARED PROOF DECIDES, THE INSTALLER NEVER DOES. This is the rule the command exists to
// enforce, and it cuts both ways:
//
//   - An installer that exits 0 proves nothing. An unprovisioned engine still exits 0 (the model just
//     answers that it cannot find the command) — the exact false green `saki doctor` was built to
//     catch — so `status:"ok"` is set only after ProfileProof passes.
//   - An installer that exits NON-zero disproves nothing. A repeat run's `codex plugin marketplace
//     add` fails with "already added"; if the proof passes, the profile IS provisioned and the
//     child's complaint is not the verdict (criterion 1.3).
//
// Idempotency (BR3) is structural, not best-effort: an already-proven profile never reaches the
// adapter, so a repeat run cannot duplicate a plugin registration.
//
// A failed setup returns a structured 200 body so the CLI keeps its normal contract (`status:"failed"`
// → EXIT.ERROR — deliberately the same code `saki doctor` returns for a failed engine report, not
// REMOTE_FAILED, which is reserved for the `{ok:false}` refusal envelope). Malformed requests are
// rejected with 422 before any adapter or child process is reached.
func (s InitEnvService) Provision(req ProvisionRequest) (int, map[string]any) {
	req, status, invalid := normalizeProvisionRequest(req)
	if invalid != nil {
		return status, invalid
	}
	base := newInitEnvResult(req)
	// Slice 1 provisions codex only. The refusal is at the SERVICE, not merely untested at the
	// adapter, so opencode's `npx … --global` (which writes outside the selected profile) is
	// unreachable until slice 2 gives it real criteria. Slice 2/3 widen this one predicate.
	if req.Engine != domain.EngineCodex {
		base["reason"] = ErrInitEnvUnsupported.Error() + unsupportedReason[req.Engine]
		return http200, base
	}
	// Before anything is written: no binary means no setup is possible, and the operator needs the
	// remediation (criterion 1.4). First, so nothing is created on this path.
	if err := s.proofs.BinaryCheck(req.Engine); err != nil {
		base["reason"] = err.Error()
		return http200, base
	}

	unlock := s.gate.lock(req.Profile)
	defer unlock()

	if s.proofs.ProfileProof(req.Engine, req.Profile) == nil {
		return succeed(base) // already provisioned — a no-op, and changed stays false
	}
	changed, provisionErr := s.provisioner.Provision(req)
	base["changed"] = changed
	if err := s.proofs.ProfileProof(req.Engine, req.Profile); err != nil {
		base["reason"] = firstError(provisionErr, err).Error()
		return http200, base
	}
	return succeed(base)
}

const http200 = 200

// succeed flips the result to ok and clears the remediation — `fix` is seeded up front so that EVERY
// failure path carries it, which would otherwise leave a stale "how to install" line on a success.
func succeed(base map[string]any) (int, map[string]any) {
	base["status"] = string(domain.StatusOK)
	base["fix"] = ""
	return http200, base
}

// normalizeProvisionRequest rejects malformed input BEFORE any adapter or child process is reached
// (criterion 1.5) and returns the request with its profile cleaned ONCE, so the path reported to the
// operator and the path handed to the provisioner and the proof are the same string.
//
// Containment note: an ABSOLUTE profile is deliberately not confined to the repository — PRD §6 says
// a legitimate default lives outside it (`~/.codex`). The repo-relative containment rule is enforced
// CLI-side (src/commands/init-env.ts). Resolution is lexical (filepath.Clean), not symlink-resolving;
// an operator who names a symlinked profile gets the profile they named.
func normalizeProvisionRequest(req ProvisionRequest) (ProvisionRequest, int, map[string]any) {
	if req.Cwd == "" || !filepath.IsAbs(req.Cwd) {
		return req, 422, map[string]any{"error": "cwd must be an absolute path"}
	}
	if info, err := os.Stat(req.Cwd); err != nil || !info.IsDir() {
		return req, 422, map[string]any{"error": "cwd must be an existing directory"}
	}
	if !isKnownEngine(req.Engine) {
		return req, 422, map[string]any{"error": "engine must be one of claude, codex, opencode"}
	}
	if req.Profile != nil && *req.Profile != "" {
		if !filepath.IsAbs(*req.Profile) {
			return req, 422, map[string]any{"error": "profile must be an absolute path"}
		}
		cleaned := filepath.Clean(*req.Profile)
		req.Profile = &cleaned
	} else {
		req.Profile = nil // normalize a pointer-to-"" to "unpinned", so every layer agrees
	}
	return req, http200, nil
}

func isKnownEngine(engine domain.RunEngine) bool {
	switch engine {
	case domain.EngineCodex, domain.EngineOpencode, domain.EngineClaude:
		return true
	default:
		return false
	}
}

// newInitEnvResult seeds the response every path returns. `fix` is attached up front so a failure on
// ANY path (missing binary, installer error, failing proof) carries remediation — an agent branching
// on this body should never receive a bare failure it cannot act on. succeed() clears it.
//
// `profile` uses doctor's own profileLabel, so `saki init-env` and `saki doctor` name a profile
// identically ("default" for unpinned) and an operator can read one against the other.
func newInitEnvResult(req ProvisionRequest) map[string]any {
	return map[string]any{
		"engine":  string(req.Engine),
		"profile": profileLabel(req.Profile),
		"changed": false,
		// Plain strings, deliberately: this map is marshalled straight to JSON, and a named
		// domain.EngineStatus in a map[string]any breaks every consumer's `.(string)` assertion
		// while looking identical on the wire.
		"status": string(domain.StatusFailed),
		"reason": "",
		"fix":    engineInstallFix(req.Engine),
	}
}

// engineInstallFix reuses doctor's remediation verbatim, so `saki init-env` and `saki doctor` can
// never print different fixes for the same unprovisioned engine.
func engineInstallFix(engine domain.RunEngine) string {
	if engine == domain.EngineCodex {
		return CodexInstallFix
	}
	return ""
}

func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// profileGate hands out one mutex per resolved profile path. Keyed by string (not by pointer) so the
// default profile and an explicitly-named one collapse onto the same key when they mean the same
// thing. Entries are never evicted: the key space is the set of profile paths an operator names,
// which is tiny and bounded by hand.
type profileGate struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func newProfileGate() *profileGate {
	return &profileGate{locks: make(map[string]*sync.Mutex)}
}

func (g *profileGate) lock(profile *string) func() {
	if g == nil {
		return func() {} // zero-value service (a test fake): nothing to serialize
	}
	key := profileLabel(profile)
	g.mu.Lock()
	m, ok := g.locks[key]
	if !ok {
		m = &sync.Mutex{}
		g.locks[key] = m
	}
	g.mu.Unlock()
	m.Lock()
	return m.Unlock
}

// CodexProvisionArgv is THE codex engine mapping — the single place the marketplace URL and the
// plugin id appear. PRD §11 (installer drift): "command forms are external contracts; keep them in
// one engine mapping". CodexInstallFix is DERIVED from it below, so the command `saki init-env`
// actually runs and the command `saki doctor` tells the operator to run cannot drift apart.
var CodexProvisionArgv = [][]string{
	{"codex", "plugin", "marketplace", "add", "https://github.com/drayanaindra/saki-builder.git"},
	{"codex", "plugin", "add", "saki-builder@saketek"},
}

func renderProvisionArgv(argv [][]string) string {
	lines := make([]string, 0, len(argv))
	for _, vec := range argv {
		lines = append(lines, strings.Join(vec, " "))
	}
	return strings.Join(lines, "\n")
}
