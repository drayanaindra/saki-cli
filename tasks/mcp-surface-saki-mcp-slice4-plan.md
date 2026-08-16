# EXECUTION PLAN: MCP surface — Slice 4 (repo/git + PRD lock tools)

**Date:** 2026-08-16
**Blocking items:** 0 (self-reviewed against source; routed to a domain-expert pass before implementation)
**Risk Score:** LOW-MED (one path-shaped argument reusing an existing guard; branch/git args already
hardened server-side, pre-existing this PRD)
**Unknown Count:** 0 / 2 max
**Behavior Spec:** N/A (backend/CLI-only, no user-visible UI)
**Source PRD:** `tasks/prd-mcp-surface-saki-mcp.md` § Slice 4 (Locked, SHIP·READY) — the FINAL slice; 13/13
tools shipped once this lands.
**Prior slices:** `tasks/mcp-surface-saki-mcp-slice{1,2,3}-plan.md` (all read) — the shipped shape is
authoritative: `exitCodeToToolResult(fn, {out:string[]})`; each tool in its own `src/mcp/tools/<tool>.ts`
exporting `register<Tool>Tool(server, makeToolCtx: ToolCtxFactory)`; `READ_ONLY_ANNOTATIONS`/
`ToolCtxFactory` live in `src/mcp/tool-ctx.ts`; the cwd-containment guard `pathEscapesCwd` lives in
`src/mcp/path-guard.ts` (slice 3 review-fix, commit 356fa11 — extracted there specifically so a THIRD
tool taking a path-shaped argument reuses it instead of copying it a third time, which this slice is).
**Appetite:** ~6 agent tasks (5 stated ACs) — within the PRD's medium band
**Kill-if:** 5.2/5.4 (failure-path fidelity) cannot be driven to 100% within appetite (PRD §6)

## Problem Statement

When my coding-agent harness needs to check/switch branches, open a merge request, or lock a PRD's
requirements — the last leg of the journey (J1: "branch, MR, and PRD-lock without shelling out") — I want
`saki_branch`, `saki_branch_list`, `saki_branch_switch`, `saki_mr_create`, `saki_prd_lock` as MCP tools,
reusing slices 1-3's translation seam and path-containment guard, so the full 13-tool journey is complete.

---

## Concrete Example Output

```
# tools/list now returns all 13 tools — the PRD's full v1 scope, shipped.

{"jsonrpc":"2.0","method":"tools/call","id":13,"params":{"name":"saki_branch","arguments":{}}}
# -> isError:false, content[0].text = the exact JSON `saki branch --json` prints

{"jsonrpc":"2.0","method":"tools/call","id":14,
 "params":{"name":"saki_branch_switch","arguments":{"branch":"feature/x","create":true}}}
# -> isError:false on success; isError:true with the REMOTE_FAILED diagnosis if switch-branch's HTTP-200
#    {ok:false,error} body fires (a dirty tree, a name git itself refuses) — never a false isError:false

{"jsonrpc":"2.0","method":"tools/call","id":15,"params":{"name":"saki_prd_lock","arguments":{"target":"F3"}}}
# -> isError:false, content[0].text = {"path":"...","locked":true,"alreadyLocked":false}

# An escaping .md target — refused BEFORE cmdPrdLock ever runs, same shape as saki_prd_show/saki_run_start:
{"jsonrpc":"2.0","method":"tools/call","id":16,
 "params":{"name":"saki_prd_lock","arguments":{"target":"../../../etc/passwd.md"}}}
# -> isError:true, content[0].text = 'Exited with code 2 (USAGE): target "..." resolves outside the repo...'
```

---

## Research findings

- **`cmdBranch(ctx)`** (`src/commands/repo.ts:28`) and **`cmdBranchList(ctx)`** (`repo.ts:40`) — no
  arguments, no caller-supplied input at all. Pure REUSE-wrap, identical shape to `saki_status`/
  `saki_roadmap_list`/`saki_runs` (slices 1-2). No security surface.
- **`cmdBranchSwitch(ctx, branch, {create})`** (`repo.ts:59`) — `branch` is a git branch NAME, not a
  filesystem path; `create` a boolean. **Verified against the actual backend, not assumed safe**: the
  server-side git-argument-injection guard (BR5) already exists and predates this PRD entirely —
  `domain.ValidateBranchName` (`backend/domain/branchname.go:13-27`) rejects a leading `-` (the classic
  git-flag-injection shape), whitespace, `..`, leading/trailing `/`, and refname-invalid characters, run
  BEFORE any git command on the `create` path (`backend/usecase/gitwrite.go:50-53`). The switch-to-existing
  path is separately guarded at the argv-construction layer: `SwitchArgs` (`backend/infra/gitargs.go:23-28`)
  emits `git switch -- <branch>` — the `--` separator stops git itself from parsing a flag-shaped existing
  branch name as an option, independent of `ValidateBranchName`. All git invocations run via fixed-argv
  `exec.Command` (`backend/infra/gitcli.go`), never a shell string — no command-injection surface exists
  or has ever existed here. **Conclusion: `saki_branch_switch` needs NO new mitigation** — it inherits
  protections that are mature, already tested (`gitargs_test.go`, `branchname_test.go`, `gitcli_test.go`),
  and were not built for this PRD. `postOk` (not `post`) is REUSE-unchanged, so a `{ok:false,error}`
  HTTP-200 body (a dirty tree, a refused name) still maps to `EXIT.REMOTE_FAILED` → `isError:true` —
  criterion 4.2's exact target.
- **`cmdMrCreate(ctx)`** (`repo.ts:82`) — no arguments. Pure REUSE-wrap; same `postOk` HTTP-200-failure
  handling as branch-switch (criterion 4.2 covers both in one acceptance criterion).
- **`cmdPrdLock(ctx, target)`** (`src/commands/prd.ts:55-68`) — **the one tool in this slice with a real
  path-shaped argument.** Calls `fetchPrd(ctx, await resolveTargetPrdPath(ctx, target))` — the IDENTICAL
  resolution chain `cmdPrdShow` uses (slice 2) and `cmdRunStart`'s build branch uses (slice 3). Both prior
  slices independently needed the same fix for the same reason: `resolveTargetPrdPath` has zero
  cwd-containment of its own for a `.md`-shaped target. `saki_prd_lock`'s MCP tool reuses
  `pathEscapesCwd` from `src/mcp/path-guard.ts` (already extracted specifically for this — slice 3's
  reviewer flagged the ORIGINAL duplication as a drift risk, and a third tool needing the identical check
  is exactly the case that extraction was for). Idempotent by design at the command level (`prd.ts:53-54`:
  "the server answers alreadyLocked rather than erroring") — criterion 4.5 exercises this directly.
- **Tool-count growth check** (mirrors slices 2-3's Architecture note): `createSakiMcpServer` grows from 8
  to 13 `register*Tool` calls — the PRD's full v1 scope. No new seam, no new pattern.

---

## Steps

| # | Action | Files | Risk | Test | Committable? |
|---|--------|-------|------|------|-------------|
| 1 | Add `registerBranchTool`/`registerBranchListTool` in NEW `src/mcp/tools/branch.ts` and `branch-list.ts` — `inputSchema: {}`, `annotations: READ_ONLY_ANNOTATIONS` (both are pure reads), wrapping `cmdBranch`/`cmdBranchList` unchanged, fresh `CapturedIO`+`Ctx` per call | NEW `src/mcp/tools/branch.ts`, `branch-list.ts` | LOW | step 5 | Yes |
| 2 | Add `registerBranchSwitchTool` in NEW `src/mcp/tools/branch-switch.ts` — `inputSchema: { branch: z.string(), create: z.boolean().optional() }`, `annotations: {readOnlyHint:false, destructiveHint:true, idempotentHint:false, openWorldHint:false}` (destructive: changes working-tree state, matching `saki_run_stop`'s reasoning from slice 3's review), `description: 'switch (or create-and-switch) the current git branch'`. Handler wraps `cmdBranchSwitch(ctx, args.branch, {create: args.create})` unchanged — NO new validation needed (see Research: server-side BR5 + argv `--` guard already cover this) | NEW `src/mcp/tools/branch-switch.ts` | LOW | step 5 | Yes |
| 3 | Add `registerMrCreateTool` in NEW `src/mcp/tools/mr-create.ts` — `inputSchema: {}`, `annotations: {readOnlyHint:false, destructiveHint:false, idempotentHint:false, openWorldHint:true}` (pushes + creates a REMOTE resource via `glab` — the one tool in this MCP surface that reaches an external system beyond the local backend, hence `openWorldHint:true`, a first for this codebase's annotation convention — see Branch Points), wraps `cmdMrCreate(ctx)` unchanged | NEW `src/mcp/tools/mr-create.ts` | LOW | step 5 | Yes |
| 4 | Add `registerPrdLockTool` in NEW `src/mcp/tools/prd-lock.ts` — `inputSchema: { target: z.string() }`, `annotations: {readOnlyHint:false, destructiveHint:false, idempotentHint:true, openWorldHint:false}` (idempotent: locking an already-locked PRD reports `alreadyLocked`, never errors — `prd.ts:61`), `description: 'freeze a PRD's requirements by roadmap id or .md path — an escaping path (../, absolute) is refused, same as saki_prd_show'`. Handler: trim `target` once, reuse `pathEscapesCwd` from `src/mcp/path-guard.ts` exactly like `prd-show.ts` (same refusal shape: `exitCodeToToolResult(async () => fail(...), captured)`), then `exitCodeToToolResult(() => cmdPrdLock(ctx, target), captured)` on the non-escaping path | NEW `src/mcp/tools/prd-lock.ts` | LOW-MED (reuses an already-proven guard, not a new one) | step 5 | Yes |
| 5 | Wire all five into `createSakiMcpServer` (`src/mcp/server.ts`) — five more `register*Tool(server, makeToolCtx)` calls added to the existing 8 | `src/mcp/server.ts` | LOW | no test of its own — proven by step 6 | Yes |
| 6 | Integration tests — NEW file, same `createSakiMcpServer` + `InMemoryTransport` + SDK `Client` pattern as slices 1-3: (a) `saki_branch` happy — content matches `saki branch --json` (4.1); (a2) `saki_branch` detached HEAD (`{branch:null}`) → isError:false, content `{branch:null}` — QA-flagged gap, `cmdBranch`'s own real branch (`repo.ts:30-34`), previously untested through the MCP wrapper; (b) `saki_branch_switch` on a dirty-tree HTTP-200 `{ok:false,error}` body (status 200, NOT a non-2xx status — `postOk`'s `ok:false` branch, not `parse()`'s status-based throw, per QA's mechanical-correctness note) → isError:true, content asserts the literal `'Exited with code 5 (REMOTE_FAILED)'` text, not just `isError:true` — the exact false-success this PRD's §2 motivating case exists to prevent (4.2); (c) `saki_mr_create` on the same HTTP-200 `{ok:false,error}` shape, same `EXIT.REMOTE_FAILED`(5) text assertion (4.2, second half); (d) `saki_branch_list` then `saki_branch_switch` to a real branch → both isError:false, content matches each CLI `--json` body (4.3); (e) `saki_mr_create` happy → isError:false, content matches `saki mr create --json` (4.4); (f) `saki_prd_lock` then `saki_prd_show` → PROVES causality, not just two independently-passing stubs (QA-flagged: `PrdResult` has no `locked` field, so a stateless per-URL stub can't distinguish "locked" from "not locked") — a bespoke STATEFUL `fetchImpl` for this one test (mirrors `mcp.test.ts`'s `let down = false` mutable-closure pattern, not the shared stateless `routedClient`) where `/api/prd`'s returned `content` carries a `<!-- prd-locked -->`-shaped marker ONLY AFTER `/api/lock-prd` has been posted — the second call's content must differ from what a call made BEFORE the lock would have returned (4.5); (g) `saki_prd_lock` with an escaping target (relative AND absolute, mirroring the slice 2/3 regression tests) → isError:true, the boundary-check message, and `cmdPrdLock` never called (no `/api/prd`/`/api/roadmap`/`/api/lock-prd` request); (h) `saki_prd_lock` with an empty target → isError:true, `EXIT.USAGE`(2); (i) `saki_branch_switch` with an empty branch name → isError:true, `EXIT.USAGE`(2); (j) `saki_branch` backend unreachable → isError:true, `EXIT.UNREACHABLE`(3) — Backend-review-flagged gap, no `down:true` case existed anywhere in this slice's list; (k) cross-tool isolation; (l) final `tools/list` — exactly 13 tools, `saki_mr_create` the only `openWorldHint:true` tool, `saki_branch_switch`/`saki_prd_lock` annotated per Steps 2/4 | NEW `src/commands/mcp-slice4.test.ts` | MED | 14 named cases (a, a2, b-l) | Yes |
| 7 | Update `docs/cli-reference.md`'s `saki mcp` tool table — add the 5 new rows, bump the running total from 8 to 13 (the PRD's full v1 scope, complete) | `docs/cli-reference.md` | LOW | doc-only | Yes |

---

## User Role Coverage

| Role | Can Do | Cannot Do | Auth Guard | Entry Point |
|------|--------|-----------|------------|--------------|
| MCP client (any agent) | call all 13 tools shipped across slices 1-4 — the PRD's complete v1 scope | read/lock a PRD whose `target` resolves outside the repo cwd (refused, step 4); anything beyond the 13 named tools (§11 Non-Goals: `saki_workitems`/`saki_roadmap_add`/`saki_proto`/`saki_screenshots` explicitly deferred) | none (unchanged posture, all 4 slices) | `saki mcp` (stdio) |

---

## Plan Wiring

### Flow: MCP client locks a PRD by roadmap id, escaping path refused
```
MCP client (tools/call saki_prd_lock {target:"F3"})
  → registerPrdLockTool's handler (src/mcp/tools/prd-lock.ts, NEW)
  → [if looksLikePath(target) && pathEscapesCwd(target, ctx.cwd): isError:true immediately — cmdPrdLock
     never called; src/mcp/path-guard.ts, REUSE from slice 3]
  → exitCodeToToolResult(() => cmdPrdLock(ctx, "F3"), captured) (src/mcp/result.ts, REUSE unchanged)
  → cmdPrdLock(ctx, target) (src/commands/prd.ts:55, REUSE unchanged)
  → resolveTargetPrdPath + fetchPrd (prd.ts:16,31) → ctx.client.get('/api/roadmap'), get('/api/prd')
  → ctx.client.postOk('/api/lock-prd', {path, cwd})
```
(`saki_branch`/`saki_branch_list`/`saki_mr_create` mirror slice 1's Flow 1 exactly — no arguments, no
boundary check. `saki_branch_switch` mirrors it with one argument that needs NO new boundary check, per
Research — the server-side git-argument guard is pre-existing and already proven.)

---

## Compatibility & Consumers

None — additive only. Every wrapped command (`cmdBranch`, `cmdBranchList`, `cmdBranchSwitch`, `cmdMrCreate`,
`cmdPrdLock`) is imported and called unchanged; no exported function's signature or behavior changes.
`createSakiMcpServer` gains 5 more registration calls (additive). `pathEscapesCwd`'s only new consumer is
`src/mcp/tools/prd-lock.ts` (`grep -rn pathEscapesCwd src/` → `path-guard.ts` (def), `prd-show.ts`,
`run-start.ts`, `updated in step 4` adds `prd-lock.ts` as the third).

**Forward compatibility:** additive-only — the last slice of this PRD; no further slices depend on this one.

---

## Migration Checklist

N/A — no database, no schema, no new backend routes (PRD §11 Non-Goal, held across all 4 slices).

---

## Branch Points (pre-declared)

- Step 2: does `saki_branch_switch` need its own copy of a git-argument-injection guard, mirroring the
  path-containment pattern from slices 2-3? **AUTO-RESOLVED: no.** Verified against the actual backend
  (`branchname.go`, `gitwrite.go`, `gitargs.go`, `gitcli.go`) that BR5 (leading-`-` rejection on create)
  and the `switch -- <branch>` argv guard (existing-branch path) both predate this PRD and already close
  the exact injection class a new MCP-specific check would target. Inventing a duplicate check here would
  contradict this PRD's own "pure translation layer, zero new backend surface" architecture decision
  (§16) for a boundary that is already provably closed — unlike slices 2-3's `pathEscapesCwd`, which
  closed a REAL, previously-unguarded gap. The security audit re-verifies this independently.
- Step 3: `saki_mr_create`'s `openWorldHint`. **AUTO-RESOLVED: `true`**, a first for this codebase's
  annotation convention (every other tool, including the process-spawning `saki_run_start`, is
  `openWorldHint:false` since the backend itself is loopback-only/closed-world). `saki_mr_create` is
  different in kind: it pushes the branch and opens a REAL merge request on a remote host via `glab` — an
  irreversible, externally-visible side effect the MCP spec's `openWorldHint` exists to flag. Reversible
  as a plan decision (a cosmetic annotation, not a behavior change) but recorded because it's the one
  tool in 13 that breaks the established `false` pattern, and a reviewer should not read that as
  inconsistency without this note.

No irreversible/HIGH-tier fork requiring a human pause: no auth change, no DB, no destructive DATA
operation this MCP layer performs directly (branch-switch and MR-creation are git/glab operations already
exposed unchanged by the existing CLI).

---

## Unknowns (must be <= 2)

None.

---

## No-Gos

- Will NOT modify `cmdBranch`/`cmdBranchList`/`cmdBranchSwitch`/`cmdMrCreate`/`cmdPrdLock` themselves
  (REUSE, unchanged) — the one new check (PRD-lock path containment) lives in the new tool file, reusing
  the existing `path-guard.ts`, not touching the shared command.
- Will NOT add a new git-argument-injection guard for `saki_branch_switch` — the existing server-side one
  (BR5 + argv `--` guard) already covers it; duplicating it would be exactly the kind of redundant,
  unreviewed check this PRD's architecture decision (§16) warns against.
- Will NOT add `saki_workitems`, `saki_roadmap_add`, `saki_proto`, or `saki_screenshots` — explicitly
  deferred, PRD §11 Non-Goals, held across all 4 slices.
- Will NOT change `exitCodeToToolResult`, `CapturedIO`, `READ_ONLY_ANNOTATIONS`, `ToolCtxFactory`, or
  `pathEscapesCwd` — every slice 1-3 seam is reused as-is.

---

## Implementation Completeness Checklist

**User Coverage** — matrix complete; 5 new capabilities (2 pure reads, 1 boundary-enforced write, 2
already-safe writes), no auth guard needed (unchanged posture across all 4 slices).
**Database & Migrations** — N/A.
**API Layer** — N/A REST; MCP tool surface covered by Plan Wiring + Steps 1-5.
**Service / Business Logic** — every function named with file path (Steps 1-5); error paths: branch-switch
HTTP-200 false-success (thrown REMOTE_FAILED, the PRD's own motivating case), mr-create same shape,
prd-lock path-escape (newly wired, boundary-rejected, reusing a proven guard), prd-lock empty target
(thrown USAGE), branch-switch empty branch (thrown USAGE) — all covered in Step 6.
**Frontend** — N/A.
**Compatibility & Consumers** — filled (`None — additive only`, verified by grep). **Prior slices** —
slices 1, 2, and 3 read (see header).
**Plan Wiring** — end-to-end, no vague verbs; the PRD-lock boundary-check short-circuit shown explicitly.

---

## Evidence Ledger

### Blocking (must be empty to present)

*(none remaining — a 3-agent domain-expert pass (Backend, Security, QA) ran against this plan before any
code was written. Backend and Security independently re-verified the Research section's "already
mitigated" claims from source, not the plan's prose, and both returned zero blockers: the branch
create/switch-to-existing asymmetry is jointly closed by `ValidateBranchName` (create path) and the
`switch -- <branch>` argv separator (existing path); `saki_mr_create` truly takes no caller input (source/
target are server-derived, never from the request body); `saki_prd_lock`'s write path is additionally
re-validated server-side by `ValidateLockRequest` (`backend/domain/lock.go:101-120`) independent of the
MCP-layer `pathEscapesCwd` check, so even a hypothetical MCP-layer bypass would still be rejected before
any mutation. QA found 2 real blockers, both fixed in Step 6 before implementation: (1) the original
`prd_lock` → `prd_show` "reflects the lock" case was unfalsifiable — `PrdResult` has no `locked` field and
the shared stateless stub can't prove the second call actually reflects the first's effect — now a bespoke
stateful stub proving causality; (2) `saki_branch`'s detached-HEAD (`branch:null`) path was untested through
the MCP wrapper with no stated trigger, unlike every other carried-forward gap — now case (a2). Backend's
warning about a missing UNREACHABLE case (this slice had none, unlike slices 1-2) is now case (j). The
dedicated post-implementation security audit (mandatory per this slice's original classification) still
runs — the pre-implementation pass is independent verification, not a substitute for it.)*

### Advisory (visible, never gates)

| Step | Note | Evidence |
|------|------|----------|
| 6 | `saki_prd_lock`'s `alreadyLocked:true` happy-repeat path (locking an already-locked PRD twice) shares one test with the happy path (case f) rather than getting its own dedicated case — low risk, `cmdPrdLock` has no branch logic of its own beyond what `postOk`'s `alreadyLocked` field already threads through unchanged | self-audit, `prd.ts:61-66` |
| — | PRD §12's "residual gap, shipped untested (Slice 2/4)" note — `saki_branch_list`'s empty-state behavior and `saki_prd_lock`'s failure paths beyond path-escape (e.g. locking a PRD that doesn't exist) — is carried forward with its stated trigger, not silently dropped | PRD §12, verbatim |
| — | All anchors verified against source, not a prior summary: `src/commands/repo.ts:1-115`, `src/commands/prd.ts:53-68`, `backend/domain/branchname.go:1-37`, `backend/usecase/gitwrite.go:42-95`, `backend/infra/gitargs.go:1-47`, `backend/infra/gitcli.go:95-103` | self-audit |

**Blocking: 0 → READY (pending the domain-expert pass below before implementation starts).**

---

## Success Criteria

- [x] `saki_branch` happy path passes (test: `mcp4: branch happy`) → 4.1
- [x] `saki_branch_switch` HTTP-200-false-success path passes, `EXIT.REMOTE_FAILED` (test: `mcp4: branch_switch dirty tree false success`) → 4.2
- [x] `saki_mr_create` HTTP-200-false-success path passes, `EXIT.REMOTE_FAILED` (test: `mcp4: mr_create false success`) → 4.2
- [x] `saki_branch_list` then `saki_branch_switch` happy loop passes (test: `mcp4: branch_list then branch_switch happy`) → 4.3
- [x] `saki_mr_create` happy path passes (test: `mcp4: mr_create happy`) → 4.4
- [x] `saki_prd_lock` then `saki_prd_show` reflects the lock, proven via a stateful stub (not two independently-passing stubs) (test: `mcp4: prd_lock then prd_show reflects lock`) → 4.5
- [x] `saki_prd_lock` escaping-target rejection passes (relative + absolute), `cmdPrdLock` proven never called (test: `mcp4: prd_lock target escape rejected`)
- [x] `saki_prd_lock` empty-target (thrown USAGE) path passes (test: `mcp4: prd_lock empty target`)
- [x] `saki_branch_switch` empty-branch (thrown USAGE) path passes (test: `mcp4: branch_switch empty branch`)
- [x] `saki_branch` detached HEAD (`branch:null`) path passes (test: `mcp4: branch detached head`) — QA-flagged gap
- [x] `saki_branch` backend unreachable path passes, `EXIT.UNREACHABLE` (test: `mcp4: branch backend unreachable`) — Backend-review-flagged gap
- [x] Cross-tool call isolation passes (test: `mcp4: cross-tool calls are isolated`)
- [x] `tools/list` returns exactly 13 tools with correct per-tool annotations (test: `mcp4: tools/list has 13 tools with correct annotations`)
- [x] `npm run typecheck` passes
- [x] `npm test` — all green
- [x] `npm run test:coverage` — new files ≥ 80%
- [ ] Dedicated security audit (mandatory, per this slice's original classification — caller-supplied
      strings flowing toward git operations) is clean or its findings are fixed before this slice is done

---

## Annotation Space

> Self-reviewed against source before presenting (per the discipline established in slices 1-3). The
> headline finding of this research pass: the risk this slice was ORIGINALLY flagged mandatory-audit for
> (branch_switch/mr_create's caller-supplied strings reaching git) turns out, on tracing the actual
> backend code, to be already fully closed by pre-existing, independently-tested protections (BR5 +
> argv `--` guards) — not a gap slice 4 introduces. The one genuinely novel risk (`saki_prd_lock`'s path
> argument) is closed by reusing slice 3's `pathEscapesCwd` extraction, which is exactly the payoff that
> extraction was built for.
>
> Reviewed by a 3-agent domain-expert pass (Backend, Security, QA) 2026-08-16, BEFORE implementation.
> Backend and Security independently re-verified the "already mitigated" reading against source and both
> confirmed it — zero blockers on the git-argument-injection question, and Security additionally found the
> `prd_lock` write path has its OWN independent server-side containment re-check (`ValidateLockRequest`,
> `backend/domain/lock.go:101-120`) beyond the MCP-layer guard, a defense-in-depth layer this plan's
> Research section hadn't cited. QA found the two real gaps: an unfalsifiable "reflects the lock" test
> design (fixed — a stateful stub proving causality) and an untested detached-HEAD path for `saki_branch`
> with no stated trigger, unlike every other carried-forward gap (fixed — new case a2). Blocking: 0 → READY.

---
Status: [x] Draft  [x] Annotated  [x] Approved (domain-expert pass clean — 2 test-design gaps fixed pre-implementation)  [ ] In Progress  [ ] Complete
Readiness Gate: [x] Evidence Ledger present  [x] Blocking Set empty  [x] Unknowns <= 2
