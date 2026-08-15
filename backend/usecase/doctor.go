package usecase

import "github.com/drayanaindra/saki-cli/backend/domain"

// DoctorEngines is the FIXED, slice-1-scoped set of engines `saki doctor` reports on — NOT
// domain.RunEngine's full enum, which also has claude (deferred to F4). Order is the JSON order
// criterion 1.3 pins.
var DoctorEngines = []domain.RunEngine{domain.EngineCodex, domain.EngineOpencode}

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
	}
	return report
}

func profileLabel(configDir *string) string {
	if configDir != nil && *configDir != "" {
		return *configDir
	}
	return "default"
}
