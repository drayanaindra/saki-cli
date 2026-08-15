import { describe, it, expect } from 'vitest'
import { renderTable, emit, truncate } from './output.js'

interface Row {
  id: string
  title: string
}

const COLS = [
  { header: 'ID', value: (r: Row) => r.id },
  { header: 'TITLE', value: (r: Row) => r.title },
]

describe('truncate', () => {
  it('leaves a short string untouched', () => {
    expect(truncate('abc', 10)).toBe('abc')
  })

  it('leaves a string of exactly max untouched', () => {
    expect(truncate('abcde', 5)).toBe('abcde')
  })

  it('cuts an over-long string to max, ellipsis included', () => {
    const out = truncate('abcdefghij', 5)
    expect(out).toHaveLength(5)
    expect(out).toBe('abcd…')
  })
})

describe('renderTable', () => {
  it('emits a header row', () => {
    const lines = renderTable([{ id: 'E1', title: 'x' }], COLS).split('\n')
    expect(lines[0]).toMatch(/^ID\s+TITLE$/)
  })

  it('pads every column to its widest cell, header included', () => {
    const lines = renderTable(
      [
        { id: 'E1', title: 'short' },
        { id: 'E1234', title: 'longer title' },
      ],
      COLS,
    ).split('\n')
    // 'E1234' (5) is the widest id, so TITLE starts at the same offset on every line.
    expect(lines[0]).toBe('ID     TITLE')
    expect(lines[1]).toBe('E1     short')
    expect(lines[2]).toBe('E1234  longer title')
  })

  it('does not pad the final column (no trailing whitespace)', () => {
    const lines = renderTable([{ id: 'E1', title: 'x' }], COLS).split('\n')
    for (const l of lines) expect(l).toBe(l.trimEnd())
  })

  it('truncates a cell wider than the column max', () => {
    const cols = [{ header: 'TITLE', value: (r: Row) => r.title, max: 6 }]
    const lines = renderTable([{ id: 'E1', title: 'an extremely long title' }], cols).split('\n')
    expect(lines[1]).toBe('an ex…')
  })

  // REGRESSION (review NIT): `c.max ? truncate(...) : value` is a truthiness guard on a count, so a
  // column declared `max: 0` silently skipped truncation entirely.
  it('honours max: 0 instead of treating it as "no max"', () => {
    const cols = [{ header: 'T', value: (r: Row) => r.title, max: 0 }]
    const lines = renderTable([{ id: 'E1', title: 'anything' }], cols).split('\n')
    expect(lines[1]).toBe('')
  })

  // REGRESSION (review NIT): slice() cuts UTF-16 units, so truncating on an emoji boundary emitted
  // a lone surrogate, and padEnd mis-measured the column.
  it('truncates on code-point boundaries, never splitting an emoji', () => {
    const out = truncate('🙂🙂🙂🙂🙂', 3)
    expect(Array.from(out)).toHaveLength(3)
    expect(out).toBe('🙂🙂…')
    expect(/[\uD800-\uDBFF](?![\uDC00-\uDFFF])/.test(out)).toBe(false)
  })

  it('aligns columns by code point so an emoji cell does not shift the next column', () => {
    const cols = [
      { header: 'A', value: (r: Row) => r.id },
      { header: 'B', value: (r: Row) => r.title },
    ]
    const lines = renderTable(
      [
        { id: '🙂', title: 'x' },
        { id: 'ab', title: 'y' },
      ],
      cols,
    ).split('\n')
    // Measure in CODE POINTS — indexOf counts UTF-16 units, which is the very confusion this fix
    // removes (an emoji is 2 units but 1 column, so indexOf would report a false misalignment).
    const col = (line: string, ch: string) => Array.from(line).indexOf(ch)
    expect(col(lines[1], 'x')).toBe(col(lines[2], 'y'))
  })

  it('returns only the header when there are no rows', () => {
    expect(renderTable([] as Row[], COLS).split('\n')).toHaveLength(1)
  })
})

describe('emit', () => {
  it('prints compact single-line JSON when json is true', () => {
    const out: string[] = []
    emit({ runId: 'r1', deduped: false }, { json: true }, (s) => out.push(s))
    expect(out).toEqual(['{"runId":"r1","deduped":false}'])
  })

  it('prints the human string when json is false', () => {
    const out: string[] = []
    emit({ runId: 'r1' }, { json: false, human: 'started r1' }, (s) => out.push(s))
    expect(out).toEqual(['started r1'])
  })

  it('falls back to JSON when json is false but no human form was given', () => {
    const out: string[] = []
    emit({ a: 1 }, { json: false }, (s) => out.push(s))
    expect(out).toEqual(['{"a":1}'])
  })
})
