import { EXIT, fail } from './exit.js'

// The agent-runtime vocabulary, in the SHARED plumbing tier alongside args/client/ctx/exit/output/
// sse/resolve/routes — not in a command.
//
// It lives here because more than one command needs it: `saki run …` picks the runtime a run executes
// on, and `saki init-env` picks the runtime whose profile it provisions. Commands never import each
// other (CLAUDE.md), so the moment the second consumer appeared, the vocabulary had to move up rather
// than be reached for sideways — otherwise `saki init-env` would pull in the SSE streamer and the PRD
// resolver transitively, just to read a three-element array.
//
// E26 — validated by the Go backend at the boundary too (backend/adapter/http.go parseRunEngine):
// only these values, or empty for the claude default.
export const RUN_ENGINES = ['claude', 'opencode', 'codex'] as const
export type RunEngine = (typeof RUN_ENGINES)[number]

export function isRunEngine(v: string): v is RunEngine {
  return (RUN_ENGINES as readonly string[]).includes(v)
}

// Shared by index.ts's `startRun` helper, the `saki_run_start` MCP tool, and `saki init-env` — one
// message and one hint, so every surface rejects an unknown engine identically.
export function assertRunEngine(v: string): RunEngine {
  if (isRunEngine(v)) return v
  fail(`unknown engine: ${v}`, EXIT.USAGE, `expected one of ${RUN_ENGINES.join(', ')}`)
}
