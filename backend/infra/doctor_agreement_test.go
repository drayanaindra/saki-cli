package infra

import (
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
		name    string
		engine  domain.RunEngine
		fixture func(t *testing.T, dir string)
	}{
		{
			name:   "codex provisioned",
			engine: domain.EngineCodex,
			fixture: func(t *testing.T, dir string) {
				writeCodexProfile(t, dir, "[plugins.\"saki-builder@saketek\"]\nenabled = true\n")
			},
		},
		{
			name:   "codex unprovisioned",
			engine: domain.EngineCodex,
			fixture: func(t *testing.T, dir string) {
				writeCodexProfile(t, dir, "")
			},
		},
		{
			name:   "opencode provisioned",
			engine: domain.EngineOpencode,
			fixture: func(t *testing.T, dir string) {
				writeOpencodeProfile(t, dir, `{"plugin":["@saketek/saki-builder"]}`)
			},
		},
		{
			name:   "opencode unprovisioned",
			engine: domain.EngineOpencode,
			fixture: func(t *testing.T, dir string) {
				writeOpencodeProfile(t, dir, `{"plugin":[]}`)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			putBinariesOnPath(t)
			dir := t.TempDir()
			tc.fixture(t, dir)

			preflightErr := preflight(usecase.SpawnSpec{
				ID: "agreement-check", Prompt: "hello", ConfigDir: &dir, Engine: tc.engine,
			})

			reports := usecase.NewDoctorService(EngineProofChecker{}).Check(&dir)
			var report domain.EngineReport
			for _, r := range reports {
				if r.Engine == string(tc.engine) {
					report = r
				}
			}

			preflightOK := preflightErr == nil
			doctorOK := report.Status == domain.StatusOK
			if preflightOK != doctorOK {
				t.Fatalf("%s: preflight ok=%v (err=%v) vs doctor ok=%v (report=%+v) — DISAGREE",
					tc.name, preflightOK, preflightErr, doctorOK, report)
			}
		})
	}
}
