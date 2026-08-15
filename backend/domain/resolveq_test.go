package domain

import "testing"

func TestResolveQuestion(t *testing.T) {
	md := "## Rabbit Holes & Open Questions\n**Open questions:**\n- **Q1** before slice 2. Fork?\n"

	t.Run("match by title prefixes the bullet", func(t *testing.T) {
		got := ResolveQuestion(md, "Q1", "pick A")
		want := "## Rabbit Holes & Open Questions\n**Open questions:**\n- ✅ RESOLVED — pick A — **Q1** before slice 2. Fork?\n"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("match by full body works too", func(t *testing.T) {
		got := ResolveQuestion(md, "**Q1** before slice 2. Fork?", "pick A")
		if !contains(got, "✅ RESOLVED — pick A — **Q1**") {
			t.Errorf("body match did not resolve: %q", got)
		}
	})

	t.Run("already-resolved bullet is a no-op", func(t *testing.T) {
		resolved := "## Rabbit Holes & Open Questions\n**Open questions:**\n- ✅ RESOLVED — old — **Q1** before slice 2. Fork?\n"
		if got := ResolveQuestion(resolved, "Q1", "new"); got != resolved {
			t.Errorf("re-resolved an already-resolved bullet: %q", got)
		}
	})

	t.Run("no matching question leaves md untouched", func(t *testing.T) {
		if got := ResolveQuestion(md, "Nonexistent", "x"); got != md {
			t.Errorf("unexpected change: %q", got)
		}
	})
}

func TestAppendResolvedQuestion(t *testing.T) {
	t.Run("splices after an existing marker", func(t *testing.T) {
		md := "## Open Questions\n**Open questions:**\n- existing q before slice 5\n"
		got := AppendResolvedQuestion(md, "New fork", "chose B")
		want := "## Open Questions\n**Open questions:**\n- ✅ RESOLVED — chose B — New fork\n- existing q before slice 5\n"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("creates the marker in a section that lacks one", func(t *testing.T) {
		md := "## Open Questions\nSome intro text\n\n## Next Section\n"
		got := AppendResolvedQuestion(md, "New fork", "chose B")
		want := "## Open Questions\nSome intro text\n\n**Open questions:**\n- ✅ RESOLVED — chose B — New fork\n\n## Next Section\n"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("appends a whole section when none exists", func(t *testing.T) {
		md := "# PRD\nbody"
		got := AppendResolvedQuestion(md, "New fork", "chose B")
		want := "# PRD\nbody\n\n## Rabbit Holes & Open Questions\n\n**Open questions:**\n- ✅ RESOLVED — chose B — New fork\n"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("idempotent when the exact bullet already exists", func(t *testing.T) {
		md := "## Open Questions\n**Open questions:**\n- ✅ RESOLVED — chose B — New fork\n"
		if got := AppendResolvedQuestion(md, "New fork", "chose B"); got != md {
			t.Errorf("appended a duplicate: %q", got)
		}
	})

	t.Run("empty question or decision is a no-op", func(t *testing.T) {
		md := "# PRD"
		if got := AppendResolvedQuestion(md, "", "x"); got != md {
			t.Errorf("empty question changed md: %q", got)
		}
		if got := AppendResolvedQuestion(md, "q", "  "); got != md {
			t.Errorf("empty decision changed md: %q", got)
		}
	})

	t.Run("collapses whitespace in the question", func(t *testing.T) {
		got := AppendResolvedQuestion("# PRD", "multi\n  line   question", "d")
		if !contains(got, "- ✅ RESOLVED — d — multi line question") {
			t.Errorf("whitespace not collapsed: %q", got)
		}
	})

	// R5 safety: a re-append whose question differs only in whitespace normalizes to the SAME bullet →
	// no duplicate (the idempotency hinge the security review flagged).
	t.Run("idempotent across whitespace-varied question (R5)", func(t *testing.T) {
		once := AppendResolvedQuestion("# PRD", "New fork", "chose B")
		twice := AppendResolvedQuestion(once, "New   fork", "chose B") // extra spaces collapse to the same bullet
		if twice != once {
			t.Errorf("R5: a whitespace-varied re-append duplicated the bullet:\nonce=%q\ntwice=%q", once, twice)
		}
	})
}

// ResolveQuestion trims the decision only (no ws-collapse) — an internal newline is preserved,
// faithful to the TS oracle (prdQuestions.ts:81 uses decision.trim()). Documents the trim-only rule.
func TestResolveQuestion_decisionTrimOnly(t *testing.T) {
	md := "## Open Questions\n**Open questions:**\n- **Q1** a fork before slice 2\n"
	got := ResolveQuestion(md, "Q1", "  keep\ninternal  ")
	if !contains(got, "✅ RESOLVED — keep\ninternal — **Q1**") {
		t.Errorf("decision not trim-only (internal newline should survive): %q", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
