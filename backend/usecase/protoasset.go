package usecase

import (
	"io"
	"time"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

// ProtoServeFS is the read-only filesystem surface the proto-serve usecase needs: existence (mirrors
// existsSync) plus a NEW binary byte-opener — the F4 ContentFS.Read returns a UTF-8 string and cannot
// stream binary .png faithfully, so serving requires an io.ReadSeekCloser + mod time. Implemented by
// infra.ContentFS. Read-only: no write method (R1 by interface segregation).
type ProtoServeFS interface {
	Exists(path string) bool
	Open(path string) (io.ReadSeekCloser, time.Time, error)
}

// ProtoServe is a resolved, ready-to-serve asset. Content is non-nil only when Status == 200 and the
// caller MUST Close it. Path is the resolved absolute path (the adapter derives the Content-Type from
// its extension). On rejection, Status is 422 (bad rel / escape) or 404 (missing), Content nil.
type ProtoServe struct {
	Status  int
	Path    string
	ModTime time.Time
	Content io.ReadSeekCloser
}

// ProtoAssetService serves the proto gallery asset route (GET /api/proto/{dir}/{rest...}) as a pure
// path-resolver over the read-only ProtoServeFS. Faithful port of apps/server/src/index.ts:544 +
// proto.ts resolveProtoAsset: 🔒 BR1 (regex + cwd-containment → 422), BR3 (missing → 404), else the
// opened bytes. Generation stays Go-owned elsewhere (kind:'proto'); this is the serve half.
type ProtoAssetService struct {
	fs ProtoServeFS
}

// NewProtoAssetService wires a read-only ProtoServeFS into the service.
func NewProtoAssetService(fs ProtoServeFS) ProtoAssetService {
	return ProtoAssetService{fs: fs}
}

// Resolve validates (cwd, rel) and opens the asset for serving. 422 on a bad/escaping rel (domain),
// 404 on a well-formed-but-missing/unreadable file (BR3 — never 500), else Status 200 with an open
// Content the caller must Close.
func (s ProtoAssetService) Resolve(cwd, rel string) ProtoServe {
	abs, status := domain.ResolveProtoRel(cwd, rel)
	if status != 0 {
		return ProtoServe{Status: status}
	}
	if !s.fs.Exists(abs) {
		return ProtoServe{Status: 404}
	}
	rc, mt, err := s.fs.Open(abs)
	if err != nil {
		// Raced away between Exists and Open, or a dir/unreadable — 404, not 500 (BR3 parity).
		return ProtoServe{Status: 404}
	}
	return ProtoServe{Status: 200, Path: abs, ModTime: mt, Content: rc}
}
