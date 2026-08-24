# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/drayanaindra/saki-cli/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/drayanaindra/saki-cli/releases/tag/v0.1.0
