package domain

import (
	"regexp"
	"strings"
)

// RESOLVED-bullet write-back for a PRD's Open-Questions section — faithful port of the PURE functions
// in apps/server/src/prdQuestions.ts (resolveQuestion:81, appendResolvedQuestion:107). Pure, no fs (R1).
// R7: byte-identical output to the TS oracle. R5 idempotency: an already-resolved bullet is left
// untouched; an already-present appended bullet is a no-op. Reuses ParseOpenQuestions + the
// headingRe/openQMarkerRe/resolvedRe vars declared in blockers.go.

const openQMarker = "**Open questions:**"

var (
	bulletLineRe = regexp.MustCompile(`^(\s*[-*]\s+)(.*)$`)
	wsCollapseRe = regexp.MustCompile(`\s+`)
)

// ResolveQuestion marks an open-question bullet RESOLVED by prefixing its body with
// "✅ RESOLVED — <decision> — ". The question is matched by parsed title (scoped to Open Questions) OR
// by full body; an already-resolved bullet is left untouched (idempotent). Mirrors prdQuestions.ts:81.
func ResolveQuestion(md, questionText, decision string) string {
	q := strings.TrimSpace(questionText)
	targetBody := q
	for _, oq := range ParseOpenQuestions(md) {
		if !oq.Resolved && oq.Title == q {
			targetBody = oq.Text
			break
		}
	}
	dec := strings.TrimSpace(decision)
	lines := strings.Split(md, "\n")
	for i, line := range lines {
		m := bulletLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		body := strings.TrimSpace(m[2])
		if body != targetBody || resolvedRe.MatchString(body) {
			continue
		}
		lines[i] = m[1] + "✅ RESOLVED — " + dec + " — " + m[2] // m[2] = UNtrimmed body (TS parity)
	}
	return strings.Join(lines, "\n")
}

// AppendResolvedQuestion durably records a decision that has NO pre-authored bullet to mark — appending
// `- ✅ RESOLVED — <decision> — <question>` under the Open-Questions section's marker (creating the
// marker, or the whole section, when absent). Idempotent: a no-op when that exact bullet already
// exists. Mirrors prdQuestions.ts:107.
func AppendResolvedQuestion(md, questionText, decision string) string {
	q := strings.TrimSpace(wsCollapseRe.ReplaceAllString(questionText, " "))
	dec := strings.TrimSpace(decision)
	if q == "" || dec == "" {
		return md
	}
	bullet := "- ✅ RESOLVED — " + dec + " — " + q
	if strings.Contains(md, bullet) {
		return md
	}

	lines := strings.Split(md, "\n")
	secStart := -1
	for i, l := range lines {
		if headingRe.MatchString(l) && strings.Contains(strings.ToLower(l), "open question") {
			secStart = i
			break
		}
	}
	if secStart == -1 {
		tail := ""
		if !strings.HasSuffix(md, "\n") {
			tail = "\n"
		}
		return md + tail + "\n## Rabbit Holes & Open Questions\n\n" + openQMarker + "\n" + bullet + "\n"
	}
	return strings.Join(insertBulletInSection(lines, secStart, bullet), "\n")
}

// insertBulletInSection inserts bullet into the Open-Questions section that begins at secStart —
// directly after the `**Open questions:**` marker when present, else after the section's last
// non-blank line (creating the marker). Extracted from AppendResolvedQuestion verbatim.
func insertBulletInSection(lines []string, secStart int, bullet string) []string {
	secEnd := len(lines)
	for i := secStart + 1; i < len(lines); i++ {
		if headingRe.MatchString(lines[i]) {
			secEnd = i
			break
		}
	}
	markerIdx := -1
	for i := secStart + 1; i < secEnd; i++ {
		if openQMarkerRe.MatchString(lines[i]) {
			markerIdx = i
			break
		}
	}
	if markerIdx != -1 {
		return spliceInsert(lines, markerIdx+1, bullet)
	}
	insertAt := secEnd
	for insertAt > secStart+1 && strings.TrimSpace(lines[insertAt-1]) == "" {
		insertAt--
	}
	return spliceInsert(lines, insertAt, "", openQMarker, bullet)
}

// spliceInsert inserts items at index i (0 ≤ i ≤ len), matching JS Array.splice(i, 0, ...items).
func spliceInsert(lines []string, i int, items ...string) []string {
	out := make([]string, 0, len(lines)+len(items))
	out = append(out, lines[:i]...)
	out = append(out, items...)
	out = append(out, lines[i:]...)
	return out
}
