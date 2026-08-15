// Output rendering. Two modes, one contract:
//   default  — an aligned table a human reads
//   --json   — one compact line an agent pipes into jq
// Every read command supports both; the JSON form is the stable machine surface.

const ELLIPSIS = '…'

export interface Column<T> {
  header: string
  value: (row: T) => string
  // Hard cap for this column's width. Cells beyond it are truncated with an ellipsis so one long
  // prompt or title can't push every other column off the terminal.
  max?: number
}

// Cut `s` to at most `max` characters, spending the last one on an ellipsis so the result is never
// wider than requested (a naive slice + '…' overflows by one).
//
// Counts CODE POINTS, not UTF-16 code units: `String.prototype.slice` splits surrogate pairs, so an
// emoji or CJK char landing on the boundary of a truncated PROMPT/TITLE cell emitted a lone
// surrogate (a replacement glyph in the terminal) and mis-measured the column width.
export function truncate(s: string, max: number): string {
  const chars = Array.from(s)
  if (chars.length <= max) return s
  if (max <= 1) return ELLIPSIS.slice(0, Math.max(0, max))
  return chars.slice(0, max - 1).join('') + ELLIPSIS
}

// Display width in code points — the unit `truncate` and the column padding both measure in.
function width(s: string): number {
  return Array.from(s).length
}

// Render rows as a whitespace-aligned table with a header. The final column is left unpadded so no
// line carries trailing whitespace (which would otherwise show up in diffs and test fixtures).
export function renderTable<T>(rows: T[], cols: Column<T>[]): string {
  // `c.max != null`, not `c.max` — a column declared `max: 0` is falsy and would silently skip
  // truncation entirely.
  const cells = rows.map((r) => cols.map((c) => (c.max != null ? truncate(c.value(r), c.max) : c.value(r))))
  const widths = cols.map((c, i) =>
    Math.max(width(c.header), ...cells.map((row) => width(row[i])), 0),
  )

  const line = (values: string[]): string =>
    values
      // padEnd counts UTF-16 units, so pad by hand against the code-point width used above —
      // otherwise one astral char per cell shifts that row's later columns left.
      .map((v, i) => (i === values.length - 1 ? v : v + ' '.repeat(Math.max(0, widths[i] + 2 - width(v)))))
      .join('')
      .trimEnd()

  return [line(cols.map((c) => c.header)), ...cells.map(line)].join('\n')
}

export interface EmitOptions {
  json?: boolean
  // The human-readable rendering. Omitted → JSON is printed in both modes, which is the right
  // default for a payload that has no better prose form.
  human?: string
}

// Print a result. `write` is injected so tests assert on captured output instead of stdout.
export function emit(
  value: unknown,
  opts: EmitOptions,
  write: (s: string) => void = console.log,
): void {
  if (!opts.json && opts.human !== undefined) {
    write(opts.human)
    return
  }
  write(JSON.stringify(value))
}
