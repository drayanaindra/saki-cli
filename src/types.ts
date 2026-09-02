// Shapes the studio server returns. Mirrors of the server's own types — declared structurally here
// (rather than imported across the workspace boundary) so the CLI stays an independent client that
// reads named fields and tolerates additive server changes.
//
// Sources: apps/server/src/roadmap.ts:12-46, apps/server/src/runManager.ts:684,
// apps/server/src/streamParser.ts:4-6, apps/server/src/sessionPayload.ts:17.

export type EpicStatus = 'Planned' | 'In-progress' | 'Shipped' | 'Blocked'
export type WorkItemType = 'Epic' | 'Feature' | 'Improvement' | 'Bug'
export type WorkItemTrack = 'PRD' | 'Plan'

export interface RoadmapItem {
  id: string
  n: number
  type: WorkItemType
  track: WorkItemTrack
  title: string
  status: EpicStatus
  goal: string
  childPrd: string | null
  childPlan: string | null
  phaseChain: string | null
}

export interface RoadmapResult {
  found: boolean
  productName?: string | null
  epics?: RoadmapItem[]
}

export interface RunMeta {
  kind?: string
  laneKey?: string
}

// The server's run status vocabulary — apps/server/src/runManager.ts:50. There is NO 'completed':
// finalize() sets 'done' on a clean exit and 'error' otherwise (runManager.ts:941), and the SSE end
// frame carries that same value (runManager.ts:953). Mirrored here so the CLI's success check can
// never drift from the server's again.
export type RunStatus = 'running' | 'done' | 'error'

// A run ended cleanly. The stop path emits {status:'error', exitCode:null} (runManager.ts:1165), so
// 'done' is the ONLY success value.
export const RUN_STATUS_DONE = 'done'

export interface RunSummary {
  id: string
  status: RunStatus | string
  prompt: string
  cwd: string
  startedAt: number
  meta?: RunMeta | null
  resumeCount?: number
  noProgressStreak?: number
  recovering?: boolean
  resumeAt?: number
  parkedReason?: string | null
  awaitingDecision?: unknown
  idempotencyKey?: string
}

// apps/server/src/streamParser.ts:4-6
export type ParsedLine = { kind: 'json'; value: unknown } | { kind: 'raw'; text: string }

// apps/server/src/runManager.ts:928
export interface RunEvent {
  seq: number
  ts: number
  line: ParsedLine
}

// `RunStatus | 'unknown'`, not `string` — sse.ts synthesizes 'unknown' when a stream ends with no
// end frame. Typing it as the closed union makes the "never drift from the server again" invariant
// MECHANICAL: comparing against a misspelled status is now a compile error, not a silent false.
export interface RunEnd {
  status: RunStatus | 'unknown'
  exitCode?: number | null
}

export interface PrdResult {
  found: boolean
  path?: string
  score?: unknown
  content?: string
  protoPreviewFile?: string | null
}

export interface SessionPayload {
  authenticated?: boolean
  devMode?: boolean
  hosted?: boolean
  claudeCodeAccess?: boolean
  gitlabConnected?: boolean
}

export interface WorkItemsResult {
  prds: Array<Record<string, unknown>>
  plans: Array<Record<string, unknown>>
}

export type WorkflowStatus = 'running' | 'parked' | 'awaiting-decision' | 'done' | 'failed' | 'stopped'
export type WorkflowPhase =
  | 'resolve'
  | 'pickup'
  | 'proto'
  | 'lock'
  | 'build'
  | 'verify'
  | 'rplan'
  | 'rplan-review'
  | 'approved'
  | 'qa'
  | 'reviewer'
  | 'wrap'

export interface WorkflowDecision {
  kind: string
  question: string
  options: string[]
  slice?: number
}

export interface WorkflowEvidence {
  checkedPaths: string[]
  commitIds: string[]
  phaseVerdicts: Record<string, string>
  verifiedAt: number
}

export interface Workflow {
  workflowId: string
  laneKey: string
  cwd: string
  target: string
  resolvedPath?: string
  engine?: string
  configDir?: string
  phase: WorkflowPhase
  childRunId?: string
  phaseHistory: Array<Record<string, unknown>>
  status: WorkflowStatus
  reason?: string
  parkedReason?: string
  awaitingDecision?: WorkflowDecision
  completionEvidence?: WorkflowEvidence
}

export interface WorkflowStartResult {
  workflowId: string
  childRunId?: string
  deduped: boolean
  phase: WorkflowPhase
  status: WorkflowStatus
  reason?: string
  workflow?: Workflow
}

export interface WorkflowEnd {
  status: WorkflowStatus | 'unknown'
  workflowId?: string
  phase?: WorkflowPhase
  reason?: string
  exitCode?: number | null
  completionEvidence?: WorkflowEvidence
}

// backend/domain/doctor.go's EngineReport, mirrored field-for-field. status is a closed union so a
// misspelled comparison is a compile error, not a silent false — same discipline as RunStatus above.
// All five fields are always present (never omitted): reason/fix are "" when there is nothing to report.
export interface EngineReport {
  engine: string
  profile: string
  status: 'ok' | 'failed' | 'unknown'
  reason: string
  fix: string
}

export interface DoctorResult {
  engines: EngineReport[]
}

export interface InitEnvResult {
  engine: string
  profile: string
  changed: boolean
  status: 'ok' | 'failed' | 'not_verified'
  reason: string
  fix: string
}
