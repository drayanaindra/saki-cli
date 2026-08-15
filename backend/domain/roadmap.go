package domain

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Pure core for the board's read path: parse `tasks/roadmap.md` into structured items + resolve/
// contain the roadmap file path. Faithful port of apps/server/src/roadmap.ts (parseRoadmap,
// resolveRoadmapPath). Parsing is LINE-ANCHORED and DEGRADES on malformed input (skips a block it
// can't read) rather than erroring — the board is a READ view (R1), it never mutates the file.

// EpicStatus is one of the four work-item statuses. String, not enum, so JSON round-trips at parity.
type EpicStatus = string

var validStatuses = map[string]bool{"Planned": true, "In-progress": true, "Shipped": true, "Blocked": true}

// The four work-item types and their two tracks (E13 §10). TYPE derives from the header prefix
// (E/F/I/B); TRACK is a pure function of type — Epic/Feature route through PRD, Improvement/Bug
// through Plan. A cross (e.g. a Bug on PRD) is a mis-routing bug, so trackByType is its sole source.
var typeByPrefix = map[string]string{"E": "Epic", "F": "Feature", "I": "Improvement", "B": "Bug"}
var trackByType = map[string]string{"Epic": "PRD", "Feature": "PRD", "Improvement": "Plan", "Bug": "Plan"}

// RoadmapItem is one parsed `### <E|F|I|B><n>` block. JSON shape mirrors the TS RoadmapItem exactly.
type RoadmapItem struct {
	ID         string  `json:"id"` // full prefixed id, e.g. 'E5', 'F2', 'I1'
	N          int     `json:"n"`
	Type       string  `json:"type"`
	Track      string  `json:"track"`
	Title      string  `json:"title"`
	Status     string  `json:"status"`
	Goal       string  `json:"goal"`
	ChildPrd   *string `json:"childPrd"`   // null when `—`/absent
	ChildPlan  *string `json:"childPlan"`  // Plan-track `**Child plan:**`; null when `—`/absent
	PhaseChain *string `json:"phaseChain"` // parent recut chain; null when absent
}

// Field matchers. PRODUCT/GOAL/CHILD stay anchored to line-start (still line-first). STATUS is the
// deliberate exception (roadmap.ts:52-58): the released /add writes `**Type:** … · **Track:** … ·
// **Status:** …`, so Status is no longer line-first — match the `**Status:**` sentinel anywhere on
// its line. The TS lookahead `(?=\s+·|\s*$)` is RE2-incompatible; the READ path only consumes group
// 1, so a CONSUMING group `(?:\s+·|\s*$)` yields a byte-identical capture (plan §"RE2 fidelity").
var (
	productRe    = regexp.MustCompile(`(?m)^#\s+Roadmap:\s*(.+?)\s*$`)
	itemHeaderRe = regexp.MustCompile(`(?m)^###\s+([EFIB])(\d+)\s*·\s*(.+?)\s*$`)
	statusRe     = regexp.MustCompile(`(?m)\*\*Status:\*\*\s*(.+?)(?:\s+·|\s*$)`)
	goalRe       = regexp.MustCompile(`(?m)^\*\*Goal:\*\*\s*(.+?)\s*$`)
	whatRe       = regexp.MustCompile(`(?m)^\*\*What:\*\*\s*(.+?)\s*$`)
	childPrdRe   = regexp.MustCompile(`(?m)^\*\*Child PRD:\*\*\s*(.+?)(?:\s+·|\s*$)`)
	childPlanRe  = regexp.MustCompile(`(?m)^\*\*Child plan:\*\*\s*(.+?)(?:\s+·|\s*$)`)
	phaseChainRe = regexp.MustCompile(`(?m)^\*\*Phase chain:\*\*\s*(.+?)\s*$`)
)

// firstGroup returns the trimmed capture group 1 of re over s, or "" (with ok=false) when no match.
func firstGroup(re *regexp.Regexp, s string) (string, bool) {
	m := re.FindStringSubmatch(s)
	if m == nil {
		return "", false
	}
	return strings.TrimSpace(m[1]), true
}

// dashNull maps the TS `raw && raw !== '—' ? raw : null` idiom: a `—` or empty value → null pointer.
func dashNull(re *regexp.Regexp, s string) *string {
	v, ok := firstGroup(re, s)
	if !ok || v == "" || v == "—" {
		return nil
	}
	return &v
}

// parseEpicBody parses one block's body (roadmap.ts:81). Returns nil (skip) when the block has no
// valid Status — never a hard error, so a malformed block degrades rather than crashing the board.
func parseEpicBody(prefix string, n int, title, body string) *RoadmapItem {
	status, ok := firstGroup(statusRe, body)
	if !ok || !validStatuses[status] {
		return nil // degrade: skip a block with no valid status
	}
	goal := ""
	if g, ok := firstGroup(goalRe, body); ok {
		goal = g
	} else if w, ok := firstGroup(whatRe, body); ok {
		goal = w
	}
	typ := typeByPrefix[prefix]
	return &RoadmapItem{
		ID:         prefix + strconv.Itoa(n),
		N:          n,
		Type:       typ,
		Track:      trackByType[typ],
		Title:      strings.TrimSpace(title),
		Status:     status,
		Goal:       goal,
		ChildPrd:   dashNull(childPrdRe, body),
		ChildPlan:  dashNull(childPlanRe, body),
		PhaseChain: dashNull(phaseChainRe, body),
	}
}

// ParseRoadmap parses the full roadmap.md text into (productName, items) — line-anchored + degrading:
// an unreadable block is skipped, a file with zero readable blocks yields an empty slice, never an
// error. Faithful port of roadmap.ts:98 (parseRoadmap).
func ParseRoadmap(content string) (productName *string, items []RoadmapItem) {
	items = []RoadmapItem{}
	if p, ok := firstGroup(productRe, content); ok {
		productName = &p
	}
	// Every `### <E|F|I|B><n>` header; the body of block i runs from the END of its header match to
	// the END of the next header match (parity: TS uses ITEM_HEADER.lastIndex for both bounds,
	// roadmap.ts:107,111). Consuming the next header line is harmless — only the FIRST field match
	// (block i's own) is read.
	idx := itemHeaderRe.FindAllStringSubmatchIndex(content, -1)
	for i := range idx {
		start := idx[i][1]
		bodyEnd := len(content)
		if i+1 < len(idx) {
			bodyEnd = idx[i+1][1]
		}
		prefix := content[idx[i][2]:idx[i][3]]
		n, _ := strconv.Atoi(content[idx[i][4]:idx[i][5]])
		title := content[idx[i][6]:idx[i][7]]
		if item := parseEpicBody(prefix, n, title, content[start:bodyEnd]); item != nil {
			items = append(items, *item)
		}
	}
	return productName, items
}

// RoadmapPath is the resolve+contain result: OK with AbsPath, or a 422 with a client-facing message.
type RoadmapPath struct {
	OK      bool
	AbsPath string
	Status  int
	Error   string
}

// ResolveRoadmapPath resolves + contains the roadmap file for a repo (roadmap.ts:232). Containment/
// cwd validation ONLY — existence is checked by the caller so a missing file degrades to found:false
// rather than a hard reject. A crafted cwd whose join escapes root → 422.
func ResolveRoadmapPath(cwd string) RoadmapPath {
	if cwd == "" {
		return RoadmapPath{Status: 422, Error: "cwd is required"}
	}
	root, err := filepath.Abs(cwd)
	if err != nil {
		return RoadmapPath{Status: 422, Error: "cwd is required"}
	}
	absPath := filepath.Join(root, "tasks", "roadmap.md")
	if absPath != root && !strings.HasPrefix(absPath, root+string(filepath.Separator)) {
		return RoadmapPath{Status: 422, Error: "path escapes cwd"}
	}
	return RoadmapPath{OK: true, AbsPath: absPath}
}
