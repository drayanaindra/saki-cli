#!/usr/bin/env bash
# Check (and if needed, provision) a codex home so `codex exec` can resolve the studio's
# /saki-builder:* journey commands.
#
# WHY THIS EXISTS. codex resolves a slash command that arrives in the message — but only if the skill
# is discoverable. Unprovisioned, every journey run still exits 0; the model simply answers that it
# cannot find the command, and the classifier parks a build that never started. The Go spawner refuses
# that spawn up front (CodexSkillsProof), and this script reports the same condition ahead of time.
#
# THE CANONICAL INSTALL IS THE PLUGIN — `codex plugin add saki-builder@saketek`. It registers
# `[plugins."saki-builder@saketek"]` in $CODEX_HOME/config.toml and carries the skills, agents and
# hooks itself. This script does NOT duplicate that: symlinking the Claude plugin's skills on top
# creates a second, VERSION-SKEWED copy of every skill (a stale cache shadowing the installed one) and
# eats the skills context budget. Symlinking is therefore opt-in (--symlink), for the one case the
# plugin cannot serve: an ephemeral pinned profile, like the studio's e2e throwaway home.
#
# Usage:
#   scripts/install-codex-skills.sh                  # check ~/.codex, print the fix if unprovisioned
#   CODEX_HOME=/path/to/profile/codex  scripts/install-codex-skills.sh
#   scripts/install-codex-skills.sh --symlink        # fallback: link skills into $CODEX_HOME/skills
#   scripts/install-codex-skills.sh --symlink --dry-run
set -euo pipefail

SYMLINK=0; DRY_RUN=0
for a in "$@"; do
  case "$a" in
    --symlink) SYMLINK=1 ;;
    --dry-run) DRY_RUN=1 ;;
    -h|--help) sed -n '2,22p' "$0"; exit 0 ;;
    *) echo "unknown flag: $a (try --help)" >&2; exit 2 ;;
  esac
done

CODEX_HOME="${CODEX_HOME:-$HOME/.codex}"
CONFIG="$CODEX_HOME/config.toml"
DEST="$CODEX_HOME/skills"

echo "codex home: $CODEX_HOME"

# --- 1. the canonical check: is the plugin registered and not disabled? -------------------------
# Mirrors backend/infra/codex.go pluginRegistered: line-anchored table match, bounded by the next
# table, so a commented-out registration or a LATER table's `enabled = false` is never misread.
plugin_registered() {
  [ -f "$CONFIG" ] || return 1
  awk '
    /^[[:space:]]*\[plugins\.[""]?saki-builder@/ { intable=1; disabled=0; found=1; next }
    /^[[:space:]]*\[/                            { if (intable) intable=0 }
    intable && /^[[:space:]]*enabled[[:space:]]*=[[:space:]]*false[[:space:]]*$/ { disabled=1 }
    END { exit (found && !disabled) ? 0 : 1 }
  ' "$CONFIG"
}

if plugin_registered; then
  echo "✓ saki-builder plugin is registered in $CONFIG"
  echo "✓ CodexSkillsProof passes — the studio will spawn codex runs for this home"
  [ "$SYMLINK" = "1" ] && echo "  (--symlink ignored: the plugin already provides the skills; linking would duplicate them)"
  exit 0
fi

# --- 2. loose skills (a manual / pinned-profile install) ---------------------------------------
if [ -f "$DEST/build/SKILL.md" ]; then
  echo "✓ loose saki-builder skills present at $DEST"
  echo "✓ CodexSkillsProof passes"
  exit 0
fi

# --- 3. unprovisioned ---------------------------------------------------------------------------
if [ "$SYMLINK" != "1" ]; then
  cat >&2 <<EOF
✗ this codex home cannot resolve the saki-builder commands.

  Install the plugin (recommended — it carries skills, agents and hooks, and stays in step):

      codex plugin marketplace add https://gitlab.com/drayanaindra/saki-builder.git
      codex plugin add saki-builder@saketek

  Or, for an ephemeral/pinned profile where a marketplace plugin is impractical:

      CODEX_HOME=$CODEX_HOME $0 --symlink
EOF
  exit 1
fi

# --- 4. --symlink fallback ----------------------------------------------------------------------
# Links (never copies) each skill dir, so a plugin upgrade is picked up with no re-run. Prefers a
# codex-side plugin cache over the Claude one so the linked skills match the codex install's version.
find_plugin_root() {
  if [ -n "${SAKI_PLUGIN_ROOT:-}" ]; then printf '%s\n' "$SAKI_PLUGIN_ROOT"; return; fi
  local d
  for cache in "$HOME/.codex/plugins/cache/saketek/saki-builder" "$HOME/.claude/plugins/cache/saketek/saki-builder"; do
    [ -d "$cache" ] || continue
    while IFS= read -r d; do
      [ -d "$d/config/skills" ] && { printf '%s\n' "$d"; return; }
    done < <(find "$cache" -mindepth 1 -maxdepth 1 -type d | sort -Vr)
  done
  return 1
}

PLUGIN_ROOT="$(find_plugin_root)" || {
  echo "✗ could not find a saki-builder plugin cache; set SAKI_PLUGIN_ROOT=<path>." >&2; exit 1; }
SRC="$PLUGIN_ROOT/config/skills"
echo "  linking from: $SRC"
[ "$DRY_RUN" = "1" ] && echo "  (dry run — nothing will be written)"
[ "$DRY_RUN" = "1" ] || mkdir -p "$DEST"

linked=0; skipped=0
for dir in "$SRC"/*/; do
  [ -d "$dir" ] || continue
  name="$(basename "$dir")"
  # A skill is a DIRECTORY carrying a SKILL.md; the plugin's config/skills also holds flat role
  # personas (architect.md, engineer.md) which are not skills.
  [ -f "$dir/SKILL.md" ] || { skipped=$((skipped + 1)); continue; }
  target="$DEST/$name"
  # Never clobber a real directory the operator (or codex) owns — only replace our own symlink.
  if [ -e "$target" ] && [ ! -L "$target" ]; then
    echo "  ! $name — a real directory already exists, left untouched"; skipped=$((skipped + 1)); continue
  fi
  if [ "$DRY_RUN" = "1" ]; then echo "  + $name"; else ln -sfn "${dir%/}" "$target"; fi
  linked=$((linked + 1))
done
echo "✓ ${linked} skill(s) linked, ${skipped} skipped"

if [ "$DRY_RUN" != "1" ] && [ ! -f "$DEST/build/SKILL.md" ]; then
  echo "✗ $DEST/build/SKILL.md missing — the studio will refuse codex spawns" >&2; exit 1
fi
