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

// putBinariesOnPath writes a minimal executable `codex` AND `opencode` into one temp dir and sets
// PATH to it, so EngineBinaryCheck succeeds for both — a caller-review finding: a test that leaves
// the binary check failing means it short-circuits (checkOne, usecase/doctor.go) BEFORE reaching
// ProfileProof, so the file it means to prove read-only is never actually opened. This is what makes
// TestDoctorService_Check_LeavesFilesystemUntouched exercise the real read path, not skip past it.
func putBinariesOnPath(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\nexit 0\n"
	for _, name := range []string{"codex", "opencode"} {
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
		for _, engine := range []domain.RunEngine{domain.EngineCodex, domain.EngineOpencode} {
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
		for _, engine := range []domain.RunEngine{domain.EngineCodex, domain.EngineOpencode} {
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

	codexPath := filepath.Join(profileDir, "codex", "config.toml")
	opencodePath := filepath.Join(profileDir, "opencode", "opencode.json")
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
		if r.Status != "failed" || !strings.Contains(r.Reason, "does not resolve @saketek/saki-builder") {
			t.Fatalf("%s: want status=failed with a ProfileProof (not BinaryCheck) reason, got %+v", r.Engine, r)
		}
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

	entries, err := os.ReadDir(runsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("doctor run left %d entr(ies) in the run-journal-style dir, want 0: %v", len(entries), entries)
	}
}
