package infra

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The standalone backend's proxy must never dial apps/server. These tests pin the three behaviors
// ListService + the SSE handler + the hosted-POST forward rely on, because each one is a coexistence
// path that has to degrade to a CLEAN refusal rather than a hang or a 502.

func TestNullProxy_ForwardReturns501(t *testing.T) {
	status, body, err := (&NullProxy{}).Forward("POST", "/api/runs", "sid=abc", []byte(`{"x":1}`))
	if err != nil {
		t.Fatalf("Forward must not error — the handler writes the status through verbatim: %v", err)
	}
	if status != 501 {
		t.Fatalf("status = %d, want 501 (not implemented on a standalone backend)", status)
	}
	if !strings.Contains(string(body), `"error"`) {
		t.Fatalf("body %s — must carry a JSON error the handler can relay verbatim", body)
	}
	if !strings.Contains(string(body), "SAKI_UPSTREAM") {
		t.Fatalf("body %s — must name SAKI_UPSTREAM so the operator can act on the refusal", body)
	}
}

// (nil, nil) — a nil slice AND a nil error — is deliberate, and it mirrors what the real HTTPProxy
// already does for a non-200 upstream (proxy.go:115-117). ListService guards the union with
// `if err == nil` (usecase/runs.go:39), so a nil error makes it walk an EMPTY upstream list, which is
// exactly "there are no upstream runs". Returning an error instead would take the degrade branch and
// mean the same thing by accident — this keeps the standalone case honest rather than error-shaped.
func TestNullProxy_GetRunsEmptyNoError(t *testing.T) {
	runs, err := (&NullProxy{}).GetRuns("sid=abc")
	if err != nil {
		t.Fatalf("GetRuns must not error — standalone means 'no upstream runs', not 'the fetch failed': %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("runs = %v, want empty", runs)
	}
}

// The SSE handler calls StreamEvents as its TERMINAL action for a run Go does not own
// (adapter/http.go:381) and returns straight after. So this must write a status and RETURN.
// If it blocked, the client would hang forever on an un-owned id — the failure this test exists to
// catch, which is why it asserts a deadline rather than just the status code.
func TestNullProxy_StreamEventsWrites404AndReturns(t *testing.T) {
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		(&NullProxy{}).StreamEvents(context.Background(), "run-it-does-not-own", "", rec)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("StreamEvents blocked — it must write a status and return, never hold the SSE connection open")
	}

	if rec.Code != 404 {
		t.Fatalf("code = %d, want 404 (Go does not own the run and there is no upstream to ask)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"error"`) {
		t.Fatalf("body %q — must be a JSON error, matching the handler's other refusals", rec.Body.String())
	}
}

// A cancelled context must not change the answer: there is still no upstream, so the refusal is the
// same and still immediate. Guards against an implementation that grows a ctx-driven wait.
func TestNullProxy_StreamEventsIgnoresCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		(&NullProxy{}).StreamEvents(ctx, "x", "", rec)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("StreamEvents blocked on a cancelled context")
	}
	if rec.Code != 404 {
		t.Fatalf("code = %d, want 404", rec.Code)
	}
}
