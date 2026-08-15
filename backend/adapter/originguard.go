package adapter

import (
	"net/http"
	"net/url"
	"strings"
)

// loopbackHosts are the hostnames that count as same-machine (parity with apps/server
// originGuard.ts). url.Hostname() strips the port and IPv6 brackets, so "::1" covers "[::1]".
var loopbackHosts = map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}

// hostnameOf extracts the hostname from a Host header ("localhost:5180") or an Origin
// ("http://localhost:5180"); "" if unparseable.
func hostnameOf(value string) string {
	if value == "" {
		return ""
	}
	if !strings.Contains(value, "://") {
		value = "http://" + value
	}
	u, err := url.Parse(value)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname()) // parity with JS new URL().hostname (lowercased)
}

// localhostOnly reports whether the request is same-machine (loopback Host) and not
// cross-origin (loopback Origin, or none) — parity with apps/server originGuard.ts. A DNS-rebind
// (Host: evil.com) or a cross-origin fetch (Origin: http://evil.com) is rejected.
func localhostOnly(r *http.Request) bool {
	if h := hostnameOf(r.Host); h == "" || !loopbackHosts[h] {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		if oh := hostnameOf(origin); oh == "" || !loopbackHosts[oh] {
			return false
		}
	}
	return true
}

// OriginGuard is the BR7 loopback guard for the repo-mutating git-write routes: it 403s any
// request that isn't same-machine + same-origin, so those routes are never reachable
// cross-origin (localhost-CSRF / DNS-rebind). Mounted per-route (not global) so it never
// changes the run-vertical routes' behavior. Parity with apps/server originGuard.ts.
func OriginGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !localhostOnly(r) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "cross-origin blocked"})
			return
		}
		next.ServeHTTP(w, r)
	})
}
