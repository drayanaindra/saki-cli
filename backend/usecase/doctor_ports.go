package usecase

import "github.com/drayanaindra/saki-cli/backend/domain"

// EngineProofs is how DoctorService checks whether an engine's binary + profile can resolve
// saki-builder — the SAME check preflight uses (rule 4, backend/infra/spawner.go's
// EngineBinaryCheck/EngineProfileProof), so a doctor verdict and a spawn refusal can never disagree.
// Implemented by infra.EngineProofChecker.
type EngineProofs interface {
	// BinaryCheck reports an ErrBinaryNotFound-wrapped error, or nil.
	BinaryCheck(engine domain.RunEngine) error
	// ProfileProof reports an ErrEngineNotProvisioned-wrapped error, or nil.
	ProfileProof(engine domain.RunEngine, configDir *string) error
}
