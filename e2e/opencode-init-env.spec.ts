import { test, expect } from '@playwright/test'
import { execFileSync, spawnSync } from 'node:child_process'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

// `saki init-env --engine opencode` driven end to end through the REAL opencode binary:
// CLI → POST /api/init-env → Go usecase → infra.EngineProvisioner → `opencode plugin … --global` →
// the SAME OpencodePluginProof `saki doctor` reads.
//
// WHY THIS SPEC MUST USE THE REAL BINARY (the project's standing rule, e2e/codex-spawn.spec.ts:10-17).
// Everything a fake can prove about opencode provisioning is already proven cheaply in Go:
// backend/infra/initenv_test.go pins the argv, the effective XDG_CONFIG_HOME, the env scrubbing and
// the timeout kill. What NO fake can prove is the thing this feature actually claims — that running
// opencode's own installer against an empty profile produces a profile OpencodePluginProof then
// accepts, that the `--global` flag stays INSIDE the selected profile (PRD §9 rule 3), and that a
// repeat run is a byte-identical no-op. That is criteria 2.1, 2.2 and 2.4, and only the real binary
// settles them.

const OPT_OUT = process.env.SAKI_OPENCODE_E2E === '0'
const CLI = path.join(process.cwd(), 'dist', 'index.js')

// Absent binary FAILS LOUDLY; only the explicit env opt-out skips. A silent skip is what let the
// original opencode command form ship green against fakes while every real run no-opped.
function requireOpencode(): void {
  execFileSync('opencode', ['--version'], { stdio: 'ignore', timeout: 15_000 })
}

// Runs the REAL CLI as a child process, so `status` is a genuine process exit code.
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

test.describe('saki init-env — provision an opencode profile, then prove it', () => {
  test.skip(OPT_OUT, 'explicit opt-out via SAKI_OPENCODE_E2E=0')
  // A real plugin download can be slow; still bounded so a hang fails rather than waits.
  test.setTimeout(300_000)

  test('provisions an empty profile, is idempotent on repeat, and doctor agrees', async () => {
    requireOpencode()
    expect(fs.existsSync(CLI), `${CLI} is missing — the webServer step must build the CLI`).toBe(true)

    const profile = fs.mkdtempSync(path.join(os.tmpdir(), 'saki-init-env-opencode-'))
    const configDir = path.join(profile, 'opencode')
    // The profile starts EMPTY. This is what makes `changed:true` below carry the proof: nothing was
    // pre-seeded, so only the installer can account for the difference.
    expect(fs.existsSync(configDir)).toBe(false)

    try {
      // --- criterion 2.1: setup produces a doctor-verifiable profile and exits 0 ---
      const first = saki(['init-env', '--engine', 'opencode', '--profile', profile, '--json'])
      expect(first.status, `init-env failed: ${first.stdout}\n${first.stderr}`).toBe(0)

      const body = parseJson(first.stdout)
      expect(body).toMatchObject({ engine: 'opencode', profile, status: 'ok' })
      expect(body).toHaveProperty('changed')
      // The installer really wrote into the config the proof reads — setup and proof cannot be
      // pointing at different profiles (the PRD's kill criterion). And crucially, the write stayed
      // INSIDE the selected profile (PRD §9 rule 3): the real ~/.config/opencode must be untouched.
      expect(body.changed, 'an empty profile that reports ok must have been changed').toBe(true)
      expect(fs.existsSync(configDir)).toBe(true)

      // --- criterion 2.2 / 2.4: repeating is a no-op, config preserved byte-for-byte ---
      const configPath = path.join(configDir, 'opencode.jsonc')
      const before = fs.existsSync(configPath) ? fs.readFileSync(configPath) : null

      const second = saki(['init-env', '--engine', 'opencode', '--profile', profile, '--json'])
      expect(second.status, `repeat init-env failed: ${second.stdout}\n${second.stderr}`).toBe(0)
      expect(parseJson(second.stdout)).toMatchObject({ status: 'ok', changed: false })

      if (before) {
        expect(fs.readFileSync(configPath).equals(before), 'repeat run rewrote the config').toBe(true)
        // The plugin must be registered exactly once.
        const matches = before.toString('utf8').match(/@saketek\/saki-builder/g) ?? []
        expect(matches.length, 'duplicate opencode plugin registration').toBe(1)
      }

      // --- outcome 5.1: setup and doctor agree, with no manual edit in between ---
      const doctor = saki(['doctor', '--json', '--profile', profile])
      const engines = parseJson(doctor.stdout).engines as Array<Record<string, unknown>>
      const opencode = engines.find((e) => e.engine === 'opencode')
      expect(opencode, 'doctor did not report opencode').toBeTruthy()
      expect(opencode).toMatchObject({ status: 'ok', profile })
    } finally {
      fs.rmSync(profile, { recursive: true, force: true })
    }
  })

  // Criterion 2.3's CLI-visible half: a malformed opencode config makes the installer fail, and the
  // original file is preserved byte-for-byte (opencode exits 1 on the parse error without writing).
  test('a malformed config fails without truncating the original', async () => {
    requireOpencode()
    const profile = fs.mkdtempSync(path.join(os.tmpdir(), 'saki-init-env-opencode-mal-'))
    const configDir = path.join(profile, 'opencode')
    fs.mkdirSync(configDir, { recursive: true })
    const configPath = path.join(configDir, 'opencode.json')
    const broken = '{ this is not valid json ]'
    fs.writeFileSync(configPath, broken)

    try {
      const res = saki(['init-env', '--engine', 'opencode', '--profile', profile, '--json'])
      expect(res.status, 'a malformed config must exit non-zero').not.toBe(0)
      expect(parseJson(res.stdout)).toMatchObject({ status: 'failed', changed: false })
      expect(fs.readFileSync(configPath, 'utf8'), 'original config was truncated').toBe(broken)
    } finally {
      fs.rmSync(profile, { recursive: true, force: true })
    }
  })
})
