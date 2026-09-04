import { EXIT, fail } from './exit.js'

// The agent-runtime vocabulary, in the SHARED plumbing tier alongside args/client/ctx/exit/output/
// sse/resolve/routes — not in a command.
//
// It lives here because more than one command needs it: `saki run …` picks the runtime a run executes
// on, and `saki init-env` picks the runtime whose profile it provisions. Commands never import each
// other (CLAUDE.md), so the moment a second consumer appeared, the vocabulary had to move up rather
// than be reached for sideways — otherwise `saki init-env` would pull in unrelated command policy
// just to read the engine names.
//
// OMP is the Oh My Pi headless coding-agent runtime. Its plugin discovery can load the
// Claude-compatible saki-builder marketplace package, so it shares the canonical command prompts
// while exposing its own JSON event stream at the backend boundary.
export const RUN_ENGINES = ['claude', 'opencode', 'codex', 'omp'] as const
export const AUTO_ENGINE = 'auto' as const
export const RUN_ENGINE_CHOICES = [...RUN_ENGINES, AUTO_ENGINE] as const
export type RunEngine = (typeof RUN_ENGINES)[number]
export type RunEngineSelection = (typeof RUN_ENGINE_CHOICES)[number]

export function isRunEngine(v: string): v is RunEngine {
  return (RUN_ENGINES as readonly string[]).includes(v)
}

export function isRunEngineSelection(v: string): v is RunEngineSelection {
  return (RUN_ENGINE_CHOICES as readonly string[]).includes(v)
}

// Shared by index.ts's `startRun` helper, the `saki_run_start` MCP tool, and `saki init-env` — one
// message and one hint, so every surface rejects an unknown engine identically.
export function assertRunEngine(v: string): RunEngine {
  if (isRunEngine(v)) return v
  fail(`unknown engine: ${v}`, EXIT.USAGE, `expected one of ${RUN_ENGINES.join(', ')}`)
}

export function assertRunEngineSelection(v: string): RunEngineSelection {
  if (isRunEngineSelection(v)) return v
  fail(`unknown engine: ${v}`, EXIT.USAGE, `expected one of ${RUN_ENGINE_CHOICES.join(', ')}`)
}
