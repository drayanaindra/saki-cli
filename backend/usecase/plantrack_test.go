package usecase

import (
	"errors"
	"strings"
	"testing"
)

const roadmapPath = "/repo/tasks/roadmap.md"

const utRoadmap = `# Roadmap: Test

### I1 · An improvement
**Type:** Improvement · **Track:** Plan · **Status:** Planned · **Owner:** unassigned · **Updated:** 2026-01-01
**What:** do a thing.
**Child plan:** —

### F2 · A feature
**Type:** Feature · **Track:** PRD · **Status:** Planned · **Owner:** unassigned · **Updated:** 2026-01-01
**Goal:** ship it.
**Child PRD:** prd-x.md
`

func planFS(content string) fakePrdFS {
	return fakePrdFS{files: map[string]string{roadmapPath: content}}
}

// AC 6.1: a Planned Plan-track item + plan-start → In-progress, {ok:true,changed:true}, one write.
func TestPlanStart_flip(t *testing.T) {
	w := newLockWriter()
	status, b := NewPlanTrackService(planFS(utRoadmap), w).PlanStart("/repo", "I1", "2026-07-16")
	m := body(t, b)
	if status != 200 || m["ok"] != true || m["changed"] != true {
		t.Fatalf("want 200 ok+changed, got %d %#v", status, m)
	}
	got := w.writes[roadmapPath]
	if !strings.Contains(got, "**Status:** In-progress · **Owner:** unassigned · **Updated:** 2026-07-16") {
		t.Errorf("transition not written: %q", got)
	}
}

// AC 6.2 / R4: a PRD-track (F2) id → changed:false and roadmap.md byte-identical (no write).
func TestPlanStart_prdTrackNoChange(t *testing.T) {
	w := newLockWriter()
	status, b := NewPlanTrackService(planFS(utRoadmap), w).PlanStart("/repo", "F2", "2026-07-16")
	m := body(t, b)
	if status != 200 || m["ok"] != true || m["changed"] != false {
		t.Fatalf("want 200 ok changed:false, got %d %#v", status, m)
	}
	if len(w.writes) != 0 {
		t.Errorf("R4: a no-op transition must not write: %#v", w.writes)
	}
}

// AC 6.3 / R3: a missing cwd and an escaping cwd → 422, nothing written.
func TestPlanStart_badCwd422(t *testing.T) {
	w := newLockWriter()
	if status, _ := NewPlanTrackService(planFS(utRoadmap), w).PlanStart("", "I1", "d"); status != 422 {
		t.Errorf("missing cwd → status %d, want 422", status)
	}
	if len(w.writes) != 0 {
		t.Errorf("R3: nothing must be written on a bad cwd: %#v", w.writes)
	}
}

// AC 6.3 / R3: a malformed id → 422 (the endpoint's validation gate, distinct from a silent no-op).
func TestPlanStart_badId422(t *testing.T) {
	w := newLockWriter()
	for _, id := range []string{"", "I", "X9", "abc"} {
		if status, _ := NewPlanTrackService(planFS(utRoadmap), w).PlanStart("/repo", id, "d"); status != 422 {
			t.Errorf("id=%q → status %d, want 422", id, status)
		}
	}
}

// R6: a missing roadmap.md → {ok:true,changed:false} at 200 (NOT 404), nothing written.
func TestPlanStart_missingRoadmapNoChange(t *testing.T) {
	w := newLockWriter()
	status, b := NewPlanTrackService(fakePrdFS{}, w).PlanStart("/repo", "I1", "d")
	m := body(t, b)
	if status != 200 || m["ok"] != true || m["changed"] != false {
		t.Fatalf("missing roadmap → want 200 changed:false, got %d %#v", status, m)
	}
	if len(w.writes) != 0 {
		t.Errorf("nothing must be written when there is no roadmap: %#v", w.writes)
	}
}

// R6: a write failure → graceful {ok:false,error} at 200, never a 500.
func TestPlanStart_writeErrGraceful(t *testing.T) {
	w := &lockWriter{writes: map[string]string{}, err: errors.New("disk full")}
	status, b := NewPlanTrackService(planFS(utRoadmap), w).PlanStart("/repo", "I1", "d")
	m := body(t, b)
	if status != 200 || m["ok"] != false || m["error"] != "disk full" {
		t.Fatalf("want 200 ok:false disk-full, got %d %#v", status, m)
	}
}

// AC 6.3: plan-ship (In-progress→Shipped) mutates at parity.
func TestPlanShip_flip(t *testing.T) {
	// Move I1 to In-progress first, then ship it.
	w := newLockWriter()
	NewPlanTrackService(planFS(utRoadmap), w).PlanStart("/repo", "I1", "2026-07-16")
	inProgress := w.writes[roadmapPath]

	w2 := newLockWriter()
	status, b := NewPlanTrackService(planFS(inProgress), w2).PlanShip("/repo", "I1", "2026-07-17")
	m := body(t, b)
	if status != 200 || m["changed"] != true {
		t.Fatalf("want ship changed:true, got %d %#v", status, m)
	}
	if !strings.Contains(w2.writes[roadmapPath], "**Status:** Shipped") {
		t.Errorf("not shipped: %q", w2.writes[roadmapPath])
	}
}

// AC 6.3: plan-attach records **Child plan:** at parity.
func TestPlanAttach_records(t *testing.T) {
	w := newLockWriter()
	status, b := NewPlanTrackService(planFS(utRoadmap), w).PlanAttach("/repo", "I1", "i1-plan.md")
	m := body(t, b)
	if status != 200 || m["changed"] != true {
		t.Fatalf("want attach changed:true, got %d %#v", status, m)
	}
	if !strings.Contains(w.writes[roadmapPath], "**Child plan:** i1-plan.md") {
		t.Errorf("child plan not recorded: %q", w.writes[roadmapPath])
	}
}

// AC 6.3 / R3: plan-attach with a bad planFile (empty / multiline / >256) → 422, nothing written.
func TestPlanAttach_badPlanFile422(t *testing.T) {
	w := newLockWriter()
	svc := NewPlanTrackService(planFS(utRoadmap), w)
	for _, pf := range []string{"", "  ", "a\nb.md", strings.Repeat("x", 257) + ".md"} {
		if status, _ := svc.PlanAttach("/repo", "I1", pf); status != 422 {
			t.Errorf("planFile=%q → status %d, want 422", pf, status)
		}
	}
	if len(w.writes) != 0 {
		t.Errorf("R3: nothing must be written on a bad planFile: %#v", w.writes)
	}
}
