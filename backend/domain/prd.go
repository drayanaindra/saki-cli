package domain

import (
	"regexp"
	"strings"
)

// Pure PRD/review path + score helpers — faithful port of the PURE functions in
// apps/server/src/prd.ts (extractScore, reviewPathFor, reviewStatePathFor). Every fs touch lives in
// usecase/infra (R1: no writer, no fs here). R7: byte-identical outputs to the TS oracle.

var scoreRe = regexp.MustCompile(`(?i)Quality Gate:?\s*([0-9]{1,3})\s*/\s*100`)

// ExtractScore pulls the self-gate score ("Quality Gate: 93/100" → "93/100") from a PRD, or "" when
// absent. Mirrors prd.ts:5 extractScore (which returns null → we use "" and the caller emits JSON null).
func ExtractScore(md string) string {
	m := scoreRe.FindStringSubmatch(md)
	if m == nil {
		return ""
	}
	return m[1] + "/100"
}

// ReviewPathFor derives the /prd-review companion path: prd-foo.md → prd-foo-review.md. Mirrors
// prd.ts:29 (path.replace(/\.md$/,'-review.md')) — only a TRAILING .md is rewritten.
func ReviewPathFor(prdPath string) string {
	if strings.HasSuffix(prdPath, ".md") {
		return prdPath[:len(prdPath)-len(".md")] + "-review.md"
	}
	return prdPath
}

// ReviewStatePathFor derives the autonomous prd-review state-file path. Mirrors prd.ts:35:
// basename(prdPath) → strip leading "prd-" → strip trailing ".md" → `${cwd}/tasks/.prd-review-<slug>-state.json`.
// Raw string interpolation (NOT filepath.Join) so the returned path is byte-identical to TS.
func ReviewStatePathFor(prdPath, cwd string) string {
	base := prdPath
	if idx := strings.LastIndex(base, "/"); idx >= 0 {
		base = base[idx+1:]
	}
	base = strings.TrimPrefix(base, "prd-")
	base = strings.TrimSuffix(base, ".md")
	return cwd + "/tasks/.prd-review-" + base + "-state.json"
}
