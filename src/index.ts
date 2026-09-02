#!/usr/bin/env node
import { realpathSync } from 'node:fs'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { parseArgs, type FlagSpec } from './args.js'
import { EXIT, CliError, type ExitCode } from './exit.js'
import { StudioClient } from './client.js'
import { resolveCwd } from './resolve.js'
import { makeCtx, type Ctx } from './ctx.js'
import { cmdStatus } from './commands/status.js'
import { cmdRuns, cmdRunStop, cmdRunTail } from './commands/runs.js'
import {
  cmdRunStart,
  assertRunVerb,
  RUN_VERBS,
  targetIsOptional,
  takesNoTarget,
  assertRunEngine,
  RUN_ENGINES,
  supportsHeal,
  cmdRunContinue,
  type RunVerb,
  type RunEngine,
} from './commands/run.js'
import { cmdRoadmapList, cmdRoadmapAdd, cmdRoadmapInit } from './commands/roadmap.js'
import { cmdGenesis } from './commands/genesis.js'
import { cmdPrdShow, cmdPrdLock } from './commands/prd.js'
import { cmdProto } from './commands/proto.js'
import {
  cmdBranch,
  cmdBranchList,
  cmdBranchSwitch,
  cmdMrCreate,
  cmdScreenshots,
  cmdWorkitems,
} from './commands/repo.js'
import { cmdArtifacts } from './commands/artifacts.js'
import { cmdDoctor } from './commands/doctor.js'
import { cmdInitEnv } from './commands/init-env.js'
import { cmdBackend } from './commands/backend.js'
import { ensureDaemon, readDaemonState } from './daemon.js'

const packageVersion: string = createRequire(import.meta.url)('../package.json').version

// Flags every command accepts.
const COMMON: FlagSpec = { json: 'boolean', cwd: 'string' }

interface CommandDef {
  // Space-separated command path, matched longest-first ('run build' before 'run').
  path: string[]
  usage: string
  summary: string
  flags: FlagSpec
  run: (ctx: Ctx, rest: string[], flags: Record<string, string | boolean>) => Promise<ExitCode>
}

// Flags a run-start accepts, shared by `run <verb>` and the top-level aliases.
const RUN_FLAGS: FlagSpec = { ...COMMON, follow: 'boolean', profile: 'string', engine: 'string' }

// The one place a run-start is dispatched. Both `saki run build X` and the alias `saki build X`
// route through here, so the two forms cannot drift in argument handling or validation.
function startRun(
  ctx: Ctx,
  verb: RunVerb,
  args: string[],
  flags: Record<string, string | boolean>,
): Promise<ExitCode> {
  // Exactly one argument. Joining stray positionals silently folded them into the prompt AND the
  // laneKey, producing a lane nothing else matches instead of an obvious usage error.
  if (args.length > 1) {
    throw new CliError(
      `run ${verb} takes exactly one argument (got ${args.length}: ${args.join(' ')})`,
      EXIT.USAGE,
      'quote it if the value contains spaces',
    )
  }
  // Validate the engine here rather than letting the backend 422 — a typo should be a usage error
  // with the valid values named, not a round-trip.
  const engine = typeof flags.engine === 'string' ? assertRunEngine(flags.engine) : undefined
  return cmdRunStart(ctx, verb, args[0] ?? '', {
    follow: flags.follow === true,
    profile: typeof flags.profile === 'string' ? flags.profile : undefined,
    heal: flags.heal === true,
    engine,
  })
}

// Top-level aliases for the verbs typed most often: `saki build E12` instead of
// `saki run build E12`. `proto` is deliberately absent — `saki proto <id>` already means "print the
// URL of an already-rendered gallery", and one name cannot mean both that and "render one".
const ALIASED_VERBS: RunVerb[] = [
  'build',
  'pickup',
  'rplan',
  'prd-review',
  'rplan-review',
  'approved',
  'qa',
  'reviewer',
  'wrap',
]

// Per-verb flags: only `wrap` accepts --heal, so `saki qa --heal` is a usage error rather than a
// silently-ignored flag.
function flagsForVerb(verb: RunVerb): FlagSpec {
  return supportsHeal(verb) ? { ...RUN_FLAGS, heal: 'boolean' } : RUN_FLAGS
}

// Shared by every other command that spawns a skill run (roadmap add/init) but isn't a `saki run
// <verb>` itself — same validate-before-spawn rule as startRun: a typo'd engine is a usage error
// with the valid values named, not a round-trip to the backend's 422.
function spawnFlagsFor(flags: Record<string, string | boolean>): { profile?: string; engine?: RunEngine } {
  return {
    profile: typeof flags.profile === 'string' ? flags.profile : undefined,
    engine: typeof flags.engine === 'string' ? assertRunEngine(flags.engine) : undefined,
  }
}

// Usage line reflects each verb's real arity: none / optional / required.
function usageForVerb(verb: RunVerb): string {
  // Every run-start accepts --engine and --follow; only the target differs. Advertising the flag on
  // some verbs and not others made `saki qa --help` under-report a flag it happily accepts.
  const tail = `[--follow] [--engine ${RUN_ENGINES.join('|')}]`
  if (takesNoTarget(verb)) return `saki ${verb}${supportsHeal(verb) ? ' [--heal]' : ''} ${tail}`
  if (targetIsOptional(verb)) return `saki ${verb} [<roadmap-id|path>] ${tail}`
  return `saki ${verb} <roadmap-id|path> ${tail} [--profile <dir>]`
}

const ALIAS_COMMANDS: CommandDef[] = ALIASED_VERBS.map((verb) => ({
  path: [verb],
  usage: usageForVerb(verb),
  summary: `alias for \`saki run ${verb}\``,
  flags: flagsForVerb(verb),
  run: (ctx, rest, flags) => startRun(ctx, verb, rest, flags),
}))

const COMMANDS: CommandDef[] = [
  ...ALIAS_COMMANDS,
  {
    path: ['status'],
    usage: 'saki status',
    summary: 'are both studio servers up, and will they let me in',
    flags: COMMON,
    run: (ctx) => cmdStatus(ctx),
  },
  {
    path: ['backend'],
    usage: 'saki backend start|stop|status',
    summary: 'start, stop, or inspect the saki backend daemon',
    flags: COMMON,
    run: (ctx, positionals, flags) => cmdBackend(ctx, positionals, flags),
  },
  {
    path: ['mcp'],
    usage: 'saki mcp',
    summary: "start an MCP server exposing saki's journey commands as typed tools",
    flags: { cwd: 'string' },
    // Lazy-loaded: every other command pays no cost for the MCP SDK + zod, and an SDK load
    // failure can't take down an unrelated command (measured 3x startup cost with a static import).
    run: async (ctx) => (await import('./commands/mcp.js')).cmdMcp(ctx),
  },
  {
    path: ['doctor'],
    usage: 'saki doctor [--profile <dir>]',
    summary: 'can each engine actually run a saki-builder command, before you dispatch a run',
    flags: { ...COMMON, profile: 'string' },
    run: (ctx, positionals, flags) => cmdDoctor(ctx, positionals, flags),
  },
  {
    path: ['init-env'],
    usage: 'saki init-env --engine claude|codex|opencode [--profile <dir>]',
    summary: 'provision and verify one engine profile',
    flags: { ...COMMON, engine: 'string', profile: 'string' },
    run: (ctx, positionals, flags) => cmdInitEnv(ctx, positionals, flags),
  },
  {
    path: ['genesis'],
    usage: 'saki genesis "<product idea>" [--restart]',
    summary: 'start a product from scratch (spawns /saki-builder:genesis)',
    flags: { ...COMMON, restart: 'boolean' },
    run: (ctx, rest, flags) => cmdGenesis(ctx, rest.join(' '), flags),
  },
  {
    path: ['roadmap', 'init'],
    usage: `saki roadmap init [--profile <dir>] [--engine ${RUN_ENGINES.join('|')}]`,
    summary: 'scaffold tasks/roadmap.md (spawns /saki-builder:roadmap init)',
    flags: { ...COMMON, profile: 'string', engine: 'string' },
    run: (ctx, _rest, flags) => cmdRoadmapInit(ctx, spawnFlagsFor(flags)),
  },
  {
    path: ['roadmap', 'list'],
    usage: 'saki roadmap list',
    summary: 'work items in this repo',
    flags: COMMON,
    run: (ctx) => cmdRoadmapList(ctx),
  },
  {
    path: ['roadmap', 'add'],
    usage: `saki roadmap add "<intent>" --epic|--feature|--improvement|--bug [--profile <dir>] [--engine ${RUN_ENGINES.join('|')}]`,
    summary: 'add a work item (spawns /saki-builder:add)',
    flags: {
      ...COMMON,
      epic: 'boolean',
      feature: 'boolean',
      improvement: 'boolean',
      bug: 'boolean',
      profile: 'string',
      engine: 'string',
    },
    run: (ctx, rest, flags) => cmdRoadmapAdd(ctx, rest.join(' '), flags, spawnFlagsFor(flags)),
  },
  {
    path: ['run', 'continue'],
    usage: 'saki run continue <workflowId> [--option <value>]',
    summary: 'resume a parked or awaiting workflow',
    flags: { ...COMMON, option: 'string' },
    run: (ctx, rest, flags) => {
      if (rest.length > 1) throw new CliError('run continue takes exactly one workflow id', EXIT.USAGE)
      return cmdRunContinue(ctx, rest[0] ?? '', { option: typeof flags.option === 'string' ? flags.option : undefined })
    },
  },
  {
    path: ['run', 'tail'],
    usage: 'saki run tail <runId>',
    summary: 'stream a run, exit with its verdict',
    flags: COMMON,
    run: (ctx, rest) => cmdRunTail(ctx, rest[0] ?? ''),
  },
  {
    path: ['run', 'stop'],
    usage: 'saki run stop <runId>',
    summary: 'stop a running run',
    flags: COMMON,
    run: (ctx, rest) => cmdRunStop(ctx, rest[0] ?? ''),
  },
  {
    path: ['run'],
    usage: `saki run <${RUN_VERBS.join('|')}> [<roadmap-id|path>] [--follow] [--profile <dir>]`,
    summary: 'start a headless skill run',
    // Superset: the nested form must accept --heal so `saki run wrap --heal` works. Per-verb
    // rejection still happens in cmdRunStart / flagsForVerb for the aliases.
    flags: { ...RUN_FLAGS, heal: 'boolean' },
    run: (ctx, rest, flags) => startRun(ctx, assertRunVerb(rest[0] ?? ''), rest.slice(1), flags),
  },
  {
    path: ['runs'],
    usage: 'saki runs',
    summary: 'runs the studio still holds',
    flags: COMMON,
    run: (ctx) => cmdRuns(ctx),
  },
  {
    path: ['prd', 'show'],
    usage: 'saki prd show <roadmap-id|path>',
    summary: 'print a PRD',
    flags: COMMON,
    run: (ctx, rest) => cmdPrdShow(ctx, rest[0] ?? ''),
  },
  {
    path: ['prd', 'lock'],
    usage: 'saki prd lock <roadmap-id|path>',
    summary: 'freeze a PRD before build',
    flags: COMMON,
    run: (ctx, rest) => cmdPrdLock(ctx, rest[0] ?? ''),
  },
  {
    path: ['proto'],
    usage: 'saki proto <roadmap-id|path> [--open]',
    summary: 'url of a rendered proto gallery',
    flags: { ...COMMON, open: 'boolean' },
    run: (ctx, rest, flags) => cmdProto(ctx, rest[0] ?? '', { open: flags.open === true }),
  },
  {
    path: ['workitems'],
    usage: 'saki workitems',
    summary: 'open PRDs and plans',
    flags: COMMON,
    run: (ctx) => cmdWorkitems(ctx),
  },
  {
    path: ['branch', 'list'],
    usage: 'saki branch list',
    summary: 'local branches',
    flags: COMMON,
    run: (ctx) => cmdBranchList(ctx),
  },
  {
    path: ['branch', 'switch'],
    usage: 'saki branch switch <name> [--create]',
    summary: 'switch branch (or create one)',
    flags: { ...COMMON, create: 'boolean' },
    run: (ctx, rest, flags) => cmdBranchSwitch(ctx, rest[0] ?? '', { create: flags.create === true }),
  },
  {
    path: ['branch'],
    usage: 'saki branch',
    summary: 'current branch',
    flags: COMMON,
    run: (ctx) => cmdBranch(ctx),
  },
  {
    path: ['mr', 'create'],
    usage: 'saki mr create',
    summary: 'push the branch and open a merge request',
    flags: COMMON,
    run: (ctx) => cmdMrCreate(ctx),
  },
  {
    path: ['artifacts'],
    usage: 'saki artifacts <runId>',
    summary: 'run artifacts (needs a browser session — see README)',
    flags: COMMON,
    run: (ctx, rest) => cmdArtifacts(ctx, rest[0] ?? ''),
  },
  {
    path: ['screenshots'],
    usage: 'saki screenshots',
    summary: '/qa screenshots in this repo',
    flags: COMMON,
    run: (ctx) => cmdScreenshots(ctx),
  },
]

// Match the most specific command whose path prefixes argv ('run tail' wins over 'run').
export function matchCommand(argv: string[]): { def: CommandDef; rest: string[] } | undefined {
  const candidates = [...COMMANDS].sort((a, b) => b.path.length - a.path.length)
  for (const def of candidates) {
    if (def.path.every((seg, i) => argv[i] === seg)) {
      return { def, rest: argv.slice(def.path.length) }
    }
  }
  return undefined
}

export function helpText(): string {
  const width = Math.max(...COMMANDS.map((c) => c.usage.length))
  return [
    'saki — command line for saki studio',
    '',
    'Commands:',
    ...COMMANDS.map((c) => `  ${c.usage.padEnd(width + 2)}${c.summary}`),
    '',
    'Common flags:',
    '  --version, -V    print the installed CLI version',
    '  --json            machine-readable output (one compact line)',
    '  --cwd <dir>       repo to act on (default: the current directory)',
    '',
    'Environment:',
    '  SAKI_STUDIO_URL   express base url (default http://localhost:8787)',
    '  SAKI_BACKEND_URL  go backend base url (default http://127.0.0.1:8788)',
    '',
    'Exit codes:',
    '  0 ok · 1 error · 2 usage · 3 studio unreachable · 4 not found',
    '  5 operation refused by the studio · 6 needs a browser session',
  ].join('\n')
}

export interface MainDeps {
  write?: (s: string) => void
  writeErr?: (s: string) => void
  env?: Record<string, string | undefined>
  cwd?: string
  fetchImpl?: typeof fetch
}

export async function main(argv: string[], deps: MainDeps = {}): Promise<ExitCode> {
  const write = deps.write ?? console.log
  const writeErr = deps.writeErr ?? console.error
  const env = deps.env ?? process.env

  try {
    if (argv.length === 1 && (argv[0] === '--version' || argv[0] === '-V')) {
      write(packageVersion)
      return EXIT.OK
    }

    if (argv.length === 0) {
      write(helpText())
      return EXIT.OK
    }

    const match = matchCommand(argv)
    if (!match) {
      throw new CliError(
        `unknown command: ${argv.join(' ')}`,
        EXIT.USAGE,
        `run \`saki --help\` — available: ${COMMANDS.map((c) => c.path.join(' ')).join(', ')}`,
      )
    }

    const parsed = parseArgs(match.rest, match.def.flags)
    if (parsed.help) {
      write(`${match.def.usage}\n  ${match.def.summary}`)
      return EXIT.OK
    }

    const lifecycleCommand = match.def.path[0] === 'backend'
    let daemonState = null
    if (!lifecycleCommand && !deps.fetchImpl && !env.SAKI_BACKEND_URL) {
      const before = await readDaemonState(env)
      daemonState = await ensureDaemon(env)
      if (daemonState.pid > 0 && (!before || before.pid !== daemonState.pid)) writeErr(`daemon:autostart {result:"success",pid:${daemonState.pid}}`)
    }

    const ctx = makeCtx({
      client: new StudioClient({
        env,
        fetchImpl: deps.fetchImpl,
        socketPath: daemonState?.socketPath ?? undefined,
      }),
      cwd: resolveCwd(
        typeof parsed.flags.cwd === 'string' ? parsed.flags.cwd : undefined,
        deps.cwd ?? process.cwd(),
      ),
      json: parsed.flags.json === true,
      write,
      writeErr,
      env,
    })

    return await match.def.run(ctx, parsed.positionals, parsed.flags)
  } catch (err) {
    if (err instanceof CliError) {
      writeErr(`error: ${err.message}`)
      if (err.hint) writeErr(`  ${err.hint}`)
      return err.code
    }
    // Anything unexpected is still a clean non-zero exit, never a raw stack on stdout.
    writeErr(`error: ${err instanceof Error ? err.message : String(err)}`)
    return EXIT.ERROR
  }
}

// Only self-execute as the real binary — importing this module in a test must not run the CLI.
//
// A naive `import.meta.url === 'file://' + process.argv[1]` does NOT work here: npm installs the
// bin as a SYMLINK (node_modules/.bin/saki -> ../@saki/cli/dist/index.js), and node reports argv[1]
// as the unresolved (often relative) link path while import.meta.url is the realpath'd target. The
// strings never match, so the CLI would silently do nothing. Compare realpaths instead.
export function isDirectInvocation(
  argvPath: string | undefined,
  moduleUrl: string,
  realpath: (p: string) => string = realpathSync,
): boolean {
  if (!argvPath) return false
  try {
    return realpath(argvPath) === realpath(fileURLToPath(moduleUrl))
  } catch {
    return false
  }
}

if (isDirectInvocation(process.argv[1], import.meta.url)) {
  // A `--help` anywhere prints help; otherwise dispatch normally.
  const argv = process.argv.slice(2)
  if (argv.length === 0 || argv[0] === '--help' || argv[0] === '-h') {
    console.log(helpText())
    process.exitCode = EXIT.OK
  } else if (argv.length === 1 && (argv[0] === '--version' || argv[0] === '-V')) {
    console.log(packageVersion)
    process.exitCode = EXIT.OK
  } else {
    process.exitCode = await main(argv)
  }
}
