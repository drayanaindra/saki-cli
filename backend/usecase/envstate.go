package usecase

import "github.com/drayanaindra/saki-cli/backend/domain"

// EnvStateFS is the read-only filesystem surface the env-state usecase needs (implemented by
// infra.OSEnvStateFS). ReadMarker returns nil when `.claude/.env-init.json` is absent or unparseable.
type EnvStateFS interface {
	Home() string
	ReadMarker(cwd string) *domain.EnvMarker
	HasClaudeDir(cwd string) bool
	HasClaudeMd(cwd string) bool
}

// EnvStateService classifies a repo's Claude env readiness (parity index.ts:879 → envState.ts). Pure
// orchestration over EnvStateFS; the classification decision lives in domain.ClassifyEnvState.
type EnvStateService struct {
	fs EnvStateFS
}

// NewEnvStateService wires a read-only EnvStateFS into the service.
func NewEnvStateService(fs EnvStateFS) EnvStateService {
	return EnvStateService{fs: fs}
}

// Classify returns none/foreign/mine for the repo at cwd.
func (s EnvStateService) Classify(cwd string) domain.EnvState {
	return domain.ClassifyEnvState(s.fs.ReadMarker(cwd), s.fs.Home(), s.fs.HasClaudeDir(cwd), s.fs.HasClaudeMd(cwd))
}
