package infra

import (
	"os/exec"
	"strings"
	"testing"
)

// TestGitCLI_SliceCommit: a commit whose message carries `slice-<n>` resolves via git log --grep;
// a non-matching n → "". Mirrors the /api/slice-meta git call.
func TestGitCLI_SliceCommit(t *testing.T) {
	repo := gitRepo(t) // has an "init" commit + a local git identity
	commitMsg := "feat: slice-2 board render on Go"
	cmd := exec.Command("git", "commit", "--allow-empty", "-m", commitMsg)
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("seed commit: %v (%s)", err, out)
	}

	got := (GitCLI{}).SliceCommit(repo, "2")
	if got == "" || !strings.Contains(got, commitMsg) {
		t.Fatalf("SliceCommit(2) = %q, want a line containing %q", got, commitMsg)
	}
	if got := (GitCLI{}).SliceCommit(repo, "99"); got != "" {
		t.Fatalf("SliceCommit(99) = %q, want empty (no match)", got)
	}
	if got := (GitCLI{}).SliceCommit(t.TempDir(), "2"); got != "" {
		t.Fatalf("SliceCommit(non-repo) = %q, want empty", got)
	}
}
