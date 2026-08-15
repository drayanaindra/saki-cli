#!/usr/bin/env bash
# PreToolUse:Edit|Write — refuse edits to files this project never hand-edits.
#
# Project-specific only. The catastrophic-command guard (rm -rf /, force-push to main, secrets in
# argv) is already active globally via the saki-builder plugin — do NOT duplicate it here.
set -uo pipefail

FILE="$(jq -r '.tool_input.file_path // empty' 2>/dev/null)"
[ -n "$FILE" ] || exit 0

deny() { printf 'BLOCKED: %s\n%s\n' "$1" "$2" >&2; exit 2; }

case "$FILE" in
  */.env|*/.env.*)
    case "$FILE" in *.env.example) exit 0 ;; esac
    deny "$FILE holds real credentials and is gitignored." \
         "Edit .env.example instead, or have the operator set the value in their own shell." ;;
  */package-lock.json|*/go.sum)
    deny "$FILE is a lockfile — generated, never hand-edited." \
         "Run 'npm install <pkg>' or 'go get <mod>' and let the tool rewrite it." ;;
  */dist/*)
    deny "$FILE is build output." \
         "Edit the source under src/ or backend/, then 'npm run build' / 'npm run backend:build'." ;;
  */node_modules/*)
    deny "$FILE is a vendored dependency." "Changes here are erased by the next install." ;;
esac

exit 0
