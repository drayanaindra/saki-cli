<!-- prd-blocking: 0 -->
<!-- slices: 1 -->
<!-- appetite: small -->
<!-- prd-locked: @saki · 2026-08-25 · ui:tasks/proto-saki-doctor-remediation-text-for-opencode-and-claude/ -->

# PRD: `saki doctor` — remediation text for opencode and claude

**Owner:** unassigned · **Status:** Locked · **Updated:** 2026-08-25 · **Appetite:** small — hours · **Item:** F5

## 1. TL;DR
`saki doctor` already renders a runnable fix for every engine's `failed` verdict (codex, opencode, claude — `usecase.doctor.go`, F6 slice 2). Codex's and claude's raw spawn-refusal errors (the error a `saki run` preflight surfaces when the profile isn't provisioned) already embed that same fix text inline. Opencode's spawn-refusal error is the one place still silent — embed `usecase.OpencodeInstallFix` into `OpencodePluginProof`'s three error paths so an opencode spawn refusal carries the same runnable fix codex and claude already show.

## 2. Problem & Evidence
F5's roadmap Goal (written at F2's recut, before F6 shipped, `tasks/roadmap.md:33-37`) claims "only codex has remediation text today; opencode's error carries none and claude has no proof yet." Re-grounding against current code shows that premise is now mostly false — the real, narrower gap is a **structural asymmetry**: two of the three engines' spawn-refusal errors embed the fix text inline, one doesn't. This PRD does not claim that asymmetry has cost anyone measured time (no ticket, no reported confusion exists) — it is a parity gap, stated as one, not laundered into an unmeasured harm.

| Claim | Tag | Source |
|---|---|---|
| `saki doctor`'s `Fix` field is populated for all three engines (codex, opencode, claude) | observed | `backend/usecase/doctor.go:60` (`engineInstallFix(engine)` runs unconditionally on any `ProfileProof` failure); confirmed green: `TestDoctorService_Check/opencode_gets_the_rendered_Fix`, `TestDoctorService_Check_ClaudeFailure` (`go test ./backend/usecase/... -run TestDoctorService -v` → PASS) |
| `claude.go`'s spawn-refusal errors already embed `usecase.ClaudeInstallFix` | observed | `backend/infra/claude.go:48,56,60`, matching `backend/infra/codex.go:76-79`'s established pattern |
| `opencode.go`'s spawn-refusal errors embed no fix text (the one remaining gap) | observed | `backend/infra/opencode.go:40,50,58` — all three `fmt.Errorf` calls wrap `usecase.ErrEngineNotProvisioned` / `ErrOpencodePluginMissing` but cite no `usecase.OpencodeInstallFix` |
| the opencode spawn-refusal error (not `saki doctor`'s output) is what an operator/agent sees when a run's preflight refuses an unprovisioned opencode spawn | observed | `backend/infra/spawner.go:172` (`EngineProfileProof` → `OpencodePluginProof`) wires the SAME preflight pair `saki doctor` calls (`backend/usecase/doctor_ports.go:7`: "so a doctor verdict and a spawn refusal can never disagree") |

**Load-bearing assumption:** opencode is the only one of the three engines whose spawn-refusal error omits the fix text codex's and claude's already carry — `observed` (table above, all four rows are direct code citations; no claim is made about measured operator time-cost, only the structural asymmetry itself, which is what this PRD closes).

## 3. Primary Job to be Done (J1)
When my opencode spawn refuses to run because the profile doesn't resolve `@saketek/saki-builder`, I want the refusal error to behave the same way codex's and claude's already do (fix text inline), so all three engines give consistent, parity-checked failure output.

## 4. Related Jobs
None — single, narrow parity fix.

## 5. Desired Outcomes / Success Metrics

| # | Outcome (Minimize/Maximize [metric] when [context]) | Target | Basis | Method | JTBD |
|---|---|---|---|---|---|
| 5.1 | Maximize [spawn-refusal errors carrying a runnable fix] when [an opencode profile fails `OpencodePluginProof`] | 100% (3/3 error paths) | baseline 0/3 → 3/3 — source: `backend/infra/opencode.go:40,50,58` (verified: none of the three `fmt.Errorf` calls reference `usecase.OpencodeInstallFix`, grep on file) | query: acceptance criteria 1.1–1.3 (runtime error-string assertions) | J1 |
| 5.2 (counter) | guards 5.1: embedding the fix text must not change the wrapped-error contract (`errors.Is(err, usecase.ErrEngineNotProvisioned)` / `errors.Is(err, ErrOpencodePluginMissing)` still true) — a text-only change that silently drops an `errors.Is` wrap would break every caller matching on it | n/a | n/a | query: existing `errors.Is` assertions in `backend/infra/opencode_test.go` still pass | guards 5.1 |

## 6. Appetite & Kill Criteria
**Appetite:** small — a couple hours. One file's error strings + one regression test.
**Kill Criteria:** if closing 5.1 (3/3 error paths carrying the fix text) can only be done by dropping either `errors.Is(err, usecase.ErrEngineNotProvisioned)` or `errors.Is(err, ErrOpencodePluginMissing)` on any path — i.e. 5.1 can't be satisfied without breaking 5.2's guard — stop and re-scope; a text-only parity win that silently breaks the wrap contract every caller matches on is not worth shipping.

## 7. Solution Shape
Embed `usecase.OpencodeInstallFix` into all three `OpencodePluginProof` error returns (`backend/infra/opencode.go:40,50,58`), in the same `"...: %v — run:\n%s"` / `"...: %s lists plugins %v — run:\n%s"` shape `claude.go` already uses — so opencode's spawn-refusal error reads exactly like codex's and claude's.

### Alternatives considered / Decision
- **Chosen: embed the fix text directly in `opencode.go`'s existing error strings** (mirrors `claude.go`/`codex.go` verbatim) — smallest diff, zero new abstraction, keeps the "one string, one place" invariant (`usecase.OpencodeInstallFix` stays the single source `saki init-env` and `saki doctor` also read).
- **Rejected: wrap the returned error one layer up in `spawner.go`** — would duplicate the "append the fix" logic across all three engines instead of reusing what codex/claude already do inline, and would apply the fix text even to the config-unreadable/unparseable cases where `claude.go`'s equivalent paths already show the same pattern is fine.
- **Rejected: leave it as a `saki doctor`-only fix (do nothing)** — the actual evidence shows the operator-facing gap is specifically the spawn-refusal error text, not `saki doctor`'s `Fix` field (already covered). Doing nothing leaves the one real asymmetry between opencode and its two sibling engines.

## 8. Vertical Slices

### Slice 1 — Opencode spawn-refusal errors embed the runnable fix
Embed `usecase.OpencodeInstallFix` into `OpencodePluginProof`'s three `fmt.Errorf` calls (`backend/infra/opencode.go:40,50,58`), matching the `claude.go`/`codex.go` message shape. `Serves: J1 · 5.1`

## 9. Acceptance Criteria per Slice

**Slice 1:**
- 1.1 [auto] Given an opencode profile missing the `@saketek/saki-builder` plugin entry, when `OpencodePluginProof` is called, then the returned error's string contains `usecase.OpencodeInstallFix`'s exact text. → 5.1
- 1.2 [auto] Given an opencode config file that is missing, when `OpencodePluginProof` is called, then the returned error's string contains `usecase.OpencodeInstallFix`'s exact text. → 5.1
- 1.3 [auto] Given an opencode config file that is unparseable (invalid JSON/JSONC), when `OpencodePluginProof` is called, then the returned error's string contains `usecase.OpencodeInstallFix`'s exact text. → 5.1
- 1.4 [auto] Given any of the three failure cases above, when the returned error is checked, then `errors.Is(err, usecase.ErrEngineNotProvisioned)` and `errors.Is(err, ErrOpencodePluginMissing)` are both still true (no regression to the existing wrap contract). → 5.2

## 10. Business Rules & Invariants
1. Every `OpencodePluginProof` failure error MUST still wrap both `usecase.ErrEngineNotProvisioned` and `ErrOpencodePluginMissing` — the fix text is additive, never a replacement for the existing `errors.Is` contract callers rely on (`backend/infra/spawner.go:172`, `backend/usecase/doctor_ports.go:12-13`). No `🔒 INVARIANT` tier (not money/stock/tenant), but tested per 1.4 regardless since a caller silently losing the wrap would break preflight routing.

## 11. Non-Goals
- ✗ Changing `saki doctor`'s `Fix` field behavior for any engine — already correct (F6 slice 2), out of scope.
- ✗ Changing `claude.go`'s spawn-refusal errors — already embeds `ClaudeInstallFix`, out of scope.
- ✗ Changing `usecase.OpencodeInstallFix`'s rendered content/argv — reused verbatim, not re-authored.
- ✗ Any change to `saki init-env`'s opencode provisioning behavior.

## 12. Rabbit Holes & Open Questions
- Rabbit hole: don't refactor `opencode.go`'s three error sites into a shared helper beyond what's needed to embed the fix — YAGNI for a 3-call-site, single-file change.
- Open question: none — the roadmap's F5 Goal text is stale (predates F6); this PRD supersedes it with the re-grounded, narrower scope. No further discovery needed.

## 13. Technical Constraints
None beyond existing hexagonal layering (`backend/infra/opencode.go` already imports `backend/usecase` for `ErrEngineNotProvisioned`; reusing `usecase.OpencodeInstallFix` from the same package adds no new dependency).

## 16. Technical Contract (thin)

**Entities (data):** none — no data surface.

**Endpoints (API):** none — no new/changed HTTP surface; this changes an internal Go error string returned through the existing `EngineProfileProof` port.

**Architecture decision (one, load-bearing):**
- CHANGE — `backend/infra/opencode.go:40,50,58` (`OpencodePluginProof`'s three error returns). Serves 8.1 · 5.1. Reused component: `usecase.OpencodeInstallFix` (`backend/usecase/doctor.go:23`, unmodified — read, not changed). ↳ Breaks: none (additive) — the error still wraps the same two sentinels (`usecase.ErrEngineNotProvisioned`, `ErrOpencodePluginMissing`) every caller (`backend/infra/spawner.go:172`, doctor's `ProfileProof` port) already matches on with `errors.Is`; only the string grows a trailing `— run:\n%s` fix line, exactly as `claude.go`/`codex.go` already do. Alternative rejected: wrapping the fix on in `spawner.go` instead (Solution Shape §7) — would duplicate per-engine logic already proven correct inline in the sibling engines.
