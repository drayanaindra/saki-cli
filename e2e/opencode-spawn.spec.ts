import { test, expect } from '@playwright/test'
import { execFileSync } from 'node:child_process'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

// E26 AC 9.5.3 — the opencode engine driven end-to-end through the REAL studio spawn path:
// POST /api/run (engine=opencode) → Go run vertical → detached `opencode run` → NDJSON journal → SSE.
//
// WHY THIS SPEC MUST USE THE REAL BINARY (§13). E26 shipped with the opencode spawn passing the whole
// prompt as the CLI message:
//
//     opencode run --format json --auto "/saki-builder:build tasks/prd-x.md"
//
// opencode's `run` does NOT expand a slash command that arrives in the message. The raw text reached
// the model, which replied that it is not a recognized command, and the run no-opped on a clean exit 0
// — so every studio journey on opencode was dead while every unit test (fake binaries, synthesised
// frames) stayed green. Only a real-binary spec closes that gap; a fake `opencode` fixture would
// re-create exactly the false green this spec exists to prevent.
//
// The spawn must therefore reach the CLI as opencode's real invocation form — `--command <bare-name>`
// with the args as the message. The saki-builder skills install BARE on opencode (`build`, `prd`, `qa`
// — docs/OPENCODE-INSTALL.md), hence the namespace strip in domain.SplitSlashCommand.
//
// Isolation: the run is pinned to a throwaway XDG_CONFIG_HOME profile carrying (a) the plugin
// declaration OpencodePluginProof requires, (b) a free model, and (c) one trivial probe command. The
// probe stands in for a saki-builder command so the spec proves RESOLUTION without running a real
// (slow, expensive, repo-mutating) journey skill. Credentials live outside XDG_CONFIG_HOME
// (~/.local/share/opencode/auth.json), so the pinned profile still authenticates.

const OPT_OUT = process.env.SAKI_OPENCODE_E2E === '0'
const PROBE_TOKEN = 'OPENCODE_COMMAND_RESOLVED'
// opencode's refusal when a slash command reaches the model as prose — the exact regression signature.
const REFUSAL = /isn't a recognized command|not a recognized command|unrecognized command/i

// §13 — absent binary/plugin FAILS LOUDLY; only the explicit env opt-out skips. A silent skip here is
// what let the defect ship, so "opencode isn't installed" must never read as a pass.
function requireOpencode(): void {
  execFileSync('opencode', ['--version'], { stdio: 'ignore', timeout: 15_000 })
}

// The run executes a REAL agent with `--auto` (auto-approves every non-denied permission), so it must
// NOT run in the repo working tree: this branch is multi-machine and routinely carries a co-worker's
// uncommitted WIP, and only the prompt text would stand between a misbehaving model and a write into
// it. A throwaway dir bounds the blast radius to something disposable.
function makeScratchCwd(): string {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'saki-oc-cwd-'))
  fs.writeFileSync(path.join(dir, 'README.md'), 'throwaway cwd for the opencode spawn e2e\n')
  return dir
}

function writeProbeProfile(): string {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'saki-oc-e2e-'))
  const profile = path.join(dir, 'opencode')
  fs.mkdirSync(path.join(profile, 'command'), { recursive: true })
  fs.writeFileSync(
    path.join(profile, 'opencode.json'),
    JSON.stringify({
      $schema: 'https://opencode.ai/config.json',
      // Declared so OpencodePluginProof (E26 9.4.1) proves green for this pinned profile.
      plugin: ['@saketek/saki-builder'],
      // Use the local 9router provider rather than the metered OpenCode Go catalog. The latter can
      // be listed while its account is exhausted, making a command-resolution test hang without
      // producing any engine event. This provider is configured without a credential here; OpenCode
      // resolves its auth from the operator's normal credential store, while the profile remains
      // isolated and contains no secret material.
      model: '9router/codex',
      provider: {
        '9router': {
          npm: '@ai-sdk/openai-compatible',
          options: { baseURL: 'http://127.0.0.1:20128/v1' },
          models: { codex: { name: 'codex' } },
        },
      },
    }),
  )
  fs.writeFileSync(
    path.join(profile, 'command', 'sakiprobe.md'),
    // Echoing $ARGUMENTS back is what lets this spec PIN opencode's real argument substitution, not
    // merely that the command resolved.
    `---\ndescription: studio spawn resolution probe\n---\nReply with exactly ${PROBE_TOKEN} then a space then $ARGUMENTS, and nothing else. Do not use any tools.\n`,
  )
  return dir
}

test.describe('opencode engine — the studio spawn resolves the command (E26 9.5.3)', () => {
  test.skip(OPT_OUT, 'explicit opt-out via SAKI_OPENCODE_E2E=0')
  // A real model turn is slower than the suite default; still bounded so a hang fails rather than waits.
  test.setTimeout(180_000)

  test('a slash-command prompt reaches opencode as --command, not as raw message text', async ({ request }) => {
    requireOpencode() // throws (fails the spec) when the binary is absent — never a silent skip

    const configDir = writeProbeProfile()
    const scratchCwd = makeScratchCwd()
    try {
      // The prompt is shaped exactly like a REAL studio journey prompt: namespaced slash command + a
      // MULTI-WORD arg. The multi-word shape is deliberate — a space-free arg is the one shape opencode
      // does not transform, so testing only that would dodge the substitution behaviour entirely (the
      // same "green against a fake, wrong against the tool" gap this whole spec exists to close).
      const spawn = await request.post('/api/run', {
        data: {
          prompt: '/saki-builder:sakiprobe tasks/prd-x.md RESUME slice 2',
          meta: { kind: 'generate', laneKey: 'e2e-opencode' },
          cwd: scratchCwd,
          configDir,
          engine: 'opencode',
        },
      })
      expect(spawn.status()).toBe(201)
      const { runId } = await spawn.json()
      expect(runId).toBeTruthy()

      const stream = await request.get(`/events/${runId}`, { timeout: 170_000 })
      expect(stream.status()).toBe(200)
      const body = await stream.text()

      // 1. THE REGRESSION LOCK: the command body actually executed, proven by its own token coming back
      //    through the studio's stream. On the pre-fix spawn the body never runs, so this fails.
      expect(body).toContain(PROBE_TOKEN)
      // 2. The args reached the command. This PINS opencode's real substitution: it joins the message
      //    args and wraps any element containing a space in literal `"`, so a multi-word arg arrives as
      //    $ARGUMENTS == "tasks/prd-x.md RESUME slice 2" — quotes included. Asserting the inner text
      //    (not the quoting) keeps the test honest about what we rely on while staying robust to
      //    whitespace/escaping detail.
      expect(body).toContain('tasks/prd-x.md RESUME slice 2')
      // 3. A belt-and-braces read of the same failure from the other side. Secondary to (1) on purpose:
      //    it depends on how the MODEL words a refusal, so it can neither be the primary lock nor be
      //    trusted alone.
      expect(body).not.toMatch(REFUSAL)
      // 4. The run finalized through the normal journal/SSE contract (engine-agnostic).
      expect(body).toContain('event: end')
      expect(body).toContain('"status":"done"')
    } finally {
      fs.rmSync(configDir, { recursive: true, force: true })
      fs.rmSync(scratchCwd, { recursive: true, force: true })
    }
  })
})
