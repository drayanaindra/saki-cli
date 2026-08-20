# Context: F4 Slice 1 — Claude installed-and-enabled proof

**Source:** `tasks/prd-saki-doctor-claude-coverage.md` §8 Slice 1, §10, §12, §16
**Date:** 2026-08-20

## Scope

Slice 1 adds the shared infrastructure proof for a selected Claude profile. It does not add Claude to `DoctorEngines`, alter the doctor JSON shape, add remediation text, provision files, or add a UI.

## Verified anchors

- `backend/infra/spawner.go:145-173` — `preflight` calls `EngineBinaryCheck`, the opencode-only prompt check, then `EngineProfileProof`.
- `backend/infra/spawner.go:195-212` — `EngineProfileProof` dispatches Codex/OpenCode and currently returns `nil` for Claude.
- `backend/infra/doctor.go:10-17` — `EngineProofChecker.ProfileProof` delegates to `EngineProfileProof`; doctor and preflight share this boundary.
- `backend/usecase/spawn.go:22-27` — `ErrEngineNotProvisioned` is the existing typed sentinel.
- `backend/infra/opencode.go:29-58` and `backend/infra/codex.go:43-78` — existing proof style reads the child-visible profile and wraps engine-specific diagnostics in `usecase.ErrEngineNotProvisioned`.
- `backend/infra/spawner.go:214-329` — a pinned Claude profile is selected with `CLAUDE_CONFIG_DIR=<configDir>`; an unpinned child uses the default Claude profile after inherited selector removal.
- `backend/infra/doctor_test.go:47-71` — delegation tests compare shared proof results.
- `backend/infra/doctor_agreement_test.go:20-101` — preflight/doctor agreement fixture pattern; Claude is not yet in the fixed doctor set, so Slice 1 tests direct preflight/shared-proof agreement only.
- `backend/adapter/http.go:100-104` — `/api/doctor` route is already origin guarded; Slice 1 does not change it.
- `docs/project-context.md:27-37` — loopback-only, spawn refusal, env scrubbing, and domain dependency invariants.

## External fixture evidence

`tasks/prd-saki-doctor-verify-engine-provisioning-before-a-run-review.md:191-200` records the 2026-08-15 machine-local probe: `installed_plugins.json` has a `plugins` registry keyed by plugin identity; entries carry `scope`, `installPath`, `version`, `installedAt`, `lastUpdated`, and `gitCommitSha`, but no enablement. `settings.json` carries `enabledPlugins`. The two supported IDs were observed with different versions, so precedence must be explicit.

The implementation will use the child-visible pinned paths:

- `<configDir>/plugins/installed_plugins.json`
- `<configDir>/settings.json`

For the default profile it will use:

- `$HOME/.claude/plugins/installed_plugins.json`
- `$HOME/.claude/settings.json`

The parser will accept the observed registry/settings nesting: `installed_plugins.json` has `version` plus a `plugins` map whose supported-ID values are non-empty arrays of installation records; `settings.json` has an `enabledPlugins` map of booleans. It will tolerate unrelated metadata needed by the real registry and fail closed for missing files, malformed JSON, wrong required types, empty installation arrays, unsupported nesting, absent supported IDs, or missing/false enablement. Settings enablement is a boolean fact only; it is not a second version source.

## Graph/persona checks

`graphify-out/GRAPH_REPORT.md` is absent; this slice is under 20 files and targeted code reads are sufficient. No `.claude/personas/` files were found. The cross-process coupling that graph analysis would miss is documented in `docs/project-context.md` and is preserved: proof reads the same profile selected by the spawner.

## Required behavior

1. Select the first installed supported ID in this exact order: `saketek@saki-builder`, then `saki-builder@saketek`.
2. Do not fall back to the lower-precedence ID when the selected higher-precedence ID is disabled; deterministic selection is part of the proof.
3. Require `settings.enabledPlugins[selectedID] == true`.
4. Return `nil` only after both installation metadata and matching enablement are proven.
5. Wrap every failed proof in `usecase.ErrEngineNotProvisioned`; do not print profile contents, credentials, or full registry data.
6. Keep `EngineProfileProof` as the only dispatcher used by both preflight and doctor.

## Out of scope

- Adding Claude to `usecase.DoctorEngines` (Slice 2).
- Doctor route/CLI changes, exit-code changes, remediation text, or response-shape changes.
- Claude installation, settings writes, migration, version comparison, process invocation, or command-resolution heuristics.
