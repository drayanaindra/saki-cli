import type { StudioClient } from './client.js'
import { EXIT, type ExitCode } from './exit.js'

// Everything a command needs, injected rather than reached for, so every command is unit-testable
// without a live studio, a real cwd, or captured stdout.
export interface Ctx {
  client: StudioClient
  cwd: string
  json: boolean
  write: (s: string) => void
  writeErr: (s: string) => void
  env: Record<string, string | undefined>
}

export type Command = (ctx: Ctx, positionals: string[], flags: Record<string, string | boolean>) => Promise<ExitCode>

export const OK: ExitCode = EXIT.OK

export function makeCtx(over: Partial<Ctx> & { client: StudioClient }): Ctx {
  return {
    cwd: process.cwd(),
    json: false,
    write: console.log,
    writeErr: console.error,
    env: process.env,
    ...over,
  }
}
