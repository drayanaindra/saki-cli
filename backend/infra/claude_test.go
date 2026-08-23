package infra

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/drayanaindra/saki-cli/backend/domain"
	"github.com/drayanaindra/saki-cli/backend/usecase"
)

func writeClaudeProfile(t *testing.T, dir, installed, settings string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugins", "installed_plugins.json"), []byte(installed), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveClaudeProfile_PresentCanonical(t *testing.T) {
	dir := t.TempDir()
	writeClaudeProfile(t, dir,
		`{"version":2,"plugins":{"saketek@saki-builder":[{"scope":"user","installPath":"/tmp/saki-builder","version":"0.5.0"}]}}`,
		`{"enabledPlugins":{"saketek@saki-builder":true}}`,
	)

	plugin, err := resolveClaudeProfile(&dir)
	if err != nil {
		t.Fatalf("canonical installed-and-enabled profile must resolve, got %v", err)
	}
	if plugin.ID != "saketek@saki-builder" {
		t.Fatalf("resolved plugin ID = %q, want saketek@saki-builder", plugin.ID)
	}
	if plugin.Version != "0.5.0" {
		t.Fatalf("resolved plugin version = %q, want 0.5.0", plugin.Version)
	}
	if plugin.InstallPath != "/tmp/saki-builder" {
		t.Fatalf("resolved plugin installPath = %q, want /tmp/saki-builder", plugin.InstallPath)
	}
}

func TestResolveClaudeProfile_FailClosedCases(t *testing.T) {
	cases := []struct {
		name      string
		installed string
		settings  string
	}{
		{"missing installed file", "", `{"enabledPlugins":{"saketek@saki-builder":true}}`},
		{"missing settings file", `{"plugins":{"saketek@saki-builder":[{"version":"1"}]}}`, ""},
		{"malformed installed", "{", `{"enabledPlugins":{"saketek@saki-builder":true}}`},
		{"malformed settings", `{"plugins":{"saketek@saki-builder":[{"version":"1"}]}}`, "{"},
		{"unsupported plugin", `{"plugins":{"unknown@marketplace":[{"version":"1"}]}}`, `{"enabledPlugins":{"unknown@marketplace":true}}`},
		{"plugins wrong type", `{"plugins":true}`, `{"enabledPlugins":{"saketek@saki-builder":true}}`},
		{"enabled plugins wrong type", `{"plugins":{"saketek@saki-builder":[{"version":"1"}]}}`, `{"enabledPlugins":true}`},
		{"empty records", `{"plugins":{"saketek@saki-builder":[]}}`, `{"enabledPlugins":{"saketek@saki-builder":true}}`},
		{"missing version", `{"plugins":{"saketek@saki-builder":[{}]}}`, `{"enabledPlugins":{"saketek@saki-builder":true}}`},
		{"disabled selected plugin", `{"plugins":{"saketek@saki-builder":[{"version":"1"}],"saki-builder@saketek":[{"version":"2"}]}}`, `{"enabledPlugins":{"saketek@saki-builder":false,"saki-builder@saketek":true}}`},
		{"spelling mismatch", `{"plugins":{"saketek@saki-builder":[{"version":"1"}]}}`, `{"enabledPlugins":{"saki-builder@saketek":true}}`},
		{"unrelated enabled settings", `{"plugins":{"saketek@saki-builder":[{"version":"1"}]}}`, `{"enabledPlugins":{"other@marketplace":true}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.installed != "" {
				writeClaudeProfile(t, dir, tc.installed, tc.settings)
			} else {
				if err := os.MkdirAll(filepath.Join(dir, "plugins"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(tc.settings), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := resolveClaudeProfile(&dir); err == nil {
				t.Fatal("invalid profile resolved successfully")
			}
			if err := ClaudeProfileProof(&dir); !errors.Is(err, usecase.ErrEngineNotProvisioned) {
				t.Fatalf("proof error = %v, want ErrEngineNotProvisioned", err)
			}
		})
	}
}

func TestResolveClaudeProfile_Precedence(t *testing.T) {
	tests := []struct {
		name        string
		installed   string
		settings    string
		wantID      string
		wantVersion string
	}{
		{
			name:        "canonical wins",
			installed:   `{"plugins":{"saketek@saki-builder":[{"version":"0.5.0"}],"saki-builder@saketek":[{"version":"0.30.2"}]}}`,
			settings:    `{"enabledPlugins":{"saketek@saki-builder":true,"saki-builder@saketek":true}}`,
			wantID:      "saketek@saki-builder",
			wantVersion: "0.5.0",
		},
		{
			name:        "fallback resolves",
			installed:   `{"plugins":{"saki-builder@saketek":[{"version":"0.30.2"}]}}`,
			settings:    `{"enabledPlugins":{"saki-builder@saketek":true}}`,
			wantID:      "saki-builder@saketek",
			wantVersion: "0.30.2",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeClaudeProfile(t, dir, tc.installed, tc.settings)
			plugin, err := resolveClaudeProfile(&dir)
			if err != nil {
				t.Fatal(err)
			}
			if plugin.ID != tc.wantID || plugin.Version != tc.wantVersion {
				t.Fatalf("resolved plugin = %#v, want %s at version %s", plugin, tc.wantID, tc.wantVersion)
			}
		})
	}
}

// writeSkillFile creates <dir>/skills/<name>/SKILL.md so a claude installPath fixture has a real
// sentinel file to stat — the codex twin of this pattern already exists via writeCodexProfile's
// config.toml; claude's proof reads a skill FILE, not a config table, so the fixture writes one.
func writeSkillFile(t *testing.T, installPath, name string) {
	t.Helper()
	dir := filepath.Join(installPath, "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeProfileProof_SkillMissing(t *testing.T) {
	dir := t.TempDir()
	installPath := filepath.Join(t.TempDir(), "cache", "saketek", "saki-builder", "0.30.2")
	if err := os.MkdirAll(installPath, 0o755); err != nil {
		t.Fatal(err)
	}
	writeClaudeProfile(t, dir,
		`{"plugins":{"saketek@saki-builder":[{"installPath":"`+installPath+`","version":"0.30.2"}]}}`,
		`{"enabledPlugins":{"saketek@saki-builder":true}}`,
	)
	// installPath exists but carries no skills/build/SKILL.md — a plugin installed+enabled with a
	// stale/partial cache, the exact case the old proof could not see.
	err := ClaudeProfileProof(&dir)
	if err == nil {
		t.Fatal("proof succeeded for a claude profile missing skills/build/SKILL.md")
	}
	if !errors.Is(err, usecase.ErrEngineNotProvisioned) {
		t.Fatalf("proof error = %v, want it to wrap ErrEngineNotProvisioned", err)
	}
	if !errors.Is(err, ErrClaudeSkillMissing) {
		t.Fatalf("proof error = %v, want it to wrap ErrClaudeSkillMissing", err)
	}
}

func TestClaudeProfileProof_SkillPresent_Succeeds(t *testing.T) {
	dir := t.TempDir()
	installPath := filepath.Join(t.TempDir(), "cache", "saketek", "saki-builder", "0.30.2")
	writeSkillFile(t, installPath, "build")
	writeClaudeProfile(t, dir,
		`{"plugins":{"saketek@saki-builder":[{"installPath":"`+installPath+`","version":"0.30.2"}]}}`,
		`{"enabledPlugins":{"saketek@saki-builder":true}}`,
	)
	if err := ClaudeProfileProof(&dir); err != nil {
		t.Fatalf("proof failed for a fully-provisioned claude profile: %v", err)
	}
}

func TestResolveClaudeProfile_ReadOnly(t *testing.T) {
	dir := t.TempDir()
	installPath := t.TempDir()
	writeSkillFile(t, installPath, "build")
	writeClaudeProfile(t, dir,
		`{"plugins":{"saketek@saki-builder":[{"installPath":"`+installPath+`","version":"1"}]}}`,
		`{"enabledPlugins":{"saketek@saki-builder":true}}`,
	)
	installedPath, settingsPath := claudeProfilePaths(&dir)
	beforeInstalled, err := os.ReadFile(installedPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeSettings, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := ClaudeProfileProof(&dir); err != nil {
		t.Fatal(err)
	}
	afterInstalled, _ := os.ReadFile(installedPath)
	afterSettings, _ := os.ReadFile(settingsPath)
	if !bytes.Equal(beforeInstalled, afterInstalled) || !bytes.Equal(beforeSettings, afterSettings) {
		t.Fatal("Claude proof changed profile files")
	}
}

func TestEngineProfileProof_ClaudeMatchesDirectProof(t *testing.T) {
	dir := t.TempDir()
	writeClaudeProfile(t, dir,
		`{"plugins":{"saketek@saki-builder":[{"version":"1"}]}}`,
		`{"enabledPlugins":{"saketek@saki-builder":true}}`,
	)
	want := ClaudeProfileProof(&dir)
	got := EngineProfileProof(domain.EngineClaude, &dir)
	if (want == nil) != (got == nil) {
		t.Fatalf("direct proof = %v, dispatcher proof = %v", want, got)
	}
}

func TestClaudeProfilePathsUseNativeDefault(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	installed, settings := claudeProfilePaths(nil)
	want := filepath.Join(home, ".claude")
	if filepath.Dir(filepath.Dir(installed)) != want || filepath.Dir(settings) != want {
		t.Fatalf("default Claude profile paths = %q, %q; want root %q", installed, settings, want)
	}
}

func TestClaudeProfileFingerprintCoversOnlyProofFiles(t *testing.T) {
	dir := t.TempDir()
	installPath := t.TempDir()
	writeSkillFile(t, installPath, "build")
	writeClaudeProfile(t, dir,
		`{"plugins":{"saketek@saki-builder":[{"installPath":"`+installPath+`","version":"1"}]}}`,
		`{"enabledPlugins":{"saketek@saki-builder":true}}`,
	)
	base := profileFingerprint(domain.EngineClaude, &dir)
	if err := os.WriteFile(filepath.Join(dir, "unrelated.json"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := profileFingerprint(domain.EngineClaude, &dir); got != base {
		t.Fatal("unrelated Claude profile file changed the fingerprint")
	}
	skillFile := filepath.Join(installPath, "skills", "build", "SKILL.md")
	if err := os.WriteFile(skillFile, []byte("---\nname: build\n---\nchanged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	afterSkillChange := profileFingerprint(domain.EngineClaude, &dir)
	if afterSkillChange == base {
		t.Fatal("skills/build/SKILL.md change did not change the fingerprint")
	}
	installed, _ := claudeProfilePaths(&dir)
	if err := os.WriteFile(installed, []byte(`{"plugins":{"saketek@saki-builder":[{"installPath":"`+installPath+`","version":"2"}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := profileFingerprint(domain.EngineClaude, &dir); got == afterSkillChange {
		t.Fatal("installed_plugins.json change did not change the fingerprint")
	}
	changed := profileFingerprint(domain.EngineClaude, &dir)
	_, settings := claudeProfilePaths(&dir)
	if err := os.WriteFile(settings, []byte(`{"enabledPlugins":{"saketek@saki-builder":false}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := profileFingerprint(domain.EngineClaude, &dir); got == changed {
		t.Fatal("settings.json change did not change the fingerprint")
	}
}
