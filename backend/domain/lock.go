package domain

import (
	"path/filepath"
	"regexp"
	"strings"
)

// PRD-lock pure core — faithful port of the PURE functions in apps/server/src/lockPrd.ts (marker
// construction, line-anchored idempotent detection, approver/ui derivation, request validation).
// Every fs/git touch lives in usecase/infra (R1). R7: byte-identical outputs to the TS oracle.
// R2: locking is idempotent — an already-locked PRD is a no-op (IsLocked short-circuits ApplyLock).

// lockLineRe is line-anchored (^…, multiline) so a mid-prose "<!-- prd-locked:" mention does NOT
// count as locked — it matches build's own gate (grep -qE '^<!-- prd-locked:') and the project's
// line-anchoring rule for sentinels. A real marker sits at column 0. Mirrors lockPrd.ts:14.
var lockLineRe = regexp.MustCompile(`(?m)^<!-- prd-locked:`)

// ownerRe extracts the **Owner:** field value. The TS lookahead `(?=\s+·|\s*$)` (lockPrd.ts:39) is
// ported to a NON-capturing CONSUMING group `(?:\s+·|\s*$)` — RE2 has no lookahead, but since only
// capture group 1 is used, consuming the ` · ` boundary leaves group 1 byte-identical.
var ownerRe = regexp.MustCompile(`(?m)^\*\*Owner:\*\*\s*(.+?)(?:\s+·|\s*$)`)

// lockStatusRe matches the first **Status:** field and CAPTURES its trailing boundary (` · ` or EOL) so
// ApplyLock can re-emit it — reproducing the TS lookahead `(?=\s+·|\s*$)` (lockPrd.ts:70) under RE2.
var lockStatusRe = regexp.MustCompile(`(?m)\*\*Status:\*\*[^\n]*?(\s+·|\s*$)`)

// IsLocked reports whether a real column-0 lock marker is present. Mirrors lockPrd.ts:17.
func IsLocked(content string) bool {
	return lockLineRe.MatchString(content)
}

// BuildLockMarker returns the freeze marker in build's exact grep shape (lockPrd.ts:23):
// `<!-- prd-locked: <approver> · <YYYY-MM-DD> · ui:<ui> -->`.
func BuildLockMarker(approver, date, ui string) string {
	return "<!-- prd-locked: " + approver + " · " + date + " · ui:" + ui + " -->"
}

// LockUiValue derives the marker's ui: value from findProtoPreview's cwd-relative return ("" = none).
// Mirrors lockPrd.ts:30: "" → "none"; else dirname(rel) with a guaranteed trailing "/".
func LockUiValue(previewRel string) string {
	if previewRel == "" {
		return "none"
	}
	dir := filepath.Dir(previewRel)
	if strings.HasSuffix(dir, "/") {
		return dir
	}
	return dir + "/"
}

// ResolveApprover picks the lock approver. Mirrors lockPrd.ts:38: the PRD **Owner:** value (prefixed
// @ if it lacks one) unless "unassigned"; else @<git user.name>; else "unassigned". gitUserName is
// passed in (the usecase runs `git config user.name`) so this stays pure.
func ResolveApprover(content, gitUserName string) string {
	if m := ownerRe.FindStringSubmatch(content); m != nil {
		owner := strings.TrimSpace(m[1])
		if owner != "" && !strings.EqualFold(owner, "unassigned") {
			if strings.HasPrefix(owner, "@") {
				return owner
			}
			return "@" + owner
		}
	}
	if name := strings.TrimSpace(gitUserName); name != "" {
		return "@" + name
	}
	return "unassigned"
}

// ApplyLock freezes the PRD text idempotently. Mirrors lockPrd.ts:64. An already-locked PRD is
// returned byte-identical (alreadyLocked=true). Otherwise: prepend the marker at column 0 (so build's
// line-anchored gate matches), then flip ONLY the first **Status:** value to "Locked" — preserving any
// ` · <trailer>` after it — or leave the text as marker-only when the PRD has no **Status:** field.
func ApplyLock(content, approver, date, ui string) (out string, alreadyLocked bool) {
	if IsLocked(content) {
		return content, true
	}
	withMarker := BuildLockMarker(approver, date, ui) + "\n" + content
	loc := lockStatusRe.FindStringSubmatchIndex(withMarker)
	if loc == nil {
		return withMarker, false // no **Status:** field → marker-only (the marker is load-bearing)
	}
	boundary := withMarker[loc[2]:loc[3]] // group 1: the ` · ` or EOL boundary, re-emitted verbatim
	return withMarker[:loc[0]] + "**Status:** Locked" + boundary + withMarker[loc[1]:], false
}

// LockRequest is the validated + resolved lock request (parity with lockPrd.ts:74 LockRequest union).
// OK true → AbsPath is safe to write; OK false → Status maps straight to the HTTP status with Error.
type LockRequest struct {
	OK      bool
	Status  int
	Error   string
	AbsPath string
}

// ValidateLockRequest validates + resolves a lock request. path must be a non-empty .md; cwd must be
// set; the resolved path must stay inside cwd (containment — R3); the file must exist. Mirrors
// lockPrd.ts:81, reproducing Node path.resolve (an absolute path replaces root; a relative path joins
// under it). exists is injected (real infra passes fs.Exists) so this stays pure/testable.
func ValidateLockRequest(path, cwd string, exists func(string) bool) LockRequest {
	if path == "" || !strings.HasSuffix(path, ".md") {
		return LockRequest{Status: 422, Error: "path must be a .md file"}
	}
	if cwd == "" {
		return LockRequest{Status: 422, Error: "cwd is required"}
	}
	root := filepath.Clean(cwd)
	abs := filepath.Clean(path)
	if !filepath.IsAbs(path) {
		abs = filepath.Clean(filepath.Join(root, path))
	}
	if abs != root && !strings.HasPrefix(abs, root+string(filepath.Separator)) {
		return LockRequest{Status: 422, Error: "path escapes cwd"}
	}
	if !exists(abs) {
		return LockRequest{Status: 422, Error: "PRD file not found"}
	}
	return LockRequest{OK: true, AbsPath: abs}
}
