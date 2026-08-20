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

var claudePluginPrecedence = []string{
	"saketek@saki-builder",
	"saki-builder@saketek",
}

type claudePlugin struct {
	ID      string
	Version string
}

type claudeInstalledPlugins struct {
	Plugins map[string][]struct {
		Version string `json:"version"`
	} `json:"plugins"`
}

type claudeSettings struct {
	EnabledPlugins map[string]bool `json:"enabledPlugins"`
}

func ClaudeProfileProof(configDir *string) error {
	if _, err := resolveClaudeProfile(configDir); err != nil {
		return fmt.Errorf("%w: %w: %v", usecase.ErrEngineNotProvisioned, ErrClaudePluginMissing, err)
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
		return claudePlugin{ID: id, Version: records[0].Version}, nil
	}
	return claudePlugin{}, errors.New("no supported installed plugin")
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
