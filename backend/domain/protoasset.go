package domain

import (
	"path/filepath"
	"regexp"
	"strings"
)

// protoAssetRel matches a proto-gallery asset rel, relative to the run's repo:
// [<subpkg>/…/]tasks/proto-<slug>/<file.ext>. The optional leading segments support a MONOREPO
// (gallery under a subpackage). Traversal (`..`) is NOT filtered here — the containment check is the
// real guard. Faithful port of apps/server/src/proto.ts:12 PROTO_ASSET_REL.
var protoAssetRel = regexp.MustCompile(`^(?:[A-Za-z0-9._-]+/)*tasks/proto-[A-Za-z0-9._-]+/[A-Za-z0-9][A-Za-z0-9._-]*\.[A-Za-z0-9]+$`)

// ResolveProtoRel validates a (cwd, rel) proto-asset pair and returns the absolute path to serve.
// Pure — no filesystem: it enforces 🔒 BR1's regex + cwd-containment (returns status 422 on any
// rejection, 0 on success). Existence (404) is the usecase's job. Faithful port of proto.ts:22-31
// (the resolveProtoAsset regex + containment guard, minus the existsSync check).
func ResolveProtoRel(cwd, rel string) (abs string, status int) {
	if cwd == "" || rel == "" {
		return "", 422
	}
	if !protoAssetRel.MatchString(rel) {
		return "", 422
	}
	root := filepath.Clean(cwd)
	full := filepath.Join(root, rel)
	if full != root && !strings.HasPrefix(full, root+string(filepath.Separator)) {
		return "", 422 // rel escapes cwd (e.g. a leading ../ subpkg segment)
	}
	return full, 0
}
