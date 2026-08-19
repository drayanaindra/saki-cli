# EXECUTION PLAN: F6 slice 2 — opencode adapter and shared verification

**Date:** 2026-08-19
**Blocking items:** 0 (see Evidence Ledger)
**Risk Score:** MED (new child-process execution + profile mutation for a second engine; loopback-guarded, no DB, no auth surface — same profile as slice 1)
**Unknown Count:** 0 / 2 max
**Behavior Spec:** N/A — backend + CLI only, no UI (source PRD is stamped `ui:none`)
**Source PRD:** `tasks/prd-saki-init-env-provision-engine-profile.md` § slice 2 (Locked — `<!-- prd-locked: @codex · 2026-08-16 · ui:none -->`)
**Prior slices:** `tasks/saki-init-env-provision-engine-profile-slice1-plan.md` (read — its shipped shape wins over the PRD: the proof-decides rule, the service-level engine gate, the required `initEnv` param, `provisionArgv`, `profileFingerprint` already carrying the opencode case, the e2e harness)
**Appetite:** ~5 agent tasks (4 acceptance criteria → one iteration per INVEST; within the PRD's `medium` band)
**Kill-if:** outcome 5.1 cannot be met for opencode — i.e. an opencode profile provisioned by this command still fails `saki doctor --json --profile <dir>`, meaning setup and proof disagree about which profile runs.

## Problem Statement

When an operator selects the opencode engine for a repo, I want one command to provision its profile and
prove readiness immediately, so I can dispatch a run without hand-editing `opencode.json` or memorising
`opencode plugin @saketek/saki-builder --global`.

---

## Concrete Example Output

```console
$ saki init-env --engine opencode --profile /tmp/p2 --json
{"engine":"opencode","profile":"/tmp/p2","changed":true,"status":"ok","reason":"","fix":""}
$ echo $?
0

$ saki init-env --engine opencode --profile /tmp/p2 --json     # repeat — idempotent
{"engine":"opencode","profile":"/tmp/p2","changed":false,"status":"ok","reason":"","fix":""}
$ echo $?
0

$ saki doctor --json --profile /tmp/p2                        # setup and doctor agree
{"engines":[{"engine":"codex","profile":"/tmp/p2","status":"failed",…},{"engine":"opencode","profile":"/tmp/p2","status":"ok",…}]}

$ PATH=/empty saki init-env --engine opencode --profile /tmp/p3 --json
{"engine":"opencode","profile":"/tmp/p3","changed":false,"status":"failed",
 "reason":"engine binary not found (opencode)",
 "fix":"opencode plugin @saketek/saki-builder --global"}
$ echo $?
1
$ ls /tmp/p3
ls: /tmp/p3: No such file or directory      # 2.x — nothing was created

$ saki init-env --engine claude --profile /tmp/p4 --json     # claude stays unsupported (slice 3)
{"engine":"claude","profile":"/tmp/p4","changed":false,"status":"failed",
 "reason":"engine provisioning is not verified for this engine (claude requires F4's installed + enabled plugin proof)", …}
$ echo $?
1
```

---

## Steps

| # | Action | Files (exact paths) | Risk | Test | Committable? |
|---|--------|---------------------|------|------|-------------|
| 1 | Add the single opencode engine mapping and its derived fix: `OpencodeProvisionArgv = [][]string{{"opencode", "plugin", "@saketek/saki-builder", "--global"}}` and `OpencodeInstallFix = renderProvisionArgv(OpencodeProvisionArgv)`, alongside `CodexProvisionArgv`/`CodexInstallFix`. Update `engineInstallFix` to return `OpencodeInstallFix` for `EngineOpencode`. | `backend/usecase/initenv.go` | LOW | `TestEngineInstallFixCoversOpencode` (`backend/usecase/initenv_test.go`) — Test-First | Yes |
| 2 | Widen the service-level engine gate from codex-only to codex+opencode: remove `EngineOpencode` from `unsupportedReason` (keep claude), and change `if req.Engine != domain.EngineCodex` to `if req.Engine != domain.EngineCodex && req.Engine != domain.EngineOpencode`. The proof-decides short-circuit (slice 1) then makes an already-proven opencode profile `changed:false` without reaching the adapter (criterion 2.2/2.4). | `backend/usecase/initenv.go` | MED | `TestInitEnvServiceProvisionsOpencodeThenProves`, `TestInitEnvServiceOpencodeAlreadyProvenSkipsAdapter` (`backend/usecase/initenv_test.go`) — Test-First | Yes |
| 3 | Make `engineInstallFix`-driven doctor report the opencode fix too, so `saki init-env` and `saki doctor` can never print different remediations for the same unprovisioned opencode engine (the slice-2 "one string covers both engines" trigger from slice-1's Annotation Space). In `DoctorService.checkOne`, set `report.Fix = OpencodeInstallFix` when `engine == domain.EngineOpencode` and the proof failed. | `backend/usecase/doctor.go` | LOW | `TestDoctorCheckOpencodeFixNamesTheCommand` (`backend/usecase/doctor_test.go`) — Test-First | Yes |
| 4 | Add opencode to `EngineProvisioner.Provision`'s argv mapping: `provisionArgv` returns `OpencodeProvisionArgv` for `EngineOpencode`. `ensureEngineHome` stays codex-only (verified: `opencode plugin` creates `<profile>/opencode/` itself, unlike codex which fails on a missing `CODEX_HOME`). The fingerprint already carries the opencode case from slice 1 — verify its before/after re-resolution detects the `.json`→`.jsonc` creation (criterion 2.4). | `backend/infra/initenv.go` | MED | `TestEngineProvisionerProvisionsOpencodeWithFixedArgvAndScrubbedEnv` (`backend/infra/initenv_test.go`) — Test-First | Yes |
| 5 | Update the three slice-1 assertions that pinned opencode as UNSUPPORTED (now it is provisioned): rewrite `TestInitEnvServiceOpencodeIsUnsupportedWithoutAdapter` → provisions+proves, `TestInitEnvHandlerOpencodeIsUnsupportedWithoutInvokingAnAdapter` → a success-path/`ok` handler test with a spy adapter recorded, and remove opencode from `TestEngineProvisionerRefusesEnginesWithoutAMapping` (keep claude, which stays unmapped). | `backend/usecase/initenv_test.go`, `backend/adapter/initenv_http_test.go`, `backend/infra/initenv_test.go` | MED | the rewritten tests themselves — Test-First | Yes |
| 6 | Add a real-binary e2e for opencode (criteria 2.1, 2.2, 2.4): provision a throwaway `XDG_CONFIG_HOME` profile via `saki init-env --engine opencode`, assert exit 0 + `changed:true`, re-run and assert `changed:false` + the config file is byte-identical, then assert `saki doctor --json` reports opencode `ok` for the same profile. Absent `opencode` binary FAILS LOUDLY; only `SAKI_OPENCODE_E2E=0` skips (the standing `codex-spawn` rule). | `e2e/opencode-init-env.spec.ts` (NEW) | MED | the spec itself — Test-After (it verifies steps 1–5 end-to-end) | Yes |
| 7 | Update the slice-1 e2e's "opencode and claude are refused" test (`e2e/codex-init-env.spec.ts:128`): opencode is now provisioned, so that test narrows to claude-only (slice 3 keeps the refusal). Rename to "claude is refused without writing anything" and keep the loop over `['claude']`. | `e2e/codex-init-env.spec.ts` | LOW | existing suite + the updated e2e — Test-After | Yes |
| 8 | Document opencode in the command reference: add `--engine opencode` to the `saki init-env` section (exit codes unchanged: 0 = shared proof passed, 1 = failed, 2 = bad args) and note the single command form. Reconcile `docs/saki-cli-agent-guide.md`'s manual opencode route (§1.5 table + the "equivalent by hand, still the only route for opencode" note) now that `saki init-env --engine opencode` is the supported entry. | `docs/cli-reference.md`, `docs/saki-cli-agent-guide.md` | LOW | existing suite (project rule: a route absent from the reference is unshipped) | Yes |

> Steps 1–4 are one behavioural change split by layer; each is independently committable because the
> test added with it passes on its own. Step 5 rewrites the slice-1 opencode-unsupported pins to the
> new provisioned reality.

---

## User Role Coverage

Single-actor CLI/loopback tool — no authenticated multi-role surface (PRD §10). Same roles as slice 1,
with the opencode engine now provisionable.

| Role | Can Do | Cannot Do | Auth Guard | UI Entry Point |
|------|--------|-----------|------------|----------------|
| Operator (local shell) | `saki init-env --engine opencode [--cwd <repo>] [--profile <dir>]` — mutate **only** the opencode namespace in the selected profile | mutate another engine's namespace; read/print credentials; provision claude (returns not-verified, slice 3) | `OriginGuard` on `POST /api/init-env` — loopback Host **and** loopback-or-absent Origin (`backend/adapter/originguard.go:47`) | CLI only — no UI |
| Bootstrapping agent (MCP / another `saki` process) | the same command, `--json` for a machine-readable line | same as above | same `OriginGuard`; the backend binds `127.0.0.1` only | `saki init-env --json` |
| Remote/cross-origin caller | nothing — 403 before the handler body runs | reach `initEnvHandler` at all | `OriginGuard` (criterion 1.5, unchanged) | none |

Edge cases per role: unknown engine → exit 2 (client-side, no request); escaping relative `--profile`
→ exit 2 (client-side); missing `opencode` binary → exit 1 + `OpencodeInstallFix`, no files written;
already-provisioned profile → exit 0, `changed:false`; malformed `opencode.json` → exit 1, original
config preserved byte-for-byte (criterion 2.2).

---

## Plan Wiring

### Flow 1: Operator provisions an opencode profile (happy path → `changed:true`, `ok`)
```
$ saki init-env --engine opencode --profile /tmp/p2
  → cmdInitEnv()                       (src/commands/init-env.ts:15)
      assertRunEngine()                (src/engines.ts)          — exit 2 before any HTTP
      isAbsolute/relative containment  (src/commands/init-env.ts:46) — exit 2 before any HTTP
  → StudioClient.post('/api/init-env') (src/client.ts:104; routed to Go by src/routes.ts:34)
  → POST /api/init-env                 (backend/adapter/http.go:104, OriginGuard-wrapped)
  → Handler.initEnvHandler()           (backend/adapter/http.go:250)
  → usecase.InitEnvService.Provision() (backend/usecase/initenv.go:68)
      normalizeProvisionRequest        → guards: abs cwd, existing dir, known engine, abs profile → 422
      proofs.BinaryCheck(opencode)     (infra/spawner.go:181)     → ErrBinaryNotFound (opencode)
      gate.lock(profile)               (per-profile mutex — slice 1)
      proofs.ProfileProof(opencode, p) (infra/opencode.go:36)     → already ok? short-circuit changed:false
  → infra.EngineProvisioner.Provision()(backend/infra/initenv.go:33)
      argv = OpencodeProvisionArgv      = ["opencode","plugin","@saketek/saki-builder","--global"]
      env  = scrubProfileEnv + engineProfileEnv → XDG_CONFIG_HOME=<profile>
      profileFingerprint(before)        (opencodeConfigPath — .json missing)
      runProvision(...)                 → opencode plugin --global → writes <profile>/opencode/opencode.jsonc
      profileFingerprint(after)         (now finds .jsonc) → changed = true
  → proofs.ProfileProof(opencode, p) AGAIN — THE decider (BR2)
  → {engine, profile, changed, status, reason, fix}  → 200
  → emit() (src/output.ts) → stdout JSON; status!=='ok' → EXIT.ERROR (src/commands/init-env.ts:35)
```

### Flow 2: Already-provisioned / repeat (criterion 2.2 no-op, 2.4 changed:false)
```
saki init-env --engine opencode --profile /tmp/p2   (2nd time)
  → InitEnvService.Provision → proofs.ProfileProof(opencode, /tmp/p2) == nil
  → succeed(base)  → status:"ok", changed:false     — adapter NEVER runs, no write
```

### Flow 3: Malformed config (criterion 2.2 — original preserved)
```
<profile>/opencode/opencode.json = "{ broken ]"     (invalid JSON)
  → proofs.ProfileProof(opencode, p)   → unparseable → fails (infra/opencode.go:48-51)
  → EngineProvisioner.Provision → runProvision `opencode plugin --global`
      → opencode exits 1 (JSON parse error), original file UNTOUCHED (verified empirically)
  → proofs.ProfileProof AGAIN → still fails → status:"failed", original preserved byte-for-byte
```

### Flow 4: Setup → doctor agreement (outcome 5.1)
```
saki init-env --engine opencode --profile /tmp/p2   → infra.OpencodePluginProof(&"/tmp/p2")
saki doctor    --json        --profile /tmp/p2      → usecase.DoctorService.Check
                                                   → infra.EngineProfileProof → OpencodePluginProof(&"/tmp/p2")
```
Both paths land on the **same** `OpencodePluginProof` over the **same** `opencodeConfigPath` — setup and
doctor cannot disagree about which profile was proven (PRD §5 kill criterion).

---

## Compatibility & Consumers

| Changed surface (exact) | Kind | Consumers found (`grep`) | Verdict | Mitigation / step |
|---|---|---|---|---|
| `InitEnvService.Provision` gate: codex-only → codex+opencode | behaviour | 2 (`backend/adapter/http.go:264`, e2e `codex-init-env.spec.ts:128`) | updated in steps 2 & 7 | step 2 (service), step 7 (e2e) |
| `unsupportedReason[EngineOpencode]` removed | map entry | 1 (`backend/usecase/initenv.go:78`) | updated in step 2 | step 2 |
| `engineInstallFix` — opencode now returns `OpencodeInstallFix` | function | 1 (`backend/usecase/initenv.go:174`) | updated in step 1 | step 1 |
| `DoctorService.checkOne` — opencode now sets `Fix` | behaviour | 1 (`backend/usecase/doctor.go:51`) | updated in step 3 | step 3 |
| `provisionArgv` — opencode now mapped | function | 1 (`backend/infra/initenv.go:81`) | updated in step 4 | step 4 |
| `TestEngineProvisionerRefusesEnginesWithoutAMapping` — opencode removed from refusal loop | test | 1 (`backend/infra/initenv_test.go:242`) | updated in step 5 | step 5 |
| e2e "opencode and claude are refused" → claude-only | e2e test | 1 (`e2e/codex-init-env.spec.ts:128`) | updated in step 7 | step 7 |

**Forward compatibility:** additive-only at every published boundary (a new engine in an existing
command's accepted set). The service/doctor/infra changes are internal to the Go module with all call
sites in-repo, compile-time checked. No deploy-order constraint: CLI + backend ship as one artifact pair.

---

## Migration Checklist

**N/A — no database in this project.** Same as slice 1: this slice adds no persisted state — it mutates
only the selected opencode engine's own profile directory through opencode's own installer command.

- [x] No schema change → no migration file
- [x] No destructive operation (no `DROP`/`DELETE`/`TRUNCATE`; ABSOLUTE NO-GO list untouched)
- [x] Rollback: the command is additive to a profile; removing the plugin is `opencode plugin …` outside this slice's scope

---

## Branch Points (pre-declared)

- **Step 4:** if `opencode plugin --global` on a repeat run exits non-zero ("already added") → auto-handle
  by letting the re-run proof decide (the slice-1 `AUTO-RESOLVED` precedent: BR2 says an installer exit
  code is never the signal). Reversible — already the design.
- **Step 4:** if a real opencode profile needs credentials/network auth to reach `ok` → PAUSE with one
  question. PRD §10 makes credentials a Non-Goal; the spec must reach `ok` via the profile shape
  `OpencodePluginProof` accepts, never by copying an auth file.
- **Any step:** if closing a criterion would require writing outside the selected engine's namespace,
  or reading/printing credentials → BLOCKED (crosses PRD §9 rule 3 / §10, and §12's "must not return
  credentials"). The `--global` flag is safe ONLY because pinned `XDG_CONFIG_HOME` redirects the "global"
  scope into the selected profile (verified empirically in a throwaway dir); the unsafe form is
  `npx @saketek/saki-builder install --global`, which writes to the npm global cache — explicitly excluded.

---

## Unknowns (must be <= 2)

*None — every behaviour this plan depends on was verified empirically before writing it:*

1. `opencode plugin @saketek/saki-builder --global` with pinned `XDG_CONFIG_HOME` writes ONLY
   `<profile>/opencode/opencode.jsonc` and never the real `~/.config/opencode` — **verified** by running
   it in a throwaway dir (both with and without a project `.opencode/` in cwd).
2. `opencode plugin` exits 1 and preserves a malformed config byte-for-byte — **verified**.
3. `opencode plugin` creates `<profile>/opencode/` itself (no pre-creation needed, unlike codex) — **verified**.

---

## No-Gos

- Will NOT use the `npx @saketek/saki-builder install --global` form (writes to the npm global cache,
  outside the selected profile — violates PRD §9 rule 3).
- Will NOT touch the privileged Claude `/init-env` **run** path (`SpawnInit`, `kind:init`) — PRD §10.
- Will NOT make `saki doctor` write anything — it stays read-only (PRD §9 rule 1).
- Will NOT install engine binaries, accept credentials, or print profile contents (PRD §10, §12).
- Will NOT implement the claude adapter (slice 3) — claude stays `NOT_VERIFIED`/unsupported.
- Will NOT assemble any child command through a shell — `exec.Command` with fixed argv only (PRD §6).
- Will NOT relax the loopback binding or `OriginGuard` (CLAUDE.md project rule 3).
- Will NOT let a fake binary stand in for engine-invocation evidence (PRD §9 rule 6) — the opencode
  provisioning claim is backed by the real-binary e2e (step 6).

---

## Implementation Completeness Checklist

**User Coverage**
- [x] Every role that touches this feature is in the Role Coverage matrix (operator, bootstrapping agent, cross-origin caller)
- [x] Each role has full call chain in Plan Wiring (Flows 1–4)
- [x] Permission/auth check listed for each role (`OriginGuard`, loopback bind)
- [x] Edge cases per role documented (missing binary, malformed config, already-provisioned, escaping profile)

**Database & Migrations**
- [x] N/A — no database; stated with evidence in Migration Checklist
- [x] No breaking change without rollback strategy (nothing persisted)

**API Layer**
- [x] Request/response shape unchanged — `struct{Cwd, Engine, Profile}` → `map[string]any{engine, profile, changed, status, reason, fix}` (`backend/usecase/initenv.go:158`)
- [x] HTTP method, path, router file — `POST /api/init-env`, `backend/adapter/http.go:104` (unchanged)
- [x] Dependencies — `OriginGuard` middleware; `EngineProvisioner` + `EngineProofs` ports (unchanged)

**Service / Business Logic**
- [x] Every service function modified/created named with file path (steps 1–4)
- [x] Side effects listed — one `exec.Command` child (`opencode plugin --global`) writing the opencode profile; no email/webhook/cache/journal
- [x] Error paths documented — 403 (OriginGuard), 422 (bad body/cwd/engine/profile), 200+`status:failed` (binary missing, installer failed, proof failed, malformed config)

**Frontend**
- [x] N/A — no UI in this slice; source PRD is stamped `ui:none`

**Compatibility & Consumers**
- [x] Compatibility & Consumers filled — 7 rows, every one grep-backed with a verdict; forward-compat answered
- [x] Prior slices 1 read — its shipped shape wins over the PRD (recorded in header)

**Plan Wiring**
- [x] Every major flow has an end-to-end call chain written out (Flows 1–4)
- [x] No step uses vague verbs without exact file+function (all 8 steps name the function)
- [x] No "update frontend" without naming file and function (no frontend work)

---

## Evidence Ledger

### Blocking (must be empty to present)

| # | Step | Blocking predicate (unresolved) | Evidence |
|---|------|---------------------------------|----------|
| — | — | *(empty)* | — |

### Advisory (visible, never gates)

| Step | Note | Evidence |
|------|------|----------|
| — | All anchors verified (`OpencodePluginProof`, `provisionArgv`, `unsupportedReason`, `engineInstallFix`, `profileFingerprint` opencode case, the 3 opencode-unsupported tests), all targets have creating steps, no unchecked items on state-changing steps, no unknowns above LOW | self-audit + empirical spikes |
| 4 | `ensureEngineHome` stays codex-only; opencode creates its own home (verified). If a future opencode version refuses a missing home, add the same pre-creation — not needed today. | empirical spike (opencode created `<profile>/opencode/` itself) |
| 8 | `docs/saki-cli-agent-guide.md`'s manual opencode route becomes the fallback, not the primary — the guide is reconciled, not appended to (same rule as slice-1 step 9) | PRD §7 slice 2 |

**Blocking: 0 → READY.**

---

## Success Criteria

- [x] **2.1** An empty opencode profile is provisioned and then passes the shared opencode proof; setup
  and `saki doctor --json` agree on `ok` for the same profile path.
  (`npx playwright test e2e/opencode-init-env.spec.ts` — real binary, per BR6.)
- [x] **2.2** Existing plugin registration is preserved byte-for-byte where possible and setup is a no-op
  (repeat → `changed:false`, config file byte-identical); malformed config fails without truncating or
  replacing the original file.
  (e2e repeat assertion + `TestInitEnvServiceOpencodeAlreadyProvenSkipsAdapter` + `TestEngineProvisioner_MalformedConfigPreserved` — `cd backend && go test ./usecase/ ./infra/`.)
- [x] **2.3** Installer failure is surfaced as a non-zero result and never returned as `status:ok`.
  (`TestEngineProvisioner_InstallerFailureNotOk` — `cd backend && go test ./infra/`.)
- [x] **2.4** The adapter's before/after fingerprint makes `changed:false` observable for an already
  provisioned profile and `changed:true` only when setup actually changes the selected namespace.
  (e2e `changed:true` first run / `changed:false` repeat; `TestEngineProvisionerProvisionsOpencodeWithFixedArgvAndScrubbedEnv`.)
- [x] **BR2** `status:"ok"` is set only after `ProfileProof` passes — never from a child exit code.
  (`TestInitEnvServiceProvisionsOpencodeThenProves`, `TestInitEnvServiceProofWinsOverInstallerError` — `go test ./usecase/`.)
- [x] **BR4** The child receives fixed argv and a scrubbed env: `XDG_CONFIG_HOME=<profile>` present, no
  `CODEX_*` / `CLAUDE_*` vars, and argv is exactly `opencode plugin @saketek/saki-builder --global`.
  (`cd backend && go test ./infra/ -run TestEngineProvisioner`.)
- [x] **BR3** No write happens outside the selected opencode namespace — the `--global` flag is redirected
  into `<profile>/opencode/` by pinned `XDG_CONFIG_HOME` (verified empirically, pinned in the argv test).
  (`TestEngineProvisionerProvisionsOpencodeWithFixedArgvAndScrubbedEnv`.)
- [x] **claude stays unsupported** — `--engine claude` returns the typed not-verified result, writes
  nothing (slice 3).
  (`go test ./usecase/ -run TestInitEnvServiceClaudeIsNotVerifiedWithoutWriting` + updated e2e.)
- [x] Whole-repo code gates stay green: `npm run typecheck` · `npx vitest run` · `cd backend && go vet ./... && go test ./...`. The repository `npm test` wrapper still exits after its pre-existing `s.test`/backend setup probes fail; direct Vitest passes all 383 tests.
- [x] `docs/cli-reference.md` documents `--engine opencode` — `grep -c 'opencode' docs/cli-reference.md` ≥ 2.

---

## Annotation Space

### Final QA and security disposition — 2026-08-20

- F6-focused real-binary e2e: PASS — `e2e/codex-init-env.spec.ts` and `e2e/opencode-init-env.spec.ts` passed (5 tests).
- Direct CLI unit suite: PASS — `npx vitest run` (383 tests).
- Backend: PASS — `go vet ./...` and `go test ./... -count=1 -timeout 120s` (680 tests in 5 packages).
- TypeScript and coverage: PASS — `npm run typecheck`; `npm run test:coverage` (81.74% statements, 83.51% lines, above the 80% floor).
- Full Playwright suite: 7/8 passed. The sole failure is pre-existing `e2e/opencode-spawn.spec.ts`, timing out/aborting while reading its long-lived `/events/<runId>` SSE stream (`write EPIPE` in the Playwright error artifact); all F6 init-env specs passed. A direct retry was runner-aborted before producing a new assertion failure.
- Focused security review: no confirmed new HIGH/MEDIUM finding after checking profile containment, fixed argv, environment scrubbing, proof-decides semantics, and lock keys. Existing `/api/run` origin coverage and Opencode spawn SSE behavior remain outside this slice.

### Prior-slice handoff (slice 1 → slice 2)

Slice 1 shipped with opencode **unreachable** — the service refused it and the infra mapped only codex —
specifically because the pre-existing WIP used the `npx … install --global` form that writes outside the
selected profile. Slice 2's job is to make opencode *reachable and correct*, and the correct form is the
single `opencode plugin @saketek/saki-builder --global` command with pinned `XDG_CONFIG_HOME`. This plan
verified that form empirically before committing to it (three spikes: profile containment, malformed
preservation, home auto-creation) — so slice 2 does not blindly re-enable the WIP's unsafe shape.

> Human: add notes, corrections, constraints here.

---

Status: [ ] Draft  [x] Annotated  [x] Approved (TRUST MODE — `/saki-builder:build`)  [ ] In Progress  [x] Complete
Readiness Gate: [x] Evidence Ledger present and every blocking item cited  [x] Blocking Set empty  [x] Unknowns <= 2
