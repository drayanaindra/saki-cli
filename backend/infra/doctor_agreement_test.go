package infra

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/drayanaindra/saki-cli/backend/domain"
	"github.com/drayanaindra/saki-cli/backend/usecase"
)

// F2 slice 2, criterion 2.2 — the PRD's kill criterion (§6). preflight (spawner.go, the function a
// REAL spawn calls) and DoctorService.Check via the REAL EngineProofChecker (the function `saki
// doctor` calls) must agree on every fixture: both fail or both succeed. This is the end-to-end proof
// that rule 4 holds — TestEngineProofChecker_DelegatesToSharedFuncs (doctor_test.go) already proves the
// two call the same functions at the unit level; this proves it through preflight's own SpawnSpec entry
// point, which is what would actually catch a future edit that made the two diverge.
//
// Prompt is "hello" (non-slash) for every case — opencode's preflight also refuses an unresolvable
// slash-command SHAPE (spawner.go:160-171), which is not a provisioning check and doctor has no
// equivalent of; a slash prompt here would contaminate the comparison with an unrelated refusal.
func TestDoctorPreflightAgreement(t *testing.T) {
	cases := []struct {
		name   string
		engine domain.RunEngine
		wantOK bool
		// binaryAbsent skips putBinariesOnPath — the engine's binary is genuinely absent, so this
		// exercises the EngineBinaryCheck half of the shared functions rather than only ProfileProof.
		binaryAbsent bool
		fixture      func(t *testing.T, dir string)
	}{
		{
			name: "codex provisioned", engine: domain.EngineCodex, wantOK: true,
			fixture: func(t *testing.T, dir string) {
				writeCodexProfile(t, dir, "[plugins.\"saki-builder@saketek\"]\nenabled = true\n")
			},
		},
		{
			name: "codex unprovisioned", engine: domain.EngineCodex, wantOK: false,
			fixture: func(t *testing.T, dir string) {
				writeCodexProfile(t, dir, "")
			},
		},
		{
			name: "opencode provisioned", engine: domain.EngineOpencode, wantOK: true,
			fixture: func(t *testing.T, dir string) {
				writeOpencodeProfile(t, dir, `{"plugin":["@saketek/saki-builder"]}`)
			},
		},
		{
			name: "opencode unprovisioned", engine: domain.EngineOpencode, wantOK: false,
			fixture: func(t *testing.T, dir string) {
				writeOpencodeProfile(t, dir, `{"plugin":[]}`)
			},
		},
		{
			name: "omp provisioned", engine: domain.EngineOMP, wantOK: true,
			fixture: func(t *testing.T, dir string) {
				installPath := filepath.Join(dir, ".omp", "plugins", "cache", "saki-builder", "0.30.3")
				if err := os.MkdirAll(filepath.Join(installPath, "config", "skills"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(installPath, "config", "skills", "build.md"), []byte("name: build\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				registry := filepath.Join(dir, ".omp", "plugins", "installed_plugins.json")
				if err := os.MkdirAll(filepath.Dir(registry), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(registry, []byte(`{"plugins":{"saki-builder@saketek":[{"installPath":"`+installPath+`","version":"0.30.3"}]}}`), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "omp unprovisioned", engine: domain.EngineOMP, wantOK: false,
			fixture: func(t *testing.T, dir string) {
				registry := filepath.Join(dir, ".omp", "plugins", "installed_plugins.json")
				if err := os.MkdirAll(filepath.Dir(registry), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(registry, []byte(`{"plugins":{}}`), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			// Criterion 2.1's own case, given agreement coverage too: preflight and doctor call the
			name: "codex binary absent", engine: domain.EngineCodex, wantOK: false, binaryAbsent: true,
			fixture: func(t *testing.T, dir string) {}, // profile is irrelevant — BinaryCheck short-circuits first
		},
		{
			// claude had NO case here before this skill-file check existed — the exact function both
			// preflight and doctor now reach for claude changed shape, and this invariant test exists
			// precisely to catch the two diverging on it.
			name: "claude provisioned", engine: domain.EngineClaude, wantOK: true,
			fixture: func(t *testing.T, dir string) {
				installPath := filepath.Join(dir, "cache", "saketek", "saki-builder", "0.30.2")
				writeSkillFile(t, installPath, "build")
				writeClaudeProfile(t, dir,
					`{"plugins":{"saketek@saki-builder":[{"installPath":"`+installPath+`","version":"0.30.2"}]}}`,
					`{"enabledPlugins":{"saketek@saki-builder":true}}`,
				)
			},
		},
		{
			// Installed + enabled, but the skill file this plan's check added is missing — the exact
			// stale/partial-cache case this plan closes; must refuse on BOTH paths identically.
			name: "claude installed but skill missing", engine: domain.EngineClaude, wantOK: false,
			fixture: func(t *testing.T, dir string) {
				installPath := filepath.Join(dir, "cache", "saketek", "saki-builder", "0.30.2")
				writeClaudeProfile(t, dir,
					`{"plugins":{"saketek@saki-builder":[{"installPath":"`+installPath+`","version":"0.30.2"}]}}`,
					`{"enabledPlugins":{"saketek@saki-builder":true}}`,
				)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.binaryAbsent {
				t.Setenv("PATH", t.TempDir()) // no engine binaries at all
			} else {
				putBinariesOnPath(t)
			}
			dir := t.TempDir()
			tc.fixture(t, dir)

			preflightErr := preflight(usecase.SpawnSpec{
				ID: "agreement-check", Prompt: "hello", ConfigDir: &dir, Engine: tc.engine,
			})

			reports := usecase.NewDoctorService(EngineProofChecker{}).Check(&dir)
			var report domain.EngineReport
			found := false
			for _, r := range reports {
				if r.Engine == string(tc.engine) {
					report, found = r, true
				}
			}
			if !found {
				t.Fatalf("%s: doctor reported no entry for engine %q — reports=%+v", tc.name, tc.engine, reports)
			}

			preflightOK := preflightErr == nil
			doctorOK := report.Status == domain.StatusOK
			if preflightOK != doctorOK {
				t.Fatalf("%s: preflight ok=%v (err=%v) vs doctor ok=%v (report=%+v) — DISAGREE",
					tc.name, preflightOK, preflightErr, doctorOK, report)
			}
			if preflightOK != tc.wantOK {
				t.Fatalf("%s: preflight ok=%v, want %v (err=%v)", tc.name, preflightOK, tc.wantOK, preflightErr)
			}
		})
	}
}
