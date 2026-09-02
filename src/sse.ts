import { EXIT, CliError } from './exit.js'
import { AUTH_HINT, codeForStatus, isAuthStatus, type StudioClient } from './client.js'
import { isHiddenFrame, renderEvent } from './renderEvent.js'
import type { RunEnd, RunEvent, WorkflowEnd } from './types.js'

// Consumer for the studio's SSE run stream (`GET /events/:id`, apps/server/src/index.ts:1291).
// The server replays every buffered event, then streams live, and closes with an `event: end`
// frame carrying {status, exitCode} (index.ts:1300).

const FRAME_SEPARATOR = /\r?\n\r?\n/

export interface Frame {
  event?: string
  data?: string
}

// Parse one SSE frame's field lines. Per the SSE spec a frame may carry several `data:` lines
// (joined with newlines) and lines starting `:` are comments/keepalives.
export function parseFrame(raw: string): Frame {
  let event: string | undefined
  const data: string[] = []
  for (const line of raw.split(/\r?\n/)) {
    if (!line || line.startsWith(':')) continue
    const colon = line.indexOf(':')
    if (colon === -1) continue
    const field = line.slice(0, colon)
    // A single leading space after the colon is part of the framing, not the value.
    const value = line.slice(colon + 1).replace(/^ /, '')
    if (field === 'event') event = value
    else if (field === 'data') data.push(value)
  }
  return { event, data: data.length ? data.join('\n') : undefined }
}

function parseJson<T>(raw: string | undefined): T | undefined {
  if (raw === undefined) return undefined
  try {
    return JSON.parse(raw) as T
  } catch {
    // A malformed frame must not kill the stream — the run is still going and the operator still
    // wants the rest of it.
    return undefined
  }
}

// Stream a run to completion, invoking `onLine` for each rendered event. Resolves with the run's
// terminal status so the caller can turn it into an exit code.
export async function streamRun(
  client: StudioClient,
  runId: string,
  onLine: (line: string) => void,
): Promise<RunEnd> {
  const res = await client.request(`/events/${encodeURIComponent(runId)}`, {
    headers: { Accept: 'text/event-stream' },
  })
  if (!res.ok) {
    if (res.status === 404) {
      throw new CliError(
        `no run with id ${runId}`,
        EXIT.NOT_FOUND,
        'run `saki runs` to list the runs the studio still holds',
      )
    }
    // `/events/:id` is gated like any /api route (authGate.ts:33 — isGatedPath covers '/events/'),
    // so a studio without DEV_MODE=1 answers 401 here. Reuse the shared status mapping instead of
    // flattening everything to ERROR, or `saki run tail` would exit 1 with no hint where every
    // other command exits 6 and tells you why.
    throw new CliError(
      `streaming run ${runId} failed with HTTP ${res.status}`,
      codeForStatus(res.status),
      isAuthStatus(res.status) ? AUTH_HINT : undefined,
    )
  }
  if (!res.body) throw new CliError(`run ${runId} returned an empty stream`, EXIT.ERROR)

  const reader = res.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  let end: RunEnd | undefined

  const drain = (flush: boolean): void => {
    // Split off every COMPLETE frame; whatever trails stays buffered for the next chunk. This is
    // what makes a mid-frame chunk boundary a non-event.
    for (;;) {
      const m = FRAME_SEPARATOR.exec(buffer)
      if (!m) break
      const raw = buffer.slice(0, m.index)
      buffer = buffer.slice(m.index + m[0].length)
      handle(raw)
    }
    if (flush && buffer.trim()) handle(buffer)
  }

  const handle = (raw: string): void => {
    const frame = parseFrame(raw)
    if (frame.event === 'end') {
      end = parseJson<RunEnd>(frame.data) ?? { status: 'unknown' }
      return
    }
    const ev = parseJson<RunEvent>(frame.data)
    // Suppress the same plumbing the studio's stream view hides, so `saki run tail` and the UI
    // show the same run.
    if (ev?.line && !isHiddenFrame(ev)) onLine(renderEvent(ev))
  }

  for (;;) {
    const { done, value } = await reader.read()
    if (done) break
    buffer += decoder.decode(value, { stream: true })
    drain(false)
  }
  buffer += decoder.decode()
  drain(true)

  // No end frame means the connection dropped before the run settled — report it rather than
  // implying success.
  return end ?? { status: 'unknown' }
}

// Stream the workflow envelope. Unlike streamRun, an ordinary child `done` frame is just another
// workflow transition; only the workflow's terminal end frame resolves this function.
export async function streamWorkflow(
  client: StudioClient,
  workflowId: string,
  onLine: (line: string) => void,
): Promise<WorkflowEnd> {
  const res = await client.request(`/events/${encodeURIComponent(workflowId)}`, {
    headers: { Accept: 'text/event-stream' },
  })
  if (!res.ok) {
    if (res.status === 404) throw new CliError(`no workflow with id ${workflowId}`, EXIT.NOT_FOUND)
    throw new CliError(
      `streaming workflow ${workflowId} failed with HTTP ${res.status}`,
      codeForStatus(res.status),
      isAuthStatus(res.status) ? AUTH_HINT : undefined,
    )
  }
  if (!res.body) throw new CliError(`workflow ${workflowId} returned an empty stream`, EXIT.ERROR)
  const reader = res.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  let end: WorkflowEnd | undefined
  const handle = (raw: string): void => {
    const frame = parseFrame(raw)
    if (frame.event === 'end') {
      end = parseJson<WorkflowEnd>(frame.data) ?? { status: 'unknown' }
      return
    }
    const event = parseJson<{ line?: RunEvent['line']; kind?: string }>(frame.data)
    if (event?.line) onLine(renderEvent({ seq: 0, ts: Date.now(), line: event.line }))
  }
  const drain = (flush: boolean): void => {
    for (;;) {
      const m = FRAME_SEPARATOR.exec(buffer)
      if (!m) break
      const raw = buffer.slice(0, m.index)
      buffer = buffer.slice(m.index + m[0].length)
      handle(raw)
    }
    if (flush && buffer.trim()) handle(buffer)
  }
  for (;;) {
    const { done, value } = await reader.read()
    if (done) break
    buffer += decoder.decode(value, { stream: true })
    drain(false)
  }
  buffer += decoder.decode()
  drain(true)
  return end ?? { status: 'unknown' }
}
