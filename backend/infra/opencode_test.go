package infra

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drayanaindra/saki-cli/backend/usecase"
)

// writeOpencodeProfile writes <dir>/opencode/opencode.json (the path a pinned XDG_CONFIG_HOME=<dir>
// makes opencode read) with the given content.
func writeOpencodeProfile(t *testing.T, dir, content string) {
	t.Helper()
	p := filepath.Join(dir, "opencode")
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "opencode.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// E26 9.4.1 — a pinned profile carrying @saketek/saki-builder in its plugin array proves green by
// reading the config (never a run exit code).
func TestOpencodePluginProof_Present(t *testing.T) {
	dir := t.TempDir()
	writeOpencodeProfile(t, dir, `{"plugin":["@saketek/saki-builder"]}`)
	if err := OpencodePluginProof(&dir); err != nil {
		t.Fatalf("a profile with the plugin must prove green, got %v", err)
	}
}

// E26 9.4.1 — the standard config shape (with the `$schema` URL, which contains `//`) still parses.
func TestOpencodePluginProof_SchemaUrlConfig(t *testing.T) {
	dir := t.TempDir()
	writeOpencodeProfile(t, dir, `{"$schema":"https://opencode.ai/config.json","plugin":["@saketek/saki-builder"]}`)
	if err := OpencodePluginProof(&dir); err != nil {
		t.Fatalf("a config with a $schema URL must prove green (no JSONC truncation), got %v", err)
	}
}

// E26 9.4.2 — a profile WITHOUT the plugin fails loudly (no silent no-op run).
// F5 slice 1 — the refusal must ALSO wrap ErrEngineNotProvisioned (the sentinel
// backend/adapter/http.go:543 routes an opencode spawn refusal on, not ErrOpencodePluginMissing alone)
// and embed usecase.OpencodeInstallFix verbatim, matching codex.go/claude.go's already-shipped pattern.
func TestOpencodePluginProof_Missing(t *testing.T) {
	dir := t.TempDir()
	writeOpencodeProfile(t, dir, `{"plugin":["some-other-plugin"]}`)
	err := OpencodePluginProof(&dir)
	if !errors.Is(err, ErrOpencodePluginMissing) {
		t.Fatalf("a profile without the plugin must fail with ErrOpencodePluginMissing, got %v", err)
	}
	if !errors.Is(err, usecase.ErrEngineNotProvisioned) {
		t.Fatalf("refusal must wrap ErrEngineNotProvisioned so the reason reaches the operator, got %v", err)
	}
	if !strings.Contains(err.Error(), usecase.OpencodeInstallFix) {
		t.Fatalf("refusal must embed usecase.OpencodeInstallFix verbatim, got %v", err)
	}
}

// E26 9.4.1 — the DEFAULT profile (no configDir) resolves to $HOME/.config/opencode — the same
// profile the spawned child reads (inherited opencode env vars are dropped, never honored).
func TestOpencodePluginProof_DefaultProfile(t *testing.T) {
	home := t.TempDir()
	writeOpencodeProfile(t, filepath.Join(home, ".config"), `{"plugin":["@saketek/saki-builder"]}`)
	t.Setenv("HOME", home)
	t.Setenv("OPENCODE_CONFIG", "/definitely/foreign/opencode.json") // must be IGNORED
	t.Setenv("XDG_CONFIG_HOME", "/definitely/foreign/xdg")
	if err := OpencodePluginProof(nil); err != nil {
		t.Fatalf("the default $HOME profile must prove green (ignoring inherited opencode env), got %v", err)
	}
}

// E26 9.4.2 — an ABSENT config file is a loud failure (cannot prove the plugin).
// F5 slice 1 — same wrap + fix-text bar as TestOpencodePluginProof_Missing (see its comment).
func TestOpencodePluginProof_NoConfigFile(t *testing.T) {
	dir := t.TempDir() // no opencode/opencode.json
	err := OpencodePluginProof(&dir)
	if !errors.Is(err, ErrOpencodePluginMissing) {
		t.Fatalf("a missing config must fail loudly, got %v", err)
	}
	if !errors.Is(err, usecase.ErrEngineNotProvisioned) {
		t.Fatalf("refusal must wrap ErrEngineNotProvisioned so the reason reaches the operator, got %v", err)
	}
	if !strings.Contains(err.Error(), usecase.OpencodeInstallFix) {
		t.Fatalf("refusal must embed usecase.OpencodeInstallFix verbatim, got %v", err)
	}
}

// F5 slice 1 — an unparseable config (fails BOTH the strict-JSON parse AND the JSONC-tolerant fallback)
// is currently the ONLY untested branch in OpencodePluginProof (opencode.go:48-51). The fixture is
// deliberately irrecoverable even after stripJSONC's comment-stripping (never just a comment/trailing
// comma, which would parse clean via the JSONC fallback and mis-hit a different branch). Assert on the
// "unparseable" substring too, to pin this specific branch rather than only the shared wrap contract.
func TestOpencodePluginProof_Unparseable(t *testing.T) {
	dir := t.TempDir()
	writeOpencodeProfile(t, dir, "{ this is not json")
	err := OpencodePluginProof(&dir)
	if !errors.Is(err, ErrOpencodePluginMissing) {
		t.Fatalf("an unparseable config must fail with ErrOpencodePluginMissing, got %v", err)
	}
	if !errors.Is(err, usecase.ErrEngineNotProvisioned) {
		t.Fatalf("refusal must wrap ErrEngineNotProvisioned so the reason reaches the operator, got %v", err)
	}
	if !strings.Contains(err.Error(), "unparseable") {
		t.Fatalf("error must pin the unparseable branch (opencode.go:50), got %v", err)
	}
	if !strings.Contains(err.Error(), usecase.OpencodeInstallFix) {
		t.Fatalf("refusal must embed usecase.OpencodeInstallFix verbatim, got %v", err)
	}
}

// The real default profile (the operator's) proves green — the kill-gate's command-resolution half.
func TestOpencodePluginProof_RealDefaultProfile(t *testing.T) {
	if err := OpencodePluginProof(nil); err != nil {
		t.Skipf("no default opencode profile with the plugin on this box: %v", err)
	}
}
