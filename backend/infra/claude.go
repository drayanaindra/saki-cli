package infra

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/drayanaindra/saki-cli/backend/usecase"
)

var ErrClaudePluginMissing = errors.New("claude profile does not resolve saki-builder")

// ErrClaudeSkillMissing is returned by ClaudeProfileProof when the plugin is installed and enabled
// but its installPath does not carry the sentinel skill file — a stale/partial plugin cache (e.g. an
// interrupted update) that the plugin-level check alone cannot see. Parity with codex's existing
// loose-install sentinel check (codexProofSkill, codex.go) — claude previously did no file-level
// verification at all.
var ErrClaudeSkillMissing = errors.New("claude profile does not carry the saki-builder skills")

var claudePluginPrecedence = []string{
	"saketek@saki-builder",
	"saki-builder@saketek",
}

type claudePlugin struct {
	ID          string
	Version     string
	InstallPath string
}

type claudeInstalledPlugins struct {
	Plugins map[string][]struct {
		Version     string `json:"version"`
		InstallPath string `json:"installPath"`
	} `json:"plugins"`
}

type claudeSettings struct {
	EnabledPlugins map[string]bool `json:"enabledPlugins"`
}

func ClaudeProfileProof(configDir *string) error {
	plugin, err := resolveClaudeProfile(configDir)
	if err != nil {
		return fmt.Errorf("%w: %w: %v", usecase.ErrEngineNotProvisioned, ErrClaudePluginMissing, err)
	}
	skillPath := filepath.Join(plugin.InstallPath, "skills", codexProofSkill, "SKILL.md")
	if _, err := os.Stat(skillPath); err != nil {
		return fmt.Errorf("%w: %w: %s", usecase.ErrEngineNotProvisioned, ErrClaudeSkillMissing, skillPath)
	}
	return nil
}

func resolveClaudeProfile(configDir *string) (claudePlugin, error) {
	installedPath, settingsPath := claudeProfilePaths(configDir)
	installedRaw, err := os.ReadFile(installedPath)
	if err != nil {
		return claudePlugin{}, fmt.Errorf("installed plugins file %s unreadable: %w", installedPath, err)
	}
	settingsRaw, err := os.ReadFile(settingsPath)
	if err != nil {
		return claudePlugin{}, fmt.Errorf("settings file %s unreadable: %w", settingsPath, err)
	}

	var installed claudeInstalledPlugins
	if err := json.Unmarshal(installedRaw, &installed); err != nil {
		return claudePlugin{}, fmt.Errorf("installed plugins file %s unparseable: %w", installedPath, err)
	}
	var settings claudeSettings
	if err := json.Unmarshal(settingsRaw, &settings); err != nil {
		return claudePlugin{}, fmt.Errorf("settings file %s unparseable: %w", settingsPath, err)
	}

	for _, id := range claudePluginPrecedence {
		records, ok := installed.Plugins[id]
		if !ok || len(records) == 0 {
			continue
		}
		if records[0].Version == "" {
			return claudePlugin{}, fmt.Errorf("installed plugin %s has no version", id)
		}
		if enabled, ok := settings.EnabledPlugins[id]; !ok || !enabled {
			return claudePlugin{}, fmt.Errorf("installed plugin %s is not enabled", id)
		}
		return claudePlugin{ID: id, Version: records[0].Version, InstallPath: records[0].InstallPath}, nil
	}
	return claudePlugin{}, errors.New("no supported installed plugin")
}

func claudeHomePath(configDir *string) string {
	installed, _ := claudeProfilePaths(configDir)
	return filepath.Dir(filepath.Dir(installed))
}

func claudeProfilePaths(configDir *string) (string, string) {
	base := ""
	if configDir != nil && *configDir != "" {
		base = *configDir
	} else if home, err := os.UserHomeDir(); err == nil {
		base = filepath.Join(home, ".claude")
	}
	return filepath.Join(base, "plugins", "installed_plugins.json"), filepath.Join(base, "settings.json")
}
