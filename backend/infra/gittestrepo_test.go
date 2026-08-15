package infra

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitRepo builds a throwaway git repo in a temp dir with the given branches (the first is the
// initial branch; the rest are created off it), an initial commit, and a git identity set — so
// the switch/list tests run on a real repo with no dependency on the host's global git config.
func gitRepo(t *testing.T, branches ...string) string {
	t.Helper()
	if len(branches) == 0 {
		branches = []string{"main"}
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	git("init", "-b", branches[0])
	git("config", "user.email", "t@saki")
	git("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "f.txt")
	git("commit", "-m", "init")
	for _, b := range branches[1:] {
		git("branch", b)
	}
	return dir
}
