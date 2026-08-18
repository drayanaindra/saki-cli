package usecase

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

var ErrInitEnvUnsupported = errors.New("engine provisioning is not verified for this engine")

type ProvisionRequest struct {
	Cwd     string
	Engine  domain.RunEngine
	Profile *string
}

type EngineProvisioner interface {
	Provision(ProvisionRequest) (changed bool, err error)
}

type InitEnvService struct {
	provisioner EngineProvisioner
	proofs      EngineProofs
}

func NewInitEnvService(provisioner EngineProvisioner, proofs EngineProofs) InitEnvService {
	return InitEnvService{provisioner: provisioner, proofs: proofs}
}

// Provision mutates only the selected engine profile, then proves the exact profile used by a spawn.
// A failed setup is returned as a structured result so the CLI can preserve its normal error/JSON
// contract; malformed requests use 422 before any adapter or external command is reached.
func (s InitEnvService) Provision(req ProvisionRequest) (int, map[string]any) {
	if req.Cwd == "" || !filepath.IsAbs(req.Cwd) {
		return 422, map[string]any{"error": "cwd must be an absolute path"}
	}
	if info, err := os.Stat(req.Cwd); err != nil || !info.IsDir() {
		return 422, map[string]any{"error": "cwd must be an existing directory"}
	}
	if req.Engine != domain.EngineCodex && req.Engine != domain.EngineOpencode && req.Engine != domain.EngineClaude {
		return 422, map[string]any{"error": "engine must be one of claude, codex, opencode"}
	}
	profile := "default"
	if req.Profile != nil && *req.Profile != "" {
		if !filepath.IsAbs(*req.Profile) {
			return 422, map[string]any{"error": "profile must be an absolute path"}
		}
		profile = filepath.Clean(*req.Profile)
	}
	base := map[string]any{"engine": string(req.Engine), "profile": profile, "changed": false, "status": "failed", "reason": "", "fix": ""}
	if req.Engine == domain.EngineClaude {
		base["reason"] = ErrInitEnvUnsupported.Error() + " (claude requires F4 doctor coverage)"
		return 200, base
	}
	changed, err := s.provisioner.Provision(req)
	base["changed"] = changed
	if err != nil {
		base["reason"] = err.Error()
		if req.Engine == domain.EngineCodex {
			base["fix"] = CodexInstallFix
		}
		return 200, base
	}
	if err := s.proofs.BinaryCheck(req.Engine); err != nil {
		base["reason"] = err.Error()
		return 200, base
	}
	if err := s.proofs.ProfileProof(req.Engine, req.Profile); err != nil {
		base["reason"] = err.Error()
		if req.Engine == domain.EngineCodex {
			base["fix"] = CodexInstallFix
		}
		return 200, base
	}
	base["status"] = "ok"
	return 200, base
}
