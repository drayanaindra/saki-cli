// Package infra implements the usecase ports against the outside world (here: the git
// CLI). It may import domain / usecase (inward); nothing imports infra except cmd wiring.
package infra

import (
	"log"
	"os/exec"
	"strings"
	"time"

	"github.com/drayanaindra/saki-cli/backend/usecase"
)

// GitCLI reads AND writes git state by shelling out with an argv array (no shell string) — the
// same injection guard as apps/server's execFileSync. It satisfies usecase.BranchReader and
// usecase.GitWriter. SwitchTimeout bounds the `git switch` exec (BR6); zero uses the 30s
// default (a test sets a short value to exercise the bound without a real 30s wait).
type GitCLI struct {
	SwitchTimeout time.Duration
	ListTimeout   time.Duration
	MRTimeout     time.Duration
	MergeTimeout  time.Duration
	PushTimeout   time.Duration
	GlabBin       string // "" → "glab" (a test points this at a stub script)
}

func (g GitCLI) pushTO() time.Duration {
	if g.PushTimeout > 0 {
		return g.PushTimeout
	}
	return 120 * time.Second
}

func (g GitCLI) mergeTO() time.Duration {
	if g.MergeTimeout > 0 {
		return g.MergeTimeout
	}
	return 60 * time.Second
}

func (g GitCLI) switchTO() time.Duration {
	if g.SwitchTimeout > 0 {
		return g.SwitchTimeout
	}
	return 30 * time.Second
}

func (g GitCLI) listTO() time.Duration {
	if g.ListTimeout > 0 {
		return g.ListTimeout
	}
	return 10 * time.Second
}

func (g GitCLI) mrTO() time.Duration {
	if g.MRTimeout > 0 {
		return g.MRTimeout
	}
	return 120 * time.Second
}

func (g GitCLI) glabBin() string {
	if g.GlabBin != "" {
		return g.GlabBin
	}
	return "glab"
}

// CurrentBranch runs `git -C <dir> branch --show-current`. An empty result or any git
// error yields nil (no branch), mirroring apps/server's {branch:null} fallback — a
// non-repo dir is "no branch", not a failure.
func (GitCLI) CurrentBranch(dir string) (*string, error) {
	out, err := exec.Command("git", "-C", dir, "branch", "--show-current").Output()
	if err != nil {
		return nil, nil
	}
	name := strings.TrimSpace(string(out))
	if name == "" {
		return nil, nil
	}
	return &name, nil
}

// ListBranches runs `git -C <dir> branch --format=%(refname:short)`. ANY git error (including a
// present-but-non-repo dir) yields an empty slice, mirroring apps/server index.ts:410-411's
// {branches:[]} fallback — a non-repo dir is "no branches", not a failure.
func (g GitCLI) ListBranches(dir string) ([]string, error) {
	out, e := runGit(g.listTO(), "-C", dir, "branch", "--format=%(refname:short)")
	if e != "" {
		return []string{}, nil // non-repo / error → [] (BR6-bounded, parity index.ts:410-411)
	}
	return ParseBranchList(out), nil
}

// Switch runs `git switch` bounded by switchTO() (BR6), NEVER --force (BR1). ok=true on success;
// on failure ok=false + git's stderr (a dirty tree that would be overwritten fails loudly, so no
// work is lost). Branch-name validation for the create path is the usecase's job (BR5), before here.
func (g GitCLI) Switch(dir, branch string, create bool) (ok bool, errMsg string) {
	if _, e := runGit(g.switchTO(), SwitchArgs(dir, branch, create)...); e != "" {
		return false, e
	}
	return true, ""
}

// Remotes returns the repo's configured remote names (`git -C <dir> remote`), or nil on any error
// (parity with apps/server create-mr's "no remote configured" guard, index.ts:441-443).
func (g GitCLI) Remotes(dir string) []string {
	out, e := runGit(g.listTO(), "-C", dir, "remote")
	if e != "" {
		return nil
	}
	return ParseBranchList(out)
}

// UserName reads `git -C <dir> config user.name`, or "" on any error (an unconfigured name or a
// non-repo dir) — feeding domain.ResolveApprover's `@<git user.name>` fallback. Fixed argv (no shell
// string) → no injection surface, parity with the TS execFileSync('git',['-C',cwd,'config','user.name'])
// at index.ts:665. Satisfies usecase.GitUserReader.
func (g GitCLI) UserName(dir string) string {
	out, e := runGit(g.listTO(), "-C", dir, "config", "user.name")
	if e != "" {
		return ""
	}
	return strings.TrimSpace(out)
}

// SliceCommit returns a slice's feat commit ("<short-sha> <subject>") via `git -C <dir> log --grep
// slice-<n> -1 --format=%h %s`, or "" on no match / git error. Fixed argv (no shell); n is a --grep
// regex value, parity with the TS execFileSync at index.ts:561. Satisfies usecase.GitSliceReader.
func (g GitCLI) SliceCommit(dir, n string) string {
	out, e := runGit(g.listTO(), "-C", dir, "log", "--grep", "slice-"+n, "-1", "--format=%h %s")
	if e != "" {
		return ""
	}
	return strings.TrimSpace(out)
}

// DefaultBranch resolves the remote's default branch from origin/HEAD (`symbolic-ref --short
// refs/remotes/origin/HEAD`, then strip the "origin/" prefix), or "" when unset — the caller
// falls back to "main" (parity with index.ts:445-449).
func (g GitCLI) DefaultBranch(dir string) string {
	out, e := runGit(g.listTO(), "-C", dir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if e != "" {
		return ""
	}
	return strings.TrimPrefix(strings.TrimSpace(out), "origin/")
}

// CurrentBranchName is CurrentBranch as a plain string ("" when detached / no branch) — the shape
// create-mr / merge-to-main need for their source-branch resolution.
func (g GitCLI) CurrentBranchName(dir string) string {
	b, _ := g.CurrentBranch(dir)
	if b == nil {
		return ""
	}
	return *b
}

// CreateMR runs `glab mr create` (bounded, BR6). glab has no -C, so it is scoped via the process
// dir (cwd). On success returns the created MR url (parsed from glab's output, which may be on
// stderr); on failure returns "" + glab's stderr (execErr). Never --force, never a shell string.
func (g GitCLI) CreateMR(dir, source, target string) (url, errMsg string) {
	_, combined, e := run(dir, g.mrTO(), g.glabBin(), MrArgs(source, target)...)
	if e != "" {
		return "", e
	}
	return FirstURL(combined), ""
}

// Merge runs a LOCAL `git merge --no-ff <source>` bounded (BR6), NEVER --force, NEVER a push (BR3).
// ok=true on success; on a conflict (or any failure) ok=false + git's stderr — the caller aborts
// the half-merge and restores the feature branch (parity with index.ts:489-507).
func (g GitCLI) Merge(dir, source string) (ok bool, errMsg string) {
	if _, e := runGit(g.mergeTO(), MergeArgs(dir, source)...); e != "" {
		return false, e
	}
	return true, ""
}

// Push pushes the current branch to a remote on successful build completion (F6 Slice 5) — the "end of
// work" counterpart to auto-branch at start. NEVER --force (BR4). Graceful: a detached HEAD or a repo
// with NO remote is a non-error SKIP (never a lifecycle change); bounded by pushTO() so a hung push is
// abandoned at the deadline (BR6-style), never blocking finalize. Prefers the "origin" remote, else the
// first configured one. This is the explicit, guarded auto-push path — distinct from Merge's documented
// "NEVER a push" (BR3), which bans a push on the merge-to-main path, not here. Satisfies usecase.Pusher.
func (g GitCLI) Push(dir string) usecase.PushResult {
	branch := g.CurrentBranchName(dir)
	if branch == "" {
		return usecase.PushResult{Skipped: "detached HEAD or no current branch"}
	}
	remotes := g.Remotes(dir)
	if len(remotes) == 0 {
		return usecase.PushResult{Skipped: "no git remote configured"}
	}
	remote := remotes[0]
	for _, r := range remotes {
		if r == "origin" {
			remote = "origin"
			break
		}
	}
	if _, e := runGit(g.pushTO(), "-C", dir, "push", remote, branch); e != "" { // NEVER --force
		return usecase.PushResult{Error: e}
	}
	return usecase.PushResult{OK: true, Branch: branch}
}

// MergeAbort runs `git merge --abort` (best-effort) to undo a half-merge after a conflict, so the
// working tree is never left mid-merge. A failed abort is logged (parity with index.ts:496-500) —
// it can't be surfaced to the client, but it must not be silent.
func (g GitCLI) MergeAbort(dir string) {
	if _, e := runGit(g.switchTO(), "-C", dir, "merge", "--abort"); e != "" {
		log.Printf("merge-to-main: git merge --abort failed after a conflict: %s", e)
	}
}
