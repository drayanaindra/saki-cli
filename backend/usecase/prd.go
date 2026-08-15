package usecase

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

const (
	errNotMdFile = "path must be a .md file"
	previewFile  = "preview.html"
	protoPrefix  = "proto-"
)

// PrdService serves the board's three read-only PRD/review routes (GET /api/prd, /api/review,
// /api/review-state) through the read-only WorkItemsFS port. Faithful port of the index.ts:344-388
// handlers + apps/server/src/prd.ts helpers. R1: read-only (no writer method). R6: malformed/missing
// input degrades to {found:false} or a 422 — never a 500. R7: byte-identical outputs to the TS oracle.
type PrdService struct {
	fs WorkItemsFS
}

// NewPrdService wires a read-only WorkItemsFS into the service.
func NewPrdService(fs WorkItemsFS) PrdService {
	return PrdService{fs: fs}
}

// ReadPrd serves GET /api/prd. Mirrors index.ts:344: an explicit `path` (must be .md) is read directly;
// otherwise the latest PRD in `cwd` is discovered. Returns {found,path,score,content,protoPreviewFile}
// with score/protoPreviewFile emitted as JSON null when absent (parity with TS's string|null).
func (s PrdService) ReadPrd(cwd, path string) (int, any) {
	if path != "" {
		if !strings.HasSuffix(path, ".md") {
			return 422, map[string]any{"error": errNotMdFile}
		}
		content, ok := s.fs.Read(path)
		if !ok {
			return 200, map[string]any{"found": false}
		}
		return 200, s.prdBody(cwd, path, content)
	}
	if cwd == "" {
		return 422, map[string]any{"error": "cwd or path is required"}
	}
	best := s.latestPrdPath(cwd)
	if best == "" {
		return 200, map[string]any{"found": false}
	}
	content, ok := s.fs.Read(best)
	if !ok {
		return 200, map[string]any{"found": false}
	}
	return 200, s.prdBody(cwd, best, content)
}

// prdBody assembles the shared {found:true,…} PRD response.
func (s PrdService) prdBody(cwd, path, content string) map[string]any {
	return map[string]any{
		"found":            true,
		"path":             path,
		"score":            nilIfEmpty(domain.ExtractScore(content)),
		"content":          content,
		"protoPreviewFile": nilIfEmpty(findProtoPreview(s.fs, cwd, path)),
	}
}

// ReadReview serves GET /api/review. Mirrors index.ts:371: path required + .md (else 422), then read
// the -review.md companion → {found:true,path,content} or {found:false}.
func (s PrdService) ReadReview(path string) (int, any) {
	if path == "" {
		return 422, map[string]any{"error": "path is required"}
	}
	if !strings.HasSuffix(path, ".md") {
		return 422, map[string]any{"error": errNotMdFile}
	}
	rp := domain.ReviewPathFor(path)
	content, ok := s.fs.Read(rp)
	if !ok {
		return 200, map[string]any{"found": false}
	}
	return 200, map[string]any{"found": true, "path": rp, "content": content}
}

// ReadReviewState serves GET /api/review-state. Mirrors index.ts:382: path required + .md (else 422),
// then read the derived state file → {found:true,path,content} or {found:false}.
func (s PrdService) ReadReviewState(path, cwd string) (int, any) {
	if path == "" || !strings.HasSuffix(path, ".md") {
		return 422, map[string]any{"error": errNotMdFile}
	}
	sp := domain.ReviewStatePathFor(path, cwd)
	content, ok := s.fs.Read(sp)
	if !ok {
		return 200, map[string]any{"found": false}
	}
	return 200, map[string]any{"found": true, "path": sp, "content": content}
}

// latestPrdPath returns the newest PRD-kind markdown under cwd by mtime, or "" when none. Faithful
// port of workitems.ts:443 — reuses domain.Classify + the read-only fs (walk + mtime). First PRD
// seen sets the baseline (TS starts from -Infinity), and a strict `>` keeps the earlier file on a tie.
func (s PrdService) latestPrdPath(cwd string) string {
	best := ""
	var bestMtime int64
	for _, full := range s.fs.WalkMarkdown(cwd) {
		if domain.Classify(cwd, full) != domain.KindPRD {
			continue
		}
		m := s.fs.MTimeMs(full)
		if best == "" || m > bestMtime {
			best = full
			bestMtime = m
		}
	}
	return best
}

var protoFileSlugRe = regexp.MustCompile(`(?i)^prd-(.+)\.md$`)

// findProtoPreview locates a PRD's proto gallery preview.html on disk and returns its path relative to
// cwd, or "" when none. Faithful port of proto.ts:44 — a read helper for /api/prd's protoPreviewFile
// (NOT the proto serve bucket, which is F5). Two PRD layouts:
//  1. prd-<slug>.md → sibling proto-<slug>/preview.html in the same dir.
//  2. .../<slug>/prd.md → slug is the parent dir; probe root + each subpkg tasks/ by exact slug,
//     then scan proto-*/index.md for a reference to this PRD's repo-relative path.
//
// findProtoPreview is a package-level helper (shared by PrdService for /api/prd's protoPreviewFile and
// by LockService for the lock marker's ui: value — DRY, one faithful port). fs is passed explicitly so
// it has no PrdService coupling.
func findProtoPreview(fs WorkItemsFS, cwd, prdPath string) string {
	if cwd == "" {
		return ""
	}
	root := filepath.Clean(strings.TrimRight(cwd, "/"))
	toRel := func(abs string) string {
		if strings.HasPrefix(abs, root+"/") {
			return abs[len(root)+1:]
		}
		return ""
	}

	base := filepath.Base(prdPath)

	// Case 1: prd-<slug>.md — gallery is a sibling in the same tasks/ dir.
	if m := protoFileSlugRe.FindStringSubmatch(base); m != nil {
		abs := filepath.Join(filepath.Dir(prdPath), protoPrefix+m[1], previewFile)
		if !fs.Exists(abs) {
			return ""
		}
		return toRel(abs)
	}

	// Case 2: <slug>/prd.md layout — filename is just "prd.md".
	if strings.ToLower(base) != "prd.md" {
		return ""
	}
	slug := filepath.Base(filepath.Dir(prdPath))
	if slug == "" || slug == "." {
		return ""
	}

	// Collect tasks/ dirs to probe: repo root + each direct top-level subdir (a non-dir entry's
	// tasks/ join simply never exists, so listing all names is equivalent to TS's isDirectory filter).
	tasksDirs := []string{filepath.Join(root, "tasks")}
	for _, name := range fs.ListDir(root) {
		tasksDirs = append(tasksDirs, filepath.Join(root, name, "tasks"))
	}

	// Probe 1: exact slug match across all tasks/ locations.
	if rel, found := probeProtoSlug(fs, tasksDirs, slug, toRel); found {
		return rel
	}

	// Probe 2: index.md scan — /proto writes an index.md referencing the source PRD path.
	prdRel := toRel(prdPath)
	if prdRel == "" {
		return ""
	}
	return probeProtoIndex(fs, tasksDirs, prdRel, toRel)
}

// probeProtoSlug looks for tasks/proto-<slug>/preview.html across every tasksDir. `found` reports that
// an existing preview matched (so the caller stops probing) — its rel path is toRel(abs), which mirrors
// Probe 1's original `return toRel(abs)` even in the (unreachable-in-practice) empty-rel case.
func probeProtoSlug(fs WorkItemsFS, tasksDirs []string, slug string, toRel func(string) string) (string, bool) {
	for _, tasksDir := range tasksDirs {
		abs := filepath.Join(tasksDir, protoPrefix+slug, previewFile)
		if fs.Exists(abs) {
			return toRel(abs), true
		}
	}
	return "", false
}

// probeProtoIndex scans each tasks/proto-*/index.md for a reference to prdRel and returns the rel path
// of that dir's preview.html when it exists, else "". Extracted from findProtoPreview's Probe 2 verbatim.
func probeProtoIndex(fs WorkItemsFS, tasksDirs []string, prdRel string, toRel func(string) string) string {
	for _, tasksDir := range tasksDirs {
		for _, name := range fs.ListDir(tasksDir) {
			if !strings.HasPrefix(name, protoPrefix) {
				continue
			}
			idx, ok := fs.Read(filepath.Join(tasksDir, name, "index.md"))
			if !ok || !strings.Contains(idx, prdRel) {
				continue
			}
			abs := filepath.Join(tasksDir, name, previewFile)
			if fs.Exists(abs) {
				return toRel(abs)
			}
		}
	}
	return ""
}

// nilIfEmpty converts "" → nil so the JSON field is null (TS parity for string|null values).
func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
