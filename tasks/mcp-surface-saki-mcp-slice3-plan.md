# EXECUTION PLAN: MCP surface — Slice 3 (run lifecycle tools)

**Date:** 2026-08-16
**Blocking items:** 0 (self-reviewed against source; routed to a domain-expert pass before implementation)
**Risk Score:** MED (process-spawning surface — first slice with a real state-changing tool)
**Unknown Count:** 0 / 2 max
**Behavior Spec:** N/A (backend/CLI-only, no user-visible UI)
**Source PRD:** `tasks/prd-mcp-surface-saki-mcp.md` § Slice 3 (Locked, SHIP·READY)
**Prior slices:** `tasks/mcp-surface-saki-mcp-slice1-plan.md`, `tasks/mcp-surface-saki-mcp-slice2-plan.md`
(both read) — the shipped shape is authoritative: `exitCodeToToolResult(fn, {out:string[]})`; each tool in
its own `src/mcp/tools/<tool>.ts` exporting `register<Tool>Tool(server, makeToolCtx: ToolCtxFactory)`;
`READ_ONLY_ANNOTATIONS` + `ToolCtxFactory` live in `src/mcp/tool-ctx.ts` (slice 2 review-fix, commit
8a7431c) — slice 3 reuses `ToolCtxFactory` but does NOT reuse `READ_ONLY_ANNOTATIONS` (run_start/run_stop
are not read-only); a boundary-check pattern that trims caller input EXACTLY ONCE and threads the SAME
trimmed value into both the guard and the wrapped `cmd*` call (slice 2's reviewer-caught regression,
commit 8a7431c) — this slice's `assertRunVerb`/`assertRunEngine` validators must not repeat that mistake.
**Appetite:** ~6 agent tasks (5 stated ACs + 1 DRY extraction) — within the PRD's medium band
**Kill-if:** 5.2/5.4 (failure-path fidelity) cannot be driven to 100% within appetite (PRD §6)

## Problem Statement

When my coding-agent harness needs to kick off, watch, or stop a saki-builder run — the write half of the
core build loop (J1/J2) — I want `saki_run_start`, `saki_run_tail`, `saki_run_stop` as MCP tools, reusing
slices 1-2's translation seam, so an agent can drive the whole build loop without shelling out.

---

## Concrete Example Output

```
# tools/list now returns 8 tools: the 5 from slices 1-2, plus saki_run_start, saki_run_tail, saki_run_stop

# Start a build, get a runId back immediately (no --follow — saki_run_tail is the blocking call):
{"jsonrpc":"2.0","method":"tools/call","id":8,
 "params":{"name":"saki_run_start","arguments":{"verb":"build","target":"F5"}}}
# -> isError:false, content[0].text = {"runId":"r1","deduped":false} (same JSON `saki run build F5 --json` prints)

# Unknown verb — same validation error the CLI's `saki run <bad-verb>` emits, byte-identical (assertRunVerb,
# shared by src/index.ts's ['run'] handler and this tool — not re-derived):
{"jsonrpc":"2.0","method":"tools/call","id":9,
 "params":{"name":"saki_run_start","arguments":{"verb":"launch-the-missiles","target":"F5"}}}
# -> isError:true, content[0].text = 'Exited with code 2 (USAGE): unknown run verb: launch-the-missiles'

# Tail a run to its terminal state — the call blocks until the run finishes, mirroring `saki run tail`'s
# own untimed blocking behavior (PRD §12 lean, no MCP-specific timeout):
{"jsonrpc":"2.0","method":"tools/call","id":10,"params":{"name":"saki_run_tail","arguments":{"runId":"r1"}}}
# -> isError:false (or true on a failed run's terminal verdict), content = [streamed lines..., final JSON
#    {runId,status,exitCode} block]

# Stop a running run:
{"jsonrpc":"2.0","method":"tools/call","id":11,"params":{"name":"saki_run_stop","arguments":{"runId":"r1"}}}
# -> isError:false, content[0].text = {"stopped":true,"runId":"r1"}
```

---

## Research findings

- **`cmdRunStart(ctx, verb: RunVerb, arg: string, flags: RunStartFlags)`** (`src/commands/run.ts:163`) —
  `verb` is a CLOSED union already validated by the caller (index.ts validates BEFORE calling it, not
  inside it — see below); `arg` is trimmed once inside `resolveTarget` (`run.ts:107`) and, for most verbs
  (`pickup`/`proto`/`rplan`), passed straight through into `buildRunPrompt(verb, target)` = `` `/saki-builder:${verb}
  ${target}` `` with NO further validation — this is BY DESIGN (`run.ts:105`: "the raw argument; these
  skills take an item id themselves"). `RunStartFlags = {profile?, follow?, engine?: RunEngine, heal?}`.
- **Verb validation lives in `src/index.ts:190-196`, INLINE, not in `run.ts`** — throws
  `` CliError(`unknown run verb: ${verb || '(none)'}`, EXIT.USAGE, `expected one of ${RUN_VERBS.join(', ')} — or \`run tail\` / \`run stop\``) ``.
  Criterion 3.2 requires the MCP tool's `isError:true` result to carry "the same validation error the CLI
  emits" — so this message must not be re-derived (drift risk) or produced via a Zod `z.enum(RUN_VERBS)`
  schema (a DIFFERENT failure shape: SDK-level protocol rejection, not an `isError:true` tool result, per
  slice 2's own established precedent that a validation criterion routes through `exitCodeToToolResult`,
  not the schema layer). **Step 1 extracts this into `export function assertRunVerb(v: string): RunVerb`
  in `run.ts`**, called from BOTH `index.ts`'s `['run']` handler (replacing the inline throw — pure
  behavior-preserving refactor, same message) and the new MCP tool.
- **Engine validation lives in `src/index.ts:68-80`** (the `startRun` helper), same shape:
  `` CliError(`unknown engine: ${flags.engine}`, EXIT.USAGE, `expected one of ${RUN_ENGINES.join(', ')}`) ``.
  Same DRY argument applies. **Step 1 also extracts `export function assertRunEngine(v: string): RunEngine`.**
- **`--follow` is deliberately NOT exposed on `saki_run_start`'s MCP schema (scope cut, see Branch
  Points).** Criterion 3.4 itself describes the intended MCP shape as TWO separate calls (`saki_run_start`
  then `saki_run_tail`), not one blocking `--follow` call — and PRD §12's open question ("saki_run_tail's
  MCP call can stay open for as long as a build takes... no special timeout") is scoped explicitly to
  `run_tail`, implying `run_start` is meant to return fast. The MCP tool never sets `flags.follow` (always
  falsy → `cmdRunStart` returns immediately per its own `if (flags.follow) return cmdRunTail(...)` branch,
  `run.ts:223`).
- **`cmdRunTail(ctx, runId)`** (`src/commands/runs.ts:56`) — blocks on `streamRun` (`src/sse.ts:48`) until
  the SSE stream's `event: end` frame arrives; every non-hidden line reaches `ctx.write` via `onLine`
  (`sse.ts:103`), which `buildToolCtx` already routes into `captured.out` — so EVERY streamed line becomes
  its own MCP content block, and the `emit({runId, ...end}, {json:true}, ctx.write)` (`runs.ts:66`, since
  `buildToolCtx` forces `ctx.json = true`) pushes the terminal `status`/`exitCode` block. `runId` empty →
  `fail(...)` `EXIT.USAGE` (`runs.ts:57`), already tested at the CLI level.
  **Correction (Backend review, verified against `result.ts:36-38`): the terminal JSON block is NOT
  necessarily the LAST content block.** `exitCodeToToolResult` only returns early on `EXIT.OK`; on a
  non-OK return (`cmdRunTail`'s failure path returns `EXIT.ERROR`, it never throws — `runs.ts:63,73`) it
  APPENDS a synthesized `"Exited with code 1 (ERROR)"` text block AFTER `captured.out` — so on a failed
  run the terminal JSON is SECOND-to-last, not last. Step 6 case (d) must locate it by parsing each
  content block and checking for a `status` field, not by indexing `content[content.length - 1]`.
- **`cmdRunStop(ctx, runId)`** (`src/commands/runs.ts:36`) — `runId` empty → `EXIT.USAGE`; a stop on an
  already-finished/unknown run gets a friendlier `CliError` re-thrown with a NOT_FOUND hint
  (`runs.ts:41-47`) — REUSE unchanged, no new logic needed.
- **Security — process-spawning surface, verified against the actual backend (NOT assumed safe):**
  `backend/infra/spawner.go:21-23` states explicitly, in an existing code comment: "the operator prompt is
  [passed via argv/env], NEVER interpolated into [a shell] string — so a prompt with quotes/newlines/[shell
  metacharacters can't break out]" — confirmed by reading `spawner.go:232-244`, which passes the prompt as
  an environment variable (`"SAKI_PROMPT="+prompt`) to `exec.Command`, never through a shell. **This means
  there is no NEW command-injection surface**: an MCP caller's `target` string reaches the spawned agent
  process exactly the way a human's CLI-typed argument already would, through the same non-shell-interpolated
  path. The verb is closed-union validated (`assertRunVerb`, Step 1) before it ever reaches `buildRunPrompt`.
  **What genuinely differs for MCP (the real, accepted risk):** an LLM agent, if steered by untrusted
  content already in its context, could autonomously trigger a real run (spawn a NEW coding-agent session)
  without a human confirming first — this is not a boundary escape, it is the tool's STATED PURPOSE (PRD
  J1/J2: "drive the build loop without shelling out"), already accepted as this PRD's core value
  proposition and consistent with `saki mcp`'s no-auth, local-single-operator trust model
  (`docs/project-context.md` § Invariants, PRD §11 Non-Goals).
- **BLOCKER (Backend + Security review, independently confirmed) — `saki_run_start`'s `target` reaches an
  UNCONTAINED filesystem path for the `build` verb, worse than slice 2's read-only leak.**
  `resolveTarget`'s `build` branch (`run.ts:113-127`) unconditionally calls `resolveTargetPrdPath(ctx, a)`
  for any `.md`-shaped argument (`looksLikePath`, `prd.ts:12-13`), which resolves an absolute or
  `../`-relative path with **zero containment check** (`prd.ts:21`) and passes it straight into `fetchPrd`
  → `GET /api/prd?path=...`. The Go backend's `ReadPrd` (`backend/usecase/prd.go:33-42`) only requires a
  `.md` suffix — no cwd-containment (contrast the roadmap/lock handlers, which ARE cwd-contained). The
  resolved path becomes both the fetched PRD path AND (via `laneKey`/the build prompt) part of what the
  newly spawned, **unsandboxed** agent process (CLAUDE.md rule 3) operates on. `{"verb":"build",
  "target":"../../../etc/passwd.md"}` (or any reachable `.md` file/path outside the repo) is a real,
  unmitigated escape — the exact shape slice 2 fixed for the READ-only `saki_prd_show`, left open here for
  a tool that also SPAWNS a process. **AUTO-RESOLVED FIX: port slice 2's `pathEscapesCwd` containment
  check (`src/mcp/tools/prd-show.ts`) into `src/mcp/tools/run-start.ts`, gated on `verb === 'build' &&
  looksLikePath(target)`, run BEFORE `cmdRunStart` is ever called** — same refusal shape
  (`exitCodeToToolResult` + `fail(msg, EXIT.USAGE)`), same trim-once-reuse-everywhere discipline slice 2's
  reviewer caught a regression on (commit 8a7431c). Added as Step 2a below; this is why the slice's Risk
  Score is MED and the security audit is mandatory, not merely a formality.
- **Warning, accepted (Security review) — `profile` flows into a real spawned process's config resolution**
  (`configDir` → `engineProfileEnv`, `backend/infra/spawner.go:316-331` → `CLAUDE_CONFIG_DIR`/
  `XDG_CONFIG_HOME`/`CODEX_HOME`), a materially different consequence than doctor's read-only `--profile`
  check (slice 2). **Not a NEW capability** — `saki build X --profile <dir>` already lets a human point at
  any directory today; the MCP tool exposes exactly the same CLI-level capability unchanged, not a
  widening. Left unvalidated in Step 2, consistent with the PRD's "pure translation layer" architecture
  decision; the security audit re-confirms this is unchanged from the CLI's existing surface.
- **Warning, accepted (Security review) — no concurrency/abuse cap on `saki_run_start` beyond `build`'s
  existing per-lane dedupe** (`activeBuildIds`, `run.ts:143-155`); the other 9 verbs have none, and neither
  does the backend generally (`grep` for rate-limiting in `backend/` returns nothing). Pre-existing,
  systemic, and out of scope for a zero-new-backend-surface translation slice (PRD §16) — noted in the
  Advisory ledger as a candidate for a future backend-level fix, not this slice's job.
- **Tool-count growth check** (mirrors slice 2's Architecture note): `createSakiMcpServer` grows from 5 to
  8 `register*Tool` calls, shape unchanged.

---

## Steps

| # | Action | Files | Risk | Test | Committable? |
|---|--------|-------|------|------|-------------|
| 1 | Extract `assertRunVerb(v: string): RunVerb` and `assertRunEngine(v: string): RunEngine` into `src/commands/run.ts` (each a thin wrapper around `isRunVerb`/`isRunEngine` + `fail(...)`, byte-identical message/hint to the current inline throws). Update `src/index.ts`'s `['run']` handler and `startRun()` helper to call these instead of inlining the checks — pure behavior-preserving refactor (same CliError message/hint/code) | `src/commands/run.ts`, `src/index.ts` | LOW (refactor, existing `run.test.ts`/`index.test.ts` pin the message) | existing tests must stay green unchanged + step 6 reuses the same message | Yes |
| 2 | Add `registerRunStartTool(server, makeToolCtx)` in NEW `src/mcp/tools/run-start.ts` — `inputSchema: { verb: z.string(), target: z.string().optional(), profile: z.string().optional(), engine: z.string().optional(), heal: z.boolean().optional() }` (verb/engine deliberately `z.string()`, not `z.enum(...)` — see Research), `annotations: {readOnlyHint:false, destructiveHint:false, idempotentHint:false, openWorldHint:false}`, `description: 'start a headless saki-builder skill run (build/pickup/proto/rplan/...); returns immediately with a runId — call saki_run_tail separately to block for its result'`. Handler: fresh `CapturedIO`+`Ctx` per call; trim `target` exactly once (`const target = (args.target ?? '').trim()`); **BEFORE calling `cmdRunStart`, if `assertRunVerb(args.verb) === 'build' && looksLikePath(target) && pathEscapesCwd(target, base.cwd)` (the same `pathEscapesCwd` shape as `src/mcp/tools/prd-show.ts`, imported/re-implemented here — see Security finding in Research), refuse via `exitCodeToToolResult(async () => fail('target "${target}" resolves outside the repo (${base.cwd}) — refusing to start a build against it', EXIT.USAGE), captured)` — `cmdRunStart` is never called on an escaping build target**; otherwise `exitCodeToToolResult(async () => cmdRunStart(ctx, assertRunVerb(args.verb), target, {profile: args.profile, engine: args.engine !== undefined ? assertRunEngine(args.engine) : undefined, heal: args.heal}), captured)` — `assertRunVerb`/`assertRunEngine` throw INSIDE the wrapped closure so a bad verb/engine surfaces as `isError:true` via the SAME seam every other failure uses, not a bespoke branch | NEW `src/mcp/tools/run-start.ts` | MED (first state-changing tool + the slice's one real security fix) | step 6 | Yes |
| 3 | Add `registerRunTailTool(server, makeToolCtx)` in NEW `src/mcp/tools/run-tail.ts` — `inputSchema: { runId: z.string() }`, `annotations: {readOnlyHint:true, destructiveHint:false, idempotentHint:true, openWorldHint:false}`, `description: 'stream a run to its terminal state and return the verdict — blocks for as long as the run takes, mirroring \`saki run tail\`'s own untimed behavior'`. Handler wraps `cmdRunTail(ctx, args.runId)` unchanged, same fresh-per-call pattern | NEW `src/mcp/tools/run-tail.ts` | LOW | step 6 | Yes |
| 4 | Add `registerRunStopTool(server, makeToolCtx)` in NEW `src/mcp/tools/run-stop.ts` — `inputSchema: { runId: z.string() }`, `annotations: {readOnlyHint:false, destructiveHint:true, idempotentHint:false, openWorldHint:false}`, `description: 'stop a running run'`. Handler wraps `cmdRunStop(ctx, args.runId)` unchanged | NEW `src/mcp/tools/run-stop.ts` | LOW | step 6 | Yes |
| 5 | Wire all three into `createSakiMcpServer` (`src/mcp/server.ts`) — three more `register*Tool(server, makeToolCtx)` calls added to the existing 5, same `makeToolCtx` reference | `src/mcp/server.ts` | LOW | no test of its own — proven by step 6 | Yes |
| 6 | Integration tests — NEW file, same `createSakiMcpServer` + `InMemoryTransport` + SDK `Client` pattern as slices 1-2, PLUS an SSE-stream-capable route stub (mirrors `src/commands/runs.test.ts`'s `routed()`'s `stream: string[]` support + its `evFrame`/`endFrame` helpers, since `saki_run_tail` hits `/events/:id`, not a plain JSON endpoint): (a) `saki_run_start` happy — verb `build`, target a ROADMAP ID (matches the PRD's own Concrete Example) with a FULL stub chain (`/api/roadmap` found w/ an item carrying `childPrd`, `/api/prd` found, `/api/run` returns `{runId}`) → isError:false, content matches `{runId,deduped}` (3.1) — resolved from QA's stub-chain-ambiguity finding: a bare id needs all three stubs, a `.md`-path target would skip `/api/roadmap`, so the test must stub for the id it actually uses; (b) `saki_run_start` unknown verb → isError:true, content contains the EXACT `assertRunVerb` message, byte-compared against `run.ts`'s own constant (3.2); (c) `saki_run_start` unknown engine → isError:true, content contains the `assertRunEngine` message (DRY-extracted twin of 3.2); (d) `saki_run_tail` on a run that ends in `error` → isError:true, content contains a block whose PARSED JSON has `status:"error"` — found by scanning `content` for a block that parses as JSON with a `status` field, NOT by indexing the last block (Backend-review correction: `exitCodeToToolResult` appends its synthesized exit-code text AFTER the terminal JSON on the non-OK path, so the JSON is second-to-last here, not last) (3.3); (e) `saki_run_start` (build, real roadmap id, full stub chain) then `saki_run_tail`, where the SECOND call's `runId` argument is the value PARSED out of the FIRST call's own content (never a hardcoded id) — the run ends `done` → both isError:false, and the test asserts the two calls carried the same id, closing QA's "could pass without threading the real id" gap (3.4 — asserts on the SDK `Client`/`InMemoryTransport` only, never `child_process`); (f) `saki_run_stop` happy — POST `/api/run/:id/stop` succeeds → isError:false, then a follow-up `saki_runs` call (stubbed to report the run as `status:"error"`, `exitCode:null` — matching `runManager.ts:1165`'s real stop shape) shows it stopped (3.5); (g) `saki_run_tail` empty `runId` → isError:true, `EXIT.USAGE`(2); (h) `saki_run_stop` on an unknown/already-finished run → isError:true, thrown-`EXIT.NOT_FOUND`(4) re-thrown message (`runs.ts:41-47`); (i) `saki_run_start` with an ESCAPING build target (`"../../../etc/passwd.md"`) → isError:true, the boundary-check message, **and neither `/api/prd` nor `/api/run` is ever requested** — the regression test for this slice's one real security fix (Step 2), same shape as slice 2's `mcp2: prd_show path escape rejected`; (j) `saki_run_start` with an EMPTY target for a required-target verb (`build`) → isError:true, `EXIT.USAGE`(2) — QA-flagged gap, the most basic failure path for the slice's headline tool, previously untested; (k) `saki_run_start` with a target given to a NO-target verb (`reviewer`) → isError:true, `EXIT.USAGE`(2) — mirror-image of (j); (l) cross-tool isolation — `saki_run_start` (failing, bad verb) then `saki_runs` (happy) back-to-back, assert no leakage; (m) final `tools/list` — exactly 8 tools; `saki_run_start`/`saki_run_stop` carry `readOnlyHint:false`, `saki_run_tail` carries `readOnlyHint:true`, `saki_run_stop` carries `destructiveHint:true` | NEW `src/commands/mcp-slice3.test.ts` | MED | 13 named cases (a)-(m) | Yes |
| 7 | Update `docs/cli-reference.md`'s `saki mcp` tool table — add the 3 new rows, bump the running total from 5 to 8 | `docs/cli-reference.md` | LOW | doc-only | Yes |

---

## User Role Coverage

| Role | Can Do | Cannot Do | Auth Guard | Entry Point |
|------|--------|-----------|------------|--------------|
| MCP client (any agent) | call `saki_run_start` (any `RUN_VERBS` verb + optional target/profile/engine/heal), `saki_run_tail`, `saki_run_stop`, plus every slice 1-2 tool | call `saki_run_start` with `--follow` (not exposed — must call `saki_run_tail` separately, see Research); call any tool with a verb/engine outside the closed unions (rejected `isError:true` via `assertRunVerb`/`assertRunEngine`) | none (matches CLI's no-auth, local-single-operator posture — unchanged from slices 1-2) | `saki mcp` (stdio) |

---

## Plan Wiring

### Flow: MCP client starts, then tails, a run to completion
```
MCP client (tools/call saki_run_start {verb:"build", target:"F5"})
  → registerRunStartTool's handler (src/mcp/tools/run-start.ts, NEW)
  → exitCodeToToolResult(async () => cmdRunStart(ctx, assertRunVerb("build"), "F5", {...}), captured)
  → assertRunVerb("build") (src/commands/run.ts, NEW — Step 1) — passes, RUN_VERBS includes "build"
  → cmdRunStart(ctx, "build", "F5", {}) (src/commands/run.ts:163, REUSE unchanged)
  → resolveTarget → fetchPrd/resolveTargetPrdPath (build's target must resolve to a real PRD path)
  → ctx.client.post('/api/run', {prompt:"/saki-builder:build <path>", cwd, meta:{kind:'build', laneKey}})
  ← {runId:"r1"} → isError:false, content[0].text = '{"runId":"r1","deduped":false}'

MCP client (tools/call saki_run_tail {runId:"r1"})
  → registerRunTailTool's handler (src/mcp/tools/run-tail.ts, NEW)
  → exitCodeToToolResult(() => cmdRunTail(ctx, "r1"), captured)
  → cmdRunTail(ctx, "r1") (src/commands/runs.ts:56, REUSE unchanged)
  → streamRun(ctx.client, "r1", onLine) (src/sse.ts:48, REUSE unchanged) — BLOCKS until `event: end`
  → ctx.client.request('/events/r1', {headers:{Accept:'text/event-stream'}})
  ← every streamed line -> captured.out; final {runId,status,exitCode} JSON -> captured.out (ctx.json=true)
  ← isError:false if status==="done", else isError:true, content's last block carries the verdict
```
(`saki_run_stop` mirrors slice 2's Flow 1 shape exactly, swapping `cmdPrdShow` for `cmdRunStop` — no
boundary check, since `runId` is an opaque studio-issued id, not a filesystem path.)

---

## Compatibility & Consumers

`assertRunVerb`/`assertRunEngine` are NEW exports in `src/commands/run.ts`, but their bodies are lifted
VERBATIM from `src/index.ts`'s existing inline checks — `git diff` on the extraction step must show
byte-identical message/hint/code, so no consumer of the CLI's current error text (a human reading stderr,
a test asserting on the message) sees any change. Consumers: `grep -rn 'unknown run verb\|unknown engine'
src/` → only `index.ts` today; `updated in step 1` moves both into `run.ts` and re-points `index.ts` at
them. `createSakiMcpServer` gains 3 more registration calls (additive). Every other wrapped command
(`cmdRunStart`, `cmdRunTail`, `cmdRunStop`) is imported and called unchanged.

**Forward compatibility:** additive-only. The `assertRunVerb`/`assertRunEngine` extraction is a pure
refactor (existing `run.test.ts`/`index.test.ts` assertions on the exact error text must still pass
unmodified — if they don't, the extraction introduced a drift and step 1 is not done).

---

## Migration Checklist

N/A — no database, no schema, no new backend routes (PRD §11 Non-Goal).

---

## Branch Points (pre-declared)

- Step 2: expose `--follow` on `saki_run_start`'s MCP schema, or not? **AUTO-RESOLVED: do NOT expose it.**
  Criterion 3.4 describes the two-call pattern (`start` then `tail`) as the intended MCP flow, and PRD
  §12's open question about `run_tail`'s unbounded blocking duration is scoped specifically to that tool,
  implying `run_start` is meant to return fast. A `--follow`-shaped single call would make ONE MCP tool
  call block for an entire build's duration under a flag an agent might not think to check, which is worse
  MCP ergonomics than two explicit calls. Reversible: adding the flag later is a pure additive schema
  change if a real need surfaces (§12-style trigger: an MCP workflow demonstrably wants one blocking call).
- Step 2: `verb`/`engine` as `z.enum(...)` (protocol-level rejection) vs `z.string()` + `assertRunVerb`/
  `assertRunEngine` (isError-level, CLI-identical message)? **AUTO-RESOLVED: `z.string()` + assert
  functions** — criterion 3.2 explicitly requires "the same validation error the CLI emits" as an
  `isError:true` tool result, which only the assert-function path produces; a Zod enum would instead
  reject at the SDK/protocol layer with a structurally different (non-`isError`) shape, failing 3.2 as
  written.
- Step 2: MCP-specific mitigation for the "an agent can now autonomously start a real run" risk?
  **AUTO-RESOLVED: none beyond existing verb/engine validation, PLUS the build-target containment guard.**
  Verified against the actual spawner (`backend/infra/spawner.go:21-23,232-244`) that the prompt is never
  shell-interpolated — no NEW command-injection surface. The remaining risk (autonomous run-starting
  without a human) is this PRD's stated purpose (J1/J2), not a bug; inventing a new authz gate here would
  contradict PRD §16's "pure translation layer, zero new backend surface" architecture decision and PRD
  §11's Non-Goals. **This lean initially missed one real, independently-confirmed gap** (Backend + Security
  review): `saki_run_start`'s `target`, for the `build` verb, resolves an uncontained filesystem path — the
  identical class of bug slice 2 fixed for `saki_prd_show`, left open here for a tool that also spawns a
  process. Fixed in Step 2 by porting the same `pathEscapesCwd` containment check. The security audit
  (mandatory for this slice) re-verifies this fix independently rather than taking this plan's word for it.
- Step 2: `profile` and per-verb concurrency — **AUTO-RESOLVED: accept unchanged, do not add new gates.**
  `profile` mirrors an existing CLI capability (`saki build X --profile <dir>`) exactly — not a widening.
  Concurrency/abuse capping is a pre-existing, systemic backend gap (no verb has one except `build`'s
  per-lane dedupe, and the backend has no general rate-limiting) — out of scope for a zero-new-backend-
  surface translation slice; recorded in the Advisory ledger as a candidate for future backend work.

No irreversible/HIGH-tier fork requiring a human pause: no auth change, no DB, no destructive DATA
operation (`saki_run_stop` stops a process, it does not delete anything — matches the CLI's own
`run stop`, already exposed today).

---

## Unknowns (must be <= 2)

None.

---

## No-Gos

- Will NOT expose `--follow` on `saki_run_start` (see Branch Points) — the MCP flow is always the two-call
  `start` then `tail` pattern.
- Will NOT modify `cmdRunStart`/`cmdRunTail`/`cmdRunStop`/`streamRun` themselves (REUSE, unchanged) — all
  new validation lives in the extracted `assertRunVerb`/`assertRunEngine` helpers and the new tool files.
- Will NOT add a new backend route, a request timeout, or any MCP-specific cancellation wiring for
  `saki_run_tail`'s blocking call (PRD §12 lean: mirror the CLI's own untimed behavior).
- Will NOT add authorization/allowlisting for which verbs an MCP caller may start — out of scope per PRD
  §11 Non-Goals (no new backend surface) and §16 (pure translation layer); the security audit verifies
  this is a deliberate, already-accepted PRD decision, not a gap.
- Will NOT change `exitCodeToToolResult`, `CapturedIO`, `READ_ONLY_ANNOTATIONS`, or `ToolCtxFactory` —
  slices 1-2's seams are reused as-is (this slice ADDS a non-read-only annotations shape per tool, it does
  not touch the shared read-only constant).

---

## Implementation Completeness Checklist

**User Coverage** — matrix complete; 3 new capabilities (2 state-changing, 1 blocking-read), no auth guard
needed (unchanged posture), the `--follow`-not-exposed restriction is noted.
**Database & Migrations** — N/A.
**API Layer** — N/A REST; MCP tool surface covered by Plan Wiring + Steps 1-5.
**Service / Business Logic** — every function named with file path (Steps 1-5); error paths: unknown verb
(newly extracted, isError-tested), unknown engine (newly extracted, isError-tested), run-tail failure
verdict (thrown-free, content-carried), run-tail empty runId (thrown, USAGE), run-stop unknown run
(thrown, NOT_FOUND, re-thrown message) — all covered in Step 6.
**Frontend** — N/A.
**Compatibility & Consumers** — filled (the `assertRunVerb`/`assertRunEngine` extraction is the one
non-additive-looking change, verbatim-message-preserving, consumer traced). **Prior slices** — slices 1
and 2 read (see header).
**Plan Wiring** — end-to-end, no vague verbs; the two-call `start`→`tail` sequence shown explicitly so the
scope cut (no `--follow`) is visible in the flow, not just asserted in prose.

---

## Evidence Ledger

### Blocking (must be empty to present)

*(none remaining — 2 real blockers surfaced by the domain-expert pass (Backend + Security, independently
confirming the SAME finding) were fixed in-place above before implementation started: (1) `saki_run_start`'s
`target` had no cwd-containment check for the `build` verb — the identical bug class slice 2 fixed for
`saki_prd_show`, here reaching an unsandboxed process spawn rather than a read-only leak — fixed by porting
`pathEscapesCwd` into Step 2, regression-tested by Step 6 case (i); (2) Backend also caught that the plan's
"terminal JSON is always the LAST content block" claim was true only on the success path — `result.ts`'s
append-on-failure ordering means it's second-to-last on a failed run — corrected in Step 6 case (d)'s
assertion strategy before any test was written, not discovered as a failing test. QA's 3 blockers (missing
id-threading in the two-call success loop; the empty-required-target path shipping untested; case (a)'s
under-specified stub chain) are folded into Step 6 cases (a), (e), (j). The one genuine security question
this slice raises beyond the path-containment fix — command injection via a caller-controlled prompt — was
checked against the actual spawner code and found already mitigated (argv/env passing, never shell
string-interpolation); recorded as a Research finding + a Branch Point, not left implicit. A dedicated
post-implementation security audit is still mandatory for this slice per its process-spawning +
caller-supplied-string surface — the domain-expert pass is a pre-implementation gate, not a substitute for
it.)*

### Advisory (visible, never gates)

| Step | Note | Evidence |
|------|------|----------|
| 6 | `saki_run_start`'s `heal` flag is silently dropped by `cmdRunStart` for non-`wrap` verbs, whereas the CLI actively rejects `--heal` on non-`wrap` verbs as USAGE via its flag-spec layer (`index.ts:104-108`) — an accepted strictness gap inherent to wrapping the underlying function rather than the CLI's argument parser, not fixed this round (low risk: a caller who passes `heal` for a non-`wrap` verb gets silent no-op, not corruption) | Backend review; `run.ts:186`, `index.ts:104-108` |
| — | No concurrency/abuse cap on `saki_run_start` beyond `build`'s own per-lane dedupe — pre-existing, systemic (no backend rate-limiting at all), out of scope for a zero-new-backend-surface slice; candidate for a future backend-level fix | Security review; `run.ts:143-155` |
| — | `profile` flows into a real spawned process's config resolution (`CLAUDE_CONFIG_DIR`/`XDG_CONFIG_HOME`/`CODEX_HOME`) — verified as an EXISTING CLI capability (`saki build X --profile <dir>`), not a new widening from MCP exposure | Security review; `spawner.go:316-331` |
| — | PRD §12's two Slice-3 "shipped untested" risks (concurrent tool calls during an open `run_tail`; client-disconnect mid-`run_tail`) are EXPLICITLY out of this round's ≤5-AC cap per the PRD itself — not silently dropped, carried forward with their stated triggers | PRD §12, verbatim |
| — | All anchors verified against source, not a prior summary: `src/commands/run.ts:13-225`, `src/commands/runs.ts:21-74`, `src/sse.ts:48-118`, `src/index.ts:55-198`, `src/types.ts:40-78`, `backend/infra/spawner.go:21-23,232-244,316-331`, `backend/usecase/prd.go:33-42` | self-audit + domain-expert pass |

**Blocking: 0 → READY.**

---

## Success Criteria

- [x] `saki_run_start` happy path passes (full roadmap+prd+run stub chain), content carries the real `runId` (test: `mcp3: run_start happy`) → 3.1
- [x] `saki_run_start` unknown-verb path passes, message byte-matches `assertRunVerb`'s CLI text (test: `mcp3: run_start unknown verb`) → 3.2
- [x] `saki_run_start` unknown-engine path passes (test: `mcp3: run_start unknown engine`)
- [x] `saki_run_tail` failure-verdict path passes, the content block whose parsed JSON carries `status` shows `"error"` (test: `mcp3: run_tail failure verdict`) → 3.3
- [x] `saki_run_start` then `saki_run_tail` success loop completes, the second call's `runId` is the value parsed out of the first call's own content (never hardcoded), no real process spawned by the test (test: `mcp3: run_start then run_tail success loop`) → 3.4
- [x] `saki_run_stop` happy path passes, a follow-up `saki_runs` shows it stopped (test: `mcp3: run_stop happy then runs shows stopped`) → 3.5
- [x] `saki_run_tail` empty-runId (thrown USAGE) path passes (test: `mcp3: run_tail empty runId`)
- [x] `saki_run_stop` unknown-run (thrown NOT_FOUND) path passes (test: `mcp3: run_stop unknown run`)
- [x] `saki_run_start` escaping build-target rejection passes, and `cmdRunStart` is proven never called (test: `mcp3: run_start build target escape rejected`) — the slice's one real security fix
- [x] `saki_run_start` empty-target-for-required-verb (thrown USAGE) path passes (test: `mcp3: run_start empty target for build`)
- [x] `saki_run_start` target-given-to-no-target-verb (thrown USAGE) path passes (test: `mcp3: run_start target given to reviewer`)
- [x] Cross-tool call isolation passes (test: `mcp3: cross-tool calls are isolated`)
- [x] `tools/list` returns exactly 8 tools with correct per-tool annotations (test: `mcp3: tools/list has 8 tools with correct annotations`)
- [x] `npm run typecheck` passes
- [x] `npm test` — all green
- [x] `npm run test:coverage` — new files ≥ 80%
- [ ] Dedicated security audit (mandatory — process spawning + caller-supplied strings) is clean or its
      findings are fixed before this slice is marked done

---

## Annotation Space

> Self-reviewed against source before presenting (per the discipline established in slices 1-2's
> rplan-review passes — verify every claim against the actual file, not a summary). The extraction of
> `assertRunVerb`/`assertRunEngine` (Step 1) exists specifically so criterion 3.2's "same validation error
> the CLI emits" is mechanically true (one function, two call sites) rather than hoped-for via a
> hand-copied string.
>
> Reviewed by a 3-agent domain-expert pass (Backend, Security, QA) 2026-08-16, BEFORE implementation.
> Backend and Security independently converged on the same real finding — `saki_run_start`'s `target` had
> no cwd-containment check for the `build` verb, the identical bug class slice 2 fixed for `saki_prd_show`
> but here reaching an unsandboxed process spawn — fixed in Step 2 by porting `pathEscapesCwd`, with a
> regression test in Step 6(i). Backend also caught a plan-correctness error (the "terminal JSON is always
> last" claim, true only on the success path) before any test was written against the wrong assertion. QA
> found 3 test-plan gaps (missing id-threading in the two-call loop, an untested empty-required-target
> path, an under-specified happy-path stub chain) all folded into Step 6. Two accepted, documented
> non-fixes: `profile`'s reach into a spawned process's config (mirrors an existing CLI capability, not a
> widening) and the absence of any concurrency cap on run-starting (pre-existing, systemic, out of scope
> for a zero-new-backend-surface slice). Blocking: 0 → READY.

---
Status: [x] Draft  [x] Annotated  [x] Approved (domain-expert pass clean — 2 blockers fixed pre-implementation)  [ ] In Progress  [ ] Complete
Readiness Gate: [x] Evidence Ledger present  [x] Blocking Set empty  [x] Unknowns <= 2
