package infra

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"time"
)

// HTTPProxy reverse-proxies to apps/server for run-kinds Go does not own (build POSTs, the hosted
// POST /api/runs) and fetches its run list for the union-merge. It is a SERVER-SIDE relay (not a
// 3xx redirect), so there is no origin crossing / host:port leak; the operator's Cookie is
// forwarded so the upstream session is honoured.
type HTTPProxy struct {
	base   string
	client *http.Client
}

func NewHTTPProxy(base string) *HTTPProxy {
	if base == "" {
		base = "http://localhost:8787"
	}
	return &HTTPProxy{base: base, client: &http.Client{Timeout: 30 * time.Second}}
}

// Forward relays a request to apps/server and returns its status + body VERBATIM, so a build
// forward preserves apps/server's 201 {runId} AND its 200 {runId,deduped:true} dedup response.
func (p *HTTPProxy) Forward(method, path, cookie string, body []byte) (int, []byte, error) {
	req, err := http.NewRequest(method, p.base+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	res, err := p.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	respBody, err := io.ReadAll(res.Body)
	if err != nil {
		return 0, nil, err
	}
	return res.StatusCode, respBody, nil
}

// StreamEvents reverse-proxies a long-lived SSE stream (GET /events/:id) for a run Go does not own
// (a build run). It relays upstream frames INCREMENTALLY (flush each chunk — never buffer the whole
// stream) and, because the request is bound to ctx (the browser's connection), a client disconnect
// cancels ctx → the upstream Read errors → the deferred Body.Close tears down the Go→apps/server hop
// too (dual-hop teardown, no leaked upstream stream).
func (p *HTTPProxy) StreamEvents(ctx context.Context, id, cookie string, w http.ResponseWriter) {
	// PathEscape the id so a crafted runId (`../`, `/`) can't manipulate the upstream apps/server path.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.base+"/events/"+url.PathEscape(id), nil)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	res, err := (&http.Client{}).Do(req) // no client timeout — a long-lived SSE; ctx bounds its life
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	defer res.Body.Close()
	for k, vv := range res.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(res.StatusCode)
	rc := http.NewResponseController(w)
	_ = rc.Flush()
	buf := make([]byte, 4096)
	for {
		n, rerr := res.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			_ = rc.Flush() // incremental — a live build stream must not freeze in a buffer
		}
		if rerr != nil {
			return // EOF, upstream close, or ctx cancel (client disconnect) — dual-hop teardown via defer
		}
	}
}

// GetRuns fetches apps/server's run list for the union. A non-200 (e.g. 401) or a transport error
// degrades to "no upstream runs" so the list still returns Go's own runs (Go owns the route). It
// uses a SHORT timeout (not the 30s Forward timeout) so a hung apps/server can't stall the studio's
// run-list poll.
func (p *HTTPProxy) GetRuns(cookie string) ([]map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.base+"/api/runs", nil)
	if err != nil {
		return nil, err
	}
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	res, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, nil
	}
	var runs []map[string]any
	if err := json.NewDecoder(res.Body).Decode(&runs); err != nil {
		return nil, err
	}
	return runs, nil
}
