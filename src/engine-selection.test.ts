import { describe, expect, it } from 'vitest'
import { StudioClient } from './client.js'
import { resolveEngineSelection, AUTO_ENGINE_ORDER } from './engine-selection.js'
import { AUTO_ENGINE } from './engines.js'
import { EXIT, CliError } from './exit.js'

function clientFor(body: unknown) {
  const urls: string[] = []
  const client = new StudioClient({
    baseUrl: 'http://studio.test',
    fetchImpl: (async (url: string | URL) => {
      urls.push(String(url))
      return {
        ok: true,
        status: 200,
        json: async () => body,
      } as unknown as Response
    }) as unknown as typeof fetch,
  })
  return { client, urls }
}

const report = (engine: string, status: 'ok' | 'failed' | 'unknown', reason = '') => ({
  engine,
  profile: `/profiles/${engine}`,
  status,
  reason,
  fix: reason ? `fix ${engine}` : '',
})

describe('resolveEngineSelection', () => {
  it('keeps the documented preference order instead of doctor response order', async () => {
    const { client, urls } = clientFor({
      engines: [report('omp', 'ok'), report('codex', 'ok'), report('claude', 'ok')],
    })

    await expect(resolveEngineSelection(client, AUTO_ENGINE, '/profiles/shared')).resolves.toBe(
      AUTO_ENGINE_ORDER[0],
    )
    expect(urls).toEqual(['http://studio.test/api/doctor?profile=%2Fprofiles%2Fshared'])
  })

  it('skips unusable preferred engines and selects the first usable fallback', async () => {
    const { client } = clientFor({
      engines: [
        report('claude', 'failed', 'profile proof failed'),
        report('codex', 'unknown', 'binary not found'),
        report('opencode', 'ok'),
      ],
    })

    await expect(resolveEngineSelection(client, AUTO_ENGINE)).resolves.toBe('opencode')
  })

  it('does not probe doctor for an explicit engine', async () => {
    const { client, urls } = clientFor({ engines: [] })

    await expect(resolveEngineSelection(client, 'omp')).resolves.toBe('omp')
    expect(urls).toHaveLength(0)
  })

  it('fails before spawn when no engine is usable', async () => {
    const { client } = clientFor({
      engines: [
        report('claude', 'failed', 'not authenticated'),
        report('codex', 'unknown', 'binary not found'),
      ],
    })

    await expect(resolveEngineSelection(client, AUTO_ENGINE)).rejects.toMatchObject({
      code: EXIT.ERROR,
      message: expect.stringContaining('no usable engine found'),
      hint: expect.stringContaining('saki doctor'),
    } satisfies Partial<CliError>)
  })
})
