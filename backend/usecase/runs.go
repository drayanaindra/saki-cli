package usecase

import (
	"sort"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

// ListService returns the UNION of the Go backend's runs and apps/server's runs, so the studio's
// single GET /api/runs shows every run during coexistence (the review's union-merge resolution).
// Dedup is by id (the Go/local copy wins), sorted newest-first. As of F6 builds are Go-owned, so the
// build recovery summary (recovering / parked / awaiting) is computed here from the Go run's
// chain-state + its durable output — not the hardcoded defaults it used to return.
type ListService struct {
	store  RunStore
	proxy  Proxy
	output FullOutput // reads a finalized build's <id>.out for parkedReason/awaitingDecision (may be nil)
}

func NewListService(store RunStore, proxy Proxy, output FullOutput) ListService {
	return ListService{store: store, proxy: proxy, output: output}
}

// List merges local + upstream runs. cwd (optional) filters to one repo. cookie carries the
// operator session to the upstream fetch. An upstream error is non-fatal — the local runs still
// return (degraded, never a 500), since Go owns the routes it serves.
func (l ListService) List(cwd, cookie string) []map[string]any {
	seen := map[string]bool{}
	out := []map[string]any{}

	for _, r := range l.store.List() { // local (Go) wins on id collisions
		v := l.runView(r)
		if cwd != "" && !cwdMatch(v, cwd) {
			continue
		}
		seen[r.ID] = true
		out = append(out, v)
	}
	if upstream, err := l.proxy.GetRuns(cookie); err == nil {
		for _, v := range upstream {
			id, _ := v["id"].(string)
			if id == "" || seen[id] {
				continue // dedup by id — a run rehydrated by both appears once (local wins)
			}
			if cwd != "" && !cwdMatch(v, cwd) {
				continue
			}
			seen[id] = true
			out = append(out, v)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return startedAt(out[i]) > startedAt(out[j]) }) // newest-first
	return out
}

// runView renders a local Run as an apps/server-compatible RunSummary object (mirrors
// runManager.ts:110 RunSummary + :570-588 list()). The auto-recovery fields (resumeCount, recovering,
// resumeAt, parkedReason, awaitingDecision, idempotencyKey) reflect the run's LIVE build chain-state so
// the UI follows the chain and re-adopts the successor runId (AC 1.4). parkedReason/awaitingDecision
// are derived from the durable <id>.out only for a finalized, not-recovering build (parity with the
// parkedReasonOf/awaitingDecisionOf guards, runManager.ts:387-403).
func (l ListService) runView(r domain.Run) map[string]any {
	var meta any
	if r.Meta != nil {
		meta = map[string]any{"kind": string(r.Meta.Kind), "laneKey": r.Meta.LaneKey}
	}
	v := map[string]any{
		"id":               r.ID,
		"status":           r.Status,
		"prompt":           r.Prompt,
		"cwd":              r.Cwd,
		"startedAt":        r.StartedAt,
		"meta":             meta,
		"resumeCount":      r.ResumeCount,
		"noProgressStreak": r.NoProgressStreak,
		"recovering":       r.PendingResume != nil,
		"idempotencyKey":   r.IdempotencyKey,
	}
	if r.PendingResume != nil {
		v["resumeAt"] = r.PendingResume.ResumeAt
	}
	if l.output != nil && kindOf(r) == "build" && r.Status != domain.StatusRunning && r.PendingResume == nil {
		lines, _ := l.output.ReadAll(r.ID)
		if reason := parkedReasonOf(lines); reason != "" {
			v["parkedReason"] = reason
		}
		if d := ParseDecision(FinalSpokenText(lines)); d != nil {
			v["awaitingDecision"] = d
		}
	}
	return v
}

func cwdMatch(v map[string]any, cwd string) bool {
	switch c := v["cwd"].(type) {
	case string:
		return c == cwd
	case *string:
		return c != nil && *c == cwd
	}
	return false
}

func startedAt(v map[string]any) int64 {
	switch n := v["startedAt"].(type) {
	case int64:
		return n
	case float64:
		return int64(n)
	}
	return 0
}
