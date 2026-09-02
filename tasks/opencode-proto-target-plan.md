# EXECUTION PLAN: Fix OpenCode proto target forwarding

**Date:** 2026-08-25
**Updated:** 2026-08-25 — revised after implementation probe
**Blocking items:** 0
**Risk Score:** MED
**Unknown Count:** 1 / 2 max
**Behavior Spec:** N/A (backend-only)
**Source PRD:** N/A (standalone)
**Prior slices:** N/A — standalone
**Appetite:** ~2 agent tasks
**Kill-if:** N/A

## Problem Statement

When an operator runs `saki run proto F3 --engine opencode`, OpenCode starts successfully but the proto skill reports that no target was provided, so no preview is rendered. Preserve the existing command dispatch while forwarding `F3` as the proto skill argument.

---

## Concrete Example Output

Current reproduction:

```text
$ saki run proto F3 --engine opencode --json
{"runId":"2f170dde-c2d8-4d6a-b52c-8c52eda06274","deduped":false}
$ saki run tail 2f170dde-c2d8-4d6a-b52c-8c52eda06274
Provide a target: `/proto E<n>`, `/proto F<n>`, or `/proto tasks/prd-<slug>.md`.
run done (exit 0)
```

Verified current adapter argv:

```text
opencode run --format json --auto --command proto -- F3
```

Expected after the fix: OpenCode invokes the installed `proto` skill with an argument form that the skill parses as target `F3`, and the run no longer emits `Provide a target`. The exact replacement argv must be established by the focused real-CLI probe before production code changes.

---

## Steps

| # | Action | Files (exact paths) | Risk | Test | Committable? |
|---|--------|---------------------|------|------|-------------|
| 1 | Run a focused real-OpenCode probe for `/saki-builder:proto F3`; verify that `--command proto -- F3` produces the no-target message and that `--command proto -- '/proto F3'` reaches the skill's target parser. | `backend/infra/spawner_test.go` helpers and local OpenCode CLI | MED | `opencode run --format json --auto --command proto -- F3`; `opencode run --format json --auto --command proto -- '/proto F3'` | Yes |
| 2 | Add regression tests for the proven skill-facing behavior, then change `buildSpawnEnv()` to normalize only OpenCode `proto` command messages to `/proto <args>` while preserving the existing command argv and all other engine/command contracts. | `backend/infra/spawner.go:222-255`, `backend/infra/spawner_test.go` | MED | `TestSpawn_OpencodeProtoTargetIsParsedBySkill`, `TestSpawn_OpencodeProtoTargetReachesCommand`, `go test ./infra ./domain -count=1` — Test-First | Yes |
| 3 | Run the complete backend and TypeScript checks, verify `saki run proto F3 --engine opencode` creates the preview, and inspect the final diff for scope. | `backend/infra/spawner_test.go`, `backend/domain/slashcmd_test.go`, `src/commands/run.test.ts` | LOW | `npm run typecheck`, `npm test`, `npm run backend:test`; manual real-engine smoke | Yes |

---

## User Role Coverage

| Role | Can Do | Cannot Do | Auth Guard | UI Entry Point |
|------|--------|-----------|------------|----------------|
| CLI operator | Render a proto through OpenCode with an ID/path target | Bypass loopback/backend validation or change profile semantics | Existing loopback/origin guard and engine/profile proof remain unchanged | `saki run proto <id> --engine opencode` |

## Plan Wiring

### Flow 1: OpenCode proto run

```text
CLI `saki run proto F3 --engine opencode`
  → `startRun()` (`src/commands/run.ts:155-233`)
  → `buildRunPrompt()` (`src/commands/run.ts:84-86`) produces `/saki-builder:proto F3`
  → `POST /api/run` with `{prompt, cwd, engine: "opencode"}` (`src/commands/run.ts:196-213`)
  → Go run adapter validates and creates `SpawnSpec` (`backend/adapter/http.go:483-551`)
  → `SplitSlashCommand()` (`backend/domain/slashcmd.go:48-53`) produces `proto` + `F3`
  → `buildRunScript()` (`backend/infra/spawner.go:39-78`)
  → `buildSpawnEnv()` (`backend/infra/spawner.go:222-255`) normalizes proto message to `/proto F3`
  → `buildRunScript()` (`backend/infra/spawner.go:39-78`) invokes `--command proto -- /proto F3`
  → installed `proto` skill parses target `F3` and writes the preview artifact
```

## Compatibility & Consumers

| Changed surface (exact) | Kind | Consumers found (`grep`) | Verdict | Mitigation / step |
|---|---|---|---|---|
| `buildRunScript()` OpenCode argv for leading slash commands | spawn contract | `backend/infra/spawner_test.go:87-169,221-261,267-...`; runtime caller `backend/infra/spawner.go` | updated in step 2; existing build/review argv must remain unchanged | step 1 locks proto-specific argv; step 3 runs full backend tests |
| `SplitSlashCommand()` output shape | function behavior | `backend/infra/spawner.go:...`; `backend/domain/slashcmd_test.go` | unaffected unless step 1 proves the split itself drops `F3` | step 2 changes this file only if evidence requires it |

**Forward compatibility:** additive test coverage plus a backward-compatible spawn-contract correction; no endpoint, CLI flag, profile, or persisted-state change.

## Migration Checklist

None — no database or schema changes.

## Branch Points (pre-declared)

- Step 1: If the recorded argv is already exactly `proto -- F3`, auto-resolve the diagnosis to the downstream skill/plugin argument parser and add the smallest execution-level regression test; do not change the spawner without evidence.
- Step 2: If `SplitSlashCommand()` returns `proto` and `F3` but OpenCode still reports no target, inspect the installed skill's command invocation contract and adjust only the adapter's message shape; preserve the `--command` security boundary.
- If the fix would require changing profile provisioning, engine selection, or loopback security, pause and scope that as a separate task.

## Unknowns

1. [MED] RESOLVED in step 1: the installed OpenCode version passes `F3` without the required command syntax; `/proto F3` reaches the target parser. Evidence: real `opencode run --format json --auto --command proto -- F3` emitted the no-target prompt, while the `/proto F3` variant emitted the target-parser prompt.

## No-Gos

- Will NOT require or alter `--profile` behavior.
- Will NOT change `saki proto <id>` gallery lookup/open behavior.
- Will NOT change engine provisioning, engine validation, permissions, or loopback security.
- Will NOT add a new dependency or abstraction.

## Implementation Completeness Checklist

**User Coverage**
- [x] CLI operator is listed with the exact command entry point.
- [x] Existing loopback and profile guards are explicitly retained.
- [x] Missing target is covered by the reproduction and regression test.

**Database & Migrations**
- [x] No schema changes; migration checklist is explicitly empty.

**API Layer**
- [x] Existing `POST /api/run` request shape is named and unchanged.
- [x] No endpoint or response schema changes.

**Service / Business Logic**
- [x] `buildRunScript()` and `SplitSlashCommand()` are named with exact paths.
- [x] Spawn failure/no-target behavior is covered.

**Frontend**
- [x] Backend-only task; no frontend changes or flow spec required.

**Compatibility & Consumers**
- [x] Existing OpenCode spawn consumers and tests are inventoried.
- [x] Forward compatibility is additive/backward-compatible.

**Plan Wiring**
- [x] CLI → HTTP → Go adapter → slash split → spawner → OpenCode argv → proto skill is fully traced.

## Evidence Ledger

### Blocking

| # | Step | Blocking predicate (unresolved) | Evidence |
|---|------|---------------------------------|----------|

### Advisory

| Step | Note | Evidence |
|------|------|----------|
| — | All anchors verified, all targets have creating steps, no unchecked items on state-changing steps, no unknowns above LOW | `src/commands/run.ts:84-86,196-213`; `backend/domain/slashcmd.go:48-53`; `backend/infra/spawner.go:39-78`; `backend/infra/spawner_test.go:221-261` |

## Readiness Attestation

All anchors verified, all targets have anchor parents and creating steps, all checklist items on state-changing steps satisfied, no unknowns above LOW.
