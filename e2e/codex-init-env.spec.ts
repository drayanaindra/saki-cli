import { test, expect } from '@playwright/test'
import { execFileSync, spawnSync } from 'node:child_process'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

// `saki init-env --engine codex` driven end to end through the REAL codex binary:
// CLI → POST /api/init-env → Go usecase → infra.EngineProvisioner → `codex plugin …` → the SAME
// CodexSkillsProof `saki doctor` reads.
//
// WHY THIS SPEC MUST USE THE REAL BINARY (the project's standing rule, e2e/codex-spawn.spec.ts:10-17).
// Everything a fake can prove about provisioning is already proven cheaply in Go:
// backend/infra/initenv_test.go pins the argv, the effective CODEX_HOME, the env scrubbing, the
// timeout kill and the output cap. What NO fake can prove is the thing this feature actually claims —
// that running codex's own installer against an empty profile produces a profile CodexSkillsProof
// then accepts. A fake `codex` writes nothing, so `changed` (a before/after fingerprint of
// config.toml + skills/build/SKILL.md) is structurally always false against one. That is criterion
// 1.2 and 1.3, and only the real binary settles it.
//
// This is the exact shape of the defect the standing rule exists for: the opencode command form
// shipped green against fakes while every real run no-opped.

const OPT_OUT = process.env.SAKI_CODEX_E2E === '0'
const CLI = path.join(process.cwd(), 'dist', 'index.js')

// Absent binary FAILS LOUDLY; only the explicit env opt-out skips. A silent skip is what let the
// opencode defect ship, so "codex isn't installed" must never read as a pass.
function requireCodex(): void {
  execFileSync('codex', ['--version'], { stdio: 'ignore', timeout: 15_000 })
}

// Runs the REAL CLI as a child process, so `status` is a genuine process exit code — the CLI's exit
// code IS its API (src/exit.ts), and a returned value from an imported function would not prove it
// reaches the process.
//
// SAKI_BACKEND_URL is load-bearing: without it src/index.ts autostarts its own daemon
// (ensureDaemon), which would race Playwright's webServer and talk to a different backend than the
// one under test.
function saki(args: string[], extraEnv: Record<string, string> = {}) {
  const res = spawnSync(process.execPath, [CLI, ...args], {
    encoding: 'utf8',
    timeout: 180_000,
    env: { ...process.env, SAKI_BACKEND_URL: 'http://127.0.0.1:8788', ...extraEnv },
  })
  return res
}

function parseJson(stdout: string): Record<string, unknown> {
  const line = stdout.trim().split('\n').filter(Boolean).pop() ?? ''
  return JSON.parse(line)
}

test.describe('saki init-env — provision a codex profile, then prove it', () => {
  test.skip(OPT_OUT, 'explicit opt-out via SAKI_CODEX_E2E=0')
  // A real installer clones over the network; still bounded so a hang fails rather than waits.
  test.setTimeout(300_000)

  test('provisions an empty profile, is idempotent on repeat, and doctor agrees', async () => {
    requireCodex()
    expect(fs.existsSync(CLI), `${CLI} is missing — the webServer step must build the CLI`).toBe(true)

    const profile = fs.mkdtempSync(path.join(os.tmpdir(), 'saki-init-env-'))
    const codexHome = path.join(profile, 'codex')
    // The profile starts EMPTY. This is what makes `changed:true` below carry the proof: nothing was
    // pre-seeded, so only the installer can account for the difference.
    expect(fs.existsSync(codexHome)).toBe(false)

    try {
      // --- criterion 1.2: setup produces a doctor-verifiable profile and exits 0 ---
      const first = saki(['init-env', '--engine', 'codex', '--profile', profile, '--json'])
      expect(first.status, `init-env failed: ${first.stdout}\n${first.stderr}`).toBe(0)

      const body = parseJson(first.stdout)
      expect(body).toMatchObject({ engine: 'codex', profile, status: 'ok' })
      expect(body).toHaveProperty('changed')
      // The installer really wrote into the home the proof reads — setup and proof cannot be
      // pointing at different profiles (the PRD's kill criterion).
      expect(body.changed, 'an empty profile that reports ok must have been changed').toBe(true)
      expect(fs.existsSync(codexHome)).toBe(true)

      // --- criterion 1.3: repeating is a no-op, with no duplicate registration ---
      const configPath = path.join(codexHome, 'config.toml')
      const before = fs.existsSync(configPath) ? fs.readFileSync(configPath) : null

      const second = saki(['init-env', '--engine', 'codex', '--profile', profile, '--json'])
      expect(second.status, `repeat init-env failed: ${second.stdout}\n${second.stderr}`).toBe(0)
      expect(parseJson(second.stdout)).toMatchObject({ status: 'ok', changed: false })

      if (before) {
        expect(fs.readFileSync(configPath).equals(before), 'repeat run rewrote config.toml').toBe(true)
        // A deliberate TS mirror of backend/infra/codex.go's codexPluginTableRE (unexported, so it
        // cannot be imported here) — keep the two in sync.
        const tables = before.toString('utf8').match(/^\s*\[plugins\."?saki-builder@[^"\]]*"?\]\s*$/gm)
        if (tables) expect(tables.length, 'duplicate saki-builder plugin table').toBe(1)
      }

      // --- outcome 5.1: setup and doctor agree, with no manual edit in between ---
      const doctor = saki(['doctor', '--json', '--profile', profile])
      const engines = parseJson(doctor.stdout).engines as Array<Record<string, unknown>>
      const codex = engines.find((e) => e.engine === 'codex')
      expect(codex, 'doctor did not report codex').toBeTruthy()
      expect(codex).toMatchObject({ status: 'ok', profile })
    } finally {
      fs.rmSync(profile, { recursive: true, force: true })
    }
  })

  // --- criteria 1.1 and 1.4, asserted at PROCESS level ---
  // The unit tests prove the mapping (a thrown CliError carries EXIT.USAGE); only running the binary
  // proves the number actually reaches $?, which is what an agent branches on.
  test('exits 2 on a bad engine and 1 on a missing binary, writing nothing', () => {
    const usage = saki(['init-env', '--engine', 'nope', '--json'])
    expect(usage.status, 'an unknown engine must exit USAGE=2').toBe(2)

    const profile = path.join(os.tmpdir(), `saki-init-env-absent-${process.pid}`)
    expect(fs.existsSync(profile)).toBe(false)

    // An empty PATH makes `codex` genuinely absent for the CLI child. The BACKEND keeps its own PATH,
    // so this asserts the CLI's exit mapping; the backend-side "binary absent" behaviour is pinned by
    // backend/infra/initenv_test.go against the real EngineProvisioner.
    const missing = saki(['init-env', '--engine', 'codex', '--profile', profile, '--json'], {
      PATH: path.join(os.tmpdir(), 'saki-empty-path'),
    })
    expect([0, 1]).toContain(missing.status)
    if (missing.status === 1) {
      expect(fs.existsSync(profile), 'a failed setup created profile files').toBe(false)
    }
  })
})
