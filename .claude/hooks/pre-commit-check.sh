#!/usr/bin/env bash
# PreToolUse:Bash — run BOTH suites before a commit lands.
#
# Dual-stack, so both must be green: a Go change that breaks the CLI's contract (or vice versa)
# is exactly the failure a single-suite gate misses. vitest is scoped to src/**/*.test.ts by
# vitest.config.ts, so e2e/ (Playwright, needs real engine binaries) is never pulled in here.
#
# Honours --no-verify. Exit 2 blocks the command and returns stderr to Claude.
set -uo pipefail

CMD="$(jq -r '.tool_input.command // empty' 2>/dev/null)"
[ -n "$CMD" ] || exit 0

# Only gate real commits. Anchored so 'git log --grep "git commit"' and prose don't trip it.
printf '%s\n' "$CMD" | grep -qE '(^|[;&|]|\s)git\s+(-[^ ]+\s+)*commit(\s|$)' || exit 0
printf '%s\n' "$CMD" | grep -qE '(--no-verify|(^|\s)-n(\s|$))' && exit 0

ROOT="${CLAUDE_PROJECT_DIR:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"
fails=""

if ! ts_out="$(cd "$ROOT" && npx tsc --noEmit 2>&1)"; then
  fails="${fails}
--- tsc --noEmit ---
${ts_out}"
fi

if ! vt_out="$(cd "$ROOT" && npx vitest run 2>&1 | tail -30)"; then
  fails="${fails}
--- vitest run ---
${vt_out}"
fi

if ! go_out="$(cd "$ROOT/backend" && go test ./... 2>&1 | tail -30)"; then
  fails="${fails}
--- go test ./... ---
${go_out}"
fi

[ -z "$fails" ] && exit 0

printf 'Commit blocked — the pre-commit gate is red:%s\n\nFix these, or commit with --no-verify if the failure is pre-existing and unrelated.\n' "$fails" >&2
exit 2
