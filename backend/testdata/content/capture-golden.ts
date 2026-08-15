// Provenance harness (Step 0) — captures the TS parser output as the golden parity oracle for the
// Go port (F4 slice 1). Run: `npx tsx backend/testdata/content/capture-golden.ts` from the repo root.
// The `path` field of a WorkItem is an absolute fs path (join(cwd,…)); we relativise it to cwd so the
// committed golden is machine-portable and the Go test compares the same relative shape.
import { writeFileSync } from 'node:fs'
import { relative, resolve } from 'node:path'
import { parseRoadmap } from '../../../apps/server/src/roadmap.ts'
import { findWorkItems, type WorkItem } from '../../../apps/server/src/workitems.ts'

const here = resolve(import.meta.dirname)
const read = (p: string) => require('node:fs').readFileSync(resolve(here, p), 'utf8')

// (a) roadmap parse goldens
for (const name of ['roadmap-allkinds', 'roadmap-malformed', 'roadmap-empty']) {
  const parsed = parseRoadmap(read(`${name}.md`))
  writeFileSync(resolve(here, `${name}.golden.json`), JSON.stringify(parsed, null, 2) + '\n')
}

// (d) workitems golden — relativise each item's absolute `path` to the repo cwd for portability.
const cwd = resolve(here, 'workitems-repo')
const rel = (items: WorkItem[]) => items.map((i) => ({ ...i, path: relative(cwd, i.path) }))
const { prds, plans } = findWorkItems(cwd)
writeFileSync(
  resolve(here, 'workitems-repo.golden.json'),
  JSON.stringify({ prds: rel(prds), plans: rel(plans) }, null, 2) + '\n',
)

console.log('captured goldens')
