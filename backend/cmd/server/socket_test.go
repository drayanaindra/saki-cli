package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/drayanaindra/saki-cli/backend/adapter"
)

// shortTempDir is deliberately NOT t.TempDir(): on macOS that sits under
// /var/folders/<...>/T/<TestName>/001, which blows past the 103-byte sun_path limit before a socket
// name is even appended. Rooting at /tmp keeps the path inside the limit the code under test enforces.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "sk")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// serveOnSocket binds socketPath, serves handler on it, and returns a client wired to that socket.
// The listener is closed and the path removed when the test ends.
func serveOnSocket(t *testing.T, socketPath string, handler http.Handler) *http.Client {
	t.Helper()
	listener, bound, err := listenUnix(socketPath)
	if err != nil {
		t.Fatalf("listenUnix(%q) = %v, want success", socketPath, err)
	}
	if bound != socketPath {
		t.Fatalf("listenUnix bound %q, want %q", bound, socketPath)
	}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		_ = server.Close()
		_ = os.Remove(socketPath)
	})
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		},
	}}
}

func healthHandler() http.Handler {
	return adapter.OriginGuard(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "service": "go"})
	}))
}

func skipOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("unix socket transport is a declared Non-Goal on Windows")
	}
}

// AC 4.4 (🔒 INVARIANT 1): the socket file is owner-read-write only. Chmod must land before the
// first accept, so stat'ing it right after bind is the real check.
func TestListenUnix_SocketIsOwnerOnly(t *testing.T) {
	skipOnWindows(t)
	socketPath := filepath.Join(shortTempDir(t), "backend.sock")
	serveOnSocket(t, socketPath, healthHandler())

	info, err := os.Stat(socketPath)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("socket permissions = %#o, want 0600", perm)
	}
}

// AC 4.6: a crashed predecessor leaves its socket file behind. The bind must remove that stale path
// first (a bare Listen would fail EADDRINUSE against a file nothing is serving), and traffic through
// the socket must still satisfy OriginGuard.
func TestListenUnix_RemovesStalePathAndPassesOriginGuard(t *testing.T) {
	skipOnWindows(t)
	socketPath := filepath.Join(shortTempDir(t), "backend.sock")
	if err := os.WriteFile(socketPath, []byte("stale socket from a crashed backend"), 0o600); err != nil {
		t.Fatalf("seed stale socket: %v", err)
	}

	client := serveOnSocket(t, socketPath, healthHandler())

	// The CLI's socket adapter sends Host: localhost; OriginGuard must accept it.
	req, err := http.NewRequest(http.MethodGet, "http://localhost/api/health", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatalf("health over unix socket: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("health over unix socket = HTTP %d, want 200", res.StatusCode)
	}
	var body struct {
		OK bool `json:"ok"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil || !body.OK {
		t.Fatalf("health body = %+v (err %v), want ok:true", body, err)
	}
}

// AC 4.6 (guard half): the socket is same-machine transport, not an OriginGuard bypass. A
// DNS-rebind Host arriving over the socket is still refused.
func TestListenUnix_NonLoopbackHostStillBlocked(t *testing.T) {
	skipOnWindows(t)
	socketPath := filepath.Join(shortTempDir(t), "backend.sock")
	client := serveOnSocket(t, socketPath, healthHandler())

	req, err := http.NewRequest(http.MethodGet, "http://evil.example.com/api/health", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatalf("request over unix socket: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("rebind Host over unix socket = HTTP %d, want 403", res.StatusCode)
	}
}

// AC 4.5: health over the unix socket answers well inside the 3 s single-measurement p50 proxy.
func TestListenUnix_HealthRespondsWithinBudget(t *testing.T) {
	skipOnWindows(t)
	socketPath := filepath.Join(shortTempDir(t), "backend.sock")
	client := serveOnSocket(t, socketPath, healthHandler())

	req, err := http.NewRequest(http.MethodGet, "http://localhost/api/health", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	start := time.Now()
	res, err := client.Do(req)
	if err != nil {
		t.Fatalf("health over unix socket: %v", err)
	}
	defer res.Body.Close()
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("health over unix socket took %v, want <= 3s", elapsed)
	}
}

// §13 + AC 4.6: an over-long path is rejected by our own guard, BEFORE net.Listen turns it into a
// bare EINVAL. Disabling the socket must not be fatal — TCP still serves.
func TestListenUnix_RejectsOverLongPathBeforeListen(t *testing.T) {
	skipOnWindows(t)
	socketPath := filepath.Join(shortTempDir(t), strings.Repeat("d", unixSocketLimit()), "backend.sock")

	listener, bound, err := listenUnix(socketPath)
	if err == nil {
		if listener != nil {
			_ = listener.Close()
		}
		t.Fatalf("listenUnix(%d-byte path) succeeded, want a descriptive error", len(socketPath))
	}
	if listener != nil || bound != "" {
		t.Fatalf("listenUnix returned listener=%v bound=%q on error, want nil/empty", listener, bound)
	}
	if !strings.Contains(err.Error(), "path too long") {
		t.Fatalf("error = %q, want it to name the path-length limit", err)
	}
}
