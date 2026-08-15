package main

import (
	"net"
	"strings"
	"testing"

	"github.com/drayanaindra/saki-cli/backend/infra"
)

// AC 4.2 (🔒 rule 5): the Go backend must bind the LOOPBACK interface only, so the privileged
// kind:init spawn is unreachable off-host at the network layer.
func TestLoopbackAddr_isLoopbackNotAllInterfaces(t *testing.T) {
	addr := loopbackAddr("8788")
	if addr != "127.0.0.1:8788" {
		t.Fatalf("loopbackAddr = %q, want 127.0.0.1:8788", addr)
	}
	// It must NOT be an all-interfaces bind (":port" / "0.0.0.0" / "::") — those are reachable off-host.
	if strings.HasPrefix(addr, ":") || strings.HasPrefix(addr, "0.0.0.0") || strings.HasPrefix(addr, "[::]") {
		t.Fatalf("addr %q is an all-interfaces bind — off-host reachable", addr)
	}
}

// A real listen on the bind address must land on a loopback IP (deterministic proof the OS refuses
// off-host connections to it — a non-loopback client can't reach a 127.0.0.1 socket).
func TestLoopbackAddr_listensOnLoopbackIP(t *testing.T) {
	ln, err := net.Listen("tcp", loopbackAddr("0")) // :0 = an ephemeral port, so the test never collides
	if err != nil {
		t.Fatalf("listen on loopback addr failed: %v", err)
	}
	defer ln.Close()
	host, _, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		t.Fatalf("bound to %q, which is not a loopback IP", host)
	}
}

// I8: SAKI_UPSTREAM decides whether this backend is STANDALONE or paired with apps/server.
//
// Unset must mean standalone — that is the whole point (the extracted saki-cli ships with no Express),
// and it is why the dev studio now pins the var explicitly in package.json's dev:backend script.
// Before I8 an unset var fell back to "http://localhost:8787", so the backend always dialled Express.
func TestSelectProxy_UnsetIsNull(t *testing.T) {
	if _, ok := selectProxy("").(*infra.NullProxy); !ok {
		t.Fatalf("selectProxy(\"\") = %T, want *infra.NullProxy — unset MUST mean standalone", selectProxy(""))
	}
}

// Whitespace is the same as unset. An env var set to " " in a shell script is a configuration typo,
// not a request to dial the host named " " — and NewHTTPProxy would silently turn it into
// http://localhost:8787 anyway (proxy.go:23-25), which would be the pre-I8 bug wearing a disguise.
func TestSelectProxy_WhitespaceIsNull(t *testing.T) {
	for _, upstream := range []string{" ", "  ", "\t", "\n"} {
		if _, ok := selectProxy(upstream).(*infra.NullProxy); !ok {
			t.Fatalf("selectProxy(%q) = %T, want *infra.NullProxy", upstream, selectProxy(upstream))
		}
	}
}

// An explicit upstream still pairs with apps/server — the dev studio's union + SSE passthrough +
// hosted-POST forward all depend on this branch, so it is a regression guard, not a formality.
func TestSelectProxy_ExplicitIsHTTP(t *testing.T) {
	if _, ok := selectProxy("http://localhost:8787").(*infra.HTTPProxy); !ok {
		t.Fatalf("selectProxy(explicit) = %T, want *infra.HTTPProxy", selectProxy("http://localhost:8787"))
	}
}

// The startup log MUST describe the mode the process is actually running in.
//
// These disagreed once: selectProxy trimmed whitespace, the log line did not, so SAKI_UPSTREAM="   "
// ran standalone while logging `upstream    `. That is worse than an unhelpful log — the dev-studio
// regression check greps this exact line precisely BECAUSE the e2e suite cannot tell the two modes
// apart, so a lying log would make the one remaining detector useless. Pinned as a paired property so
// the two can never drift again.
func TestProxyMode_AgreesWithSelectProxy(t *testing.T) {
	for _, upstream := range []string{"", " ", "\t", "\n", "  \t ", "http://localhost:8787", " http://x "} {
		_, isNull := selectProxy(upstream).(*infra.NullProxy)
		saysStandalone := proxyMode(upstream) == "standalone (no upstream)"
		if isNull != saysStandalone {
			t.Fatalf("upstream %q: selectProxy standalone=%v but log says %q — the log must not lie about the mode",
				upstream, isNull, proxyMode(upstream))
		}
	}
}
