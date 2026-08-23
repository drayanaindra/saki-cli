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
	if req.Engine != domain.EngineCodex && req.Engine != domain.EngineOpencode && req.Engine != domain.EngineClaude {
		base["reason"] = ErrInitEnvUnsupported.Error()
		return http200, base
	}
	// Before anything is written: no binary means no setup is possible, and the operator needs the
	// remediation (criterion 1.4). Claude has no binary preflight contract; the installer path performs
	// its own bounded lookup.
	if req.Engine != domain.EngineClaude {
		if err := s.proofs.BinaryCheck(req.Engine); err != nil {
			base["reason"] = err.Error()
			return http200, base
		}
	}

	unlock := s.gate.lock(req.Engine, req.Profile)
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
	base["status"] = string(domain.InitEnvStatusOK)
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
		"status": string(domain.InitEnvStatusFailed),
		"reason": "",
		"fix":    engineInstallFix(req.Engine),
	}
}

// engineInstallFix reuses doctor's remediation verbatim, so `saki init-env` and `saki doctor` can
// never print different fixes for the same unprovisioned engine.
func engineInstallFix(engine domain.RunEngine) string {
	switch engine {
	case domain.EngineCodex:
		return CodexInstallFix
	case domain.EngineOpencode:
		return OpencodeInstallFix
	case domain.EngineClaude:
		return ClaudeInstallFix
	default:
		return ""
	}
}

func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// profileGate hands out one mutex per engine profile path. Default and explicitly named paths that
// resolve to the same filesystem profile share a key; different engines remain isolated even when they
// share a profile root.
type profileGate struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func newProfileGate() *profileGate {
	return &profileGate{locks: make(map[string]*sync.Mutex)}
}

func (g *profileGate) lock(engine domain.RunEngine, profile *string) func() {
	if g == nil {
		return func() {} // zero-value service (a test fake): nothing to serialize
	}
	key := profileLockKey(engine, profile)
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

func profileLockKey(engine domain.RunEngine, profile *string) string {
	root := profilePath(engine, profile)
	return string(engine) + "\x00" + root
}

func profilePath(engine domain.RunEngine, profile *string) string {
	if profile != nil && *profile != "" {
		root := filepath.Clean(*profile)
		switch engine {
		case domain.EngineCodex:
			return filepath.Join(root, "codex")
		case domain.EngineOpencode:
			return filepath.Join(root, "opencode")
		default:
			return root
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "default"
	}
	switch engine {
	case domain.EngineCodex:
		return filepath.Join(home, ".codex")
	case domain.EngineOpencode:
		return filepath.Join(home, ".config", "opencode")
	case domain.EngineClaude:
		return filepath.Join(home, ".claude")
	default:
		return "default"
	}
}

// CodexProvisionArgv is THE codex engine mapping — the single place the marketplace URL and the
// plugin id appear. PRD §11 (installer drift): "command forms are external contracts; keep them in
// one engine mapping". CodexInstallFix is DERIVED from it below, so the command `saki init-env`
// actually runs and the command `saki doctor` tells the operator to run cannot drift apart.
var CodexProvisionArgv = [][]string{
	{"codex", "plugin", "marketplace", "add", "https://github.com/drayanaindra/saki-builder.git"},
	{"codex", "plugin", "add", "saki-builder@saketek"},
}

// OpencodeProvisionArgv is THE opencode engine mapping — the single place the plugin id appears.
// A single vector: `opencode plugin @saketek/saki-builder --global`. The `--global` flag is SAFE here
// ONLY because provisionEnv pins XDG_CONFIG_HOME to the selected profile, which redirects opencode's
// "global" scope into <profile>/opencode — the profile OpencodePluginProof then reads (verified
// empirically: with XDG_CONFIG_HOME pinned, `--global` writes only <profile>/opencode/opencode.jsonc,
// never ~/.config/opencode or a project .opencode). The unsafe form — `npx @saketek/saki-builder
// install --global`, which writes to the npm global cache outside the profile (PRD §9 rule 3) — is
// deliberately NOT here. OpencodeInstallFix is DERIVED below, so the command init-env runs and the
// command doctor tells the operator to run cannot drift apart (PRD §11).
var OpencodeProvisionArgv = [][]string{
	{"opencode", "plugin", "@saketek/saki-builder", "--global"},
}

// ClaudeProvisionArgv is the fixed user-scope installer contract for the Claude profile.
//
// Known limitation: unlike CodexProvisionArgv/OpencodeProvisionArgv, this cannot be truly isolated
// per profile. Hand-verified against claude 2.1.235: `plugin marketplace add`/`plugin install` write
// their install record (installed_plugins.json) only to the real ~/.claude, for every --scope (user,
// project, local) and regardless of CLAUDE_CONFIG_DIR — there is no upstream mechanism to redirect
// it. engineProfileEnv still pins CLAUDE_CONFIG_DIR here (consistent with codex/opencode's contract,
// and it's the only lever this repo has), but a claude --profile run resolves against the operator's
// real ~/.claude regardless. See docs/cli-reference.md § Known limitations.
var ClaudeProvisionArgv = [][]string{
	{"claude", "plugin", "marketplace", "add", "https://gitlab.com/drayanaindra/saki-builder.git", "--scope", "user"},
	{"claude", "plugin", "install", "saki-builder@saketek", "--scope", "user"},
}

func renderProvisionArgv(argv [][]string) string {
	lines := make([]string, 0, len(argv))
	for _, vec := range argv {
		lines = append(lines, strings.Join(vec, " "))
	}
	return strings.Join(lines, "\n")
}
