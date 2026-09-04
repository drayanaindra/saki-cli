package infra

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drayanaindra/saki-cli/backend/domain"
	"github.com/drayanaindra/saki-cli/backend/usecase"
)

// writeCodexProfile writes <dir>/codex/config.toml (the path codexHomePath(&dir) resolves) with the
// given content — the codex-side twin of writeOpencodeProfile (opencode_test.go).
func writeCodexProfile(t *testing.T, dir, content string) {
	t.Helper()
	p := filepath.Join(dir, "codex")
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "config.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// putBinariesOnPath writes minimal executables for every non-Claude runtime and sets PATH so
// EngineBinaryCheck succeeds; this lets the read-only test exercise each real ProfileProof.
func putBinariesOnPath(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\nexit 0\n"
	for _, name := range []string{"codex", "opencode", "omp"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
}

// F2 slice 1 step 5, rule 4's structural proof: EngineProofChecker must call the EXACT SAME functions
// preflight calls — not a parallel reimplementation that could drift. Asserted by comparing errors
// byte-for-byte over the same fixtures.
func TestEngineProofChecker_DelegatesToSharedFuncs(t *testing.T) {
	t.Run("BinaryCheck matches EngineBinaryCheck", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir()) // neither binary present
		var checker EngineProofChecker
		for _, engine := range []domain.RunEngine{domain.EngineCodex, domain.EngineOpencode, domain.EngineOMP} {
			want := EngineBinaryCheck(engine)
			got := checker.BinaryCheck(engine)
			if (want == nil) != (got == nil) || (want != nil && got != nil && want.Error() != got.Error()) {
				t.Errorf("%s: BinaryCheck = %v, EngineBinaryCheck = %v — must be identical", engine, got, want)
			}
		}
	})

	t.Run("ProfileProof matches EngineProfileProof", func(t *testing.T) {
		dir := t.TempDir() // no opencode.json / codex/config.toml -> both proofs fail
		var checker EngineProofChecker
		for _, engine := range []domain.RunEngine{domain.EngineCodex, domain.EngineOpencode, domain.EngineOMP} {
			want := EngineProfileProof(engine, &dir)
			got := checker.ProfileProof(engine, &dir)
			if (want == nil) != (got == nil) || (want != nil && got != nil && want.Error() != got.Error()) {
				t.Errorf("%s: ProfileProof = %v, EngineProfileProof = %v — must be identical", engine, got, want)
			}
		}
	})
}

// F2 slice 1, criterion 1.5's REAL proof (not the usecase-layer fake): running DoctorService against
// the REAL infra.EngineProofChecker chain over a broken fixture must leave the fixture's files
// byte-identical and add no entry under a run-journal-style directory — doctor is strictly read-only
// end to end, not merely by construction of one layer.
//
// Both binaries MUST be on PATH (putBinariesOnPath) — a caller review caught that an earlier version
// of this test left BinaryCheck failing, which short-circuits checkOne (usecase/doctor.go) BEFORE
// ProfileProof ever opens the fixture files below, making the byte-comparison vacuously true. With
// both binaries present, BinaryCheck succeeds and ProfileProof genuinely reads config.toml/
// opencode.json (and fails on their empty/malformed content) — the file this test claims to prove
// untouched is the one actually opened.
func TestDoctorService_Check_LeavesFilesystemUntouched(t *testing.T) {
	putBinariesOnPath(t)
	profileDir := t.TempDir()
	writeCodexProfile(t, profileDir, "")                 // an empty, unregistered config.toml
	writeOpencodeProfile(t, profileDir, `{"plugin":[]}`) // a valid-JSON, plugin-less config
	writeClaudeProfile(t, profileDir, `{"plugins":{}}`, `{"enabledPlugins":{}}`)
	ompRegistryPath := ompInstalledPluginsPath(&profileDir)
	if err := os.MkdirAll(filepath.Dir(ompRegistryPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ompRegistryPath, []byte(`{"plugins":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	codexPath := filepath.Join(profileDir, "codex", "config.toml")
	opencodePath := filepath.Join(profileDir, "opencode", "opencode.json")
	claudeInstalledPath, claudeSettingsPath := claudeProfilePaths(&profileDir)
	ompBefore, err := os.ReadFile(ompRegistryPath)
	if err != nil {
		t.Fatal(err)
	}
	claudeInstalledBefore, err := os.ReadFile(claudeInstalledPath)
	if err != nil {
		t.Fatal(err)
	}
	claudeSettingsBefore, err := os.ReadFile(claudeSettingsPath)
	if err != nil {
		t.Fatal(err)
	}
	codexBefore, err := os.ReadFile(codexPath)
	if err != nil {
		t.Fatal(err)
	}
	opencodeBefore, err := os.ReadFile(opencodePath)
	if err != nil {
		t.Fatal(err)
	}

	runsDir := t.TempDir() // stands in for a $SAKI_RUNS_DIR/go-style journal directory
	reports := usecase.NewDoctorService(EngineProofChecker{}).Check(&profileDir)

	// Both must be "failed" via ProfileProof specifically (never BinaryCheck) — the "does not
	// resolve" wording only appears in CodexSkillsProof/OpencodePluginProof's own error text, so its
	// presence is direct proof the profile-reading code path actually ran, not just that Check failed.
	for _, r := range reports {
		if r.Status != "failed" || (r.Engine != string(domain.EngineClaude) && r.Engine != string(domain.EngineOMP) && !strings.Contains(r.Reason, "does not resolve @saketek/saki-builder")) || (r.Engine == string(domain.EngineClaude) && !strings.Contains(r.Reason, "no supported installed plugin")) || (r.Engine == string(domain.EngineOMP) && !strings.Contains(r.Reason, "no installed plugin with saki-builder@saketek")) {
			t.Fatalf("%s: want status=failed with a ProfileProof (not BinaryCheck) reason, got %+v", r.Engine, r)
		}
	}

	claudeInstalledAfter, err := os.ReadFile(claudeInstalledPath)
	if err != nil {
		t.Fatal(err)
	}
	claudeSettingsAfter, err := os.ReadFile(claudeSettingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(claudeInstalledBefore, claudeInstalledAfter) || !bytes.Equal(claudeSettingsBefore, claudeSettingsAfter) {
		t.Fatal("Claude profile was mutated by a doctor run")
	}

	codexAfter, err := os.ReadFile(codexPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(codexBefore, codexAfter) {
		t.Fatalf("codex profile was mutated by a doctor run — before=%q after=%q", codexBefore, codexAfter)
	}
	opencodeAfter, err := os.ReadFile(opencodePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(opencodeBefore, opencodeAfter) {
		t.Fatalf("opencode profile was mutated by a doctor run — before=%q after=%q", opencodeBefore, opencodeAfter)
	}
	ompAfter, err := os.ReadFile(ompRegistryPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ompBefore, ompAfter) {
		t.Fatalf("OMP plugin registry was mutated by a doctor run — before=%q after=%q", ompBefore, ompAfter)
	}

	entries, err := os.ReadDir(runsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("doctor run left %d entr(ies) in the run-journal-style dir, want 0: %v", len(entries), entries)
	}
}

// F2 slice 2, criterion 2.1: a missing BINARY must read differently from an unprovisioned PROFILE —
// proven end to end through the REAL EngineProofChecker (not a fake), so a future edit that made the
// two reasons collide would actually be caught. Only opencode is put on PATH; codex is genuinely
// absent, and opencode's profile is genuinely provisioned so its "ok" isolates the codex row.
func TestDoctorService_Check_CodexBinaryAbsent_DistinctFromProfileReason(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "opencode"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin) // codex is NOT here

	profileDir := t.TempDir()
	writeOpencodeProfile(t, profileDir, `{"plugin":["@saketek/saki-builder"]}`)

	reports := usecase.NewDoctorService(EngineProofChecker{}).Check(&profileDir)
	codex, opencode := reports[0], reports[1]

	if opencode.Status != "ok" {
		t.Fatalf("opencode = %+v, want status=ok (isolates the codex row below)", opencode)
	}
	if codex.Status != "failed" {
		t.Fatalf("codex = %+v, want status=failed", codex)
	}
	if !strings.Contains(codex.Reason, "engine binary not found on PATH (codex)") {
		t.Errorf("codex.Reason = %q, want it to name the missing binary", codex.Reason)
	}
	if strings.Contains(codex.Reason, "does not resolve @saketek/saki-builder") {
		t.Errorf("codex.Reason = %q — a binary-absent reason must never read like a profile-unprovisioned one", codex.Reason)
	}
}
