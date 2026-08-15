import { describe, it, expect } from 'vitest'
import { backendFor, DEFAULT_GO_URL, DEFAULT_TS_URL } from './routes.js'

describe('backendFor — the CLI must split traffic exactly as the UI proxy does', () => {
  // The bug this table fixes: the CLI sent everything to Express while the UI sent most of it to
  // Go, so the two had SEPARATE run stores — a UI build was invisible to `saki runs`, and
  // `saki build` on the same PRD spawned a second concurrent build.
  it('routes the run vertical to Go', () => {
    for (const p of ['/api/run', '/api/run?x=1', '/api/runs', '/api/run/abc-123/stop', '/events/abc']) {
      expect(backendFor(p, true)).toBe('go')
    }
  })

  it('routes the read surface to Go', () => {
    for (const p of ['/api/doctor', '/api/roadmap', '/api/prd', '/api/workitems', '/api/screenshots', '/api/screenshot']) {
      expect(backendFor(p, true)).toBe('go')
    }
  })

  it('routes the git surface to Go', () => {
    for (const p of ['/api/branch', '/api/branches', '/api/switch-branch', '/api/create-mr']) {
      expect(backendFor(p, true)).toBe('go')
    }
  })

  it('keeps what Go has NOT taken over on Express', () => {
    for (const p of ['/api/health', '/api/session', '/api/runs/abc/artifacts', '/api/artifact/abc']) {
      expect(backendFor(p, true)).toBe('ts')
    }
  })

  it('does not confuse /api/runs with /api/run', () => {
    expect(backendFor('/api/runs', true)).toBe('go')
    expect(backendFor('/api/run', true)).toBe('go')
    // …and an artifacts path under /api/runs/ is NOT the Go list route
    expect(backendFor('/api/runs/abc/artifacts', true)).toBe('ts')
  })

  it('routes proto ASSETS to Go but leaves unrelated prefixes alone', () => {
    expect(backendFor('/api/proto/abc/tasks/x.html', true)).toBe('go')
    expect(backendFor('/api/protoype', true)).toBe('ts')
  })

})

// I8 — the standalone world. This is the shape the extracted `saki-cli` ships: one backend, so the
// split table is never consulted. When backend/ + apps/cli move to their own repo, the describe block
// ABOVE (and the GO_ROUTES table it guards) is deleted, and this one becomes the whole story.
describe('backendFor — with no Express configured, everything is Go', () => {
  it('routes even the Express-only paths to Go, because there is no Express', () => {
    for (const p of [
      '/api/session',
      '/api/runs/abc/artifacts',
      '/api/artifact/abc',
      '/api/health',
      '/api/protoype',
    ]) {
      expect(backendFor(p, false)).toBe('go')
    }
  })

  it('routes the journey surface to Go too (unchanged from the configured case)', () => {
    for (const p of ['/api/run', '/api/runs', '/events/abc', '/api/roadmap', '/api/proto/x/y.html']) {
      expect(backendFor(p, false)).toBe('go')
    }
  })

  // The flag is what decides, not the path — the guard against someone later "simplifying" the
  // short-circuit into the regex table and silently reintroducing the two-server assumption.
  it('is decided by the flag alone: the same path differs by configuration', () => {
    expect(backendFor('/api/session', true)).toBe('ts')
    expect(backendFor('/api/session', false)).toBe('go')
  })
})

describe('defaults', () => {
  it('match the ports run.sh boots', () => {
    expect(DEFAULT_TS_URL).toBe('http://localhost:8787')
    expect(DEFAULT_GO_URL).toBe('http://127.0.0.1:8788')
  })
})
