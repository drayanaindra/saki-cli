package domain_test

import (
	"testing"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

// E26 follow-up — the studio emits `/saki-builder:<name> <args>` for every journey run
// (frontend/src/lib/commands.ts:13). opencode installs the same skills BARE (`build`, `prd`, `qa` —
// docs/OPENCODE-INSTALL.md), and its `run` subcommand does NOT expand a slash command that arrives in
// the message — it hands the raw text to the model, which answers "isn't a recognized command".
// SplitSlashCommand is the seam that turns that prompt into opencode's real invocation form
// (`--command <bare-name>` + the args as the message).
func TestSplitSlashCommand(t *testing.T) {
	tests := []struct {
		desc     string
		prompt   string
		wantName string
		wantRest string
		wantOK   bool
	}{
		{
			desc:     "namespaced command → bare name + args",
			prompt:   "/saki-builder:build tasks/prd-x.md",
			wantName: "build",
			wantRest: "tasks/prd-x.md",
			wantOK:   true,
		},
		{
			desc:     "bare command (legacy symlink install) → unchanged name",
			prompt:   "/build tasks/prd-x.md",
			wantName: "build",
			wantRest: "tasks/prd-x.md",
			wantOK:   true,
		},
		{
			desc:     "leading whitespace tolerated",
			prompt:   "  /saki-builder:pickup E6",
			wantName: "pickup",
			wantRest: "E6",
			wantOK:   true,
		},
		{
			desc:     "command with no args → empty rest",
			prompt:   "/saki-builder:roadmap",
			wantName: "roadmap",
			wantRest: "",
			wantOK:   true,
		},
		{
			desc:     "hyphenated command name survives the namespace strip",
			prompt:   "/saki-builder:rplan-review tasks/plan.md",
			wantName: "rplan-review",
			wantRest: "tasks/plan.md",
			wantOK:   true,
		},
		{
			desc: "the resume path's em-dash suffix stays in the args (parity with the claude path, " +
				"which passes the same prompt verbatim)",
			prompt:   "/saki-builder:build tasks/prd-x.md — RESUME: continue from slice 3",
			wantName: "build",
			wantRest: "tasks/prd-x.md — RESUME: continue from slice 3",
			wantOK:   true,
		},
		{
			desc:   "prose prompt (buildPlanProtoPrompt) → not a command, message form",
			prompt: "Proto a Plan-track work item (roadmap item I4) that has a plan but no PRD.",
			wantOK: false,
		},
		{
			desc:   "empty prompt → not a command",
			prompt: "",
			wantOK: false,
		},
		{
			desc:   "bare slash with no name → not a command",
			prompt: "/ tasks/prd-x.md",
			wantOK: false,
		},
		{
			desc:   "a path-looking prompt is not a command (no leading-token name)",
			prompt: "/usr/local/bin/thing --flag",
			wantOK: false,
		},
		{
			desc: "over-long name is refused so a malformed --command is never spawned (degrades to " +
				"the message form rather than a broken invocation)",
			prompt: "/" + string(make([]byte, 0)) + longName(65) + " arg",
			wantOK: false,
		},
		{
			desc:     "a name at the length ceiling is still accepted",
			prompt:   "/" + longName(64) + " arg",
			wantName: longName(64),
			wantRest: "arg",
			wantOK:   true,
		},
		{
			desc:     "multiple spaces between name and args collapse out of the rest",
			prompt:   "/saki-builder:qa    tasks/plan.md",
			wantName: "qa",
			wantRest: "tasks/plan.md",
			wantOK:   true,
		},
		{
			desc: "a name starting with '-' is refused — it would reach the spawn as `--command -x`, " +
				"which yargs will not consume as the option's value",
			prompt: "/-x arg",
			wantOK: false,
		},
		{
			desc:   "a name interrupted by punctuation is refused (no partial match)",
			prompt: "/saki-builder:build, then do X",
			wantOK: false,
		},
		{
			desc:   "a path-shaped continuation is refused (no partial match)",
			prompt: "/saki-builder:build/x y",
			wantOK: false,
		},
		{
			desc:     "a command terminated by a newline splits (the arg block follows on later lines)",
			prompt:   "/saki-builder:build\ntasks/prd-x.md",
			wantName: "build",
			wantRest: "tasks/prd-x.md",
			wantOK:   true,
		},
		{
			desc:     "a non-breaking space is trimmed from the rest (strings.TrimSpace is Unicode-aware)",
			prompt:   "/saki-builder:wrap  ",
			wantName: "wrap",
			wantRest: "",
			wantOK:   true,
		},
		{
			desc:     "trailing whitespace is trimmed from the rest",
			prompt:   "/saki-builder:wrap   ",
			wantName: "wrap",
			wantRest: "",
			wantOK:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			name, rest, ok := domain.SplitSlashCommand(tc.prompt)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (prompt %q)", ok, tc.wantOK, tc.prompt)
			}
			if !tc.wantOK {
				// A non-command must report empty parts so a caller that ignores `ok` cannot
				// accidentally spawn `--command ""`.
				if name != "" || rest != "" {
					t.Fatalf("non-command returned name=%q rest=%q, want both empty", name, rest)
				}
				return
			}
			if name != tc.wantName {
				t.Errorf("name = %q, want %q", name, tc.wantName)
			}
			if rest != tc.wantRest {
				t.Errorf("rest = %q, want %q", rest, tc.wantRest)
			}
		})
	}
}

// The name must never carry the namespace: opencode resolves BARE command names, so a leaked
// `saki-builder:` prefix would spawn an unresolvable `--command`.
func TestSplitSlashCommandStripsEveryNamespace(t *testing.T) {
	for _, prompt := range []string{
		"/saki-builder:build x",
		"/other-ns:build x",
		"/a:build x",
	} {
		name, _, ok := domain.SplitSlashCommand(prompt)
		if !ok {
			t.Fatalf("%q: want ok", prompt)
		}
		if name != "build" {
			t.Errorf("%q: name = %q, want %q", prompt, name, "build")
		}
	}
}

// LooksLikeSlashCommand separates "prose → message form is fine" from "meant as a command but did not
// parse → must refuse loudly on opencode", so the two must not be confused.
func TestLooksLikeSlashCommand(t *testing.T) {
	meant := []string{"/saki-builder:build x", "/build", "  /build, then do X", "/-x arg", "/"}
	for _, p := range meant {
		if !domain.LooksLikeSlashCommand(p) {
			t.Errorf("%q: want LooksLikeSlashCommand=true", p)
		}
	}
	prose := []string{"Proto a Plan-track work item that has a plan but no PRD.", "", "do /not/ treat this as a command"}
	for _, p := range prose {
		if domain.LooksLikeSlashCommand(p) {
			t.Errorf("%q: want LooksLikeSlashCommand=false", p)
		}
	}
	// The load-bearing pair: these parse as "meant as a command" yet fail the split — exactly the set
	// that must refuse rather than degrade into opencode's known-dead message form.
	for _, p := range []string{"/build, then do X", "/-x arg", "/" + longName(65) + " arg"} {
		if _, _, ok := domain.SplitSlashCommand(p); ok {
			t.Errorf("%q: expected the split to fail", p)
		}
		if !domain.LooksLikeSlashCommand(p) {
			t.Errorf("%q: expected it to still read as a command attempt", p)
		}
	}
}

func longName(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}
