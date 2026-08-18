package infra

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/drayanaindra/saki-cli/backend/domain"
	"github.com/drayanaindra/saki-cli/backend/usecase"
)

const codexMarketplace = "https://github.com/drayanaindra/saki-builder.git"

// EngineProvisioner invokes only fixed argv vectors. User input selects an engine/profile env var;
// it never becomes shell text or an executable argument.
type EngineProvisioner struct{}

func (EngineProvisioner) Provision(req usecase.ProvisionRequest) (bool, error) {
	if err := EngineBinaryCheck(req.Engine); err != nil {
		return false, err
	}
	before := profileFingerprint(req.Engine, req.Profile)
	switch req.Engine {
	case domain.EngineCodex:
		if err := runProvision(req, "codex", "plugin", "marketplace", "add", codexMarketplace); err != nil {
			return false, err
		}
		if err := runProvision(req, "codex", "plugin", "add", "saki-builder@saketek"); err != nil {
			return false, err
		}
	case domain.EngineOpencode:
		if err := runProvision(req, "opencode", "plugin", "@saketek/saki-builder", "--global"); err != nil {
			return false, err
		}
		if err := runProvision(req, "npx", "@saketek/saki-builder", "install", "--global"); err != nil {
			return false, err
		}
	default:
		return false, usecase.ErrInitEnvUnsupported
	}
	after := profileFingerprint(req.Engine, req.Profile)
	return before != after, nil
}

func runProvision(req usecase.ProvisionRequest, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = req.Cwd
	cmd.Env = provisionEnv(req.Engine, req.Profile)
	out, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(out))
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("%s provisioning failed: %s", string(req.Engine), message)
	}
	return nil
}

func provisionEnv(engine domain.RunEngine, profile *string) []string {
	env := scrubProfileEnv(os.Environ(), engine, profile != nil && *profile != "")
	if profile == nil || *profile == "" {
		return env
	}
	switch engine {
	case domain.EngineCodex:
		env = append(env, "CODEX_HOME="+filepath.Join(*profile, "codex"))
	case domain.EngineOpencode:
		env = append(env, "XDG_CONFIG_HOME="+*profile)
	case domain.EngineClaude:
		env = append(env, "CLAUDE_CONFIG_DIR="+*profile)
	}
	return env
}

// profileFingerprint is intentionally narrow: it covers exactly the files each proof reads, while
// still detecting creation/removal and content changes without exposing profile contents in a response.
func profileFingerprint(engine domain.RunEngine, profile *string) string {
	var paths []string
	switch engine {
	case domain.EngineCodex:
		home := codexHomePath(profile)
		paths = []string{filepath.Join(home, "config.toml"), filepath.Join(home, "skills", codexProofSkill, "SKILL.md")}
	case domain.EngineOpencode:
		paths = []string{opencodeConfigPath(profile)}
	default:
		return ""
	}
	h := sha256.New()
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			_, _ = h.Write([]byte("missing:" + path + "\n"))
			continue
		}
		_, _ = h.Write([]byte(path + "\n"))
		_, _ = h.Write(data)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}
