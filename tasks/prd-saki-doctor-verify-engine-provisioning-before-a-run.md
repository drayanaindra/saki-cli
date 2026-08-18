<!-- prd-blocking: 0 -->
<!-- slices: 2 -->
<!-- appetite: medium -->
<!-- revision-passes: 3 -->
<!-- prd-locked: @drayanaindra · 2026-08-15 · ui:none -->

# PRD: `saki doctor` — verify engine provisioning before a run

**Owner:** unassigned · **Status:** Locked · **Updated:** 2026-08-15 · **Appetite:** medium — a few days · **Item:** F2

## 1. TL;DR

`saki doctor` answers one question before any work is dispatched: **can an engine actually run a
saki-builder command?** It probes codex and opencode for a binary on PATH and a profile that resolves
the `/saki-builder:*` commands, reporting a per-(engine, profile) verdict with the exact fix — and
exits 0 only when every reported engine is ready, so it doubles as a pre-build gate. Today those
checks exist but fire only **after** a run is dispatched; this moves the diagnosis left, with claude
coverage and opencode remediation text deliberately deferred (§11).

## 2. Problem & Evidence

Multi-engine support is a **stated product commitment**: `README.md:31` requires "an engine on PATH:
`claude`, `codex`, or `opencode`", `package.json:5` describes the CLI as running "on claude, codex or
opencode", both non-claude engines carry **real-binary** e2e specs (`e2e/codex-spawn.spec.ts`,
`e2e/opencode-spawn.spec.ts`), and each has dedicated infra (`backend/infra/codex.go`,
`backend/infra/opencode.go`).

The failure this guards is **silent by construction**. From the README: *"Without the skills
installed, a run still exits 0 while the model simply answers that it cannot find the command — so
the backend refuses such a spawn up front rather than parking a build that never started."* The
codebase already invests against it: two proofs (`backend/infra/codex.go:60`,
`backend/infra/opencode.go:36`), a sentinel carrying the *reason* (`backend/usecase/spawn.go:27`), and
a 125-line install/diagnose script (`scripts/install-codex-skills.sh`).

**The gap this PRD closes: the checks fire too late.** `preflight` (`backend/infra/spawner.go:149`)
runs at spawn time — after a run is dispatched, and for `/saki-builder:build` after a build has
parked. There is no way to ask "am I ready?" without spending a run to find out.

**Load-bearing assumption:** provisioning failures are **silent and expensive when they occur** — a
run exits 0 having done nothing, and a `build` parks — `observed` (the README quote above; the
sentinel's comment at `spawn.go:25-26`: *"the whole point of these proofs is that the operator learns
why a run was refused"*; `codex.go:72-74` embeds remediation text precisely because the failure is
otherwise opaque).

**Frequency is NOT claimed and NOT measurable today** — a deliberate scoping statement, not an
omission. A provisioning refusal **cannot** be counted from the journals: `domain.Run` has no error
field (`backend/domain/run.go:85-114`) and a refused spawn is removed from the store
(`s.store.Delete(id)`, `backend/usecase/spawn.go:85`). Any "count past refusals" query returns 0 by
construction, so it is evidence of nothing. This PRD rests on the failure's *cost*, which is
observable, not its *rate*, which is not. Adding a journal error field would make rate measurable and
is out of scope (§11).

**Spike:** is a claude proof mechanically buildable? → **Yes, but it needs TWO files, not one.**
`~/.claude/plugins/installed_plugins.json` carries `installPath`/`version` per plugin id, but **no
enablement** — its entry keys are `scope, installPath, version, installedAt, lastUpdated,
gitCommitSha`. Enablement lives separately in `~/.claude/settings.json` → `enabledPlugins`. The
registry also held **two** id spellings with **different versions** (`saketek@saki-builder` @0.5.0,
`saki-builder@saketek` @0.30.2), so resolution order must be pinned. This is why claude coverage is
deferred to its own item (§11) rather than folded in here — it is a two-file read with an enablement
join and an ordering rule, not a mirror of the existing proofs.
(source: probe of both files, 2026-08-15 — machine-local, n=1)

## 3. Primary Job to be Done

**J1** — When I am about to dispatch unattended agent work, I want to find out my engine setup is
broken *without* spending a run to discover it, so a build never parks on a problem I could have seen
in advance.

## 4. Related Jobs

**J2** — When I am provisioning a new machine, I want the diagnosis to tell me the exact commands that
fix it, so I do not have to read the spawn source to find out.

**J3** — When I am an agent or CI job about to call `saki build`, I want a machine-readable verdict and
a branchable exit code, so I can refuse fast instead of parking a run.

## 5. Desired Outcomes / Success Metrics

| # | Outcome (Minimize/Maximize [metric] when [context]) | Target | Basis | Method | JTBD |
|---|---|---|---|---|---|
| 5.1 | Minimize **dispatches required to learn an engine is unready** | 1+ → 0 | `baseline 1+` — source: the only path to the verdict today is `preflight` inside `Spawn()` (`spawner.go:149`, called at `usecase/spawn.go:83`), so at least one dispatch is required | `query: before/after file count of $SAKI_RUNS_DIR/go across a doctor invocation — expect delta 0 (the dir is env-overridable, journal.go:47-59, so this runs against a scratch dir)` — criterion 1.5 | J1 |
| 5.2 | Maximize engines with a **pre-dispatch** provisioning verdict | 0/3 → **2/3** (MVP scope; 3/3 when claude ships — §11) | `baseline 0/3` — source: no pre-dispatch verdict exists for any engine today; `preflight` covers 2/3 but only *at* spawn (`spawner.go:154-178`), claude not at all (`:179-180`) | `query: count engines in doctor's --json output whose status != "unknown"` | J1 · J3 |
| 5.3 | Maximize the share of `failed` codex verdicts carrying the installer's **complete** remediation | 0 → 1 of 1 | `baseline 0` — source: no verdict exists pre-dispatch today; `codex.go:72-74` embeds only the second of the installer's two lines (`install-codex-skills.sh:74-75`) | `query: count(codex failed && fix contains BOTH installer lines) / count(codex failed)` | J2 |
| 5.4 | **Counter-metric** — Minimize FALSE GREEN: doctor reports `ok` for a codex/opencode profile `preflight` would refuse. Guards 5.2: coverage chased by guessing instead of proving makes doctor confidently wrong, which is worse than silent | 0 | `baseline` — no verdict exists today, so no disagreement is possible yet; the risk is created by this feature | `query: over the codex/opencode fixture set, count(fixtures where doctor status=ok AND preflight returns ErrEngineNotProvisioned) — expect 0` — criterion 2.2 | guards 5.2 |

## 6. Appetite & Kill Criteria

**Appetite:** medium — a few days (2 slices, within the ≤4 band).

**Kill criteria:** stop if criterion **2.2** cannot be made to pass — doctor's verdict and
`preflight`'s provisioning result cannot be made to agree over the same codex/opencode fixture
profiles. That is the 5.4 false-green condition, checkable in-repo from slice 2, so it can actually
fire. Scoped to codex + opencode deliberately: claude is outside `preflight` (§11), so including it
would guarantee a disagreement by design and make the trigger fire for the wrong reason.

## 7. Solution Shape

**Chosen: extract the engine→proof mapping ONCE, and have both `preflight` and the new read-only
doctor route read it.** Doctor is a *surfacing* feature — the detection logic already exists and is
already trusted by the spawn path. Because both callers read the same mapping, the verdict and the
refusal cannot disagree (5.4). **The extraction lands in slice 1, with doctor** — shipping doctor over
its own private switch first would be the design this section rejects, and slice 2 would then rip it out.

### Alternatives considered / Decision

| Option | Why not |
|---|---|
| **Doctor owns its own engine→proof switch** | Cheaper, but two mappings drift, and the drift *is* the 5.4 false green. Rejected — which is why the extraction is in slice 1, not later. |
| **Re-implement the checks in the TS CLI** | Two implementations of "is this profile provisioned" drift for the same reason, and it strands codex TOML parsing in a second language. |
| **Extend `saki status`** | `status` answers "are the servers reachable" — different question, different cadence. Overloading it makes its `--json` do two jobs and its exit code ambiguous. |
| **`saki run --dry-run`** — run today's `preflight` and exit | Cheapest option, genuinely close. Rejected because `preflight` is engine-*specific* (one `SpawnSpec`) and carries an opencode-only prompt refusal needing a prompt. It answers "would *this* run start", not "is my setup ready". |
| **Make `preflight` stricter** | Today's behaviour, and precisely the problem: it requires dispatching work to learn you weren't ready. Does not serve J1. |

## 8. Vertical Slices

**Slice 1 — one shared engine→proof mapping, and `saki doctor` reports codex + opencode.**
The extraction and the command land together (§7). End-to-end vertical: CLI → OriginGuard-mounted
route → usecase service → the shared mapping → the existing proofs. Human-readable table plus
`--json`, `--profile <dir>`, the exit-code contract, and the no-spawn guarantee.
`Serves: J1 · 5.1`
`Assumes: preflight carries more than a proof map — LookPath sentinel ordering (spawner.go:154,172) and an opencode-ONLY prompt refusal (spawner.go:165-167); the extraction must preserve their precedence. Plus a DoctorService port threaded through NewHandler (already 16 args, backend/adapter/http.go:55-57) + main.go wiring, a src/index.ts COMMANDS entry (:127), and a docs/cli-reference.md entry`

**Slice 2 — verdicts you can trust: binary-absent, provable agreement, and complete remediation.**
Makes the verdict trustworthy rather than merely present: a missing binary reads differently from a
missing profile, doctor's verdict is *proven* to match what a spawn would do, and codex's fix is the
installer's complete two-line remediation.
`Serves: J2 · 5.4`
`Assumes: codex.go:72-74 embeds only the second of the installer's two lines (install-codex-skills.sh:74-75) — completing it changes the operator-facing spawn refusal text too, since the same string is what ErrEngineNotProvisioned carries`

## 9. Acceptance Criteria per Slice

**Slice 1**
- 1.1 [auto] Given codex and opencode are both provisioned, when I run `saki doctor`, then both rows
  read `ok` and the process exits `0`. → 5.2
- 1.2 [auto] Given `saki doctor --profile <dir>` where `<dir>` is a codex home with no saki-builder
  skills, when the command runs, then the codex row reads `failed` and the process exits **exactly `1`
  (`EXIT.ERROR`)**. → 5.2
- 1.3 [auto] Given any provisioning state, when I run `saki doctor --json`, then stdout parses as JSON
  containing **exactly** the reported set — `{codex, opencode}` — each entry carrying `engine`,
  `profile`, `status`, `reason`, `fix`. Later slices may add fields, never remove them. → 5.2
- 1.4 [auto] Given the shared mapping is extracted, when `preflight` runs for opencode with all three
  faults present (binary off PATH, prompt `/build, then do X`, plugin-less profile), then it returns
  `ErrBinaryNotFound`; with PATH restored, `ErrUnresolvableCommand`; with the prompt fixed,
  `ErrEngineNotProvisioned` — the peel-off proving precedence, not just each branch. → guardrail: error-path
- 1.5 [auto] Given a **failing** codex/opencode profile, when `saki doctor` completes, then **no file
  is added under `$SAKI_RUNS_DIR/go`, no engine process was started, and the probed profile's files
  are byte-identical to before the run** — doctor printed the fix, it did not apply it. → 5.1

**Slice 2**
- 2.1 [auto] Given `codex` is not on the backend's PATH, when I run `saki doctor --json`, then the
  codex row reads `failed` with a reason naming the **missing binary**, distinct from the
  un-provisioned-profile reason. → 5.2
- 2.2 [auto] Given codex/opencode fixture profiles spanning provisioned and un-provisioned, when
  doctor's verdict and `preflight`'s provisioning result are compared for each, then they agree on
  every fixture. → 5.4
- 2.3 [auto] Given codex reports `failed`, when its `fix` is compared with
  `scripts/install-codex-skills.sh`, then it contains **both** lines — `codex plugin marketplace add
  https://github.com/drayanaindra/saki-builder.git` and `codex plugin add saki-builder@saketek` — and
  the test fails if either drifts from the script. → 5.3
- 2.4 [auto] Given the backend is running, when `GET /api/doctor` is sent with a non-loopback `Host`
  header, then it is rejected `403` by `OriginGuard` (doctor discloses local profile paths).
  → guardrail: security
- 2.5 [auto] Given the backend is not running, when I run `saki doctor`, then it exits **`3`
  (`EXIT.UNREACHABLE`)** — never `1` — so "the route failed" is distinguishable from "engines not
  ready". → guardrail: error-path

## 10. Business Rules & Invariants

1. 🔒 **INVARIANT — doctor is strictly read-only.** It probes profiles and never installs, writes,
   repairs or mutates an engine profile, config file or plugin registry. *(Failure path: criterion
   **1.5** asserts a **failing** profile's files are byte-identical after the run — the case where
   "helpfully" applying the fix it just printed is most tempting.)*
2. 🔒 **INVARIANT — exit `0` if and only if every **reported** engine is `ok`.** **"Reported" is a
   fixed set, not a runtime choice: every engine for which this slice has a proof — exactly
   `{codex, opencode}` here, pinned by criterion 1.3.** An engine is omitted only when no proof exists
   for it yet (claude, §11); it is **never** omitted because its probe failed. A `failed` or `unknown`
   engine must never produce exit `0`. *(Failure path: criterion 1.2 asserts exit `1` on a broken
   profile; 2.5 asserts a dead backend is `3`, not a false `1`.)*
3. An engine doctor cannot verify reports **`unknown`**, never `ok`. In MVP scope this is
   near-unreachable: `OpencodePluginProof` returns `ErrEngineNotProvisioned` for *any* read failure
   including EACCES (`opencode.go:38-41`), so "unreadable" is indistinguishable from "absent" for both
   reported engines. The state exists for claude (§11) and for a future unreadable-vs-absent
   discriminator; adding that discriminator would change the sentinel surface the spawn path reads and
   is out of scope.
4. Doctor's **provisioning** verdict and `preflight`'s **provisioning** refusal are computed by the
   SAME function over the same `configDir`. Doctor additionally reports the **binary-absent** case
   (criterion 2.1); `preflight`'s **prompt** refusal (`ErrUnresolvableCommand`) is outside doctor's
   scope, since doctor has no prompt. *(Criteria 1.4, 2.2.)*
5. Doctor never spawns anything to reach its verdict — proofs read config, never inferring install
   state from a run exit code (the standing rule at `backend/infra/spawner.go:175-177`).
   *(Criterion 1.5.)*

## 11. Non-Goals

- ✗ **Claude coverage — deferred to its own roadmap item.** Not a mirror of the existing proofs: it
  needs `installed_plugins.json` **and** `settings.json` → `enabledPlugins` (the registry carries no
  enablement), plus a pinned resolution order for the two id spellings, which carry different versions
  (§2 spike). Folding it in here would breach the appetite.
- ✗ **Changing spawn behaviour for claude.** Adding a claude refusal to `preflight` could reject setups
  that work today — a riskier change with its own blast radius, and a separate decision.
- ✗ **opencode and claude remediation text — deferred.** Only codex has any today
  (`codex.go:72-74`); opencode's error carries none (`opencode.go:58`). Authoring the other two is its
  own item; slice 2 completes codex's because the text already exists and is merely truncated.
- ✗ **Reporting the resolved saki-builder version.** `pluginRegistered` reads a TOML table with no
  version (`codex.go:80-94`) and `OpencodePluginProof` parses only `Plugin []string`
  (`opencode.go:42-44`) — the field would be unpopulatable for both reported engines.
- ✗ **Installing or repairing anything.** Doctor diagnoses and prints the fix (Invariant 1).
- ✗ **Adding a journal error field to make refusal *rate* measurable.** Worth doing, but it is a
  run-journal schema change — Inv-1/Inv-2 territory — and not this feature (§2).
- ✗ **Checking engine authentication or quota.** A different question from "can it resolve the
  commands", needing live credentials, and it would put secrets on the diagnosis path.

## 12. Rabbit Holes & Open Questions

- **Rabbit hole — the extraction growing into a `preflight` rewrite.** Slice 1 extracts the *mapping*;
  the LookPath ordering and the opencode prompt refusal stay exactly where they are (criterion 1.4
  pins their precedence with a peel-off fixture).
- **Rabbit hole — reimplementing config parsing in TS.** The proofs are Go and already work. If a
  slice starts parsing `config.toml` in TypeScript, stop.
- **Resolved (was open) — exit code:** `1` (`EXIT.ERROR`) for "doctor ran, engines not ready"; `3`
  (`EXIT.UNREACHABLE`) when the backend is unreachable (criterion 2.5). `EXIT.ERROR` is also the
  generic backend-failure code (`src/client.ts:61`), so **`--json` is the discriminator** for an agent
  needing more than ready/not-ready. No new code; renumbering is forbidden.
- **Resolved (was open) — profile scope:** `EngineReport` is per **(engine, profile)**. Doctor probes
  the default profile and accepts `--profile <dir>`, threaded into the proofs' existing
  `configDir *string` — needed in slice 1 because both proofs deliberately ignore inherited env
  (`codex.go:46-47`, `opencode.go:33-35`), so criterion 1.2 is otherwise untestable.

## 13. Technical Constraints

- **The exit code is the API** (`src/exit.ts`). Doctor maps onto existing codes; renumbering is
  forbidden. Every command takes `--json`.
- **Backend is Stage 3 hexagonal.** The doctor service belongs in `usecase` behind a port, proofs stay
  in `infra`; an adapter calling `infra` directly is a layering break.
- **Loopback-only bind is a hard invariant** (`docs/project-context.md § Invariants`). Doctor's route
  mounts with the `OriginGuard`-wrapped reads (`backend/adapter/http.go:86-92`), not the unguarded
  run-vertical routes — it discloses local profile paths. Criterion 2.4.
- **A route absent from `docs/cli-reference.md` is unshipped** (CLAUDE.md checklist).

## 14. Dependencies

- Existing proofs: `backend/infra/codex.go:60`, `backend/infra/opencode.go:36`.
- Sentinels: `backend/usecase/spawn.go:20` (`ErrBinaryNotFound`), `:27` (`ErrEngineNotProvisioned`),
  and **`backend/infra/opencode.go:23` (`ErrUnresolvableCommand`)** — note this one lives in `infra`,
  not `usecase`; the slice-1 extraction must not drag it across the layer boundary.
- `scripts/install-codex-skills.sh:74-75` — the two-line remediation slice 2 completes against.
- No new third-party dependency; the repo has zero runtime dependencies.

## 15. Screens & UI Reference

No user-visible screens — CLI/backend only. The surface is terminal output: a per-(engine, profile)
verdict table and its `--json` equivalent.

## 16. Technical Contract (thin)

**Entities (data):**

| Entity | Reuse / Change / New | Evidence (`path:line`) or note | Serves |
|---|---|---|---|
| `RunEngine` (the enum doctor iterates) | REUSE | `backend/domain/run.go:17-22` | 8.1 · 5.2 |
| `EngineReport` (per (engine, profile): status ok/failed/unknown · reason · fix) | NEW | tri-state per rule 3; **no version field** (§11) | 8.1 · 5.2 |

**Endpoints (API):**

| Method + path — purpose | Reuse / Change / New | Evidence or note | Serves |
|---|---|---|---|
| `GET /api/doctor` — one provisioning verdict per (engine, profile) | NEW | mounts with the `OriginGuard`-wrapped reads at `backend/adapter/http.go:86-92` | 8.1 · 5.1 |
| `saki doctor` — CLI command rendering the above | NEW | new entry in the command table at `src/index.ts:127` | 8.1 · 5.3 |

**Architecture decision (one, load-bearing):**

- **Extract the engine→proof mapping into one shared helper that BOTH `preflight` and the doctor
  service read — in slice 1, together with doctor**, so doctor never ships over a private switch (§7).
  CHANGED component `backend/infra/spawner.go:149` (`preflight`).
  `↳ Breaks: preflight is NOT only a proof map — it also carries LookPath sentinel ordering (spawner.go:154,172; the build path parks on ErrBinaryNotFound) and an opencode-ONLY prompt refusal (spawner.go:165-167, ErrUnresolvableCommand). Consumers depending on that precedence: usecase/buildengine.go:473 (parks on ErrBinaryNotFound, no re-arm; inside rearmOrPark at :472) and adapter/http.go:503 (maps both sentinels to a status). The extraction must preserve refusal order — criterion 1.4 pins it with a cumulative peel-off fixture.`
  Serves 5.4. **Alternative rejected:** doctor owning its own switch — cheaper, but two mappings drift,
  and the drift is exactly the false green 5.4 guards.
