package infra

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gitOut runs a git command in dir and returns its trimmed stdout.
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

func fileExists(dir, rel string) bool {
	_, err := os.Stat(filepath.Join(dir, rel))
	return err == nil
}

// stubGlab writes a throwaway `glab` shell script (chmod +x) and returns its path, so the create-MR
// tests exercise glab's success / nonzero-exit / blocking behavior without a real glab or remote.
func stubGlab(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "glab")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// gitIn runs a git command inside dir, failing the test on error.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v (%s)", args, err, out)
	}
}

func TestGitCLI_CurrentBranch(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	run("init", "-b", "trunk")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	run("commit", "--allow-empty", "-m", "init")

	got, err := GitCLI{}.CurrentBranch(dir)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got == nil || *got != "trunk" {
		t.Fatalf("want trunk, got %v", got)
	}

	// A non-repo dir returns nil (no branch), not an error — parity with {branch:null}.
	if b, err := (GitCLI{}).CurrentBranch(t.TempDir()); err != nil || b != nil {
		t.Fatalf("non-repo: want nil,nil got %v err %v", b, err)
	}
}

func TestGitCLI_ListBranches(t *testing.T) {
	dir := gitRepo(t, "main", "feature/x", "dev")
	got, err := GitCLI{}.ListBranches(dir)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := map[string]bool{"main": true, "feature/x": true, "dev": true}
	if len(got) != 3 {
		t.Fatalf("want 3 branches, got %v", got)
	}
	for _, b := range got {
		if !want[b] {
			t.Fatalf("unexpected branch %q in %v", b, got)
		}
	}
	// non-repo dir → [] (parity index.ts:410-411), never an error.
	if b, err := (GitCLI{}).ListBranches(t.TempDir()); err != nil || len(b) != 0 {
		t.Fatalf("non-repo: want [],nil got %v err %v", b, err)
	}
}

func TestGitCLI_Switch_happy(t *testing.T) {
	dir := gitRepo(t, "main", "feature/x")
	ok, msg := GitCLI{}.Switch(dir, "feature/x", false)
	if !ok || msg != "" {
		t.Fatalf("want ok, got ok=%v msg=%q", ok, msg)
	}
	if cur, _ := (GitCLI{}).CurrentBranch(dir); cur == nil || *cur != "feature/x" {
		t.Fatalf("want on feature/x, got %v", cur)
	}
}

// AC 1.4 / BR1: a dirty tree whose change conflicts with the target makes `git switch` fail
// loudly (ok=false + git's stderr) and the working-tree change is left intact (no work lost).
func TestGitCLI_Switch_dirtyLoudFail(t *testing.T) {
	dir := gitRepo(t, "main", "feature/x")
	// feature/x commits f.txt = "on-feature"
	gitIn(t, dir, "switch", "feature/x")
	writeFile(t, dir, "f.txt", "on-feature\n")
	gitIn(t, dir, "commit", "-am", "feature change")
	gitIn(t, dir, "switch", "main")
	// dirty, uncommitted local change on main that would be overwritten by switching to feature/x
	writeFile(t, dir, "f.txt", "dirty-local\n")

	ok, msg := GitCLI{}.Switch(dir, "feature/x", false)
	if ok {
		t.Fatal("want switch to FAIL on a conflicting dirty tree, got ok")
	}
	if msg == "" {
		t.Fatal("want git's stderr message on the failed switch, got empty")
	}
	// the local change is intact — no work lost, and still on main.
	if got := readFile(t, dir, "f.txt"); got != "dirty-local\n" {
		t.Fatalf("dirty change not intact: %q", got)
	}
	if cur, _ := (GitCLI{}).CurrentBranch(dir); cur == nil || *cur != "main" {
		t.Fatalf("want still on main, got %v", cur)
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestGitCLI_CreateMR_success(t *testing.T) {
	glab := stubGlab(t, `echo "View this merge request: https://gitlab.com/x/y/-/merge_requests/7"`)
	url, msg := GitCLI{GlabBin: glab}.CreateMR(t.TempDir(), "feat", "main")
	if msg != "" || url != "https://gitlab.com/x/y/-/merge_requests/7" {
		t.Fatalf("url=%q msg=%q", url, msg)
	}
}

func TestGitCLI_CreateMR_nonzeroExit(t *testing.T) {
	glab := stubGlab(t, `echo "glab: not authenticated" >&2; exit 1`)
	url, msg := GitCLI{GlabBin: glab}.CreateMR(t.TempDir(), "feat", "main")
	if url != "" || msg != "glab: not authenticated" {
		t.Fatalf("url=%q msg=%q", url, msg)
	}
}

// BR6: a blocking glab is bounded by MRTimeout — it returns an errMsg quickly, never hangs.
func TestGitCLI_CreateMR_blockingBounded(t *testing.T) {
	glab := stubGlab(t, `sleep 30`)
	start := time.Now()
	url, msg := GitCLI{GlabBin: glab, MRTimeout: 100 * time.Millisecond}.CreateMR(t.TempDir(), "feat", "main")
	if time.Since(start) > 5*time.Second {
		t.Fatal("CreateMR did not respect MRTimeout")
	}
	if url != "" || msg == "" {
		t.Fatalf("want bounded failure, url=%q msg=%q", url, msg)
	}
}

func TestGitCLI_Merge_happy(t *testing.T) {
	dir := gitRepo(t, "main", "feat")
	gitIn(t, dir, "switch", "feat")
	writeFile(t, dir, "g.txt", "feat\n")
	gitIn(t, dir, "add", "g.txt")
	gitIn(t, dir, "commit", "-m", "feat work")
	gitIn(t, dir, "switch", "main")

	ok, msg := GitCLI{}.Merge(dir, "feat")
	if !ok || msg != "" {
		t.Fatalf("merge ok=%v msg=%q", ok, msg)
	}
	if !fileExists(dir, "g.txt") {
		t.Fatal("merge --no-ff did not bring feat's file onto main")
	}
	if fileExists(dir, ".git/MERGE_HEAD") {
		t.Fatal("still mid-merge after a clean merge")
	}
}

// AC 3.3 / BR1: a merge conflict → git merge fails (ok:false + stderr); MergeAbort restores the
// tree so it is never left mid-merge (.git/MERGE_HEAD gone).
func TestGitCLI_Merge_conflictThenAbort(t *testing.T) {
	dir := gitRepo(t, "main", "feat")
	// feat and main both change f.txt → a real conflict.
	gitIn(t, dir, "switch", "feat")
	writeFile(t, dir, "f.txt", "feat-change\n")
	gitIn(t, dir, "commit", "-am", "feat change")
	gitIn(t, dir, "switch", "main")
	writeFile(t, dir, "f.txt", "main-change\n")
	gitIn(t, dir, "commit", "-am", "main change")

	ok, msg := GitCLI{}.Merge(dir, "feat")
	if ok || msg == "" {
		t.Fatalf("want conflict failure, got ok=%v msg=%q", ok, msg)
	}
	if !fileExists(dir, ".git/MERGE_HEAD") {
		t.Fatal("expected a half-merge state after the conflict")
	}
	GitCLI{}.MergeAbort(dir)
	if fileExists(dir, ".git/MERGE_HEAD") {
		t.Fatal("MergeAbort left the tree mid-merge")
	}
}

// AC 3.4 / BR3: a local merge NEVER pushes — a bare remote's main ref is byte-identical after.
func TestGitCLI_Merge_neverPushes(t *testing.T) {
	dir := gitRepo(t, "main", "feat")
	gitIn(t, dir, "switch", "feat")
	writeFile(t, dir, "g.txt", "feat\n")
	gitIn(t, dir, "add", "g.txt")
	gitIn(t, dir, "commit", "-m", "feat work")
	gitIn(t, dir, "switch", "main")

	bare := filepath.Join(t.TempDir(), "remote.git")
	if out, err := exec.Command("git", "init", "--bare", bare).CombinedOutput(); err != nil {
		t.Fatalf("init bare: %v (%s)", err, out)
	}
	gitIn(t, dir, "remote", "add", "origin", bare)
	gitIn(t, dir, "push", "origin", "main")
	before := gitOut(t, bare, "rev-parse", "main")

	ok, msg := GitCLI{}.Merge(dir, "feat")
	if !ok || msg != "" {
		t.Fatalf("merge ok=%v msg=%q", ok, msg)
	}
	if after := gitOut(t, bare, "rev-parse", "main"); after != before {
		t.Fatalf("BR3 violated: bare remote main changed %s -> %s (merge pushed)", before, after)
	}
}

func TestGitCLI_Remotes_DefaultBranch_CurrentName(t *testing.T) {
	dir := gitRepo(t, "main", "feat")
	if r := (GitCLI{}).Remotes(dir); len(r) != 0 {
		t.Fatalf("want no remotes, got %v", r)
	}
	gitIn(t, dir, "remote", "add", "origin", "https://example.com/x.git")
	if r := (GitCLI{}).Remotes(dir); len(r) != 1 || r[0] != "origin" {
		t.Fatalf("want [origin], got %v", r)
	}
	// no origin/HEAD set → "" (the caller falls back to "main").
	if d := (GitCLI{}).DefaultBranch(dir); d != "" {
		t.Fatalf("want empty default branch, got %q", d)
	}
	gitIn(t, dir, "switch", "feat")
	if n := (GitCLI{}).CurrentBranchName(dir); n != "feat" {
		t.Fatalf("want feat, got %q", n)
	}
}
