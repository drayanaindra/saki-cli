# EXECUTION PLAN: Homebrew formula + persistent background service for saki-backend

**Date:** 2026-08-24
**Blocking items:** 0 (see Evidence Ledger)
**Risk Score:** MED (new external repo + an existing-function behavior change with an existing test to update; no auth/payment/migration/multi-tenant surface)
**Unknown Count:** 0 / 2 max
**Behavior Spec:** N/A (backend/tooling-only, no UI)
**Source PRD:** N/A (standalone, Plan-track)
**Prior slices:** N/A — slice 1 / standalone
**Item:** I4
**Appetite:** ~7 agent tasks
**Kill-if:** N/A (Plan-track item, no PRD-derived kill metric)

## Problem Statement

When I want `saki-backend` reachable at all times — not just while a `saki` CLI command happens to be
running — I want a single `brew install` to set up a background service (auto-started at login, restarted
on crash) instead of relying on `saki`'s lazy per-command auto-start (F1), so something other than the
`saki` CLI (a direct API caller, a long-running integration) can rely on it being up.

---

## Concrete Example Output

Today: `saki-backend` only ever runs via `ensureDaemon()` (`src/daemon.ts:168`), spawned lazily by a
`saki` CLI invocation. No Homebrew tap exists (`gh repo list drayanaindra` — no `homebrew-*` repo;
`gh api users/saketek` → 404, confirming `@saketek` is only an npm scope, not a GitHub namespace).
`v0.1.0`'s GitHub Release already has the 4 platform binaries + real checksums this plan will embed
directly (fetched live):

```
$ curl -sL https://github.com/drayanaindra/saki-cli/releases/download/v0.1.0/SHA256SUMS.txt
ad08e42a8309b2ea9a8b53fd6357f86bbd37f200a338c4d0e456eb212af5dec4  saki-backend-darwin-amd64
721758255149075a17d435aa8dc0c9f0b93c2a4cb9f68a162a90216fa08beacd  saki-backend-darwin-arm64
86c4d81b9a1f86b3b3b65b76ae2b0ba3410a23c529bc7fa7e3801d5a3e2ada6e  saki-backend-linux-amd64
6f6c1e3b490e172e73cde3593b70249d23c96083d664484f31e97aac16504d4c  saki-backend-linux-arm64
```

After this plan:

```
$ brew tap drayanaindra/tap
$ brew install saki
$ brew services start saki
==> Successfully started `saki` (label: homebrew.mxcl.saki)
$ curl -s http://127.0.0.1:8788/api/health
{"ok":true}
$ saki backend status          # the npm-installed CLI, talking to the SAME service-managed backend
backend healthy (pid null)     # no state file — reachability probe confirms it's up anyway
```

`brew services` generates the launchd `LaunchAgent`/systemd user unit **from one Ruby `service do`
block** in the formula — Homebrew's own service DSL, not two hand-written platform files.

---

## Steps

| # | Action | Files (exact paths) | Risk | Test | Committable? |
|---|--------|---------------------|------|------|-------------|
| 1 | Create GitHub repo `drayanaindra/homebrew-tap` (public, MIT, empty) — the standard Homebrew personal-tap naming convention (`brew tap USER/tap` resolves to `homebrew-tap`) | new repo, not in this checkout | LOW | manual: `gh repo view drayanaindra/homebrew-tap` succeeds | Yes |
| 2 | Write `Formula/saki.rb` in that repo: `version "0.1.0"`; `on_macos`/`on_linux` × `Hardware::CPU.arm?` branches pointing at the 4 real `v0.1.0` release asset URLs with the 4 real sha256 digests above; `def install; bin.install "saki-backend-#{...}" => "saki-backend"; end`; a `service do; run [opt_bin/"saki-backend"]; keep_alive true; log_path var/"log/saki-backend.log"; error_log_path var/"log/saki-backend.log"; end` block (this single block is what `brew services` turns into a launchd plist on macOS and a systemd user unit on Linux — no hand-written platform files needed); a `test do; assert_predicate bin/"saki-backend", :exist?; assert_predicate bin/"saki-backend", :executable?; end` block (Homebrew requires a `test do` on every formula) | `homebrew-tap/Formula/saki.rb` (new, other repo) | MED (formula must be syntactically valid Ruby AND pass `brew audit` — a bad `service do` block fails silently at `brew services start`, not at install) | manual: `ruby -c Formula/saki.rb` (syntax) then `brew audit --strict --online Formula/saki.rb` (Homebrew's own style/correctness linter) | Yes |
| 3 | Real install smoke test: `brew tap drayanaindra/tap && brew install drayanaindra/tap/saki && brew services start saki`, then `curl http://127.0.0.1:8788/api/health`, then `brew services stop saki && brew uninstall saki && brew untap drayanaindra/tap` — **adjusted in impl: BLOCKED on this dev machine** by outdated Xcode Command Line Tools (`brew install` → `Error: Your Command Line Tools are too outdated`, an interactive Software-Update/`sudo xcode-select --install` fix, not something to route around). Substituted the strongest verification possible without a full install: `brew tap` succeeded (formula parsed, tapped), `brew install --dry-run`-equivalent resolver output confirmed `Would install 1 formula: saki (0.1.0)` on the correct `on_macos`/arm64 branch, and an independent `curl` + `shasum -a 256` of the exact release asset the formula points at matched the embedded checksum byte-for-byte. Untapped afterward (net zero on this machine, same intent as the original cleanup). Full `brew services start` + health-check verification deferred to whenever CLT is updated — see Evidence Ledger | N/A (verification only) | LOW | manual: commands above, each checked for its actual (adjusted) output | No (verifies steps 1–2; not itself a commit) |
| 4 | Fix `cmdBackend`'s `status` branch in `src/commands/backend.ts:24` (the `else` arm when `readDaemonState` returns `null`) to probe reachability via the existing `ensureHealthy({goUrl: ctx.client.goUrl})` helper (`src/commands/backend.ts:29`) instead of hardcoding `healthy: false` — so a service-managed backend (no state file, because `brew services` never goes through `ensureDaemon()`/`writeState()`) is correctly reported healthy | `src/commands/backend.ts` | MED (changes an existing function's observable output for an existing input shape — state-changing in the sense that a monitoring/status consumer's read now differs) | Test-Along: extend `src/commands/backend.test.ts` — see step 5 | Yes |
| 5 | Update `src/commands/backend.test.ts`: rewrite the existing "unavailable" sub-case (lines 87–96, currently asserts `healthy:false` with no fetch stub) to `vi.stubGlobal('fetch', vi.fn(async () => { throw new Error('ECONNREFUSED') }))` before calling `cmdBackend(..., ['status'], {})`, keep asserting `{pid:null, healthy:false, ...}` — now via a genuine failed probe, not a hardcoded value; add a new `it('reports healthy via reachability probe when no state file exists', ...)` case: `daemon.readDaemonState.mockResolvedValue(null)`, `vi.stubGlobal('fetch', vi.fn(async () => ({ok:true, json: async () => ({ok:true})})))`, assert `{pid:null, healthy:true, goUrl:ctx.client.goUrl, socketPath:null}` | `src/commands/backend.test.ts` | LOW | `npx vitest run -t cmdBackend` (Test-Along, same commit as step 4) | Yes (grouped with step 4) |
| 6 | Add "Install via Homebrew (persistent service)" section to `README.md`, after the existing "Install via npm" block: `brew tap drayanaindra/tap && brew install saki && brew services start saki`; one explicit coexistence line: *"If you've already used the `saki` CLI directly (which lazily spawns its own backend), run `saki backend stop` first — `saki-backend` binds `127.0.0.1:8788` exclusively (`backend/cmd/server/main.go:148-150`, `log.Fatal` on bind failure) so a lazily-spawned instance and the brew service can't both hold the port. Once the service owns it, `saki` CLI commands reuse it automatically (`src/daemon.ts:176`'s health-probe reuse) — no conflict in that direction."* | `README.md` | LOW | manual: reads correctly, commands match steps 1–3 exactly | Yes |
| 7 | Add a step to `docs/RELEASING.md`'s procedure (after the existing step 8 "Verify"): *"9. Update the Homebrew tap: in `drayanaindra/homebrew-tap`, bump `Formula/saki.rb`'s `version` and all 4 URL/sha256 pairs to match the new release's `SHA256SUMS.txt`, commit, push. (Manual — no CI automation yet; see I4 plan's No-Gos.)"* | `docs/RELEASING.md` | LOW | manual: step reads correctly, references the right file | Yes |
| 8 | Flip `tasks/roadmap.md` `### I4` block `**Child plan:** —` → `**Child plan:** tasks/i4-homebrew-service-plan.md` | `tasks/roadmap.md` | LOW | manual: grep confirms | Yes |

---

## User Role Coverage

Not a user-facing feature (no UI, no auth roles) — install-time / operator actors:

| Role | Can Do | Cannot Do | Auth Guard | Entry Point |
|------|--------|-----------|------------|--------------|
| Homebrew user (macOS/Linux) | `brew tap` + `brew install saki` + `brew services start saki` for an always-on backend | install on an unsupported platform (Homebrew itself gates this — same darwin/linux × amd64/arm64 matrix as I1, no new exclusion introduced) | N/A (no auth) | `brew install`, `brew services start` |
| npm-installed `saki` CLI user | keeps working unchanged whether or not the brew service exists — `ensureDaemon()`'s existing reuse-via-health-probe (`src/daemon.ts:176`) already handles a service-managed backend with no code change | start a SECOND backend on the same port if one (lazy or service) already owns it — surfaces as `log.Fatal`/exit 1 from the Go process, not a silent corruption | N/A | `saki <any command>` |
| Repo maintainer | update the tap formula per release (step 7, manual) | auto-sync — explicitly out of scope this round (No-Gos) | `gh`/git push access to `drayanaindra/homebrew-tap` | `docs/RELEASING.md` step 9 |

---

## Plan Wiring

### Flow 1: Homebrew install + persistent service
```
brew tap drayanaindra/tap
  → clones drayanaindra/homebrew-tap, reads Formula/saki.rb
brew install saki
  → downloads the matching platform saki-backend-{os}-{arch} asset from the v0.1.0 GitHub Release
  → verifies against the sha256 embedded in Formula/saki.rb
  → installs to opt_bin/saki-backend (Formula/saki.rb `install` block)
brew services start saki
  → Homebrew's service DSL generates + loads a launchd LaunchAgent (macOS) or systemd --user unit (Linux)
  → that unit execs opt_bin/saki-backend directly (no env vars needed — SAKI_UPSTREAM unset ⇒ standalone, docs/project-context.md)
  → backend/cmd/server/main.go:148 binds 127.0.0.1:8788 (unchanged — no code touched)
```

### Flow 2: `saki backend status` against a service-managed backend (no state file)
```
saki backend status
  → src/index.ts:135 cmdBackend(ctx, ['status'], {})
  → src/commands/backend.ts:24 readDaemonState(ctx.env) → null (brew's launchd/systemd never calls ensureDaemon/writeState)
  → [CHANGED, step 4] ensureHealthy({goUrl: ctx.client.goUrl}) → real fetch to 127.0.0.1:8788/api/health → true
  → emits {pid:null, healthy:true, goUrl, socketPath:null}
```

---

## Compatibility & Consumers

| Changed surface (exact) | Kind | Consumers found (`grep`) | Verdict | Mitigation / step |
|---|---|---|---|---|
| `cmdBackend`'s `status` branch output when `readDaemonState` returns `null` (`src/commands/backend.ts:24-26`) | function behavior (existing input shape, new output value) | 2 (`src/index.ts:135` route wiring — passes through untyped, unaffected by the *value* of `healthy`; `src/commands/backend.test.ts:87-96` — asserts `healthy:false` unconditionally for this branch) | `src/index.ts:135` unaffected (shape unchanged, only the boolean value differs) · `backend.test.ts:87-96` updated in step 5 | step 5 |
| No MCP tool wraps `cmdBackend` (`grep -rln "cmdBackend" src/mcp/` → 0 matches) | — | 0 | none found (grep: `grep -rln "cmdBackend\|backend.*status" src/mcp/`) | — |
| `backend/cmd/server/main.go` bind logic, `backend/adapter/originguard.go`, loopback invariant | — | this plan does not modify it — Homebrew just execs the existing binary the same way `./dist/saki-backend &` already did | unaffected (no code change; verified by reading `backend/cmd/server/main.go:141-150`) | — |

**Forward compatibility:** additive-only for the CLI/daemon side (a new, optional install path). The one
behavior change (`cmdBackend` status) is a correctness fix on an existing branch, not a new field or a
removed one — its consumer (the test) is updated in the same commit.

---

## Migration Checklist

N/A — no database, no schema change (`saki-cli` has no persistence layer beyond journal files, untouched
by this plan).

---

## Branch Points (pre-declared)

- Step 1: tap repo name (`drayanaindra/homebrew-tap`, tapped as `drayanaindra/tap`) → auto-resolved, not
  paused on: reversible (an empty new repo, renamable/deletable with zero downstream cost since nothing
  depends on it yet), and it's the standard Homebrew personal-tap convention (`brew tap USER/NAME` requires
  the repo to be literally named `homebrew-NAME`) — the unsurprising choice for anyone following Homebrew's
  own docs. `AUTO-RESOLVED: what to name the tap repo → drayanaindra/homebrew-tap (tapped as
  drayanaindra/tap) — matches Homebrew's own naming convention, no GitHub org "saketek" exists to use
  instead (gh api users/saketek → 404)`.
- Step 2: automating the formula's version/sha bump on every saki-cli release (vs. the manual step 7) →
  auto-resolved to manual for THIS plan: reversible (automation can be added later without touching the
  formula's shape), and appetite-scoped — I4's ask was "a service via a single install," not "a fully
  automated two-repo release pipeline." `AUTO-RESOLVED: automate the tap formula bump in CI → not now,
  manual step in docs/RELEASING.md — keeps this plan's scope to the stated ask, automating it is a
  reversible follow-up`.
- Step 3: the smoke test in step 3 actually installing brew packages on this dev machine → **PAUSE only
  if** `brew` is unavailable (`command -v brew` — already probed, present: Homebrew 6.0.18) or the test
  would leave the machine in a different state than before (mitigated: step 3 explicitly uninstalls +
  untaps at the end, netting zero persistent change to this dev machine).

---

## Unknowns

None outstanding — the two candidate unknowns (tap repo naming, automation scope) were both resolved
during research (Branch Points above).

---

## No-Gos

- Will NOT hand-write a separate launchd `.plist` or systemd `.service` file — Homebrew's `service do`
  block (step 2) generates both from one source, which is the point of using Homebrew here at all.
- Will NOT automate the tap formula's version/sha bump in CI this round — step 7 is a manual
  `docs/RELEASING.md` step; automating it is a reversible follow-up (Branch Points, step 2).
- Will NOT modify `backend/cmd/server/main.go`'s bind logic, `backend/adapter/originguard.go`, or any
  loopback-only invariant — the service execs the exact same binary the same way; no change to what it
  binds to or how.
- Will NOT change `src/daemon.ts`'s `ensureDaemon()` spawn/reuse logic — its existing health-probe reuse
  (`daemon.ts:176`) already makes a service-managed backend work with zero changes there; only the
  `status` *reporting* branch in `backend.ts` (a different, smaller surface) needs a fix.
- Will NOT publish the npm CLI's own `saki-backend` copy through Homebrew — Homebrew only manages the
  backend binary as a service; the `saki` CLI itself stays npm-only (I1), matching that the two
  distribution channels serve different needs (CLI vs. always-on service).

---

## Implementation Completeness Checklist

**User Coverage**
- [x] Every role (Homebrew user, npm CLI user, maintainer) is in the Role Coverage matrix
- [x] Each role's path traced in Plan Wiring (Flow 1, Flow 2)
- [x] No auth applicable (install-time/service tooling, no HTTP auth surface) — N/A noted
- [x] Edge cases documented: port-already-held conflict (step 6's README note), no-state-file service scenario (step 4/5)

**Database & Migrations**
- [x] N/A — no schema change (see Migration Checklist)

**API Layer**
- [x] N/A — no new HTTP endpoint; `cmdBackend` status response shape unchanged (only a value differs)

**Service / Business Logic**
- [x] Every function modified named with file path (`cmdBackend` — `src/commands/backend.ts:24`, reusing existing `ensureHealthy` — `src/commands/backend.ts:29`)
- [x] Side effects listed: Formula install downloads a binary + starts a background process (steps 2-3); no side effects from the `backend.ts` fix beyond an extra fetch call already made in the sibling branch
- [x] Error cases documented: bind-port conflict (`log.Fatal`, exit 1 — README note), formula syntax/audit failure (step 2 Test column), failed reachability probe (step 5's rewritten test)

**Frontend**
- [x] N/A — no UI

**Compatibility & Consumers**
- [x] Table filled — one real changed surface (`cmdBackend` status branch), one explicit `none found (grep: …)` row, one N/A row for the untouched backend bind logic
- [x] Prior slices: N/A — slice 1 / standalone

**Plan Wiring**
- [x] Both flows traced end-to-end with exact files/functions
- [x] No vague "update X" steps — every step names the file and function/section

---

## Evidence Ledger

### Blocking (must be empty to present)

*(none)*

### Advisory (visible, never gates)

| Step | Note | Evidence |
|------|------|----------|
| 1-2 | The tap repo (`drayanaindra/homebrew-tap`) is created and pushed to as part of this plan's implementation, not merely documented — same risk tier as other GitHub-visible actions already taken this session (making saki-cli public, setting NPM_TOKEN) | Branch Points, step 1 |
| 7 | Tap formula version bump stays manual (no CI automation) — explicit scope cut, not an oversight | No-Gos |
| 3 | The install smoke test runs on this session's own dev machine and is explicitly cleaned up (uninstall + untap) afterward — not left resident | Step 3 action column |
| 2-3 | Full `brew audit`/`brew install`/`brew services start` blocked on this dev machine by outdated Xcode Command Line Tools — a genuine environment limitation (interactive Software Update / `sudo xcode-select --install`), not routed around. User explicitly chose to proceed on the substitute verification (formula syntax valid, tapped successfully, brew's resolver picked the correct platform branch, independent checksum match on the real release asset) rather than block on it. The formula's *correctness* is verified; its *live-service behavior* under `brew services` is not, until CLT is updated on some machine | `brew install` → `Error: Your Command Line Tools are too outdated`; user confirmed via AskUserQuestion 2026-08-24 |

| — | All anchors verified (`src/daemon.ts:168` `ensureDaemon`, `src/daemon.ts:176` reuse-via-health-probe, `src/commands/backend.ts:6-30` `cmdBackend`/`ensureHealthy`, `src/commands/backend.test.ts:80-96` existing status tests, `backend/cmd/server/main.go:141-150` bind + `log.Fatal`, `docs/project-context.md` § Topology/Invariants, `gh api users/saketek` → 404, `gh repo list drayanaindra` → no homebrew-* repo, real `v0.1.0` `SHA256SUMS.txt` fetched live — all grepped/read/run directly), all targets (`homebrew-tap` repo + `Formula/saki.rb`, `backend.ts`/`backend.test.ts` edits, README/RELEASING.md additions) have a creating step and a named anchor location, no unchecked items on any state-changing step, no unknowns above LOW | self-audit |

**Blocking: 0 → READY.**

---

## Success Criteria

- [x] `ruby -c Formula/saki.rb` (in the `homebrew-tap` repo) → `Syntax OK`
- [ ] 🔲 BLOCKED (environment): `brew audit --strict --online drayanaindra/tap/saki` — this dev machine's Xcode Command Line Tools are too outdated for Homebrew's Ruby toolchain (`Error: Your Command Line Tools are too outdated`). Substitute run: `ruby -c` (above) + `brew tap drayanaindra/tap` succeeding (formula parses and loads under Homebrew's own Ruby, not just the standalone interpreter) — re-run the real audit once CLT is updated
- [ ] 🔲 BLOCKED (environment): `brew tap drayanaindra/tap && brew install drayanaindra/tap/saki && brew services start saki` — same CLT wall (`brew install` → `Error: Your Command Line Tools are too outdated`). Substitute run (all passed): `brew tap` succeeded; brew's resolver reported `Would install 1 formula: saki (0.1.0)` on the correct `on_macos`/`Hardware::CPU.arm?` branch for this machine; `curl -sL <the exact darwin-arm64 release URL in the formula> | shasum -a 256` matched the formula's embedded sha256 byte-for-byte; downloaded file is a real `Mach-O 64-bit executable arm64`. Re-run the real install + `brew services start` once CLT is updated
- [ ] 🔲 BLOCKED (environment): `curl -s http://127.0.0.1:8788/api/health` while the service is running — depends on the above; re-run once CLT is updated
- [x] `brew untap drayanaindra/tap` → exits 0 (cleanup performed — no install occurred to uninstall, since install itself was CLT-blocked)
- [x] `npx vitest run -t cmdBackend` → all `cmdBackend` tests pass, including the new "reachability probe" case and the rewritten "unavailable" case
- [x] `grep -q "brew services start saki" README.md && grep -q "saki backend stop" README.md` → exits 0 (coexistence note present)
- [x] `grep -q "homebrew-tap" docs/RELEASING.md` → exits 0 (release-time update step present)
- [x] `awk '/### I4/,/^$/' tasks/roadmap.md | grep -q 'Child plan:\*\* tasks/i4-homebrew-service-plan.md'` → exits 0
- [x] `npm run typecheck && npm test` → both exit 0 (no regressions outside the intended `backend.test.ts` change)

---

## Annotation Space

> Human: add notes, corrections, constraints here.
> Claude will revise plan and re-check the Blocking Set before proceeding.

---
Status: [x] Draft  [ ] Annotated  [ ] Approved  [ ] In Progress  [ ] Complete
Readiness Gate: [x] Evidence Ledger present and every blocking item cited  [x] Blocking Set empty  [x] Unknowns <= 2
