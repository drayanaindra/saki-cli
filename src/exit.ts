// The CLI's exit-code contract. This is the CLI's real API surface: an agent branches on these
// numbers, so they are as load-bearing as stdout and must never be renumbered casually.
//
// REMOTE_FAILED exists because two studio routes report failure in a 200 body, not an HTTP status
// (`POST /api/switch-branch` and `POST /api/create-mr` return `{ok:false, error}` — see
// apps/server/src/index.ts:811,831). An agent branching on HTTP status alone would read a failed
// `git switch` as success; mapping `ok:false` to a non-zero exit is what keeps the contract honest.
export const EXIT = {
  OK: 0,
  ERROR: 1,
  USAGE: 2,
  UNREACHABLE: 3,
  NOT_FOUND: 4,
  REMOTE_FAILED: 5,
  AUTH_REQUIRED: 6,
} as const

export type ExitCode = (typeof EXIT)[keyof typeof EXIT]

// An error carrying the process exit code to use. Thrown anywhere, caught once in main().
export class CliError extends Error {
  readonly code: ExitCode
  // Optional second line of guidance (how to fix it) — printed under the message.
  readonly hint?: string

  constructor(message: string, code: ExitCode = EXIT.ERROR, hint?: string) {
    super(message)
    this.name = 'CliError'
    this.code = code
    this.hint = hint
  }
}

// Convenience thrower so call sites read as a guard rather than a construction.
export function fail(message: string, code: ExitCode = EXIT.ERROR, hint?: string): never {
  throw new CliError(message, code, hint)
}
