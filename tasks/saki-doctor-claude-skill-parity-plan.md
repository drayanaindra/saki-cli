# EXECUTION PLAN: `saki doctor` — bring claude's profile proof to skill-file parity with codex

**Item:** I3
**Date:** 2026-08-23
**Blocking items:** 0 (see Evidence Ledger)
**Risk Score:** LOW
**Unknown Count:** 0 / 2 max
**Behavior Spec:** N/A (backend-only — no UI, no new/changed endpoint a user hits; `saki doctor`'s JSON shape is unchanged, only the string content of an existing `reason` field can now differ for one engine)
**Source PRD:** N/A (standalone Plan-track item, seeded from roadmap `I3`)
**Prior slices:** N/A — standalone
**Appetite:** ~5 agent tasks (small, single-surface fix — TDD implementation step + 3 fixture-repair steps + 1 fingerprint-parity step)
**Kill-if:** N/A (Plan-track item; no §5/§6 PRD metric to inherit)

## Problem Statement

`saki doctor` and the spawn-preflight it shares proof functions with (rule 4, `backend/infra/spawner.go:149-156`)
are supposed to catch a saki-builder install that can't actually resolve its commands, so a run refuses loudly
instead of silently no-opping (`docs/project-context.md` Invariants: "A spawn is refused when the engine
profile cannot resolve the `saki-builder` commands"). For **codex**, that proof already goes one level deeper
than "is the plugin registered" — its loose-install fallback additionally stats a sentinel skill file,
`skills/build/SKILL.md` (`backend/infra/codex.go:25,63-66`, `codexProofSkill = "build"` — "the journey's
load-bearing command"). For **claude**, the proof (`backend/infra/claude.go:35-39` `ClaudeProfileProof`) never
does this — it only checks that `installed_plugins.json` lists a known plugin id with a non-empty version AND
that `settings.json` has it enabled. It never reads a single file the plugin is supposed to have installed. A
claude profile whose plugin cache is stale, half-written, or corrupted (installed + enabled, but missing the
skill directories underneath — e.g. `installPath` still points at a version whose files were partially removed
mid-update) currently reports `saki doctor` `ok`, then a real run silently fails when the model can't find the
invoked `/saki-builder:*` command (the exact failure class `ErrEngineNotProvisioned` exists to catch, just not
reached here).

`installed_plugins.json`'s plugin record already carries an `installPath` — confirmed against the real local
file, `/Users/saki/.claude/plugins/installed_plugins.json`:
```json
"saki-builder@saketek": [{"scope":"user","installPath":"/Users/saki/.claude/plugins/cache/saketek/saki-builder/0.30.2", ...}]
```
— and that path really does contain `skills/build/SKILL.md` (confirmed by reading it). The Go struct that
parses this file (`claudeInstalledPlugins`, `claude.go:26-29`) simply never captures the field.

## Scope decision (recorded, not re-litigated by the implementer)

The roadmap item's text also names "verify the SPECIFIC skill/command a run is about to invoke" and
"plugin-version verification". Investigating `preflight()` (`spawner.go:161`) shows the invoked command name
IS knowable there (`domain.SplitSlashCommand(spec.Prompt)`), so a per-invocation check is technically
possible for preflight. It is **deliberately out of scope for this plan**: `saki doctor` has no in-flight
prompt to derive a command from, and `TestDoctorPreflightAgreement`
(`backend/infra/doctor_agreement_test.go`) enforces, by design ("rule 4" — comments at `doctor.go:5-7`,
`spawner.go:152-156`), that preflight and doctor can never disagree on the same fixture. Making preflight
check a *dynamic* per-invocation command while doctor keeps a *static* general check would make that
invariant meaningless (the two would legitimately diverge on real slash-command spawns, just outside this
test's only non-slash fixture). Reconciling that — e.g. giving `saki doctor` an optional `--command` flag —
is a real, larger design change belonging to a future item, not a call this plan makes unilaterally.
**This plan instead closes the sharpest, safely-fixable gap: claude's proof does strictly less file-level
verification than codex's already-accepted design, for no stated reason.** It brings claude to the same
sentinel-file check codex already does — still a fixed, non-dynamic predicate, so it stays 100%
inside the existing rule-4 contract (same inputs -> same verdict, everywhere, always). Per-invoked-command
coverage and opencode coverage (its npm-resolved plugin path isn't exposed in `opencode.json` or anywhere
locally inspectable — confirmed by reading `backend/infra/opencode.go:60-116`, no per-skill signal exists)
remain explicit non-goals here.

---

## Concrete Example Output

**Before this fix** — a claude profile at `~/.claude` with `saketek@saki-builder` installed+enabled, but
`installPath` pointing at a directory whose `skills/` subtree got partially wiped by an interrupted update
(plugin metadata intact, skill files gone):
```
$ saki doctor --json
{"engines":[{"engine":"claude","profile":"default","status":"ok","reason":"","fix":""}, ...]}
```
`ok` — even though `/saki-builder:build` would immediately fail to resolve in a real run.

**After this fix**, same broken profile:
```
$ saki doctor --json
{"engines":[{"engine":"claude","profile":"default","status":"failed",
  "reason":"engine profile cannot resolve the saki-builder commands: claude profile does not carry the saki-builder skills: /Users/x/.claude/plugins/cache/saketek/saki-builder/0.30.2/skills/build/SKILL.md",
  "fix":"claude plugin marketplace add https://gitlab.com/drayanaindra/saki-builder.git --scope user\nclaude plugin install saki-builder@saketek --scope user"}, ...]}
```
`fix` is populated for free — `doctor.checkOne` (`usecase/doctor.go:60`) already sets
`report.Fix = engineInstallFix(engine)` on any `ProfileProof` failure, and `ClaudeInstallFix`
(`usecase/doctor.go:26`) already exists.

A genuinely-complete claude profile (the normal case — verified against the real local install above)
is unaffected: `status` stays `ok`.

---

## Steps

| # | Action | Files (exact paths) | Risk | Test | Committable? |
|---|--------|---------------------|------|------|-------------|
| 1 | Add failing tests for the new skill-file check: `TestClaudeProfileProof_SkillMissing` (installed+enabled, `installPath` set to a temp dir with NO `skills/build/SKILL.md` → `ClaudeProfileProof` returns an error wrapping both `usecase.ErrEngineNotProvisioned` and a new `ErrClaudeSkillMissing`) and `TestClaudeProfileProof_SkillPresent_Succeeds` (same, but the file exists → nil) | `backend/infra/claude_test.go` (new tests, appended after `TestResolveClaudeProfile_Precedence`) | LOW | *is* the test (Test-First / Red) | No — step 2 makes it Green |
| 2 | Implement the check: add `InstallPath string \`json:"installPath"\`` to the plugin-record struct inside `claudeInstalledPlugins` (`claude.go:26-29`) and to `claudePlugin` (`claude.go:20-23`); populate it in `resolveClaudeProfile`'s success return (`claude.go:71`, the `return claudePlugin{ID: id, Version: records[0].Version}, nil` line — add `InstallPath: records[0].InstallPath`); add `var ErrClaudeSkillMissing = errors.New("claude profile does not carry the saki-builder skills")` next to `ErrClaudePluginMissing` (`claude.go:14`); in `ClaudeProfileProof` (`claude.go:35-39`), after `resolveClaudeProfile` succeeds, `filepath.Join(plugin.InstallPath, "skills", codexProofSkill, "SKILL.md")` and `os.Stat` it — on failure return `fmt.Errorf("%w: %w: %s", usecase.ErrEngineNotProvisioned, ErrClaudeSkillMissing, skillPath)` (mirrors the existing wrap style at `claude.go:37`). `codexProofSkill` is already an unexported `const` in the same `infra` package (`codex.go:25`) — reused directly, no new export | `backend/infra/claude.go` | LOW | `TestClaudeProfileProof_SkillMissing`, `TestClaudeProfileProof_SkillPresent_Succeeds` (from step 1, now Green) | Yes |
| 3 | Repair `TestResolveClaudeProfile_ReadOnly` (`claude_test.go:177-200`) — create a temp `installPath` subdir, write `skills/build/SKILL.md` into it via a new `writeSkillFile` test helper, set `"installPath":"<that dir>"` in the JSON passed to `writeClaudeProfile`. — adjusted in impl: `TestEngineProfileProof_ClaudeMatchesDirectProof` (`claude_test.go:151-162`, originally listed here too) needed **no change** — it only asserts `(want == nil) == (got == nil)`, and both sides now return the SAME non-nil error for its no-installPath fixture, so the boolean equality still holds without any edit; confirmed by running the full suite before touching it (it never appeared in the failure list) | `backend/infra/claude_test.go` | LOW | `TestResolveClaudeProfile_ReadOnly`, re-run to confirm still Green; `TestEngineProfileProof_ClaudeMatchesDirectProof` confirmed still Green unmodified | Yes |
| 4 | Same repair for the shared spawner fixture `claudeProvenProfile` (`spawner_test.go:26-34`) — it backs every claude-engine spawn test (`TestShSpawner_SpawnsAndFinalizes`, and 3 others via `claudeSpawnSpec`, `spawner_test.go:50,73,375,519`); add the same `installPath` + real `skills/build/SKILL.md` under a temp dir it creates | `backend/infra/spawner_test.go` | LOW | existing suite (`TestShSpawner_SpawnsAndFinalizes` + the 3 other `claudeSpawnSpec` callers) | Yes |
| 5 | Extend `plugin.InstallPath` coverage into `TestResolveClaudeProfile_PresentCanonical` (`claude_test.go:27-44`) — its fixture already carries `"installPath":"/tmp/saki-builder"` (line 30) but the assertion never checks the field was actually parsed; add `if plugin.InstallPath != "/tmp/saki-builder" { t.Fatalf(...) }` right after the existing Version check (line 41-43) | `backend/infra/claude_test.go` | LOW | `TestResolveClaudeProfile_PresentCanonical` | Yes |
| 6 | Keep `profileFingerprint`'s stated contract true ("covers exactly the files each proof reads", `initenv.go:153-154`): in its `case domain.EngineClaude:` branch (`initenv.go:159-163`), after computing `installed, settings := claudeProfilePaths(profile)`, additionally call `resolveClaudeProfile(profile)` — on success and non-empty `InstallPath`, append `filepath.Join(plugin.InstallPath, "skills", codexProofSkill, "SKILL.md")` to `paths`; on failure, leave `paths` as today's 2 entries (matches the function's existing "missing file hashes to a placeholder" behavior — no new failure mode). Extend `TestClaudeProfileFingerprintCoversOnlyProofFiles` (`claude_test.go:176-204`) with a case mirroring its existing `installed`/`settings`-changed assertions: after setting a valid `installPath` + skill file in the fixture, write new content to the skill file and assert the fingerprint changes (same pattern as the existing "installed_plugins.json change did not change the fingerprint" check) | `backend/infra/initenv.go`, `backend/infra/claude_test.go` | LOW | `TestClaudeProfileFingerprintCoversOnlyProofFiles` (extended) | Yes |
| 7 | Full regression pass: `go vet ./...` (from `backend/`) and `npm run backend:test`, confirm the real-binary e2e `e2e/claude-init-env.spec.ts` still asserts `doctor` reports claude `ok` after a real `claude plugin install` (no code change needed there — it already exercises the real install, which genuinely lays down `skills/build/SKILL.md`, so it becomes a *stronger* regression guard for this change, not a broken one) | — (verification only) | LOW | `npm run backend:test`, `go vet ./...`, `npx playwright test e2e/claude-init-env.spec.ts` (manual — requires real `claude` CLI + network, not run by `backend:test`) | Yes |

> Steps 3-6 all repair/extend **existing** tests that assert claude-proof success — none of them touch a
> DIFFERENT engine's fixtures (codex/opencode paths are untouched by this plan).

---

## User Role Coverage

Not applicable in the customer/admin/merchant sense — `saki-cli` is a single-operator local tool
(`docs/cli-reference.md`: "This is a local, single-operator tool by design"). The only actor is:

| Role | Can Do | Cannot Do | Auth Guard | Entry Point |
|------|--------|-----------|------------|-------------|
| Operator (or an agent running `saki` on the operator's behalf) | Run `saki doctor` and get an accurate claude verdict; have a broken claude profile refused BEFORE a run silently no-ops | Nothing new is restricted — this only tightens a check that was already meant to fail closed | N/A (loopback-only backend, no session gate — `docs/cli-reference.md` §"⚠ DEV_MODE") | `saki doctor`, any `saki run`/`saki prd`/etc. spawn with `--engine claude` (or the default engine) |

---

## Plan Wiring

### Flow 1: `saki doctor` reports claude's verdict
```
saki doctor (src/commands/doctor.ts) → GET /api/doctor (backend/adapter/http.go)
  → usecase.DoctorService.Check (backend/usecase/doctor.go:43) → checkOne (doctor.go:52-63)
  → infra.EngineProofChecker.ProfileProof (backend/infra/doctor.go:14-16)
  → infra.EngineProfileProof(EngineClaude, configDir) (backend/infra/spawner.go:197,209)
  → infra.ClaudeProfileProof(configDir) (backend/infra/claude.go:35, THIS PLAN adds the skill-file stat here)
  → domain.EngineReport{Status, Reason, Fix} → JSON response
```

### Flow 2: a real spawn is refused before it can no-op
```
POST /api/run (backend/adapter/http.go) → usecase.RunService.Spawn (backend/usecase/spawn.go:58)
  → infra.ShSpawner.Spawn (backend/infra/spawner.go:116) → preflight(spec) (spawner.go:145)
  → infra.EngineProfileProof(EngineClaude, spec.ConfigDir) → infra.ClaudeProfileProof (same function as Flow 1)
  → error wraps usecase.ErrEngineNotProvisioned → spawn refused, run never starts (no silent no-op)
```

### Flow 3: `saki init-env --engine claude` provisioning gate + confirmation
```
saki init-env (src/commands/init-env.ts) → POST /api/init-env (backend/adapter/http.go)
  → usecase.InitEnvService.Provision (backend/usecase/initenv.go:84,89 — both ProfileProof calls)
  → infra.ClaudeProfileProof (same function) — now also gates on the skill file existing after install
  → infra.EngineProvisioner.Provision (backend/infra/initenv.go:33) uses profileFingerprint (initenv.go:155, THIS PLAN extends its claude branch) to compute `changed`
```

---

## Compatibility & Consumers

`ClaudeProfileProof`'s signature is unchanged — this plan changes its internal behavior only (it can now
fail closed in a case it previously passed). Every caller is internal to this repo (the function is
unexported-package-scoped to Go build consumers within `backend/`, not a public API or wire contract).

| Changed surface (exact) | Kind | Consumers found (`grep`) | Verdict | Mitigation / step |
|---|---|---|---|---|
| `infra.ClaudeProfileProof` behavior (still same func signature) | internal Go function behavior | `backend/infra/spawner.go:209` (`EngineProfileProof` dispatcher) | updated in step 2 — this IS the fix; both preflight and doctor pick up the stricter check identically (same dispatcher, no divergence) | step 2 |
| ↳ via dispatcher → `preflight()` real-spawn refusal | internal | `backend/infra/spawner.go:161` | intended — a stale claude profile now refuses the spawn loudly instead of the model silently answering "command not found" | step 2 (no separate mitigation needed — this is the invariant working as designed) |
| ↳ via dispatcher → `saki doctor` verdict | internal | `backend/usecase/doctor.go:58` (`checkOne`) | intended — doctor now correctly reports `failed` for a stale claude profile instead of a false `ok` | step 2 |
| ↳ via dispatcher → `saki init-env` provisioning gate/confirmation | internal | `backend/usecase/initenv.go:84,89` | intended — a stale profile is no longer treated as "already provisioned"; a real `claude plugin install` (which does create the skill file, confirmed against the live local install) still confirms `ok` afterward | step 2, verified by the existing real-binary e2e (step 7) |
| `profileFingerprint`'s documented contract ("covers exactly the files each proof reads", `initenv.go:153`) | internal invariant, no code consumer beyond itself | `backend/infra/initenv.go:50,61` (`before`/`after` diffing in `Provision`) | updated in step 6 — kept truthful now that the proof reads one more file | step 6 |
| `EngineReport` JSON shape (`domain.EngineReport`, `domain.go` fields `engine/profile/status/reason/fix`) | HTTP/CLI response schema | `src/commands/doctor.ts` (CLI consumer), `docs/cli-reference.md` (no claude-specific sample text found there — grepped, only a codex example exists) | unaffected — no field added/removed/renamed, only the runtime *value* of an existing free-text `reason` string can now differ for claude | — |

None of these are consumers outside this repo — `saki-cli`'s backend is not imported as a library and has
no other service depending on it (confirmed: `docs/project-context.md` Topology lists exactly two
deployables, `saki` and `saki-backend`, talking only over the documented HTTP/SSE/journal-file edges).

**Forward compatibility:** behavior-tightening on an internal function, no wire/schema change. A genuinely
complete claude install (the normal case) is unaffected — verified against this machine's real
`~/.claude/plugins/installed_plugins.json` + its real `skills/build/SKILL.md`. Only a profile that was
already broken in a way the old check couldn't see flips from a false `ok` to a correct `failed` — that is
the fix, not a regression to mitigate.

---

## Migration Checklist

None — no schema, no DB, no new persisted state. `installPath` is a field already present in
`installed_plugins.json` (Claude Code's own file, not something `saki-cli` writes); this plan only starts
reading a field it already ignored.

- [x] N/A — no migrations

---

## Branch Points (pre-declared)

- Step 2: If a real operator's `installed_plugins.json` predates `installPath` being written (an old Claude
  Code version) → `InstallPath` parses as `""` → the new `os.Stat` on `filepath.Join("", "skills", ...)`
  resolves to a relative path in the process's CWD, almost certainly missing → fails closed with
  `ErrClaudeSkillMissing`. This is the CORRECT reversible default (fail closed, matching every other proof
  in this codebase — `AUTO-RESOLVED: should a missing installPath fail open or closed → fail closed, consistent
  with ClaudeProfileProof/OpencodePluginProof/CodexSkillsProof all already failing closed on any unresolvable
  proof input`). Not expected to actually occur — every locally-observed `installed_plugins.json` (this
  machine's real file) already carries `installPath`, and it is Claude Code's own field, not user-editable.
- No PAUSE or BLOCKED branch points — this plan does not approach a `🔒 INVARIANT` or No-Go; it reinforces one.

---

## Unknowns (must be <= 2)

None. Every reference below is verified against real code or the real local `~/.claude` install
(see Evidence Ledger).

---

## No-Gos

- Will NOT change `EngineProfileProof`'s or `ProfileProof`'s function **signature** — no command-name
  parameter is threaded through; that would require also changing `usecase.EngineProofs` (the port),
  `infra.EngineProofChecker`, and both `usecase/initenv.go` call sites, and — per the Scope decision above
  — would risk breaking the rule-4 doctor/preflight agreement invariant. Out of scope for this plan.
- Will NOT add per-invoked-command verification to `saki doctor` or `preflight()`.
- Will NOT touch `OpencodePluginProof` or `CodexSkillsProof`'s plugin-registered branch — no locally
  inspectable per-skill signal exists for either (verified by reading both files; codex's marketplace
  plugin writes nothing under `<home>/skills/`, opencode's npm-resolved plugin path isn't recorded in
  `opencode.json`).
- Will NOT touch `codexProofSkill`'s value or codex's existing loose-install check — only reusing the
  same constant from `claude.go`, not modifying `codex.go`.
- Will NOT add a plugin-version floor/pin — the item's "plugin-version verification" phrase is satisfied
  indirectly (a version whose skill files are missing now fails), not by comparing against a minimum
  semver, which would need a source of truth for "the current saki-builder version" this repo does not
  have and should not hardcode (it would drift the moment saki-builder ships a release, recreating the
  exact staleness problem this plan fixes).

---

## Implementation Completeness Checklist

**User Coverage**
- [x] Single "operator" role — in the Role Coverage matrix
- [x] Full call chain in Plan Wiring (Flows 1-3)
- [x] N/A — no auth/permission surface (loopback-only tool, no session gate)
- [x] Edge case documented: missing/empty `installPath` (Branch Points)

**Database & Migrations**
- [x] N/A — no schema change

**API Layer**
- [x] N/A — no new/changed endpoint; `GET /api/doctor` response shape unchanged (Compatibility table)

**Service / Business Logic**
- [x] Every function modified named with file path (Steps 2, 6)
- [x] Error path documented: `ErrClaudeSkillMissing` wrapped in `usecase.ErrEngineNotProvisioned` (Step 2)

**Frontend**
- [x] N/A — backend-only, explicitly noted in header

**Compatibility & Consumers**
- [x] Table filled, every row has a verdict, no unmitigated `breaks` row
- [x] N/A — standalone plan, no prior slices

**Plan Wiring**
- [x] 3 flows, each end-to-end with exact file:line
- [x] No vague "update X" steps — every step names the exact function/struct/const

---

## Evidence Ledger

### Blocking (must be empty to present)

*(empty)*

### Advisory (visible, never gates)

| Step | Note | Evidence |
|------|------|----------|
| — | Per-invoked-command / opencode / codex-plugin-registered coverage remains unimplemented (documented non-goal, not a defect in this plan's own scope) | Scope decision section above; `backend/infra/opencode.go:60-116` (no per-skill signal); `backend/infra/codex.go:43-59` (plugin-registered branch writes nothing under `<home>/skills/`) |
| — | All anchors verified, all targets have anchor parents and creating steps, all checklist items on state-changing steps satisfied, no unknowns above LOW | self-audit |

**Blocking: 0 → READY.**

Anchor verification (§4a):
- `backend/infra/claude.go:14` `ErrClaudePluginMissing` — grep confirmed, exists (new `ErrClaudeSkillMissing` sits beside it, step 2, creating-step cited).
- `backend/infra/claude.go:20-23` `claudePlugin` struct, `:26-29` `claudeInstalledPlugins` struct, `:35-39` `ClaudeProfileProof`, `:71` `resolveClaudeProfile` return — all read verbatim above, line numbers current as of this research pass.
- `backend/infra/codex.go:25` `codexProofSkill = "build"` — grep confirmed, same package (`infra`), directly reusable.
- `backend/infra/initenv.go:153-163` `profileFingerprint` — read verbatim, claude branch confirmed to read only `installed`/`settings` today.
- `backend/infra/claude_test.go:27-204` — all 5 existing tests referenced (lines 27,46,88,126,151,164,176) read in full; the 3 requiring fixture repair (126,151,176) and the 1 requiring an added assertion (27) identified by which ones call `ClaudeProfileProof`/`EngineProfileProof` and expect success without `installPath` set to a real directory.
- `backend/infra/spawner_test.go:26-43` `claudeProvenProfile`/`claudeSpawnSpec` and its 4 call sites (`:50,73,375,519`) — grep confirmed exhaustive (`grep -n "claudeProvenProfile\|claudeSpawnSpec"`).
- `backend/infra/doctor_agreement_test.go:20-100` — read in full; confirmed it has NO claude case today (only codex/opencode), so this plan's change cannot newly break it (pre-existing gap, out of scope to extend here).
- `backend/usecase/initenv_test.go:117-133,296-320,410-427` — read; these exercise `EngineProvisioner.Provision` directly with a fake `claude` binary and never call `ClaudeProfileProof`, so unaffected.
- `backend/usecase/initenv_test.go:47` `stubProofs.ProfileProof` — confirmed usecase-level tests use a stub, fully decoupled from `infra.ClaudeProfileProof`'s internals.
- `e2e/claude-init-env.spec.ts:30-70` — read; real-binary e2e already asserts `doctor` reports claude `ok` after a real `claude plugin install`, which genuinely creates `skills/build/SKILL.md` (independently confirmed by reading the real local install path).
- `docs/cli-reference.md`, `docs/saki-cli-agent-guide.md` — grepped for claude-specific sample error text; only a codex example exists, so no doc text goes stale.
- `grep -rln "writeClaudeProfile\|installed_plugins.json\|ClaudeProfileProof\|EngineClaude"` across `backend/` and `e2e/` — full caller/consumer sweep, all 14 hits enumerated and dispositioned above (Compatibility table + Steps).

---

## Success Criteria

- [x] ✅ HARDENED — `go test ./backend/infra/... -run TestClaudeProfileProof` passes, including the 2 new tests from Step 1 (→ Steps 1-2)
- [x] ✅ HARDENED — `go test ./backend/infra/...` passes in full (no regression in `TestResolveClaudeProfile_*`, `TestEngineProfileProof_ClaudeMatchesDirectProof`, `TestClaudeProfileFingerprintCoversOnlyProofFiles`, `TestShSpawner_*`) (→ Steps 3-6)
- [x] ✅ HARDENED — `go vet ./...` (run from `backend/`) reports no issues (→ Step 7)
- [x] ✅ HARDENED — `npm run backend:test` (repo root) passes in full (→ Step 7)
- [ ] 🔲 MANUAL — Given a real claude profile at `~/.claude` (or `--profile <dir>`) with `saki-builder@saketek` installed+enabled, When its skill directory is removed (`rm -rf "$(python3 -c "import json;print(json.load(open('~/.claude/plugins/installed_plugins.json'))['plugins']['saki-builder@saketek'][0]['installPath'])")"/skills/build`) and `saki doctor --json --profile <dir>` is run, Then the claude entry reads `"status":"failed"` with `reason` containing `does not carry the saki-builder skills` and a non-empty `fix` — restoring the directory (or re-running `claude plugin install saki-builder@saketek --scope user`) flips it back to `"status":"ok"` (→ Concrete Example Output, Step 2)
- [ ] 🔲 MANUAL — `npx playwright test e2e/claude-init-env.spec.ts` still passes against the real `claude` binary (requires network + a real CLI login; not part of `backend:test`) (→ Step 7)

---

## Annotation Space

> Human: add notes, corrections, constraints here.

---
Status: [x] Draft  [ ] Annotated  [x] Approved  [ ] In Progress  [x] Complete
Readiness Gate: [x] Evidence Ledger present and every blocking item cited  [x] Blocking Set empty  [x] Unknowns <= 2
