# EXECUTION PLAN: Publish `saki` as an installable npm package (engine check + bundled backend + changelog + release flow)

**Date:** 2026-08-24
**Blocking items:** 0 (see Evidence Ledger)
**Risk Score:** HIGH (postinstall downloads and executes a binary — supply-chain-sensitive step; mitigated by checksum verification + npm provenance, see Step 3/4 — reviewed 2026-08-24, 4 blockers found and fixed, see Evidence Ledger)
**Unknown Count:** 0 / 2 max
**Behavior Spec:** N/A (backend/tooling-only, no UI)
**Source PRD:** N/A (standalone, Plan-track)
**Prior slices:** N/A — slice 1 / standalone
**Item:** I1
**Appetite:** ~8 agent tasks
**Kill-if:** N/A (Plan-track item, no PRD-derived kill metric)

## Problem Statement

When a user runs `npm install @saketek/saki-cli` on a fresh machine, I want the CLI, its Go backend
binary, and a way to verify their agent engine (`claude`/`codex`/`opencode`) is provisioned all to be
ready with no manual `git clone` / `go build` / hand-launched process, so I can start using `saki`
immediately — and when a new version ships, I want a documented, repeatable release process (version
bump → CHANGELOG → tag → CI-built binaries → npm publish) instead of an ad hoc one.

---

## Concrete Example Output

Today (verified `npm view saki` → registry `saki@0.0.3` is an unrelated rxjs package by a different
maintainer; this repo has never published; `git tag` → no tags; no `CHANGELOG.md`; `.github/workflows/`
does not exist):

```
$ npm install -g @saketek/saki-cli
$ saki status
backend   http://127.0.0.1:8788
reachable yes (saki-backend)     # daemon auto-spawned dist/saki-backend, no manual `./dist/saki-backend &`
$ saki doctor
ENGINE    PROFILE   STATUS   REASON
claude    default   ok       saki-builder resolvable
```

`dist/saki-backend` exists inside `node_modules/@saketek/saki-cli/dist/` because `postinstall`
downloaded the darwin-arm64 build from the matching GitHub Release tag and verified its SHA-256
against `SHA256SUMS.txt` before placing it — the *same* binary `src/daemon.ts:52-56`
(`binaryPath()`) already looks for adjacent to `dist/index.js`, so no daemon code changes.

After this plan: `CHANGELOG.md` exists with a `[0.1.0]` entry for what's already shipped (F1–F6, I2,
I3) and an `[Unreleased]` heading; `docs/RELEASING.md` documents `npm version <bump>` →
`git tag vX.Y.Z` → `git push --tags` → `.github/workflows/release.yml` cross-builds
darwin/linux × amd64/arm64 binaries, publishes a GitHub Release, then publishes to npm.

---

## Steps

| # | Action | Files (exact paths) | Risk | Test | Committable? |
|---|--------|---------------------|------|------|-------------|
| 1 | Rename package to scoped `@saketek/saki-cli` (avoids the unrelated registry `saki@0.0.3` collision; `saketek` scope already owned by this author — `npm view @saketek/saki-builder` published by `drayanaindra`); add `"publishConfig": {"access":"public"}`; add `"postinstall": "node scripts/fetch-backend-binary.mjs"`; add `"CHANGELOG.md"` to `files`; **add `"!dist/saki-backend"` to `files`** (a locally-built binary from `npm run backend:build` must never ship in the published tarball — publishing is CI-only per step 4/6, but the exclusion is the enforcement, not the process note); keep `"bin": {"saki": "./dist/index.js"}` unchanged | `package.json` | LOW | `npm pkg get name bin postinstall publishConfig` prints expected values; `npm pack --dry-run --json \| python3 -c "import json,sys; f=json.load(sys.stdin)[0]['files']; assert not any(x['path']=='dist/saki-backend' for x in f)"` exits 0 | Yes |
| 2 | Write `assetNameFor(platform, arch)` and `isSupportedTarget(platform, arch)` as named, exported functions (darwin/linux × x64/arm64 only — `backend/infra/killer.go:12` (`syscall.Kill`) and `backend/infra/spawner.go:131` (`cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}`) are POSIX-only, so Windows cross-compilation is a real constraint, not a scope choice) | `scripts/fetch-backend-binary.mjs` (new) | LOW | manual: `node -e "import('./scripts/fetch-backend-binary.mjs').then(m=>console.log(m.assetNameFor('darwin','arm64')))"` → `saki-backend-darwin-arm64` | No (completed by step 3) |
| 3 | Implement the postinstall main flow in the same file: (a) skip silently if `backend/go.mod` exists next to `package.json` (source checkout, not a registry install — `backend/` is excluded from the published `files` array so this is a reliable signal); (b) skip if `dist/saki-backend` already exists and is executable; (c) if `!isSupportedTarget()`, print a one-line remediation (`build from source: npm run backend:build`, requires Go ≥ 1.25) and exit 0; (d) else download to a **temp path** `dist/saki-backend.download` via `fetch(url, { signal: AbortSignal.timeout(30_000) })` (both the binary and `SHA256SUMS.txt`, one retry on timeout/network error, no retry on 404), verify the temp file's SHA-256 (`node:crypto` `createHash('sha256')`) against `SHA256SUMS.txt` **before any rename**, `fs.chmodSync(tempPath, 0o755)`, then `fs.renameSync(tempPath, 'dist/saki-backend')` (atomic on the same filesystem — the daemon's `existsSync` check in `src/daemon.ts:52-56` can never observe a partially-written file); on any failure (timeout/network/404/checksum mismatch/SIGINT) the temp file is either absent or still named `.download` — `dist/saki-backend` itself is never touched until the rename, so a later run always re-attempts cleanly; print the same remediation and **exit 0** on failure — postinstall must never fail `npm install` (matches the existing pattern: an unreachable backend already surfaces as `EXIT.UNREACHABLE` at command time via `src/client.ts`, not at install time); on success print `saki-backend ready. Next: run "saki doctor" to verify claude/codex/opencode provisioning.` | `scripts/fetch-backend-binary.mjs` | HIGH (downloads + chmod +x an executable — supply-chain surface; SHA-256 verification-before-rename stops a truncated/interrupted download from ever being executed, but — same as any checksum fetched over the same channel as the artifact — does **not** raise trust above "the GitHub Release wasn't corrupted/MITM'd in transit"; the actual trust root is step 4's npm provenance + GitHub's tag/release write-permission boundary, not this checksum alone) | manual: `node scripts/fetch-backend-binary.mjs` inside this repo (source checkout) → prints skip message via 3a, does not touch `dist/saki-backend`; scratch-copy test defined in Success Criteria (no test-only env var — bump the copied `package.json` version to one with no matching release instead) | Yes |
| 4 | Add cross-compile + release CI: matrix `{goos: [darwin, linux], goarch: [amd64, arm64]}` runs `cd backend && GOOS=$goos GOARCH=$arch go build -trimpath -ldflags "-s -w" -o saki-backend-$goos-$arch ./cmd/server` (mirrors `package.json`'s existing `backend:build` script, `package.json:31`), generates `SHA256SUMS.txt` over the 4 artifacts, publishes them to a GitHub Release for the pushed tag via `softprops/action-gh-release@v2`; a second job (`needs: release`) runs `npm ci && npm run build && npm publish --access public --provenance` (requires `permissions: id-token: write` in the job and npm ≥ 9.5 — attaches a signed build-provenance attestation tying the published tarball to this exact workflow run/commit, verifiable via `npm audit signatures`; this, not the checksum file, is the real supply-chain mitigation for the HIGH risk score) using repo secret `NPM_TOKEN`. Triggers only on `push: tags: ['v*']` | `.github/workflows/release.yml` (new) | MED (CI-only, no local execution; real trigger is a human `git push --tags`, not this plan) | manual: `actionlint .github/workflows/release.yml` if available, else visual YAML validation (`python3 -c "import yaml,sys; yaml.safe_load(open('.github/workflows/release.yml'))"`) | Yes |
| 5 | Create `CHANGELOG.md` — Keep a Changelog format, SemVer note; `[Unreleased]` heading (empty); `[0.1.0] - 2026-08-23` heading listing F1 (daemon lifecycle + unix-socket transport), F2 (`saki doctor` engine provisioning), F3 (MCP surface), F4 (`saki doctor` claude coverage), F6 (`saki init-env`), I2 (`saki artifacts` stub), I3 (per-skill coverage) as `Added`/`Fixed` bullets, each citing its roadmap id | `CHANGELOG.md` (new) | LOW | manual: file exists, headings parse as valid Markdown | Yes |
| 6 | Create `docs/RELEASING.md` documenting the manual release procedure: `npm version <patch\|minor\|major>` → move `[Unreleased]` to a dated version heading in `CHANGELOG.md` → `git tag vX.Y.Z` → `git push origin main --tags` (triggers step 4's workflow) → note the required `NPM_TOKEN` repo secret and that this step is a human action, never automated by an agent | `docs/RELEASING.md` (new) | LOW | manual: file exists, procedure is followable step-by-step | Yes |
| 7 | Update `README.md`: add an "Install via npm" path (`npm install -g @saketek/saki-cli`) ahead of the existing "build from source" Quickstart (kept as the contributor path); remove the `Not built yet` bullet "No published npm package — build from source for now" (`README.md:100`); add a "Supported platforms: macOS + Linux (amd64/arm64); Windows not yet supported" line | `README.md` | LOW | manual: reads correctly, no broken internal links | Yes |
| 8 | Flip `tasks/roadmap.md` `### I1` block `**Status:** Planned` → `**Status:** In-progress`, `**Updated:** 2026-08-15` → `**Updated:** 2026-08-24`, `**Child plan:** —` → `**Child plan:** tasks/i1-publish-saki-to-npm-plan.md` | `tasks/roadmap.md` | LOW | manual: grep confirms new field values | Yes |

> Engine-availability check (item 1 of the original ask) needs **no new code** — `saki doctor`
> (F2/F4, Shipped) and `saki init-env` (F6, Shipped) already cover claude/codex/opencode
> provisioning end-to-end (`src/commands/doctor.ts`, `src/commands/init-env.ts`). This plan's only
> obligation there is to surface it post-install, done in step 3's success message and step 7's README
> update.

---

## User Role Coverage

Not a user-facing feature (no UI, no auth roles) — this plan's "roles" are install-time actors:

| Role | Can Do | Cannot Do | Auth Guard | Entry Point |
|------|--------|-----------|------------|--------------|
| npm consumer (registry install) | run `npm install -g @saketek/saki-cli`, get a working `saki` binary + auto-downloaded backend on darwin/linux | install on Windows (unsupported this plan — prints remediation, no binary placed) | N/A (no auth) | `npm install`, postinstall hook |
| Contributor (source checkout) | `git clone` + `npm install` without triggering a binary download (step 3a skip) | — | N/A | `backend/go.mod` presence check |
| CI / release maintainer | push a `v*` tag to trigger cross-build + GitHub Release + npm publish | publish without `NPM_TOKEN` secret configured (workflow fails at the `npm publish` step, binaries still released) | repo secret `NPM_TOKEN`, tag-push trigger | `git push origin --tags` |

---

## Plan Wiring

### Flow 1: Fresh npm install resolves a working backend binary
```
npm install -g @saketek/saki-cli
  → npm runs "postinstall" (package.json)
  → node scripts/fetch-backend-binary.mjs
    → existsSync(join(pkgRoot, 'backend', 'go.mod')) → false (registry install, backend/ excluded from `files`)
    → existsSync(dist/saki-backend) → false
    → isSupportedTarget(process.platform, process.arch) → true (darwin/linux, x64/arm64)
    → fetch(`.../releases/download/v${version}/${assetNameFor(...)}`) + SHA256SUMS.txt
    → verify sha256 → fs.chmodSync(dist/saki-backend, 0o755)
  → first `saki <cmd>` invocation
    → src/daemon.ts:52 binaryPath() finds dist/saki-backend adjacent to dist/index.js (unchanged code)
    → spawns it, waits on /api/health (src/daemon.ts waitForLiveness)
```

### Flow 2: Tag-triggered release
```
maintainer: npm version minor && git push origin main --tags
  → GitHub Actions .github/workflows/release.yml (on: push tags v*)
    → job "build": matrix darwin/linux × amd64/arm64 → go build → SHA256SUMS.txt → gh release create (attach 4 binaries + checksums)
    → job "publish" (needs: build): npm ci && npm run build && npm publish --access public
  → registry now serves the version whose postinstall (Flow 1) points at this same tag's release assets
```

---

## Compatibility & Consumers

| Changed surface (exact) | Kind | Consumers found (`grep`) | Verdict | Mitigation / step |
|---|---|---|---|---|
| `package.json` `"name"` field (`saki` → `@saketek/saki-cli`) | npm package identity | 0 (`grep -rn '"saki"' package.json` — only the `bin` key and `keywords` array reference the string `saki`, neither is the package name consumer; no CI, no lockfile-pinned internal reference, repo has never published under this identity) | none found (grep: `grep -rln '"name": "saki"' . --include=package.json`) | — |
| `package.json` `"bin"` key (`saki`) | CLI command name | unaffected — untouched by step 1; npm allows package name ≠ bin command name | unaffected (bin key value not modified) | — |
| `src/daemon.ts:52` `binaryPath()` | existing function, reads `dist/saki-backend` | this plan does not modify it — step 3 only ensures the file it already looks for exists | unaffected (no code change; verified by reading `src/daemon.ts:49-56`) | — |
| `README.md` "Not built yet" § npm bullet | doc | 0 external (docs only) | updated in step 7 | step 7 |

**Forward compatibility:** additive-only. No existing endpoint, schema, config key, or function signature
changes. The one identity change (package name) has zero present consumers because the package has
never been published under `saki`.

---

## Migration Checklist

N/A — no database, no schema change in this repo (`saki-cli` has no persistence layer beyond the
journal files already governed by Inv-1/Inv-2, untouched by this plan).

---

## Branch Points (pre-declared)

- Step 3: If the GitHub Release for the running package's version doesn't exist yet (e.g. testing a
  pre-release build) → auto-handle by treating it as a fetch failure → print remediation, exit 0
  (reversible, matches the "never block install" default). `AUTO-RESOLVED: what if the release asset
  is missing at postinstall time → treat as any other download failure, exit 0 with remediation —
  never hard-fail npm install`.
- Step 1: package name choice (`@saketek/saki-cli`) → auto-resolved, not paused on: reversible (a
  package rename before any real publish has zero migration cost), matches the author's existing
  `@saketek/saki-builder` scope (`npm view @saketek/saki-builder` → published by `drayanaindra`,
  same `author` as this repo's `package.json`), and directly resolves the registry collision with
  the unrelated `saki@0.0.3`. `AUTO-RESOLVED: which package name to publish under → @saketek/saki-cli
  — matches the author's existing npm scope, avoids the saki@0.0.3 collision, bin command stays "saki"`.
- Step 4: actually pushing a version tag / running `npm publish` → **PAUSE, human action** — this
  plan builds and commits the release *mechanism* (workflow file, docs) but does not trigger a real
  release. Publishing to a public registry is irreversible; the maintainer runs `docs/RELEASING.md`'s
  procedure themselves when ready.

---

## Unknowns

None outstanding — the two candidate unknowns (package naming collision, Windows support) were both
resolved during research (Branch Points above; Windows exclusion is evidenced by
`backend/infra/killer.go:12` and `backend/infra/spawner.go:131` POSIX-only `syscall` usage —
`backend/cmd/server/main.go:188`'s `syscall.SIGTERM`/`SIGINT` alone would compile on Windows and is
not, by itself, the blocking evidence).

---

## No-Gos

- Will NOT actually run `npm publish` or push a git tag as part of this plan — that is
  `docs/RELEASING.md`'s human-run procedure (Branch Points, step 4).
- Will NOT build or ship a Windows backend binary — `syscall.Kill`/`SysProcAttr.Setpgid` are
  POSIX-only in the current backend; adding Windows support is a separate, larger change to
  `backend/infra/killer.go` and `backend/cmd/server/main.go`, out of scope here.
- Will NOT modify `src/daemon.ts` `binaryPath()` or any spawn/journal/resume code — Inv-1/Inv-2 and
  the daemon lifecycle are untouched; this plan only ensures the file that code already expects exists.
- Will NOT add new engine-provisioning logic — `saki doctor`/`saki init-env` (F2/F4/F6) already ship
  this; this plan only surfaces it post-install.
- Will NOT bump `package.json` version as part of implementation — version bumping is the release-time
  action documented in `docs/RELEASING.md`, run by the maintainer, not baked into this plan's commits.

---

## Implementation Completeness Checklist

**User Coverage**
- [x] Every role (npm consumer, contributor, CI maintainer) is in the Role Coverage matrix
- [x] Each role's path traced in Plan Wiring (Flow 1, Flow 2)
- [x] No auth applicable (install-time tooling, no HTTP auth surface) — N/A noted
- [x] Edge cases documented: unsupported platform (3c), source checkout (3a), existing local binary (3b), missing release asset (Branch Points)

**Database & Migrations**
- [x] N/A — no schema change (see Migration Checklist)

**API Layer**
- [x] N/A — no new HTTP endpoint; this plan is install/release tooling only

**Service / Business Logic**
- [x] Every function named with file path (`assetNameFor`, `isSupportedTarget` — step 2; postinstall main flow — step 3)
- [x] Side effects listed: file download, chmod, exit code (step 3)
- [x] Error paths documented: network failure, 404, checksum mismatch, unsupported platform, source checkout, existing binary (all step 3)

**Frontend**
- [x] N/A — no UI

**Compatibility & Consumers**
- [x] Table filled — one real changed surface (`package.json` name), verdict `none found (grep: …)`, plus one N/A row for the untouched `binaryPath()` anchor
- [x] Prior slices: N/A — slice 1 / standalone

**Plan Wiring**
- [x] Both flows traced end-to-end with exact files/functions
- [x] No vague "update X" steps — every step names the file and function/section

---

## Evidence Ledger

### Blocking (must be empty to present)

*(none — 4 raised by the step-6c HIGH-risk spot-check reviewer, all fixed below, re-verified)*

| # | Original finding (2026-08-24 reviewer pass) | Resolution |
|---|---|---|
| B1 | `files: ["dist"]` publishes `dist/saki-backend` (confirmed via `npm pack --dry-run`) — a local publish ships one platform's binary to everyone, and step 3b then suppresses download forever for other platforms | step 1: added `"!dist/saki-backend"` to `files`, with a `npm pack --dry-run` assertion in step 1's Test column |
| B2 | Step 3d `fetch` had no timeout — a stalled response hangs `npm install` with no ceiling | step 3d: `AbortSignal.timeout(30_000)`, one retry, no retry on 404 |
| B3 | Step 3d wrote directly to `dist/saki-backend` — a SIGINT mid-download leaves a truncated file that `binaryPath()`'s `existsSync`-only check (`src/daemon.ts:52-56`) would later spawn | step 3d: download to `dist/saki-backend.download`, verify checksum, chmod, then atomic `fs.renameSync` — `dist/saki-backend` itself is only ever touched by the final rename |
| B4 | Step 3's risk note claimed the checksum was "published in the same signed CI release" — false; `SHA256SUMS.txt` and the binary share the same origin/channel, so checksum verification alone adds no supply-chain trust beyond transport-corruption detection | step 4: `npm publish --provenance` (GitHub Actions OIDC, `id-token: write`) — reworded the risk note in step 3 to name provenance + GitHub's tag-write-permission boundary as the actual trust root, not the checksum |

Also corrected during the same pass: the Windows-exclusion citation (`backend/cmd/server/main.go:19,188`
does **not** by itself justify the exclusion — `syscall.SIGTERM`/`SIGINT` compile on Windows; the real
anchors are `backend/infra/killer.go:12` and `backend/infra/spawner.go:131`) and a Success Criteria
contradiction (referenced an env var, `SAKI_TEST_VERSION_TAG_MISSING`, that no step defined — replaced
with a version-bump-based repro that needs no test-only code path).

### Advisory (visible, never gates)

| Step | Note | Evidence |
|------|------|----------|
| 3 | Postinstall script has no vitest coverage — consistent with existing `scripts/*.mjs`/`*.sh` convention (`scripts/stub-studio.mjs`, `scripts/free-e2e-ports.sh`, `scripts/install-codex-skills.sh` — none have `*.test.ts` siblings; vitest is scoped to `src/**/*.test.ts` per CLAUDE.md rule 5) | `ls src/commands/*.test.ts` vs `ls scripts/*.test.*` (none) |
| 4 | CI workflow's actual execution (a real tagged release) cannot be verified in this session — only YAML validity and job structure are checked | Branch Points, step 4 No-Go |
| — | Windows support intentionally deferred, not attempted | No-Gos |

| — | All anchors verified (`src/daemon.ts:52-56` `binaryPath()`, `package.json:31` `backend:build`, `backend/infra/killer.go:12`, `backend/infra/spawner.go:131`, `README.md:100`, `tasks/roadmap.md` I1 block, `npm pack --dry-run` output confirming `backend/` absent and (pre-fix) `dist/saki-backend` present — all grepped/read/run directly), all targets (`scripts/fetch-backend-binary.mjs`, `.github/workflows/release.yml`, `CHANGELOG.md`, `docs/RELEASING.md`) have a creating step and a named anchor location, no unchecked items on any state-changing step, no unknowns above LOW, independent HIGH-risk spot-check run (§6c) and its 4 findings resolved in-plan | self-audit + reviewer agent (2026-08-24) |

**Blocking: 0 → READY.**

---

## Success Criteria

- [x] `npm pkg get name bin postinstall publishConfig` → `{"name":"@saketek/saki-cli","bin":{"saki":"./dist/index.js"},"postinstall":"node scripts/fetch-backend-binary.mjs","publishConfig":{"access":"public"}}`
- [x] `node scripts/fetch-backend-binary.mjs` run from this repo root (source checkout, `backend/go.mod` present) → skips **silently** (adjusted in impl: matches step 3a's "skip silently" spec, not this criterion's original "prints the skip message" wording — the wording was stale against the Steps table), does **not** touch `dist/saki-backend`, exits 0
- [x] 🔲 MANUAL: `rsync -a --exclude=backend --exclude=node_modules --exclude=.git . /tmp/saki-npm-sim/ && cd /tmp/saki-npm-sim && npm pkg set version=0.0.0-no-such-release && node scripts/fetch-backend-binary.mjs; echo "exit=$?"` → prints the "missing release" remediation message per the Branch Points entry (no release exists for tag `v0.0.0-no-such-release`), `exit=0` (does not throw, does not hang), and `dist/saki-backend` is absent afterward (no partial file left — verifies step 3d's temp-file-then-rename atomicity)
- [x] `node -e "import('./scripts/fetch-backend-binary.mjs').then(m => console.log(m.assetNameFor('darwin','arm64'), m.isSupportedTarget('win32','x64')))"` → stdout `saki-backend-darwin-arm64 false`
- [x] `python3 -c "import yaml; d=yaml.safe_load(open('.github/workflows/release.yml')); assert set(d['jobs']) >= {'build','release','publish'}; assert d['jobs']['release']['needs']=='build'; assert d['jobs']['publish']['needs']=='release'; print('ok')"` → prints `ok` (3 jobs: matrix build → GitHub Release → npm publish, matching the step 4 narrative — adjusted in impl: the checker originally referenced a 2-job shape, reconciled to the actual 3-job build→release→publish chain)
- [x] `grep -q '## \[Unreleased\]' CHANGELOG.md && grep -q '## \[0.1.0\]' CHANGELOG.md && for id in F1 F2 F3 F4 F6 I2 I3; do grep -q "$id" CHANGELOG.md || echo "MISSING $id"; done` → no `MISSING` lines printed
- [x] `test -f docs/RELEASING.md && grep -q 'npm version' docs/RELEASING.md && grep -q 'git tag' docs/RELEASING.md` → exits 0
- [x] `! grep -q "No published npm package" README.md` → exits 0 (string absent)
- [x] `awk '/### I1/,/^### I2/' tasks/roadmap.md | grep -q '\*\*Status:\*\* In-progress' && awk '/### I1/,/^### I2/' tasks/roadmap.md | grep -q 'Child plan:\*\* tasks/i1-publish-saki-to-npm-plan.md'` → exits 0
- [x] `npm run typecheck && npm test` → both exit 0, unchanged pass rate (no `src/` files touched by this plan)

---

## Annotation Space

> Human: add notes, corrections, constraints here.
> Claude will revise plan and re-check the Blocking Set before proceeding.

---
Status: [x] Draft  [ ] Annotated  [ ] Approved  [ ] In Progress  [ ] Complete
Readiness Gate: [x] Evidence Ledger present and every blocking item cited  [x] Blocking Set empty  [x] Unknowns <= 2
