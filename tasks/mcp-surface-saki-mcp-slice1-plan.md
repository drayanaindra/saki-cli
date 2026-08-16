# EXECUTION PLAN: MCP surface — Slice 1 (server skeleton + `saki_status`)

**Date:** 2026-08-16
**Blocking items:** 0 (see Evidence Ledger — reviewed by rplan-review, 12 blockers fixed in-place)
**Risk Score:** LOW
**Unknown Count:** 0 / 2 max
**Behavior Spec:** N/A (backend/CLI-only, no user-visible UI)
**Source PRD:** `tasks/prd-mcp-surface-saki-mcp.md` § Slice 1 (Locked, SHIP·READY)
**Prior slices:** N/A — slice 1
**Appetite:** ~5 agent tasks (5 acceptance criteria, INVEST rule 5) — within the PRD's medium band
**Kill-if:** 5.2/5.4 (exit-code/verdict matrix) cannot be driven to 100% within appetite (PRD §6)

## Problem Statement

When my coding-agent harness needs a reliable, structured success/failure signal from `saki` instead of
hand-parsing shell output, I want `saki mcp` to expose the CLI's status check as a typed MCP tool over
stdio, so the walking skeleton proves the translation mechanism (returned `ExitCode` + thrown `CliError`
→ MCP `isError`, WITH the numeric exit code preserved in content) works before the remaining 12 tools
reuse it.

---

## Concrete Example Output

```
$ saki mcp &                     # starts a stdio MCP server, prints nothing to stdout (protocol only)

# an MCP client sends:  {"jsonrpc":"2.0","method":"tools/list","id":1}
# receives:  {"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"saki_status","description":"...",
#             "inputSchema":{...},"annotations":{"readOnlyHint":true,"destructiveHint":false,
#             "idempotentHint":true,"openWorldHint":false}}]}}

# client calls the tool (backend reachable):
#   {"jsonrpc":"2.0","method":"tools/call","id":2,"params":{"name":"saki_status","arguments":{}}}
# receives (isError:false — content matches `saki status --json`'s body verbatim, ONE block):
#   {"jsonrpc":"2.0","id":2,"result":{"isError":false,
#     "content":[{"type":"text","text":"{\"expressConfigured\":false,\"backendUrl\":\"http://127.0.0.1:8788\",\"backendReachable\":true,...}"}]}}

# backend down, client calls again (a FRESH call — no leftover state from the previous one):
# receives (isError:true — the real body PLUS a synthesized exit-code line, TWO blocks):
#   {"jsonrpc":"2.0","id":3,"result":{"isError":true,
#     "content":[{"type":"text","text":"{\"backendUrl\":\"http://127.0.0.1:8788\",\"backendReachable\":false,...}"},
#                {"type":"text","text":"Exited with code 3 (UNREACHABLE)"}]}}

# client calls with a bad/unregistered tool name — a normal tool RESULT with isError:true (verified
# against the installed SDK — NOT a top-level JSON-RPC error), not a hang or crash:
#   {"jsonrpc":"2.0","method":"tools/call","id":4,"params":{"name":"saki_nonexistent","arguments":{}}}
# receives: {"jsonrpc":"2.0","id":4,"result":{"isError":true,
#   "content":[{"type":"text","text":"MCP error -32602: Tool saki_nonexistent not found"}]}}
```

---

## Research findings (ground the plan — read before Steps)

- **`main()`'s real dispatch shape** (`src/index.ts:308-353`): `matchCommand(argv)` → `CommandDef.run(ctx,
  positionals, flags): Promise<ExitCode>`, inside a top-level `try { … } catch (err) { if (err instanceof
  CliError) { … return err.code } … return EXIT.ERROR }`. Confirms the CLI's real failure signal is a MIX
  of a returned `ExitCode` and a thrown `CliError`; `saki mcp`'s tool handlers replicate that same catch
  per-call (no single top-level catch inside a long-running MCP server process).
- **`cmdStatus` (`src/commands/status.ts:53-131`) never itself throws.** Its internal `probe()`
  (`status.ts:39-46`) already catches any `CliError` from `ctx.client.health()` and returns
  `{reachable:false}` — `cmdStatus` always RETURNS an `ExitCode`, never throws. **AC 1.2 (backend
  unreachable) therefore exercises `exitCodeToToolResult`'s RETURN branch, not its THROW branch** — the
  throw branch needs its own direct unit test (Step 2), independent of which real command exercises it.
  **Also confirmed (rplan-review, QA + Backend, verified against `src/client.ts` + `src/exit.ts:24`):
  a thrown `CliError` can have `hint === undefined`** (any non-auth HTTP failure — `StudioClient.parse()`
  only sets a hint on `isAuthStatus`) — this branch needs its own test too, distinct from the
  has-a-hint case.
- **`emit()`** (`src/output.ts:63-73`): with `ctx.json=true`, writes `JSON.stringify(value)` as a single
  string via `ctx.write`. Capturing `ctx.write`'s one call gives the exact same JSON body
  `saki status --json` prints. `ctx.writeErr` output (the human-guidance lines `cmdStatus` prints on
  failure, `status.ts:120-138`) is deliberately NOT folded into the MCP content — it's redundant with the
  structured JSON body's own fields (`backendReachable`, etc.) and `exitCodeToToolResult`'s own synthesized
  exit-code line already carries the machine-actionable signal.
- **`Ctx`** (`src/ctx.ts`): `{client, cwd, json, write, writeErr}`, injectable via `makeCtx(over)`.
- **`CliError`** (`src/exit.ts:21-31`): `{message, code: ExitCode, hint?}`. `fail()` is at `exit.ts:35`
  (line 29 is the constructor's `this.code = code` — a wrong citation in an earlier draft, corrected here).
  Thrown by `StudioClient` on transport failure — `UNREACHABLE` at `src/client.ts:125` (`originOf`) and
  `:157` (`requestOn`) — and on HTTP 401/403 at `:183-185` (`AUTH_REQUIRED`, via `codeForStatus`) — a
  DISTINCT failure class from transport loss, not the same thing (both still map to `isError:true`).
- **The CLI's `ExitCode` enum has SIX distinct values** (`src/exit.ts:8`:
  `OK=0,ERROR=1,USAGE=2,UNREACHABLE=3,NOT_FOUND=4,REMOTE_FAILED=5,AUTH_REQUIRED=6`), and CLAUDE.md rule 1
  states the exit code "is as load-bearing as stdout." **A bare boolean `isError` would collapse all six
  into one bit, losing exactly the distinction a shell-driven agent gets today from `$?`** (rplan-review,
  Architecture — verified, real regression risk since this pattern is reused by 12 more tools). Fix baked
  into Step 2 below: every non-OK path appends a synthesized `Exited with code ${code} (${SYMBOLIC_NAME})`
  content block, so the numeric+symbolic code survives the translation.
- **`@modelcontextprotocol/sdk` v1.30.0** — first production dependency (`package.json` has zero
  `dependencies` today). API (WebFetch of the SDK's Node reference): `new McpServer({name, version})` from
  `@modelcontextprotocol/sdk/server/mcp.js`; `server.registerTool(name, {title, description, inputSchema,
  annotations}, handler)` (`inputSchema` a Zod raw-shape object); `new StdioServerTransport()` from
  `@modelcontextprotocol/sdk/server/stdio.js`; `await server.connect(transport)`. Handler returns
  `{content: [{type:'text', text: string}], isError?: boolean}` — `isError` is a real, spec-defined
  `CallToolResult` field (this PRD's whole premise needs it — a structured signal, not text-parsing).
  **Verified against the installed SDK (rplan-review, QA):** `StdioServerTransport` registers only
  `'data'`/`'error'` listeners on `process.stdin` — **no `'end'` handler** — so "the process exits once
  stdin closes" is NOT something the SDK gives for free; `cmdMcp` must add its own `stdin.on('end', ...)`
  listener (Step 5 below) to make that contract real, or AC 1.4's `child.stdin.end()` assertion has nothing
  to hang the exit on.
- **Testability seam (rplan-review, QA + Architecture — BOTH independently blocked on this):** as
  originally drafted, `cmdMcp` both builds the `McpServer` AND immediately connects a hardcoded
  `new StdioServerTransport()` — leaving no way to substitute the SDK's `InMemoryTransport` for fast,
  real-server integration tests, and no seam for the 12 more tools slices 2-4 add (which would otherwise
  have to be registered inline in an ever-growing `mcp.ts`, violating CLAUDE.md's "one file per command" /
  the 40-LOC function ceiling). Fixed by splitting server construction (unconnected, reusable, one file per
  tool) from the connect step (Steps 3-5 below).
- **Test convention** (`src/commands/doctor.test.ts:1-40`): vitest + a `routedCtx()` helper stubbing
  `fetchImpl` keyed by URL substring, `down:true` for a rejected fetch. **Spawned-process convention**
  (project pattern, `patterns.md`): never spawn `npm run` in a test/background context — use the direct
  binary path, `node_modules/.bin/tsx src/index.ts mcp`, never `node dist/index.js mcp` (no `pretest`
  build step exists — spawning the compiled output would flake on a fresh checkout).
- **`vitest.config.ts`**: `include: ['src/**/*.test.ts']`, coverage `v8` + `json-summary`, **no per-test
  timeout override anywhere in the repo** — the default 5000ms is tight for "spawn tsx + MCP handshake +
  exchange + exit," so the real-process test (Step 6) needs an explicit longer timeout.

---

## Steps

| # | Action | Files (exact paths) | Risk | Test | Committable? |
|---|--------|---------------------|------|------|-------------|
| 1 | `npm install @modelcontextprotocol/sdk zod` — first production dependency | `package.json`, `package-lock.json` | LOW | `npm ls @modelcontextprotocol/sdk zod` resolves + `npm run typecheck` (proves the SDK's subpath exports `/server/mcp.js`, `/server/stdio.js`, `/inMemory.js` resolve under `moduleResolution:"NodeNext"`) | Yes |
| 2 | Add `exitCodeToToolResult(fn: () => Promise<ExitCode>, captured: CapturedIO): Promise<{content:{type:'text';text:string}[]; isError:boolean}>` in NEW `src/mcp/result.ts`. Contract: content always starts with `captured.out` mapped to text blocks. Then: `fn()` resolves `EXIT.OK` → `isError:false`, no extra block. `fn()` resolves any other code → `isError:true`, append `` `Exited with code ${code} (${symbolicName(code)})` ``. `fn()` throws a `CliError` → `isError:true`, append `` `Exited with code ${err.code} (${symbolicName(err.code)}): ${err.message}` ``, and append `err.hint` as its own block **only when `err.hint` is truthy**. `fn()` throws a non-`CliError` → `isError:true`, append `` `Exited with code ${EXIT.ERROR} (ERROR): ${err.message}` `` — **`err.stack` is NEVER included in content** (it goes nowhere in this function; the caller's `captured.err`/`ctx.writeErr` is the only place raw error detail may land, and even that is local stderr, never shipped to the MCP client) | NEW `src/mcp/result.ts` | LOW | `src/mcp/result.test.ts` — (a) resolves EXIT.OK → isError:false, content = out only; (b) resolves EXIT.UNREACHABLE → isError:true, content includes `"Exited with code 3 (UNREACHABLE)"`; (c) throws `CliError('x', EXIT.UNREACHABLE, 'do y')` → isError:true, content includes the code line AND a separate `'do y'` block; (d) throws `CliError('x', EXIT.ERROR)` **with no hint** → isError:true, content has exactly the out blocks + one code-line block, no `"undefined"` text anywhere; (e) throws a plain `Error('boom')` → isError:true, content includes `"Exited with code 1 (ERROR): boom"`, and does NOT include the stack trace string | Yes |
| 3 | Add `registerStatusTool(server: McpServer, makeToolCtx: () => Ctx): void` in NEW `src/mcp/tools/status.ts`. On EVERY invocation the handler allocates a **fresh** `const captured: CapturedIO = {out:[], err:[]}` and a **fresh** `Ctx` via `makeToolCtx()` (never reused/hoisted across calls — this is what makes back-to-back tool calls independent), then calls `exitCodeToToolResult(() => cmdStatus(ctx), captured)`. Registers with `server.registerTool('saki_status', {title:'saki status', description:'are both studio servers up, and will they let me in', inputSchema:{}, annotations:{readOnlyHint:true, destructiveHint:false, idempotentHint:true, openWorldHint:false}}, handler)` | NEW `src/mcp/tools/status.ts` | LOW | covered by Step 6's integration tests (this file has no independent logic worth a unit test beyond the registration call itself) | Yes |
| 4 | Add `createSakiMcpServer(ctx: Ctx): McpServer` in NEW `src/mcp/server.ts` — constructs `new McpServer({name:'saki', version})` (version read via `createRequire(import.meta.url)('../../package.json').version` — a RUNTIME read, since `package.json` sits outside `tsconfig.json`'s `rootDir:"src"` and a static import would fail `tsc` regardless of `resolveJsonModule`), calls `registerStatusTool(server, () => makeCtx({client: ctx.client, cwd: ctx.cwd, json:true, write:(s)=>captured.out.push(s), writeErr:(s)=>captured.err.push(s)}))` — wait, `captured` belongs inside the tool handler (Step 3), so this factory just passes a `makeToolCtx` closure that Step 3's handler uses to build both `captured` and `ctx` together — **returns the server UNCONNECTED** (no transport, no `connect()` call — that's Step 5's job) | NEW `src/mcp/server.ts` | LOW | `src/mcp/server.test.ts` — `createSakiMcpServer(ctx)` returns an `McpServer` instance; calling it twice returns two independent instances (no shared module-level state) | Yes |
| 5 | Add `cmdMcp(ctx: Ctx): Promise<ExitCode>` in NEW `src/commands/mcp.ts` — calls `createSakiMcpServer(ctx)`, wraps `await server.connect(new StdioServerTransport())` in try/catch (a throw here → `ctx.writeErr` + `EXIT.ERROR`, AC 1.5); on success, awaits `new Promise<void>((resolve) => process.stdin.on('end', resolve))` (the EXPLICIT close-detection listener the SDK doesn't provide — see Research) before returning `EXIT.OK` | NEW `src/commands/mcp.ts` | MED (new command, long-lived process) | covered by Step 7/8 integration + real-process tests | Yes |
| 6 | Wire `saki mcp` into the command table: import `cmdMcp` in `src/index.ts`; add `{path:['mcp'], usage:'saki mcp', summary:'start an MCP server exposing saki's journey commands as typed tools', flags:{cwd:'string'}, run:(ctx)=>cmdMcp(ctx)}` to `COMMANDS` — **`flags` is `{cwd:'string'}` only, no `json`**: a long-lived stdio server has no human-vs-JSON output toggle, so `--json` is deliberately not offered rather than silently ignored | `src/index.ts` | LOW | `src/index.test.ts` — 2 direct assertions: `matchCommand(['mcp'])?.def.path` equals `['mcp']`; `helpText()` contains `'saki mcp'` | Yes |
| 7 | Integration tests against the REAL `McpServer` (via `createSakiMcpServer`) + the SDK's `InMemoryTransport.createLinkedPair()` + the SDK's `Client` class (no real stdio, no real process — fast): (a) AC 1.1 — reachable-backend `routedCtx`-style stub → `isError:false`, `content` matches `saki status --json`'s JSON body byte-for-byte; (b) AC 1.2 — unreachable backend (`down:true`) → `isError:true`, `content` includes the real returned JSON body (`backendReachable:false`) AND the synthesized `"Exited with code 3 (UNREACHABLE)"` line (this criterion is now written against what `cmdStatus` ACTUALLY produces — a returned code, no hint — not an imagined thrown-CliError payload); (c) AC 1.3 — `tools/list` → exactly one tool named `saki_status` with `inputSchema:{}` and the stated `annotations`; (d) two-calls-in-one-session isolation — call `saki_status` reachable then unreachable on the SAME connected server, assert the second response's `content` contains ONLY the second call's data (proves the fresh-`captured`-per-call fix); (e) unregistered tool name — `tools/call` with `name:'saki_nonexistent'` → the SDK returns a JSON-RPC error, not a hang, not an uncaught exception | NEW `src/commands/mcp.test.ts` | MED | test names: `mcp: saki_status happy path`, `mcp: saki_status backend unreachable (returned-code path)`, `mcp: tools/list contains exactly saki_status with schema+annotations`, `mcp: back-to-back calls are isolated`, `mcp: unregistered tool name errors cleanly` | Yes |
| 8 | AC 1.4 — real-process stdout-purity + graceful-close test: spawn `node_modules/.bin/tsx src/index.ts mcp` (never `dist/`, never bare `npm run` — see Research) as a REAL child process with piped stdio; write a `tools/list` then one `tools/call` (`saki_status`) over its stdin; capture ALL stdout bytes and assert every newline-delimited frame `JSON.parse`s cleanly (no stray non-protocol bytes — proves the `__stdio purity__` invariant); THEN `child.stdin.end()`, race the `'exit'` event against an explicit failure-timeout, assert `exitCode === 0` (proves the Step 5 stdin-`'end'` listener actually closes the process). Explicit vitest test timeout `15_000` (spawn + tsx transpile + MCP handshake routinely exceeds the 5000ms default) | `src/commands/mcp.test.ts` (same file) | MED | test name: `mcp: real process stdout is pure MCP frames and exits 0 on stdin close` | Yes (grouped with Step 7) |
| 9 | AC 1.5 — transport-init-failure unit test: stub `StdioServerTransport`'s constructor (via `vi.mock` or a constructor-arg injection point) to throw inside `cmdMcp`'s `server.connect(new StdioServerTransport())` call, assert `cmdMcp` returns `EXIT.ERROR` and `ctx.writeErr` received a message (not `err.stack`) — no hang, no uncaught throw | `src/commands/mcp.test.ts` (same file) | LOW | test name: `mcp: transport init failure exits non-zero with a message, not a stack trace` | Yes (grouped with Step 7) |
| 10 | Update `docs/cli-reference.md` — add the `saki mcp` command entry (CLAUDE.md checklist: "a route absent from the reference is unshipped") | `docs/cli-reference.md` | LOW | doc-only, no test | Yes |

---

## User Role Coverage

Not applicable in the traditional web-app sense — `saki` is a local, single-operator CLI/MCP tool with no
auth roles.

| Role | Can Do | Cannot Do | Auth Guard | Entry Point |
|------|--------|-----------|------------|--------------|
| MCP client (any agent) | query `tools/list` (returns exactly `saki_status`) and call `saki_status` over stdio | call any other tool (not registered yet — slices 2-4); expect a 6-way distinct failure signal beyond the code embedded in content text | none (matches the CLI's own no-auth-gate posture; loopback-only backend, `docs/project-context.md` § Invariants) | `saki mcp` (stdio) |
| Process supervisor (the MCP host that spawned `saki mcp`) | read the process's own exit code + stderr on a startup failure (AC 1.5) | inspect MCP protocol content (that's the MCP client's channel, not the OS process channel) | none (OS process semantics) | `saki mcp`'s process exit/stderr |

---

## Plan Wiring

### Flow 1: MCP client checks status via `saki_status`
```
MCP client (tools/call saki_status)
  → StdioServerTransport (src/commands/mcp.ts, connected in cmdMcp)
  → registerStatusTool's handler (src/mcp/tools/status.ts) — allocates fresh {captured, ctx} per call
  → exitCodeToToolResult(() => cmdStatus(freshCtx), captured) (src/mcp/result.ts)
  → cmdStatus(ctx) (src/commands/status.ts:53, UNCHANGED — reused verbatim)
  → ctx.client.health('go') / StudioClient (src/client.ts)
  → GET http://127.0.0.1:8788/api/health
```

### Flow 2: `saki mcp` process startup + graceful shutdown
```
saki mcp (CLI entry, src/index.ts COMMANDS)
  → cmdMcp(ctx) (src/commands/mcp.ts, NEW)
  → createSakiMcpServer(ctx) (src/mcp/server.ts, NEW) — builds + registers, UNCONNECTED
  → server.connect(new StdioServerTransport())  [throw → EXIT.ERROR + writeErr, AC 1.5]
  → await stdin 'end' listener (explicit — the SDK doesn't provide this) → EXIT.OK
```

---

## Compatibility & Consumers

None — additive only. `cmdStatus` is imported and called unchanged (no signature change); `src/index.ts`
gains one new `CommandDef` entry (additive); `package.json` gains two new dependencies (additive, first
production deps). No existing surface is changed or removed.

**Forward compatibility:** additive-only.

---

## Migration Checklist

N/A — no database, no schema (CLI/MCP process only).

---

## Branch Points (pre-declared)

- Step 5: `server.connect()` throwing sync vs. the transport failing async after connect → auto-handle
  both the same way (one try/catch around `connect()`) — reversible, cheapest uniform handling.
  `AUTO-RESOLVED: how to detect transport-init failure (AC 1.5) → wrap server.connect() in try/catch,
  treat any throw during connect as init failure — the SDK doesn't distinguish sync/async failure modes
  in its public API.`
- Step 2: a thrown non-`CliError` (a genuine bug) reaching the helper → map to `EXIT.ERROR`-shaped
  `isError:true` with `err.message` only (never `err.stack`), mirroring `main()`'s own catch-all —
  reversible, matches precedent + closes the Security review's info-leak warning.
  `AUTO-RESOLVED: unknown-throw shape → mirror main()'s catch-all mapping (EXIT.ERROR), message only,
  never the stack, so no internal detail reaches the MCP transcript.`
- Step 5: the SDK provides no stdin-close signal → add an explicit `process.stdin.on('end', ...)`
  listener rather than relying on SDK behavior that doesn't exist — reversible (the listener is the CLI's
  own code, not an SDK dependency). `AUTO-RESOLVED: graceful-close mechanism → explicit stdin 'end'
  listener in cmdMcp, verified against the installed SDK (no 'end' handler registered by the transport
  itself).`

No irreversible/HIGH-tier forks in this slice (no auth, no DB, no destructive op). The two rplan-review
architecture blockers (per-tool file seam, exit-code fidelity) were DESIGN decisions with one clearly
correct answer given the codebase's stated conventions (CLAUDE.md's "one file per command," rule 1's
"exit code is the API") — resolved in-plan, not treated as forks.

---

## Unknowns (must be <= 2)

None. All research questions resolved above (SDK API + actual installed-package behavior confirmed by
the rplan-review QA pass; `cmdStatus`'s throw/return behavior confirmed by reading the source).

---

## No-Gos

- Will NOT add any tool besides `saki_status` this slice (slices 2-4 add the rest, each as its own
  `src/mcp/tools/<tool>.ts` module following this slice's `registerStatusTool` pattern).
- Will NOT implement HTTP/SSE transport (PRD §11 — stdio only).
- Will NOT auto-start the Go backend from `cmdMcp` (PRD §11 — same precondition as every other command).
- Will NOT modify `cmdStatus` itself (REUSE, unchanged, per §16).
- Will NOT add a request timeout to `StudioClient`'s underlying fetch — the security review's "unbounded
  wait if the backend is wedged" finding is real but is PRE-EXISTING CLI-wide behavior (the same
  characteristic a human hitting Ctrl-C works around today), not a regression this slice introduces;
  fixing it would touch `src/client.ts` for every command, well beyond this slice's boundary. Logged as a
  follow-on concern, not fixed here.
- Will NOT test the MCP SDK's own protocol-level validation (e.g. a malformed `initialize` handshake) —
  that is the SDK's responsibility as a trusted dependency, not this slice's code.

---

## Implementation Completeness Checklist

**User Coverage**
- [x] Both roles (MCP client, process supervisor) in the coverage matrix
- [x] Full call chain in Plan Wiring (Flow 1 + Flow 2)
- [x] No auth guard needed — documented why (matches CLI's existing no-auth posture)
- [x] Edge cases documented: backend unreachable (AC 1.2), unregistered tool (Step 7e), transport-init
      failure (AC 1.5), graceful close (Step 8)

**Database & Migrations** — N/A, no schema.

**API Layer** — N/A REST; the MCP tool is the equivalent surface, covered by Plan Wiring + Steps 3/6.

**Service / Business Logic**
- [x] Every function named with file path (Steps 2-6)
- [x] Side effects: none beyond stdout/stdin (documented)
- [x] Error paths: UNREACHABLE (AC 1.2), transport-init failure (AC 1.5), non-CliError throw (Step 2 test e), hint-absent CliError (Step 2 test d), unregistered tool (Step 7e)

**Frontend** — N/A, no UI (headless MCP server).

**Compatibility & Consumers**
- [x] Filled — "None — additive only"
- [x] Prior slices — N/A, slice 1

**Plan Wiring**
- [x] Both flows end-to-end
- [x] No vague verbs — every step names exact function + file

---

## Evidence Ledger

### Blocking (must be empty to present)

*(none — all 12 blockers from the rplan-review panel (Backend ×4, Architecture ×2, QA ×5, Product ×1 —
after dedup) were fixed in-place above: package.json version read via runtime `createRequire` (Step 4);
fresh `captured`+`Ctx` per call (Step 3); exit-code-preserving content composition (Step 2); explicit
stdin-'end' close mechanism (Step 5) + its test (Step 8); testability seam via `createSakiMcpServer`
(Step 4) + `InMemoryTransport` integration tests (Step 7); hint-absent + non-CliError-throw unit tests
(Step 2); real-process test pinned to `tsx`, never `dist/` (Step 8); unregistered-tool-name test (Step 7e);
`err.stack` never in content (Step 2 + Security review); `inputSchema`+`annotations` declared (Step 3))*

### Advisory (visible, never gates)

| Step | Note | Evidence |
|------|------|----------|
| — | `StudioClient`'s underlying fetch has no request timeout — a wedged (not down) backend blocks a tool call indefinitely. Pre-existing CLI-wide behavior (Security review), not a slice-1 regression; logged as a follow-on, not fixed here (No-Gos) | `src/client.ts` (grep confirms zero timeout-related code) |
| — | The MCP SDK's own protocol validation (malformed `initialize`, etc.) is trusted, not tested by this slice (Product review) | No-Gos |
| — | CLAUDE.md's CLI file-layout description doesn't yet name `src/mcp/` — worth a doc update at some point (Architecture review), not blocking this slice's code | Advisory only |
| — | All anchors verified, all targets have anchor parents and creating steps, no unchecked items on state-changing steps, no unknowns above LOW | self-audit against `src/index.ts:308-353`, `src/commands/status.ts:39-131`, `src/ctx.ts`, `src/exit.ts:8,21-35`, `src/client.ts:125,157,183-185`, `src/output.ts:63-73`, `src/commands/doctor.test.ts:1-40`, `vitest.config.ts`, the installed `@modelcontextprotocol/sdk@1.30.0` package (rplan-review QA verification) |

**Blocking: 0 → READY.**

---

## Success Criteria

- [x] 1.1 `saki_status` happy path — `isError:false`, content matches `saki status --json` verbatim (test: `mcp: saki_status happy path`) — PASS
- [x] 1.2 `saki_status` backend-unreachable — `isError:true`, real returned-code body + synthesized exit-code line present (test: `mcp: saki_status backend unreachable (returned-code path)`) — PASS
- [x] 1.3 `tools/list` — exactly `saki_status`, with `inputSchema:{}` + annotations (test: `mcp: tools/list contains exactly saki_status with schema+annotations`) — PASS
- [x] 1.4 real-process stdout purity + graceful close — every byte is a valid MCP frame, process exits 0 on stdin close (test: `mcp: real process stdout is pure MCP frames and exits 0 on stdin close`) — PASS
- [x] 1.5 transport-init failure — non-zero exit + stderr message (no stack trace), no hang (test: `mcp: transport init failure exits non-zero with a message, not a stack trace`) — PASS
- [x] Back-to-back call isolation (test: `mcp: back-to-back calls are isolated`) — PASS
- [x] Unregistered tool name handled cleanly (test: `mcp: unregistered tool name errors cleanly`) — PASS
- [x] `npm run typecheck` passes with the new files — PASS (exit 0)
- [x] `npm test` — all new + existing tests green — PASS (21 files, 318 tests)
- [x] `npm run test:coverage` — new files at/above the 80% floor — PASS (overall 95.03%; `src/mcp/`: 96%/80%/85.71%/100%; `mcp.ts`: 100%/50%/100%/100%)

---

## Annotation Space

> Reviewed by `/saki-builder:rplan-review` 2026-08-16 — 5 domain experts (Backend, Security, Architecture,
> QA, Product), 12 blockers found and fixed in-place, 3 findings downgraded to Advisory with reasoning
> (unbounded fetch wait — pre-existing/out-of-scope; SDK protocol validation — trusted dependency; CLAUDE.md
> doc drift — non-blocking). Blocking: 0 → READY.

---
Status: [x] Draft  [x] Annotated  [x] Approved (rplan-review clean)  [ ] In Progress  [ ] Complete
Readiness Gate: [x] Evidence Ledger present and every blocking item cited  [x] Blocking Set empty  [x] Unknowns <= 2
