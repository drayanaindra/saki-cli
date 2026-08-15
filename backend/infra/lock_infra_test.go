package infra

import (
	"path/filepath"
	"testing"
)

// TestContentFS_WriteFile: the slice-3 write port round-trips + overwrites on the real filesystem.
func TestContentFS_WriteFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "prd.md")
	fs := ContentFS{}
	if err := fs.WriteFile(p, "hello\nworld"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got, ok := fs.Read(p); !ok || got != "hello\nworld" {
		t.Fatalf("roundtrip: ok=%v got=%q", ok, got)
	}
	if err := fs.WriteFile(p, "again"); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if got, _ := fs.Read(p); got != "again" {
		t.Fatalf("overwrite got %q, want again", got)
	}
}

// TestGitCLI_UserName: a repo's LOCAL user.name resolves; a bad dir (git -C fails) → "" (the
// resolveApprover fallback). gitRepo sets a local user.name "t" that overrides any host global.
func TestGitCLI_UserName(t *testing.T) {
	repo := gitRepo(t)
	if got := (GitCLI{}).UserName(repo); got != "t" {
		t.Fatalf("UserName(repo) = %q, want t", got)
	}
	if got := (GitCLI{}).UserName(filepath.Join(t.TempDir(), "does-not-exist")); got != "" {
		t.Fatalf("UserName(bad dir) = %q, want empty", got)
	}
}
