package domain

import (
	"path/filepath"
	"strings"
)

// ShotFile is one screenshot the walk found: its absolute path plus mod time (millis since epoch).
// The usecase sorts these newest-first and relativizes them against cwd — keeping the walk (infra) a
// dumb collector and the ordering/relativize logic (usecase) pure + testable. Parity: the anonymous
// {path,m} shape in apps/server/src/screenshots.ts:7.
type ShotFile struct {
	Path    string
	MTimeMs int64
}

// ResolveScreenshotRel validates a (cwd, rel) screenshot pair and returns the absolute path to serve.
// Pure — no filesystem: it enforces 🔒 BR2's `.png` suffix (case-insensitive) + cwd-containment
// (status 422 on any rejection, 0 on success). Existence (404) is the usecase's job. Faithful port of
// screenshots.ts:28-37 resolveScreenshot (minus the existsSync check).
func ResolveScreenshotRel(cwd, rel string) (abs string, status int) {
	if cwd == "" || rel == "" {
		return "", 422
	}
	if filepath.IsAbs(rel) {
		return "", 422 // absolute rel escapes cwd
	}
	if !strings.HasSuffix(strings.ToLower(rel), ".png") {
		return "", 422
	}
	root := filepath.Clean(cwd)
	full := filepath.Join(root, rel)
	if full != root && !strings.HasPrefix(full, root+string(filepath.Separator)) {
		return "", 422 // rel escapes cwd
	}
	return full, 0
}
