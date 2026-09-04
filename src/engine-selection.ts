import { EXIT, fail } from './exit.js'
import type { StudioClient } from './client.js'
import {
  AUTO_ENGINE,
  type RunEngine,
  type RunEngineSelection,
} from './engines.js'
import type { DoctorResult } from './types.js'

// Claude remains first so an explicit omission and `--engine auto` do not unexpectedly move an
// existing workload to a different runtime. Newer runtimes are still available as fallbacks when
// the preferred profile or binary is not usable.
export const AUTO_ENGINE_ORDER: readonly RunEngine[] = ['claude', 'codex', 'opencode', 'omp']

export async function resolveEngineSelection(
  client: StudioClient,
  selection: RunEngineSelection | undefined,
  profile?: string,
): Promise<RunEngine | undefined> {
  if (selection !== AUTO_ENGINE) return selection
  const result = await client.get<DoctorResult>('/api/doctor', profile ? { profile } : undefined)
  const reports = Array.isArray(result?.engines) ? result.engines : []
  const selected = AUTO_ENGINE_ORDER.find((engine) =>
    reports.some((report) => report.engine === engine && report.status === 'ok'),
  )
  if (selected) return selected

  const details = reports
    .map((report) => `${report.engine}: ${report.reason || report.status}`)
    .join('; ')
  fail(
    `no usable engine found${details ? ` (${details})` : ''}`,
    EXIT.ERROR,
    'run `saki doctor` and provision at least one engine before retrying',
  )
}
