package usecase

import "github.com/drayanaindra/saki-cli/backend/domain"

// DoctorEngines is the FIXED ordered set of engines `saki doctor` reports on.
var DoctorEngines = []domain.RunEngine{domain.EngineCodex, domain.EngineOpencode, domain.EngineOMP, domain.EngineClaude}

// CodexInstallFix is codex's complete two-line remediation — the SAME text infra.CodexSkillsProof
// embeds in its spawn-refusal error (backend/infra/codex.go), so the operator-facing error and
// doctor's Fix field can never drift out of sync.
var CodexInstallFix = renderProvisionArgv(CodexProvisionArgv)

// OpencodeInstallFix is opencode's single-line remediation rendered from its installer mapping.
var OpencodeInstallFix = renderProvisionArgv(OpencodeProvisionArgv)

// OMPInstallFix is OMP's two-step marketplace remediation rendered from its installer mapping.
var OMPInstallFix = renderProvisionArgv(OMPProvisionArgv)

// ClaudeInstallFix is Claude's user-scope remediation rendered from the exact init-env argv.
var ClaudeInstallFix = renderProvisionArgv(ClaudeProvisionArgv)

// DoctorService computes a pre-dispatch provisioning verdict per DoctorEngines. It never spawns
// anything (rule 5) and never installs/writes/repairs (rule 1) — Check's only capability is the
// EngineProofs port, so both are structural guarantees, not merely observed behavior.
type DoctorService struct {
	proofs EngineProofs
}

func NewDoctorService(proofs EngineProofs) DoctorService {
	return DoctorService{proofs: proofs}
}

// Check probes every engine in DoctorEngines against configDir (nil = default profile). Per engine,
// BinaryCheck runs first and SHORT-CIRCUITS on error — ProfileProof is never called once BinaryCheck
// already failed, so the reported reason is never silently overwritten by a second call (mirrors
// preflight's own early-return, backend/infra/spawner.go).
func (s DoctorService) Check(configDir *string) []domain.EngineReport {
	profile := profileLabel(configDir)
	reports := make([]domain.EngineReport, 0, len(DoctorEngines))
	for _, engine := range DoctorEngines {
		reports = append(reports, s.checkOne(engine, profile, configDir))
	}
	return reports
}

func (s DoctorService) checkOne(engine domain.RunEngine, profile string, configDir *string) domain.EngineReport {
	report := domain.EngineReport{Engine: string(engine), Profile: profile, Status: domain.StatusOK}
	if err := s.proofs.BinaryCheck(engine); err != nil {
		report.Status, report.Reason = domain.StatusFailed, err.Error()
		return report
	}
	if err := s.proofs.ProfileProof(engine, configDir); err != nil {
		report.Status, report.Reason = domain.StatusFailed, err.Error()
		report.Fix = engineInstallFix(engine)
	}
	return report
}

func profileLabel(configDir *string) string {
	if configDir != nil && *configDir != "" {
		return *configDir
	}
	return "default"
}
