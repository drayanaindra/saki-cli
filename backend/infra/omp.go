package infra

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/drayanaindra/saki-cli/backend/usecase"
)

// ErrOMPPluginMissing is returned when OMP cannot resolve the installed saki-builder marketplace
// plugin from the profile used by a spawn. The proof reads OMP's own installed-plugin registry and
// the plugin's declared build skill; it never infers readiness from a model run exit code.
var ErrOMPPluginMissing = errors.New("omp profile does not resolve saki-builder")

const ompPluginID = "saki-builder@saketek"

// ompInstalledPlugins is the registry written by `omp plugin install`. The registry is intentionally
// decoded into only the fields needed by the proof, keeping the adapter independent of OMP internals.
type ompInstalledPlugins struct {
	Plugins map[string][]struct {
		Version     string `json:"version"`
		InstallPath string `json:"installPath"`
	} `json:"plugins"`
}

// OMPPluginProof proves that the OMP profile a future spawn will use has the saki-builder plugin and
// its load-bearing build skill. OMP stores marketplace plugins below <HOME>/.omp/plugins; a pinned
// saki profile maps HOME to that profile, while an unpinned run uses the operator's real HOME.
func OMPPluginProof(configDir *string) error {
	registryPath := ompInstalledPluginsPath(configDir)
	raw, err := os.ReadFile(registryPath)
	if err != nil {
		return fmt.Errorf("%w: %w: registry %s unreadable: %v — run:\n%s", usecase.ErrEngineNotProvisioned, ErrOMPPluginMissing, registryPath, err, usecase.OMPInstallFix)
	}
	var registry ompInstalledPlugins
	if err := json.Unmarshal(raw, &registry); err != nil {
		return fmt.Errorf("%w: %w: registry %s unparseable: %v — run:\n%s", usecase.ErrEngineNotProvisioned, ErrOMPPluginMissing, registryPath, err, usecase.OMPInstallFix)
	}
	entries := registry.Plugins[ompPluginID]
	for _, entry := range entries {
		if entry.Version == "" || entry.InstallPath == "" {
			continue
		}
		for _, skill := range []string{
			filepath.Join(entry.InstallPath, "config", "skills", sentinelProofSkill+".md"),
			filepath.Join(entry.InstallPath, "skills", sentinelProofSkill, "SKILL.md"),
		} {
			if _, err := os.Stat(skill); err == nil {
				return nil
			}
		}
	}
	return fmt.Errorf("%w: %w: %s has no installed plugin with %s skill — run:\n%s", usecase.ErrEngineNotProvisioned, ErrOMPPluginMissing, registryPath, ompPluginID, usecase.OMPInstallFix)
}

// ompHomePath resolves the HOME the OMP child uses. A pinned saki profile is a complete HOME root;
// this keeps OMP's auth/session/settings/cache namespace together and prevents writes to the user's
// normal ~/.omp tree. Unpinned runs deliberately use the process's normal HOME.
func ompHomePath(configDir *string) string {
	if configDir != nil && *configDir != "" {
		return filepath.Clean(*configDir)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return home
}

func ompInstalledPluginsPath(configDir *string) string {
	return filepath.Join(ompHomePath(configDir), ".omp", "plugins", "installed_plugins.json")
}

// ompProfilePaths returns the registry and candidate skill files used by the fingerprint. Missing
// skill candidates are included deliberately: creation/removal of either supported plugin layout
// changes the fingerprint even when OMP's registry metadata stays byte-identical.
func ompProfilePaths(configDir *string) []string {
	paths := []string{ompInstalledPluginsPath(configDir)}
	raw, err := os.ReadFile(paths[0])
	if err != nil {
		return paths
	}
	var registry ompInstalledPlugins
	if err := json.Unmarshal(raw, &registry); err != nil {
		return paths
	}
	for _, entry := range registry.Plugins[ompPluginID] {
		if entry.InstallPath == "" {
			continue
		}
		paths = append(paths,
			filepath.Join(entry.InstallPath, "config", "skills", sentinelProofSkill+".md"),
			filepath.Join(entry.InstallPath, "skills", sentinelProofSkill, "SKILL.md"),
		)
	}
	return paths
}
