package adapter

import (
	"bytes"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/drayanaindra/saki-cli/backend/infra"
	"github.com/drayanaindra/saki-cli/backend/usecase"
)

// F5 · E14·P4 slice 1: adapter-level tests for GET /api/proto/{dir}/{rest...}. The domain suite proves
// the regex+containment, the usecase suite proves the resolve/open flow; these lock the HTTP wiring the
// pure tests can't see — base64url {dir} decode, the {rest...} splat, byte-identical binary serving with
// an extension-correct Content-Type, ServeMux `..` pre-emption, and the OriginGuard (🔒 BR5).

var htmlBody = []byte(`<html><body><img src="shot.png"></body></html>`)
var pngBody = []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x01, 0x02, 0x03, 0xff}

// protoServer stands up a real ContentFS-backed proto sub-handler over a temp repo holding
// tasks/proto-foo/{preview.html,shot.png}. Returns the server + the base64url-encoded cwd segment.
func protoServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	cwd := t.TempDir()
	dir := filepath.Join(cwd, "tasks", "proto-foo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "preview.html"), htmlBody, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "shot.png"), pngBody, 0o644); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	NewProtoHandler(usecase.NewProtoAssetService(infra.ContentFS{})).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, base64.RawURLEncoding.EncodeToString([]byte(cwd))
}

// get issues a GET, optionally with an Origin header, WITHOUT following redirects, returning the
// response (caller closes body).
func get(t *testing.T, url, origin string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// AC 1.1: happy path — .html + sibling .png served 200, byte-identical, extension-correct Content-Type.
func TestProtoRoute_serves_html_and_png(t *testing.T) {
	srv, dir := protoServer(t)

	res := get(t, srv.URL+"/api/proto/"+dir+"/tasks/proto-foo/preview.html", "")
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("html status = %d, want 200", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Fatalf("html Content-Type = %q, want text/html; charset=utf-8", ct)
	}
	if b, _ := io.ReadAll(res.Body); !bytes.Equal(b, htmlBody) {
		t.Fatalf("html body not byte-identical")
	}

	res2 := get(t, srv.URL+"/api/proto/"+dir+"/tasks/proto-foo/shot.png", "")
	defer res2.Body.Close()
	if res2.StatusCode != 200 {
		t.Fatalf("png status = %d, want 200", res2.StatusCode)
	}
	if ct := res2.Header.Get("Content-Type"); ct != "image/png" {
		t.Fatalf("png Content-Type = %q, want image/png", ct)
	}
	if b, _ := io.ReadAll(res2.Body); !bytes.Equal(b, pngBody) {
		t.Fatalf("png body not byte-identical: %v", b)
	}
}

// AC 1.3(a): a syntactically-clean rel that fails the proto regex → 422, no file bytes.
func TestProtoRoute_badRel_422(t *testing.T) {
	srv, dir := protoServer(t)
	res := get(t, srv.URL+"/api/proto/"+dir+"/tasks/other/file.html", "")
	defer res.Body.Close()
	if res.StatusCode != 422 {
		t.Fatalf("status = %d, want 422", res.StatusCode)
	}
	if b, _ := io.ReadAll(res.Body); bytes.Contains(b, htmlBody) {
		t.Fatalf("rejected request must not emit file bytes")
	}
}

// AC 1.3(b): a raw ../../etc/passwd URL — ServeMux cleanPath redirects it away before the handler, so
// the request never resolves to file bytes (non-200).
func TestProtoRoute_rawTraversal_non200(t *testing.T) {
	srv, dir := protoServer(t)
	res := get(t, srv.URL+"/api/proto/"+dir+"/../../etc/passwd", "")
	defer res.Body.Close()
	if res.StatusCode == 200 {
		t.Fatalf("raw traversal returned 200 — must be pre-empted")
	}
}

// AC 1.3(c): a non-decodable base64url {dir} → 422.
func TestProtoRoute_badDirEncoding_422(t *testing.T) {
	srv, _ := protoServer(t)
	res := get(t, srv.URL+"/api/proto/!!!/tasks/proto-foo/preview.html", "")
	defer res.Body.Close()
	if res.StatusCode != 422 {
		t.Fatalf("status = %d, want 422", res.StatusCode)
	}
}

// AC 1.3(d): a cross-origin Origin header → 403 (🔒 BR5, OriginGuard), never served.
func TestProtoRoute_crossOrigin_403(t *testing.T) {
	srv, dir := protoServer(t)
	res := get(t, srv.URL+"/api/proto/"+dir+"/tasks/proto-foo/preview.html", "http://evil.example.com")
	defer res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatalf("cross-origin status = %d, want 403", res.StatusCode)
	}
	if b, _ := io.ReadAll(res.Body); bytes.Contains(b, htmlBody) {
		t.Fatalf("cross-origin request must not emit file bytes")
	}
}

// AC 1.4: a well-formed rel whose target file does not exist → 404 (not 200/500).
func TestProtoRoute_missing_404(t *testing.T) {
	srv, dir := protoServer(t)
	res := get(t, srv.URL+"/api/proto/"+dir+"/tasks/proto-foo/nope.html", "")
	defer res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatalf("status = %d, want 404", res.StatusCode)
	}
}
