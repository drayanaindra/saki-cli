# PRD Review — Daemon lifecycle + unix-socket transport

**Round:** 2 · **Verdict:** SHIP · **Readiness:** READY · **Blocking findings:** 0 · **Updated:** 2026-08-16

## Review summary

F1 is ready for planning. The prior review’s blockers and high-severity findings are reflected in the
PRD: the spawn lock writes a sentinel before spawning, state is UID-scoped, PID health is checked
against `/api/health`, restart rehydration has an acceptance criterion, socket cleanup and permissions
are explicit, lifecycle commands are excluded from auto-start, and the Node/Go state-file wire format
is defined.

Round 2 also closes the remaining design ambiguity by selecting `undici` for unix-socket fetch and
adds explicit stale-socket and OriginGuard coverage.

## Findings and dispositions

| ID | Finding | Disposition |
|---|---|---|
| R1–R3 | Spawn-lock race, socket cleanup through `os.Exit`, and missing Inv-2 acceptance coverage. | Fixed in §§8–10: sentinel-before-spawn, explicit SIGTERM cleanup, and AC 3.5. |
| R4–R16 | Missing socket chmod/pre-bind cleanup, lifecycle skip rules, spawn errors, PID ownership/recycling, route scoping, startup ordering, and direct Go liveness probing. | Fixed in the slice assumptions, technical constraints, and acceptance criteria. |
| R17–R31 | Circular metrics, vague kill wording, missing idempotency/error paths, corrupted PID handling, state-dir creation, MCP boundary, and socket path limits. | Fixed or explicitly bounded in §§5, 6, 8–13. |
| R32–R36 | Multi-user state isolation, status discriminator, PID recycling, in-flight-stop behavior, and state-file schema. | Fixed in the UID-scoped state path, wire-format contract, and lifecycle rules. |
| R37 | Socket adapter implementation was left as an open choice. | Fixed: `undici` is the required production dependency and `Host: localhost` is pinned for OriginGuard. |
| R38 | Stale socket and unix-socket OriginGuard behavior lacked explicit acceptance coverage. | Fixed with AC 4.6. |

## Readiness matrix

| Gate | Result |
|---|---|
| Primary job and evidence | Pass |
| Appetite and kill criterion | Pass |
| Four vertical slices | Pass — each has a startable boundary and failure paths |
| Wire format | Pass — JSON schema, sentinel, nullability, and ownership are explicit |
| Security | Pass — loopback TCP, UID-scoped 0700 directory, 0600 socket, OriginGuard, and env boundaries |
| Exit-code contract | Pass — missing/unreachable remains `3`; lifecycle commands are idempotent |
| Restart invariant | Pass — stop/start rehydration is explicitly required |
| Implementation dependencies | Pass — `undici` and npm packaging impact are named |

**Implementation handoff:** begin with Slice 1’s daemon module and direct Go health probe. Preserve
the existing CLI exit codes and keep socket transport scoped to Go-only requests.
