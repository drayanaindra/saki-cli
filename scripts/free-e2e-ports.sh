#!/usr/bin/env bash
# Free the e2e stack's ports BEFORE Playwright boots (run as the `pree2e` npm lifecycle hook).
#
# Playwright's webServer uses `reuseExistingServer: true` (playwright.config.ts). If a dev server is
# already listening — a leftover from a prior run that didn't fully tear down, or the operator's own
# `npm run dev` — Playwright REUSES it instead of booting a fresh stack, so its webServer.env (the
# fake-`claude` fixture PATH + SAKI_RUNS_DIR + DEV_MODE) never applies. A reused, no-env Go backend on
# :8788 then spawns the REAL `claude`, which hangs → the go-spawn SSE specs time out. Freeing the
# ports forces a clean, env-carrying boot every time.
#
# Idempotent and non-fatal: no listener on a port is fine, and it always exits 0 so it never blocks
# the e2e run (lsof missing → empty result → skip; matters only locally, CI boots fresh anyway).
for port in 5180 8787 8788; do
  pids="$(lsof -tiTCP:"$port" -sTCP:LISTEN 2>/dev/null)"
  if [ -n "$pids" ]; then
    echo "pree2e: freeing :$port (pids: $pids)"
    # shellcheck disable=SC2086 # word-splitting is intentional — kill each pid
    kill -9 $pids 2>/dev/null
  fi
done
exit 0
