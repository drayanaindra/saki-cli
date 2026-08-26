package infra

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/drayanaindra/saki-cli/backend/domain"
	"github.com/drayanaindra/saki-cli/backend/usecase"
)

// streamFlags pins the claude headless invocation (stream-json REQUIRES --verbose) — mirrors
// runManager.ts:153.
var streamFlags = []string{"--output-format", "stream-json", "--verbose"}

// buildRunScript is the FIXED shell script the detached child runs (mirrors runManager.ts:413-416),
// now engine-aware (E26): claude (the default) keeps today's invocation; opencode runs
// `opencode run --format json`; codex runs `codex exec --json`. SECURITY: the operator prompt is
// passed via the $SAKI_PROMPT ENV VAR
// (referenced quoted here), NEVER interpolated into this string — so a prompt with quotes/newlines/
// backticks cannot break out of the shell (no command injection). A shell (`sh -c`) is required for
// the `> $SAKI_OUT` redirection and the `echo $? > $SAKI_EXIT` durable-exit contract; the command
// string carries no user input, so the `sh -c` is safe (the same guard apps/server documents).
//
// The elevated --dangerously-skip-permissions is appended ONLY for kind:"init" ON THE CLAUDE ENGINE
// (E26: an opencode spawn never gets it — its permission model is profile-scoped). /init-env must
// write under the target repo's .claude/ dir, which Claude Code protects even under acceptEdits
// headless, so skip-permissions is the only mode that lets it scaffold. It is a STATIC string keyed
// on the run kind (never from user input), scoped to init (builds/other kinds never get it), and the
// dangerous-command PreToolUse hook still runs under it (permission mode ≠ hook bypass). The elevated
// flag is kept off-host- and cross-origin-unreachable by the 127.0.0.1 bind + the init route's
// loopback/origin guard (§10 rule 5), NOT by withholding it from Go.
// hasCmd reports whether the prompt carried a leading slash command that was split out into
// $SAKI_CMD (see domain.SplitSlashCommand). It is engine-scoped: only the opencode branch reads it —
// claude resolves the namespaced command straight from the message, so its script is unaffected.
func buildRunScript(kind domain.RunKind, engine domain.RunEngine, hasCmd bool) string {
	engine = domain.ResolveEngine(engine)
	var perm, cmd string
	switch engine {
	case domain.EngineOpencode:
		// E26 — `--auto` auto-approves permissions NOT explicitly denied by the profile, so a headless
		// `opencode run` can actually execute the journey's tool steps (bash read/write in the target
		// repo). Without it, an `ask`-scoped permission (the operator's default `bash:"*":"ask"`) cannot
		// prompt in headless mode and is AUTO-REJECTED — a pickup/build that immediately no-ops (the
		// 9.3.5 tool-turn kill-gate fails). Same trust model as the claude headless path: the operator
		// explicitly selected opencode to run their OWN journey in their OWN repo. Explicit `deny`
		// rules in the profile still hold.
		//
		// E26 follow-up — `--command` is REQUIRED for a journey run. opencode's `run` does not expand a
		// slash command that arrives in the message: the raw `/saki-builder:build …` text reaches the
		// model, which answers "isn't a recognized command" and the run no-ops on a clean exit 0 (so the
		// classifier parks a build that never started). The bare command name is env-referenced as
		// $SAKI_CMD and the args as $SAKI_PROMPT, so 🔒 BR3 holds — zero prompt/command bytes enter this
		// static string. A prompt with NO slash command (the Plan-track proto prompt opens with prose)
		// keeps the plain message form.
		//
		// The trailing `--` before the message is load-bearing: opencode parses its CLI with yargs, so a
		// message that STARTS with `--` is otherwise read as an unknown flag and the CLI prints its help
		// and exits 1 without running anything. Studio prompts really do carry such args
		// (`/saki-builder:prd-review --review-only`, `/saki-builder:add --list`,
		// `/saki-builder:genesis "…" --restart`), so `--` terminates flag parsing.
		//
		// The args stay ONE quoted argv element on purpose. opencode substitutes the message into the
		// command body by joining its args and wrapping any element containing a space in literal `"`
		// (inner quotes backslash-escaped), so a multi-word arg reaches the skill as
		// `$ARGUMENTS` == `"tasks/prd-x.md — RESUME: …"` — quotes included. That is opencode's own
		// behaviour, not something this layer can undo: passing the args PRE-SPLIT as several argv
		// elements (which would join back unquoted) makes opencode 1.18.16 crash outright — yargs
		// coerces a numeric token to a number and the join hits `G.includes is not a function`. So a
		// single quoted element is the only shape that runs at all; the skills tolerate the wrapping
		// quotes. Verified against opencode 1.18.16 and pinned by the multi-word e2e assertion.
		cmd = `opencode run --format json --auto -- "$SAKI_PROMPT"`
		if hasCmd {
			cmd = `opencode run --format json --auto --command "$SAKI_CMD" -- "$SAKI_PROMPT"`
		}
	case domain.EngineCodex:
		// codex takes the CLAUDE shape, not opencode's. Spiked against codex-cli 0.147.0: `codex exec`
		// DOES resolve a leading slash command that arrives in the message — including the namespaced
		// `/saki-builder:build …` the studio emits — because codex lists $CODEX_HOME/skills/*/SKILL.md in
		// the model's context and the model reads the matching one. Args reach the skill VERBATIM (no
		// quote-wrapping — opencode's `--command` split and its yargs workarounds are not needed here).
		// So the whole prompt goes as the message and no $SAKI_CMD is in play.
		//
		// `--dangerously-bypass-approvals-and-sandbox` is codex's headless analog of opencode's `--auto`
		// and claude's --dangerously-skip-permissions: a journey run must write files, run tests and push,
		// none of which the default sandbox permits, and headless codex cannot prompt for approval — so
		// without it the run is silently confined and no-ops. Same trust model as the other two engines:
		// the operator explicitly selected codex to run their OWN journey in their OWN repo.
		//
		// `--skip-git-repo-check` keeps a non-repo cwd failing on real errors rather than on codex's repo
		// precondition. `< /dev/null` is load-bearing: `codex exec` READS STDIN when it is a pipe (it
		// announces "Reading additional input from stdin..." and appends it as a <stdin> block), and the
		// detached child inherits the server's stdin — without the redirect a run can block forever on it.
		cmd = `codex exec --json --skip-git-repo-check --dangerously-bypass-approvals-and-sandbox "$SAKI_PROMPT" < /dev/null`
	default:
		cmd = `claude -p "$SAKI_PROMPT" ` + strings.Join(streamFlags, " ")
		if kind.IsInit() {
			perm = " --dangerously-skip-permissions"
		}
	}
	return cmd + perm + ` > "$SAKI_OUT" 2>&1; echo $? > "$SAKI_EXIT"`
}

// ShSpawner spawns a detached run and finalizes it via a goroutine that waits for the process and
// reads $SAKI_EXIT. Setpgid puts the child in its own process group so a server restart does not
// kill it (the .exit sentinel is the durable finish signal; full restart-rehydrate is Slice 4).
type ShSpawner struct {
	journal usecase.Journal
	sh      string // "" = the real "sh"; tests substitute a fake shell
}

func NewShSpawner(journal usecase.Journal) *ShSpawner { return &ShSpawner{journal: journal} }

func (s *ShSpawner) Spawn(spec usecase.SpawnSpec) (int, func() int, error) {
	if err := preflight(spec); err != nil {
		return 0, nil, err
	}
	sh := s.sh
	if sh == "" {
		sh = "sh"
	}
	_, _, hasCmd := opencodeCommandSplit(spec)
	cmd := exec.Command(sh, "-c", buildRunScript(spec.Kind, spec.Engine, hasCmd)) //nolint:gosec // fixed script; input via $SAKI_CMD/$SAKI_PROMPT env (no injection)
	if spec.Cwd != nil {
		cmd.Dir = *spec.Cwd
	}
	cmd.Env = buildSpawnEnv(spec, s.journal)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return 0, nil, err
	}
	pid := cmd.Process.Pid
	// wait blocks until the child exits, then reads the durable exit sentinel. The caller arms this
	// in a goroutine AFTER journaling the pid.
	wait := func() int {
		_ = cmd.Wait()
		return readExitCode(s.journal.ExitPath(spec.ID))
	}
	return pid, wait, nil
}

// preflight runs the engine's BEFORE-SPAWN refusals, so a run that cannot possibly work fails loudly
// (→ the build path parks immediately) instead of no-opping on a clean exit and parking a build that
// never started. Claude has no binary check, but its selected profile is proven before spawn.
//
// F2 slice 1: the binary-on-PATH and profile-proof halves are EngineBinaryCheck/EngineProfileProof
// (below) — the SAME functions `saki doctor`'s pre-dispatch verdict calls (via infra.EngineProofChecker),
// so a doctor verdict and a spawn refusal can never disagree (rule 4). The opencode-only prompt-shape
// refusal stays HERE, inline, between the two — it is not a provisioning check, and doctor has no
// prompt to check, so it is deliberately excluded from both shared functions.
func preflight(spec usecase.SpawnSpec) error {
	engine := domain.ResolveEngine(spec.Engine)
	if err := EngineBinaryCheck(engine); err != nil {
		return err
	}
	if engine == domain.EngineOpencode {
		// A prompt the operator MEANT as a command but which does not parse (`/build, then do X`, a
		// >64-char name, a leading `-`) must refuse loudly rather than fall through to the plain message
		// form — on opencode that form is the KNOWN-DEAD path this whole change exists to close (raw
		// slash text → "isn't a recognized command" → no-op on exit 0). Prose prompts are unaffected:
		// they don't open with `/`, so they legitimately take the message form.
		//
		// 🔒 This refusal is opencode-ONLY. codex resolves the same prompt from the message (spike S1),
		// so extending it there would reject prompts that demonstrably work.
		if _, _, ok := domain.SplitSlashCommand(spec.Prompt); !ok && domain.LooksLikeSlashCommand(spec.Prompt) {
			return fmt.Errorf("%w: opencode cannot resolve this prompt as a command", ErrUnresolvableCommand)
		}
	}
	return EngineProfileProof(engine, spec.ConfigDir)
}

// EngineBinaryCheck verifies the engine's CLI is on the spawner's PATH (E26 9.1.5). Checked BEFORE
// spawning so a missing binary is ErrBinaryNotFound, not a silent exit-127 no-op. claude has no binary
// check — its absence already surfaces via the durable exit code, and it resolves commands from the
// message with nothing to prove. Shared by preflight and `saki doctor` (rule 4) — the SAME function,
// not a parallel copy that could drift.
func EngineBinaryCheck(engine domain.RunEngine) error {
	switch engine {
	case domain.EngineOpencode:
		if _, err := exec.LookPath("opencode"); err != nil {
			return fmt.Errorf("%w (opencode)", usecase.ErrBinaryNotFound)
		}
	case domain.EngineCodex:
		if _, err := exec.LookPath("codex"); err != nil {
			return fmt.Errorf("%w (codex)", usecase.ErrBinaryNotFound)
		}
	}
	return nil
}

// EngineProfileProof proves the engine's profile resolves the saki-builder commands (NEVER a run exit
// code — rule 4). Shared by preflight and `saki doctor` — the SAME function, not a parallel copy that
// could drift.
func EngineProfileProof(engine domain.RunEngine, configDir *string) error {
	switch engine {
	case domain.EngineOpencode:
		// E26 9.4.1/9.4.2 — prove the profile carries the saki-builder plugin by reading its config.
		// A plugin-less profile refuses loudly, no silent no-op.
		return OpencodePluginProof(configDir)
	case domain.EngineCodex:
		// Same rule-4 reasoning: a codex home without the saki-builder skills still exits 0 — the model
		// just answers that it cannot find the command — so install state is proven by READING the
		// home, never inferred from the run.
		return CodexSkillsProof(configDir)
	case domain.EngineClaude:
		return ClaudeProfileProof(configDir)
	default:
		return nil
	}
}

// buildSpawnEnv assembles the detached child's env, engine-aware (E26): claude (the default) keeps
// today's exact env — CLAUDE_CONFIG_DIR dropped when no profile is pinned (so an inherited value can't
// silently pin the wrong profile), CLAUDE_CONFIG_DIR set when one is, and CLAUDE_CODE_MAX_RETRIES
// defaulted. opencode gets the symmetric treatment: inherited OPENCODE_CONFIG/XDG_CONFIG_HOME are
// dropped (or XDG_CONFIG_HOME pinned to an explicit profile), and NO claude-only env is passed. codex
// is the same shape again on CODEX_HOME (pinned to <configDir>/codex). The per-engine detail lives in
// scrubProfileEnv + engineProfileEnv so adding a runtime is one case each, not a new branch here.
func buildSpawnEnv(spec usecase.SpawnSpec, journal usecase.Journal) []string {
	base := os.Environ()
	engine := domain.ResolveEngine(spec.Engine)
	base = scrubProfileEnv(base, engine, spec.ConfigDir != nil)
	// E26 follow-up — on opencode a journey prompt is invoked via `--command <bare-name>` with the ARGS
	// as the message (buildRunScript). So the child gets the split: $SAKI_CMD = the namespace-stripped
	// name, $SAKI_PROMPT = the args alone. Passing the whole prompt as the message would re-inject the
	// unresolvable `/saki-builder:build …` text into the command body. claude is untouched (🔒 BR5): it
	// resolves the namespaced command from the message, so it always receives the FULL prompt and no
	// $SAKI_CMD. `name`/`ok` are recomputed from the same helper the script branch keys on, so the two
	// can never disagree about whether a command is in play.
	prompt := spec.Prompt
	name, args, ok := opencodeCommandSplit(spec)
	if ok {
		prompt = args
		if name == "proto" {
			prompt = "/proto " + args
		}
	} else {
		// Symmetric with the CLAUDE_CONFIG_DIR / OPENCODE_CONFIG drops above: never let an inherited
		// SAKI_CMD leak into a spawn that is NOT using the --command form. Harmless today (the script
		// only dereferences it when hasCmd), but it keeps "what the child sees is what this function
		// decided" true rather than accidentally true.
		base = filterEnv(base, "SAKI_CMD")
	}
	env := append(base,
		"SAKI_PROMPT="+prompt,
		"SAKI_OUT="+journal.OutPath(spec.ID),
		"SAKI_EXIT="+journal.ExitPath(spec.ID),
	)
	if ok {
		env = append(env, "SAKI_CMD="+name)
	}
	if spec.ConfigDir != nil {
		env = append(env, engineProfileEnv(engine, *spec.ConfigDir))
	}
	// CLAUDE_CODE_MAX_RETRIES is a claude-only knob — keyed on the claude engine itself, so adding a
	// third runtime never accidentally hands it a claude env (an `!= opencode` test would have).
	if engine == domain.EngineClaude && os.Getenv("CLAUDE_CODE_MAX_RETRIES") == "" {
		env = append(env, "CLAUDE_CODE_MAX_RETRIES=2")
	}
	return env
}

// engineMarkerPrefix is the env-var namespace each runtime stamps on its own children. The prefixes
// are disjoint and nothing else claims them, so a prefix test identifies "this var belongs to engine
// X" exactly. `CLAUDE` deliberately covers the un-underscored `CLAUDECODE=1` as well as `CLAUDE_*`.
var engineMarkerPrefix = map[domain.RunEngine]string{
	domain.EngineClaude:   "CLAUDE",
	domain.EngineOpencode: "OPENCODE",
	domain.EngineCodex:    "CODEX",
}

// scrubProfileEnv drops inherited env that must not leak into this spawn.
//
// 1. FOREIGN ENGINE MARKERS. A non-claude spawn sheds every OTHER engine's marker namespace. Two
// distinct failures ride on this, and both are live whenever the studio itself was started from
// inside an agent session (the normal way this repo is developed):
//
//   - Misdetection. /init-env picks the environment it scaffolds by sniffing these markers
//     (OPENCODE → opencode, CLAUDECODE → claude). An inherited CLAUDECODE=1 makes a CODEX run
//     scaffold the claude set — a repo that looks initialized and loads nothing, since codex reads
//     AGENTS.md, not CLAUDE.md. Whole-namespace, not a hand-picked list: the marker that misleads is
//     whichever one the other engine happens to set, and that list is not ours to track.
//   - Credential leak. A live claude session exports CLAUDE_CODE_MESSAGING_TOKEN and
//     CLAUDE_CODE_MESSAGING_SOCKET. Handing those to a THIRD-PARTY runtime (codex, opencode) gives a
//     foreign agent a working handle on the operator's claude session. Nothing needs them but claude.
//
// claude's own env is left untouched (🔒 BR5 / rule 1 — its behaviour stays byte-identical). The
// provisioning path follows the same rule and only replaces CLAUDE_CONFIG_DIR when explicitly pinned.
//
// 2. OWN PROFILE VARS — always shed the inherited selector before optionally pinning the selected
// profile below. Removing it for pinned profiles prevents duplicate entries from preserving a foreign
// OPENCODE_CONFIG (or another engine's selector) in the child environment.
func scrubProfileEnv(base []string, engine domain.RunEngine, _ bool) []string {
	if engine != domain.EngineClaude {
		for other, prefix := range engineMarkerPrefix {
			if other != engine {
				base = filterEnvPrefix(base, prefix)
			}
		}
	}
	switch engine {
	case domain.EngineOpencode:
		base = filterEnv(base, "OPENCODE_CONFIG")
		base = filterEnv(base, "XDG_CONFIG_HOME")
	case domain.EngineCodex:
		base = filterEnv(base, "CODEX_HOME")
	default:
		base = filterEnv(base, "CLAUDE_CONFIG_DIR")
	}
	return base
}

// engineProfileEnv renders the `KEY=VALUE` an engine reads its PINNED profile from. Each engine names
// the same studio `configDir` differently, and codex additionally nests: it reads $CODEX_HOME as the
// home itself, so the profile dir holds that home at <configDir>/codex — mirroring opencode's
// <configDir>/opencode. One profile dir, one subdir per engine.
func engineProfileEnv(engine domain.RunEngine, dir string) string {
	switch engine {
	case domain.EngineOpencode:
		return "XDG_CONFIG_HOME=" + dir
	case domain.EngineCodex:
		// Resolved by the SAME helper CodexSkillsProof reads, so the proof and the spawn can never
		// disagree about which home actually runs.
		return "CODEX_HOME=" + codexHomePath(&dir)
	default:
		return "CLAUDE_CONFIG_DIR=" + dir
	}
}

// opencodeCommandSplit is the SINGLE source of truth for "does this spawn use opencode's `--command`
// form, and with what name/args". Both the script builder and the env builder key on it, so they can
// never disagree (a script expecting $SAKI_CMD with no env entry would spawn `--command ""`).
// Returns ok=false for any claude spawn and for any prompt that is not a leading slash command.
func opencodeCommandSplit(spec usecase.SpawnSpec) (name, args string, ok bool) {
	if domain.ResolveEngine(spec.Engine) != domain.EngineOpencode {
		return "", "", false
	}
	return domain.SplitSlashCommand(spec.Prompt)
}

// filterEnvPrefix returns env with every `PREFIX...=...` entry removed — the whole namespace an engine
// stamps on its children, not one hand-picked key. Matches on the NAME only (the part before the first
// `=`), so a value that merely starts with the prefix is never mistaken for a marker.
func filterEnvPrefix(env []string, prefix string) []string {
	out := make([]string, 0, len(env))
	for _, e := range env {
		name, _, found := strings.Cut(e, "=")
		if found && strings.HasPrefix(name, prefix) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// filterEnv returns env with any `KEY=...` entry for the given key removed (a fresh slice — the
// os.Environ() backing array is left untouched).
func filterEnv(env []string, key string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env))
	for _, e := range env {
		if !strings.HasPrefix(e, prefix) {
			out = append(out, e)
		}
	}
	return out
}

// readExitCode reads the $SAKI_EXIT sentinel; -1 when it is missing/unparseable.
func readExitCode(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return -1
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return -1
	}
	return n
}
