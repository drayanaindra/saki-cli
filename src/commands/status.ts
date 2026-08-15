import { emit } from '../output.js'
import { EXIT, CliError, type ExitCode } from '../exit.js'
import type { Ctx } from '../ctx.js'
import type { Backend } from '../routes.js'
import type { SessionPayload } from '../types.js'

export interface StatusReport {
  // Is there a second (Express) server in this configuration at all? (I8) THE DISCRIMINATOR an agent
  // should read FIRST: when false, every Express-derived field below is null — "we did not ask" — not
  // false. This follows the `sessionRead` precedent below rather than dropping the keys, so a reader
  // that predates I8 sees a falsy value instead of a missing one.
  expressConfigured: boolean
  // Omitted entirely when !expressConfigured — reporting `url: 'http://localhost:8787', reachable: false`
  // for a server nobody configured and nobody probed is a lie the first version of this told.
  url?: string
  reachable?: boolean
  service?: string
  // The Go backend (:8788) — the SECOND server the CLI depends on. It serves runs, roadmap, prd,
  // branch, proto and screenshots (routes.ts), so it fails separately from Express and has to be
  // reported separately: a live Express alone is not a working studio.
  backendUrl: string
  backendReachable: boolean
  backendService?: string
  // null = no Express configured, so the question does not apply (Go has no session gate at all).
  devMode: boolean | null
  authenticated: boolean | null
  claudeCodeAccess: boolean | null
  // false when /api/session could not be read — devMode/authenticated below are then unknown, not
  // known-false. Lets an agent tell "studio says off" from "we could not ask".
  sessionRead: boolean
}

interface Probe {
  reachable: boolean
  service?: string
}

// Probe one backend's /api/health, turning "the socket never opened" into a reported `no` rather
// than a thrown error. Status has to describe BOTH servers even when one is dead — letting the
// first failure propagate is what left the other server's state invisible.
async function probe(ctx: Ctx, backend: Backend): Promise<Probe> {
  try {
    const health = await ctx.client.health(backend)
    return { reachable: health?.ok === true, service: health?.service }
  } catch (err) {
    if (!(err instanceof CliError)) throw err
    return { reachable: false }
  }
}

// `saki status` — the first thing an agent should run. Answers "are both studio servers there, and
// will they let me in", so a later exit 3 is never a surprise.
export async function cmdStatus(ctx: Ctx): Promise<ExitCode> {
  const expressConfigured = ctx.client.expressConfigured
  // Probe Express ONLY when one is configured. Standalone (the extracted saki-cli shape) there is no
  // second server, so probing would dial a default nobody asked for and report it dead.
  const [studio, backend] = await Promise.all([
    expressConfigured ? probe(ctx, 'ts') : Promise.resolve<Probe>({ reachable: false }),
    probe(ctx, 'go'),
  ])

  let session: SessionPayload = {}
  let sessionRead = false
  // Only worth asking once health said Express is up; otherwise the read fails for the reason
  // already reported and the warning below would just repeat it.
  if (expressConfigured && studio.reachable) {
    try {
      // /api/health and /api/session are both exempt from the auth gate (authGate.ts:21-28), so this
      // works whether or not DEV_MODE is set.
      session = await ctx.client.get<SessionPayload>('/api/session')
      sessionRead = true
    } catch (err) {
      // Health passed, so the studio IS up; a session read failure is informational, not fatal.
      if (!(err instanceof CliError)) throw err
      // But say so. Silently reporting `devMode off / auth anonymous` is indistinguishable from a
      // real answer, and would send an operator off to restart a studio that was already fine.
      ctx.writeErr(`warning: could not read /api/session (${err.message}) — devMode/auth below are unknown, shown as off`)
    }
  }

  const report: StatusReport = {
    expressConfigured,
    ...(expressConfigured
      ? { url: ctx.client.baseUrl, reachable: studio.reachable, service: studio.service }
      : {}),
    backendUrl: ctx.client.originOf('go'),
    backendReachable: backend.reachable,
    backendService: backend.service,
    devMode: expressConfigured ? session.devMode === true : null,
    authenticated: expressConfigured ? session.authenticated === true : null,
    // Surfaced because POST /api/run is gated on it separately from the session
    // (requireClaudeCodeAccess, index.ts:1248) — without it `saki run …` 403s while everything
    // else works, which is otherwise a baffling failure. Standalone there is no such gate: Go never
    // checks it, so `null` here means "not applicable", never "blocked".
    claudeCodeAccess: expressConfigured ? session.claudeCodeAccess === true : null,
    sessionRead,
  }

  const answer = (up: boolean, service?: string): string =>
    up ? `yes${service ? ` (${service})` : ''}` : 'no'

  // The go backend leads the report now — it serves every journey command, so it is the line that
  // decides whether the studio is usable. Express is reported after it, or named as absent.
  const goLines = [
    `backend   ${report.backendUrl}`,
    `reachable ${answer(report.backendReachable, report.backendService)}`,
  ]
  const expressLines = expressConfigured
    ? [
        `studio    ${report.url}`,
        `reachable ${answer(report.reachable === true, report.service)}`,
        `devMode   ${report.sessionRead ? (report.devMode ? 'on' : 'off') : 'unknown (session unreadable)'}`,
        `auth      ${report.sessionRead ? (report.authenticated ? 'authenticated' : 'anonymous') : 'unknown'}`,
        `runs      ${report.claudeCodeAccess ? 'allowed' : 'BLOCKED — POST /api/run will 403'}`,
      ]
    : // One line, and it says what to do about it. Printing `devMode off` here would be worse than
      // silence: it reads as a studio that refused us, when in fact there is no gate to refuse.
      [`express   not configured (set SAKI_STUDIO_URL to include it)`]

  emit(report, { json: ctx.json, human: [...goLines, ...expressLines].join('\n') }, ctx.write)

  // Name what each dead server costs. `reachable no` alone doesn't tell an operator that a green
  // Express still can't run a build, which is the whole failure this command exists to pre-empt.
  if (expressConfigured && !report.reachable) {
    ctx.writeErr(`error: express is not answering at ${report.url} — session, artifacts and DEV_MODE come from it`)
  }
  if (!report.backendReachable) {
    ctx.writeErr(`error: the go backend is not answering at ${report.backendUrl} — runs, roadmap, prd, branch and proto come from it`)
  }
  // Standalone, a dead GO backend is the ONLY thing that makes the studio unusable — an absent Express
  // is the expected configuration, not a fault, so it must not turn a working studio into exit 3.
  if ((expressConfigured && !report.reachable) || !report.backendReachable) {
    ctx.writeErr(
      expressConfigured
        ? '  start both with ./run.sh (or npm run dev:server / npm run dev:backend)'
        : '  start it with npm run dev:backend',
    )
    return EXIT.UNREACHABLE
  }
  return EXIT.OK
}
