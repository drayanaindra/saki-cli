import { EXIT, CliError, type ExitCode } from '../exit.js'

// Buffers a command's ctx.write output so it can be folded into an MCP tool result instead of going to
// the real stdout (which the stdio MCP transport owns for protocol frames). ctx.writeErr's diagnostic
// lines are NOT captured here — the tool handler routes them straight to the real process stderr, which
// is outside the MCP protocol channel and safe for local debugging (see registerStatusTool).
export interface CapturedIO {
  out: string[]
}

export interface ToolResult {
  content: { type: 'text'; text: string }[]
  isError: boolean
  // The SDK's CallToolResult is a Zod-inferred type carrying an index signature; without one here TS
  // rejects returning this shape from a registerTool handler even though the known fields match.
  [key: string]: unknown
}

function symbolicName(code: number): string {
  const entry = Object.entries(EXIT).find(([, value]) => value === code)
  return entry ? entry[0] : String(code)
}

// The one seam every MCP tool handler reuses: a command's real failure signal is a MIX of a returned
// ExitCode and a thrown CliError (src/index.ts main()'s own try/catch has to handle both) — this
// reproduces that same mapping per tool call, since a long-lived MCP server has no single top-level catch.
// Every non-OK path appends a synthesized "Exited with code N (NAME)" block so the CLI's six-way exit
// code survives translation into MCP's boolean isError, instead of being collapsed to a single bit.
export async function exitCodeToToolResult(
  fn: () => Promise<ExitCode>,
  captured: CapturedIO,
): Promise<ToolResult> {
  try {
    const code = await fn()
    const content = captured.out.map((text) => ({ type: 'text' as const, text }))
    if (code === EXIT.OK) return { content, isError: false }
    content.push({ type: 'text', text: `Exited with code ${code} (${symbolicName(code)})` })
    return { content, isError: true }
  } catch (err) {
    const content = captured.out.map((text) => ({ type: 'text' as const, text }))
    if (err instanceof CliError) {
      content.push({
        type: 'text',
        text: `Exited with code ${err.code} (${symbolicName(err.code)}): ${err.message}`,
      })
      if (err.hint) content.push({ type: 'text', text: err.hint })
    } else {
      // Message only — never err.stack. This text can reach an MCP client's transcript; internal file
      // paths and stack frames have no reason to be there (the full error is local-debugging-only, via
      // ctx.writeErr -> real stderr, never this content).
      const message = err instanceof Error ? err.message : String(err)
      content.push({ type: 'text', text: `Exited with code ${EXIT.ERROR} (${symbolicName(EXIT.ERROR)}): ${message}` })
    }
    return { content, isError: true }
  }
}
