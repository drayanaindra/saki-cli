package usecase

import (
	"errors"
	"strings"
	"testing"
)

// F4 · P3 slice 5: usecase tests for POST /api/resolve-blocker. Cover the endpoint decision table from
// the TS oracle (index.ts:601): match (5.1c), append (5.2c), idempotent no-op (5.3c/R5), all-invalid
// 422 (5.4c), missing PRD 404, and the write-error 500 that is faithful to index.ts:643 (NOT graceful).

// rbFS is an in-memory WorkItemsFS whose Read backs the resolve-blocker read; only Read is exercised.
type rbFS struct{ files map[string]string }

func (f rbFS) WalkMarkdown(string) []string { return nil }
func (f rbFS) Read(p string) (string, bool) { s, ok := f.files[p]; return s, ok }
func (f rbFS) Exists(p string) bool         { _, ok := f.files[p]; return ok }
func (f rbFS) MTimeMs(string) int64         { return 0 }
func (f rbFS) ListDir(string) []string      { return nil }

// rbWriter records the last write, or returns err to exercise the 500 branch.
type rbWriter struct {
	last string
	err  error
}

func (w *rbWriter) WriteFile(_, content string) error {
	if w.err != nil {
		return w.err
	}
	w.last = content
	return nil
}

func dec(q, d string) map[string]any { return map[string]any{"question": q, "decision": d} }

// 5.1c: a matching open-question bullet is prefixed ✅ RESOLVED — <decision>, {resolved:1}, written.
func TestResolveBlocker_match(t *testing.T) {
	md := "## Rabbit Holes & Open Questions\n**Open questions:**\n- **Q1** before slice 2. Fork?\n"
	w := &rbWriter{}
	svc := NewResolveBlockerService(rbFS{files: map[string]string{"/r/prd.md": md}}, w)
	status, body := svc.ResolveBlocker("/r/prd.md", []any{dec("Q1", "pick A")})
	if status != 200 || body.(map[string]any)["resolved"] != 1 {
		t.Fatalf("want 200 resolved:1, got %d %#v", status, body)
	}
	if !strings.Contains(w.last, "✅ RESOLVED — pick A — **Q1** before slice 2. Fork?") {
		t.Errorf("bullet not marked in write: %q", w.last)
	}
}

// 5.2c: no pre-authored bullet → the decision is appended so the operator is never dead-ended.
func TestResolveBlocker_append(t *testing.T) {
	w := &rbWriter{}
	svc := NewResolveBlockerService(rbFS{files: map[string]string{"/r/prd.md": "# PRD\nbody"}}, w)
	status, body := svc.ResolveBlocker("/r/prd.md", []any{dec("Synthesized fork", "chose B")})
	if status != 200 || body.(map[string]any)["resolved"] != 1 {
		t.Fatalf("want 200 resolved:1, got %d %#v", status, body)
	}
	if !strings.Contains(w.last, "- ✅ RESOLVED — chose B — Synthesized fork") {
		t.Errorf("decision not appended: %q", w.last)
	}
}

// 5.3c / R5: a decision already recorded → {resolved:0}, HTTP 200 (idempotent success, NOT 422), NO write.
func TestResolveBlocker_idempotentNoWrite(t *testing.T) {
	md := "## Rabbit Holes & Open Questions\n**Open questions:**\n- ✅ RESOLVED — chose B — Synthesized fork\n"
	w := &rbWriter{}
	svc := NewResolveBlockerService(rbFS{files: map[string]string{"/r/prd.md": md}}, w)
	status, body := svc.ResolveBlocker("/r/prd.md", []any{dec("Synthesized fork", "chose B")})
	if status != 200 || body.(map[string]any)["resolved"] != 0 {
		t.Fatalf("want 200 resolved:0, got %d %#v", status, body)
	}
	if w.last != "" {
		t.Errorf("R5: an idempotent no-op still wrote: %q", w.last)
	}
}

// 5.4c: an all-empty / all-invalid decisions[] (no valid question+decision) → 422, NO write.
func TestResolveBlocker_allInvalid422(t *testing.T) {
	w := &rbWriter{}
	svc := NewResolveBlockerService(rbFS{files: map[string]string{"/r/prd.md": "# PRD"}}, w)
	cases := [][]any{
		{dec("", "d")},                  // empty question
		{dec("q", "  ")},                // blank decision
		{map[string]any{"question": 1}}, // non-string fields
		{"not-an-object"},               // non-object item
	}
	for _, d := range cases {
		if status, _ := svc.ResolveBlocker("/r/prd.md", d); status != 422 {
			t.Errorf("decisions=%#v status %d, want 422", d, status)
		}
	}
	if w.last != "" {
		t.Errorf("an all-invalid payload wrote: %q", w.last)
	}
}

// Validation parity: a non-.md prdPath or an empty decisions[] → 422 before any read.
func TestResolveBlocker_badRequest422(t *testing.T) {
	svc := NewResolveBlockerService(rbFS{files: map[string]string{}}, &rbWriter{})
	if s, _ := svc.ResolveBlocker("/r/prd.txt", []any{dec("q", "d")}); s != 422 {
		t.Errorf("non-.md path status %d, want 422", s)
	}
	if s, _ := svc.ResolveBlocker("/r/prd.md", []any{}); s != 422 {
		t.Errorf("empty decisions status %d, want 422", s)
	}
}

// A missing PRD → 404 (read failure), parity with index.ts.
func TestResolveBlocker_missing404(t *testing.T) {
	svc := NewResolveBlockerService(rbFS{files: map[string]string{}}, &rbWriter{})
	if s, _ := svc.ResolveBlocker("/r/gone.md", []any{dec("q", "d")}); s != 404 {
		t.Errorf("missing PRD status %d, want 404", s)
	}
}

// A write failure → 500 with the error body (faithful to index.ts:643 — this endpoint does NOT
// degrade gracefully like lock-prd).
func TestResolveBlocker_writeErr500(t *testing.T) {
	md := "## Rabbit Holes & Open Questions\n**Open questions:**\n- **Q1** a fork\n"
	w := &rbWriter{err: errors.New("disk full")}
	svc := NewResolveBlockerService(rbFS{files: map[string]string{"/r/prd.md": md}}, w)
	status, body := svc.ResolveBlocker("/r/prd.md", []any{dec("Q1", "pick A")})
	if status != 500 {
		t.Fatalf("want 500 on write error, got %d", status)
	}
	if body.(map[string]any)["error"] != "disk full" {
		t.Errorf("want the error surfaced, got %#v", body)
	}
}

// Partial success: an invalid item is skipped and the valid one still resolves (parity per-item continue).
func TestResolveBlocker_partialSkipInvalid(t *testing.T) {
	md := "## Rabbit Holes & Open Questions\n**Open questions:**\n- **Q1** a fork\n"
	w := &rbWriter{}
	svc := NewResolveBlockerService(rbFS{files: map[string]string{"/r/prd.md": md}}, w)
	status, body := svc.ResolveBlocker("/r/prd.md", []any{"garbage", dec("Q1", "pick A")})
	if status != 200 || body.(map[string]any)["resolved"] != 1 {
		t.Fatalf("want 200 resolved:1 (invalid skipped), got %d %#v", status, body)
	}
}
