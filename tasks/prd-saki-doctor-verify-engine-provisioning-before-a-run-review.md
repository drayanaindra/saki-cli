<!-- review-verdict: SHIP -->
<!-- failure-surface: 2/2 -->
<!-- round: 3 -->

# PRD Review — `saki doctor`

**Reviewer:** unassigned · **Date:** 2026-08-15 · **Status:** Open
**PRD reviewed:** `tasks/prd-saki-doctor-verify-engine-provisioning-before-a-run.md` — blocking 0 · Updated 2026-08-15
**Verdict:** DISCOVERY-FIRST · **Readiness:** NOT READY · **Failure-surface:** 0/3 · **Round:** 1

> ## ⚠ ROUND-1 VERDICT WITHDRAWN AND CORRECTED (2026-08-15)
>
> **This round-1 record carried a DISCOVERY-FIRST verdict that was WRONG. It is superseded by round 2.**
> Retained unedited below as the review trail; the reasoning error is recorded here so it is not repeated.
>
> **The error.** Round 1 read "0 of 214 journals carry `ErrEngineNotProvisioned`" as disconfirming
> evidence that provisioning failures occur. It is not evidence — it is a **measurement artifact**.
> `preflight` runs inside `Spawn()`; a refusal returns an error → `s.store.Delete(id)`
> (`backend/usecase/spawn.go:83`) → and `domain.Run` has **no error field**
> (`backend/domain/run.go:85-108`). A provisioning refusal can therefore never appear in a journal.
> **The query is structurally incapable of returning anything but 0.** The same mechanism was verified
> in round 1 and not connected to the conclusion drawn from it.
>
> **The second error.** Round 1 asked whether codex/opencode are "real runtimes for this repo",
> treating one machine's usage log as grounds to question the product definition. Multi-engine support
> is a **stated commitment**: `README.md:31`, `package.json:5`, real-binary e2e specs for both
> non-claude engines, and dedicated `codex.go` / `opencode.go` / `osprofiles.go` / an install script.
> The `~/.saki-pipeline/runs` dir is also **shared with the predecessor `apps/server`**
> (`backend/infra/journal.go:37`) — those 214 journals are pipeline-studio history, not saki-cli
> engine usage.
>
> **What survives:** the structural findings below (R2, R3, R4, R5, R6, R7 and most HIGHs) are real and
> were all applied in round 2. **What is withdrawn:** R1's "disconfirmed" framing — the failure *rate*
> is unmeasured, not disproven, and the PRD now rests on the failure's observable *cost* instead.
> Corrected verdict for round 1: **REVISE**, not DISCOVERY-FIRST.

---

## The round-1 finding (as recorded — see the correction above)

| Check (`~/.saki-pipeline/runs/go`) | Result |
|---|---|
| Total Go run journals | 214 |
| Journals carrying `ErrEngineNotProvisioned` | **0** ← artifact: cannot be non-zero |
| Engine distribution | **214/214 claude** (field absent = claude) |

Mechanism, verified independently: `domain.Run` carries **no error field**
(`backend/domain/run.go:85-108`), and a refused spawn is removed from the store —
`s.store.Delete(id)` (`backend/usecase/spawn.go:83`). This is the correct and durable finding: §5.1 and
§5.4 as originally written were never measurable. It is *not* evidence about how often the failure occurs.

## Findings (ledger)

### R1 · BLOCK · panel:premise + coordinator · §2
Finding: `"**Load-bearing assumption:** … — observed"` — laundered *and* disconfirmed. Every cited source
(two proofs, a sentinel, an install script, a README TODO) is defensive code the team wrote; it evidences a
past author's anticipation, not operator incidence. The real data reads 0/214.
Fix: retag `assumed` and publish the real baseline; if it stays 0, retire or re-premise as a once-per-machine
setup aid — not a recurring-failure guard.
Disposition: Open

### R2 · BLOCK · panel:premise · §2 vs §11
Finding: §2 names exactly one concrete harm — `"a claude profile whose saki-builder plugin is missing still
resolves nothing"` — and §11 forbids fixing it (`"✗ Changing spawn behaviour for claude"`). The operator with
the broken profile is precisely the one who will not have run an opt-in diagnostic first.
Fix: either wire `ClaudeSkillsProof` into `preflight` behind an opt-out, or delete the claude harm from §2 and
admit this is a setup convenience.
Disposition: Open

### R3 · BLOCK · panel:metrics + panel:impl + coordinator · §5 5.1
Finding: `query:` Method against a field that does not exist. Journals record `<id>.out`, `<id>.exit` and the
run record only (`backend/infra/journal.go:62-99`); no error field; a refused spawn is deleted from the store.
Fix: replace with an observable substitute, or add an instrumentation slice that persists the refusal reason.
Disposition: Open (partially applied — Method retagged `external`, but the outcome remains unmeasured)

### R4 · BLOCK · panel:metrics · §5 5.4
Finding: same defect, plus two more — no `engine` field is journalled to join on, and Invariant 1 makes doctor
read-only so it leaves no trace of what it reported.
Fix: rewrite as a paired in-repo assertion (doctor `ok` ⇒ preflight does not refuse over the same fixture).
Disposition: Open (partially applied)

### R5 · BLOCK · panel:metrics + panel:premise · §6
Finding: `"stop if 5.4 is non-zero after slice 2"` — the kill criterion is gated on an unmeasurable metric, so
it can never fire. A kill criterion that cannot trigger is not a kill criterion.
Fix: re-anchor on a signal that can fire.
Disposition: Open

### R6 · BLOCK · panel:impl · §9 1.1 / §10 rule 2
Finding: 1.1 requires `"every engine row reads ok and the process exits 0"`, but claude has no proof until
Slice 2 and rule 2 forbids exit 0 with an `unknown` engine — **Slice 1 can never exit 0**. Forward dependency
(slice N needs N+1).
Fix: scope rule 2 to *checked* engines; add a Slice-1 criterion asserting claude reads `unknown` and Slice 2
flips it.
Disposition: Open

### R7 · BLOCK · panel:impl + panel:premise · §12 profile scope
Finding: both proofs deliberately ignore inherited env (`codex.go:46-47`, `opencode.go:33-35`), so no test can
point them at a fixture home without a `configDir` input — criteria 1.2 and 2.1–2.4 are **untestable** until
profile scope is decided. `backend/usecase/profiles.go:34` already enumerates default + every `~/.claude-<name>`,
so one claude row can read `ok` while the profile a run actually pins is broken (a 5.4 false-green by construction).
Fix: decide profile scope in the PRD; make `EngineReport` per (engine, profile).
Disposition: Open

### R8–R19 · HIGH · panel (condensed — full text in the transcript)
- §5 5.3 baseline mis-attributed: measures *doctor's* coverage, but doctor does not exist → true baseline 0/3, not 2/3.
- §5 5.3 Method tautological — reads the feature's own output; cannot fail once slice 2 ships.
- §5 5.2 `< 5s` has no §9 criterion asserting duration, and is non-discriminating (both proofs are single file reads).
- §16 `↳ Breaks: none (additive)` is false: `preflight` also carries `LookPath` sentinel ordering (`spawner.go:154,172`) and an opencode-only prompt refusal (`spawner.go:165-167`); blast radius unstated.
- §10 rule 3 (`unknown` never `ok`) is mechanically unreachable for codex/opencode — `OpencodePluginProof` returns `ErrEngineNotProvisioned` for ANY read failure incl. EACCES (`opencode.go:38-41`).
- §10 rule 4 not testable and false for two live paths (claude never refuses; opencode can refuse via `ErrUnresolvableCommand`).
- §9 3.1 assumes every engine has fix text — only codex does (`codex.go:72-74`); opencode's error carries none (`opencode.go:58`), claude has neither.
- §9 3.2 cross-language single-sourcing is unstated horizontal work — the installer already re-implements `pluginRegistered` in awk (`install-codex-skills.sh:43-51`).
- §1 binary-absent path has no criterion despite being a distinct sentinel.
- §3 J1 is a feature in a job costume; pre-commits to 3/3 coverage with 214/214 single-engine usage.
- §4 J3 is slice-orphaned (served by 5.3 only, no §8 slice).
- §7 Decision Log omits two existing readiness surfaces (`usecase/profiles.go`, `usecase/envstate.go` → `/api/profiles`, `/api/env-state`) and the cheapest option (`saki run --dry-run`).

### R20–R24 · MED/LOW · panel — citation defects
`journal.go:40`→`:58` · `src/index.ts:130-254`→`:127` · `spawner.go:176-178`→`:175-177` ·
§16 route cite `http.go:69-74` (unguarded run routes) → `:86-92` (the `OriginGuard`-wrapped reads doctor belongs with) ·
"3-branch script" is a 125-line script with ≥6 conditionals.

## Readiness (Definition of Ready)

| # | DoR | Verdict |
|---|---|---|
| 1 | Slice 1 startable now | ❌ R6 — Slice 1 cannot satisfy its own criterion 1.1 |
| 2 | No build-blocking open question | ❌ R7 — profile scope blocks every fixture-based criterion |
| 3 | Dependencies available | ✅ all in-repo, no new third-party dep |
| 4 | Bet accepted or validated | ❌ R1 — premise disconfirmed (0/214), not merely unvalidated |
| 5 | No open BLOCK/HIGH | ❌ 7 BLOCK, 12 HIGH |

**Readiness: NOT READY** — R1 (structural, not authorable away), R6, R7.

## Technical contract (§16) check & residual gaps

- §16: present · rows cited · CHANGE row present with a `↳ Breaks:` note — but the note is **wrong** (R11): `preflight` carries sentinel ordering and an opencode-only prompt refusal beyond the engine→proof map.
- Arch: the shared-mapping refactor's real blast radius → `/saki-builder:rplan`, *if* the premise survives.
- UI/UX: none — no user-visible screens.

## Unverifiable claims (grounding TODOs, not defects)

- §2 operator incidence of un-provisioned profiles — no operator population; the one observable machine reads 0.
- §2 spike is machine-local, n=1; no evidence the registry key shape is stable across claude versions.
- §5.2 "today's dispatch-a-run-and-read-the-error loop" has no measured duration.
- §6 "operators would stop trusting it" — behavioural prediction, untested.

## Recommendation

**DISCOVERY-FIRST.** Do not proceed to `/saki-builder:rplan`. The cheapest next step is not a better spec —
it is deciding whether the problem is real for this product:

1. The data says this repo's pipeline runs **claude only**. Confirm whether codex/opencode are actually
   intended runtimes in practice, or aspirational. If aspirational, doctor's codex/opencode coverage is
   speculative and slices 1 + 3 lose most of their value.
2. If the real pain is the **claude** silent no-op (§2's only concrete harm), the honest feature is far
   smaller than this PRD: a claude proof wired into `preflight` — which §11 currently forbids. That is a
   ~1-slice change, not a medium-appetite command.
3. Re-premise around a harm that has occurred, or accept the bet explicitly and record it.

---

# Round 2 — 2026-08-15

**Verdict:** REVISE · **Readiness:** NOT READY · **Round:** 2 · **Reconcile:** 11 fixed · 2 partial · 15 new

Round 1: 7 BLOCK / 12 HIGH → Round 2: 2 BLOCK / 8 HIGH / 8 MED-LOW. Blocker volume is **converging**
(7→2), but the recut introduced new structural defects and surfaced an appetite breach.

## New BLOCKs (introduced by the round-2 recut)

### R25 · BLOCK · §8 Slice 1 vs §16
Slice 1 ships the design §16 explicitly **rejects**. The shared engine→proof mapping arrives in slice 2,
so slice 1 necessarily ships "doctor owning its own switch" — which slice 2 then rips out. The recut
created throwaway work and leaves 5.4 unguarded in the walking skeleton.
Fix: fold the mapping extraction into slice 1, or state the throwaway explicitly.

### R26 · BLOCK · §9 2.3 vs §10 rule 4 · and §6
Criterion 2.3 ("doctor and preflight agree on **every** fixture") is unscoped, but slice 3 *guarantees*
disagreement for claude: 3.2 makes doctor report `failed` while 3.5 requires preflight to return `nil`.
§6 hangs the kill criterion on 2.3 — so the kill trigger is wired to a criterion a later slice
guarantees will fail. Fix: scope 2.3 to codex + opencode fixtures.

## Top new HIGHs

- **Slice 1 ships a 5.4 false green.** Neither proof does `LookPath`; binary-absence isn't a verdict
  until 2.2. Slice 1 reports `ok` for an engine preflight refuses with `ErrBinaryNotFound`.
- **1.2 depends on slice 2.** It needs a broken codex fixture, but `--profile` arrives in 2.4 and
  `CodexSkillsProof` deliberately ignores inherited `CODEX_HOME` (`codex.go:46-47`). R6 was fixed and a
  new forward dependency introduced.
- **Claude enablement needs a SECOND file — verified.** `installed_plugins.json` entries carry
  `scope/installPath/version/installedAt/gitCommitSha` and **no enablement**; `enabledPlugins` lives in
  `~/.claude/settings.json`. Criterion 3.3's "enabled install" is unanswerable from the spiked file, and
  slice 3's `Assumes:` doesn't scope the second read. An installed-but-disabled plugin reads `ok`.
- **`resolved version` is unpopulatable** for codex/opencode (`pluginRegistered` reads no version;
  `OpencodePluginProof` parses only `Plugin []string`), and no criterion touches the field.
- **3.3 vs §11 conflict:** the two id spellings carry *different* versions (@0.5.0 / @0.30.2), so
  "reports the resolved version" is non-deterministic under order-agnostic resolution.
- **4.2's string isn't runnable standalone:** the installer emits **two** lines (`marketplace add` then
  `plugin add`); `codex.go:72-74` embeds only the second. 4.1 asserts runnability; 4.2 would freeze the gap.

## MED

- **Exit 1 collides.** `EXIT.ERROR` is already the generic backend-failure code (`src/client.ts:61`), so
  "engines not ready" is indistinguishable from "the route errored" — defeating J3. No backend-down criterion.
- **"reported" is undefined** (coordinator-found and confirmed) — nothing forbids omitting a broken engine
  and exiting 0.
- 5.1/5.4 Methods are test-authoring instructions wearing a `query:` label; 5.2's share has no denominator.
- 2.1's three isolated faults prove each branch, not their precedence (needs a cumulative peel-off fixture).
- **Appetite breached.** Slice 1 alone = a new usecase port + 16→17-arg `NewHandler` + an OriginGuard route
  + a CLI command + `--json` + docs + an e2e 403 spec; slice 2 refactors load-bearing `preflight` with two
  live consumers. Not "a few days" — and the BLOCK fixes make slice 1 bigger still.

## Citation defects (round 2)

`spawn.go:83`→**`:85`** for `store.Delete` · `spawner.go:299` is in **`engineProfileEnv`** (`:290`), not
`buildSpawnEnv` · `buildengine.go:473`→**`:472`** · `ErrUnresolvableCommand` is `infra/opencode.go:23`,
not usecase.

## Recommendation

The correctness findings are all applicable. The **appetite** is not a correctness question — it is the
owner's budget call, and it changes the slicing, so it is asked before round 3 rather than assumed.

---

# Round 3 — 2026-08-15

**Verdict:** SHIP · **Readiness:** READY · **Round:** 3 · **Failure-surface:** 2/2

Owner chose **cut to MVP at medium**. Recut 4 → **2 slices**; claude coverage and opencode/claude
remediation deferred to **F4** and **F5** with objective triggers.

## Round-2 BLOCKs — how the recut resolved them

- **R25 (slice 1 ships the rejected design)** — DISSOLVED. The shared engine→proof mapping now lands
  *in slice 1, with doctor*, so doctor never ships over a private switch. §7 states this explicitly.
- **R26 (2.3 unscoped → kill criterion fires by design)** — DISSOLVED, not patched. With claude out of
  MVP scope there is no claude fixture to disagree on; §6 scopes the trigger to codex + opencode and
  says why.
- **Slice-1 false green (no LookPath)** and **1.2's forward dependency on `--profile`** — both fixed by
  the same move: `LookPath` and `--profile` threading are in slice 1, where its own criteria need them.

## Fixed this round

Rule 4 vs criterion 2.2 contradiction (doctor DOES report binary-absent; only the *prompt* refusal is
out of scope) · `"reported"` pinned as a fixed set with criterion 1.3 · exit-code collision addressed
(criterion 2.5: dead backend → `3`, with `--json` as the discriminator) · 5.3 share now carries a
denominator · criterion 1.4 is a cumulative peel-off that actually proves precedence · `resolved
version` dropped from `EngineReport` (unpopulatable for both reported engines) · vestigial
`profiles.go` reuse claim removed · `ErrUnresolvableCommand` listed at its real path (`infra`, not
`usecase`).

## Coordinator-caught in round 3 (defects in my own rewrite)

- TL;DR had drifted back to 4 sentences.
- §5 Methods were labelled `test:` — not one of the three permitted classes; rewritten as real `query:`
  forms (a before/after file-count delta; a count over the fixture set).
- **🔒 rule 1's failure-path criterion had been reintroduced as a dangling reference** — it cited 2.3,
  which asserts the fix string, not read-only-ness. Folded the byte-identical assertion into 1.5.
  This is the *same* defect caught in round 1, reintroduced by the rewrite.

## Correction to the round-2 reconcile agent

It reported `buildengine.go:472` as the `ErrBinaryNotFound` guard. **It is at `:473`** (`:472` is the
enclosing `rearmOrPark` signature) — the original citation was right and was "corrected" to a wrong
value on the agent's word. Restored, and both lines are now cited. Verified by grep.

## Readiness (Definition of Ready)

| # | DoR | Verdict |
|---|---|---|
| 1 | Slice 1 startable now | ✅ walking skeleton, no forward dependency — mapping, LookPath and `--profile` all in-slice |
| 2 | No build-blocking open question | ✅ both §12 questions resolved in-PRD |
| 3 | Dependencies available | ✅ all in-repo, no new third-party dep |
| 4 | Bet accepted or validated | ✅ load-bearing assumption `observed` + cited; no DISCOVERY-RISK banner |
| 5 | No open BLOCK/HIGH | ✅ round-2 BLOCKs dissolved by the recut; HIGHs applied or deferred to F4/F5 |

**Readiness: READY.**

## Technical contract (§16)

Present · every row cited (REUSE `path:line` / NEW) · the CHANGE row carries a real `↳ Breaks:` naming
both hidden behaviours and both live consumers · slice-coherent · stayed at shape altitude.
Residual gaps → `/saki-builder:rplan`: the extraction's concrete signature and the fixture-profile
harness. No UI layer.
