package adapter

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// AC 1.5 / BR7: non-loopback Host or Origin → 403; loopback Host + loopback-or-absent Origin → pass.
func TestOriginGuard(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	guarded := OriginGuard(ok)

	cases := []struct {
		name, host, origin string
		want               int
	}{
		{"loopback host, no origin", "localhost:8788", "", 200},
		{"127.0.0.1 host, loopback origin", "127.0.0.1:8788", "http://localhost:5180", 200},
		{"loopback host, cross origin", "localhost:8788", "http://evil.com", 403},
		{"dns-rebind host", "evil.com:8788", "", 403},
		{"cross host + cross origin", "attacker.test", "http://attacker.test", 403},
		{"subdomain-of-loopback attack", "localhost.evil.com:8788", "", 403},
		{"ipv6 loopback host", "[::1]:8788", "", 200},
		{"uppercase loopback host (lowercased parity)", "LOCALHOST:8788", "", 200},
		{"empty host fails closed", "", "", 403},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/branches?cwd=/r", nil)
			req.Host = c.host
			if c.origin != "" {
				req.Header.Set("Origin", c.origin)
			}
			rec := httptest.NewRecorder()
			guarded.ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Fatalf("host=%q origin=%q want %d got %d (%s)", c.host, c.origin, c.want, rec.Code, rec.Body.String())
			}
		})
	}
}
