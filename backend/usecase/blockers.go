package usecase

import (
	"strconv"
	"strings"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

// BlockersService serves GET /api/blockers — why a slice is blocked + the decisions it waits on.
// Faithful port of the index.ts:576 endpoint. Read-only (R1). R6: missing PRD → 404, bad input → 422,
// never a 500. R7: byte-identical {n,reason,deferred,questions} vs TS.
type BlockersService struct {
	fs WorkItemsFS
}

// NewBlockersService wires a read-only WorkItemsFS.
func NewBlockersService(fs WorkItemsFS) BlockersService {
	return BlockersService{fs: fs}
}

// ReadBlockers validates + assembles the blocker view for slice n of prd. cwd may be "" (the progress
// scratchpad is then omitted). n is parsed strictly as an integer (the board always sends one; a
// non-integer/empty n → 422, mirroring TS's !Number.isInteger(n) guard for every real input).
func (s BlockersService) ReadBlockers(cwd, prd, nRaw string) (int, any) {
	n, err := strconv.Atoi(nRaw)
	if prd == "" || !strings.HasSuffix(prd, ".md") || err != nil {
		return 422, map[string]any{"error": "prd (.md) and integer n are required"}
	}
	prdMd, ok := s.fs.Read(prd)
	if !ok {
		return 404, map[string]any{"error": "PRD not found"}
	}
	progress := s.readProgress(cwd, prd)
	reason, _ := domain.BlockerReason(progress, n) // "" exactly when no blocker → JSON null via nilIfEmpty
	return 200, map[string]any{
		"n":         n,
		"reason":    nilIfEmpty(reason),
		"deferred":  containsInt(domain.DeferredSliceNums(progress), n),
		"questions": domain.QuestionsForSlice(prdMd, n),
	}
}

// readProgress finds + reads the PRD's /build progress scratchpad (first existing candidate), or "".
func (s BlockersService) readProgress(cwd, prd string) string {
	if cwd == "" {
		return ""
	}
	for _, c := range domain.BuildArtifactCandidates(cwd, prd, "progress") {
		if s.fs.Exists(c) {
			if content, ok := s.fs.Read(c); ok {
				return content
			}
			return ""
		}
	}
	return ""
}

// GitSliceReader resolves a slice's feat commit (git log --grep). Implemented by infra.GitCLI.
type GitSliceReader interface {
	SliceCommit(cwd, n string) string
}

// SliceMetaService serves GET /api/slice-meta — the per-slice feat commit. Faithful port of
// index.ts:558. Read-only. R6: a slice with no commit → null, git error → null, never 500.
type SliceMetaService struct {
	git GitSliceReader
}

// NewSliceMetaService wires the git slice-commit reader.
func NewSliceMetaService(git GitSliceReader) SliceMetaService {
	return SliceMetaService{git: git}
}

// SliceMeta returns {commit} for slice n in cwd. n is a raw string (grepped as `slice-<n>`, matching
// TS String(req.query.n)); 422 when either cwd or n is empty.
func (s SliceMetaService) SliceMeta(cwd, n string) (int, any) {
	if cwd == "" || n == "" {
		return 422, map[string]any{"error": "cwd and n are required"}
	}
	return 200, map[string]any{"commit": nilIfEmpty(s.git.SliceCommit(cwd, n))}
}

// containsInt reports whether xs contains v.
func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
