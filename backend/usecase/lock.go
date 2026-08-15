package usecase

import "github.com/drayanaindra/saki-cli/backend/domain"

// ContentWriter writes a content file (create/overwrite). Kept SEPARATE from the read-only
// RoadmapFS/WorkItemsFS ports so the board read services stay structurally write-free (R1) — only
// LockService, a write route, takes a writer. Implemented by infra.ContentFS.
type ContentWriter interface {
	WriteFile(path, content string) error
}

// GitUserReader resolves `git config user.name` for the lock approver fallback ("" when unset or a
// non-repo dir). Implemented by infra.GitCLI; a minimal one-method port (interface segregation).
type GitUserReader interface {
	UserName(cwd string) string
}

// LockService serves POST /api/lock-prd — the PRD freeze the studio writes at Approve→Run Build.
// Faithful port of the index.ts:656 endpoint + lockPrd.ts core. R2: idempotent (already-locked → no-op,
// no second marker). R3: cwd-contained (domain.ValidateLockRequest). R6: any fs failure degrades to
// {ok:false} at HTTP 200, never a 500. R7: byte-identical marker + Status flip vs the TS oracle.
type LockService struct {
	fs     WorkItemsFS
	writer ContentWriter
	git    GitUserReader
}

// NewLockService wires the read fs, the content writer, and the git user.name reader.
func NewLockService(fs WorkItemsFS, writer ContentWriter, git GitUserReader) LockService {
	return LockService{fs: fs, writer: writer, git: git}
}

// Lock validates + freezes the PRD at path (contained in cwd), idempotently. date is injected for
// deterministic parity with the TS opts.date. Returns the HTTP status + JSON body verbatim; the
// response echoes the original request `path` (not the resolved abs), matching index.ts:656.
func (s LockService) Lock(cwd, path, date string) (int, any) {
	v := domain.ValidateLockRequest(path, cwd, s.fs.Exists)
	if !v.OK {
		return v.Status, map[string]any{"error": v.Error} // R3: 422 on non-.md / escape / missing
	}
	content, ok := s.fs.Read(v.AbsPath)
	if !ok {
		// R6: a read failure after the exists check (race/perm) — never applyLock onto "" and clobber.
		return 200, map[string]any{"ok": false, "error": "read failed"}
	}
	if domain.IsLocked(content) {
		return 200, map[string]any{"ok": true, "alreadyLocked": true, "path": path} // R2 no-op
	}
	ui := domain.LockUiValue(findProtoPreview(s.fs, cwd, v.AbsPath))
	approver := domain.ResolveApprover(content, s.git.UserName(cwd))
	next, _ := domain.ApplyLock(content, approver, date, ui)
	if err := s.writer.WriteFile(v.AbsPath, next); err != nil {
		return 200, map[string]any{"ok": false, "error": err.Error()} // R6: graceful, never 500
	}
	return 200, map[string]any{"ok": true, "locked": true, "path": path}
}
