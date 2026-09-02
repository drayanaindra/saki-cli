# Context: OpenCode proto target forwarding

## Evidence

- `saki run proto F3 --engine opencode --json` creates a run, but the run output is `Provide a target: /proto E<n>...` and exits 0.
- CLI builds `/saki-builder:proto F3` in `src/commands/run.ts:84-86` and passes `engine` in `src/commands/run.ts:196-213`.
- `backend/domain/slashcmd.go:48-53` splits this into command `proto` and remainder `F3`.
- `backend/infra/spawner.go:75-78` invokes `opencode run ... --command "$SAKI_CMD" -- "$SAKI_PROMPT"`.
- Existing real-spawner regression coverage in `backend/infra/spawner_test.go:221-261` verifies the expected argv for `build`, but no proto-specific target execution assertion exists.

## Scope

Backend-only adapter regression: preserve the existing OpenCode command invocation shape while ensuring `proto` receives its target argument and does not fall back to the skill's missing-target prompt. No profile, provisioning, CLI syntax, or proto gallery behavior changes.

## Graphify Findings

No graph report present; scope is limited to the CLI-to-Go spawner seam and existing tests.
