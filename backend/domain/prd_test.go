package domain

import "testing"

// TestExtractScore (AC 2.1): faithful port of prd.ts:5 extractScore —
// /Quality Gate:?\s*([0-9]{1,3})\s*\/\s*100/i → "<n>/100" else "".
func TestExtractScore(t *testing.T) {
	cases := []struct {
		name, md, want string
	}{
		{"basic", "Quality Gate: 93/100", "93/100"},
		{"no-colon", "Quality Gate 7/100", "7/100"},
		{"case-insensitive", "quality gate: 100/100", "100/100"},
		{"spaces-around-slash", "Quality Gate: 42 / 100", "42/100"},
		{"three-digit", "Quality Gate: 100/100", "100/100"},
		{"embedded", "blah\nsome Quality Gate:5/100 line\nblah", "5/100"},
		{"miss", "no score here", ""},
		{"wrong-denominator", "Quality Gate: 93/50", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ExtractScore(c.md); got != c.want {
				t.Fatalf("ExtractScore(%q) = %q, want %q", c.md, got, c.want)
			}
		})
	}
}

// TestReviewPathFor (AC 2.3): prd.ts:29 — path.replace(/\.md$/,'-review.md'), only the trailing .md.
func TestReviewPathFor(t *testing.T) {
	cases := []struct{ in, want string }{
		{"tasks/prd-foo.md", "tasks/prd-foo-review.md"},
		{"/repo/docs/prd/x/prd.md", "/repo/docs/prd/x/prd-review.md"},
		{"noext", "noext"}, // no trailing .md → unchanged
		{"a.md.md", "a.md-review.md"},
	}
	for _, c := range cases {
		if got := ReviewPathFor(c.in); got != c.want {
			t.Fatalf("ReviewPathFor(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestReviewStatePathFor (AC 2.4): prd.ts:35 — basename → strip ^prd- + .md$ →
// `${cwd}/tasks/.prd-review-<slug>-state.json` (raw interpolation, NOT path.join).
func TestReviewStatePathFor(t *testing.T) {
	cases := []struct{ prd, cwd, want string }{
		{"tasks/prd-foo.md", "/repo", "/repo/tasks/.prd-review-foo-state.json"},
		{"/abs/tasks/prd-go-workflow.md", "/repo", "/repo/tasks/.prd-review-go-workflow-state.json"},
		{"prd-bar.md", "/w", "/w/tasks/.prd-review-bar-state.json"},
		{"notprd.md", "/w", "/w/tasks/.prd-review-notprd-state.json"}, // no prd- prefix → slug=notprd
	}
	for _, c := range cases {
		if got := ReviewStatePathFor(c.prd, c.cwd); got != c.want {
			t.Fatalf("ReviewStatePathFor(%q,%q) = %q, want %q", c.prd, c.cwd, got, c.want)
		}
	}
}
