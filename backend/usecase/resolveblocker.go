package usecase

import (
	"strings"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

// ResolveBlockerService serves POST /api/resolve-blocker — durably record the operator's blocked-slice
// answers as `✅ RESOLVED` bullets the resumed /build reads. Faithful port of the index.ts:601 endpoint
// + prdQuestions.ts resolveQuestion/appendResolvedQuestion. R5 idempotent (a recorded decision → a
// successful no-op, resolved:0). R7: match-or-append-or-noop, status codes, and {resolved:<changed>}.
//
// Parity notes reproduced from the TS oracle: NO cwd-containment (only a .md check — the endpoint takes
// no cwd to root a jail on; the write is same-machine/same-origin via OriginGuard); a write failure →
// HTTP 500 (index.ts:643), NOT the graceful {ok:false} lock-prd uses; an invalid decision item is
// SKIPPED per-item (the rest still process), so `decisions` is decoded leniently as []any.
type ResolveBlockerService struct {
	fs     WorkItemsFS
	writer ContentWriter
}

// NewResolveBlockerService wires the read fs + the content writer.
func NewResolveBlockerService(fs WorkItemsFS, writer ContentWriter) ResolveBlockerService {
	return ResolveBlockerService{fs: fs, writer: writer}
}

// ResolveBlocker validates + applies the decisions to prdPath. decisions is the lenient []any decode of
// the JSON body's decisions[]; each element is expected to be an object with string question+decision,
// and any element that isn't is skipped (parity with the TS per-item `continue`).
func (s ResolveBlockerService) ResolveBlocker(prdPath string, decisions []any) (int, any) {
	if prdPath == "" || !strings.HasSuffix(prdPath, ".md") || len(decisions) == 0 {
		return 422, map[string]any{"error": "prdPath (.md) and a non-empty decisions[] are required"}
	}
	md, ok := s.fs.Read(prdPath)
	if !ok {
		return 404, map[string]any{"error": "PRD not found"}
	}

	valid, changed := 0, 0
	for _, d := range decisions {
		q, dec, ok := decisionFields(d)
		if !ok {
			continue // non-object or missing/empty question|decision → skip (TS per-item continue)
		}
		valid++
		if next := domain.ResolveQuestion(md, q, dec); next != md {
			md = next
			changed++
			continue
		}
		// No pre-authored bullet matched — append so the operator is never dead-ended.
		if appended := domain.AppendResolvedQuestion(md, q, dec); appended != md {
			md = appended
			changed++
		}
		// else: already recorded — an idempotent no-op (R5), still a valid resolve; do not 422.
	}

	// Only an all-empty/all-invalid decisions[] is a client error; a valid-but-already-recorded decision
	// is an idempotent success (changed==0, valid>0) → let the resume proceed.
	if valid == 0 {
		return 422, map[string]any{"error": "no matching unresolved questions"}
	}
	if changed > 0 {
		if err := s.writer.WriteFile(prdPath, md); err != nil {
			return 500, map[string]any{"error": err.Error()} // parity: index.ts:643 returns 500
		}
	}
	return 200, map[string]any{"resolved": changed}
}

// decisionFields extracts a decision item's trimmed question + decision, or ok=false when the item is
// not an object or either field is missing / not a string / empty after trim (TS index.ts:615 guard).
func decisionFields(d any) (question, decision string, ok bool) {
	m, isMap := d.(map[string]any)
	if !isMap {
		return "", "", false
	}
	q, qok := m["question"].(string)
	dec, dok := m["decision"].(string)
	if !qok || !dok || strings.TrimSpace(q) == "" || strings.TrimSpace(dec) == "" {
		return "", "", false
	}
	return q, dec, true
}
