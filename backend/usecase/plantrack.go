package usecase

import (
	"strings"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

// PlanTrackService serves the three plan-track write routes — POST /api/roadmap/plan-start | plan-ship |
// plan-attach — the studio calls to move a Plan-track (Improvement/Bug) card through its lifecycle in
// `tasks/roadmap.md`. Faithful port of the index.ts:687/709/731 endpoints + the pure roadmap.ts writers.
//
// Parity guarantees (R3/R4/R6/R7): R3 cwd-contained + id-validated (a bad/escaping cwd or id → 422,
// nothing written outside cwd, never 500); R4 guarded-mutation + idempotent (the pure domain transition
// no-ops unless the block matches); R6 graceful — a missing roadmap → {ok:true,changed:false} at 200, a
// read/write failure → {ok:false,error} at 200 (the lock-prd shape, NOT resolve-blocker's 500).
type PlanTrackService struct {
	fs     RoadmapFS
	writer ContentWriter
}

// NewPlanTrackService wires the read-only roadmap fs + the content writer.
func NewPlanTrackService(fs RoadmapFS, writer ContentWriter) PlanTrackService {
	return PlanTrackService{fs: fs, writer: writer}
}

// PlanStart flips a Planned Plan-track item → In-progress. date is injected for deterministic parity.
func (s PlanTrackService) PlanStart(cwd, id, date string) (int, any) {
	return s.runWrite(cwd, id, nil, func(c string) (string, bool) { return domain.StartPlanTrack(c, id, date) })
}

// PlanShip advances an In-progress (or Planned) Plan-track item → Shipped.
func (s PlanTrackService) PlanShip(cwd, id, date string) (int, any) {
	return s.runWrite(cwd, id, nil, func(c string) (string, bool) { return domain.ShipPlanTrack(c, id, date) })
}

// PlanAttach records a Plan-track item's `**Child plan:**` file. planFile is guarded (non-empty,
// single-line, ≤256) after the id check and before touching the file (parity with index.ts:731 order).
func (s PlanTrackService) PlanAttach(cwd, id, planFile string) (int, any) {
	guard := func() (int, any) {
		if !validPlanFile(planFile) {
			return 422, map[string]any{"error": "planFile must be a non-empty single-line filename"}
		}
		return 0, nil
	}
	return s.runWrite(cwd, id, guard, func(c string) (string, bool) { return domain.RecordChildPlan(c, id, planFile) })
}

// runWrite is the shared resolve → validate → read → apply → write pipeline for all three routes. guard
// (nil for start/ship; the planFile check for attach) runs after id-validation. apply is the pure
// domain transition. Never 500s (R3/R6): a bad cwd/id/planFile → 422; a missing roadmap → changed:false;
// a read/write failure → graceful {ok:false} at 200.
func (s PlanTrackService) runWrite(cwd, id string, guard func() (int, any), apply func(string) (string, bool)) (int, any) {
	resolved := domain.ResolveRoadmapPath(cwd)
	if !resolved.OK {
		return resolved.Status, map[string]any{"error": resolved.Error} // R3: 422 on missing/escaping cwd
	}
	if !domain.MatchItemID(id) {
		return 422, map[string]any{"error": `id must match /^[EFIB]\d+$/`}
	}
	if guard != nil {
		if status, body := guard(); status != 0 {
			return status, body
		}
	}
	if !s.fs.Exists(resolved.AbsPath) {
		return 200, map[string]any{"ok": true, "changed": false} // no roadmap → nothing to flip
	}
	content, ok := s.fs.Read(resolved.AbsPath)
	if !ok {
		// A read failure after the exists check (race/perm) — never apply onto "" and clobber (R6).
		return 200, map[string]any{"ok": false, "error": "read failed"}
	}
	next, changed := apply(content)
	if changed {
		if err := s.writer.WriteFile(resolved.AbsPath, next); err != nil {
			return 200, map[string]any{"ok": false, "error": err.Error()} // R6: graceful, never 500
		}
	}
	return 200, map[string]any{"ok": true, "changed": changed}
}

// validPlanFile mirrors the index.ts:731 planFile guard: non-empty after trim, single-line (no CR/LF),
// ≤256. The byte-length cap matches the TS `.length > 256` for the ASCII filenames this path ever sees.
func validPlanFile(planFile string) bool {
	if strings.TrimSpace(planFile) == "" {
		return false
	}
	if strings.ContainsAny(planFile, "\n\r") {
		return false
	}
	return len(planFile) <= 256
}
