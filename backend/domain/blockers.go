package domain

import (
	"regexp"
	"strings"
)

// Blocker + open-question parsing for the board's blocked-slice panel (GET /api/blockers). Faithful
// port of apps/server/src/prdQuestions.ts (parseOpenQuestions/questionsForSlice/blockerReason). Pure —
// no fs (R1). R7: byte-identical outputs to the TS oracle. DeferredSliceNums (the `deferred` flag) and
// BuildArtifactCandidates (the progress-file path) are already ported in workitems.go and reused.

// OpenQuestion mirrors the prdQuestions.ts OpenQuestion interface — JSON field names match TS exactly
// so /api/blockers' `questions` array is byte-shape-identical (owner/deadlineSlice are null when absent).
type OpenQuestion struct {
	Text          string  `json:"text"`
	Title         string  `json:"title"`
	Owner         *string `json:"owner"`
	DeadlineSlice *int    `json:"deadlineSlice"`
	Resolved      bool    `json:"resolved"`
}

var (
	headingRe      = regexp.MustCompile(`^#{1,6}\s`)
	openQMarkerRe  = regexp.MustCompile(`(?i)^\s*\*\*\s*open questions`)
	rabbitMarkerRe = regexp.MustCompile(`(?i)^\s*\*\*\s*rabbit holes`)
	oqBulletRe     = regexp.MustCompile(`^\s*[-*]\s+(.*)$`)
	resolvedRe     = regexp.MustCompile(`(?i)✅\s*RESOLVED`)
	boldRe         = regexp.MustCompile(`\*\*(.+?)\*\*`)
	oqOwnerRe      = regexp.MustCompile(`(?i)Owner:?\s*([^—\n]+?)\s*(?:—|$)`)
	beforeSliceRe  = regexp.MustCompile(`(?i)before\s+slice\s+(\d+)`)
	trailingColon  = regexp.MustCompile(`:$`)
)

// ParseOpenQuestions parses the "Open questions:" bullets under a *…Open Questions* heading — the
// sibling "Rabbit holes:" bullets are skipped. Mirrors prdQuestions.ts:15.
func ParseOpenQuestions(md string) []OpenQuestion {
	out := []OpenQuestion{}
	inSection, inOpenQ := false, false
	for _, line := range strings.Split(md, "\n") {
		if headingRe.MatchString(line) {
			inSection = strings.Contains(strings.ToLower(line), "open question")
			inOpenQ = false
			continue
		}
		if !inSection {
			continue
		}
		if openQMarkerRe.MatchString(line) {
			inOpenQ = true
			continue
		}
		if rabbitMarkerRe.MatchString(line) {
			inOpenQ = false
			continue
		}
		if !inOpenQ {
			continue
		}
		m := oqBulletRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		body := strings.TrimSpace(m[1])
		if body == "" {
			continue
		}
		out = append(out, parseQuestionBody(body))
	}
	return out
}

// parseQuestionBody builds an OpenQuestion from a bullet body (mirrors the per-bullet fields at
// prdQuestions.ts:33-44).
func parseQuestionBody(body string) OpenQuestion {
	title := body
	if bm := boldRe.FindStringSubmatch(body); bm != nil {
		title = bm[1]
	} else {
		title = truncateRunes(body, 60)
	}
	title = strings.TrimSpace(trailingColon.ReplaceAllString(title, ""))

	q := OpenQuestion{Text: body, Title: title, Resolved: resolvedRe.MatchString(body)}
	if om := oqOwnerRe.FindStringSubmatch(body); om != nil {
		owner := strings.TrimSpace(om[1])
		q.Owner = &owner
	}
	if dm := beforeSliceRe.FindStringSubmatch(body); dm != nil {
		if n, ok := atoiInt(dm[1]); ok {
			q.DeadlineSlice = &n
		}
	}
	return q
}

// QuestionsForSlice returns the unresolved open questions gating slice n — any with a `before slice M`
// deadline where M ≤ n — sorted by deadline ascending. Mirrors prdQuestions.ts:51.
func QuestionsForSlice(md string, n int) []OpenQuestion {
	var gated []OpenQuestion
	for _, q := range ParseOpenQuestions(md) {
		if !q.Resolved && q.DeadlineSlice != nil && *q.DeadlineSlice <= n {
			gated = append(gated, q)
		}
	}
	// stable insertion sort by DeadlineSlice asc (small n; preserves document order on ties, like the
	// TS Array.sort which is stable in V8 for this comparator).
	for i := 1; i < len(gated); i++ {
		for j := i; j > 0 && *gated[j-1].DeadlineSlice > *gated[j].DeadlineSlice; j-- {
			gated[j-1], gated[j] = gated[j], gated[j-1]
		}
	}
	if gated == nil {
		return []OpenQuestion{}
	}
	return gated
}

var (
	blockedCheckboxRe = regexp.MustCompile(`^\s*[-*]\s*\[ \]`)
	blockedWordRe     = regexp.MustCompile(`(?i)BLOCKED`)
	blockFmtARe       = regexp.MustCompile(`^\s*[-*]\s*\[ \]\s*(\d+)\.`)
	blockFmtBRe       = regexp.MustCompile(`^\s*[-*]\s*\[ \]\s*\*{0,2}Slice\s+(\d+)\s*[—-]`)
	blockedReasonRe   = regexp.MustCompile(`(?i)BLOCKED:?\s*(.+)$`)
)

// BlockerReason returns the text after `BLOCKED:` on slice n's line in a /build progress scratchpad,
// or ("", false) when none. Mirrors prdQuestions.ts:58 (which returns string|null).
func BlockerReason(md string, n int) (string, bool) {
	for _, line := range strings.Split(md, "\n") {
		if !blockedCheckboxRe.MatchString(line) || !blockedWordRe.MatchString(line) {
			continue
		}
		s := strings.ReplaceAll(line, "~~", "")
		num := -1
		if mA := blockFmtARe.FindStringSubmatch(s); mA != nil {
			num, _ = atoiInt(mA[1])
		} else if mB := blockFmtBRe.FindStringSubmatch(s); mB != nil {
			num, _ = atoiInt(mB[1])
		}
		if num != n {
			continue
		}
		if rm := blockedReasonRe.FindStringSubmatch(strings.ReplaceAll(s, "**", "")); rm != nil {
			return strings.TrimSpace(rm[1]), true
		}
		return "", false
	}
	return "", false
}

// truncateRunes returns the first n runes of s (parity approximation of TS String.slice(0,n)).
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// atoiInt parses a base-10 int; ok=false on any non-integer.
func atoiInt(s string) (int, bool) {
	n := 0
	if s == "" {
		return 0, false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}
