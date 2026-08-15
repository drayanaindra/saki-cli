#!/usr/bin/env bash
# PostToolUse:Edit|Write — type-check the side of the repo that was actually touched.
#
# saki-cli is dual-stack, so a blanket "run the type checker" would run BOTH checkers on every
# edit and roughly triple the feedback loop. Route on the edited path instead: a .ts edit runs
# tsc, a .go edit runs go vet, anything else is a no-op.
#
# Exit 2 is the contract that surfaces stderr back to Claude; exit 0 stays silent.
set -uo pipefail

ROOT="${CLAUDE_PROJECT_DIR:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"
FILE="$(jq -r '.tool_input.file_path // empty' 2>/dev/null)"
[ -n "$FILE" ] || exit 0

case "$FILE" in
  *.ts)
    # e2e/ is Playwright and is excluded from tsconfig's `include` — nothing to check.
    case "$FILE" in */e2e/*) exit 0 ;; esac
    out="$(cd "$ROOT" && npx tsc --noEmit 2>&1)" && exit 0
    printf 'tsc --noEmit failed after editing %s:\n%s\n' "$FILE" "$out" >&2
    exit 2
    ;;
  *.go)
    out="$(cd "$ROOT/backend" && go vet ./... 2>&1)" && exit 0
    printf 'go vet ./... failed after editing %s:\n%s\n' "$FILE" "$out" >&2
    exit 2
    ;;
esac

exit 0
