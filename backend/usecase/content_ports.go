package usecase

// Read-only ports for the board's content bucket (F4). Only reads are exposed — no write method — so
// R1 (a board read never mutates the file it parses) holds structurally, not just by convention.

// RoadmapFS reads the roadmap file. Read returns (content, true) or ("", false) when unreadable.
type RoadmapFS interface {
	Exists(path string) bool
	Read(path string) (string, bool)
}

// WorkItemsFS walks + reads the markdown work queue.
type WorkItemsFS interface {
	// WalkMarkdown returns every *.md path under cwd (bounded depth/count, skips noise/dot dirs,
	// never follows symlinks). Absolute paths (parity with the TS join(cwd,…)).
	WalkMarkdown(cwd string) []string
	Read(path string) (string, bool)
	Exists(path string) bool
	MTimeMs(path string) int64
	// ListDir returns the base names directly under dir (empty on any error). Used by the
	// plan-artifact frontier check; never recurses.
	ListDir(dir string) []string
}

// CommitVerifier resolves whether a commit SHA exists in cwd or a child repo (for /build resume).
type CommitVerifier interface {
	CommitExists(cwd, sha string) bool
}
