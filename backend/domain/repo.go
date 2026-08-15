// Package domain holds the core entities and rules of the orchestrator. It has ZERO
// dependencies on any outer layer (usecase / adapter / infra) — the clean-architecture
// dependency rule, verified by AC 1.3 (`go list -deps ./domain/...` imports nothing outward).
package domain

// Repo is a git working directory the operator is orchestrating.
type Repo struct {
	Dir string
}

// Valid reports whether the repo has a usable working dir. An empty Dir encodes the
// "cwd is required" rule the branch read enforces (parity with apps/server /api/branch).
func (r Repo) Valid() bool {
	return r.Dir != ""
}
