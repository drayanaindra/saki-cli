<!-- prd-blocking: 0 -->
<!-- slices: 2 -->
<!-- appetite: small -->
<!-- prd-locked: @drayanaindra · 2026-08-20 · ui:none -->

# PRD: `saki doctor` — Claude coverage

**Owner:** unassigned · **Status:** Locked · **Updated:** 2026-08-20 · **Appetite:** small — hours · **Item:** F4

## 1. TL;DR

Extend the existing read-only `saki doctor` provisioning check to report Claude alongside Codex and OpenCode. Claude is healthy only when the selected profile proves both that a `saki-builder` plugin is installed and that the same plugin is enabled; missing, malformed, or mismatched evidence remains a failed verdict and never becomes a false green.

This is a backend-only change. It does not install or repair Claude, change the CLI response contract, or implement Claude provisioning; F6 consumes this proof afterward.

## 2. Problem & Evidence

Today `saki doctor` reports Codex and OpenCode, while Claude is the default run engine and already has a profile selector in the spawn path. A Claude profile can therefore remain unverified until a run is attempted. The existing F2 contract deliberately deferred Claude because installation and enablement are stored separately and two supported plugin-ID spellings can carry different versions (`tasks/roadmap.md:27-31`; `tasks/prd-saki-doctor-verify-engine-provisioning-before-a-run.md`).

The current doctor list contains only Codex and OpenCode (`backend/usecase/doctor.go:10-14`), and the shared proof dispatcher returns no Claude proof (`backend/infra/doctor.go:195-212`). The CLI already renders arbitrary engine reports and returns `EXIT.ERROR` for any non-`ok` report (`src/commands/doctor.ts:6-43`), so the required change is server-side proof and engine enumeration, not a new CLI shape.

**Load-bearing assumption:** Claude's installed-plugin registry and enabled-plugin settings are readable from the selected profile and use stable JSON shapes — `assumed`

**Spike:** Repository scan of the current doctor, spawn, and F2/F6 contracts → the exact Claude file paths and JSON shapes are not yet implemented in this repository; implementation must validate the external file shapes with fixture tests and preserve failure on malformed input (source: `backend/infra/doctor.go:195-212`, `tasks/roadmap.md:27-31`).

This is an explicitly accepted compatibility bet for the small slice: unknown or changed shapes fail closed as `failed`; they never become `ok`.

## 3. Primary Job to be Done

**J1:** When I am about to dispatch a Claude run, I want `saki doctor` to verify the selected Claude profile before dispatch, so I can fix provisioning without discovering the problem through a no-op run.

## 4. Related Jobs

None.

## 5. Desired Outcomes / Success Metrics

| # | Outcome (Minimize/Maximize [metric] when [context]) | Target | Basis | Method | JTBD |
|---|---|---|---|---|---|
| 5.1 | Maximize the share of doctor checks that correctly classify Claude profile readiness | 100% of fixture cases with complete evidence are `ok`, and every missing/malformed/mismatched case is `failed` | aspirational | query: backend proof test results by fixture case | J1 |
| 5.2 | Minimize time from Claude profile setup failure to an actionable pre-dispatch diagnosis | One `saki doctor` invocation reports Claude with status and reason before any run spawn | aspirational | query: doctor response contains one Claude report before any run spawn | J1 |
| 5.3 | Minimize accidental writes during diagnosis | Zero profile writes or engine process launches during doctor checks | aspirational | query: test spies on filesystem writes and process spawning | J1 |
| 5.4 | Minimize false-green Claude readiness verdicts | Zero `ok` verdicts when either installation or enablement proof is absent, malformed, or selected from different plugin IDs | aspirational | query: negative proof fixtures | guards 5.1 |

## 6. Appetite & Kill Criteria

**Appetite:** small — hours, limited to two vertical slices and the existing doctor/proof path.

**Kill criteria:** Stop this PRD if the stable Claude profile file shapes cannot be established from real profile fixtures within the small appetite; do not replace proof with a run exit code or a heuristic. Stop if the implementation requires changing the CLI response shape or adding a second doctor implementation outside the shared backend proof path.

## 7. Solution Shape

Use the existing doctor architecture: add Claude to the fixed reported-engine set, implement one Claude profile-proof function that reads the selected profile's installed-plugin registry and enabled-plugin settings, and dispatch through the existing `EngineProfileProof` port used by doctor and preflight. Resolve plugin IDs in a pinned order, then require installation and enablement for the same selected ID/version before returning `ok`.

### Alternatives considered / Decision

- **Chosen: shared proof adapter plus fixture-driven Claude resolver.** Reuses the existing doctor/preflight boundary, keeps the CLI contract unchanged, and makes false-green cases testable.
- **Mirror Codex/OpenCode with a single file or loose skill scan:** rejected because F2 explicitly identifies Claude as a two-file installation-plus-enable proof; a one-file heuristic cannot prove enablement.
- **Probe Claude by spawning it and reading the exit code:** rejected because doctor is pre-dispatch and read-only; an engine can exit successfully while not resolving `/saki-builder:*` commands.

## 8. Vertical Slices

### Slice 1 — Resolve Claude installed-and-enabled proof

**Serves: J1 · 5.1**

Add a pure, testable Claude profile proof behind the existing infrastructure proof boundary. It reads the selected profile's installed-plugin registry and settings enablement map, resolves the two supported IDs in the documented precedence order, and returns a typed provisioning failure for missing, malformed, uninstalled, disabled, or mismatched evidence.

### Slice 2 — Report Claude through doctor without side effects

**Serves: J1 · 5.2**

Add Claude to the existing doctor engine set and wire the shared proof so `/api/doctor` and `saki doctor` report the Claude profile using the existing `EngineReport` shape and exit-code behavior, while tests prove doctor performs no install, repair, or spawn.

## 9. Acceptance Criteria per Slice

### Slice 1

- 1.1 [auto] Given a selected Claude profile with a supported plugin ID present in `installed_plugins.json` and the same ID enabled in `settings.json`, when the shared Claude proof runs, then it returns success and the proof test records the resolved ID/version. → 5.1
- 1.2 [auto] Given a profile where either file is missing, malformed, the plugin is absent, the plugin is disabled, or the installed and enabled IDs resolve to different supported spellings, when the proof runs, then it returns `ErrEngineNotProvisioned` (or the existing typed equivalent) and never returns success. → security
- 1.3 [auto] Given both supported plugin IDs exist with different versions, when the proof runs, then it selects the documented precedence ID consistently and the fixture test proves the lower-precedence version cannot win. → 5.1
- 1.4 [auto] Given an unrecognized plugin ID or unrelated enabled setting, when the proof runs, then it returns a provisioning failure without panicking or treating unrelated data as Claude readiness. → validation

### Slice 2

- 2.1 [auto] Given Codex, OpenCode, and Claude profiles are checked, when `GET /api/doctor` is called, then the JSON contains exactly one report for each engine and Claude's report uses the existing `engine`, `profile`, `status`, `reason`, and `fix` fields. → 5.2
- 2.2 [auto] Given Claude proof is missing or failed, when `saki doctor --json` runs against the backend response, then the command exits `1`, preserves the failed Claude report, and does not claim overall success. → 5.1
- 2.3 [auto] Given a doctor request for any profile, when the check completes, then filesystem-spy and process-spawn assertions show no install, repair, or engine launch occurred. → 5.3
- 2.4 [auto] Given a non-loopback request to the doctor endpoint, when the request is sent, then the existing origin guard rejects it and no profile files are read or modified. → privacy

## 10. Business Rules & Invariants

1. **Claude readiness requires a two-part proof:** an installed registry entry and an enabled setting must identify the same selected supported plugin ID. Linked criteria: 1.1, 1.2, 1.3.
2. **Plugin-ID precedence is deterministic:** when both supported IDs are present, the resolver uses the documented precedence order and does not select by map iteration order or version comparison. Linked criterion: 1.3.
3. **Doctor is read-only and pre-dispatch:** it may read profile files and return a diagnosis, but it must not install plugins, edit settings, repair files, spawn an engine, or infer readiness from a prior run. Linked criteria: 2.2, 2.3.
4. **A failed reported engine makes doctor fail:** the existing CLI contract remains `EXIT.OK` only when every reported engine is `ok`; any Claude failure returns `EXIT.ERROR`. Linked criterion: 2.2.
5. **The endpoint remains loopback protected:** Claude proof must be reachable only through the existing origin guard and cannot widen backend exposure. Linked criterion: 2.4.

## 11. Non-Goals

- Claude plugin installation, repair, migration, or profile scaffolding; F6 provisioning remains a separate follow-up.
- Changes to the `DoctorResult` / `EngineReport` JSON shape, CLI flags, exit-code numbers, or human table formatting.
- Replacing proof with a Claude process invocation, run exit code, network request, or loose skill-file heuristic.
- Supporting arbitrary third-party Claude plugins or silently accepting unknown plugin-ID spellings.
- Changing loopback binding, origin protection, engine environment scrubbing, or run supervision.

## 12. Rabbit Holes & Open Questions

- **Before Slice 1:** Confirm the real Claude profile fixture shapes for `installed_plugins.json` and `settings.json`; if the files are JSONC or versioned by installation, preserve the smallest parser needed and keep malformed input failed.
- **Before Slice 1:** Confirm the exact precedence order for `saketek@saki-builder` versus `saki-builder@saketek`; this PRD chooses `saketek@saki-builder` first because it is the canonical registry spelling in the roadmap seed, with the alternate spelling as fallback.
- Do not expand this item into Claude provisioning or plugin-version migration if the fixture requires a broader compatibility layer.

## 13. Technical Constraints

- Preserve the backend Stage 3 boundary: domain remains dependency-free; filesystem and JSON parsing stay in `backend/infra`; doctor orchestration remains in `backend/usecase`.
- Reuse the existing `DoctorService`, `EngineProofChecker`, `EngineProfileProof`, `EngineReport`, `DoctorResult`, and `/api/doctor` route rather than creating parallel paths.
- Use real profile fixtures for proof semantics; fake engine binaries cannot establish Claude command resolution.
- Do not print or persist credentials found in Claude profile files.

## 14. Dependencies

- F2 doctor and proof contract is shipped.
- F6 Claude provisioning is intentionally waiting on this proof and must not be marked complete until its adapter agrees with doctor.

## 16. Technical Contract (thin)

**Entities (data):**

| Entity | Reuse / Change / New | Evidence (`path:line`) or note | Serves |
|---|---|---|---|
| Claude profile proof result | NEW | A shared infrastructure proof outcome for installed-and-enabled Claude readiness | 8.1 · 5.1 |

**Endpoints (API):**

| Method + path — purpose | Reuse / Change / New | Evidence or note | Serves |
|---|---|---|---|
| GET `/api/doctor` — report pre-dispatch readiness for all supported engines | CHANGE | `backend/adapter/http.go:100-104`; `↳ Breaks: existing CLI consumers depend on the current report fields and exit-code interpretation; change is additive by adding the missing engine report` | 8.2 · 5.2 |

**Architecture decision (one, load-bearing):**

- Extend the existing shared proof dispatcher and `DoctorEngines` set rather than adding CLI-local Claude logic — reused at `backend/infra/doctor.go:195-212` and `backend/usecase/doctor.go:10-14`; this keeps doctor and preflight on one proof path. Serves 8.1/8.2 · 5.1/5.2. Reject a separate CLI proof because it would drift from the backend path and could report readiness the spawner cannot use.

**Proof contract:** The installed registry supplies the selected plugin identity and version; `settings.json` supplies enablement for that selected identity. Enablement is a boolean/configuration fact, not a second version source. If the registry has both supported spellings, precedence is `saketek@saki-builder` then `saki-builder@saketek`; the chosen installed identity must be the enabled identity. Missing, malformed, or ambiguous evidence fails closed.

**Residual handoff:** Exact Claude file locations, registry nesting, and settings representation remain implementation questions for `/saki-builder:rplan`; Slice 1 is startable with fixture cases because its required behavior is fail-closed for unknown shapes.

**Compatibility:** `EngineReport`/`DoctorResult` and the `/api/doctor` path stay additive; existing Codex/OpenCode reports and CLI exit values remain unchanged. The changed doctor engine set is consumed by the existing CLI renderer and by preflight through the shared proof dispatcher.

**No schema migration:** This feature reads existing local profile files and adds no database or journal data.

**No UI:** No user-visible screens; this is a backend/CLI change.

**Decision boundary:** If real fixtures show that enablement cannot be joined to an installed identity without a broader migration or version-resolution policy, stop this PRD under the kill criterion rather than widening the slice.
**Compatibility:**

- `EngineReport` and `DoctorResult` remain shape-compatible; consumers must tolerate the existing additive Claude report through the fixed engine set.
- `EXIT.OK` and `EXIT.ERROR` remain unchanged.
- The selected profile path continues to be the same path used by engine spawning; the proof must not inspect an unrelated global profile.
