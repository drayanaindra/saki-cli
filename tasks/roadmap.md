# Roadmap: saki

The portfolio for `saki-cli` — the **runtime** half of the orchestrator (the workflow lives in the
separate `saki-builder` repo). Every piece of work traces to an item here.

Items below are seeded from the README's "Not built yet" section: honest packaging gaps in v0.1,
not aspirational scope. Add more with `/saki-builder:add "<intent>"`.

- **PRD track** (Epic · Feature) → `/saki-builder:pickup <id>` writes and reviews the PRD.
- **Plan track** (Improvement · Bug) → skip the PRD, go straight to `/saki-builder:rplan`.

### F1 · Daemon lifecycle + unix-socket transport
**Type:** Feature · **Track:** PRD · **Status:** Planned · **Owner:** unassigned · **Updated:** 2026-08-15
**Goal:** `saki` starts and supervises its own backend instead of requiring a hand-launched `./dist/saki-backend &`, and can talk to it over a unix socket rather than a TCP port.
**Child PRD:** —

### F2 · `saki doctor` — verify engine provisioning before a run
**Type:** Feature · **Track:** PRD · **Status:** Planned · **Owner:** unassigned · **Updated:** 2026-08-15
**Goal:** One command that reports whether each engine (`claude`/`codex`/`opencode`) is on PATH and has `saki-builder` resolvable, so a missing profile is caught before a spawn is refused mid-run.
**Child PRD:** —

### F3 · MCP surface (`saki mcp`)
**Type:** Feature · **Track:** PRD · **Status:** Planned · **Owner:** unassigned · **Updated:** 2026-08-15
**Goal:** Expose the journey commands as MCP tools for agents that prefer a tool call to a shell, without forking the exit-code contract that shell callers depend on.
**Child PRD:** —

### I1 · Publish `saki` to npm
**Type:** Improvement · **Track:** Plan · **Status:** Planned · **Owner:** unassigned · **Updated:** 2026-08-15
**What:** Ship a published package so consumers stop building from source. Covers the prepublish build, the `bin` wiring, `files` contents, and how the Go backend binary is distributed alongside the Node CLI.
**Child plan:** —

### I2 · `saki artifacts` companion orchestrator
**Type:** Improvement · **Track:** Plan · **Status:** Blocked · **Owner:** unassigned · **Updated:** 2026-08-15
**What:** `saki artifacts` depends on a companion orchestrator that is not part of this repo, so the command cannot be exercised end-to-end here. Blocked until that dependency is identified and either vendored, stubbed behind a port, or documented as an external requirement.
**Child plan:** —
