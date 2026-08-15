import { emit, renderTable, type Column } from '../output.js'
import { EXIT, type ExitCode } from '../exit.js'
import type { Ctx } from '../ctx.js'
import type { DoctorResult, EngineReport } from '../types.js'

const DOCTOR_COLUMNS: Column<EngineReport>[] = [
  { header: 'ENGINE', value: (r) => r.engine },
  { header: 'PROFILE', value: (r) => r.profile },
  { header: 'STATUS', value: (r) => r.status },
  { header: 'REASON', value: (r) => r.reason, max: 60 },
]

// `saki doctor` — can each reported engine actually run a saki-builder command, before a run is
// dispatched. Exit 0 iff every reported engine is ok (criterion 1.2: exactly EXIT.ERROR on any other
// state, never merely non-zero). A studio-unreachable or gated-studio failure never reaches this
// logic — ctx.client.get throws first, via the existing client.ts contract (EXIT.UNREACHABLE=3 /
// EXIT.AUTH_REQUIRED=6), unchanged by this slice.
export async function cmdDoctor(
  ctx: Ctx,
  _positionals: string[],
  flags: Record<string, string | boolean>,
): Promise<ExitCode> {
  const profile = typeof flags.profile === 'string' ? flags.profile : undefined
  const res = await ctx.client.get<DoctorResult>('/api/doctor', profile ? { profile } : undefined)
  // Defend against a malformed body ({}/{"engines":null}) with a clean diagnosis rather than an
  // uncaught TypeError on `.length` of undefined.
  const engines = res.engines ?? []
  const allOk = engines.length > 0 && engines.every((e) => e.status === 'ok')

  emit(
    res,
    { json: ctx.json, human: engines.length ? renderTable(engines, DOCTOR_COLUMNS) : 'no engines reported' },
    ctx.write,
  )

  if (!allOk) {
    for (const e of engines) {
      if (e.status !== 'ok' && e.fix) ctx.writeErr(`fix (${e.engine}): ${e.fix}`)
    }
    return EXIT.ERROR
  }
  return EXIT.OK
}
