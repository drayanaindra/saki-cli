# Roadmap: saki

The portfolio for `saki-cli` — the **runtime** half of the orchestrator (the workflow lives in the
separate `saki-builder` repo). Every piece of work traces to an item here.

Items below are seeded from the README's "Not built yet" section: honest packaging gaps in v0.1,
not aspirational scope. Add more with `/saki-builder:add "<intent>"`.

- **PRD track** (Epic · Feature) → `/saki-builder:pickup <id>` writes and reviews the PRD.
- **Plan track** (Improvement · Bug) → skip the PRD, go straight to `/saki-builder:rplan`.

### F1 · Daemon lifecycle + unix-socket transport
**Type:** Feature · **Track:** PRD · **Status:** Shipped · **Owner:** unassigned · **Updated:** 2026-08-23
**Goal:** `saki` starts and supervises its own backend instead of requiring a hand-launched `./dist/saki-backend &`, and can talk to it over a unix socket rather than a TCP port.
**Child PRD:** prd-daemon-lifecycle-unix-socket-transport.md

### F2 · `saki doctor` — verify engine provisioning before a run
**Type:** Feature · **Track:** PRD · **Status:** Shipped · **Owner:** unassigned · **Updated:** 2026-08-16
**Goal:** One command that reports whether each engine (`claude`/`codex`/`opencode`) is on PATH and has `saki-builder` resolvable, so a missing profile is caught before a spawn is refused mid-run.
**Child PRD:** prd-saki-doctor-verify-engine-provisioning-before-a-run.md

### F3 · MCP surface (`saki mcp`)
**Type:** Feature · **Track:** PRD · **Status:** Shipped · **Owner:** unassigned · **Updated:** 2026-08-16
**Goal:** Expose the journey commands as MCP tools for agents that prefer a tool call to a shell, without forking the exit-code contract that shell callers depend on.
**Child PRD:** prd-mcp-surface-saki-mcp.md

### F4 · `saki doctor` — claude coverage
**Type:** Feature · **Track:** PRD · **Status:** Shipped · **Owner:** unassigned · **Updated:** 2026-08-20
**Goal:** Give claude a pre-dispatch provisioning verdict so doctor covers 3/3 engines instead of 2/3. Deferred from F2: not a mirror of the existing proofs — it needs `installed_plugins.json` **and** `settings.json` → `enabledPlugins` (the registry carries no enablement), plus a pinned resolution order for the two plugin-id spellings, which carry different versions.
**Child PRD:** prd-saki-doctor-claude-coverage.md
**Phase chain:** F2 (MVP) → F4 [trigger: F2 shipped and a claude-profile provisioning failure is reported]

### F5 · `saki doctor` — remediation text for opencode and claude
**Type:** Feature · **Track:** PRD · **Status:** Planned · **Owner:** unassigned · **Updated:** 2026-08-15
**Goal:** Every `failed` verdict carries a runnable fix, not just codex's. Deferred from F2: only codex has remediation text today (`backend/infra/codex.go:72-74`); opencode's error carries none (`backend/infra/opencode.go:58`) and claude has no proof yet, so authoring both is its own item.
**Child PRD:** —
**Phase chain:** F2 (MVP) → F5 [trigger: F2 shipped and an opencode `failed` verdict is seen without a fix]

### F6 · `saki init-env` — provision an engine profile
**Type:** Feature · **Track:** PRD · **Status:** Shipped · **Owner:** unassigned · **Updated:** 2026-08-20
**Goal:** Provisioning an engine profile (`opencode`/`claude`/`codex`) is a real `saki-cli` command instead of an external/manual step, so `saki doctor` and a run's `--engine` flag have something the CLI itself can set up, not just check.
**Target user & Job (JTBD):** As an operator setting up `saki-cli` in a repo (or an agent bootstrapping one), when I need a chosen engine's profile ready to run `/saki-builder:*` skills, I want one command to provision it so I can start running without manual file wrangling or a separate script.
**User flow:** `saki init-env --engine <codex|opencode|claude>` → scaffolds/verifies the profile → confirms via `saki doctor` → ready to run
**Success signal:** immediately after `saki init-env --engine <e>` completes, `saki doctor` reports that engine `ok`, with no manual file editing in between.
**Child PRD:** prd-saki-init-env-provision-engine-profile.md

### I1 · Publish `saki` to npm
**Type:** Improvement · **Track:** Plan · **Status:** Planned · **Owner:** unassigned · **Updated:** 2026-08-15
**What:** Ship a published package so consumers stop building from source. Covers the prepublish build, the `bin` wiring, `files` contents, and how the Go backend binary is distributed alongside the Node CLI.
**Child plan:** —

### I2 · `saki artifacts` companion orchestrator
**Type:** Improvement · **Track:** Plan · **Status:** Shipped · **Owner:** unassigned · **Updated:** 2026-08-16
**What:** `saki artifacts` depends on a companion orchestrator that is not part of this repo. A dependency-free loopback stub now serves the artifact, health, and session routes so the command can be exercised end-to-end here without weakening the real studio's session gate.
**Child plan:** tasks/i2-artifacts-companion-orchestrator-plan.md

### I3 · `saki doctor` — verify per-skill coverage, not just plugin presence
**Type:** Improvement · **Track:** Plan · **Status:** In-progress · **Owner:** unassigned · **Updated:** 2026-08-23
**What:** doctor / spawn-preflight only prove the saki-builder plugin is installed and enabled — never that the SPECIFIC skill/command a run is about to invoke actually exists in that install. Add per-command / plugin-version verification so a stale or partial install is caught before a run, not after.
**Repro / Context:** `backend/infra/claude.go:35` `ClaudeProfileProof` checks `installed_plugins.json` + `settings.json` only (plugin-level, any known id). `backend/infra/opencode.go:37` `OpencodePluginProof` checks `opencode.json` lists the plugin string only. `backend/infra/codex.go:60` `CodexSkillsProof`'s loose-install fallback treats presence of ONE sentinel file (`skills/build/SKILL.md`, `codexProofSkill`) as proof ALL saki-builder skills are present. No version pin exists anywhere (backend or `package.json`). Consequence: a stale/partial saki-builder install passes `saki doctor` `ok`, then a run silently fails when the model can't find the invoked command (the exact silent-no-op class `ErrEngineNotProvisioned` exists to prevent, just not reached for this case).
**Child plan:** tasks/saki-doctor-claude-skill-parity-plan.md
