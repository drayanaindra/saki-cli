import { z } from 'zod'
import type { McpServer } from '@modelcontextprotocol/sdk/server/mcp.js'
import { cmdRunTail } from '../../commands/runs.js'
import { exitCodeToToolResult, type ToolResult } from '../result.js'
import { buildToolCtx, type ToolCtxFactory } from '../tool-ctx.js'

// Read-only — this tool only streams and reports a run's state, it never mutates anything. Blocks for
// as long as the run takes (PRD §12 lean: mirror `saki run tail`'s own untimed behavior, no MCP-specific
// timeout or cancellation).
const RUN_TAIL_ANNOTATIONS = {
  readOnlyHint: true,
  destructiveHint: false,
  idempotentHint: true,
  openWorldHint: false,
} as const

// An MCP client typically holds a tool result's entire content in its own context (unlike a terminal,
// which scrolls) — and cmdRunTail pushes ONE content block per streamed line (result.ts's captured.out
// mapping), so a long-running build could return tens of thousands of blocks. Cap it: keep the head
// (early progress, for context) and the tail (the terminal status/exit-code blocks criterion 3.3 depends
// on), never the middle, which is the least useful part of an ended run's transcript.
const MAX_CONTENT_BLOCKS = 200
const KEEP_TAIL = 5

function capContent(result: ToolResult): ToolResult {
  if (result.content.length <= MAX_CONTENT_BLOCKS) return result
  const omitted = result.content.length - MAX_CONTENT_BLOCKS
  // -1 so head + the marker block itself + tail sums to exactly MAX_CONTENT_BLOCKS, not one over it.
  const keepHead = MAX_CONTENT_BLOCKS - KEEP_TAIL - 1
  return {
    ...result,
    content: [
      ...result.content.slice(0, keepHead),
      { type: 'text' as const, text: `... ${omitted} lines truncated ...` },
      ...result.content.slice(result.content.length - KEEP_TAIL),
    ],
  }
}

export function registerRunTailTool(server: McpServer, makeToolCtx: ToolCtxFactory): void {
  server.registerTool(
    'saki_run_tail',
    {
      title: 'saki run tail',
      description:
        "stream a run to its terminal state and return the verdict — blocks for as long as the run takes, mirroring `saki run tail`'s own untimed behavior",
      inputSchema: { runId: z.string() },
      annotations: RUN_TAIL_ANNOTATIONS,
    },
    async (args: { runId: string }) => {
      const { ctx, captured } = buildToolCtx(makeToolCtx())
      return capContent(await exitCodeToToolResult(() => cmdRunTail(ctx, args.runId), captured))
    },
  )
}
