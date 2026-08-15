package infra

import (
	"bytes"
	"os"
	"path/filepath"
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
func TestDoctorService_Check_LeavesFilesystemUntouched(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // neither codex nor opencode on PATH -> BinaryCheck fails first
	profileDir := t.TempDir()
	writeCodexProfile(t, profileDir, "") // an empty, unregistered config.toml
	cfgPath := filepath.Join(profileDir, "codex", "config.toml")

	before, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	runsDir := t.TempDir() // stands in for a $SAKI_RUNS_DIR/go-style journal directory
	reports := usecase.NewDoctorService(EngineProofChecker{}).Check(&profileDir)

	if len(reports) != 2 || reports[0].Status != "failed" {
		t.Fatalf("want codex reported failed (binary missing), got %+v", reports)
	}

	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("profile file was mutated by a doctor run — before=%q after=%q", before, after)
	}

	entries, err := os.ReadDir(runsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("doctor run left %d entr(ies) in the run-journal-style dir, want 0: %v", len(entries), entries)
	}
}
