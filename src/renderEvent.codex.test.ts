import { describe, it, expect } from 'vitest'
import { renderEvent, isHiddenFrame } from './renderEvent.js'
import { isRunEngine, RUN_ENGINES } from './commands/run.js'
import type { RunEvent } from './types.js'

const ev = (value: unknown): RunEvent => ({ seq: 0, ts: 0, line: { kind: 'json', value } })

describe('renderEvent — codex frames', () => {
  it('renders an agent_message as the spoken text', () => {
    expect(renderEvent(ev({ type: 'item.completed', item: { type: 'agent_message', text: 'SLICE 1 ✓' } }))).toBe('SLICE 1 ✓')
  })

  it('renders tool items in the ⚙ shape', () => {
    expect(renderEvent(ev({ type: 'item.completed', item: { type: 'command_execution', command: 'npm test' } }))).toBe(
      '⚙ command_execution(npm test)',
    )
  })

  it('names a subagent delegation by its inner tool', () => {
    const spawn = renderEvent(
      ev({
        type: 'item.completed',
        item: { type: 'collab_tool_call', tool: 'spawn_agent', prompt: 'Return exactly ALPHA', receiver_thread_ids: ['t1'] },
      }),
    )
    expect(spawn).toContain('spawn_agent')
    expect(spawn).not.toContain('collab_tool_call')
    expect(
      renderEvent(
        ev({ type: 'item.completed', item: { type: 'collab_tool_call', tool: 'wait', prompt: null, receiver_thread_ids: ['t1', 't2'] } }),
      ),
    ).toBe('⚙ wait(2 agents)')
  })

  it('never prints the bare frame type', () => {
    const out = renderEvent(ev({ type: 'item.completed', item: { type: 'file_change', path: 'src/a.ts' } }))
    expect(out).not.toBe('item.completed')
    expect(out).toContain('src/a.ts')
  })
})

// The CLI's tail and the studio's stream view must hide the SAME frames, or the two surfaces show
// visibly different streams for one run (the reason isHiddenFrame exists at all).
describe('isHiddenFrame — codex parity with the studio filter', () => {
  it('hides the run-structure frames and codex reasoning', () => {
    for (const type of ['thread.started', 'turn.started', 'turn.completed', 'item.updated']) {
      expect(isHiddenFrame(ev({ type }))).toBe(true)
    }
    expect(isHiddenFrame(ev({ type: 'item.started', item: { type: 'command_execution' } }))).toBe(true)
    expect(isHiddenFrame(ev({ type: 'item.completed', item: { type: 'reasoning', text: 'hmm' } }))).toBe(true)
  })

  it('keeps speech and tool work', () => {
    expect(isHiddenFrame(ev({ type: 'item.completed', item: { type: 'agent_message', text: 'hi' } }))).toBe(false)
    expect(isHiddenFrame(ev({ type: 'item.completed', item: { type: 'command_execution' } }))).toBe(false)
  })

  it('leaves claude and opencode frames alone', () => {
    expect(isHiddenFrame(ev({ type: 'assistant', message: { content: [{ text: 'hi' }] } }))).toBe(false)
    expect(isHiddenFrame(ev({ type: 'text', part: { text: 'hi' } }))).toBe(false)
  })
})

describe('--engine codex', () => {
  it('is an accepted engine', () => {
    expect(isRunEngine('codex')).toBe(true)
    expect(RUN_ENGINES).toContain('codex')
  })

  it('still rejects a typo (a usage error, not a backend 422)', () => {
    expect(isRunEngine('codexx')).toBe(false)
    expect(isRunEngine('CODEX')).toBe(false)
  })
})
