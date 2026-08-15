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

// writeArgvRecordingCodex writes an executable `codex` sh that records its OWN argv (one arg per line)
// into $SAKI_OUT, so a test can assert the exact command line the real spawner produced rather than
// only the script string. Prepended to PATH (not substituted) because these tests run the real `sh`.
func writeArgvRecordingCodex(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\" >> \"$SAKI_OUT\"; done\necho 0 > \"$SAKI_EXIT\"\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// writeEnvRecordingCodex writes a `codex` that dumps its ENV into $SAKI_OUT (for the CODEX_HOME tests).
func writeEnvRecordingCodex(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\nenv > \"$SAKI_OUT\"\necho 0 > \"$SAKI_EXIT\"\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// skillProvenProfile returns a pinned profile dir carrying the saki-builder `build` skill at the exact
// path the spawned child reads (<dir>/codex/skills/build/SKILL.md), so CodexSkillsProof passes.
func skillProvenProfile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	skill := filepath.Join(dir, "codex", "skills", "build")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: build\ndescription: autonomous multi-slice build\n---\nBuild the PRD.\n"
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func outLines(t *testing.T, j usecase.Journal, id string) []string {
	t.Helper()
	b, err := os.ReadFile(j.OutPath(id))
	if err != nil {
		t.Fatalf("read out: %v", err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// THE REGRESSION LOCK, asserted on the REAL spawner's argv.
//
// codex takes the CLAUDE shape, not opencode's: `codex exec` resolves a namespaced slash command that
// arrives in the message (spiked against codex-cli 0.147.0), so the WHOLE prompt is the message and
// there is no `--command` split. If someone ever "unifies" codex with the opencode branch, this fails.
func TestSpawn_CodexSendsTheWholePromptAsTheMessage(t *testing.T) {
	writeArgvRecordingCodex(t)
	cfgDir := skillProvenProfile(t)
	j := NewFileJournal(t.TempDir())
	sp := NewShSpawner(j)

	const prompt = "/saki-builder:build tasks/prd-x.md RESUME slice 2"
	_, wait, err := sp.Spawn(usecase.SpawnSpec{
		ID: "c1", Prompt: prompt, ConfigDir: &cfgDir, Kind: "build", Engine: domain.EngineCodex,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if code := wait(); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	argv := outLines(t, j, "c1")
	if argv[0] != "exec" {
		t.Errorf("argv[0] = %q, want the `exec` subcommand", argv[0])
	}
	// The prompt must arrive as ONE argv element, verbatim — no split, no quote-wrapping (unlike
	// opencode, codex substitutes args into the skill untouched).
	last := argv[len(argv)-1]
	if last != prompt {
		t.Errorf("message argv = %q, want the whole prompt %q", last, prompt)
	}
	// ...and NOT via opencode's --command form.
	for _, a := range argv {
		if a == "--command" {
			t.Fatalf("codex must not use opencode's --command form: %v", argv)
		}
	}
	for _, want := range []string{"--json", "--dangerously-bypass-approvals-and-sandbox", "--skip-git-repo-check"} {
		if !contains(argv, want) {
			t.Errorf("argv missing %q: %v", want, argv)
		}
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// A prompt that does NOT parse as a command must still spawn on codex. This is the 🔒 inverse of
// TestSpawn_OpencodeRefusesAnUnresolvableSlashPrompt: opencode's message form is a known-dead path, so
// it refuses — codex's is the WORKING path, so refusing there would reject prompts that demonstrably
// run. Prose (the Plan-track proto prompt) must spawn too.
func TestSpawn_CodexNeverRefusesAnUnresolvableSlashPrompt(t *testing.T) {
	writeArgvRecordingCodex(t)
	cfgDir := skillProvenProfile(t)
	j := NewFileJournal(t.TempDir())
	sp := NewShSpawner(j)

	for _, prompt := range []string{
		"/saki-builder:build, then do X",
		"/-x arg",
		"Proto a Plan-track work item that has a plan but no PRD.",
	} {
		_, _, err := sp.Spawn(usecase.SpawnSpec{
			ID: "c2", Prompt: prompt, ConfigDir: &cfgDir, Kind: "build", Engine: domain.EngineCodex,
		})
		if err != nil {
			t.Errorf("prompt %q must spawn on codex, got %v", prompt, err)
		}
	}
}

// A pinned profile reaches the child as CODEX_HOME=<dir>/codex — the SAME path CodexSkillsProof read,
// so the home that was proven is the home that runs.
func TestSpawn_CodexPinsCodexHomeToTheProfile(t *testing.T) {
	writeEnvRecordingCodex(t)
	cfgDir := skillProvenProfile(t)
	j := NewFileJournal(t.TempDir())
	sp := NewShSpawner(j)

	_, wait, err := sp.Spawn(usecase.SpawnSpec{
		ID: "c3", Prompt: "/saki-builder:build x", ConfigDir: &cfgDir, Kind: "build", Engine: domain.EngineCodex,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	wait()
	env := outLines(t, j, "c3")
	want := "CODEX_HOME=" + filepath.Join(cfgDir, "codex")
	if !contains(env, want) {
		t.Errorf("child env missing %q", want)
	}
	// codex never carries claude-only env (symmetric with the opencode spawn).
	for _, e := range env {
		if strings.HasPrefix(e, "CLAUDE_CONFIG_DIR=") || strings.HasPrefix(e, "CLAUDE_CODE_MAX_RETRIES=") {
			t.Errorf("codex spawn leaked claude-only env: %q", e)
		}
	}
}

// An UNPINNED codex spawn drops an inherited CODEX_HOME, so a foreign profile can never be picked up
// (symmetric with the OPENCODE_CONFIG/XDG_CONFIG_HOME drops).
func TestSpawn_CodexUnpinnedDropsInheritedCodexHome(t *testing.T) {
	writeEnvRecordingCodex(t)
	t.Setenv("CODEX_HOME", "/tmp/some-foreign-codex-home")
	j := NewFileJournal(t.TempDir())
	sp := NewShSpawner(j)

	// No ConfigDir → the proof reads ~/.codex. Skip when the operator has no saki-builder install
	// there: this test is about the ENV drop, not about the host's codex setup.
	if err := CodexSkillsProof(nil); err != nil {
		t.Skip("no saki-builder skills in ~/.codex — run scripts/install-codex-skills.sh")
	}
	_, wait, err := sp.Spawn(usecase.SpawnSpec{ID: "c4", Prompt: "/saki-builder:build x", Kind: "build", Engine: domain.EngineCodex})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	wait()
	for _, e := range outLines(t, j, "c4") {
		if strings.HasPrefix(e, "CODEX_HOME=") {
			t.Errorf("unpinned codex spawn must drop an inherited CODEX_HOME, got %q", e)
		}
	}
}

// 🔒 THE ENGINE-MARKER SCRUB. The studio is routinely started from inside an agent session, so its
// own env carries that agent's markers. Two distinct failures ride on them reaching a foreign child:
//
//  1. /init-env sniffs these markers to choose which environment to scaffold. An inherited CLAUDECODE=1
//     makes a CODEX run scaffold the claude set — a repo that looks initialized and loads nothing,
//     because codex reads AGENTS.md, not CLAUDE.md.
//  2. A live claude session exports CLAUDE_CODE_MESSAGING_TOKEN/_SOCKET. Handing those to a
//     third-party runtime gives a foreign agent a working handle on the operator's claude session.
func TestSpawn_CodexShedsForeignEngineMarkers(t *testing.T) {
	writeEnvRecordingCodex(t)
	// Exactly what a studio launched from a claude session (and, for the cross case, an opencode one)
	// hands down.
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("CLAUDE_CODE_MESSAGING_TOKEN", "sentinel-not-a-real-token")
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", "/tmp/sentinel.sock")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sentinel-session")
	t.Setenv("OPENCODE", "1")
	cfgDir := skillProvenProfile(t)
	j := NewFileJournal(t.TempDir())
	sp := NewShSpawner(j)

	_, wait, err := sp.Spawn(usecase.SpawnSpec{
		ID: "c7", Prompt: "/saki-builder:init-env tasks/prd-x.md", ConfigDir: &cfgDir,
		Kind: "init", Engine: domain.EngineCodex,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	wait()
	for _, e := range outLines(t, j, "c7") {
		name, _, _ := strings.Cut(e, "=")
		if strings.HasPrefix(name, "CLAUDE") || strings.HasPrefix(name, "OPENCODE") {
			t.Errorf("a codex child must not inherit the %q marker namespace, got %q", name, e)
		}
	}
	// ...while its OWN marker namespace is untouched (CODEX_HOME is what makes the profile work).
	if !contains(outLines(t, j, "c7"), "CODEX_HOME="+filepath.Join(cfgDir, "codex")) {
		t.Error("the codex child lost its own CODEX_HOME")
	}
}

// The symmetric case: an opencode child sheds claude's and codex's namespaces, keeps its own.
func TestSpawn_OpencodeShedsForeignEngineMarkers(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("CLAUDE_CODE_MESSAGING_TOKEN", "sentinel-not-a-real-token")
	t.Setenv("CODEX_HOME", "/tmp/foreign-codex")
	cfgDir := pluginProvenProfile(t)
	j := NewFileJournal(t.TempDir())

	env := buildSpawnEnv(usecase.SpawnSpec{
		ID: "o7", Prompt: "/saki-builder:build x", ConfigDir: &cfgDir, Kind: "build", Engine: domain.EngineOpencode,
	}, j)
	for _, e := range env {
		name, _, _ := strings.Cut(e, "=")
		if strings.HasPrefix(name, "CLAUDE") || strings.HasPrefix(name, "CODEX") {
			t.Errorf("an opencode child must not inherit %q, got %q", name, e)
		}
	}
}

// 🔒 BR5 — claude's env stays byte-identical. It must STILL see its own markers: scrubbing is scoped to
// non-claude engines, so this proves the change did not leak sideways into the default path.
func TestSpawn_ClaudeKeepsItsOwnMarkers(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sentinel-session")
	j := NewFileJournal(t.TempDir())
	cfg := t.TempDir()

	env := buildSpawnEnv(usecase.SpawnSpec{ID: "k1", Prompt: "/saki-builder:build x", ConfigDir: &cfg, Kind: "build"}, j)
	for _, want := range []string{"CLAUDECODE=1", "CLAUDE_CODE_SESSION_ID=sentinel-session"} {
		if !contains(env, want) {
			t.Errorf("claude must keep %q — its env is byte-identical by invariant", want)
		}
	}
}

// filterEnvPrefix matches on the NAME only: a var whose VALUE happens to start with the prefix is not
// a marker and must survive.
func TestFilterEnvPrefix_MatchesNameNotValue(t *testing.T) {
	in := []string{"CLAUDECODE=1", "CLAUDE_X=2", "EDITOR=CLAUDE_IS_MY_VALUE", "PATH=/bin", "NOEQUALS"}
	got := filterEnvPrefix(in, "CLAUDE")
	for _, want := range []string{"EDITOR=CLAUDE_IS_MY_VALUE", "PATH=/bin", "NOEQUALS"} {
		if !contains(got, want) {
			t.Errorf("filterEnvPrefix dropped %q, which is not a marker", want)
		}
	}
	for _, gone := range []string{"CLAUDECODE=1", "CLAUDE_X=2"} {
		if contains(got, gone) {
			t.Errorf("filterEnvPrefix kept the marker %q", gone)
		}
	}
}

// A codex home WITHOUT the saki-builder skills refuses loudly THROUGH the spawner. Without this the run
// still exits 0 — the model just answers that it cannot find the command — and the classifier parks a
// build that never started (rule 4: prove install state by reading, never from an exit code).
func TestSpawn_CodexSkillsMissingFailsLoudly(t *testing.T) {
	writeArgvRecordingCodex(t)
	empty := t.TempDir() // a profile dir with no codex/skills/build/SKILL.md
	j := NewFileJournal(t.TempDir())
	sp := NewShSpawner(j)

	_, _, err := sp.Spawn(usecase.SpawnSpec{
		ID: "c5", Prompt: "/saki-builder:build x", ConfigDir: &empty, Kind: "build", Engine: domain.EngineCodex,
	})
	if !errors.Is(err, ErrCodexSkillsMissing) {
		t.Fatalf("want ErrCodexSkillsMissing, got %v", err)
	}
}

// A missing codex binary is ErrBinaryNotFound (→ the build path parks immediately rather than
// re-arming a spawn that can never succeed — buildengine.go rearmOrPark).
func TestSpawn_CodexBinaryMissingIsBinaryNotFound(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no codex on PATH
	j := NewFileJournal(t.TempDir())
	sp := NewShSpawner(j)

	_, _, err := sp.Spawn(usecase.SpawnSpec{ID: "c6", Prompt: "x", Kind: "build", Engine: domain.EngineCodex})
	if !errors.Is(err, usecase.ErrBinaryNotFound) {
		t.Fatalf("want ErrBinaryNotFound, got %v", err)
	}
}

// pluginRegisteredProfile writes a profile whose config.toml registers the saki-builder plugin the way
// `codex plugin add` really does — shape copied from a live ~/.codex/config.toml.
func pluginRegisteredProfile(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	home := filepath.Join(dir, "codex")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// THE CANONICAL INSTALL. `codex plugin add` registers the plugin in config.toml and the plugin carries
// the skills — nothing lands in <home>/skills/. A proof that only looked there would refuse the normal,
// supported installation (which is exactly the bug this test was written after finding).
func TestCodexSkillsProof_AcceptsTheRegisteredPlugin(t *testing.T) {
	real := `model = "gpt-5.6-luna"

[marketplaces.saketek]
source_type = "git"
source = "https://github.com/drayanaindra/saki-builder.git"

[plugins."saki-builder@saketek"]
enabled = true

[hooks.state]
`
	dir := pluginRegisteredProfile(t, real)
	if err := CodexSkillsProof(&dir); err != nil {
		t.Fatalf("a profile with the plugin registered must pass, got %v", err)
	}
}

func TestCodexSkillsProof_PluginTableVariants(t *testing.T) {
	cases := []struct {
		desc string
		toml string
		ok   bool
	}{
		{"registered with enabled=true", "[plugins.\"saki-builder@saketek\"]\nenabled = true\n", true},
		{"registered with no enabled key (codex's default for a registered plugin)", "[plugins.\"saki-builder@saketek\"]\n", true},
		{"a fork's marketplace still counts", "[plugins.\"saki-builder@myfork\"]\nenabled = true\n", true},
		{"unquoted table name", "[plugins.saki-builder@saketek]\nenabled = true\n", true},
		{"explicitly disabled", "[plugins.\"saki-builder@saketek\"]\nenabled = false\n", false},
		{"a DIFFERENT plugin is registered", "[plugins.\"other@saketek\"]\nenabled = true\n", false},
		{"no plugins at all", "model = \"x\"\n", false},
		// 🔒 line-anchored: a commented-out or quoted registration must NOT read as live.
		{"commented out", "# [plugins.\"saki-builder@saketek\"]\nenabled = true\n", false},
		{"mentioned inside a string value", "note = \"add [plugins.\\\"saki-builder@saketek\\\"] later\"\n", false},
		// 🔒 the NEXT table's enabled=false must not be attributed to this plugin.
		{"a later table is disabled", "[plugins.\"saki-builder@saketek\"]\n\n[plugins.\"other@x\"]\nenabled = false\n", true},
	}
	for _, tc := range cases {
		dir := pluginRegisteredProfile(t, tc.toml)
		err := CodexSkillsProof(&dir)
		if tc.ok && err != nil {
			t.Errorf("%s: want pass, got %v", tc.desc, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("%s: want refusal, got pass", tc.desc)
		}
	}
}

func TestCodexSkillsProof(t *testing.T) {
	proven := skillProvenProfile(t)
	if err := CodexSkillsProof(&proven); err != nil {
		t.Errorf("a profile carrying the build skill must pass, got %v", err)
	}
	empty := t.TempDir()
	if err := CodexSkillsProof(&empty); !errors.Is(err, ErrCodexSkillsMissing) {
		t.Errorf("want ErrCodexSkillsMissing, got %v", err)
	}
	// The error must name the installer — a refusal the operator cannot act on is only half a refusal.
	err := CodexSkillsProof(&empty)
	if err == nil || !strings.Contains(err.Error(), "install-codex-skills.sh") {
		t.Errorf("refusal must point at the installer, got %v", err)
	}
	// ...and it must ALSO carry the usecase sentinel, which is what makes the HTTP layer surface that
	// diagnosis instead of a generic "spawn failed" (adapter writeSpawnResult).
	if !errors.Is(err, usecase.ErrEngineNotProvisioned) {
		t.Errorf("refusal must wrap ErrEngineNotProvisioned so the reason reaches the operator, got %v", err)
	}
}

// The proof and the spawn must resolve the SAME home, or a run could be proven against one profile and
// executed against another.
func TestCodexHomePath(t *testing.T) {
	dir := "/tmp/profile"
	if got, want := codexHomePath(&dir), filepath.Join(dir, "codex"); got != want {
		t.Errorf("pinned = %q, want %q", got, want)
	}
	home, _ := os.UserHomeDir()
	if got, want := codexHomePath(nil), filepath.Join(home, ".codex"); got != want {
		t.Errorf("unpinned = %q, want %q", got, want)
	}
	empty := ""
	if got, want := codexHomePath(&empty), filepath.Join(home, ".codex"); got != want {
		t.Errorf("empty configDir = %q, want the default %q", got, want)
	}
}

// 🔒 rule 1 — adding codex must not perturb the claude or opencode scripts. This pins all three shapes
// side by side, so a future edit to one is visible as a diff to the others.
func TestBuildRunScript_ThreeEngines(t *testing.T) {
	claude := buildRunScript("build", domain.EngineClaude, false)
	if !strings.HasPrefix(claude, `claude -p "$SAKI_PROMPT" --output-format stream-json --verbose`) {
		t.Errorf("claude script drifted: %q", claude)
	}
	oc := buildRunScript("build", domain.EngineOpencode, true)
	if !strings.Contains(oc, `--command "$SAKI_CMD"`) {
		t.Errorf("opencode script drifted: %q", oc)
	}
	cx := buildRunScript("build", domain.EngineCodex, false)
	if !strings.Contains(cx, `codex exec --json`) || !strings.Contains(cx, `"$SAKI_PROMPT" < /dev/null`) {
		t.Errorf("codex script drifted: %q", cx)
	}
	// Every engine keeps the durable-exit contract.
	for name, s := range map[string]string{"claude": claude, "opencode": oc, "codex": cx} {
		if !strings.Contains(s, `> "$SAKI_OUT" 2>&1; echo $? > "$SAKI_EXIT"`) {
			t.Errorf("%s lost the durable-exit contract: %q", name, s)
		}
	}
}

// codex must never get claude's elevated init flag (its own bypass flag already covers the write path,
// and --dangerously-skip-permissions is not a codex flag at all).
func TestBuildRunScript_CodexInitHasNoClaudePermissionFlag(t *testing.T) {
	s := buildRunScript("init", domain.EngineCodex, false)
	if strings.Contains(s, "--dangerously-skip-permissions") {
		t.Errorf("codex init leaked claude's permission flag: %q", s)
	}
}
