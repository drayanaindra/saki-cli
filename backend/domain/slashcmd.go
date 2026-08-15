package domain

import (
	"regexp"
	"strings"
)

// slashCommandRE matches a prompt's LEADING slash command, capturing the bare name with any namespace
// ("saki-builder:", or any `word:`) dropped.
//
// The name must START alphanumeric: a leading `-` would reach the spawn as `--command -x`, which yargs
// refuses to consume as the option's value (the same flag-parsing hazard the `--` terminator closes for
// the message). The `{0,63}` tail bounds the total at 64 — it is the only value that reaches the spawn
// as a CLI flag argument, so an unbounded capture from an operator-authored prompt has no business
// being passed through.
//
// NOTE: this is deliberately STRICTER than commandNameOf in apps/server/src/cmdNs.ts:14
// (`/^\s*\/(?:[\w-]+:)?([\w-]+)/`), which has no trailing boundary and no length cap. That one only
// rewrites a namespace for claude, where an over-matched name is harmless; here the capture becomes an
// argv element. The two therefore disagree on inputs like `/build, then do X` — which is why an
// unsplittable `/`-leading prompt is REFUSED at the spawn rather than silently degraded (see
// SplitSlashCommand / ErrUnresolvableCommand).
var slashCommandRE = regexp.MustCompile(`^\s*/(?:[\w-]+:)?([A-Za-z0-9][A-Za-z0-9_-]{0,63})(?:\s+|$)`)

// leadingSlashRE matches a prompt that OPENS with a slash — i.e. one the operator meant as a command,
// whether or not it parses as one.
var leadingSlashRE = regexp.MustCompile(`^\s*/`)

// LooksLikeSlashCommand reports whether the prompt opens with `/`. Used to tell "prose, send as a
// message" (fine) apart from "meant to be a command but did not parse" (must fail loudly on opencode —
// the plain message form is a KNOWN-DEAD path there, see SplitSlashCommand).
func LooksLikeSlashCommand(prompt string) bool { return leadingSlashRE.MatchString(prompt) }

// SplitSlashCommand splits a studio prompt into the BARE command name and its remaining arguments.
//
// The studio emits `/saki-builder:<name> <args>` for every journey run (frontend/src/lib/commands.ts).
// Claude Code resolves that namespaced form directly from the message, so the claude spawn passes the
// whole prompt through untouched. opencode does NOT: its `run` subcommand never expands a slash command
// that arrives in the message — the raw text reaches the model, which answers that it is not a
// recognized command, and the run no-ops on a clean exit 0. opencode's real invocation form is
// `run --command <bare-name> "<args>"`, and it installs the saki-builder skills BARE (`build`, `prd`,
// `qa` — per docs/OPENCODE-INSTALL.md as shipped inside the @saketek/saki-builder npm package; that doc
// is NOT vendored in this repo), hence the namespace strip.
//
// Returns ok=false for a prompt that is not a leading slash command (e.g. the Plan-track proto prompt,
// which opens with prose) — the caller then falls back to the plain message form. A refused prompt
// reports empty parts, so a caller that ignores ok can never spawn `--command ""`.
func SplitSlashCommand(prompt string) (name, rest string, ok bool) {
	m := slashCommandRE.FindStringSubmatch(prompt)
	if m == nil {
		return "", "", false
	}
	return m[1], strings.TrimSpace(prompt[len(m[0]):]), true
}
