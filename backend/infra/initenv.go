package infra

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/drayanaindra/saki-cli/backend/domain"
	"github.com/drayanaindra/saki-cli/backend/usecase"
)

// provisionTimeout bounds each installer child. `codex plugin marketplace add` clones over the
// network, so an unreachable registry would otherwise hang the request goroutine for the daemon's
// lifetime — there is no `saki init-env --stop`. Same BR6 reasoning as execx.go's bounded git exec.
// A var, not a const, solely so the timeout test can shorten it — production never reassigns it.
var provisionTimeout = 120 * time.Second

// maxProvisionMessage caps what a child's output can contribute to an HTTP response. PRD §12: the
// route must not expose profile contents beyond doctor's reason/fix contract, and a failing installer
// happily echoes the offending config.toml line or a resolved registry URL. Bounded AND first-line-only.
const maxProvisionMessage = 512

// EngineProvisioner invokes only FIXED argv vectors, sourced from usecase's single engine mapping.
// User input selects an engine and a profile directory; it never becomes shell text, and never an
// executable name or argument.
type EngineProvisioner struct{}

func (EngineProvisioner) Provision(req usecase.ProvisionRequest) (bool, error) {
	if err := EngineBinaryCheck(req.Engine); err != nil {
		return false, err // nothing is created on this path (criterion 1.4)
	}
	argv, ok := provisionArgv(req.Engine)
	if !ok {
		return false, usecase.ErrInitEnvUnsupported
	}
	// The engine home must EXIST before its installer runs. `codex plugin marketplace add` refuses to
	// create PATH aliases when CODEX_HOME points at a missing directory and fails the command — so a
	// fresh `--profile` (the whole point of this feature) could never be provisioned. Found only by
	// the real-binary e2e; every fake exits 0 regardless of whether the directory is there.
	// This is still inside the selected engine's own namespace (BR3) and runs AFTER the binary check,
	// so criterion 1.4's "no profile files are created" is unaffected.
	if err := ensureEngineHome(req.Engine, req.Profile); err != nil {
		return false, err
	}
	before := profileFingerprint(req.Engine, req.Profile)
	env := provisionEnv(req.Engine, req.Profile)
	// Every command runs even after an earlier one fails, and the FIRST error is kept: a repeat run's
	// `marketplace add` exits non-zero with "already added" while `plugin add` still needs to run.
	// The caller's shared proof — not this error — decides the verdict (🔒 BR2).
	var firstErr error
	for _, vec := range argv {
		if err := runProvision(req.Cwd, env, vec); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	after := profileFingerprint(req.Engine, req.Profile)
	return before != after, firstErr
}

// ensureEngineHome creates the child-visible home the installer writes into and the proof then reads
// — resolved by the SAME helper both of those use, so the directory created is never a third path.
// 0700 because a profile can hold the engine's credentials.
func ensureEngineHome(engine domain.RunEngine, profile *string) error {
	var home string
	var label string
	switch engine {
	case domain.EngineCodex:
		home, label = codexHomePath(profile), "codex"
	case domain.EngineClaude:
		home, label = claudeHomePath(profile), "claude"
	case domain.EngineOMP:
		home, label = ompHomePath(profile), "omp"
	default:
		return nil
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return fmt.Errorf("cannot create the %s home %s: %w", label, home, err)
	}
	return nil
}

// provisionArgv resolves the engine's fixed command list. The usecase selects supported engines first;
// this remains a defence-in-depth guard for direct adapter callers.
func provisionArgv(engine domain.RunEngine) ([][]string, bool) {
	switch engine {
	case domain.EngineCodex:
		return usecase.CodexProvisionArgv, true
	case domain.EngineOpencode:
		return usecase.OpencodeProvisionArgv, true
	case domain.EngineOMP:
		return usecase.OMPProvisionArgv, true
	case domain.EngineClaude:
		return usecase.ClaudeProvisionArgv, true
	default:
		return nil, false
	}
}

// runProvision executes one fixed argv vector, bounded by provisionTimeout with Stdin=nil so a child
// can never block on a prompt (BR6, mirroring execx.go's run). Output is captured into a bounded
// buffer — an installer that prints megabytes cannot balloon the response or the daemon's memory.
func runProvision(dir string, env []string, argv []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), provisionTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdin = nil
	cmd.WaitDelay = time.Second // close pipes promptly after SIGKILL, so no orphan blocks Run()
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out

	err := cmd.Run()
	if err == nil {
		return nil
	}
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("%s timed out after %s", strings.Join(argv, " "), provisionTimeout)
	}
	return fmt.Errorf("%s failed: %s", strings.Join(argv, " "), summarizeChildOutput(out.String(), err))
}

// summarizeChildOutput reduces a child's output to ONE bounded line for the HTTP response. Full
// output is deliberately not forwarded: it is third-party text that can carry profile contents or a
// credentialed registry URL (PRD §12), unlike doctor's fixed-message contract.
func summarizeChildOutput(output string, err error) string {
	for _, line := range strings.Split(output, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			if len(trimmed) > maxProvisionMessage {
				return trimmed[:maxProvisionMessage] + "… (truncated)"
			}
			return trimmed
		}
	}
	return err.Error()
}

// provisionEnv renders the child's environment through the SAME two helpers a spawn uses
// (scrubProfileEnv + engineProfileEnv), never a second per-engine switch — CLAUDE.md project rule 6.
// That is what guarantees the installer writes into exactly the home CodexSkillsProof then reads.
func provisionEnv(engine domain.RunEngine, profile *string) []string {
	pinned := profile != nil && *profile != ""
	env := scrubProfileEnv(os.Environ(), engine, pinned)
	if !pinned {
		return env // unpinned: scrubProfileEnv already dropped any inherited CODEX_HOME
	}
	return append(env, engineProfileEnv(engine, *profile))
}

// profileFingerprint is intentionally narrow: it covers exactly the files each proof reads, so it
// detects creation/removal and content changes without exposing profile contents in a response.
func profileFingerprint(engine domain.RunEngine, profile *string) string {
	var paths []string
	switch engine {
	case domain.EngineCodex:
		home := codexHomePath(profile)
		paths = []string{filepath.Join(home, "config.toml"), filepath.Join(home, "skills", sentinelProofSkill, "SKILL.md")}
	case domain.EngineOpencode:
		paths = []string{opencodeConfigPath(profile)}
	case domain.EngineOMP:
		paths = ompProfilePaths(profile)
	case domain.EngineClaude:
		installed, settings := claudeProfilePaths(profile)
		paths = []string{installed, settings}
		if plugin, err := resolveClaudeProfile(profile); err == nil && plugin.InstallPath != "" {
			paths = append(paths, filepath.Join(plugin.InstallPath, "skills", sentinelProofSkill, "SKILL.md"))
		}
	default:
		return ""
	}
	h := sha256.New()
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			_, _ = h.Write([]byte("missing:" + path + "\n"))
			continue
		}
		_, _ = h.Write([]byte(path + "\n"))
		_, _ = h.Write(data)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}
