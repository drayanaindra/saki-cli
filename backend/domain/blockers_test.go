package domain

import "testing"

const oqFixture = `# PRD

## Rabbit Holes & Open Questions
**Rabbit holes:**
- A rabbit hole bullet that must NOT be parsed as an open question

**Open questions:**
- **DB choice** — Owner: alice — before slice 2. Which store?
- ✅ RESOLVED — postgres — **Cache layer** before slice 3
- Plain question before slice 5 with no owner

## Non-Goals
- **Decoy** before slice 1 — outside the Open Questions section, must be ignored
`

func TestParseOpenQuestions(t *testing.T) {
	qs := ParseOpenQuestions(oqFixture)
	if len(qs) != 3 {
		t.Fatalf("parsed %d questions, want 3: %#v", len(qs), qs)
	}
	// Bullet 1: bold title, owner, deadline, unresolved.
	if qs[0].Title != "DB choice" {
		t.Errorf("q0 title = %q, want DB choice", qs[0].Title)
	}
	if qs[0].Owner == nil || *qs[0].Owner != "alice" {
		t.Errorf("q0 owner = %v, want alice", qs[0].Owner)
	}
	if qs[0].DeadlineSlice == nil || *qs[0].DeadlineSlice != 2 {
		t.Errorf("q0 deadline = %v, want 2", qs[0].DeadlineSlice)
	}
	if qs[0].Resolved {
		t.Errorf("q0 should be unresolved")
	}
	// Bullet 2: resolved, bold title, no owner.
	if !qs[1].Resolved || qs[1].Title != "Cache layer" || qs[1].Owner != nil {
		t.Errorf("q1 = %#v, want resolved+Cache layer+no owner", qs[1])
	}
	if qs[1].DeadlineSlice == nil || *qs[1].DeadlineSlice != 3 {
		t.Errorf("q1 deadline = %v, want 3", qs[1].DeadlineSlice)
	}
	// Bullet 3: no bold → body-as-title, no owner, deadline 5.
	if qs[2].Owner != nil || qs[2].DeadlineSlice == nil || *qs[2].DeadlineSlice != 5 {
		t.Errorf("q2 = %#v, want no owner + deadline 5", qs[2])
	}
	if qs[2].Title != "Plain question before slice 5 with no owner" {
		t.Errorf("q2 title = %q", qs[2].Title)
	}
}

func TestQuestionsForSlice(t *testing.T) {
	// n=3: q0 (deadline 2, unresolved) only; q1 resolved, q2 deadline 5 > 3.
	if got := QuestionsForSlice(oqFixture, 3); len(got) != 1 || got[0].Title != "DB choice" {
		t.Fatalf("n=3 → %#v, want [DB choice]", got)
	}
	// n=5: q0 (2) then q2 (5), sorted by deadline asc; q1 excluded (resolved).
	got := QuestionsForSlice(oqFixture, 5)
	if len(got) != 2 || *got[0].DeadlineSlice != 2 || *got[1].DeadlineSlice != 5 {
		t.Fatalf("n=5 → %#v, want [d2, d5]", got)
	}
	// n=1: none gate slice 1.
	if got := QuestionsForSlice(oqFixture, 1); len(got) != 0 {
		t.Fatalf("n=1 → %#v, want []", got)
	}
	// Never nil (JSON [] parity).
	if QuestionsForSlice("", 9) == nil {
		t.Fatal("want non-nil empty slice")
	}
}

const progFixture = `# Build progress

- [x] 1. Done slice — shipped
- [ ] 2. Some slice — BLOCKED: waiting on decision X
- [ ] ~~Slice 3 — Title~~ — **BLOCKED**: another reason
- [ ] 4. A slice with no blocker yet
`

func TestBlockerReason(t *testing.T) {
	// Format A: "- [ ] 2. … BLOCKED: <reason>".
	if r, ok := BlockerReason(progFixture, 2); !ok || r != "waiting on decision X" {
		t.Errorf("n=2 → (%q,%v), want waiting on decision X", r, ok)
	}
	// Format B: "- [ ] ~~Slice 3 — …~~ — **BLOCKED**: <reason>" (strip ~~ and **).
	if r, ok := BlockerReason(progFixture, 3); !ok || r != "another reason" {
		t.Errorf("n=3 → (%q,%v), want another reason", r, ok)
	}
	// A checked slice is never blocked.
	if _, ok := BlockerReason(progFixture, 1); ok {
		t.Errorf("n=1 (checked) should have no blocker")
	}
	// An unchecked slice with no BLOCKED marker → none.
	if _, ok := BlockerReason(progFixture, 4); ok {
		t.Errorf("n=4 (no BLOCKED) should have no reason")
	}
}
