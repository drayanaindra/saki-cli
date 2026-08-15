package usecase

import (
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

// ScreenshotFS is the read-only filesystem surface the screenshot usecase needs: the Slice-1 serve
// surface (Exists + the binary Open) plus a NEW screenshot walk. Implemented by infra.ContentFS.
// Read-only: no write method (R1 by interface segregation).
type ScreenshotFS interface {
	Exists(path string) bool
	Open(path string) (io.ReadSeekCloser, time.Time, error)
	// WalkShots collects every .png under cwd's SHOT_DIRS (depth ≤4), each with its mod time — a NEW
	// depth-4 walk, NOT WalkMarkdown (depth-6/.md/ignore-dirs would be a parity bug). Ordering +
	// relativize are the usecase's job (kept pure here).
	WalkShots(cwd string) []domain.ShotFile
}

// ScreenshotService serves the two screenshot routes (GET /api/screenshots list + GET /api/screenshot
// serve) as a pure resolver over the read-only ScreenshotFS. Faithful port of
// apps/server/src/screenshots.ts findScreenshots + resolveScreenshot: 🔒 BR2 (.png + cwd-containment
// → 422), BR3 (missing → 404), repo-global (v1, NOT slice-keyed).
type ScreenshotService struct {
	fs ScreenshotFS
}

// NewScreenshotService wires a read-only ScreenshotFS into the service.
func NewScreenshotService(fs ScreenshotFS) ScreenshotService {
	return ScreenshotService{fs: fs}
}

// List returns every screenshot under cwd as newest-first repo-relative rels. Faithful port of
// findScreenshots: sort by mtime desc (sort.SliceStable → equal-mtime ties keep read-dir order),
// map to repo-relative, drop anything that relativizes outside cwd (defensive `..` filter, parity
// with screenshots.ts:23). Always a non-nil slice so the JSON body is `{"shots":[]}` not `{"shots":null}`.
func (s ScreenshotService) List(cwd string) []string {
	root := filepath.Clean(cwd)
	shots := s.fs.WalkShots(root)
	sort.SliceStable(shots, func(i, j int) bool { return shots[i].MTimeMs > shots[j].MTimeMs })
	out := []string{}
	for _, sh := range shots {
		rel, err := filepath.Rel(root, sh.Path)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		out = append(out, rel)
	}
	return out
}

// Resolve validates (cwd, rel) and opens the screenshot for serving. 422 on a non-.png/escaping rel
// (domain), 404 on a well-formed-but-missing/unreadable file (BR3 — never 500), else Status 200 with
// an open Content the caller must Close. Mirrors ProtoAssetService.Resolve (same ProtoServe result).
func (s ScreenshotService) Resolve(cwd, rel string) ProtoServe {
	abs, status := domain.ResolveScreenshotRel(cwd, rel)
	if status != 0 {
		return ProtoServe{Status: status}
	}
	if !s.fs.Exists(abs) {
		return ProtoServe{Status: 404}
	}
	rc, mt, err := s.fs.Open(abs)
	if err != nil {
		return ProtoServe{Status: 404}
	}
	return ProtoServe{Status: 200, Path: abs, ModTime: mt, Content: rc}
}
