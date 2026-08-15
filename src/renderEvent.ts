import type { RunEvent } from './types.js'

// One stream event → one terminal line.
//
// MIRRORS `describeEvent` in frontend/src/components/LiveLog.tsx:18, minus the tone/colour half.
// Deliberately duplicated rather than shared: the original lives in a .tsx that imports React and
// the design-system tokens, neither of which belongs in a CLI. Kept in step with its source by this
// file's own tests. If a third consumer appears, extract a shared pure module then (rule of 3).

const TOOL_INPUT_KEYS = ['file_path', 'path', 'command', 'pattern', 'url', 'prompt'] as const
const MAX_TOOL_INPUT = 52

interface ContentBlock {
  type?: string
  text?: string
  name?: string
  input?: unknown
}

interface CodexItem {
  type?: string
  text?: string
  command?: string
  path?: string
  tool?: string
  receiver_thread_ids?: string[]
}

interface FrameValue {
  type?: string
  subtype?: string
  text?: string
  part?: { type?: string; text?: string }
  item?: CodexItem
  message?: { content?: ContentBlock[] }
}

// A short, human-meaningful stand-in for a tool call's arguments (the path it touched, the command
// it ran) — the whole input object would drown the stream.
export function summarizeToolInput(input: unknown): string {
  if (!input || typeof input !== 'object') return ''
  const o = input as Record<string, unknown>
  const key = TOOL_INPUT_KEYS.find((k) => typeof o[k] === 'string')
  if (!key) return ''
  const s = String(o[key])
  return s.length > MAX_TOOL_INPUT ? `${s.slice(0, MAX_TOOL_INPUT)}…` : s
}

function renderAssistant(v: FrameValue): string {
  const parts: string[] = []
  for (const c of v.message?.content ?? []) {
    if (c?.text) parts.push(c.text)
    else if (c?.type === 'tool_use') parts.push(`⚙ ${c.name ?? 'tool'}(${summarizeToolInput(c.input)})`)
  }
  return parts.join('  ') || 'assistant'
}

// Noisy system subtypes the studio hides — mirrors frontend/src/lib/streamFilter.ts:11.
const HIDDEN_SYSTEM_SUBTYPES = new Set(['thinking_tokens', 'task_progress'])

// E26 opencode `--format json` PLUMBING frames: run-structure events with no user-facing content.
// Mirrors streamFilter.ts:16. Claude never emits these types, so this is opencode-only.
const OPENCODE_PLUMBING_TYPES = new Set(['step_start', 'step_finish', 'tool_use', 'step_error'])

// codex `exec --json` run-structure frames. Mirrors streamFilter.ts CODEX_PLUMBING_TYPES; `item.started`
// is plumbing because its `item.completed` twin carries the same item once finished.
const CODEX_PLUMBING_TYPES = new Set(['thread.started', 'turn.started', 'turn.completed', 'item.started', 'item.updated'])

// True for a frame the studio's own stream view suppresses. `renderEvent` mirrors LiveLog's
// describeEvent, but the UI ALSO runs filterStream() alongside it (StreamPanel.tsx:7) — without
// this the CLI's tail of an opencode run prints the plumbing the studio hides, so the two surfaces
// would show visibly different streams for the same run.
export function isHiddenFrame(ev: RunEvent): boolean {
  if (ev.line.kind !== 'json') return false
  const v = (ev.line.value ?? {}) as FrameValue
  if (typeof v !== 'object' || v === null) return false
  if (v.type && OPENCODE_PLUMBING_TYPES.has(v.type)) return true
  if (v.type && CODEX_PLUMBING_TYPES.has(v.type)) return true
  if (v.type === 'item.completed' && v.item?.type === 'reasoning') return true // codex thinking
  return v.type === 'system' && !!v.subtype && HIDDEN_SYSTEM_SUBTYPES.has(v.subtype)
}

// One codex `item.completed` payload → one terminal line. Mirrors describeCodexItem in
// frontend/src/components/LiveLog.tsx, minus the tone half.
function renderCodexItem(item: CodexItem | undefined): string {
  const t = item?.type ?? 'item'
  if (t === 'agent_message') return item?.text ?? ''
  if (t === 'reasoning') return item?.text ?? 'reasoning'
  // Subagent delegation — name it by the inner `tool` (`spawn_agent`/`wait`); see LiveLog.tsx.
  if (t === 'collab_tool_call') {
    const n = item?.receiver_thread_ids?.length ?? 0
    const arg = summarizeToolInput(item) || (n ? `${n} agent${n === 1 ? '' : 's'}` : '')
    return `⚙ ${item?.tool ?? t}(${arg})`
  }
  return `⚙ ${t}(${summarizeToolInput(item)})`
}

export function renderEvent(ev: RunEvent): string {
  // Non-JSON output (claude's stderr, warnings, and the studio's own breadcrumbs) arrives raw and
  // must reach the operator verbatim — the build-complete sentinels travel this path.
  if (ev.line.kind === 'raw') return ev.line.text

  const v = (ev.line.value ?? {}) as FrameValue
  const type = (typeof v === 'object' && v?.type) || 'event'

  if (type === 'assistant') return renderAssistant(v)
  // opencode `--format json` puts the model's message at part.text (E26).
  if (type === 'text') return v.part?.text ?? v.text ?? 'text'
  // codex `exec --json` puts the model's message — and its tool work — on `item.completed` (E26).
  if (type === 'item.completed') return renderCodexItem(v.item)
  if (type === 'result') return `result ${v.subtype ?? ''}`.trim()
  if (type === 'system') return `system/${v.subtype ?? ''}`
  return String(type)
}
