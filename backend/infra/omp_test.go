package infra

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drayanaindra/saki-cli/backend/domain"
	"github.com/drayanaindra/saki-cli/backend/usecase"
)

func ompProvenProfile(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	installPath := filepath.Join(home, ".omp", "plugins", "cache", "saki-builder")
	if err := os.MkdirAll(filepath.Join(installPath, "config", "skills"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installPath, "config", "skills", sentinelProofSkill+".md"), []byte("name: build\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	registryPath := ompInstalledPluginsPath(&home)
	if err := os.MkdirAll(filepath.Dir(registryPath), 0o700); err != nil {
		t.Fatal(err)
	}
	registry := `{"plugins":{"saki-builder@saketek":[{"installPath":"` + installPath + `","version":"0.30.3"}]}}`
	if err := os.WriteFile(registryPath, []byte(registry), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestOMPPluginProof_RequiresInstalledBuildSkill(t *testing.T) {
	home := ompProvenProfile(t)
	if err := OMPPluginProof(&home); err != nil {
		t.Fatalf("proven OMP profile must pass: %v", err)
	}

	registryPath := ompInstalledPluginsPath(&home)
	if err := os.WriteFile(registryPath, []byte(`{"plugins":{"saki-builder@saketek":[{"installPath":"/missing","version":"0.30.3"}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := OMPPluginProof(&home); !errors.Is(err, ErrOMPPluginMissing) || !errors.Is(err, usecase.ErrEngineNotProvisioned) {
		t.Fatalf("missing OMP skill must return both proof errors, got %v", err)
	}
}

func TestOMPProfileFingerprintTracksSkillContent(t *testing.T) {
	home := ompProvenProfile(t)
	base := profileFingerprint(domain.EngineOMP, &home)
	skill := filepath.Join(home, ".omp", "plugins", "cache", "saki-builder", "config", "skills", "build.md")
	if err := os.WriteFile(skill, []byte("name: build\nversion: changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := profileFingerprint(domain.EngineOMP, &home); got == base {
		t.Fatal("OMP skill content change must change the profile fingerprint")
	}
}

func TestBuildRunScript_OMPCommand(t *testing.T) {
	s := buildRunScript("init", domain.EngineOMP, true)
	want := `omp --print --mode json --no-session --no-pty --auto-approve -- "$SAKI_PROMPT"`
	if !strings.Contains(s, want) {
		t.Fatalf("OMP script must use print JSON mode with detached-run flags: %s", s)
	}
	if strings.Contains(s, "claude") || strings.Contains(s, "--dangerously-skip-permissions") || strings.Contains(s, "SAKI_CMD") {
		t.Fatalf("OMP script must not inherit Claude elevation or OpenCode command handling: %s", s)
	}
}

func TestBuildSpawnEnv_OMPIsolatesProfileAndSelectors(t *testing.T) {
	t.Setenv("HOME", "/operator-home")
	t.Setenv("OMP_PROFILE", "operator")
	t.Setenv("PI_PROFILE", "operator")
	t.Setenv("PI_CONFIG_DIR", "/operator-config")
	t.Setenv("PI_CODING_AGENT_DIR", "/operator-agent")
	dir := "/profiles/omp-p"
	env := buildSpawnEnv(usecase.SpawnSpec{ID: "omp-env", Prompt: "prompt", Engine: domain.EngineOMP, ConfigDir: &dir}, NewFileJournal(t.TempDir()))
	valueOf := func(key string) (string, bool) {
		var value string
		found := false
		for _, item := range env {
			if strings.HasPrefix(item, key+"=") {
				value, found = strings.TrimPrefix(item, key+"="), true
			}
		}
		return value, found
	}
	if got, _ := valueOf("HOME"); got != dir {
		t.Fatalf("pinned OMP must redirect HOME to profile, got %q", got)
	}
	for _, key := range []string{"OMP_PROFILE", "PI_PROFILE", "PI_CONFIG_DIR", "PI_CODING_AGENT_DIR"} {
		if _, found := valueOf(key); found {
			t.Fatalf("pinned OMP must scrub inherited %s", key)
		}
	}
}

func TestEngineBinaryCheck_OMP(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if err := EngineBinaryCheck(domain.EngineOMP); !errors.Is(err, usecase.ErrBinaryNotFound) {
		t.Fatalf("missing omp binary must be reported, got %v", err)
	}
}
