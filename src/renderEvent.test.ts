import { describe, it, expect } from 'vitest'
import { renderEvent, summarizeToolInput, isHiddenFrame } from './renderEvent.js'
import type { RunEvent } from './types.js'

function ev(line: RunEvent['line'], seq = 1): RunEvent {
  return { seq, ts: 0, line }
}

describe('summarizeToolInput', () => {
  it('picks the first recognised key', () => {
    expect(summarizeToolInput({ file_path: '/a/b.ts' })).toBe('/a/b.ts')
    expect(summarizeToolInput({ command: 'ls -la' })).toBe('ls -la')
  })

  it('returns empty for a non-object or an unrecognised shape', () => {
    expect(summarizeToolInput(undefined)).toBe('')
    expect(summarizeToolInput('nope')).toBe('')
    expect(summarizeToolInput({ unknown_key: 'x' })).toBe('')
  })

  it('truncates a long value', () => {
    const out = summarizeToolInput({ command: 'x'.repeat(200) })
    expect(out.length).toBeLessThanOrEqual(53)
    expect(out.endsWith('…')).toBe(true)
  })
})

describe('renderEvent', () => {
  it('prints a raw line verbatim — stderr and warnings must not be swallowed', () => {
    expect(renderEvent(ev({ kind: 'raw', text: 'PRD_BUILD_COMPLETE' }))).toBe('PRD_BUILD_COMPLETE')
  })

  it('joins assistant text blocks', () => {
    const line = {
      kind: 'json' as const,
      value: { type: 'assistant', message: { content: [{ text: 'hello' }, { text: 'world' }] } },
    }
    expect(renderEvent(ev(line))).toBe('hello  world')
  })

  it('summarises a tool_use block', () => {
    const line = {
      kind: 'json' as const,
      value: {
        type: 'assistant',
        message: { content: [{ type: 'tool_use', name: 'Edit', input: { file_path: 'a.ts' } }] },
      },
    }
    expect(renderEvent(ev(line))).toBe('⚙ Edit(a.ts)')
  })

  it('falls back to "assistant" when the message has no renderable content', () => {
    const line = { kind: 'json' as const, value: { type: 'assistant', message: { content: [] } } }
    expect(renderEvent(ev(line))).toBe('assistant')
  })

  it('renders an opencode text frame from part.text', () => {
    const line = { kind: 'json' as const, value: { type: 'text', part: { text: 'from opencode' } } }
    expect(renderEvent(ev(line))).toBe('from opencode')
  })

  it('falls back to top-level text on a text frame with no part', () => {
    const line = { kind: 'json' as const, value: { type: 'text', text: 'flat' } }
    expect(renderEvent(ev(line))).toBe('flat')
  })

  it('renders a result frame with its subtype', () => {
    const line = { kind: 'json' as const, value: { type: 'result', subtype: 'success' } }
    expect(renderEvent(ev(line))).toBe('result success')
  })

  it('renders a system frame with its subtype', () => {
    const line = { kind: 'json' as const, value: { type: 'system', subtype: 'init' } }
    expect(renderEvent(ev(line))).toBe('system/init')
  })

  it('falls back to the bare type for an unrecognised frame', () => {
    const line = { kind: 'json' as const, value: { type: 'step_start' } }
    expect(renderEvent(ev(line))).toBe('step_start')
  })

  it('does not throw on a json frame that is null or shapeless', () => {
    expect(renderEvent(ev({ kind: 'json', value: null }))).toBe('event')
    expect(renderEvent(ev({ kind: 'json', value: 42 }))).toBe('event')
  })
})

// REGRESSION (review NIT): renderEvent mirrors LiveLog's describeEvent, but the UI ALSO applies
// filterStream() alongside it (StreamPanel.tsx:7). Without the same suppression, `saki run tail` of
// an opencode-engine run printed the step_start/step_finish/tool_use/step_error plumbing the studio
// hides — two surfaces showing visibly different streams for one run.
describe('isHiddenFrame — parity with frontend/src/lib/streamFilter.ts', () => {
  it('hides every opencode plumbing frame', () => {
    for (const type of ['step_start', 'step_finish', 'tool_use', 'step_error']) {
      expect(isHiddenFrame(ev({ kind: 'json', value: { type } }))).toBe(true)
    }
  })

  it('hides the noisy system subtypes', () => {
    for (const subtype of ['thinking_tokens', 'task_progress']) {
      expect(isHiddenFrame(ev({ kind: 'json', value: { type: 'system', subtype } }))).toBe(true)
    }
  })

  it('keeps everything a user actually wants to read', () => {
    expect(isHiddenFrame(ev({ kind: 'raw', text: 'PRD_BUILD_COMPLETE' }))).toBe(false)
    expect(isHiddenFrame(ev({ kind: 'json', value: { type: 'assistant' } }))).toBe(false)
    expect(isHiddenFrame(ev({ kind: 'json', value: { type: 'text', part: { text: 'hi' } } }))).toBe(false)
    expect(isHiddenFrame(ev({ kind: 'json', value: { type: 'result', subtype: 'success' } }))).toBe(false)
    expect(isHiddenFrame(ev({ kind: 'json', value: { type: 'system', subtype: 'init' } }))).toBe(false)
  })

  it('never hides a raw line — build sentinels travel that path', () => {
    expect(isHiddenFrame(ev({ kind: 'raw', text: 'step_start' }))).toBe(false)
  })

  it('does not throw on a shapeless frame', () => {
    expect(isHiddenFrame(ev({ kind: 'json', value: null }))).toBe(false)
    expect(isHiddenFrame(ev({ kind: 'json', value: 42 }))).toBe(false)
  })
})
