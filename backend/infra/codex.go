package infra

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/drayanaindra/saki-cli/backend/usecase"
)

// ErrCodexSkillsMissing is returned by CodexSkillsProof when the codex home a spawn will use cannot
// resolve the saki-builder commands — a loud refusal, never a silent no-op run.
//
// This is the codex twin of ErrOpencodePluginMissing, and exists for the same rule-4 reason: install
// state is proven by READING the profile, never inferred from a run's exit code. Unprovisioned,
// `codex exec` still exits 0 — the model simply answers that it cannot find the command, and the
// classifier parks a build that never started.
// Worded to mirror ErrOpencodePluginMissing exactly, so the two engines' refusals read alike — and so
// it does not echo the ErrEngineNotProvisioned text it is wrapped in.
var ErrCodexSkillsMissing = errors.New("codex profile does not resolve @saketek/saki-builder")

// codexProofSkill is the skill the loose-skills fallback reads as the marker for a manual install.
// `build` is the journey's load-bearing command, so its absence means a build run cannot resolve.
const codexProofSkill = "build"

// codexPluginTableRE matches the config.toml table that registers the saki-builder plugin, e.g.
//
//	[plugins."saki-builder@saketek"]
//
// LINE-ANCHORED (project rule): the same text can appear inside a string value or a comment, and an
// unanchored match would read a commented-out registration as a live one. The marketplace suffix is
// left open — a plugin installed from a fork registers under a different marketplace name.
var codexPluginTableRE = regexp.MustCompile(`(?m)^\s*\[plugins\."?saki-builder@[^"\]]*"?\]\s*$`)

// codexDisabledRE matches an explicit `enabled = false` inside that table.
var codexDisabledRE = regexp.MustCompile(`(?m)^\s*enabled\s*=\s*false\s*$`)

// nextTableRE matches the start of the FOLLOWING TOML table, bounding the plugin table's body.
var nextTableRE = regexp.MustCompile(`(?m)^\s*\[`)

// CodexSkillsProof proves, by reading the profile (NEVER a run exit code — rule 4), that the codex home
// an upcoming spawn will use can resolve the saki-builder commands. The path is resolved EXACTLY the way
// the spawned child resolves it (buildSpawnEnv): a pinned configDir becomes CODEX_HOME=<dir>/codex; with
// no pinned profile the child drops any inherited CODEX_HOME → codex reads ~/.codex. The proof therefore
// ignores the parent process's own CODEX_HOME too, validating the SAME home that actually runs.
//
// TWO install shapes both count, because both really work:
//
//  1. THE CANONICAL ONE — the saki-builder plugin registered in <home>/config.toml
//     (`[plugins."saki-builder@saketek"] enabled = true`). This is the codex analog of opencode's
//     `"plugin": ["@saketek/saki-builder"]`, and it is what `codex plugin add` writes. The plugin
//     carries the skills, so nothing lands in <home>/skills/.
//  2. Loose skills at <home>/skills/build/SKILL.md — a manual/pinned-profile install (an ephemeral
//     throwaway profile cannot practically register a marketplace plugin, and the e2e uses exactly
//     this shape).
//
// Accepting only (2) would refuse the normal, supported installation.
func CodexSkillsProof(configDir *string) error {
	home := codexHomePath(configDir)
	if pluginRegistered(filepath.Join(home, "config.toml")) {
		return nil
	}
	skill := filepath.Join(home, "skills", codexProofSkill, "SKILL.md")
	if _, err := os.Stat(skill); err == nil {
		return nil
	}
	// Wrapped in BOTH sentinels: ErrCodexSkillsMissing for callers that care which engine failed,
	// usecase.ErrEngineNotProvisioned so the HTTP layer surfaces this diagnosis (fix included)
	// instead of a generic "spawn failed". The remediation is usecase.CodexInstallFix — the SAME
	// constant doctor's EngineReport.Fix reads (F2 slice 2) — so this message and that field can
	// never drift apart.
	return fmt.Errorf("%w: %w: %s registers no enabled saki-builder plugin and %s is absent — run:\n%s\n"+
		"(or bash scripts/install-codex-skills.sh to check)",
		usecase.ErrEngineNotProvisioned, ErrCodexSkillsMissing, filepath.Join(home, "config.toml"), skill,
		usecase.CodexInstallFix)
}

// pluginRegistered reports whether config.toml carries an ENABLED saki-builder plugin table. A table
// with no `enabled` key counts as enabled (that is codex's default for a registered plugin); an
// explicit `enabled = false` does not.
func pluginRegistered(configPath string) bool {
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return false
	}
	loc := codexPluginTableRE.FindIndex(raw)
	if loc == nil {
		return false
	}
	body := raw[loc[1]:] // everything after the table header…
	if next := nextTableRE.FindIndex(body); next != nil {
		body = body[:next[0]] // …bounded by the next table, so a LATER table's `enabled = false`
	} //                        can never be misread as this plugin's
	return !codexDisabledRE.Match(body)
}

// codexHomePath resolves the CODEX_HOME the SPAWNED CHILD reads. A pinned profile nests the engine's
// home under it (<configDir>/codex), mirroring how opencode reads <configDir>/opencode from the same
// profile dir — one profile dir, one subdir per engine. Unpinned falls back to codex's own default,
// ~/.codex, which is what the child sees once buildSpawnEnv has dropped any inherited CODEX_HOME.
func codexHomePath(configDir *string) string {
	if configDir != nil && *configDir != "" {
		return filepath.Join(*configDir, "codex")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".codex" // best-effort; the Stat fails loudly downstream
	}
	return filepath.Join(home, ".codex")
}
