# EXECUTION PLAN: MCP surface — Slice 2 (read-only journey tools)

**Date:** 2026-08-16
**Blocking items:** 0 (see Evidence Ledger — reviewed by rplan-review, 9 blockers fixed pre-implementation)
**Risk Score:** LOW
**Unknown Count:** 0 / 2 max
**Behavior Spec:** N/A (backend/CLI-only, no user-visible UI)
**Source PRD:** `tasks/prd-mcp-surface-saki-mcp.md` § Slice 2 (Locked, SHIP·READY)
**Prior slices:** `tasks/mcp-surface-saki-mcp-slice1-plan.md` (read) — the shipped shape is authoritative
over the PRD's §7 prose where they'd differ: `exitCodeToToolResult(fn, {out:string[]})` (NOT `{out,err}` —
the `err` field was removed in slice 1's review-fix pass, d95947a, as dead code); each tool lives in its
own `src/mcp/tools/<tool>.ts` exporting `register<Tool>Tool(server, makeToolCtx)`; `cmdMcp` is
lazy-imported from `src/index.ts` (dynamic import, not static); `ctx.writeErr` routes to real
`process.stderr`, never captured; every non-OK path gets a synthesized `Exited with code N (NAME)` content
block. This slice reuses ALL of it unchanged — no seam changes.
**Appetite:** ~5 agent tasks (5 acceptance criteria) — within the PRD's medium band
**Kill-if:** 5.1/5.2 (exit-code/verdict matrix) cannot be driven to 100% within appetite (PRD §6)

## Problem Statement

When my coding-agent harness needs to see this repo's roadmap, doctor status, a PRD's content, or the
studio's held runs — the read-only "what's the state of things" half of the journey — I want each as its
own typed MCP tool, reusing slice 1's translation seam, so the visibility half of the journey works before
slice 3 adds the write-heavy run lifecycle.

---

## Concrete Example Output

```
# tools/list now returns 5 tools: saki_status, saki_doctor, saki_roadmap_list, saki_prd_show, saki_runs

# saki_prd_show is the first tool with a REAL parameter:
{"jsonrpc":"2.0","method":"tools/call","id":5,
 "params":{"name":"saki_prd_show","arguments":{"target":"F3"}}}
# -> isError:false, content[0].text = the exact JSON `saki prd show F3 --json` prints

# saki_prd_show with a path that escapes the repo — rejected BEFORE cmdPrdShow ever runs:
{"jsonrpc":"2.0","method":"tools/call","id":5.5,
 "params":{"name":"saki_prd_show","arguments":{"target":"../../etc/some.md"}}}
# -> isError:true, content[0].text = 'target "../../etc/some.md" resolves outside the repo (/repo) — refusing to read it'

# saki_doctor, engine profile misconfigured — the fix hint IS in the JSON body (engines[].fix), verified:
{"jsonrpc":"2.0","method":"tools/call","id":6,"params":{"name":"saki_doctor","arguments":{}}}
# -> isError:true, content = [the doctor JSON report incl. engines[].fix, "Exited with code 1 (ERROR)"]

# saki_roadmap_list / saki_runs take no arguments (same shape as saki_status):
{"jsonrpc":"2.0","method":"tools/call","id":7,"params":{"name":"saki_runs","arguments":{}}}
# -> isError:false, content[0].text = the exact JSON `saki runs --json` prints
```

---

## Research findings

- **`cmdDoctor(ctx, positionals, flags)`** (`src/commands/doctor.ts:18`) — takes an optional `--profile
  <dir>` flag (`flags.profile`); returns `EXIT.OK`/`EXIT.ERROR`, never throws for a normal doctor failure.
  **Verified precisely (rplan-review QA + Product, cross-checked against the actual file):** `cmdDoctor`
  calls `emit(res, ...)` — which writes the FULL `DoctorResult` (including every `EngineReport.fix` field,
  `types.ts:109`) to `ctx.write` → `captured.out` — **before** the `ctx.writeErr('fix (...): ...')` loop
  that only formats the same data as a human-readable line. So the fix hint DOES reach MCP `content`, via
  the JSON body's `engines[].fix` field — it is NOT silently dropped (an earlier review draft claimed
  otherwise; checked against `doctor.ts:30-38` and found false). What's genuinely missing is a TEST that
  asserts on the right thing: the parsed JSON body's `engines[].fix`, not a `writeErr`-only formatted
  string. MCP tool: `inputSchema: { profile: z.string().optional() }`.
- **`cmdRoadmapList(ctx)`** (`src/commands/roadmap.ts:43`) — no params. `fail()`s with `EXIT.NOT_FOUND`
  when no `tasks/roadmap.md` exists — a REAL thrown-`CliError` path this slice can test natively.
- **`cmdRuns(ctx)`** (`src/commands/runs.ts:21`) — no params. Its own logic never returns non-OK, but
  `ctx.client.get('/api/runs', ...)` (`runs.ts:22`) throws a `CliError` on a down backend exactly like
  `cmdStatus`'s `/api/health` call (slice 1, `mcp.test.ts:52-61`, `EXIT.UNREACHABLE`=3) — this is the first
  time that throw path is wrapped for `/api/runs`, and it needs its own test (not previously planned).
  MCP tool: `inputSchema: {}`.
- **`cmdPrdShow(ctx, target)`** (`src/commands/prd.ts:47`, now with `looksLikePath` exported at line 10 —
  see the security fix below) — **the first tool with a real, externally supplied string parameter.**
  `target` is a roadmap id (`E12`/`F3`) or a `.md` path (`resolveTargetPrdPath`, `prd.ts:16-26`).
  `fail()`s `EXIT.USAGE` on an empty target; `EXIT.NOT_FOUND` on THREE distinct unresolvable shapes — no
  roadmap file (`prd.ts:23-25`), an unknown roadmap id (`resolve.ts` `findItem`), or a resolved path not on
  disk (`prd.ts:36-41` `fetchPrd`) — all real thrown-`CliError`s a caller (an agent passing a stale/wrong
  id, the single most likely real failure) can hit. MCP tool: `inputSchema: { target: z.string() }` (no
  `.min(1)` — an empty string deliberately reaches `cmdPrdShow`'s own `EXIT.USAGE` path rather than being
  rejected at the MCP schema layer, so that failure mode stays observable through the same seam as every
  other tool's; a fully OMITTED `target` is rejected by the SDK's own Zod validation before the handler
  runs — a distinct, protocol-level failure shape, out of scope for this slice per the PRD's non-goals on
  MCP-primitive scope).
  **Security finding, verified and FIXED in this slice (rplan-review Security — real, not a false
  positive):** `target` flowing into `resolvePath(ctx.cwd, t)` when `looksLikePath` is TRUE lets a caller
  escape `ctx.cwd` via `../` segments or an absolute path. This is the SAME code `saki prd show <target>`
  (the CLI) already exercises — but the **threat model differs for MCP**: the CLI requires a human to
  deliberately type the path; an MCP tool call's `target` argument can be steered by an LLM agent acting on
  content already in its context (a roadmap item, another file) — a confused-deputy/indirect-injection
  shape — and the full file content then lands in the calling agent's transcript (the exact class of leak
  CLAUDE.md's Secrets rule exists to prevent). This repo already has a proven containment pattern for the
  identical shape: `backend/domain/lock.go:108-115` (`ValidateLockRequest`) cwd-contains a `.md` path before
  reading it. Step 4 below ports that pattern into the MCP tool boundary (NOT into `cmdPrdShow`/
  `resolveTargetPrdPath` themselves — those stay REUSE-unchanged per the PRD's §16; the check runs in the
  new `src/mcp/tools/prd-show.ts` file, before `cmdPrdShow` is ever called).
- **Call isolation (rplan-review Backend + Product — a regression-class already found once on this exact
  seam):** slice 1 shipped a dedicated test proving a FRESH `CapturedIO`/`Ctx` is built per tool call
  (`mcp.test.ts:73-103`, "back-to-back calls are isolated"). All 4 new tools MUST follow the same "never
  hoisted" rule (already true of `status.ts`'s pattern, which this slice copies) — but copying by hand
  across 4 new files, with no test proving it, is exactly how the bug reappears. Step 6 adds an explicit
  cross-tool isolation case.
- **Tool-count growth check** (rplan-review Architecture, slice 1): confirms `src/mcp/tools/<tool>.ts`
  scales as intended — this slice adds 4 sibling files, `cmdMcp`/`createSakiMcpServer` unchanged in shape.

---

## Steps

| # | Action | Files | Risk | Test | Committable? |
|---|--------|-------|------|------|-------------|
| 1 | Add `registerDoctorTool(server, makeToolCtx)` in NEW `src/mcp/tools/doctor.ts` — `inputSchema: { profile: z.string().optional() }`, `annotations: {readOnlyHint:true, destructiveHint:false, idempotentHint:true, openWorldHint:false}`, `description: 'can codex/opencode actually run a saki-builder command, before you dispatch a run'`. Handler builds a **fresh** `CapturedIO`+`Ctx` per call (never hoisted — mirrors `status.ts:18-27` exactly) and calls `cmdDoctor(ctx, [], args.profile ? {profile: args.profile} : {})` via `exitCodeToToolResult` | NEW `src/mcp/tools/doctor.ts` | LOW | covered by step 6 | Yes |
| 2 | Add `registerRoadmapListTool(server, makeToolCtx)` in NEW `src/mcp/tools/roadmap-list.ts` — `inputSchema: {}`, same annotations pattern, `description: 'work items in this repo'`, fresh `CapturedIO`+`Ctx` per call, wraps `cmdRoadmapList(ctx)` | NEW `src/mcp/tools/roadmap-list.ts` | LOW | step 6 | Yes |
| 3 | Add `registerRunsTool(server, makeToolCtx)` in NEW `src/mcp/tools/runs.ts` — `inputSchema: {}`, same annotations, `description: 'runs the studio still holds'`, fresh `CapturedIO`+`Ctx` per call, wraps `cmdRuns(ctx)` (the list command — NOT `cmdRunTail`/`cmdRunStop`, which take a caller-supplied `runId` and are explicitly out of scope for this slice, deferred to slice 3) | NEW `src/mcp/tools/runs.ts` | LOW | step 6 | Yes |
| 4 | Add `registerPrdShowTool(server, makeToolCtx)` in NEW `src/mcp/tools/prd-show.ts` — `inputSchema: { target: z.string() }`, same annotations, `description: 'print a PRD by roadmap id or a .md path; a path is read relative to the repo — an escaping path (../, absolute) is refused'`. Handler: **before** calling `cmdPrdShow`, if `looksLikePath(args.target)` (imported from `../../commands/prd.js`, now exported) AND the resolved absolute path is not `ctx.cwd` itself and does not start with `ctx.cwd + path.sep` (mirrors `backend/domain/lock.go:108-115`'s containment check), return `{content:[{type:'text', text: 'target "<target>" resolves outside the repo (<cwd>) — refusing to read it'}], isError:true}` immediately — `cmdPrdShow` is never called on an escaping path. Otherwise, fresh `CapturedIO`+`Ctx` per call, `exitCodeToToolResult(() => cmdPrdShow(ctx, args.target), captured)` | NEW `src/mcp/tools/prd-show.ts`; `src/commands/prd.ts` (export `looksLikePath`, line 10 — purely additive, no behavior change) | MED (new validation logic) | step 6 | Yes |
| 5 | Wire all four into `createSakiMcpServer` (`src/mcp/server.ts`) — extract the inline `makeToolCtx` lambda `server.ts` currently passes only to `registerStatusTool` into a single `const makeToolCtx = () => ({client: ctx.client, cwd: ctx.cwd})`, then pass that SAME reference to all 5 `register*Tool` calls (not 5 separate inline lambdas) | `src/mcp/server.ts` | LOW | no test of its own — proven by step 6's tests all passing through one server instance | Yes |
| 6 | Integration tests — one new file, same pattern as slice 1's `mcp.test.ts` (`createSakiMcpServer` + `InMemoryTransport.createLinkedPair` + SDK `Client`): (a) `saki_doctor` happy (all engines ok) → isError:false; (b) `saki_doctor` a failed engine → isError:true, **assert on the parsed JSON body's `engines[].fix` field** (not a stderr-line substring — see Research); (c) `saki_doctor` with a `profile` arg → the stub's request received `?profile=<value>` (proves the MCP `inputSchema` param actually threads through — untested by (a)/(b) alone); (d) `saki_roadmap_list` happy — content matches `roadmap list --json`; (e) `saki_roadmap_list` no `tasks/roadmap.md` → isError:true, thrown-`EXIT.NOT_FOUND`(4) code line; (f) `saki_runs` happy, empty list; (g) `saki_runs` backend unreachable → isError:true, `"Exited with code 3 (UNREACHABLE)"`; (h) `saki_prd_show` happy with a roadmap-id `target` (e.g. `"F3"`) → content matches `prd show F3 --json`; (i) `saki_prd_show` happy with a `.md`-path `target` (the `looksLikePath` branch — the one this slice's security fix and Backend's review both flag as previously untested) → isError:false, content matches the real file; (j) `saki_prd_show` with an escaping `target` (`"../outside.md"` and an absolute path outside `ctx.cwd`) → isError:true, the boundary-check message, **and `cmdPrdShow` never gets called** (assert no `/api/prd` or `/api/roadmap` request was made for this case — proves the check runs BEFORE the wrapped command, not after); (k) `saki_prd_show` with an empty `target` → isError:true, `EXIT.USAGE`(2); (l) `saki_prd_show` with an unknown roadmap id (e.g. `"E999"`) → isError:true, thrown-`EXIT.NOT_FOUND`(4) — the realistic "wrong id" failure; (m) cross-tool call isolation — call `saki_doctor` (failing) then `saki_roadmap_list` (happy) back-to-back on ONE connected server, assert the second response contains no trace of the first's content; (n) final `tools/list` — exactly 5 tools, each carrying `readOnlyHint:true, destructiveHint:false` | NEW `src/commands/mcp-slice2.test.ts` | MED | 14 named cases above (a)-(n) | Yes |
| 7 | Update `docs/cli-reference.md`'s `saki mcp` section — bump the tool count note from "v1 registers `saki_status`" to name all 5 tools now live | `docs/cli-reference.md` | LOW | doc-only | Yes |

---

## User Role Coverage

| Role | Can Do | Cannot Do | Auth Guard | Entry Point |
|------|--------|-----------|------------|--------------|
| MCP client (any agent) | call `saki_status`, `saki_doctor`, `saki_roadmap_list`, `saki_prd_show`, `saki_runs`; a malformed/omitted argument gets a protocol-level schema rejection from the SDK (a different, non-`isError` shape than every described `isError:true` case above — a caller must branch on both) | call any write tool (slices 3-4); read a `.md` file outside the repo via `saki_prd_show` (blocked by step 4's containment check) | none (matches CLI's no-auth posture) | `saki mcp` (stdio) |

---

## Plan Wiring

### Flow: MCP client reads a PRD by roadmap id or path
```
MCP client (tools/call saki_prd_show {target:"F3"})
  → registerPrdShowTool's handler (src/mcp/tools/prd-show.ts, NEW)
  → [if looksLikePath(target) && escapes ctx.cwd: return isError:true immediately — cmdPrdShow never called]
  → exitCodeToToolResult(() => cmdPrdShow(ctx, "F3"), captured) (src/mcp/result.ts, REUSE unchanged)
  → cmdPrdShow(ctx, target) (src/commands/prd.ts:47, REUSE unchanged)
  → resolveTargetPrdPath + fetchPrd (prd.ts:16,31)
  → ctx.client.get('/api/roadmap'), ctx.client.get('/api/prd')
```
(`saki_doctor`/`saki_roadmap_list`/`saki_runs` mirror slice 1's Flow 1 exactly, swapping `cmdStatus` for
`cmdDoctor`/`cmdRoadmapList`/`cmdRuns`, no boundary check — only `saki_prd_show` takes a path-shaped input.)

---

## Compatibility & Consumers

`src/commands/prd.ts`'s `looksLikePath` function changes from module-private to `export`ed (line 10) — a
pure additive change (no behavior change to the function or to `cmdPrdShow`/`resolveTargetPrdPath`, which
still call it exactly as before). Consumers: `grep -rn looksLikePath src/` → only `prd.ts` itself today;
`updated in step 4` adds the one new consumer (`src/mcp/tools/prd-show.ts`). Every other wrapped command
(`cmdDoctor`, `cmdRoadmapList`, `cmdRuns`, `cmdPrdShow` itself) is imported and called unchanged.
`createSakiMcpServer` gains 4 more registration calls (additive) plus the `makeToolCtx` extraction (step 5,
a pure refactor of `server.ts`'s own internals — no external signature change).

**Forward compatibility:** additive-only (the `looksLikePath` export is the one non-purely-additive-looking
change, but it exports an already-existing, unchanged function — no caller's behavior changes).

---

## Migration Checklist

N/A — no database, no schema.

---

## Branch Points (pre-declared)

- Step 1: `saki doctor`'s `--profile` flag → expose as an optional MCP param (`profile:
  z.string().optional()`), passed through only when set — reversible, matches the CLI's own optional-flag
  shape. `AUTO-RESOLVED: --profile as MCP tool param → optional string, omitted arg = omitted flag.`
- Step 4: `target`'s security posture → **fixed, not deferred** (reversing the original draft's "no new
  mitigation" call). The rplan-review Security pass found the "already-reviewed, no new attack surface"
  claim unsubstantiated (`grep` for any existing traversal test/doc returned nothing) and identified a
  real MCP-specific threat-model change (agent-steerable input vs. human-typed input) with a proven,
  in-repo fix pattern (`lock.go:108-115`) — cheap, narrow, and scoped to the new MCP boundary file, not the
  shared REUSE-unchanged command. `AUTO-RESOLVED: target escaping ctx.cwd on the looksLikePath branch →
  reject at the MCP tool boundary (src/mcp/tools/prd-show.ts), before cmdPrdShow is ever called — mirrors
  this repo's own lock.go containment pattern for the identical .md-path shape.`

No irreversible/HIGH-tier forks (no auth, no DB, no destructive op — all four tools are read-only; the new
containment check is itself a safety addition, not a risk).

---

## Unknowns (must be <= 2)

None.

---

## No-Gos

- Will NOT add `saki_workitems` this slice (deferred out of v1 scope per PRD §11/§12).
- Will NOT modify `cmdDoctor`/`cmdRoadmapList`/`cmdRuns`/`cmdPrdShow`/`resolveTargetPrdPath` themselves
  (REUSE, unchanged) — the path-containment check lives in the new MCP tool file, not in these.
- Will NOT change `exitCodeToToolResult` or the `CapturedIO` shape — slice 1's seam is reused as-is.
- Will NOT wrap `cmdRunTail`/`cmdRunStop` as `saki_runs` — that's the list command only; the per-run
  commands (which take a caller-supplied `runId`) are slice 3's job.
- Will NOT add a `.min(1)` constraint to `target`'s Zod schema — the empty-string case is deliberately left
  to reach `cmdPrdShow`'s own `EXIT.USAGE` path (see Research), not rejected at the protocol layer.

---

## Implementation Completeness Checklist

**User Coverage** — matrix complete; 4 new read-only capabilities + 1 boundary-enforced restriction
(escaping paths), no auth guard needed (unchanged posture); the schema-rejection shape is noted.
**Database & Migrations** — N/A.
**API Layer** — N/A REST; MCP tool surface covered by Plan Wiring + Steps 1-5.
**Service / Business Logic** — every function named with file path (Steps 1-5); error paths: doctor
failure (JSON-field assertion fixed), roadmap-not-found (thrown), runs-unreachable (thrown, newly
covered), empty-target (thrown), unknown-id-not-found (thrown, newly covered), path-escape (newly added,
boundary-rejected) — all covered in Step 6.
**Frontend** — N/A.
**Compatibility & Consumers** — filled (the one non-additive-looking change, `looksLikePath` export, has
its consumer traced). **Prior slices** — slice 1 read (see header).
**Plan Wiring** — end-to-end, no vague verbs; the boundary-check short-circuit is shown explicitly in the
Flow diagram so it's clear `cmdPrdShow` is skipped on an escaping path, not merely warned about.

---

## Evidence Ledger

### Blocking (must be empty to present)

*(none — all 9 blockers from the rplan-review panel (Backend ×4, Security ×2, QA ×3, Product ×2 — after
dedup and one downgrade) were fixed in-place above: doctor's fix-hint test now asserts the correct JSON
field, not a false "silently dropped" claim (verified against `doctor.ts` directly — Product's stronger
claim did not survive verification and was downgraded to a test-correctness fix, per the dedup rule);
`profile` param now has its own threading test (case c); `annotations` + `description` added to all 4
tools + asserted in the final `tools/list` test (case n); explicit "fresh per call, never hoisted"
statement + a cross-tool isolation test (case m); the `.md`-path branch of `prd_show` now has a dedicated
happy-path test (case i) AND the security fix (case j); `saki_runs` gets a failure-path test (case g);
`saki_prd_show`'s realistic wrong-id failure now has a test (case l); the path-traversal security finding
is FIXED (containment check at the MCP boundary, step 4) with a regression test proving both the rejection
AND that the wrapped command is never called on a rejected path (case j))*

### Advisory (visible, never gates)

| Step | Note | Evidence |
|------|------|----------|
| 6 | A populated-list case for `saki_runs`'s happy path (only empty-list is tested) — low risk, `runs.ts` has no branch logic of its own for this and the new wrapper adds none; not fixed this round | QA review, `src/commands/runs.ts:21-33` |
| 4 | `saki_prd_show`'s full file content is returned unbounded into the MCP result channel — matches the CLI's existing behavior (not new), but worth remembering if the MCP host ever logs full tool-call payloads | Security review |
| — | `--profile` pass-through verified clean end-to-end (`doctor.ts:23`→`http.go:236-240`→`doctor.go:33-48`, no shell/string-concat use, stays a typed argument) | Security review, self-verified |
| — | All anchors verified, all targets have creating steps, no unknowns above LOW | self-audit against `src/commands/doctor.ts:18`, `src/commands/roadmap.ts:43`, `src/commands/runs.ts:21-33`, `src/commands/prd.ts:10-47`, `backend/domain/lock.go:108-115`, slice 1's shipped `src/mcp/{result,server}.ts` + `src/mcp/tools/status.ts` |

**Blocking: 0 → READY.**

---

## Success Criteria

- [x] `saki_doctor` happy path passes (test: `mcp2: doctor happy`)
- [x] `saki_doctor` engine-failure path passes, asserting the JSON `engines[].fix` field (test: `mcp2: doctor engine failure`)
- [x] `saki_doctor` `profile` arg threads through to the request (test: `mcp2: doctor profile param`)
- [x] `saki_roadmap_list` happy path passes (test: `mcp2: roadmap_list happy`)
- [x] `saki_roadmap_list` thrown-NOT_FOUND path passes (test: `mcp2: roadmap_list no roadmap file`)
- [x] `saki_runs` happy path passes (test: `mcp2: runs happy`)
- [x] `saki_runs` unreachable-backend path passes (test: `mcp2: runs backend unreachable`)
- [x] `saki_prd_show` happy path (roadmap id) passes (test: `mcp2: prd_show happy id`)
- [x] `saki_prd_show` happy path (`.md` path) passes (test: `mcp2: prd_show happy path`)
- [x] `saki_prd_show` escaping-path rejection passes, and `cmdPrdShow` is proven never called (test: `mcp2: prd_show path escape rejected`)
- [x] `saki_prd_show` empty-target (thrown USAGE) path passes (test: `mcp2: prd_show empty target`)
- [x] `saki_prd_show` unknown-id (thrown NOT_FOUND) path passes (test: `mcp2: prd_show unknown id`)
- [x] Cross-tool call isolation passes (test: `mcp2: cross-tool calls are isolated`)
- [x] `tools/list` returns exactly the 5 tools shipped so far, each with `readOnlyHint:true` (test: `mcp2: tools/list has 5 tools`)
- [x] `npm run typecheck` passes
- [x] `npm test` — all green (333/333)
- [x] `npm run test:coverage` — new files 100%, suite total 95.44% (≥ 80% floor)

---

## Annotation Space

> Reviewed by `/saki-builder:rplan-review` 2026-08-16 — 4 domain experts (Backend, Security, QA, Product),
> 9 blockers found and fixed in-place (including a genuine MCP-specific path-traversal/confused-deputy
> finding from Security, closed via a boundary check ported from `backend/domain/lock.go`'s existing
> pattern), 1 blocker (Product's "fix hint silently dropped" claim) verified against the code and found
> incorrect — downgraded to a test-correctness fix once the real gap (wrong assertion target, not missing
> data) was identified. Blocking: 0 → READY.

---
Status: [x] Draft  [x] Annotated  [x] Approved (rplan-review clean)  [x] In Progress  [x] Complete
Readiness Gate: [x] Evidence Ledger present  [x] Blocking Set empty  [x] Unknowns <= 2
