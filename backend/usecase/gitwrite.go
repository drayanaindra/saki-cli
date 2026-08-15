package usecase

import (
	"fmt"
	"log"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

// ValidationError is a 422 with a client-facing message — a missing field or an invalid new
// branch name (BR5). The adapter maps it to HTTP 422 with Msg as the body's error text.
type ValidationError struct{ Msg string }

func (e ValidationError) Error() string { return e.Msg }

// SwitchResult is the outcome of a branch switch: OK true with Branch on success; OK false with
// Error (git's stderr) on a graceful failure (e.g. a dirty tree). Never a throw — parity with
// apps/server's {ok,branch} / {ok:false,error} at HTTP 200.
type SwitchResult struct {
	OK     bool
	Branch string
	Error  string
}

// GitWriteService is the F3 git-write use case (list + switch), driven through a GitWriter port.
type GitWriteService struct {
	w GitWriter
}

// NewGitWriteService wires a GitWriter into the service.
func NewGitWriteService(w GitWriter) GitWriteService { return GitWriteService{w: w} }

// Branches lists the repo's local branches. It enforces the cwd-required rule (422, parity with
// apps/server GET /api/branches) before touching the writer.
func (s GitWriteService) Branches(r domain.Repo) ([]string, error) {
	if !r.Valid() {
		return nil, ErrCwdRequired
	}
	return s.w.ListBranches(r.Dir)
}

// Switch checks out an existing branch, or creates-and-switches (create=true). It upholds:
//   - cwd + branch required (422) — parity with apps/server switch-branch;
//   - BR5: on the create path the name is validated BEFORE any git command runs, so an invalid
//     name (422) never reaches the writer.
func (s GitWriteService) Switch(r domain.Repo, branch string, create bool) (SwitchResult, error) {
	if !r.Valid() || branch == "" {
		return SwitchResult{}, ValidationError{Msg: "cwd and branch (strings) are required"}
	}
	if create {
		if err := domain.ValidateBranchName(branch); err != nil {
			return SwitchResult{}, ValidationError{Msg: err.Error()}
		}
	}
	ok, errMsg := s.w.Switch(r.Dir, branch, create)
	return SwitchResult{OK: ok, Branch: branch, Error: errMsg}, nil
}

// MRResult is the outcome of a create-MR: OK true + URL on success; OK false + Error on a graceful
// no-op (no remote / detached HEAD / source==target / glab failure). Never a throw — parity with
// apps/server's {ok,url} / {ok:false,error} at HTTP 200.
type MRResult struct {
	OK    bool
	URL   string
	Error string
}

// CreateMR pushes the current branch + opens a merge request via glab, resolving the target from
// the remote default branch (fallback "main"). It reproduces apps/server create-mr's graceful
// guards (index.ts:437-462), all returning OK=false at HTTP 200 (BR2): a missing remote, a detached
// HEAD, and source==target. cwd is required (422).
func (s GitWriteService) CreateMR(r domain.Repo) (MRResult, error) {
	if !r.Valid() {
		return MRResult{}, ValidationError{Msg: "cwd (string) is required"}
	}
	if len(s.w.Remotes(r.Dir)) == 0 {
		return MRResult{OK: false, Error: "no git remote configured — add a remote before creating an MR"}, nil
	}
	target := s.w.DefaultBranch(r.Dir)
	if target == "" {
		target = "main"
	}
	source := s.w.CurrentBranchName(r.Dir)
	if source == "" {
		return MRResult{OK: false, Error: "detached HEAD — switch to a branch before creating an MR"}, nil
	}
	if source == target {
		return MRResult{OK: false, Error: fmt.Sprintf("already on the target branch %q — switch to a feature branch first", target)}, nil
	}
	url, errMsg := s.w.CreateMR(r.Dir, source, target)
	if errMsg != "" {
		return MRResult{OK: false, Error: errMsg}, nil
	}
	return MRResult{OK: true, URL: url}, nil
}

// MergeResult is the outcome of a local merge-to-main: OK true + Branch(target)+Source on success;
// OK false + Error on a graceful failure (detached HEAD / no local base / source==target / a
// dirty-tree switch / a merge conflict). Never a throw — parity with apps/server at HTTP 200.
type MergeResult struct {
	OK     bool
	Branch string
	Source string
	Error  string
}

// firstLocalBase returns "main" (then "master") if present in the local branches, else "" — the
// merge target resolution (parity with index.ts:476-479).
func firstLocalBase(locals []string) string {
	for _, base := range []string{"main", "master"} {
		for _, b := range locals {
			if b == base {
				return base
			}
		}
	}
	return ""
}

// MergeToMain does a LOCAL --no-ff merge of the current feature branch into the local base branch
// (main, then master). It reproduces apps/server merge-to-main (index.ts:468-508) exactly, all
// failures returning OK=false at HTTP 200 (BR2), and is LOCAL-only — never pushes, never --force
// (BR3). On a merge conflict it aborts the half-merge and switches back to the feature branch so the
// working tree is never left mid-merge (BR1). cwd is required (422).
func (s GitWriteService) MergeToMain(r domain.Repo) (MergeResult, error) {
	if !r.Valid() {
		return MergeResult{}, ValidationError{Msg: "cwd (string) is required"}
	}
	// source = current branch; resolve BEFORE switching away (guard detached HEAD).
	source := s.w.CurrentBranchName(r.Dir)
	if source == "" {
		return MergeResult{OK: false, Error: "detached HEAD — switch to a feature branch before merging"}, nil
	}
	locals, _ := s.w.ListBranches(r.Dir)
	target := firstLocalBase(locals)
	if target == "" {
		return MergeResult{OK: false, Error: "no local main or master branch to merge into"}, nil
	}
	if source == target {
		return MergeResult{OK: false, Error: fmt.Sprintf("already on %q — switch to a feature branch first", target)}, nil
	}
	// switch to the target (NO --force: a dirty tree fails here and nothing is merged — BR1).
	if ok, msg := s.w.Switch(r.Dir, target, false); !ok {
		return MergeResult{OK: false, Error: msg}, nil
	}
	// merge the feature branch. On conflict, abort the half-merge + switch back to the feature branch
	// so the working tree is restored to a clean, pre-merge state (BR1).
	if ok, msg := s.w.Merge(r.Dir, source); !ok {
		s.w.MergeAbort(r.Dir)
		// A failed switch-back strands the tree on the base branch — it can't be surfaced to the
		// client, but it must not be silent (parity with index.ts:501-505).
		if ok, sbMsg := s.w.Switch(r.Dir, source, false); !ok {
			log.Printf("merge-to-main: could not switch back to %q after aborting a conflict: %s", source, sbMsg)
		}
		return MergeResult{OK: false, Error: msg}, nil
	}
	return MergeResult{OK: true, Branch: target, Source: source}, nil
}
