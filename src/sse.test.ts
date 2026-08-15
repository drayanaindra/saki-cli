import { describe, it, expect } from 'vitest'
import { parseFrame, streamRun } from './sse.js'
import { StudioClient } from './client.js'
import { EXIT, CliError } from './exit.js'
import type { RunEvent } from './types.js'

// A fetch stub whose response body streams the given chunks verbatim — chunk boundaries are the
// point of these tests, so they must survive into the reader untouched.
function streamingFetch(chunks: string[], status = 200): typeof fetch {
  return (async () => {
    const enc = new TextEncoder()
    const body = new ReadableStream<Uint8Array>({
      start(controller) {
        for (const c of chunks) controller.enqueue(enc.encode(c))
        controller.close()
      },
    })
    return { ok: status >= 200 && status < 300, status, body } as unknown as Response
  }) as unknown as typeof fetch
}

function clientWith(chunks: string[], status = 200): StudioClient {
  return new StudioClient({ baseUrl: 'http://studio.test', fetchImpl: streamingFetch(chunks, status) })
}

function evFrame(seq: number, text: string): string {
  const ev: RunEvent = { seq, ts: 0, line: { kind: 'raw', text } }
  return `data: ${JSON.stringify(ev)}\n\n`
}

const endFrame = (status: string, exitCode: number) =>
  `event: end\ndata: ${JSON.stringify({ status, exitCode })}\n\n`

describe('parseFrame', () => {
  it('reads a bare data frame', () => {
    expect(parseFrame('data: {"a":1}')).toEqual({ event: undefined, data: '{"a":1}' })
  })

  it('reads a named event frame', () => {
    expect(parseFrame('event: end\ndata: {"status":"done"}')).toEqual({
      event: 'end',
      data: '{"status":"done"}',
    })
  })

  it('tolerates a missing space after the colon', () => {
    expect(parseFrame('data:{"a":1}').data).toBe('{"a":1}')
  })

  it('joins multi-line data per the SSE spec', () => {
    expect(parseFrame('data: line1\ndata: line2').data).toBe('line1\nline2')
  })

  it('ignores comment/keepalive lines', () => {
    expect(parseFrame(': keepalive\ndata: {"a":1}').data).toBe('{"a":1}')
  })
})

describe('streamRun', () => {
  it('yields whole events in order', async () => {
    const lines: string[] = []
    const end = await streamRun(clientWith([evFrame(1, 'one'), evFrame(2, 'two'), endFrame('done', 0)]), 'r1', (s) => lines.push(s))
    expect(lines).toEqual(['one', 'two'])
    expect(end).toEqual({ status: 'done', exitCode: 0 })
  })

  it('reassembles events split MID-FRAME across chunk boundaries', async () => {
    const whole = evFrame(1, 'split-me') + endFrame('done', 0)
    // Cut the stream at an awkward offset — inside the JSON payload of the first frame.
    const cut = 20
    const lines: string[] = []
    const end = await streamRun(clientWith([whole.slice(0, cut), whole.slice(cut)]), 'r1', (s) => lines.push(s))
    expect(lines).toEqual(['split-me'])
    expect(end.status).toBe('done')
  })

  it('reassembles when every chunk is a single character', async () => {
    const whole = evFrame(1, 'drip') + endFrame('done', 0)
    const lines: string[] = []
    await streamRun(clientWith(whole.split('')), 'r1', (s) => lines.push(s))
    expect(lines).toEqual(['drip'])
  })

  it('surfaces the terminal status of a failed run', async () => {
    const end = await streamRun(clientWith([endFrame('error', 1)]), 'r1', () => {})
    expect(end).toEqual({ status: 'error', exitCode: 1 })
  })

  it('throws NOT_FOUND for an unknown run id', async () => {
    try {
      await streamRun(clientWith([], 404), 'nope', () => {})
      throw new Error('expected throw')
    } catch (err) {
      const e = err as CliError
      expect(e.code).toBe(EXIT.NOT_FOUND)
      expect(e.message).toContain('nope')
    }
  })

  // REGRESSION (review WARN): `/events/:id` is gated like any /api route (authGate.ts:33 —
  // isGatedPath covers '/events/'), so a studio without DEV_MODE=1 answers 401 here. Flattening
  // every non-404 to ERROR made `saki run tail` exit 1 with no hint while every other command
  // exited 6 and explained itself.
  it('maps a gated stream (401) to AUTH_REQUIRED with the DEV_MODE hint', async () => {
    try {
      await streamRun(clientWith([], 401), 'r1', () => {})
      throw new Error('expected throw')
    } catch (err) {
      const e = err as CliError
      expect(e.code).toBe(EXIT.AUTH_REQUIRED)
      expect(e.hint).toContain('DEV_MODE=1')
    }
  })

  it('skips a malformed data frame rather than aborting the stream', async () => {
    const lines: string[] = []
    const end = await streamRun(
      clientWith(['data: {not json\n\n', evFrame(2, 'after'), endFrame('done', 0)]),
      'r1',
      (s) => lines.push(s),
    )
    expect(lines).toEqual(['after'])
    expect(end.status).toBe('done')
  })

  it('returns an unknown status when the stream ends with no end frame', async () => {
    const end = await streamRun(clientWith([evFrame(1, 'only')]), 'r1', () => {})
    expect(end.status).toBe('unknown')
  })
})
