<!-- prd-locked: n/a (build-driven) -->

# EXECUTION PLAN: opencode spawn-refusal errors embed the runnable fix

**Date:** 2026-08-25
**Blocking items:** 0 (see Evidence Ledger)
**Risk Score:** LOW
**Unknown Count:** 0 / 2 max
**Behavior Spec:** N/A (backend-only — internal Go error string, no UI/endpoint)
**Source PRD:** `tasks/prd-saki-doctor-remediation-text-for-opencode-and-claude.md` § Slice 1 (Item F5, Locked)
**Prior slices:** N/A — slice 1 / standalone
**Appetite:** ~1 agent task (from PRD slice — small appetite, ≤2 slices, this is the only one)
**Kill-if:** 5.1 (3/3 error paths carrying the fix) can only be reached by breaking 5.2 (the `errors.Is` wrap contract) — N/A, not hit (see Steps)

## Problem Statement

When `OpencodePluginProof` refuses an opencode spawn because the profile doesn't resolve
`@saketek/saki-builder`, I want the returned error to carry the runnable fix command inline — the
same way `backend/infra/claude.go` and `backend/infra/codex.go` already do — so all three engines'
spawn-refusal errors behave consistently.

---

## Concrete Example Output

**Before** (today, `backend/infra/opencode.go:58`, plugin array present but doesn't list saki-builder):
```
engine profile cannot resolve the saki-builder commands: opencode profile does not resolve @saketek/saki-builder: /home/x/.config/opencode/opencode.json lists plugins [some-other-plugin]
```

**After** (this slice, all 3 call sites get the same suffix):
```
# line 58 — plugin list present, saki-builder not in it:
engine profile cannot resolve the saki-builder commands: opencode profile does not resolve @saketek/saki-builder: /home/x/.config/opencode/opencode.json lists plugins [some-other-plugin] — run:
opencode plugin @saketek/saki-builder --global

# line 40 — config file missing/unreadable:
engine profile cannot resolve the saki-builder commands: opencode profile does not resolve @saketek/saki-builder: config /home/x/.config/opencode/opencode.json unreadable: open /home/x/.config/opencode/opencode.json: no such file or directory — run:
opencode plugin @saketek/saki-builder --global

# line 50 — config file present but unparseable:
engine profile cannot resolve the saki-builder commands: opencode profile does not resolve @saketek/saki-builder: config /home/x/.config/opencode/opencode.json unparseable: invalid character '{' looking for beginning of object key string — run:
opencode plugin @saketek/saki-builder --global
```
(The `— run:\n<cmd>` suffix is `usecase.OpencodeInstallFix`'s exact rendered text — one line,
`opencode plugin @saketek/saki-builder --global` — appended verbatim at all 3 sites, matching
`claude.go:48,56,60`'s `"...: %v — run:\n%s"` shape.)

---

## Steps

| # | Action | Files (exact paths) | Risk | Test | Committable? |
|---|--------|---------------------|------|------|-------------|
| 1 | Embed `usecase.OpencodeInstallFix` into `OpencodePluginProof`'s 3 `fmt.Errorf` calls (lines 40, 50, 58), matching `codex.go:78`'s `"...— run:\n%s"` convention; extend/add the 3 matching tests, each asserting ALL THREE: `errors.Is(err, usecase.ErrEngineNotProvisioned)` (the sentinel `backend/adapter/http.go:543` routes on — the load-bearing one, not just the plugin-specific sentinel), `errors.Is(err, ErrOpencodePluginMissing)`, and `strings.Contains(err.Error(), usecase.OpencodeInstallFix)` (assert via the constant, never a hardcoded copy of the fix string — mirrors `codex_test.go:402-409` / `claude_test.go:159-162` line-for-line) | `backend/infra/opencode.go` (lines 40, 50, 58); `backend/infra/opencode_test.go` (extend `TestOpencodePluginProof_NoConfigFile`, add `TestOpencodePluginProof_Unparseable`, extend `TestOpencodePluginProof_Missing`) | LOW | `TestOpencodePluginProof_NoConfigFile`, `TestOpencodePluginProof_Unparseable` (new), `TestOpencodePluginProof_Missing` — all in `backend/infra/opencode_test.go` | Yes |

> Test-first (Red→Green→Refactor): write the 3 assertions against the CURRENT (unfixed) code first —
> they fail (no `OpencodeInstallFix` text in the error) — then make the 3 `fmt.Errorf` edits, re-run to
> green. `TestOpencodePluginProof_Unparseable` is new (no existing invalid-JSON test); the other two are
> reuse-first extensions of tests that already exercise the same 2 code paths (`NoConfigFile` → line 40,
> `Missing` → line 58), keeping to 3 test functions for 3 code paths — no new test file, no new helper.
>
> **`TestOpencodePluginProof_Unparseable` is load-bearing for the coverage floor, not optional polish** —
> `opencode.go:48-51` (`json.Unmarshal(raw)` then `json.Unmarshal(stripJSONC(raw))`, both failing → line 50)
> is currently the ONLY branch in `OpencodePluginProof` with zero test coverage; this is the sole test that
> covers it. **Fixture must genuinely fail BOTH parses** — `stripJSONC` only strips `//`/`/* */` comments
> (string-aware) and does not repair malformed JSON, so a naive comment-only/trailing-comma fixture could
> silently parse clean via the JSONC fallback and mis-hit the success path instead. Use
> `writeOpencodeProfile(t, dir, "{ this is not json")` (irrecoverable even after comment-stripping), and
> additionally assert `strings.Contains(err.Error(), "unparseable")` (the literal substring `opencode.go:50`
> emits) to pin the branch — not just the wrap-chain assertions above, which alone can't distinguish line 50
> from line 40/58.

---

## User Role Coverage

N/A — this is an internal Go error-string change with no user-facing role distinction; the "user" is
whichever operator/agent triggers an opencode spawn preflight failure or runs `saki doctor` (both existing
callers, unchanged by this slice — see Compatibility & Consumers).

---

## Plan Wiring

### Flow 1: opencode spawn preflight refuses an unprovisioned profile
```
saki run (CLI, --engine opencode)              (src/commands/run.ts — unchanged)
  → backend HTTP spawn request                  (unchanged)
  → backend/infra/spawner.go:172 EngineProfileProof(engine, configDir)
  → backend/infra/opencode.go:36 OpencodePluginProof(configDir)   ← THIS SLICE changes lines 40, 50, 58
  → error returned up through EngineProfileProof → spawn refusal surfaced to the CLI/HTTP caller
```

### Flow 2: `saki doctor` reports the same failure
```
saki doctor (CLI)                              (src/commands/doctor.ts — unchanged)
  → backend/usecase/doctor.go:43 DoctorService.Check
  → backend/usecase/doctor.go:58 s.proofs.ProfileProof(engine, configDir)  → OpencodePluginProof (same function, unchanged call site)
  → backend/usecase/doctor.go:60 report.Fix = engineInstallFix(engine)     (unchanged — already populated, out of scope)
```
Flow 2 is included only to show `OpencodePluginProof`'s error text and doctor's `Fix` field are two
independent surfaces (§11 Non-Goal: doctor's `Fix` field is not touched) — this slice changes only Flow 1's
error string.

---

## Compatibility & Consumers

| Changed surface (exact) | Kind | Consumers found (`grep`) | Verdict | Mitigation / step |
|---|---|---|---|---|
| `OpencodePluginProof` error string (3 return sites, `backend/infra/opencode.go:40,50,58`) | error message text | 2 call sites: `backend/infra/spawner.go:172` (via `EngineProfileProof`), `backend/usecase/doctor_test.go` fakes (test-only, don't call the real function) — verified: `grep -rn "OpencodePluginProof" backend --include=*.go` | unaffected | both callers match on `errors.Is(err, usecase.ErrEngineNotProvisioned)` / `errors.Is(err, ErrOpencodePluginMissing)`, never the string content (`grep -rn "OpencodePluginProof" backend/infra/spawner.go backend/usecase` shows no string-equality assertion) — a text-only append doesn't change wrap behavior |

**Forward compatibility:** additive-only (the error string grows a trailing `— run:\n<cmd>` suffix; both
sentinel wraps are unchanged, verified by AC 1.4 / Step 1's tests).

---

## Migration Checklist

N/A — no schema change.

---

## Branch Points (pre-declared)

- None. The change is mechanical (3 `fmt.Errorf` edits mirroring an already-shipped pattern in `claude.go`) —
  no reversible fork, no irreversible decision, no guardrail proximity.

---

## Unknowns (must be <= 2)

None.

---

## No-Gos

- Will NOT touch `saki doctor`'s `Fix` field behavior (`backend/usecase/doctor.go`) — already correct.
- Will NOT touch `backend/infra/claude.go` — already embeds `ClaudeInstallFix`.
- Will NOT change `usecase.OpencodeInstallFix`'s rendered content or `OpencodeProvisionArgv`.
- Will NOT touch `saki init-env`'s opencode provisioning behavior.

---

## Implementation Completeness Checklist

**User Coverage** — N/A, no role distinction (see User Role Coverage above).

**Database & Migrations** — N/A, no schema change.

**API Layer** — N/A, no HTTP surface (internal Go function).

**Service / Business Logic**
- [x] `OpencodePluginProof` (`backend/infra/opencode.go:36-59`) is the only function modified/created — named with file path.
- [x] No new side effects — pure string change to existing error returns.
- [x] Error paths documented: missing config (line 40), unparseable config (line 50), plugin-not-listed (line 58) — all 3 covered by Step 1's tests.

**Frontend** — N/A, no UI.

**Compatibility & Consumers**
- [x] Compatibility & Consumers filled — 1 changed surface, 2 consumers found, verdict `unaffected` with citation.
- [x] Prior slices: N/A — slice 1 / standalone.

**Plan Wiring**
- [x] Flow 1 (spawn preflight) and Flow 2 (saki doctor, unchanged) both traced end-to-end.
- [x] Step 1 names exact file + function (`OpencodePluginProof`, `backend/infra/opencode.go`) and exact test functions.

---

## Evidence Ledger

### Blocking (must be empty to present) — resolved by rplan-review round 1

| # | Step | Blocking predicate (resolved) | Evidence | Resolution |
|---|------|--------------------------------|----------|------------|
| B1 | 1 | Test cell only said "assert the wrap contract" — didn't name `usecase.ErrEngineNotProvisioned`, the sentinel production code actually routes on | Backend + Product experts (converged); confirmed `backend/adapter/http.go:543` (`case errors.Is(err, usecase.ErrBinaryNotFound), errors.Is(err, usecase.ErrEngineNotProvisioned):`) routes on it, not `ErrOpencodePluginMissing` alone | Step 1 + Success Criteria 1.4 now name both `errors.Is` assertions explicitly |
| B2 | 1 | Fix-text assertion didn't bind to the `usecase.OpencodeInstallFix` constant — risked a hardcoded-literal test that silently decouples from the source of truth | Product expert; confirmed `backend/usecase/doctor.go:19-23`'s stated anti-drift purpose + the precedent `TestOpencodeInstallFixIsRenderedFromTheProvisionArgv` (`backend/usecase/initenv_test.go:451`) exists exactly to guard this | Success Criteria 1.1-1.3 now specify `strings.Contains(err.Error(), usecase.OpencodeInstallFix)` |
| B3 | 1 | `TestOpencodePluginProof_Unparseable`'s fixture was unspecified — a naive comment-only fixture could parse clean via the JSONC fallback and miss line 50 entirely; line 50 was also unflagged as the coverage floor's only gap | QA expert; confirmed via read of `backend/infra/opencode.go:36-59` + `opencode_test.go:1-77` that no existing test exercises the double-parse-failure branch | Step 1 note + Success Criteria 1.3 now pin the exact fixture (`"{ this is not json"`) and an `"unparseable"` substring assertion to pin the branch |

### Advisory (visible, never gates)

| Step | Note | Evidence |
|------|------|----------|
| — | User Role Coverage / API Layer / Frontend sections are N/A (backend-only, no HTTP/UI surface) | PRD §16: "no data surface... no new/changed HTTP surface" |
| — | `saki doctor`'s `Reason` field will duplicate the fix text already in `Fix` (same as codex/claude today) — pre-existing, accepted pattern, not a regression | Backend + Product experts; `backend/usecase/doctor.go:54-60` |
| — | All anchors verified, all targets have anchor parents and creating steps, all checklist items on state-changing steps satisfied, no unknowns above LOW | self-audit + 3 expert agents; anchors: `backend/infra/opencode.go:36-59`, `backend/infra/claude.go:45-60`, `backend/infra/codex.go:76-79`, `backend/usecase/doctor.go:23`, `backend/adapter/http.go:543`, `backend/infra/opencode_test.go:1-77`, `backend/infra/codex_test.go:395-412`, `backend/infra/claude_test.go:150-163` (all read) |

**Blocking: 3 → 0 (rplan-review round 1) → READY.**

---

## Success Criteria

- [x] 1.1 `TestOpencodePluginProof_Missing` (extended) — a profile listing other plugins (not saki-builder) → `strings.Contains(err.Error(), usecase.OpencodeInstallFix)` (via the constant, not a hardcoded literal). PASS.
- [x] 1.2 `TestOpencodePluginProof_NoConfigFile` (extended) — a missing config file → `strings.Contains(err.Error(), usecase.OpencodeInstallFix)`. PASS.
- [x] 1.3 `TestOpencodePluginProof_Unparseable` (new, fixture `"{ this is not json"`) — invalid JSON that fails BOTH `json.Unmarshal(raw)` and `json.Unmarshal(stripJSONC(raw))` → `strings.Contains(err.Error(), usecase.OpencodeInstallFix)` AND `strings.Contains(err.Error(), "unparseable")` (pins branch to `opencode.go:50`, distinct from 1.1/1.2). PASS.
- [x] 1.4 All three tests above ALSO assert `errors.Is(err, usecase.ErrEngineNotProvisioned)` (the sentinel `backend/adapter/http.go:543` routes an opencode spawn refusal on) AND `errors.Is(err, ErrOpencodePluginMissing)`. PASS.
- [x] 1.5 `backend/infra/doctor_test.go`'s existing `strings.Contains(r.Reason, "does not resolve @saketek/saki-builder")` assertion still passes unmodified — the existing wrap-prefix text is untouched, only the fix text is appended. PASS (`TestDoctorService_Check` green).
- [x] `go test ./backend/infra/... ./backend/usecase/...` passes (no regression to existing suite). PASS — full `go test ./...` green (adapter, cmd/server, domain, infra, usecase all `ok`).

---

## Annotation Space

> Human: add notes, corrections, constraints here.

---
Status: [x] Draft  [ ] Annotated  [x] Approved  [ ] In Progress  [ ] Complete
Readiness Gate: [x] Evidence Ledger present and every blocking item cited  [x] Blocking Set empty  [x] Unknowns <= 2
