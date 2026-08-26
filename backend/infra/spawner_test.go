package infra

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drayanaindra/saki-cli/backend/domain"
	"github.com/drayanaindra/saki-cli/backend/usecase"
)

// writeFakeSh writes a shell that IGNORES its `-c` script and just writes the run triplet's out +
// exit (with the given exit code), so the test never invokes the real `claude`.
func writeFakeSh(t *testing.T, exitCode string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fakesh")
	script := "#!/bin/sh\nprintf '{\"kind\":\"raw\"}\\n' > \"$SAKI_OUT\"\necho " + exitCode + " > \"$SAKI_EXIT\"\nexit 0\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func claudeProvenProfile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	installPath := t.TempDir()
	writeSkillFile(t, installPath, "build")
	writeClaudeProfile(t, dir,
		`{"plugins":{"saketek@saki-builder":[{"installPath":"`+installPath+`","version":"0.5.0"}]}}`,
		`{"enabledPlugins":{"saketek@saki-builder":true}}`,
	)
	return dir
}

func claudeSpawnSpec(t *testing.T, spec usecase.SpawnSpec) usecase.SpawnSpec {
	t.Helper()
	if spec.ConfigDir == nil {
		dir := claudeProvenProfile(t)
		spec.ConfigDir = &dir
	}
	return spec
}

func TestShSpawner_SpawnsAndFinalizes(t *testing.T) {
	j := NewFileJournal(t.TempDir())
	sp := NewShSpawner(j)
	sp.sh = writeFakeSh(t, "0")

	pid, wait, err := sp.Spawn(claudeSpawnSpec(t, usecase.SpawnSpec{ID: "r1", Prompt: "hi", Kind: "generate"}))
	if err != nil {
		t.Fatal(err)
	}
	if pid <= 0 {
		t.Fatalf("pid %d", pid)
	}
	if code := wait(); code != 0 { // blocks until the fake sh exits + reads the .exit sentinel
		t.Fatalf("exit %d, want 0", code)
	}
	if _, err := os.Stat(j.OutPath("r1")); err != nil {
		t.Fatalf("no .out written: %v", err)
	}
	if _, err := os.Stat(j.ExitPath("r1")); err != nil {
		t.Fatalf("no .exit written: %v", err)
	}
}

func TestShSpawner_NonZeroExit(t *testing.T) {
	// mirrors "claude off PATH → exit 127" → wait() returns non-zero (AC 2.4).
	j := NewFileJournal(t.TempDir())
	sp := NewShSpawner(j)
	sp.sh = writeFakeSh(t, "127")
	_, wait, err := sp.Spawn(claudeSpawnSpec(t, usecase.SpawnSpec{ID: "r2", Prompt: "x"}))
	if err != nil {
		t.Fatal(err)
	}
	if code := wait(); code != 127 {
		t.Fatalf("want 127, got %d", code)
	}
}

func TestBuildRunScript_PromptViaEnvNoInjection(t *testing.T) {
	for _, engine := range []domain.RunEngine{"", domain.EngineOpencode} {
		for _, hasCmd := range []bool{false, true} {
			s := buildRunScript("", engine, hasCmd)
			if !strings.Contains(s, `"$SAKI_PROMPT"`) || !strings.Contains(s, "$SAKI_OUT") || !strings.Contains(s, "$SAKI_EXIT") {
				t.Fatalf("script must reference env vars, not interpolate a prompt (engine=%s hasCmd=%v): %s", engine, hasCmd, s)
			}
		}
	}
}

// E26 9.1.1 — an opencode spawn executes `opencode run --format json --auto` (never `claude`), keeps the
// journal contract, and never carries the claude-only elevated flag. `--auto` makes headless tool turns
// (bash read/write the journey needs) auto-approve instead of auto-rejecting on an `ask` profile.
func TestBuildRunScript_OpencodeCommand(t *testing.T) {
	s := buildRunScript("build", domain.EngineOpencode, false)
	if !strings.Contains(s, `opencode run --format json --auto -- "$SAKI_PROMPT"`) {
		t.Fatalf("opencode script must run `opencode run --format json --auto`: %s", s)
	}
	if strings.Contains(s, "claude") {
		t.Fatalf("opencode script must not spawn claude: %s", s)
	}
	if strings.Contains(s, "--dangerously-skip-permissions") {
		t.Fatalf("opencode script must never carry the claude-only elevated flag: %s", s)
	}
	if !strings.Contains(s, `> "$SAKI_OUT" 2>&1`) || !strings.Contains(s, `echo $? > "$SAKI_EXIT"`) {
		t.Fatalf("opencode script must keep the journal .out/.exit contract: %s", s)
	}
}

// E26 follow-up — a studio journey prompt is a slash command, and opencode's `run` does NOT expand one
// that arrives in the message (the raw text reaches the model, which answers "isn't a recognized
// command" and the run no-ops on exit 0). With a command split out, the spawn must use opencode's real
// invocation form: `--command "$SAKI_CMD"` with the args as the message.
func TestBuildRunScript_OpencodeUsesCommandFormWhenPromptIsASlashCommand(t *testing.T) {
	s := buildRunScript("build", domain.EngineOpencode, true)
	if !strings.Contains(s, `opencode run --format json --auto --command "$SAKI_CMD" -- "$SAKI_PROMPT"`) {
		t.Fatalf("opencode script must invoke via --command when the prompt carries one: %s", s)
	}
	// 🔒 BR3 — neither the command name nor the prompt may be interpolated into the static script.
	if strings.Contains(s, "saki-builder") || strings.Contains(s, "/build") {
		t.Fatalf("script must carry zero prompt/command bytes (env-referenced only): %s", s)
	}
	if !strings.Contains(s, `> "$SAKI_OUT" 2>&1`) || !strings.Contains(s, `echo $? > "$SAKI_EXIT"`) {
		t.Fatalf("opencode --command script must keep the journal .out/.exit contract: %s", s)
	}
}

// The fallback stays intact: a prose prompt (the Plan-track proto prompt opens with prose, not a slash
// command) must still spawn the plain message form — never `--command ""`.
func TestBuildRunScript_OpencodeMessageFormWhenNoCommand(t *testing.T) {
	s := buildRunScript("proto", domain.EngineOpencode, false)
	if strings.Contains(s, "--command") {
		t.Fatalf("a prompt with no slash command must not spawn --command: %s", s)
	}
}

// E26 9.1.2 — a SpawnSpec with no engine produces the byte-identical claude command from today.
// 🔒 BR5 (no claude regression): the claude branch ignores the command split entirely, so BOTH hasCmd
// values must produce that exact string — claude resolves the namespaced command from the message.
func TestBuildRunScript_ClaudeDefaultByteIdentical(t *testing.T) {
	const want = `claude -p "$SAKI_PROMPT" --output-format stream-json --verbose > "$SAKI_OUT" 2>&1; echo $? > "$SAKI_EXIT"`
	for _, hasCmd := range []bool{false, true} {
		if got := buildRunScript("", "", hasCmd); got != want {
			t.Fatalf("empty-engine default must be byte-identical to today's claude command (hasCmd=%v):\n got: %s\nwant: %s", hasCmd, got, want)
		}
		if got := buildRunScript("", domain.EngineClaude, hasCmd); got != want {
			t.Fatalf("explicit claude must match the empty-engine default (hasCmd=%v): %s", hasCmd, got)
		}
	}
}

// F7 · P6 slice 4: the elevated flag is applied ONLY for kind:init, and NEVER for any other kind — so
// a build/generate/empty spawn can never be coaxed into an elevated run. E26: never on opencode either.
func TestBuildRunScript_ElevatedFlagScopedToInit(t *testing.T) {
	const flag = "--dangerously-skip-permissions"
	if got := buildRunScript("init", "", false); !strings.Contains(got, flag) {
		t.Fatalf("init script must carry %s: %s", flag, got)
	}
	for _, k := range []domain.RunKind{"", "build", "generate", "review", "proto"} {
		if got := buildRunScript(k, "", false); strings.Contains(got, flag) {
			t.Fatalf("kind %q must NOT carry the elevated flag: %s", k, got)
		}
	}
	for _, hasCmd := range []bool{false, true} {
		if got := buildRunScript("init", domain.EngineOpencode, hasCmd); strings.Contains(got, flag) {
			t.Fatalf("opencode init must NOT carry the claude-only elevated flag (hasCmd=%v): %s", hasCmd, got)
		}
	}
}

// writeFakeOpencode writes an executable `opencode` sh that dumps its env into $SAKI_OUT and records a
// clean exit — a stand-in for the real CLI so a test can exercise the spawner's opencode path.
func writeFakeOpencode(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\nenv > \"$SAKI_OUT\"\necho 0 > \"$SAKI_EXIT\"\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "opencode"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// writeArgvRecordingOpencode writes an executable `opencode` sh that records its OWN argv (one arg per
// line) into $SAKI_OUT — so a test can assert the exact command line the real spawner produced, rather
// than only the script string.
// The fake is PREPENDED to the existing PATH (not substituted for it) because these tests run the real
// `sh` — replacing PATH outright makes the spawn fail with `"sh": executable file not found`.
func writeArgvRecordingOpencode(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\" >> \"$SAKI_OUT\"; done\necho 0 > \"$SAKI_EXIT\"\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "opencode"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// pluginProvenProfile returns a pinned profile dir whose config declares the saki-builder plugin, so
// OpencodePluginProof passes and the spawn proceeds (reuses the opencode_test.go writer).
func pluginProvenProfile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeOpencodeProfile(t, dir, `{"$schema":"https://opencode.ai/config.json","plugin":["@saketek/saki-builder"]}`)
	return dir
}

// E26 follow-up — the load-bearing regression lock, asserted on the REAL spawner's argv.
//
// Before this fix the studio spawned `opencode run --format json --auto "/saki-builder:build …"`.
// opencode does not expand a slash command that arrives in the message, so the model received the raw
// text, answered "isn't a recognized command", and the run no-opped on a clean exit 0 — every journey
// run on opencode was dead. The spawn must therefore reach the CLI as `--command build` + the args.
//
// Covers the fresh spawn AND the auto-resume successor: buildengine.go:205 rebuilds the SpawnSpec from
// run.Prompt/run.Engine verbatim, so a successor is byte-identical in shape to this spec (🔒 BR2 — a
// resumed opencode build must not silently fall back to the unresolvable message form).
func TestSpawn_OpencodeProtoTargetIsParsedBySkill(t *testing.T) {
	cfgDir := pluginProvenProfile(t)
	j := NewFileJournal(t.TempDir())
	env := buildSpawnEnv(usecase.SpawnSpec{
		ID:        "proto-env",
		Prompt:    "/saki-builder:proto F3",
		ConfigDir: &cfgDir,
		Kind:      "generate",
		Engine:    domain.EngineOpencode,
	}, j)
	for _, value := range env {
		if value == "SAKI_PROMPT=/proto F3" {
			return
		}
	}
	t.Fatalf("SAKI_PROMPT not normalized for proto: %v", env)
}

func TestSpawn_OpencodeProtoTargetReachesCommand(t *testing.T) {
	writeArgvRecordingOpencode(t)
	cfgDir := pluginProvenProfile(t)
	j := NewFileJournal(t.TempDir())
	sp := NewShSpawner(j)

	_, wait, err := sp.Spawn(usecase.SpawnSpec{
		ID:        "proto-r1",
		Prompt:    "/saki-builder:proto F3",
		ConfigDir: &cfgDir,
		Kind:      "generate",
		Engine:    domain.EngineOpencode,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if code := wait(); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}

	argv, err := os.ReadFile(j.OutPath("proto-r1"))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimRight(string(argv), "\n"), "\n")
	want := []string{"run", "--format", "json", "--auto", "--command", "proto", "--", "/proto F3"}
	if len(got) != len(want) {
		t.Fatalf("argv = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("argv = %q, want %q", got, want)
		}
	}
}

func TestSpawn_OpencodeInvokesTheCommandNotTheRawSlashText(t *testing.T) {
	writeArgvRecordingOpencode(t)
	cfgDir := pluginProvenProfile(t)
	j := NewFileJournal(t.TempDir())
	sp := NewShSpawner(j)

	_, wait, err := sp.Spawn(usecase.SpawnSpec{
		ID:        "r1",
		Prompt:    "/saki-builder:build tasks/prd-x.md",
		ConfigDir: &cfgDir,
		Kind:      "build",
		Engine:    domain.EngineOpencode,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if code := wait(); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}

	argv, err := os.ReadFile(j.OutPath("r1"))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimRight(string(argv), "\n"), "\n")
	want := []string{"run", "--format", "json", "--auto", "--command", "build", "--", "tasks/prd-x.md"}
	if len(got) != len(want) {
		t.Fatalf("argv = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("argv = %q, want %q", got, want)
		}
	}
	// The regression itself: the unresolvable raw slash text must never reach the CLI as the message.
	for _, a := range got {
		if strings.HasPrefix(a, "/saki-builder:") || strings.HasPrefix(a, "/build") {
			t.Fatalf("raw slash-command text reached opencode as a message arg: %q", got)
		}
	}
}

// Regression (caught by the real-binary e2e, invisible to a fake): opencode parses its CLI with yargs,
// so a message that STARTS with `--` is read as an unknown flag — the CLI prints its help and exits 1
// having run nothing. Studio prompts really carry such args (`/saki-builder:prd-review --review-only`,
// `/saki-builder:add --list`), so the `--` terminator must keep them intact as the message.
func TestSpawn_OpencodeFlagLikeArgsReachTheModelVerbatim(t *testing.T) {
	writeArgvRecordingOpencode(t)
	cfgDir := pluginProvenProfile(t)
	j := NewFileJournal(t.TempDir())
	sp := NewShSpawner(j)

	_, wait, err := sp.Spawn(usecase.SpawnSpec{
		ID:        "r3",
		Prompt:    "/saki-builder:prd-review --review-only",
		ConfigDir: &cfgDir,
		Kind:      "review",
		Engine:    domain.EngineOpencode,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if code := wait(); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	argv, err := os.ReadFile(j.OutPath("r3"))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimRight(string(argv), "\n"), "\n")
	want := []string{"run", "--format", "json", "--auto", "--command", "prd-review", "--", "--review-only"}
	if len(got) != len(want) {
		t.Fatalf("argv = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("argv = %q, want %q", got, want)
		}
	}
}

// The fallback path: a prose prompt has no command to resolve, so it must still arrive as the plain
// message (never `--command ""`, which opencode would refuse).
func TestSpawn_OpencodeProsePromptStaysAMessage(t *testing.T) {
	writeArgvRecordingOpencode(t)
	cfgDir := pluginProvenProfile(t)
	j := NewFileJournal(t.TempDir())
	sp := NewShSpawner(j)

	const prose = "Proto a Plan-track work item that has a plan but no PRD."
	_, wait, err := sp.Spawn(usecase.SpawnSpec{ID: "r2", Prompt: prose, ConfigDir: &cfgDir, Kind: "proto", Engine: domain.EngineOpencode})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if code := wait(); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	argv, err := os.ReadFile(j.OutPath("r2"))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimRight(string(argv), "\n"), "\n")
	want := []string{"run", "--format", "json", "--auto", "--", prose}
	if len(got) != len(want) {
		t.Fatalf("argv = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("argv = %q, want %q", got, want)
		}
	}
}

// A prompt the operator MEANT as a command but which does not parse must refuse loudly rather than
// degrade into the plain message form — on opencode that form is the KNOWN-DEAD path (raw slash text →
// "isn't a recognized command" → no-op on exit 0), so a silent fallback would re-create the very defect
// the --command invocation closes.
func TestSpawn_OpencodeRefusesAnUnresolvableSlashPrompt(t *testing.T) {
	writeArgvRecordingOpencode(t)
	cfgDir := pluginProvenProfile(t)
	j := NewFileJournal(t.TempDir())
	sp := NewShSpawner(j)

	for _, prompt := range []string{"/saki-builder:build, then do X", "/-x arg"} {
		_, _, err := sp.Spawn(usecase.SpawnSpec{ID: "r9", Prompt: prompt, ConfigDir: &cfgDir, Kind: "build", Engine: domain.EngineOpencode})
		if !errors.Is(err, ErrUnresolvableCommand) {
			t.Errorf("prompt %q: want ErrUnresolvableCommand, got %v", prompt, err)
		}
	}
}

// ...but PROSE is not a failed command — it legitimately takes the message form and must still spawn.
func TestSpawn_OpencodeProseIsNotTreatedAsAFailedCommand(t *testing.T) {
	writeArgvRecordingOpencode(t)
	cfgDir := pluginProvenProfile(t)
	j := NewFileJournal(t.TempDir())
	sp := NewShSpawner(j)

	_, wait, err := sp.Spawn(usecase.SpawnSpec{
		ID: "r10", Prompt: "Proto a Plan-track work item that has a plan but no PRD.",
		ConfigDir: &cfgDir, Kind: "proto", Engine: domain.EngineOpencode,
	})
	if err != nil {
		t.Fatalf("a prose prompt must still spawn, got %v", err)
	}
	if code := wait(); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
}

// 🔒 BR5 — the refusal is opencode-scoped. claude resolves the namespaced command from the message and
// has no --command form, so the same unparseable prompt must spawn there exactly as it does today.
func TestSpawn_ClaudeNeverRefusesAnUnresolvableSlashPrompt(t *testing.T) {
	j := NewFileJournal(t.TempDir())
	sp := NewShSpawner(j)
	sp.sh = writeFakeSh(t, "0")
	if _, _, err := sp.Spawn(claudeSpawnSpec(t, usecase.SpawnSpec{ID: "r11", Prompt: "/saki-builder:build, then do X", Kind: "build"})); err != nil {
		t.Fatalf("claude must be unaffected by the opencode command-split refusal, got %v", err)
	}
}

// E26 9.4.2 — a plugin-less opencode profile refuses loudly THROUGH the spawner (no silent no-op).
func TestSpawn_OpencodePluginMissingFailsLoudly(t *testing.T) {
	t.Setenv("PATH", writeFakeOpencode(t))
	cfgDir := t.TempDir() // no opencode.json → cannot prove the plugin
	j := NewFileJournal(t.TempDir())
	sp := NewShSpawner(j)
	if _, _, err := sp.Spawn(usecase.SpawnSpec{ID: "r1", Prompt: "x", ConfigDir: &cfgDir, Engine: domain.EngineOpencode}); !errors.Is(err, ErrOpencodePluginMissing) {
		t.Fatalf("a plugin-less profile must fail loudly via the spawner, got %v", err)
	}
}

// E26 — engine-aware env parity: claude-only envs never reach an opencode spawn, opencode envs never
// reach claude, and each engine's profile env is dropped/pinned symmetrically.
func TestBuildSpawnEnv_EngineParity(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/inherited-claude")
	t.Setenv("CLAUDE_CODE_MAX_RETRIES", "")
	t.Setenv("OPENCODE_CONFIG", "/inherited-opencode")
	t.Setenv("XDG_CONFIG_HOME", "/inherited-xdg")
	j := NewFileJournal(t.TempDir())
	claudeDir := "/profiles/claude-p"
	opencodeDir := "/profiles/opencode-p"

	has := func(env []string, key string) bool {
		for _, e := range env {
			if strings.HasPrefix(e, key+"=") {
				return true
			}
		}
		return false
	}
	hasExact := func(env []string, entry string) bool {
		for _, e := range env {
			if e == entry {
				return true
			}
		}
		return false
	}

	// claude default — inherited CLAUDE_CONFIG_DIR dropped, retry env defaulted (today's behavior).
	claudeDefault := buildSpawnEnv(usecase.SpawnSpec{ID: "a", Prompt: "p", Kind: "build"}, j)
	if hasExact(claudeDefault, "CLAUDE_CONFIG_DIR=/inherited-claude") {
		t.Fatal("claude default must drop an inherited CLAUDE_CONFIG_DIR")
	}
	if !has(claudeDefault, "CLAUDE_CODE_MAX_RETRIES") {
		t.Fatal("claude default must set CLAUDE_CODE_MAX_RETRIES")
	}
	// claude with a pinned profile.
	claudePinned := buildSpawnEnv(usecase.SpawnSpec{ID: "a", Prompt: "p", ConfigDir: &claudeDir, Kind: "build"}, j)
	if !hasExact(claudePinned, "CLAUDE_CONFIG_DIR="+claudeDir) {
		t.Fatal("claude with a profile must pin CLAUDE_CONFIG_DIR")
	}
	// opencode default — inherited opencode envs dropped, NO claude envs.
	ocDefault := buildSpawnEnv(usecase.SpawnSpec{ID: "a", Prompt: "p", Engine: domain.EngineOpencode, Kind: "build"}, j)
	if hasExact(ocDefault, "OPENCODE_CONFIG=/inherited-opencode") {
		t.Fatal("opencode default must drop an inherited OPENCODE_CONFIG")
	}
	if hasExact(ocDefault, "XDG_CONFIG_HOME=/inherited-xdg") {
		t.Fatal("opencode default must drop an inherited XDG_CONFIG_HOME")
	}
	if has(ocDefault, "CLAUDE_CODE_MAX_RETRIES") || has(ocDefault, "CLAUDE_CONFIG_DIR") {
		t.Fatal("opencode must never carry claude-only envs")
	}
	// opencode with a pinned profile.
	ocPinned := buildSpawnEnv(usecase.SpawnSpec{ID: "a", Prompt: "p", ConfigDir: &opencodeDir, Engine: domain.EngineOpencode, Kind: "build"}, j)
	if !hasExact(ocPinned, "XDG_CONFIG_HOME="+opencodeDir) {
		t.Fatal("opencode with a profile must pin XDG_CONFIG_HOME")
	}
	if has(ocPinned, "CLAUDE_CONFIG_DIR") || has(ocPinned, "CLAUDE_CODE_MAX_RETRIES") {
		t.Fatal("pinned opencode must still never carry claude-only envs")
	}
}

// E26 follow-up — the command split reaches the child as env, never as script bytes (🔒 BR3).
// opencode: SAKI_CMD carries the BARE name and SAKI_PROMPT carries only the ARGS (the whole prompt
// would re-inject the unresolvable `/saki-builder:build …` text as the command's message).
// claude: unchanged — the FULL prompt in SAKI_PROMPT and no SAKI_CMD at all (🔒 BR5).
func TestBuildSpawnEnv_CommandSplit(t *testing.T) {
	j := NewFileJournal(t.TempDir())
	const prompt = "/saki-builder:build tasks/prd-x.md"

	valueOf := func(env []string, key string) (string, bool) {
		var out string
		var found bool
		for _, e := range env { // last write wins, mirroring exec's env semantics
			if strings.HasPrefix(e, key+"=") {
				out, found = strings.TrimPrefix(e, key+"="), true
			}
		}
		return out, found
	}

	oc := buildSpawnEnv(usecase.SpawnSpec{ID: "a", Prompt: prompt, Kind: "build", Engine: domain.EngineOpencode}, j)
	if got, _ := valueOf(oc, "SAKI_CMD"); got != "build" {
		t.Errorf("opencode SAKI_CMD = %q, want %q (bare, namespace stripped)", got, "build")
	}
	if got, _ := valueOf(oc, "SAKI_PROMPT"); got != "tasks/prd-x.md" {
		t.Errorf("opencode SAKI_PROMPT = %q, want the args only", got)
	}

	// A prose prompt has no command: the full text is the message and SAKI_CMD is absent.
	const prose = "Proto a Plan-track work item that has a plan but no PRD."
	ocProse := buildSpawnEnv(usecase.SpawnSpec{ID: "a", Prompt: prose, Kind: "proto", Engine: domain.EngineOpencode}, j)
	if _, found := valueOf(ocProse, "SAKI_CMD"); found {
		t.Error("a prose prompt must not set SAKI_CMD")
	}
	if got, _ := valueOf(ocProse, "SAKI_PROMPT"); got != prose {
		t.Errorf("prose SAKI_PROMPT = %q, want the full prompt", got)
	}

	// 🔒 BR5 — claude is untouched by the split.
	for _, engine := range []domain.RunEngine{"", domain.EngineClaude} {
		cl := buildSpawnEnv(usecase.SpawnSpec{ID: "a", Prompt: prompt, Kind: "build", Engine: engine}, j)
		if got, _ := valueOf(cl, "SAKI_PROMPT"); got != prompt {
			t.Errorf("claude (engine=%q) SAKI_PROMPT = %q, want the FULL prompt", engine, got)
		}
		if _, found := valueOf(cl, "SAKI_CMD"); found {
			t.Errorf("claude (engine=%q) must not set SAKI_CMD", engine)
		}
	}
}

func TestSpawn_OpencodeBinaryNotFound(t *testing.T) {
	j := NewFileJournal(t.TempDir())
	sp := NewShSpawner(j)
	emptyPath := t.TempDir() // no `opencode` binary here
	t.Setenv("PATH", emptyPath)
	if _, _, err := sp.Spawn(usecase.SpawnSpec{ID: "r1", Prompt: "x", Engine: domain.EngineOpencode}); !errors.Is(err, usecase.ErrBinaryNotFound) {
		t.Fatalf("want ErrBinaryNotFound, got %v", err)
	}
}

// E26 9.1.1 — a claude spawn is unaffected by the engine pre-check (claude is the default; a missing
// opencode binary must not affect it).
func TestSpawn_ClaudeIgnoresOpencodeBinaryCheck(t *testing.T) {
	j := NewFileJournal(t.TempDir())
	sp := NewShSpawner(j)
	sp.sh = writeFakeSh(t, "0")
	t.Setenv("PATH", t.TempDir()) // no claude/opencoide on PATH — but the fake sh short-circuits the real one
	pid, wait, err := sp.Spawn(claudeSpawnSpec(t, usecase.SpawnSpec{ID: "r2", Prompt: "x", Engine: ""}))
	if err != nil {
		t.Fatalf("claude default must spawn regardless of PATH: %v", err)
	}
	if pid <= 0 {
		t.Fatalf("pid %d", pid)
	}
	if code := wait(); code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
}

func TestFilterEnv(t *testing.T) {
	in := []string{"A=1", "CLAUDE_CONFIG_DIR=/x", "B=2"}
	out := filterEnv(in, "CLAUDE_CONFIG_DIR")
	for _, e := range out {
		if strings.HasPrefix(e, "CLAUDE_CONFIG_DIR=") {
			t.Fatalf("CLAUDE_CONFIG_DIR not filtered: %v", out)
		}
	}
	if len(out) != 2 {
		t.Fatalf("want 2 remaining, got %d", len(out))
	}
}

// F2 slice 1, criterion 1.4 — a peel-off proving PRECEDENCE, not just each branch in isolation: as
// each earlier fault is cleared, preflight must fall through to the NEXT check in order, not jump
// straight to a nil error. This is the regression proof for extracting preflight's body into
// EngineBinaryCheck/EngineProfileProof (spawner.go) — the extraction must not reorder or skip a check.
func TestPreflight_OpencodePeelOff(t *testing.T) {
	j := NewFileJournal(t.TempDir())

	// (1) binary off PATH -> ErrBinaryNotFound, before anything else is even considered.
	t.Setenv("PATH", t.TempDir())
	sp := NewShSpawner(j)
	if _, _, err := sp.Spawn(usecase.SpawnSpec{ID: "peel1", Prompt: "/build, then do X", Engine: domain.EngineOpencode}); !errors.Is(err, usecase.ErrBinaryNotFound) {
		t.Fatalf("(1) binary missing: want ErrBinaryNotFound, got %v", err)
	}

	// (2) PATH restored, prompt still unparseable -> ErrUnresolvableCommand (never reaches the profile proof).
	t.Setenv("PATH", writeFakeOpencode(t))
	if _, _, err := sp.Spawn(usecase.SpawnSpec{ID: "peel2", Prompt: "/build, then do X", Engine: domain.EngineOpencode}); !errors.Is(err, ErrUnresolvableCommand) {
		t.Fatalf("(2) unparseable prompt: want ErrUnresolvableCommand, got %v", err)
	}

	// (3) prompt fixed (parseable), profile has no opencode.json -> ErrEngineNotProvisioned.
	brokenProfile := t.TempDir()
	if _, _, err := sp.Spawn(usecase.SpawnSpec{ID: "peel3", Prompt: "/build x", ConfigDir: &brokenProfile, Engine: domain.EngineOpencode}); !errors.Is(err, usecase.ErrEngineNotProvisioned) {
		t.Fatalf("(3) unprovisioned profile: want ErrEngineNotProvisioned, got %v", err)
	}
}
