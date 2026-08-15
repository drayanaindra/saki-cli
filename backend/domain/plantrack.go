package domain

import (
	"regexp"
	"strconv"
	"strings"
)

// Plan-track roadmap WRITE path (F4 slice 6): the three block-scoped transitions the studio applies to
// `tasks/roadmap.md`. Faithful port of the PURE functions in apps/server/src/roadmap.ts
// (startPlanTrack:181, shipPlanTrack:189, recordChildPlan:200 + the shared transitionPlanTrackStatus:157
// / findItemBlockSpan:136). Pure, no fs — the usecase wires containment + read/write.
//
// Guarantees carried from the TS oracle (R4 🔒 / R7): BLOCK-SCOPED (only the target id's `### <p><n>`
// block is edited — a sibling's `**Status:** Planned` elsewhere is never touched); PLAN-TRACK-ONLY (an
// E/F id is a no-op); gated + IDEMPOTENT (a status flip only fires when the current status is in `from`).

// The write regexes are the RE2 counterparts of the read statusRe/childPlanRe. RE2 has no lookahead, so
// where the TS write path relies on a zero-width `(?=\s+·|\s*$)` to leave the metadata line's ` · … `
// tail in place, we capture that separator as a 3rd group and splice it back (replaceFirstField). The
// value/separator split mirrors the read regexes exactly so a written line re-parses identically (R7).
var (
	planIDRe         = regexp.MustCompile(`^([EFIB])(\d+)$`)
	statusWriteRe    = regexp.MustCompile(`(?m)\*\*Status:\*\*\s*(.+?)(\s+·|\s*$)`)
	childPlanWriteRe = regexp.MustCompile(`(?m)^\*\*Child plan:\*\*\s*(.+?)(\s+·|\s*$)`)
	updatedWriteRe   = regexp.MustCompile(`(?m)(\*\*Updated:\*\*\s*)(\S+)`)
)

// MatchItemID reports whether id is a syntactically valid work-item id `^[EFIB]\d+$`. It is the write
// endpoints' 422 gate — distinct from a transition's silent no-op on a well-formed but PRD-track /
// unknown id (index.ts:691 validates the shape before touching the file).
func MatchItemID(id string) bool { return planIDRe.MatchString(id) }

// StartPlanTrack flips a Plan-track item Planned → In-progress (roadmap.ts:181). No-op (changed=false,
// content unchanged) unless the id is a Plan-track block currently in `Planned`. date bumps **Updated:**.
func StartPlanTrack(content, id, date string) (string, bool) {
	return transitionPlanTrackStatus(content, id, []string{"Planned"}, "In-progress", date)
}

// ShipPlanTrack advances a Plan-track item In-progress (or Planned, defensively) → Shipped
// (roadmap.ts:189). Idempotent: an already-Shipped/Blocked item is a byte-identical no-op.
func ShipPlanTrack(content, id, date string) (string, bool) {
	return transitionPlanTrackStatus(content, id, []string{"In-progress", "Planned"}, "Shipped", date)
}

// RecordChildPlan replaces a Plan-track item's `**Child plan:**` value with planFile (roadmap.ts:200).
// No-op when the id is malformed / PRD-track, planFile is blank, the block has no `**Child plan:**`
// field, or the value already equals planFile (idempotent). Block-scoped; planFile is spliced literally
// (a `$` in a filename is never read as a replacement token).
func RecordChildPlan(content, id, planFile string) (string, bool) {
	prefix, n, ok := planTrackTarget(id)
	if !ok {
		return content, false
	}
	file := strings.TrimSpace(planFile)
	if file == "" {
		return content, false
	}
	start, end, found := findItemBlockSpan(content, prefix, n)
	if !found {
		return content, false
	}
	block := content[start:end]
	current, hasField := firstGroup(childPlanRe, block)
	if !hasField {
		return content, false // no `**Child plan:**` field to write into (never injects)
	}
	if current == file {
		return content, false // already attached → idempotent no-op
	}
	next := replaceFirstField(childPlanWriteRe, block, "**Child plan:** ", file)
	return content[:start] + next + content[end:], true
}

// transitionPlanTrackStatus is the shared engine: flip the target id's `**Status:**` from one of `from`
// to `to` within its block (and bump **Updated:** when date is non-empty). Mirrors roadmap.ts:157.
func transitionPlanTrackStatus(content, id string, from []string, to, date string) (string, bool) {
	prefix, n, ok := planTrackTarget(id)
	if !ok {
		return content, false // malformed id or PRD-track → no-op
	}
	start, end, found := findItemBlockSpan(content, prefix, n)
	if !found {
		return content, false // id not found → no-op
	}
	block := content[start:end]
	current, hasStatus := firstGroup(statusRe, block)
	if !hasStatus || !containsStr(from, current) {
		return content, false // gated + idempotent: current status not in `from`
	}
	next := replaceFirstField(statusWriteRe, block, "**Status:** ", to)
	if date != "" {
		next = replaceFirstUpdated(next, date)
	}
	return content[:start] + next + content[end:], true
}

// planTrackTarget parses id and returns (prefix, n, ok) only when id is a well-formed Plan-track id
// (Improvement/Bug). A malformed id or a PRD-track (E/F) id → ok=false, the shared no-op gate.
func planTrackTarget(id string) (prefix string, n int, ok bool) {
	m := planIDRe.FindStringSubmatch(id)
	if m == nil {
		return "", 0, false
	}
	if trackByType[typeByPrefix[m[1]]] != "Plan" {
		return "", 0, false
	}
	num, _ := strconv.Atoi(m[2])
	return m[1], num, true
}

// findItemBlockSpan returns the [start, end) offsets of item `<prefix><n>`'s block — from its `### `
// header START to the next item header's start (or EOF). Splicing the whole block keeps the edit
// scoped. Note: unlike ParseRoadmap (which uses the header END as the body start), the write path needs
// the header START so the returned slice includes the header. Mirrors roadmap.ts:136.
func findItemBlockSpan(content, prefix string, n int) (start, end int, ok bool) {
	idx := itemHeaderRe.FindAllStringSubmatchIndex(content, -1)
	for i := range idx {
		if content[idx[i][2]:idx[i][3]] != prefix {
			continue
		}
		num, _ := strconv.Atoi(content[idx[i][4]:idx[i][5]])
		if num != n {
			continue
		}
		end = len(content)
		if i+1 < len(idx) {
			end = idx[i+1][0] // next header's START (m.index parity)
		}
		return idx[i][0], end, true
	}
	return 0, 0, false
}

// replaceFirstField replaces the FIRST match of re (a 2-group `value`,`separator` regex) in s,
// rebuilding it as label+newVal+separator via index splice — no ReplaceAll (first-match parity with the
// TS `.replace` without `/g`) and no `$`-token expansion (a `$` in newVal stays literal, unlike
// ReplaceAllString). The separator (group 2) is the ` · … `/EOL the TS zero-width lookahead left intact.
func replaceFirstField(re *regexp.Regexp, s, label, newVal string) string {
	loc := re.FindStringSubmatchIndex(s)
	if loc == nil {
		return s
	}
	sep := s[loc[4]:loc[5]]
	return s[:loc[0]] + label + newVal + sep + s[loc[1]:]
}

// replaceFirstUpdated bumps the FIRST `**Updated:** <date>` in s, preserving the captured
// `**Updated:** ` prefix (group 1) and replacing only the date token (group 2). Mirrors the TS
// UPDATED_RE.replace(…, `$1<date>`). A no-op when the block has no **Updated:** line.
func replaceFirstUpdated(s, date string) string {
	loc := updatedWriteRe.FindStringSubmatchIndex(s)
	if loc == nil {
		return s
	}
	return s[:loc[0]] + s[loc[2]:loc[3]] + date + s[loc[1]:]
}

// containsStr reports whether x is in xs (the `from`-set membership test for a status transition).
func containsStr(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}
