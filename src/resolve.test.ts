import { describe, it, expect } from 'vitest'
import { resolveCwd, resolvePrdPath, resolvePlanPath, findItem, protoUrl, encodeProtoDir } from './resolve.js'
import { EXIT, CliError } from './exit.js'
import type { RoadmapItem } from './types.js'

function item(over: Partial<RoadmapItem> = {}): RoadmapItem {
  return {
    id: 'E12',
    n: 12,
    type: 'Epic',
    track: 'PRD',
    title: 'Checkout redesign',
    status: 'Planned',
    goal: 'g',
    childPrd: 'prd-checkout.md',
    childPlan: null,
    phaseChain: null,
    ...over,
  }
}

describe('resolveCwd', () => {
  it('defaults to the process cwd', () => {
    expect(resolveCwd(undefined, '/proc/here')).toBe('/proc/here')
  })

  it('resolves a relative flag against the process cwd', () => {
    expect(resolveCwd('sub', '/proc/here')).toBe('/proc/here/sub')
  })

  it('passes an absolute flag through', () => {
    expect(resolveCwd('/elsewhere', '/proc/here')).toBe('/elsewhere')
  })
})

describe('findItem', () => {
  const epics = [item(), item({ id: 'F3', n: 3, type: 'Feature', track: 'PRD' })]

  it('finds by exact id', () => {
    expect(findItem(epics, 'E12').id).toBe('E12')
  })

  it('finds case-insensitively so `e12` works', () => {
    expect(findItem(epics, 'e12').id).toBe('E12')
  })

  it('throws NOT_FOUND naming the id when absent', () => {
    try {
      findItem(epics, 'B99')
      throw new Error('expected throw')
    } catch (err) {
      const e = err as CliError
      expect(e.code).toBe(EXIT.NOT_FOUND)
      expect(e.message).toContain('B99')
    }
  })
})

describe('resolvePrdPath — tolerant of either childPrd shape (plan Unknown 1)', () => {
  it('joins tasks/ for a bare filename', () => {
    expect(resolvePrdPath(item({ childPrd: 'prd-checkout.md' }), '/repo')).toBe(
      '/repo/tasks/prd-checkout.md',
    )
  })

  it('does not double the tasks/ segment when the field already carries it', () => {
    expect(resolvePrdPath(item({ childPrd: 'tasks/prd-checkout.md' }), '/repo')).toBe(
      '/repo/tasks/prd-checkout.md',
    )
  })

  it('resolves both shapes to the SAME path', () => {
    const bare = resolvePrdPath(item({ childPrd: 'prd-x.md' }), '/repo')
    const prefixed = resolvePrdPath(item({ childPrd: 'tasks/prd-x.md' }), '/repo')
    expect(bare).toBe(prefixed)
  })

  it('passes an absolute childPrd through untouched', () => {
    expect(resolvePrdPath(item({ childPrd: '/abs/tasks/prd-x.md' }), '/repo')).toBe(
      '/abs/tasks/prd-x.md',
    )
  })

  it('throws NOT_FOUND with a pickup hint when the item has no PRD yet', () => {
    try {
      resolvePrdPath(item({ childPrd: null }), '/repo')
      throw new Error('expected throw')
    } catch (err) {
      const e = err as CliError
      expect(e.code).toBe(EXIT.NOT_FOUND)
      expect(e.message).toContain('E12')
      expect(e.hint).toContain('saki run pickup E12')
    }
  })
})

describe('resolvePlanPath — Plan-track items point at childPlan, NOT childPrd', () => {
  it('joins tasks/ for a bare plan filename', () => {
    expect(resolvePlanPath(item({ childPlan: 'fix-x-plan.md' }), '/repo')).toBe('/repo/tasks/fix-x-plan.md')
  })

  it('does not double the tasks/ segment', () => {
    expect(resolvePlanPath(item({ childPlan: 'tasks/fix-x-plan.md' }), '/repo')).toBe(
      '/repo/tasks/fix-x-plan.md',
    )
  })

  it('reads childPlan even when childPrd is also set — they are different fields', () => {
    const both = item({ childPrd: 'prd-x.md', childPlan: 'fix-x-plan.md' })
    expect(resolvePlanPath(both, '/repo')).toBe('/repo/tasks/fix-x-plan.md')
    expect(resolvePrdPath(both, '/repo')).toBe('/repo/tasks/prd-x.md')
  })

  it('throws NOT_FOUND with an rplan hint when the item has no plan yet', () => {
    try {
      resolvePlanPath(item({ childPlan: null }), '/repo')
      throw new Error('expected throw')
    } catch (err) {
      const e = err as CliError
      expect(e.code).toBe(EXIT.NOT_FOUND)
      expect(e.message).toContain('E12')
      expect(e.hint).toContain('saki rplan E12')
    }
  })
})

describe('encodeProtoDir / protoUrl', () => {
  it('round-trips through the exact decode the server performs', () => {
    // apps/server/src/index.ts:938 decodes with:
    //   Buffer.from(dir.replace(/-/g,'+').replace(/_/g,'/'), 'base64').toString('utf8')
    const cwd = '/Users/me/Project/some repo/with+slash/and?more'
    const dir = encodeProtoDir(cwd)
    const decoded = Buffer.from(dir.replace(/-/g, '+').replace(/_/g, '/'), 'base64').toString('utf8')
    expect(decoded).toBe(cwd)
  })

  it('emits base64url with no padding and no url-hostile characters', () => {
    const dir = encodeProtoDir('/a/bb/ccc/dddd/eeeee')
    expect(dir).not.toContain('=')
    expect(dir).not.toContain('+')
    expect(dir).not.toContain('/')
  })

  it('builds the full gallery url from base, cwd and rel', () => {
    const url = protoUrl('http://localhost:8787', '/repo', 'tasks/proto-x/index.html')
    expect(url).toBe(
      `http://localhost:8787/api/proto/${encodeProtoDir('/repo')}/tasks/proto-x/index.html`,
    )
  })

  it('strips a leading slash on rel so the path never doubles up', () => {
    const url = protoUrl('http://localhost:8787', '/repo', '/tasks/proto-x/index.html')
    expect(url).not.toContain('//tasks')
  })
})
