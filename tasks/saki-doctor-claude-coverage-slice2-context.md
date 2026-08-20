# Context: F4 Slice 2 — report Claude through doctor

**Source:** `tasks/prd-saki-doctor-claude-coverage.md` §8 Slice 2, §9, §10, §16
**Prior shipped slice:** `tasks/saki-doctor-claude-coverage-slice1-plan.md`
**Date:** 2026-08-20

## Scope

Slice 2 adds Claude to the fixed doctor report set. It does not change the `/api/doctor` route, `EngineReport`/`DoctorResult` JSON shape, CLI flags, exit-code values, profile proof semantics, provisioning, repair, settings writes, or engine spawning.

## Verified anchors and shipped shape

- `backend/usecase/doctor.go:5-8` — `DoctorEngines` is the fixed ordered report set, currently Codex then OpenCode.
- `backend/usecase/doctor.go:27-69` — `DoctorService.Check` runs `BinaryCheck` then `ProfileProof` for each fixed engine and returns existing `domain.EngineReport` values.
- `backend/infra/doctor.go:5-16` — `EngineProofChecker` delegates to `EngineBinaryCheck` and `EngineProfileProof`.
- `backend/infra/spawner.go:194-212` — `EngineProfileProof` now dispatches Claude to `ClaudeProfileProof`; Slice 1 is the shared proof used by preflight and doctor.
- `backend/infra/claude.go:35-85` — Claude proof reads only the selected profile's `plugins/installed_plugins.json` and `settings.json`, returning an error-only provisioning result.
- `backend/adapter/http.go:240-248` — `doctorHandler` maps `?profile` to `configDir`, calls `h.doctor.Check`, and returns HTTP 200 with `{engines:[...]}`.
- `backend/adapter/http.go:100-104` — `/api/doctor` is already wrapped in `OriginGuard`.
- `backend/adapter/doctor_http_test.go:32-118` — existing HTTP route, production wiring, profile-threading, and non-loopback tests.
- `backend/infra/doctor_test.go:32-143` — existing usecase behavior tests; hardcoded two-engine counts and indexes must be updated for the additive third report.
- `backend/infra/doctor_agreement_test.go:20-100` — preflight/doctor agreement fixture; its doctor lookup already searches by engine and remains valid after enumeration.
- `src/commands/doctor.ts:18-43` — existing CLI renders arbitrary reports and returns `EXIT.ERROR` unless the non-empty report set is entirely `ok`; no implementation change is needed.
- `src/commands/doctor.test.ts:53-142` — existing CLI tests use two-report fixtures; the Slice 2 regression adds a failed Claude report and preserves the existing `EXIT.ERROR` assertion.
- `src/types.ts:101-114` — `EngineReport` and `DoctorResult` already accept arbitrary engine names and retain exactly five fields.
- `src/exit.ts:8-18` — `EXIT.ERROR` is frozen at `1` and must not change.
- `backend/cmd/server/main.go:110-123` — production wiring already injects `infra.EngineProofChecker` into `usecase.NewDoctorService`.
- `docs/project-context.md:27-37` — loopback-only, read-only doctor, shared proof, and domain layering invariants.

## Required behavior

1. `DoctorEngines` contains exactly `domain.EngineCodex`, `domain.EngineOpencode`, and `domain.EngineClaude`, in the existing order with Claude appended.
2. Every report retains exactly `engine`, `profile`, `status`, `reason`, and `fix` fields; no selected Claude plugin ID/version is exposed.
3. A failed Claude proof produces one `engine:"claude"`, `status:"failed"` report with the existing diagnostic reason and no new remediation text (F5 remains separate).
4. `saki doctor --json` keeps the existing `EXIT.ERROR == 1` behavior when any report is failed; the CLI implementation and exit constants remain unchanged.
5. Doctor invokes only `BinaryCheck` and `ProfileProof`; it never installs, repairs, writes profile files, or spawns an engine.
6. The existing `/api/doctor` OriginGuard rejects non-loopback Host/Origin requests before the doctor service is called.

## Test strategy

- Usecase tests assert exactly three reports, deterministic order, five populated keys/fields, Claude failure propagation, and exactly one binary/profile proof call per reported engine.
- Adapter tests assert the HTTP body contains exactly three reports and one report for each engine, `?profile` reaches all proof calls, the production constructor still returns a clean three-report response, and non-loopback requests return 403 without invoking proofs.
- CLI unit tests assert a failed Claude report returns `EXIT.ERROR` and preserves the report in JSON output. No CLI source change is required.
- Existing Slice 1 real filesystem tests remain the proof-semantic authority; Slice 2 tests do not use fake binaries to claim Claude command resolution.

## Non-goals

- No Claude installer, profile mutation, repair, migration, version comparison, or F5 fix text.
- No new route or response type.
- No change to loopback binding, OriginGuard, engine environment scrubbing, preflight, or spawn supervision.
- No profile contents, credentials, selected plugin IDs, or versions in doctor API output.

## Graph/persona checks

`graphify-out/GRAPH_REPORT.md` is absent; this slice touches fewer than 20 targeted files. No `.claude/personas/` files were found. Cross-process coupling is documented in `docs/project-context.md` and remains unchanged.
