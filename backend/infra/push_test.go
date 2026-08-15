package infra

import (
	"os/exec"
	"testing"
)

// AC 5.3 — a repo with NO remote skips gracefully (never an error / lifecycle change).
func TestGitCLIPush_NoRemoteSkips(t *testing.T) {
	res := GitCLI{}.Push(gitRepo(t, "feat"))
	if res.OK || res.Skipped == "" {
		t.Fatalf("no-remote push must skip gracefully, got %+v", res)
	}
}

// AC 5.1 — with a real (bare) remote, the current branch is pushed (never --force), OK + branch set.
func TestGitCLIPush_HappyPath(t *testing.T) {
	dir := gitRepo(t, "feat")
	remote := t.TempDir()
	// a bare repo to push into, wired as origin
	if out, err := exec.Command("git", "init", "--bare", remote).CombinedOutput(); err != nil {
		t.Fatalf("init bare: %v (%s)", err, out)
	}
	gitIn(t, dir, "remote", "add", "origin", remote)

	res := GitCLI{}.Push(dir)
	if !res.OK || res.Branch != "feat" {
		t.Fatalf("happy-path push: got %+v", res)
	}
	// the branch really landed on the remote
	if out, err := exec.Command("git", "-C", remote, "branch", "--list", "feat").CombinedOutput(); err != nil || len(out) == 0 {
		t.Fatalf("branch not pushed to remote: %v (%s)", err, out)
	}
}
