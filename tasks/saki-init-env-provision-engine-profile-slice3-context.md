# F6 slice 3 research context — Claude after F4

**Date:** 2026-08-20
**Source branch:** `feature/saki-init-env-provision-engine-profile` at `99d3112`
**Scope:** Claude provisioning only; no source implementation started.

## Verified shipped state

- F6 slice 2 is shipped in `99d3112`; `InitEnvService.Provision` accepts codex and opencode and rejects every other engine before binary lookup, profile locking, proof, or adapter invocation (`backend/usecase/initenv.go:67-99`).
- The Claude rejection currently returns HTTP 200 with `status:"failed"`, `changed:false`, and the reason `claude requires F4's installed + enabled plugin proof` (`backend/usecase/initenv.go:13-19,72-79`).
- `EngineProfileProof` has codex/opencode cases and returns `nil` in the default case; adding Claude to the doctor/spawn path before a real proof exists would create a false green (`backend/infra/spawner.go:195-211`).
- `DoctorEngines` is deliberately `{codex, opencode}`; its comment reserves future Claude support for F4 (`backend/usecase/doctor.go:5-8`).
- `StatusUnknown` exists for a future unverifiable engine, but it is an `EngineStatus` used by doctor reports (`backend/domain/doctor.go:3-12`), not an init-env result status.
- `InitEnvResult.status` is currently `'ok' | 'failed'` (`src/types.ts:116-123`), and the CLI maps all non-`ok` states to `EXIT.ERROR` (`src/commands/init-env.ts:34-41`).
- Claude profile environment plumbing already exists and is shared with spawn: pinned profiles use `CLAUDE_CONFIG_DIR`, while unpinned Claude removes inherited profile selection (`backend/infra/spawner.go:214-220,273-295`).
- No Claude provisioning argv, Claude fingerprint, or Claude profile proof exists in the committed F6 branch. The target branch search found no `installed_plugins.json`, `enabledPlugins`, or Claude proof adapter under `backend/`.

## F4 evidence

`tasks/roadmap.md:27-31` marks F4 Planned, with no child PRD. The F2 PRD records the required two-file proof: `installed_plugins.json` proves installation, while `settings.json` → `enabledPlugins` proves enablement, and two plugin-ID spellings require pinned resolution order (`tasks/prd-saki-doctor-verify-engine-provisioning-before-a-run-review.md:53-61`). Therefore F6 cannot honestly implement or claim the positive Claude path on this branch.

## Design decision

Use a distinct wire status `not_verified` for the pre-F4 Claude result. Do not reuse `failed` (ordinary setup/proof failure) or doctor’s `unknown` (doctor report vocabulary). The result remains HTTP 200, `changed:false`, `fix:""`, and exits `EXIT.ERROR`; it performs no binary check, lock, profile read/write, or adapter call. The positive adapter path remains F4-gated and must not receive invented installer argv or proof logic.

## Graphify

`graphify-out/GRAPH_REPORT.md` is absent. Fewer than 20 targeted files were required, so graph research was skipped. `docs/project-context.md` was used through the repository instructions: loopback-only, no credentials, fixed argv, and no journal mutation remain applicable.
