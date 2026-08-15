import { test, expect } from '@playwright/test'
import { execFileSync } from 'node:child_process'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

// The codex engine driven end-to-end through the REAL studio spawn path:
// POST /api/run (engine=codex) → Go run vertical → detached `codex exec` → NDJSON journal → SSE.
//
// WHY THIS SPEC MUST USE THE REAL BINARY. The project's standing rule: a fake-binary test cannot prove
// an engine invocation. The opencode command form shipped GREEN against fakes while every real run
// no-opped, because the fake could not know that `opencode run` does not expand a slash command that
// arrives in the message. codex's behaviour is the OPPOSITE — `codex exec` DOES resolve a leading
// slash command from the message, including the namespaced `/saki-builder:…` the studio emits — and
// that difference is exactly the kind of fact only the real binary can settle. A fake `codex` fixture
// would re-create the same false green.
//
// What this locks, specifically:
//   1. codex resolves a NAMESPACED saki command from the message (no --command split — the opencode
//      workaround must never be "unified" onto codex);
//   2. args reach the skill VERBATIM (codex does not quote-wrap multi-word args the way opencode does);
//   3. the run finalizes through the normal journal/SSE contract.
//
// Isolation: the run is pinned to a throwaway CODEX_HOME profile carrying (a) a `build` skill so
// CodexSkillsProof passes, and (b) one trivial probe skill standing in for a real saki-builder command
// — so the spec proves RESOLUTION without running a slow, expensive, repo-mutating journey skill.
// Credentials live at ~/.codex/auth.json and are copied into the pinned home so it authenticates.

const OPT_OUT = process.env.SAKI_CODEX_E2E === '0'
const PROBE_TOKEN = 'CODEX_COMMAND_RESOLVED'
// A model refusal when a slash command is not resolvable — the regression signature.
const REFUSAL = /isn't a recognized command|not a recognized command|unrecognized command|no such skill/i

// Absent binary FAILS LOUDLY; only the explicit env opt-out skips. A silent skip is what let the
// opencode defect ship, so "codex isn't installed" must never read as a pass.
function requireCodex(): void {
  execFileSync('codex', ['--version'], { stdio: 'ignore', timeout: 15_000 })
}

// The run executes a REAL agent with --dangerously-bypass-approvals-and-sandbox, so it must NOT run in
// the repo working tree: this branch is multi-machine and routinely carries a co-worker's uncommitted
// WIP, and only the prompt text would stand between a misbehaving model and a write into it. A
// throwaway dir bounds the blast radius to something disposable.
function makeScratchCwd(): string {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'saki-cx-cwd-'))
  fs.writeFileSync(path.join(dir, 'README.md'), 'throwaway cwd for the codex spawn e2e\n')
  return dir
}

function writeSkill(dir: string, name: string, body: string): void {
  fs.mkdirSync(path.join(dir, name), { recursive: true })
  fs.writeFileSync(path.join(dir, name, 'SKILL.md'), body)
}

// The studio pins CODEX_HOME=<configDir>/codex (backend/infra/codex.go), so the profile nests that way.
function writeProbeProfile(): string {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'saki-cx-e2e-'))
  const home = path.join(dir, 'codex')
  const skills = path.join(home, 'skills')
  fs.mkdirSync(skills, { recursive: true })

  // Present so CodexSkillsProof passes for this pinned profile (it reads skills/build/SKILL.md).
  writeSkill(skills, 'build', '---\nname: build\ndescription: unused in this spec\n---\nDo nothing.\n')
  // Echoing the arguments back is what lets this spec PIN codex's real argument substitution, not
  // merely that the command resolved.
  writeSkill(
    skills,
    'sakiprobe',
    `---\nname: sakiprobe\ndescription: Studio spawn resolution probe. Use when asked to run the sakiprobe.\n---\nReply with exactly ${PROBE_TOKEN} then a space then the arguments you were given, and nothing else. Do not use any tools.\n`,
  )
  fs.writeFileSync(path.join(home, 'config.toml'), 'model_reasoning_effort = "low"\n')
  // Auth lives in the codex home, so a pinned home needs the operator's credentials copied in.
  const auth = path.join(os.homedir(), '.codex', 'auth.json')
  if (fs.existsSync(auth)) fs.copyFileSync(auth, path.join(home, 'auth.json'))
  return dir
}

test.describe('codex engine — the studio spawn resolves the command', () => {
  test.skip(OPT_OUT, 'explicit opt-out via SAKI_CODEX_E2E=0')
  // A real model turn is slower than the suite default; still bounded so a hang fails rather than waits.
  test.setTimeout(240_000)

  test('a namespaced slash-command prompt resolves, with its args reaching the skill verbatim', async ({ request }) => {
    requireCodex() // throws (fails the spec) when the binary is absent — never a silent skip

    const configDir = writeProbeProfile()
    const scratchCwd = makeScratchCwd()
    try {
      // Shaped exactly like a REAL studio journey prompt: namespaced slash command + a MULTI-WORD arg.
      // The multi-word shape is deliberate — it is the shape opencode transforms, so asserting it here
      // is what proves codex does NOT, rather than assuming the two engines agree.
      const spawn = await request.post('/api/run', {
        data: {
          prompt: '/saki-builder:sakiprobe tasks/prd-x.md RESUME slice 2',
          meta: { kind: 'generate', laneKey: 'e2e-codex' },
          cwd: scratchCwd,
          configDir,
          engine: 'codex',
        },
      })
      expect(spawn.status()).toBe(201)
      const { runId } = await spawn.json()
      expect(runId).toBeTruthy()

      const stream = await request.get(`/events/${runId}`, { timeout: 230_000 })
      expect(stream.status()).toBe(200)
      const body = await stream.text()

      // 1. THE REGRESSION LOCK: the skill body actually executed, proven by its own token coming back
      //    through the studio's stream. If codex ever stops resolving a message-borne slash command
      //    (or someone routes codex through opencode's --command form), this fails.
      expect(body).toContain(PROBE_TOKEN)
      // 2. The args reached the skill VERBATIM — no quote-wrapping, no re-splitting. This is the
      //    behaviour the spawner's single-argv-element decision rests on.
      expect(body).toContain('tasks/prd-x.md RESUME slice 2')
      // 3. Read of the same failure from the other side. Secondary on purpose: it depends on how the
      //    MODEL words a refusal, so it can neither be the primary lock nor be trusted alone.
      expect(body).not.toMatch(REFUSAL)
      // 4. The run finalized through the normal journal/SSE contract (engine-agnostic).
      expect(body).toContain('event: end')
      expect(body).toContain('"status":"done"')
    } finally {
      fs.rmSync(configDir, { recursive: true, force: true })
      fs.rmSync(scratchCwd, { recursive: true, force: true })
    }
  })

  // The loud-refusal half of the contract: a codex home WITHOUT the saki-builder skills must be
  // rejected at the spawn, not discovered as a no-op run that exits 0 and parks a build that never
  // started (rule 4 — install state is proven by reading, never inferred from an exit code).
  test('a codex home without the saki-builder skills is refused at the spawn', async ({ request }) => {
    requireCodex()
    const bare = fs.mkdtempSync(path.join(os.tmpdir(), 'saki-cx-bare-'))
    try {
      const spawn = await request.post('/api/run', {
        data: {
          prompt: '/saki-builder:build tasks/prd-x.md',
          meta: { kind: 'generate', laneKey: 'e2e-codex-bare' },
          cwd: bare,
          configDir: bare,
          engine: 'codex',
        },
      })
      expect(spawn.status()).toBe(500)
      expect(await spawn.text()).toMatch(/skills|install-codex-skills/i)
    } finally {
      fs.rmSync(bare, { recursive: true, force: true })
    }
  })
})
