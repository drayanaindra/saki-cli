import { EXIT, fail } from './exit.js'

// A minimal argv parser. Deliberately hand-rolled: this module has zero runtime dependencies so it
// parses every command's args, including `saki mcp`, before that command's own (lazy-loaded) deps
// are ever pulled in.

export type FlagKind = 'string' | 'boolean'
export type FlagSpec = Readonly<Record<string, FlagKind>>

export interface ParsedArgs {
  positionals: string[]
  flags: Record<string, string | boolean>
  help: boolean
}

// `-h` / `--help` are accepted for every command without being declared in a spec, so a user can
// always ask for help even when the rest of the argv is wrong.
function isHelp(token: string): boolean {
  return token === '-h' || token === '--help'
}

export function parseArgs(argv: string[], spec: FlagSpec): ParsedArgs {
  const positionals: string[] = []
  const flags: Record<string, string | boolean> = {}
  let help = false

  for (let i = 0; i < argv.length; i++) {
    const token = argv[i]

    // Everything after a bare `--` is positional, even if it looks like a flag. This is what lets
    // `saki roadmap add -- --feature-ish text` pass through free-text that starts with dashes.
    if (token === '--') {
      positionals.push(...argv.slice(i + 1))
      break
    }

    if (isHelp(token)) {
      help = true
      continue
    }

    if (!token.startsWith('--')) {
      positionals.push(token)
      continue
    }

    // Split only on the FIRST `=` so a value may itself contain `=` (paths, query strings).
    const body = token.slice(2)
    const eq = body.indexOf('=')
    const name = eq === -1 ? body : body.slice(0, eq)
    const inlineValue = eq === -1 ? undefined : body.slice(eq + 1)

    // hasOwn, not `spec[name]` — a bare property read walks Object.prototype, so `--constructor`
    // and `--toString` resolve to inherited members and get accepted (or produce a nonsense
    // "needs a value") instead of "unknown flag".
    const kind = Object.hasOwn(spec, name) ? spec[name] : undefined
    if (!kind) {
      fail(`unknown flag: --${name}`, EXIT.USAGE, `run with --help to see the accepted flags`)
    }

    if (kind === 'boolean') {
      // A boolean never consumes the next token — `--json extra` leaves `extra` positional.
      flags[name] = inlineValue === undefined ? true : inlineValue !== 'false'
      continue
    }

    if (inlineValue !== undefined) {
      flags[name] = inlineValue
      continue
    }

    // Space-form: take the next token, but refuse to swallow another flag or run off the end —
    // both mean the user forgot the value, and silently accepting would produce a confusing
    // downstream error instead of a clear usage one.
    const next = argv[i + 1]
    if (next === undefined || next.startsWith('--')) {
      fail(`flag --${name} needs a value`, EXIT.USAGE)
    }
    flags[name] = next
    i++
  }

  return { positionals, flags, help }
}
