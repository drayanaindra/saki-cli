package infra

import (
	"context"
	"net/http"

	"github.com/drayanaindra/saki-cli/backend/usecase"
)

// NullProxy is the Proxy used when the Go backend runs STANDALONE — no apps/server upstream.
//
// The three methods below are the only places Go reaches for Express, and all three exist purely for
// COEXISTENCE with the TS server: the run-list union (usecase/runs.go:39), the SSE passthrough for a
// run id Go does not own (adapter/http.go:381), and the hosted POST /api/runs forward
// (adapter/http.go:528). Standalone, none of them has anything to talk to — so each degrades to a
// CLEAN, immediate refusal rather than a dial to a port nothing is listening on (which would surface
// as a 502 after a TCP timeout, or a hung SSE connection).
//
// Selected by cmd/server/main.go when SAKI_UPSTREAM is unset/empty. Pairing with apps/server stays
// available by setting it explicitly — see NewHTTPProxy.
//
// NOTE the deliberate asymmetry with NewHTTPProxy: that constructor treats an EMPTY base as
// "http://localhost:8787" (proxy.go:23-25), so it can never express "no upstream". Standalone is this
// type's job, not an empty-string mode of the HTTP one — do not "simplify" the two together.
type NullProxy struct{}

// Compile-time proof that NullProxy satisfies the port ListService is wired against. The adapter's
// Proxy interface is a SUPERSET of this one (it adds StreamEvents, implemented below), and
// selectProxy's adapter.Proxy return type proves that half at compile time in cmd/server.
var _ usecase.Proxy = (*NullProxy)(nil)

// The refusals name SAKI_UPSTREAM so an operator who hits one is told how to get the paired
// behavior back, instead of reading a bare status code.
const (
	errNoUpstream = `{"error":"no upstream configured — this backend is running standalone; set SAKI_UPSTREAM to pair it with a companion orchestrator"}`
	errNoSuchRun  = `{"error":"no such run — this backend is running standalone and does not own that id"}`
)

// Forward refuses with 501, which the adapter relays VERBATIM (adapter/http.go:531-535). The error is
// nil on purpose: a non-nil error would make the adapter answer 502 "upstream unreachable", which
// would be a lie — there is no upstream to be unreachable, the route is simply not implemented here.
func (p *NullProxy) Forward(_, _, _ string, _ []byte) (int, []byte, error) {
	return http.StatusNotImplemented, []byte(errNoUpstream), nil
}

// GetRuns reports "no upstream runs" as an EMPTY list with a NIL error — the same shape the real
// HTTPProxy already returns for a non-200 upstream (proxy.go:115-117). ListService guards the union
// with `if err == nil` (usecase/runs.go:39), so this makes it walk an empty upstream list and return
// Go's own runs unchanged. Returning an error would reach the same output through the degrade branch,
// but would mean "the fetch failed" — which is false, and would mislead anyone reading that code.
func (p *NullProxy) GetRuns(_ string) ([]map[string]any, error) {
	return nil, nil
}

// StreamEvents answers 404 and RETURNS IMMEDIATELY. The handler calls this as its terminal action for
// an un-owned id and returns straight after (adapter/http.go:378-382), and it has not written any
// header yet at that point — so this status is the one the client actually receives. Returning
// promptly is the load-bearing property: a proxy that blocked here would hang the browser's
// EventSource on an unknown run id forever. ctx is ignored deliberately — there is no upstream
// request to bind to its lifetime.
func (p *NullProxy) StreamEvents(_ context.Context, _, _ string, w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(errNoSuchRun))
}
