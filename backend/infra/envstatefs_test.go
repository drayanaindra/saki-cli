package infra

import (
	"os"
	"path/filepath"
	"testing"
)

// writeMarker writes cwd/.claude/.env-init.json with the given raw JSON body.
func writeMarker(t *testing.T, cwd, body string) {
	t.Helper()
	dir := filepath.Join(cwd, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env-init.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadMarker_absentIsNil(t *testing.T) {
	if m := (OSEnvStateFS{}).ReadMarker(t.TempDir()); m != nil {
		t.Fatalf("absent marker must be nil, got %+v", m)
	}
}

func TestReadMarker_readsConfig(t *testing.T) {
	cwd := t.TempDir()
	writeMarker(t, cwd, `{"tool":"init-env","config":"/home/me","version":3}`)
	m := (OSEnvStateFS{}).ReadMarker(cwd)
	if m == nil || m.Config != "/home/me" {
		t.Fatalf("want config=/home/me, got %+v", m)
	}
}

// A sibling field written with a drifted type (version as a string) must NOT fail the parse — only
// `config` is load-bearing (parity with the TS loose cast). This is the reviewer-flagged divergence.
func TestReadMarker_tolerantOfSiblingTypeDrift(t *testing.T) {
	cwd := t.TempDir()
	writeMarker(t, cwd, `{"config":"/home/me","version":"3","createdAt":42}`)
	m := (OSEnvStateFS{}).ReadMarker(cwd)
	if m == nil || m.Config != "/home/me" {
		t.Fatalf("type-drifted sibling fields must not break config read, got %+v", m)
	}
}

func TestReadMarker_malformedIsNil(t *testing.T) {
	cwd := t.TempDir()
	writeMarker(t, cwd, `not json`)
	if m := (OSEnvStateFS{}).ReadMarker(cwd); m != nil {
		t.Fatalf("malformed marker must be nil, got %+v", m)
	}
}

func TestHasClaudeDirAndMd(t *testing.T) {
	cwd := t.TempDir()
	fs := OSEnvStateFS{}
	if fs.HasClaudeDir(cwd) || fs.HasClaudeMd(cwd) {
		t.Fatal("empty dir must have neither .claude nor CLAUDE.md")
	}
	if err := os.MkdirAll(filepath.Join(cwd, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "CLAUDE.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !fs.HasClaudeDir(cwd) || !fs.HasClaudeMd(cwd) {
		t.Fatal("both .claude and CLAUDE.md should be detected")
	}
}
