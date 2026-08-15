import { emit } from '../output.js'
import { EXIT, CliError, fail, type ExitCode } from '../exit.js'
import type { Ctx } from '../ctx.js'

// `saki artifacts <runId>` — a KNOWN-LIMITED command.
//
// GET /api/runs/:id/artifacts (apps/server/src/index.ts:2194) calls getSession() directly and 401s
// without a session cookie. DEV_MODE=1 does NOT help: it short-circuits the auth MIDDLEWARE
// (authGate.ts:71) but not the handler's own session read.
//
// The guard is deliberate — it is an IDOR protection (the sibling route returns 404 rather than 403
// specifically to avoid leaking artifact existence across users). Weakening it so a local CLI could
// read artifacts would trade a real multi-tenant safety property for convenience, so the CLI
// explains the 401 instead of routing around it.
export async function cmdArtifacts(ctx: Ctx, runId: string): Promise<ExitCode> {
  if (!runId.trim()) {
    fail('artifacts needs a run id', EXIT.USAGE, 'usage: saki artifacts <runId>')
  }

  // I8: standalone there is no Express, and artifact listing lives only there. Fail before the request
  // rather than after a confusing 404 from a Go backend that never served this route. Exit 3
  // (UNREACHABLE) rather than 2 (USAGE): the arguments were fine, the server simply isn't there.
  if (!ctx.client.expressConfigured) {
    fail(
      'artifact listing needs the express studio',
      EXIT.UNREACHABLE,
      'set SAKI_STUDIO_URL to point at one — the go backend does not serve artifact routes',
    )
  }

  try {
    const res = await ctx.client.get<{ artifacts?: unknown[] }>(
      `/api/runs/${encodeURIComponent(runId.trim())}/artifacts`,
    )
    const artifacts = res?.artifacts ?? []
    emit(
      { artifacts },
      {
        json: ctx.json,
        human: artifacts.length
          ? artifacts.map((a) => JSON.stringify(a)).join('\n')
          : `no artifacts recorded for ${runId}`,
      },
      ctx.write,
    )
    return EXIT.OK
  } catch (err) {
    if (err instanceof CliError && err.code === EXIT.AUTH_REQUIRED) {
      throw new CliError(
        'artifact listing requires a browser session',
        EXIT.AUTH_REQUIRED,
        `DEV_MODE does not seed one — open the run in the studio UI at ${ctx.client.baseUrl} to view its artifacts`,
      )
    }
    throw err
  }
}
