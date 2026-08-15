package domain

import (
	"strings"
	"testing"
)

// Golden fixture: two Planned Plan-track items (I1, I4) to prove block-scoping, one PRD-track (F2), one
// In-progress Plan-track (B3) with a child plan, and one Plan-track item WITHOUT a `**Child plan:**`
// field (I5). Metadata lines carry the released /add shape (`· **Owner:** … · **Updated:** …`) so the
// tail-preservation + Updated-bump behaviour is exercised.
const planFixture = `# Roadmap: Test

### I1 · First improvement
**Type:** Improvement · **Track:** Plan · **Status:** Planned · **Owner:** unassigned · **Updated:** 2026-01-01
**What:** do a thing.
**Child plan:** —

### I4 · Second improvement
**Type:** Improvement · **Track:** Plan · **Status:** Planned · **Owner:** unassigned · **Updated:** 2026-01-01
**What:** another thing.
**Child plan:** —

### F2 · A feature
**Type:** Feature · **Track:** PRD · **Status:** Planned · **Owner:** unassigned · **Updated:** 2026-01-01
**Goal:** ship it.
**Child PRD:** prd-x.md

### B3 · A bug
**Type:** Bug · **Track:** Plan · **Status:** In-progress · **Owner:** me · **Updated:** 2026-01-02
**What:** fix it.
**Child plan:** old-plan.md

### I5 · No child-plan field
**Type:** Improvement · **Track:** Plan · **Status:** Planned · **Owner:** unassigned
**What:** legacy block without a child-plan line.
`

func TestStartPlanTrack_flip(t *testing.T) {
	out, changed := StartPlanTrack(planFixture, "I1", "2026-07-16")
	if !changed {
		t.Fatal("want changed=true for a Planned Plan-track item")
	}
	// Status flipped + Owner tail preserved + Updated bumped, all on I1's line.
	if !strings.Contains(out, "**Status:** In-progress · **Owner:** unassigned · **Updated:** 2026-07-16") {
		t.Errorf("I1 line not transitioned/tail-preserved:\n%s", out)
	}
	// Block-scoped: I4 (also Planned Plan-track) is untouched, incl. its old Updated date.
	if !strings.Contains(out, "### I4 · Second improvement\n**Type:** Improvement · **Track:** Plan · **Status:** Planned · **Owner:** unassigned · **Updated:** 2026-01-01") {
		t.Errorf("block-scope leak: I4 changed:\n%s", out)
	}
	// F2 (PRD-track) still Planned, untouched.
	if !strings.Contains(out, "### F2 · A feature\n**Type:** Feature · **Track:** PRD · **Status:** Planned") {
		t.Errorf("F2 must be untouched:\n%s", out)
	}
}

func TestStartPlanTrack_prdTrackNoop(t *testing.T) {
	out, changed := StartPlanTrack(planFixture, "F2", "2026-07-16")
	if changed || out != planFixture {
		t.Fatal("R4: a PRD-track id must be a byte-identical no-op")
	}
}

func TestStartPlanTrack_nonPlannedNoop(t *testing.T) {
	out, changed := StartPlanTrack(planFixture, "B3", "2026-07-16") // B3 is In-progress
	if changed || out != planFixture {
		t.Fatal("gated: a non-Planned item must be a byte-identical no-op")
	}
}

func TestStartPlanTrack_malformedAndUnknownNoop(t *testing.T) {
	for _, id := range []string{"", "I", "X9", "9I", "I99"} {
		out, changed := StartPlanTrack(planFixture, id, "2026-07-16")
		if changed || out != planFixture {
			t.Errorf("id=%q must be a byte-identical no-op", id)
		}
	}
}

func TestShipPlanTrack_fromInProgress(t *testing.T) {
	out, changed := ShipPlanTrack(planFixture, "B3", "2026-07-16")
	if !changed {
		t.Fatal("want changed=true shipping an In-progress item")
	}
	if !strings.Contains(out, "**Status:** Shipped · **Owner:** me · **Updated:** 2026-07-16") {
		t.Errorf("B3 not shipped/tail-preserved:\n%s", out)
	}
}

func TestShipPlanTrack_fromPlannedDefensive(t *testing.T) {
	out, changed := ShipPlanTrack(planFixture, "I1", "2026-07-16") // Planned is in ship's `from`
	if !changed || !strings.Contains(out, "### I1 · First improvement\n**Type:** Improvement · **Track:** Plan · **Status:** Shipped") {
		t.Errorf("ship must accept Planned defensively:\n%s", out)
	}
}

func TestShipPlanTrack_idempotentNoop(t *testing.T) {
	shipped, _ := ShipPlanTrack(planFixture, "B3", "2026-07-16")
	again, changed := ShipPlanTrack(shipped, "B3", "2026-07-17")
	if changed || again != shipped {
		t.Fatal("R4: shipping an already-Shipped item must be a byte-identical no-op")
	}
}

func TestRecordChildPlan_replace(t *testing.T) {
	out, changed := RecordChildPlan(planFixture, "I1", "i1-plan.md")
	if !changed || !strings.Contains(out, "### I1 · First improvement") {
		t.Fatalf("want changed=true:\n%s", out)
	}
	if !strings.Contains(out, "**Child plan:** i1-plan.md") {
		t.Errorf("child plan not written:\n%s", out)
	}
	// Block-scoped: I4's child plan is still `—`.
	if strings.Count(out, "**Child plan:** —") != 1 {
		t.Errorf("block-scope leak on child-plan write:\n%s", out)
	}
}

func TestRecordChildPlan_idempotentNoop(t *testing.T) {
	out, changed := RecordChildPlan(planFixture, "B3", "old-plan.md") // already the value
	if changed || out != planFixture {
		t.Fatal("R4: writing the same child plan must be a byte-identical no-op")
	}
}

func TestRecordChildPlan_noFieldNoop(t *testing.T) {
	out, changed := RecordChildPlan(planFixture, "I5", "x-plan.md") // I5 has no **Child plan:** line
	if changed || out != planFixture {
		t.Fatal("no `**Child plan:**` field → never injects, byte-identical no-op")
	}
}

func TestRecordChildPlan_prdTrackNoop(t *testing.T) {
	out, changed := RecordChildPlan(planFixture, "F2", "x-plan.md")
	if changed || out != planFixture {
		t.Fatal("R4: a PRD-track id must be a byte-identical no-op")
	}
}

func TestRecordChildPlan_dollarSafe(t *testing.T) {
	out, changed := RecordChildPlan(planFixture, "I1", "a$1b-plan.md")
	if !changed || !strings.Contains(out, "**Child plan:** a$1b-plan.md") {
		t.Errorf("a `$` in the filename must be spliced literally (no group expansion):\n%s", out)
	}
}

// The transitions never touch a read: a written line must re-parse to the value just written (R7 round-trip).
func TestStartPlanTrack_roundTripsThroughParser(t *testing.T) {
	out, _ := StartPlanTrack(planFixture, "I1", "2026-07-16")
	_, items := ParseRoadmap(out)
	for _, it := range items {
		if it.ID == "I1" && it.Status != "In-progress" {
			t.Fatalf("written status did not re-parse: %q", it.Status)
		}
	}
}
