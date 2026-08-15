import { spawn } from 'node:child_process'
import { platform } from 'node:os'
import { emit } from '../output.js'
import { EXIT, fail, type ExitCode } from '../exit.js'
import { protoUrl } from '../resolve.js'
import { fetchPrd, resolveTargetPrdPath } from './prd.js'
import type { Ctx } from '../ctx.js'

export type Opener = (url: string) => void

// Hand the URL to the desktop's default handler. Detached + unref'd so the CLI exits immediately
// instead of waiting on a browser process.
function defaultOpener(url: string): void {
  const cmd = platform() === 'darwin' ? 'open' : platform() === 'win32' ? 'start' : 'xdg-open'
  spawn(cmd, [url], { detached: true, stdio: 'ignore' }).unref()
}

export interface ProtoFlags {
  open?: boolean
}

// `saki proto <id|path>` — print the studio URL of a PRD's rendered proto gallery.
//
// Printing (rather than opening) is the default because the useful output for an agent is the
// string; `--open` is the human affordance.
export async function cmdProto(
  ctx: Ctx,
  target: string,
  flags: ProtoFlags,
  opener: Opener = defaultOpener,
): Promise<ExitCode> {
  const prd = await fetchPrd(ctx, await resolveTargetPrdPath(ctx, target))
  const rel = prd.protoPreviewFile?.trim()
  if (!rel) {
    fail(
      `no proto preview rendered for ${prd.path}`,
      EXIT.NOT_FOUND,
      `run \`saki run proto ${target}\` to render one`,
    )
  }

  // Route through originFor, NOT the raw `baseUrl` field: /api/proto/ is Go-owned (routes.ts), and a
  // direct `.baseUrl` read bypasses the split table entirely — standalone it would print/open a dead
  // http://localhost:8787 URL for a route the go backend is already serving.
  const url = protoUrl(ctx.client.originFor('/api/proto/'), ctx.cwd, rel)
  emit({ url, prd: prd.path, preview: rel }, { json: ctx.json, human: url }, ctx.write)
  if (flags.open) opener(url)
  return EXIT.OK
}
