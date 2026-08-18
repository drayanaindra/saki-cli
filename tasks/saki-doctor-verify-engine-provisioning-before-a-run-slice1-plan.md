# EXECUTION PLAN: saki doctor — Slice 1 (shared engine→proof mapping + codex/opencode report)

**Date:** 2026-08-15
**Blocking items:** 0 (see Evidence Ledger)
**Risk Score:** MED (touches load-bearing spawn-refusal code; no DB, no auth, no money)
**Unknown Count:** 0 / 2 max
**Behavior Spec:** N/A — backend/CLI-only. `saki-cli` is headless by design (`docs/project-context.md`
§ Deliberate non-goals: "No UI, no browser"); there is no web page for a Gherkin flow to describe.
**Source PRD:** `tasks/prd-saki-doctor-verify-engine-provisioning-before-a-run.md` § Slice 1
**Prior slices:** N/A — slice 1
**Appetite:** ~7 agent tasks (5 acceptance criteria, one extra task each for the shared-mapping
extraction and the 7-file Handler-constructor ripple)
**Kill-if:** N/A for this slice (the PRD's kill criterion, §6, is anchored to criterion 2.2 in Slice 2)

## Problem Statement

When an operator or `/saki-builder:build` is about to dispatch work through codex or opencode, they
want to know — without spending a run — whether that engine's binary is on PATH and its profile
resolves the saki-builder commands, so a broken setup is caught before a build parks.

---

## Concrete Example Output

```
$ saki doctor
ENGINE    PROFILE  STATUS  REASON
codex     default  ok
opencode  default  failed  engine profile cannot resolve the saki-builder commands: opencode profile does not resolve @saketek/saki-builder: config /Users/x/.config/opencode/opencode.json unreadable: open /Users/x/.config/opencode/opencode.json: no such file or directory
$ echo $?
1

$ saki doctor --json
{"engines":[{"engine":"codex","profile":"default","status":"ok","reason":"","fix":""},{"engine":"opencode","profile":"default","status":"failed","reason":"engine profile cannot resolve the saki-builder commands: opencode profile does not resolve @saketek/saki-builder: config /Users/x/.config/opencode/opencode.json unreadable: open /Users/x/.config/opencode/opencode.json: no such file or directory","fix":""}]}
$ echo $?
1

$ saki doctor --profile /tmp/broken-codex-home
ENGINE    PROFILE                    STATUS  REASON
codex     /tmp/broken-codex-home     failed  engine profile cannot resolve the saki-builder commands: codex profile does not resolve @saketek/saki-builder: /tmp/broken-codex-home/codex/config.toml registers no enabled saki-builder plugin and /tmp/broken-codex-home/codex/skills/build/SKILL.md is absent — run `codex plugin add saki-builder@saketek` (or bash scripts/install-codex-skills.sh to check)
opencode  /tmp/broken-codex-home     failed  ...
$ echo $?
1
```

`fix` is present but empty (`""`) for every engine in this slice — Slice 2 (criterion 2.3) is what
populates codex's complete two-line remediation; opencode's is deferred to F5. The field's PRESENCE
(not its content) is what criterion 1.3 requires.

---

## Design decision (not in the PRD §16 — resolved here, cited)

**How the "one shared engine→proof mapping" (§7 Solution Shape) is actually built.** `preflight`
(`backend/infra/spawner.go:149-182`) does three things per engine, in order: (1) a binary-on-PATH
check via `exec.LookPath` (lines 154, 172), (2) an opencode-ONLY prompt-shape refusal (lines 165-167,
between the binary check and the proof — this is NOT a provisioning check, doctor has no prompt to
check), (3) the profile proof (`OpencodePluginProof`/`CodexSkillsProof`, lines 170, 178).

Doctor needs (1) and (3) but never (2) (rule 4: *"preflight's prompt refusal is outside doctor's
scope"*). So the extraction splits `preflight`'s body into two named, reusable functions —
`EngineBinaryCheck` and `EngineProfileProof` — and `preflight` is rewritten to call them with the
prompt check still sandwiched between, in the same order. This is verified as a **zero-behavior-change
refactor**: `preflight` (line 149) has exactly ONE caller in the whole backend
(`ShSpawner.Spawn`, `spawner.go:118` — verified: `grep -rn "preflight(" backend --include='*.go'`
returns only the definition and this one call site), so its return values for every input are the
only contract that matters, and those are unchanged.

`CodexSkillsProof` / `OpencodePluginProof` are **not modified at all** — same signature, same body,
same single caller (verified: each greps to exactly one call site, inside `preflight`). `doctor`
reaches them through the new `EngineProfileProof` wrapper, so both callers (spawn-time `preflight`,
pre-dispatch `doctor`) run the identical function (rule 4, literally, not just "the same behavior").

**Where `EngineReport` and the `DoctorService` port live.** Mirroring the existing precedent
(`domain.RoadmapItem` is the read-model `RoadmapService` returns up through `adapter` to JSON,
`backend/domain/roadmap.go`), `EngineReport` is a plain data struct in `domain` (zero outbound deps,
just string fields). The port `EngineProofs` (implemented by `infra`, consumed by `usecase`) mirrors
`Spawner`/`Journal` in `backend/usecase/run_ports.go` — an interface `usecase` defines, `infra`
implements.

---

## Steps

| # | Action | Files (exact paths) | Risk | Test | Committable? |
|---|--------|---------------------|------|------|-------------|
| 1 | Extract `EngineBinaryCheck(engine domain.RunEngine) error` and `EngineProfileProof(engine domain.RunEngine, configDir *string) error` from `preflight`'s body (`backend/infra/spawner.go:149-182`); rewrite `preflight` to call them in the same order (binary → opencode-only prompt check, unchanged inline → profile proof). Both funcs MUST preserve `preflight`'s `default: return nil` no-op for `domain.EngineClaude` — claude does not go through either check | `backend/infra/spawner.go` (edit `preflight`, add the two new funcs directly above/below it) | MED (load-bearing spawn-refusal code — a sentinel-identity change on EITHER engine silently flips `rearmOrPark`'s park-vs-retry decision, `backend/usecase/buildengine.go:473`, which special-cases only `errors.Is(spawnErr, ErrBinaryNotFound)`) | `TestPreflight_OpencodePeelOff` (NEW, criterion 1.4) — three fixtures in sequence: binary off PATH → `ErrBinaryNotFound`; PATH restored + prompt `/build, then do X` (unparseable) → `ErrUnresolvableCommand`; prompt fixed (`SplitSlashCommand`-parseable) + a profile with no `opencode.json` → `ErrEngineNotProvisioned`. Plus re-run FOUR existing regression tests unchanged — the sentinel-identity proof for BOTH engines the extraction touches, not just opencode: `TestSpawn_OpencodeBinaryNotFound` + `TestSpawn_ClaudeIgnoresOpencodeBinaryCheck` (`backend/infra/spawner_test.go:483,495`) AND `TestSpawn_CodexBinaryMissingIsBinaryNotFound` + `TestSpawn_CodexSkillsMissingFailsLoudly` (`backend/infra/codex_test.go:305,289`) | Yes |
| 2 | Add `EngineReport` struct: `Engine, Profile, Status, Reason, Fix string` (all `json:"..."` camelCase, no `omitempty` — criterion 1.3 requires every field present even when empty) | `backend/domain/doctor.go` (NEW) | LOW | compiles + used by step 4's test | No — completed by step 4 |
| 3 | Add `EngineProofs` interface: `BinaryCheck(engine domain.RunEngine) error` and `ProfileProof(engine domain.RunEngine, configDir *string) error` | `backend/usecase/doctor_ports.go` (NEW) | LOW | compiles + used by step 4's test | No — completed by step 4 |
| 4 | Add `DoctorEngines = []domain.RunEngine{domain.EngineCodex, domain.EngineOpencode}` (the FIXED, slice-1-scoped reported set — NOT `domain.RunEngine`'s full enum, which also has claude) and `DoctorService{proofs EngineProofs}` with `NewDoctorService(proofs EngineProofs) DoctorService` and `func (s DoctorService) Check(configDir *string) []domain.EngineReport` — iterates `DoctorEngines` in order; per engine, calls `BinaryCheck` FIRST and **short-circuits on error** (mirrors `preflight`'s own early-return at `spawner.go:154-156` — `ProfileProof` is NOT called when `BinaryCheck` already failed, so the reported `reason` is never silently overwritten by a second call); only when `BinaryCheck` is nil does it call `ProfileProof`; `status:"failed"` + `reason:err.Error()` on either failing, `status:"ok"` + empty reason/fix when both are nil; `profile` field = `*configDir` or `"default"` | `backend/usecase/doctor.go` (NEW) | MED (the rules-1/2/5 logic lives here) | `TestDoctorService_Check` (NEW, criteria 1.1/1.2/1.3 backend half + rules 1,2,5) — fake `EngineProofs` recording calls: (a) both nil → 2 reports, both `status:"ok"`; (b) codex `ProfileProof` returns an error, `BinaryCheck` nil → codex report `status:"failed"`, `reason` = that error's `.Error()`, opencode still `"ok"`; (c) assert the returned slice is **exactly** `[codex, opencode]` in that order with all 5 JSON fields set (criterion 1.3); (d) assert the fake recorded ONLY `BinaryCheck`/`ProfileProof` calls — nothing else (proves rule 5, "never spawns anything", and rule 1, "never installs/writes", by construction: `Check` has no other capability to exercise); (e) codex `BinaryCheck` returns an error → codex report `status:"failed"`, `reason` = the `BinaryCheck` error text, AND assert the fake's `ProfileProof` was **never called** for codex (proves the short-circuit — this is the primary-JTBD failure mode, "is the binary on PATH", and was otherwise untested by cases (a)-(d) alone) | Yes |
| 5 | Add `infra.EngineProofChecker` (zero-value struct) implementing `usecase.EngineProofs` by delegating to step 1's `EngineBinaryCheck`/`EngineProfileProof` | `backend/infra/doctor.go` (NEW) | LOW | `TestEngineProofChecker_DelegatesToSharedFuncs` (NEW) — asserts `EngineProofChecker{}.BinaryCheck(codex)`/`.ProfileProof(...)` return byte-identical errors to calling `EngineBinaryCheck`/`EngineProfileProof` directly over the same fixtures (the rule-4 proof: same underlying function, not just same shape). **Plus** `TestDoctorService_Check_LeavesFilesystemUntouched` (NEW, criterion 1.5's REAL proof — the step-4 fake proves `DoctorService`'s own code never writes, but says nothing about the real `CodexSkillsProof`/`OpencodePluginProof` chain it eventually calls): build `usecase.NewDoctorService(infra.EngineProofChecker{})` — the real infra, not a fake — over a `t.TempDir()` fixture holding a broken `codex/config.toml`; snapshot the fixture dir's file bytes before, run `.Check(&dir)`, re-read and `bytes.Equal`-assert unchanged; also assert `os.ReadDir(filepath.Join(t.TempDir(),"go"))` (a fresh `$SAKI_RUNS_DIR`-style dir, per `journal.go:47-59`) gains no new entry | Yes |
| 6 | Add `Handler.doctor usecase.DoctorService` field; add `doctor usecase.DoctorService` as the 17th `NewHandler` parameter (after `planTrack`); add `doctorHandler(w http.ResponseWriter, r *http.Request)`: read `r.URL.Query().Get("profile")` → `*string` (nil if empty) → `h.doctor.Check(configDir)` → `writeJSON(w, http.StatusOK, map[string]any{"engines": reports})`; register `mux.Handle("GET /api/doctor", OriginGuard(http.HandlerFunc(h.doctorHandler)))` in the OriginGuard-wrapped read block (`backend/adapter/http.go:86-92`, alongside `/api/roadmap` etc. — NOT the unguarded run-vertical block at :69-74) | `backend/adapter/http.go` (edit `Handler` struct ~line 45-52, `NewHandler` line 55-56, add `doctorHandler` near `roadmapHandler` line 223, add `mux.Handle` at line 92) | MED (route surface; must sit with the guarded reads per §13) | `TestDoctorHandler_ReturnsEngines` (NEW) — `GET /api/doctor` → 200, body `{"engines":[...]}` with 2 entries; `GET /api/doctor?profile=/tmp/x` → the fake `DoctorService`'s recorded `configDir` argument is `"/tmp/x"`. **Plus `TestDoctorHandler_RealWiring` (NEW, step 7's gate)** — an `httptest.NewServer`-based test, matching the existing pattern at `backend/adapter/http_test.go:353,384`, that builds `Handler` via `NewHandler(..., usecase.NewDoctorService(infra.EngineProofChecker{}))` — the EXACT production construction, not a fake — against a real broken-profile fixture, asserting the JSON over a genuine HTTP round trip (Go → handler → service → infra → filesystem). This is the only thing in the whole slice that proves `main.go` actually wires the real service and not an accidentally-pasted `usecase.DoctorService{}` zero-value (which would compile fine everywhere else and panic on the first real `saki doctor` call — nothing else in this plan's test suite goes through the production constructor) | No — grouped with step 7 (adding a 17th REQUIRED `NewHandler` param doesn't compile until every call site is updated in the same commit) |
| 7 | Update all 10 `NewHandler(` call sites for the new 17th arg — production (`main.go`) passes the real service; the 9 test call sites across 6 unrelated test files pass the zero-value `usecase.DoctorService{}` (legal: no field is set from outside the package, and none of these tests exercise `/api/doctor`) | `backend/cmd/server/main.go:115` (`doctorSvc := usecase.NewDoctorService(infra.EngineProofChecker{})`, then pass it), `backend/adapter/plantrack_http_test.go:34`, `backend/adapter/prd_http_test.go:28`, `backend/adapter/blockers_http_test.go:23`, `backend/adapter/http_test.go:318,327,336,412` (4 call sites, 1 file), `backend/adapter/resolve_http_test.go:24`, `backend/adapter/lock_http_test.go:25` | MED (breaking-signature ripple across 7 files — 1 production + 6 test — all 10 call sites verified by direct grep — see Compatibility & Consumers) | `go build ./...` + `go test ./...` (existing suite, all 7 files) must stay green — **plus the NEW `TestDoctorHandler_RealWiring`** (backend/adapter, real production wiring — see step 6's Test column addition) — no other new test here, this step's job is compile-parity | Yes — this step's completion (all 10 call sites updated) is what makes the step-6+7 pair compile; commit both together |
| 8 | Add `EngineReport { engine: string; profile: string; status: 'ok' \| 'failed' \| 'unknown'; reason: string; fix: string }` and `DoctorResult { engines: EngineReport[] }` interfaces | `src/types.ts` | LOW | used by step 9's test | No — completed by step 9 |
| 9 | Add `cmdDoctor(ctx: Ctx, _positionals: string[], flags: Record<string, string \| boolean>): Promise<ExitCode>` — reads `flags.profile` (string \| undefined), calls `ctx.client.get<DoctorResult>('/api/doctor', profile ? {profile} : undefined)`, defends against a malformed body with `const engines = res.engines ?? []` (never bare `res.engines`, which would throw a raw `TypeError` on `{}`/`{"engines":null}` instead of a clean diagnosis), renders via `renderTable` (columns: ENGINE, PROFILE, STATUS, REASON max 60) when `!ctx.json`, computes `allOk = engines.length > 0 && engines.every(e => e.status === 'ok')`; on `!allOk`, `ctx.writeErr` each non-ok engine's `fix` line (skip empty `fix`) and return `EXIT.ERROR` (criterion 1.2: exactly `1`); else `EXIT.OK`. Note in the doc comment: a studio-unreachable / gated-studio failure never reaches this logic — `ctx.client.get` throws first (`EXIT.UNREACHABLE`=3 / `EXIT.AUTH_REQUIRED`=6 via the existing `client.ts` contract, unchanged by this slice) | `src/commands/doctor.ts` (NEW) | MED (exit-code contract — must be exactly `1`, never any other non-zero code, per criterion 1.2) | `src/commands/doctor.test.ts` (NEW, criteria 1.1/1.2/1.3 CLI half) — using the `routedCtx` stub pattern from `src/commands/run.test.ts:82-100` (its `gets: string[]` array records every GET URL; the sibling `ctxFor` at `run.test.ts:7` only records POST bodies and has no URL capture — do not use it here): (a) both engines `ok` in the stubbed response → `cmdDoctor` returns `EXIT.OK` (0); (b) codex `failed` → returns `EXIT.ERROR` (1) — exactly, asserted with `toBe`; (c) `--json` mode: captured `out` line parses as JSON with `engines.length === 2` and every entry has all 5 keys; (d) `flags.profile = '/tmp/x'` → `routedCtx`'s `gets` array contains a URL whose query string is `profile=%2Ftmp%2Fx`; (e) a malformed `{}` response body → `cmdDoctor` returns `EXIT.ERROR` (1) cleanly, no uncaught `TypeError` | Yes |
| 10 | Add `/^\/api\/doctor($|\?)/,` to `GO_ROUTES` (so a configured-Express deployment still routes `/api/doctor` to Go, which owns the only implementation) | `src/routes.ts` | LOW | `src/routes.test.ts` — add one assertion `expect(backendFor('/api/doctor', true)).toBe('go')` alongside the existing "routes the read surface to Go" case (line 14) | Yes |
| 11 | Import `cmdDoctor` from `./commands/doctor.js`; add a `COMMANDS` entry: `{ path: ['doctor'], usage: 'saki doctor [--profile <dir>]', summary: 'can codex/opencode actually run a saki-builder command, before you dispatch a run', flags: { ...COMMON, profile: 'string' }, run: (ctx, positionals, flags) => cmdDoctor(ctx, positionals, flags) }` | `src/index.ts` (imports block near line 20-30, `COMMANDS` array near line 127) | LOW | existing `matchCommand`/dispatch tests in `src/index.test.ts` continue to pass unchanged (no COMMANDS-iterating test exists that would need a `doctor` addition — verified: the `helpText` test at line 101 checks a curated subset, not every command) | Yes |
| 12 | Add a `saki doctor` line to the `Commands` code block, plus one short paragraph under it noting `--profile`, that `0` = all reported engines ready / `1` = at least one not ready, and that a studio-unreachable/gated failure surfaces as the EXISTING `3`/`6` codes (unchanged by this slice — not a new doctor-specific code). Also remove the now-false `README.md:102` bullet "No `saki doctor` to verify engine provisioning before a run" from its `## Not built yet` list, so the two published docs don't contradict each other on day one | `docs/cli-reference.md` (the `## Commands` code block, ~line 115-141), `README.md` (the `## Not built yet` list, ~line 102) | LOW | `grep -q 'saki doctor' docs/cli-reference.md` (CLAUDE.md checklist: "a route absent from docs/cli-reference.md is unshipped") + `! grep -q 'No .saki doctor.' README.md` | Yes |

> Rule: Each "Action" cell names the exact function/method being added or changed. No vague steps.
> **XP Rule:** Steps 1, 4, 5, 6, 9 carry business logic and each names its Test-First target.
> **XP Rule:** Steps 2/3 are grouped into step 4's commit (they're inert without it); step 6/7 are one
> atomic commit (same signature change, split across files only because Go requires it); step 8 is
> grouped into step 9's commit for the same reason.

---

## User Role Coverage

`saki-cli` has no web UI and no user accounts (`docs/project-context.md` § Deliberate non-goals:
"No UI, no browser"; the backend has no auth, only a loopback-only network guard). The PRD's two JTBD
consumers (§3/§4) are both unauthenticated callers of the same read-only CLI command, distinguished
only by *how* they read the exit code:

| Role | Can Do | Cannot Do | Auth Guard | Entry Point |
|------|--------|-----------|------------|-------------|
| Operator (human at a terminal) | Run `saki doctor` / `saki doctor --profile <dir>` / `saki doctor --json`; read the table or JSON; read a non-zero exit code | Trigger any write, install, or spawn (rule 1) | None (loopback-only network guard, `originguard.go`) applies equally to every caller | `saki doctor` CLI invocation |
| Agent / CI caller (e.g. `/saki-builder:build`, a CI script) | Same as Operator, but consumes `--json` + the exit code programmatically to gate a subsequent dispatch | Same | Same | `saki doctor --json` (or a direct `GET /api/doctor`, still loopback-only) |

---

## Plan Wiring

### Flow 1: Operator/CI runs `saki doctor` (the new read path)
```
cmdDoctor (src/commands/doctor.ts, NEW)
  → ctx.client.get<DoctorResult>('/api/doctor', profile ? {profile} : undefined) (src/client.ts:195 get())
  → GET /api/doctor (src/routes.ts GO_ROUTES entry, NEW → resolves to Go)
  → Handler.doctorHandler (backend/adapter/http.go, NEW) — mounted OriginGuard(...) at http.go:86-92 block
  → DoctorService.Check(configDir) (backend/usecase/doctor.go, NEW)
      → for engine in DoctorEngines = [codex, opencode]:
          EngineProofs.BinaryCheck(engine) → infra.EngineProofChecker.BinaryCheck (backend/infra/doctor.go, NEW)
            → infra.EngineBinaryCheck(engine) (backend/infra/spawner.go, extracted step 1)
          EngineProofs.ProfileProof(engine, configDir) → infra.EngineProofChecker.ProfileProof (backend/infra/doctor.go, NEW)
            → infra.EngineProfileProof(engine, configDir) (backend/infra/spawner.go, extracted step 1)
              → codex: CodexSkillsProof(configDir) (backend/infra/codex.go:60, UNCHANGED)
              → opencode: OpencodePluginProof(configDir) (backend/infra/opencode.go:36, UNCHANGED)
  → []domain.EngineReport (backend/domain/doctor.go, NEW)
  → JSON {"engines":[...]} (200) → cmdDoctor renders table (or --json) → exit 0 (all ok) or 1 (any not ok)
```

### Flow 2: Spawn-time refusal (regression-only — unchanged observable behavior, now built on the shared functions)
```
RunService.Spawn (backend/usecase/spawn.go:58, UNCHANGED)
  → ShSpawner.Spawn (backend/infra/spawner.go:117, UNCHANGED)
  → preflight(spec) (backend/infra/spawner.go:149, body refactored — same order, same returns)
      → EngineBinaryCheck(engine) (NEW extracted func, step 1)
      → [opencode only] SplitSlashCommand/LooksLikeSlashCommand prompt refusal (UNCHANGED, inline)
      → EngineProfileProof(engine, spec.ConfigDir) (NEW extracted func, step 1)
  → error (or nil) → writeSpawnResult maps ErrBinaryNotFound/ErrEngineNotProvisioned to HTTP 500 with
    the diagnosis text (backend/adapter/http.go:503, UNCHANGED)
```

---

## Compatibility & Consumers

| Changed surface (exact) | Kind | Consumers found (`grep`) | Verdict | Mitigation / step |
|---|---|---|---|---|
| `preflight(spec usecase.SpawnSpec) error` (`backend/infra/spawner.go:149`) | function body (signature unchanged) | 1 (`backend/infra/spawner.go:118`, inside `ShSpawner.Spawn`) | unaffected — return values for every input are byte-identical; verified by step 1's `TestPreflight_OpencodePeelOff` + the two pre-existing regression tests | step 1 |
| `CodexSkillsProof` / `OpencodePluginProof` (`backend/infra/codex.go:60`, `backend/infra/opencode.go:36`) | function (not modified at all) | 1 each (`preflight`, before AND after the extraction) | unaffected — same signature, same body, same single caller (now reached via `EngineProfileProof`) | step 1 |
| `adapter.NewHandler(...)` (`backend/adapter/http.go:55`) | function signature — 16 args → 17 (adds `doctor usecase.DoctorService`) | **10 call sites total** (verified: `grep -rn "NewHandler(" backend --include='*.go'` → 11 hits minus the definition line itself = 10 real calls): 1 production (`backend/cmd/server/main.go:115`) + 9 test call sites across 6 distinct files (`backend/adapter/{plantrack_http_test.go:34, prd_http_test.go:28, blockers_http_test.go:23, http_test.go:318\|327\|336\|412 [4 sites, 1 file], resolve_http_test.go:24, lock_http_test.go:25}`) | breaks — every call site needs the new arg | step 7: production gets the real `usecase.NewDoctorService(infra.EngineProofChecker{})`; the 6 unrelated test files get the zero-value `usecase.DoctorService{}` (legal — no unexported field is set from outside the package, and none of these tests touch `/api/doctor`) |
| `Handler` struct (`backend/adapter/http.go`) | struct shape — adds `doctor usecase.DoctorService` field | same 10 sites (they construct via `NewHandler`, never a struct literal) | updated in step 6 | step 6 |
| `adapter.NewHandler(...)` param count | pre-existing — already 16 params before this slice, → 17 after | N/A (not a NEW violation, an EXISTING one this slice's diff line touches) | CLAUDE.md's params≤7 rule is already broken by this constructor today; the SonarQube "Clean as You Code" gate grades the changed line, so widening it MAY trip S107 on push | Branch Point (below): accepted pre-existing debt for this slice, not restructured into a `HandlerDeps` struct — that refactor is out of this slice's scope. If `sonar-gate.sh` blocks the push, the documented manual-push bypass (CLAUDE.md § Pre-merge Gate) applies with this row as the reason |

**Forward compatibility:** additive-only for every external contract (`GET /api/doctor` is a brand-new
route; `EngineReport`'s JSON shape only grows in later slices per criterion 1.3: *"Later slices may
add fields, never remove them"*). The one non-additive change (`NewHandler`'s signature) is internal
Go — no wire format changes, and its blast radius is fully enumerated above, not a guess.

---

## Migration Checklist

N/A — no database, no schema change in this repo (`saki-cli` has none; state lives in NDJSON journal
files and roadmap/PRD markdown, per `docs/project-context.md`).

---

## Branch Points (pre-declared)

- Step 1: the extraction could theoretically be done as a single `EngineProvisioningCheck(engine,
  configDir)` combining binary+profile in one call → **auto-decided: split into two funcs**
  (reversible — `AUTO-RESOLVED: combine binary+profile into one func, or keep them separate? →
  keep separate (EngineBinaryCheck / EngineProfileProof) — the opencode prompt-refusal check in
  preflight sits BETWEEN binary and profile checks, so a single combined func would either have to
  take the prompt-refusal as a parameter (leaking spawn-only concerns into a doctor-shared function)
  or duplicate the binary check outside it. Two funcs let preflight sandwich its prompt check exactly
  where it is today, unchanged.`)
- Step 6: mount `/api/doctor` in the OriginGuard-wrapped block vs. the unguarded run-vertical block →
  **auto-decided: OriginGuard-wrapped** (reversible, but §13 states it explicitly: *"doctor's route
  must sit with the OriginGuard-wrapped reads... it discloses local profile paths"* — not a judgment
  call, a stated constraint. `AUTO-RESOLVED: which route block? → the OriginGuard-wrapped read block
  (http.go:86-92) — §13 states this explicitly, and doctor discloses local filesystem paths in its
  reason/profile fields, matching every other content-read route already there.`)
- Step 6/7: `NewHandler`'s param count is already 16 (well over CLAUDE.md's non-negotiable ≤7) before
  this slice, and this slice pushes it to 17 → **auto-decided: accept as pre-existing debt, do NOT
  restructure into a `HandlerDeps` struct this slice** (reversible — `AUTO-RESOLVED: refactor
  NewHandler's params into a struct now, or accept the pre-existing debt and add one more? → accept
  the debt — a params-struct refactor touching all 10 existing call sites is a correctness-neutral
  restructure far outside this slice's scope (PRD §7 rejected exactly this kind of over-broad change
  for the mapping extraction; the same reasoning applies here), and the constructor already violates
  the rule today, so this slice is not the first violation. If the SonarQube pre-merge gate blocks the
  push on the changed signature line, the documented manual bypass (CLAUDE.md § Pre-merge Gate) applies
  with this Branch Point as the citation.`)
- No irreversible or `[human]`-tagged forks in this slice.

---

## Unknowns (must be <= 2)

None. Every anchor was verified by direct read or grep before this plan was written (see Evidence
Ledger).

---

## No-Gos

- Will NOT touch `CodexSkillsProof` or `OpencodePluginProof`'s signature or body — this slice reuses
  them exactly as-is (PRD §7, §16).
- Will NOT report claude — `DoctorEngines` is `[codex, opencode]` only; claude coverage is F4,
  deferred (PRD §11 Non-Goal, PRD §12).
- Will NOT change `preflight`'s observable behavior, refusal order, or the sentinels it returns
  (criterion 1.4; Compatibility & Consumers row 1).
- Will NOT populate a meaningful `fix` string for any engine yet — every `fix` is `""` in this slice;
  Slice 2 (criterion 2.3) and F5 own the remediation text (PRD §11 Non-Goal).
- Will NOT install, write, or repair any engine profile, config file, or plugin registry (🔒 rule 1).
- Will NOT spawn any engine process to reach a verdict (rule 5).
- Will NOT add a `resolved version` field to `EngineReport` (PRD §11 Non-Goal — unpopulatable for
  codex/opencode; `pluginRegistered`/`OpencodePluginProof` read no version).

---

## Implementation Completeness Checklist

**User Coverage**
- [x] Both roles this feature has (Operator, Agent/CI) are in the Role Coverage matrix
- [x] Each role's full call chain is in Plan Wiring Flow 1
- [x] Auth guard stated for each role (loopback-only, applies to both identically)
- [x] Edge cases documented: both ok (exit 0), one failed (exit 1, criterion 1.2), `--profile` pointed
  at a broken home (Concrete Example Output)

**Database & Migrations**
- [x] N/A — no schema change (Migration Checklist section states this)

**API Layer**
- [x] Request shape: `GET /api/doctor?profile=<dir>` (optional query param) — step 6
- [x] Response shape: `EngineReport` (step 2) named + file path given
- [x] HTTP method, path, router file written out (step 6)
- [x] Dependencies listed: `OriginGuard` wrapper (step 6, cites §13)

**Service / Business Logic**
- [x] Every service function named with file path (steps 1, 3, 4, 5)
- [x] Side effects listed: NONE — this is the point (rules 1, 5); step 4's test asserts it
- [x] Error paths documented: `BinaryCheck` fails → `status:"failed"`; `ProfileProof` fails →
  `status:"failed"`; both nil → `"ok"` (step 4)

**Frontend**
- [x] N/A — no web UI (`saki-cli` is headless); the CLI IS the "frontend" here, covered by steps
  8-11 with loading/error states N/A (a single synchronous request, no loading state to render;
  error states are the `status:"failed"` rows themselves, not a separate UI state)

**Compatibility & Consumers**
- [x] Compatibility & Consumers filled — every changed existing surface has a consumers cell (incl.
  exact grep results) + a verdict; every `breaks` verdict has a mitigation step (step 7)
- [x] Prior slices: N/A — slice 1

**Plan Wiring**
- [x] Both flows have a written end-to-end call chain
- [x] No step uses a vague verb without an exact file+function
- [x] No "add endpoint" without naming method, path, and handler file (step 6)

---

## Evidence Ledger

### Blocking (must be empty to present)

*(none)*

### Advisory (visible, never gates)

| Step | Note | Evidence |
|------|------|----------|
| 11 | `src/index.ts`'s `helpText()` test (line 101) is a curated subset and was not extended to assert `'doctor'` — cosmetic, no criterion requires it | `src/index.test.ts:101-107` |
| — | All anchors verified, all targets have real anchor parents and creating steps, no unchecked items on state-changing steps, no unknowns above LOW | self-audit — every `path:line` cite in this plan was read or grepped directly during research (see chat transcript): `spawner.go` full read; `codex.go`, `opencode.go` full read; `spawn.go` full read; `buildengine.go:472-473`, `domain/run.go:1-30`, `http.go:55,65-95,495-510` read; `run_ports.go`, `usecase/ports.go` read; `routes.ts`, `client.ts`, `exit.ts`, `output.ts`, `ctx.ts`, `status.ts` full read; `run.ts:88,192` grepped; `roadmap.ts:43-62` read; `roadmap.go` (domain) struct grepped; `profiles.go` head read; `main.go:1-30,85-120` read; `index.ts:110-145` read; `docs/cli-reference.md:9-235` headings + `115-141` body read; consumer inventory for `preflight`/`CodexSkillsProof`/`OpencodePluginProof`/`NewHandler` run via `grep -rn` across `backend --include='*.go'` (9 real call sites found, not assumed); `spawner_test.go`'s `writeFakeOpencode`/`pluginProvenProfile`/`TestSpawn_OpencodeBinaryNotFound`/`TestSpawn_ClaudeIgnoresOpencodeBinaryCheck` read for the PATH-faking + regression-test pattern; `routes.test.ts`, `run.test.ts`, `roadmap.test.ts`, `index.test.ts:90-115` read for CLI test conventions; confirmed no `backend/domain/doctor.go` / `backend/usecase/doctor.go` / `backend/adapter/doctor_http.go` collision |

**Blocking: 0 → READY.**

---

## Success Criteria

Verbatim from the PRD, plus the exact command/behavior that verifies each (for `/saki-builder:qa`).
**Test titles are pinned, not merely described** — step 9's `it(...)` blocks in `src/commands/doctor.test.ts`
MUST contain these literal substrings, so the `-t` filters below actually match (a paraphrased title
would make `/saki-builder:qa` false-negative a real pass):

- [x] 1.1 — `saki doctor` with both engines provisioned → both rows `ok`, exit `0`.
      `go test ./backend/usecase/ -run TestDoctorService_Check` (backend half, case (a)) +
      `npx vitest run src/commands/doctor.test.ts -t "both engines ok"` — pin the title
      `it('both engines ok — exit 0', ...)`
- [x] 1.2 — `saki doctor --profile <broken codex home>` → codex row `failed`, exit **exactly** `1`.
      `npx vitest run src/commands/doctor.test.ts -t "codex failed"` — pin the title
      `it('codex failed — exit 1', ...)`, asserts `toBe(EXIT.ERROR)`, not merely `toBeGreaterThan(0)`
- [x] 1.3 — `saki doctor --json` → JSON with exactly `{codex, opencode}`, each carrying `engine`,
      `profile`, `status`, `reason`, `fix`. `go test ./backend/usecase/ -run TestDoctorService_Check`
      (shape, case (c)) + `npx vitest run src/commands/doctor.test.ts -t "json"` — pin the title
      `it('--json shape', ...)`
- [x] 1.4 — the `preflight` peel-off (`ErrBinaryNotFound` → `ErrUnresolvableCommand` →
      `ErrEngineNotProvisioned`) is unchanged after the extraction, for BOTH engines the refactor
      touches (not opencode alone — codex goes through the identical `EngineBinaryCheck`/
      `EngineProfileProof` extraction and has its own sentinel-identity regression tests).
      `go test ./backend/infra/ -run TestPreflight_OpencodePeelOff` +
      `go test ./backend/infra/ -run 'TestSpawn_OpencodeBinaryNotFound|TestSpawn_ClaudeIgnoresOpencodeBinaryCheck|TestSpawn_CodexBinaryMissingIsBinaryNotFound|TestSpawn_CodexSkillsMissingFailsLoudly'`
- [x] 1.5 — a failing profile's files are byte-identical after `saki doctor` runs; no run journal
      file is created; no engine process starts.
      `go test ./backend/usecase/ -run TestDoctorService_Check` (structural half — the fake
      `EngineProofs` was the ONLY thing called) **AND**
      `go test ./backend/usecase/ -run TestDoctorService_Check_LeavesFilesystemUntouched` (the REAL
      proof — runs the actual `infra.EngineProofChecker` chain, not a fake, and asserts the fixture
      dir's bytes are unchanged + no journal-dir entry appears)
- [x] `go build ./...` and `go test ./...` (whole backend suite, incl. all `NewHandler` call sites —
      **adjusted in impl:** the count grew from the 10 pre-existing sites this plan enumerated to 11
      after step 6's own `TestDoctorHandler_RealWiring` (`backend/adapter/doctor_http_test.go:74`)
      added a genuinely new one; re-verified by `grep -rn "NewHandler(" backend --include='*.go'` →
      13 hits = 1 definition + 1 doc-comment mention + 11 real calls)
      pass — proves the Compatibility & Consumers mitigation actually compiles and stays green
- [x] `npx tsc --noEmit` and `npx vitest run` (whole CLI suite) pass
- [x] `grep -q 'saki doctor' docs/cli-reference.md` — the route is documented (CLAUDE.md checklist)

---

## Annotation Space

> Human: add notes, corrections, constraints here.
> Claude will revise plan and re-check the Blocking Set before proceeding.

---
Status: [ ] Draft  [ ] Annotated  [ ] Approved  [x] In Progress  [ ] Complete
Readiness Gate: [x] Evidence Ledger present and every blocking item cited  [x] Blocking Set empty  [x] Unknowns <= 2
