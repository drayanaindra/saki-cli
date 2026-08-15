// Package usecase orchestrates domain objects. It defines the ports (interfaces) it
// needs from the outside world; infra implements them (dependency inversion). It may
// import domain (inward) but nothing from adapter / infra.
package usecase

// BranchReader reads the current git branch of a working directory. Implemented by
// infra (GitCLI); the usecase depends only on this abstraction.
type BranchReader interface {
	// CurrentBranch returns the current branch name, or nil when there is none
	// (detached / empty repo). "No branch" is nil, not an error.
	CurrentBranch(dir string) (*string, error)
}

// GitWriter performs the git-write bucket (F3). Implemented by infra (GitCLI). The usecase
// depends only on this abstraction; branch-name validation (BR5) happens in the usecase
// BEFORE Switch is ever called on the create path.
type GitWriter interface {
	// ListBranches returns the repo's local branches, or [] for a non-repo dir (never errors).
	ListBranches(dir string) ([]string, error)
	// Switch checks out (create=false) or creates-and-checks-out (create=true) a branch.
	// ok=true on success; on failure ok=false + git's stderr message (never --force).
	Switch(dir, branch string, create bool) (ok bool, errMsg string)
	// Remotes returns the repo's configured remote names (nil if none).
	Remotes(dir string) []string
	// DefaultBranch returns the remote default branch (origin/HEAD), or "" when unset.
	DefaultBranch(dir string) string
	// CurrentBranchName returns the current branch, or "" when detached / none.
	CurrentBranchName(dir string) string
	// CreateMR opens a merge request source→target; url on success, else "" + the error message.
	CreateMR(dir, source, target string) (url string, errMsg string)
	// Merge runs a LOCAL `git merge --no-ff <source>` (never push, never --force). ok=true on
	// success; on a conflict/failure ok=false + git's stderr.
	Merge(dir, source string) (ok bool, errMsg string)
	// MergeAbort undoes a half-merge after a conflict (best-effort), so the tree is never mid-merge.
	MergeAbort(dir string)
}
