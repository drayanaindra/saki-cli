# EXECUTION PLAN: saki doctor — Slice 2 (trustworthy verdicts: binary-absent, provable agreement, complete remediation)

**Date:** 2026-08-16
**Blocking items:** 0 (see Evidence Ledger)
**Risk Score:** MED (changes an operator-facing spawn-refusal string that is also doc-quoted verbatim; no DB, no auth, no money)
**Unknown Count:** 0 / 2 max
**Behavior Spec:** N/A — backend/CLI-only, headless by design (`docs/project-context.md` § Deliberate
non-goals: "No UI, no browser").
**Source PRD:** `tasks/prd-saki-doctor-verify-engine-provisioning-before-a-run.md` § Slice 2
**Prior slices:** `tasks/saki-doctor-verify-engine-provisioning-before-a-run-slice1-plan.md` — read in
full. Divergence from what slice 1 assumed: three of slice 2's five criteria (2.1 distinct
binary-absent reason, 2.4 OriginGuard-403, 2.5 unreachable-exits-3) are **already true behaviorally**
as a side effect of how slice 1 was built (`checkOne`'s BinaryCheck/ProfileProof split already
produces distinct reason text; `GET /api/doctor` was mounted OriginGuard-wrapped in slice 1;
`cmdDoctor`'s unreachable path already rides the existing `client.ts` contract unchanged — see its own
docstring, `src/commands/doctor.ts:14-17`). None of the three had a **dedicated regression test**
proving it, though — slice 1's Success Criteria never claimed them. This plan therefore skews toward
Test-Along regression coverage for 2.1/2.4/2.5 and real implementation + tests for 2.2 (genuinely new
test infrastructure) and 2.3 (genuinely new behavior: `Fix` is populated for the first time).
**Appetite:** ~6 agent tasks (5 acceptance criteria; 2.3 splits into a source-of-truth constant step
plus its drift-guard test step because "don't hardcode two copies of the string" is itself a testable
contract)
**Kill-if:** §6's kill criterion is anchored HERE — criterion 2.2 (doctor↔preflight agreement) is the
metric the PRD's kill-if references; if this plan cannot make that comparison hold on every fixture,
the feature's core claim (rule 4: doctor and preflight can never disagree) is false and the PRD is
killed, not shipped degraded.

## Problem Statement

When `codex` is missing from PATH, its `saki doctor` reason currently reads no differently in *proof*
than a missing-profile failure would (both are just "failed" + arbitrary text — nothing pins the
distinction as a load-bearing contract). Nothing proves doctor's verdict actually matches what a real
spawn would do. And codex's `fix` field is silently empty, so an operator told "failed" has no
runnable next step. This slice makes the verdict trustworthy, not merely present.

---

## Concrete Example Output

```
$ saki doctor --json    # codex genuinely absent from PATH, opencode provisioned
{"engines":[
  {"engine":"codex","profile":"default","status":"failed","reason":"engine binary not found on PATH (codex)","fix":""},
  {"engine":"opencode","profile":"default","status":"ok","reason":"","fix":""}
]}

$ saki doctor --json --profile /tmp/broken-codex-home   # both binaries present, codex profile unprovisioned
{"engines":[
  {"engine":"codex","profile":"/tmp/broken-codex-home","status":"failed","reason":"engine profile cannot resolve the saki-builder commands: codex profile does not resolve @saketek/saki-builder: /tmp/broken-codex-home/codex/config.toml registers no enabled saki-builder plugin and /tmp/broken-codex-home/codex/skills/build/SKILL.md is absent — run:\ncodex plugin marketplace add https://github.com/drayanaindra/saki-builder.git\ncodex plugin add saki-builder@saketek\n(or bash scripts/install-codex-skills.sh to check)","fix":"codex plugin marketplace add https://github.com/drayanaindra/saki-builder.git\ncodex plugin add saki-builder@saketek"},
  {"engine":"opencode","profile":"/tmp/broken-codex-home","status":"ok","reason":"","fix":""}
]}
```

The binary-absent row's `reason` never contains "does not resolve @saketek/saki-builder" (that
substring is unique to a `ProfileProof` failure) — that textual distinctness, proven by a real
end-to-end test (not a fake), is criterion 2.1. The second example's `fix` is non-empty for codex for
the first time in this feature, sourced from one Go constant that both the operator-facing spawn
refusal (`backend/infra/codex.go`) and doctor's `EngineReport.Fix` read — never two independent copies
of the string (criterion 2.3).

---

## Design decisions (not fully specified by the PRD §16 — resolved here, cited)

**2.3 — one constant, two readers, never two copies.** `usecase` has zero outbound deps and already
owns `ErrEngineNotProvisioned`; `infra/codex.go` already imports `usecase` (for that same sentinel).
So the two-line remediation text becomes `usecase.CodexInstallFix` — a package-level string constant
in `backend/usecase/doctor.go` — and BOTH `CodexSkillsProof`'s error message (`backend/infra/codex.go`)
and `DoctorService.checkOne`'s `Fix` field read the same constant. This keeps the dependency direction
intact (`usecase` still has zero outbound deps; `infra` already depends on `usecase`, not the reverse)
and makes "the test fails if either drifts from the script" achievable without a second hardcoded copy
anywhere in production code — only the test itself pins the two literal lines, and checks both the
constant AND the installer script against them (§ Step 3 below).

**2.3 — `Fix` is populated ONLY on a `ProfileProof` failure, never a `BinaryCheck` failure.** A missing
*binary* is not fixed by plugin-install commands (`codex plugin add` does nothing if `codex` itself
isn't installed) — the two failures need different remediations, and this slice only has a
`ProfileProof`-side remediation authored. `checkOne` already structurally distinguishes the two
branches (short-circuit, `backend/usecase/doctor.go:36-43`); Step 1 hooks `Fix`-population into the
existing `ProfileProof`-failure branch only, so a binary-absent codex report keeps `Fix: ""` (matches
2.1's own fixture, which never exercises `Fix`).

**2.2 — the agreement test drives the REAL `preflight`, not a re-derivation of it.** `preflight`
(`backend/infra/spawner.go:155`) is unexported — reachable only from a test in package `infra`. This is
exactly what makes the proof real: the new test calls `preflight(usecase.SpawnSpec{...})` (the literal
function a spawn calls) and separately `usecase.NewDoctorService(EngineProofChecker{}).Check(&dir)`
(the literal service `saki doctor` calls) against the SAME fixture directory, and asserts their
per-engine yes/no verdicts agree. `TestEngineProofChecker_DelegatesToSharedFuncs` (existing, slice 1)
already proves `EngineProofChecker` calls the same functions as `preflight` at the unit level — this
new test is the end-to-end version using real fixtures, the one that would actually catch a future
edit that made `preflight` and `doctor` diverge (e.g., someone adding a check to one and not the
other). For opencode, `preflight` also runs a prompt-shape refusal between `EngineBinaryCheck` and
`EngineProfileProof` (`spawner.go:160-171`) that doctor deliberately has no equivalent of (rule 4's own
carve-out, already documented in slice 1's plan) — the fixture's `Prompt` field is a plain non-slash
string (`"hello"`) specifically so that unrelated refusal never fires and contaminates the comparison.

---

## Steps

| # | Action | Files (exact paths) | Risk | Test | Committable? |
|---|--------|---------------------|------|------|---------------|
| 1 | Add `const CodexInstallFix = "codex plugin marketplace add https://github.com/drayanaindra/saki-builder.git\ncodex plugin add saki-builder@saketek"` to `backend/usecase/doctor.go`; in `checkOne`, when `s.proofs.ProfileProof(engine, configDir)` fails AND `engine == domain.EngineCodex`, set `report.Fix = CodexInstallFix` | `backend/usecase/doctor.go` | MED (business logic — the field an operator acts on) | Test-First: `TestDoctorService_Check/codex_profile_failure_populates_Fix` — asserts `codex.Fix == CodexInstallFix` when only `ProfileProof` fails for codex; `TestDoctorService_Check/codex_binary_failure_leaves_Fix_empty` — asserts `codex.Fix == ""` when `BinaryCheck` fails for codex (short-circuit path, Fix never touched); `TestDoctorService_Check/opencode_never_gets_a_Fix` — asserts `opencode.Fix == ""` even when opencode's `ProfileProof` fails (F5 scope boundary, unchanged) | Yes |
| 2 | In `backend/infra/codex.go`'s `CodexSkillsProof`, change the `Errorf` call so the message embeds BOTH installer lines via `usecase.CodexInstallFix` instead of the single hardcoded `codex plugin add saki-builder@saketek` clause — new format: `"...is absent — run:\n%s\n(or bash scripts/install-codex-skills.sh to check)"` with `usecase.CodexInstallFix` as the `%s` arg | `backend/infra/codex.go` (lines 71-75) | MED (changes an operator-facing string emitted at real spawn time) | Test-Along: extend the existing table-driven codex proof tests in `backend/infra/codex_test.go` (grep for the existing `TestCodexSkillsProof*` and add an assertion that a failing case's error message contains both `usecase.CodexInstallFix` lines) — confirms the wrap didn't silently drop the marketplace-add line | No — completes with step 8 (doc consumers must update in the same commit as the text they quote verbatim) |
| 3 | New test `TestCodexInstallFix_MatchesInstallerScript` in `backend/usecase/doctor_test.go`: read `../../scripts/install-codex-skills.sh` (relative from `backend/usecase`), assert the file contains both literal lines `codex plugin marketplace add https://github.com/drayanaindra/saki-builder.git` and `codex plugin add saki-builder@saketek`, AND assert `CodexInstallFix` contains both — so either side drifting independently fails the test (criterion 2.3's own wording) | `backend/usecase/doctor_test.go` | LOW (test-only) | Test-Along (pure test, no separate impl — Step 1 already built the constant) | Yes |
| 4 | New test `TestDoctorService_Check_CodexBinaryAbsent_DistinctFromProfileReason` in `backend/infra/doctor_test.go`: `t.Setenv("PATH", ...)` a temp dir containing ONLY a fake `opencode` binary (codex absent), `writeOpencodeProfile` with a provisioned `{"plugin":["@saketek/saki-builder"]}` fixture (so opencode reports ok, isolating the codex row), run `usecase.NewDoctorService(EngineProofChecker{}).Check(&dir)`, assert the codex report has `Status == domain.StatusFailed`, `Reason` contains `"engine binary not found on PATH (codex)"`, and `Reason` does NOT contain `"does not resolve @saketek/saki-builder"` (the substring unique to a `ProfileProof` failure — same technique slice 1's HIGH-bug fix used, `backend/infra/doctor_test.go:104-110`) | `backend/infra/doctor_test.go` | MED (real end-to-end proof of criterion 2.1 — a fake-based test would not catch a real regression here, per the "test doubles validate structure, not live behavior" house pattern) | Test-Along | Yes |
| 5 | New file `backend/infra/doctor_agreement_test.go`, function `TestDoctorPreflightAgreement`: table-driven over `{engine: codex, opencode} × {profile: provisioned, unprovisioned}` (4 cases). `putBinariesOnPath(t)` (reuse, `doctor_test.go:32-42`); per case write the fixture (`writeCodexProfile`/`writeOpencodeProfile`, reuse) in a fresh `t.TempDir()`; call `preflight(usecase.SpawnSpec{ID: "agreement-check", Prompt: "hello", ConfigDir: &dir, Engine: engine})` (package-private — reachable because this test lives in package `infra`) and `usecase.NewDoctorService(EngineProofChecker{}).Check(&dir)`, pick the matching engine's report, assert `(preflightErr == nil) == (report.Status == domain.StatusOK)` | `backend/infra/doctor_agreement_test.go` (NEW) | HIGH (this is the PRD's kill-criterion test — a failure here means the feature's core claim is false) | Test-First: the table above IS the test (no new production code — `preflight`, `EngineBinaryCheck`, `EngineProfileProof`, `EngineProofChecker` all already exist unchanged from slice 1; this step only proves they agree) | Yes |
| 6 | New test `TestDoctorHandler_RejectsNonLoopbackHost` in `backend/adapter/doctor_http_test.go`: `mux := doctorHandlerFor(&fakeEngineProofs{}).Routes()` (reuse, `doctor_http_test.go:28-30`), build a `GET /api/doctor` request, set `req.Host = "evil.com"`, `mux.ServeHTTP(rec, req)`, assert `rec.Code == http.StatusForbidden` — same pattern as the existing git-write regression at `backend/adapter/http_test.go:556-569` | `backend/adapter/doctor_http_test.go` | MED (security regression guard — criterion 2.4) | Test-Along | Yes |
| 7 | Extend `routedCtx` in `src/commands/doctor.test.ts` to accept a `down?: boolean` route entry (mirror `src/commands/runs.test.ts:9-18`'s `routed()`: `if (r.down) throw new TypeError('fetch failed')` inside the stub's fetch impl); add test `'exits UNREACHABLE when the backend is down'` — routes `/api/doctor` to `{ down: true }`, asserts `cmdDoctor(ctx, [], {})` **rejects** with a `CliError` carrying `code: EXIT.UNREACHABLE` — adjusted in impl: `cmdDoctor` never catches `ctx.client.get`'s throw (Flow 5), so the assertion is `.rejects.toMatchObject({ code: EXIT.UNREACHABLE })` per the house convention every other command test uses (`src/commands/prd.test.ts`, `run.test.ts`, `runs.test.ts`, `roadmap.test.ts`, `repo.test.ts`), not a returned value | `src/commands/doctor.test.ts` | MED (proves criterion 2.5 for `cmdDoctor` specifically, not just the shared client contract in the abstract) | Test-Along | Yes |
| 8 | Update the two doc call-sites that quote the codex spawn-refusal text verbatim, now stale after step 2: `docs/cli-reference.md:187-193` (console example block) and `docs/saki-cli-agent-guide.md:139-140` (same quoted text) — both get the completed two-line remediation. Also complete `docs/cli-reference.md:168`'s "Provision with" table cell for the codex row to both lines, for consistency with the table's own `bash` block two rows below it (`docs/cli-reference.md:172-173`, which already lists both lines) | `docs/cli-reference.md`, `docs/saki-cli-agent-guide.md` | LOW (docs only) | Test-After (no test; verified by re-grep for the exact new text after edit) | Yes — completes step 2's atomic commit |

> Steps 2 and 8 are grouped into ONE commit: the operator-facing string and the two docs that quote it
> verbatim must never land in separate commits (a doc/code drift window, however brief, is exactly the
> failure mode step 3's drift-guard test exists to prevent at the code layer — the docs layer has no
> automated guard, so the discipline is "same commit" instead).

---

## User Role Coverage

Unchanged from slice 1 — `saki doctor` has exactly one "role": the local operator/agent running the
CLI on the same machine as the backend (loopback-only, no auth surface). No new role is introduced by
this slice.

| Role | Can Do | Cannot Do | Auth Guard | UI Entry Point |
|------|--------|-----------|------------|-----------------|
| Local operator/agent | Run `saki doctor [--profile <dir>]`; read a codex `fix` when profile-unprovisioned | Trigger doctor remotely (loopback + OriginGuard reject non-loopback Host, step 6 locks this in for `/api/doctor` specifically) | OriginGuard (`backend/adapter/originguard.go`), same as all 8 other GET routes | CLI only — no UI (headless by design) |

---

## Plan Wiring

### Flow 1: codex binary absent → distinct reason (2.1)
```
saki doctor --json (src/commands/doctor.ts:cmdDoctor)
  → ctx.client.get('/api/doctor') (src/client.ts)
  → GET /api/doctor (backend/adapter/http.go:97, doctorHandler)
  → usecase.DoctorService.Check() → checkOne() (backend/usecase/doctor.go:25-44)
  → infra.EngineProofChecker.BinaryCheck() → infra.EngineBinaryCheck() (backend/infra/spawner.go:181-193)
  → returns wrapped usecase.ErrBinaryNotFound ("engine binary not found on PATH (codex)")
  → domain.EngineReport{Status: StatusFailed, Reason: "...", Fix: ""} — ProfileProof never called (short-circuit)
```

### Flow 2: doctor verdict ≡ preflight verdict (2.2)
```
Test only — no user-facing call chain; the two REAL production entry points compared directly:
  ShSpawner.Spawn() → preflight(spec) (backend/infra/spawner.go:118,155)
    → EngineBinaryCheck(engine) → EngineProfileProof(engine, configDir)
  usecase.NewDoctorService(infra.EngineProofChecker{}).Check(configDir) → checkOne()
    → infra.EngineProofChecker.BinaryCheck() → infra.EngineProofChecker.ProfileProof()
      → EngineBinaryCheck(engine) / EngineProfileProof(engine, configDir)   [SAME functions]
Both chains bottom out in the identical EngineBinaryCheck/EngineProfileProof calls (rule 4) — step 5
proves this holds end to end over real fixtures, not merely "both call the same Go function names".
```

### Flow 3: codex complete remediation (2.3)
```
CodexSkillsProof() fails (backend/infra/codex.go:52-75)
  → error wraps usecase.CodexInstallFix (backend/usecase/doctor.go, NEW const)
  → checkOne() sets report.Fix = usecase.CodexInstallFix (backend/usecase/doctor.go, step 1)
  → domain.EngineReport.Fix (JSON field, unchanged shape)
  → src/commands/doctor.ts:38 — ctx.writeErr(`fix (${e.engine}): ${e.fix}`) (unchanged, already generic)
```

### Flow 4: OriginGuard on /api/doctor (2.4)
```
GET /api/doctor with Host: evil.com
  → backend/adapter/http.go:97 — mux.Handle("GET /api/doctor", OriginGuard(http.HandlerFunc(h.doctorHandler)))
  → backend/adapter/originguard.go — OriginGuard middleware rejects non-loopback Host → 403
  → h.doctorHandler never invoked
```

### Flow 5: backend unreachable → exit 3 (2.5)
```
saki doctor (src/commands/doctor.ts:cmdDoctor)
  → ctx.client.get('/api/doctor') → StudioClient.requestOn() (src/client.ts:149-161)
  → fetchImpl throws (socket never opened)
  → catch → throw new CliError(..., EXIT.UNREACHABLE, ...) (src/client.ts:156-160)
  → uncaught by cmdDoctor (it never wraps client.get in try/catch) → propagates to main()'s single catch
  → process exits 3
```

---

## Compatibility & Consumers

| Changed surface (exact) | Kind | Consumers found (`grep`) | Verdict | Mitigation / step |
|---|---|---|---|---|
| `CodexSkillsProof`'s error message text (`backend/infra/codex.go:52-75`) | operator-facing error string | `docs/cli-reference.md:187-193` (`grep -n "registers no enabled saki-builder plugin" docs/cli-reference.md`); `docs/saki-cli-agent-guide.md:139-140` (same grep); `backend/infra/doctor_test.go:108` (substring check on `"does not resolve @saketek/saki-builder"` only — preserved, unaffected); `backend/usecase/doctor_test.go:45-50` (uses an independent fake-supplied string, never reads the real error — unaffected); no e2e spec references it (`grep -rln "does not resolve @saketek\|codex plugin add" e2e/` → 0 matches) | 2 consumers `breaks` (stale doc text) — mitigated; 2 unaffected; 0 found elsewhere | step 8 (docs), same commit as step 2 |
| `usecase.DoctorService.checkOne`'s `EngineReport.Fix` field | struct field, now populated for the first time for codex | `src/commands/doctor.ts:38` (`ctx.writeErr` — already handles any non-empty `fix` generically, no change needed); `src/commands/doctor.test.ts` (existing fix-present/fix-absent tests, both `it()` cases still pass — they parametrize `fix` as test input, they don't assert it stays `""`) | unaffected (both consumers already generic over `fix`'s value) | — |
| `preflight` (unexported, `backend/infra/spawner.go:155`) | function, unchanged signature | `ShSpawner.Spawn` (`backend/infra/spawner.go:118`, the one production caller — verified `grep -rn "preflight(" backend --include='*.go'` returns only the definition + this call site + step 5's new test call) | unaffected — step 5 calls it read-only from a new test, adds no new caller in production code | — |

**Forward compatibility:** additive-only for `EngineReport.Fix` (was always present per slice 1's
JSON shape, just always `""` before — no client needs a version bump to start reading a non-empty
value). The `CodexSkillsProof` text change is a pure string content change behind an already-existing
error path; no caller pattern-matches on the message's exact wording in production code (only tests and
docs quote it, both updated in this plan).

---

## Migration Checklist

N/A — no schema change in this slice.

---

## Branch Points (pre-declared)

- Step 1: If a future engine (F4/claude) needs a `Fix` too → NOT this slice's concern; the `engine ==
  domain.EngineCodex` guard is intentionally narrow (reversible — decided here, not paused: extending
  it is a trivial one-line change when F4 lands, and building it generically now for a single real
  consumer would be the YAGNI violation self-review (§6a #8) explicitly flags).
- Step 5: If `preflight` and doctor's verdict ever disagree on a fixture → this is NOT a bug to silently
  paper over. BLOCKED per the Kill-if header — surface it as a failing test and stop; do not weaken the
  assertion or add a special-case exemption to make it pass (would defeat the PRD's own kill criterion).

---

## Unknowns (must be <= 2)

None. Every reference below was verified by direct read/grep during research (see Evidence Ledger).

---

## No-Gos

- Will NOT populate `Fix` for opencode (F5, explicitly deferred — mirrors slice 1's own boundary).
- Will NOT add a `Fix` for a codex `BinaryCheck` failure (no remediation text has been authored for
  "install the codex CLI itself" — out of scope; `Fix` stays `""` on that path, matching 2.1's fixture).
- Will NOT touch `EngineBinaryCheck`, `EngineProfileProof`, `OpencodePluginProof`, or `OriginGuard`
  itself — all proven correct by slice 1 and this slice's own agreement test; only `CodexSkillsProof`'s
  error *string* changes, and only its remediation clause.
- Will NOT add claude coverage (F4) or install-time repair (rule 1: doctor is read-only) — unchanged
  boundaries from the PRD's §11 non-goals.

---

## Implementation Completeness Checklist

**User Coverage**
- [x] Single role (local operator/agent) — unchanged from slice 1, listed in Role Coverage matrix
- [x] Full call chain traced per criterion in Plan Wiring (Flows 1-5)
- [x] Auth guard (OriginGuard) listed and locked in by a dedicated test (step 6)
- [x] Edge cases: binary-absent (2.1), unreachable backend (2.5), both new dedicated tests

**Database & Migrations**
- [x] N/A — no schema change (Migration Checklist section states this explicitly)

**API Layer**
- [x] No new endpoint — `GET /api/doctor` (existing, slice 1). Response shape unchanged (`Fix` was
  always a JSON field, now sometimes non-empty for codex).

**Service / Business Logic**
- [x] `checkOne` change named with file path (step 1) and exact new field-population logic
- [x] Side effects: none (still read-only — rule 1 unchanged; no filesystem/network write introduced)
- [x] Error paths: binary-absent and profile-unprovisioned both explicitly tested (steps 4, existing
  slice-1 tests)

**Frontend**
- [x] N/A — no UI, headless CLI. `src/commands/doctor.ts` needs zero code changes (its `fix` rendering
  is already generic over any non-empty value, verified by reading the file) — only its test file gains
  a new case (step 7).

**Compatibility & Consumers**
- [x] Compatibility & Consumers filled — every changed surface has a consumers cell + verdict, the one
  `breaks` verdict (docs) has a mitigation step (8), forward-compat answered
- [x] Prior slices 1..N-1 read — slice 1 read in full, divergence recorded in header

**Plan Wiring**
- [x] Every criterion (2.1-2.5) has an end-to-end call chain written out (Flows 1-5)
- [x] No step says "update X" without exact file + function
- [x] No step adds an endpoint without method/path (none added — existing route reused)

---

## Evidence Ledger

Readiness is a boolean: the plan is presentable when the **Blocking** table is empty.

### Blocking (must be empty to present)

*(none)*

### Advisory (visible, never gates)

| Step | Note | Evidence |
|------|------|----------|
| 2 | `docs/cli-reference.md:168`'s "Provision with" table cell is completed for consistency even though only the console-example block (187-193) is a genuine `breaks` consumer of the changed string (the table cell was already independently authored, not quoted from the error text) — cosmetic, LOW | `docs/cli-reference.md:168` vs `:172-173` (the two-line bash block two rows below, already correct) |
| — | All anchors verified by direct read/grep during research (this session): `backend/usecase/doctor.go` (full file read), `backend/usecase/doctor_test.go` (full file read), `backend/infra/doctor.go` (full file read), `backend/infra/doctor_test.go` (full file read), `backend/infra/codex.go` (full file read, lines 72/74 confirmed as the PRD's cited `Assumes` anchor), `scripts/install-codex-skills.sh` (lines 74-75 confirmed), `backend/adapter/http.go` (line 97 confirmed OriginGuard-wrapped), `backend/adapter/doctor_http_test.go` (full file read, `doctorHandlerFor` helper confirmed reusable), `backend/adapter/http_test.go:556-569` (403-via-mux pattern confirmed reusable), `backend/infra/spawner.go:145-204` (preflight + EngineBinaryCheck + EngineProfileProof read in full), `backend/usecase/spawn.go:20` (`ErrBinaryNotFound` message text confirmed), `src/commands/doctor.ts` (full file read — confirmed zero code change needed for 2.3/2.5 on the CLI side), `src/exit.ts` (full file read, `EXIT.UNREACHABLE = 3` confirmed), `src/commands/runs.test.ts:9-18,161-165` (the `down: true` → `throw new TypeError('fetch failed')` pattern confirmed as the house convention to mirror), `docs/cli-reference.md` (doctor section + codex console example read in full), `docs/saki-cli-agent-guide.md:139-140` (second consumer of the quoted error text found via full-repo grep) — all targets have a named creating step and no checklist item on any state-changing step is unchecked, no unknowns above LOW | self-audit |

**Blocking: 0 → READY.**

---

## Success Criteria

- [x] 2.1 — `go test ./backend/infra/... -run TestDoctorService_Check_CodexBinaryAbsent_DistinctFromProfileReason -v` passes: codex row `status=failed`, `reason` contains `"engine binary not found on PATH (codex)"`, does not contain `"does not resolve @saketek/saki-builder"` (→ 5.2). PASS — also verified LIVE against the real backend with codex's directory excluded from its PATH: `{"engine":"codex","status":"failed","reason":"engine binary not found on PATH (codex)","fix":""}`, exit 1.
- [x] 2.2 — `go test ./backend/infra/... -run TestDoctorPreflightAgreement -v` passes on all 4 fixture cases (codex/opencode × provisioned/unprovisioned): `preflight`'s error-or-nil agrees with doctor's `ok`/`failed` verdict on every case (→ 5.4; this is the PRD's kill-criterion test). PASS — 5/5 (1 top-level + 4 subtests).
- [x] 2.3 — `go test ./backend/usecase/... -run TestDoctorService_Check -v` passes (Fix-population subtests) AND `go test ./backend/usecase/... -run TestCodexInstallFix_MatchesInstallerScript -v` passes: codex's `fix`, when populated, contains both `codex plugin marketplace add https://github.com/drayanaindra/saki-builder.git` and `codex plugin add saki-builder@saketek`, cross-checked against `scripts/install-codex-skills.sh`'s own text (→ 5.3). PASS — 10/10 + 1/1.
- [x] 2.4 — `go test ./backend/adapter/... -run TestDoctorHandler_RejectsNonLoopbackHost -v` passes: `GET /api/doctor` with `Host: evil.com` returns 403 (→ guardrail: security). PASS — also verified LIVE: `curl -H 'Host: evil.example.com' http://127.0.0.1:8788/api/doctor` → 403.
- [x] 2.5 — `npx vitest run -t "exits UNREACHABLE when the backend is down"` passes: `cmdDoctor` rejects with a `CliError{code: EXIT.UNREACHABLE}` (3) when the fetch rejects — adjusted in impl: throws (propagated, uncaught), not a returned value; matches every sibling command test's convention (→ guardrail: error-path). PASS — also verified LIVE: backend killed, `node dist/index.js doctor --json` → exit 3, "cannot reach the studio".
- [x] Whole-repo regression: `cd backend && go build ./... && go vet ./... && go test ./... -timeout 120s` all green. PASS — 638 tests passed, 5 packages, vet clean.
- [x] Whole-repo regression: `npx tsc --noEmit && npx vitest run` all green. PASS — 302 tests passed, 16 files.
- [x] Docs consistency: `grep -n "codex plugin marketplace add" docs/cli-reference.md docs/saki-cli-agent-guide.md` both match the post-step-2 error text (no stale single-line quote remains). PASS.
- [x] Coverage floor (≥80%, both stacks): backend 83.6% statements, CLI 96.57% lines. PASS.

---

## Annotation Space

> Human: add notes, corrections, constraints here.
> Claude will revise plan and re-check the Blocking Set before proceeding.

(Invoked by `/saki-builder:build` in TRUST MODE — auto-approved, no human annotation expected before
`/saki-builder:approved` begins.)

---
Status: [ ] Draft  [ ] Annotated  [x] Approved  [ ] In Progress  [ ] Complete
Readiness Gate: [x] Evidence Ledger present and every blocking item cited  [x] Blocking Set empty  [x] Unknowns <= 2
