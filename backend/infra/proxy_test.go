package infra

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPProxy_ForwardVerbatim(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/run" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Cookie") != "sid=abc" {
			t.Errorf("cookie not forwarded: %q", r.Header.Get("Cookie"))
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"meta":{"kind":"build"}}` {
			t.Errorf("body %s", body)
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"runId":"x","deduped":true}`))
	}))
	defer upstream.Close()

	status, body, err := NewHTTPProxy(upstream.URL).Forward("POST", "/api/run", "sid=abc", []byte(`{"meta":{"kind":"build"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if status != 200 || string(body) != `{"runId":"x","deduped":true}` {
		t.Fatalf("status %d body %s (want verbatim)", status, body)
	}
}

func TestHTTPProxy_GetRuns(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id":"b1","status":"running","startedAt":10}]`))
	}))
	defer upstream.Close()
	runs, err := NewHTTPProxy(upstream.URL).GetRuns("")
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0]["id"] != "b1" {
		t.Fatalf("runs %v", runs)
	}
}

func TestHTTPProxy_StreamEvents(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/events/r1" {
			t.Errorf("path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("data: hello\n\n"))
	}))
	defer upstream.Close()

	// wrap StreamEvents in an outer server so it gets a real Flush-capable ResponseWriter.
	outer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		NewHTTPProxy(upstream.URL).StreamEvents(r.Context(), "r1", "", w)
	}))
	defer outer.Close()

	res, err := http.Get(outer.URL + "/x")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	if res.Header.Get("Content-Type") != "text/event-stream" || !strings.Contains(string(body), "data: hello") {
		t.Fatalf("passthrough relay failed: ct=%q body=%q", res.Header.Get("Content-Type"), body)
	}
}

func TestHTTPProxy_GetRuns_Non200Degrades(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401) // apps/server auth-gated
	}))
	defer upstream.Close()
	runs, err := NewHTTPProxy(upstream.URL).GetRuns("")
	if err != nil || runs != nil {
		t.Fatalf("a 401 should degrade to (nil,nil), got %v %v", runs, err)
	}
}
