import { defineConfig } from '@playwright/test'

// Playwright is the runner for `e2e/` ONLY — the real-binary specs that a fake-binary test could
// never prove (CLAUDE.md project rule 2, e2e/codex-spawn.spec.ts:10-17). There is no browser here:
// `saki` is headless by design (docs/project-context.md § "No UI, no browser"), so these specs use
// the `request` fixture and child processes, never a page.
//
// `testDir: './e2e'` is the other half of vitest's `exclude: ['e2e/**']` — each runner owns exactly
// one tree, so neither ever collects the other's specs (vitest.config.ts:3-5).
export default defineConfig({
  testDir: './e2e',
  // A real engine turn is slow; the per-spec override in codex-spawn.spec.ts raises it further.
  timeout: 120_000,
  // Real agents mutate real profiles — running two at once would race on the same engine home.
  workers: 1,
  fullyParallel: false,
  // These specs drive the REAL backend, so a retry would re-run a real spawn. Fail honestly instead.
  retries: 0,
  reporter: [['list']],
  use: {
    // The Go backend's loopback bind (backend/cmd/server/main.go:147). Must stay 127.0.0.1: the
    // backend rejects any non-loopback Host header (backend/adapter/originguard.go:47), so a
    // `localhost`-vs-`127.0.0.1` mismatch would surface as a 403, not a connection error.
    baseURL: 'http://127.0.0.1:8788',
  },
  webServer: {
    // BOTH artifacts are built first. The Go binary never hot-reloads, so a stale dist/saki-backend
    // would silently serve the previous revision's routes; and the CLI specs execute dist/index.js
    // as a real child process, so a stale bundle would test the previous revision's exit codes.
    command: 'npm run build && npm run backend:build && ./dist/saki-backend',
    url: 'http://127.0.0.1:8788/api/health',
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
    stdout: 'pipe',
    stderr: 'pipe',
  },
})
