package infra

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

// OSEnvStateFS implements usecase.EnvStateFS over the real filesystem. Read-only.
type OSEnvStateFS struct{}

// Home returns the operator's home dir (parity with Node os.homedir()); "" if unresolvable, which the
// classifier treats as never-matching a marker config.
func (OSEnvStateFS) Home() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

// ReadMarker reads + parses `.claude/.env-init.json` under cwd; nil when absent or unparseable
// (parity envState.ts:19 readEnvMarker — a try/catch returning null). Only `config` is load-bearing
// for classification, so it is unmarshaled ALONE into a minimal struct: this matches the TS loose
// cast, which type-checks nothing but `config === home`, and avoids a Go-only divergence where a
// type-drifted sibling field (e.g. `version` written as a string) would fail the whole parse and
// mis-classify a config-matching marker as `foreign`.
func (OSEnvStateFS) ReadMarker(cwd string) *domain.EnvMarker {
	raw, err := os.ReadFile(filepath.Join(cwd, ".claude", ".env-init.json"))
	if err != nil {
		return nil
	}
	var m struct {
		Config string `json:"config"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return &domain.EnvMarker{Config: m.Config}
}

// HasClaudeDir reports whether cwd/.claude exists (any type — parity existsSync).
func (OSEnvStateFS) HasClaudeDir(cwd string) bool { return exists(filepath.Join(cwd, ".claude")) }

// HasClaudeMd reports whether cwd/CLAUDE.md exists.
func (OSEnvStateFS) HasClaudeMd(cwd string) bool { return exists(filepath.Join(cwd, "CLAUDE.md")) }

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
