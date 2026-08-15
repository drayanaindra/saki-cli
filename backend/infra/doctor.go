package infra

import "github.com/drayanaindra/saki-cli/backend/domain"

// EngineProofChecker implements usecase.EngineProofs by delegating to EngineBinaryCheck/
// EngineProfileProof (spawner.go) — the SAME functions preflight calls, so `saki doctor`'s verdict
// and a spawn refusal can never disagree (rule 4). Zero-value struct; no state.
type EngineProofChecker struct{}

func (EngineProofChecker) BinaryCheck(engine domain.RunEngine) error {
	return EngineBinaryCheck(engine)
}

func (EngineProofChecker) ProfileProof(engine domain.RunEngine, configDir *string) error {
	return EngineProfileProof(engine, configDir)
}
