# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.5.0] - 2026-09-04

### Added

- Added Oh My Pi (`omp`) as a fourth engine, including isolated profile provisioning, doctor
  verification, JSON event rendering, and Hermes agent guidance.

## [0.4.1] - 2026-09-02

### Fixed

- `saki roadmap add` now supplies the complete autonomous intake shape, so headless runs do not stop
  at interactive confirmation for either OpenCode or Codex.

## [0.4.0] - 2026-09-02

### Added

- **F7** — `saki build <roadmap-id> --follow` now drives a durable, restart-safe workflow across
  pickup, proto/lock, build, QA, review, and verified completion, with workflow deduplication and
  explicit continuation for parked or awaiting-decision work.
- Added the agent-facing `.claude/skills/saki-cli/SKILL.md` operating contract.

### Fixed

- **F5** — `saki doctor` and spawn-preflight now provide remediation text for OpenCode and Claude.
- Hardened daemon startup state-path handling and startup ordering for isolated lifecycle runs.

## [0.3.1] - 2026-08-26

### Fixed

- OpenCode proto runs now forward targets such as `F3` to the proto skill correctly.
- OpenCode spawn-refusal errors now include the engine remediation command.

## [0.3.0] - 2026-08-25

### Added

- `saki roadmap add` / `saki roadmap init` now accept `--engine <e>` and `--profile <dir>`, same
  as `saki run <verb>` — previously these two spawns always used the default engine profile even
  when the rest of a session pinned a specific one.

## [0.2.0] - 2026-08-24

### Added

- `saki roadmap init` — scaffolds `tasks/roadmap.md` by spawning `/saki-builder:roadmap init`,
  same pattern as `saki roadmap add`, instead of requiring the skill to be invoked directly.
- `saki genesis "<product idea>" [--restart]` — starts a product from scratch by spawning
  `/saki-builder:genesis`, giving the greenfield entry point a real CLI verb.

## [0.1.0] - 2026-08-23

### Added

- **F1** — Daemon lifecycle + unix-socket transport: `saki` starts and supervises its own backend
  instead of requiring a hand-launched `./dist/saki-backend &`, and talks to it over a unix socket
  when available (falls back to TCP).
- **F2** — `saki doctor`: reports whether each engine (`claude`/`codex`/`opencode`) is on `PATH` and
  has `saki-builder` resolvable, so a missing profile is caught before a spawn is refused mid-run.
- **F3** — MCP surface (`saki mcp`): exposes the journey commands as MCP tools for agents that
  prefer a tool call to a shell.
- **F4** — `saki doctor` claude coverage: a pre-dispatch provisioning verdict for claude, covering
  3/3 engines instead of 2/3.
- **F6** — `saki init-env`: provisions an engine profile (`opencode`/`claude`/`codex`) as a real
  `saki-cli` command instead of an external/manual step.
- **I2** — `saki artifacts` companion orchestrator: a dependency-free loopback stub serves the
  artifact, health, and session routes so the command can be exercised end-to-end without the
  external studio.

### Fixed

- **I3** — `saki doctor` / spawn-preflight now verify per-skill coverage, not just plugin presence,
  catching a stale or partial `saki-builder` install before a run instead of after.

[Unreleased]: https://github.com/drayanaindra/saki-cli/compare/v0.5.0...HEAD
[0.5.0]: https://github.com/drayanaindra/saki-cli/releases/tag/v0.5.0
[0.4.1]: https://github.com/drayanaindra/saki-cli/compare/v0.4.0...v0.4.1
[0.3.1]: https://github.com/drayanaindra/saki-cli/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/drayanaindra/saki-cli/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/drayanaindra/saki-cli/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/drayanaindra/saki-cli/releases/tag/v0.1.0
