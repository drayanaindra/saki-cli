# Releasing

`saki` publishes two artifacts on every release: the `@saketek/saki-cli` npm package, and the
darwin/linux `saki-backend` binaries attached to the matching GitHub Release. Publishing is a
human-run action — no agent triggers it automatically.

## Prerequisites (one-time)

- Repo secret `NPM_TOKEN` — an npm automation token with publish rights to `@saketek/saki-cli`,
  set in GitHub repo Settings → Secrets and variables → Actions.
- Push access to `main` and permission to push tags.

## Procedure

1. Decide the semver bump (`patch` / `minor` / `major`) from what's in `## [Unreleased]` in
   `CHANGELOG.md`.
2. `npm version <patch|minor|major>` — bumps `package.json`'s `version` and creates a git commit
   (do **not** let this also create the tag yet; `npm version` tags by default — either pass
   `--no-git-tag-version` and tag manually after the CHANGELOG edit below, or amend the tag after
   step 3).
3. Move the `## [Unreleased]` section content in `CHANGELOG.md` under a new heading
   `## [X.Y.Z] - YYYY-MM-DD` (today's date), and add the two new link references at the bottom
   (`[Unreleased]: .../compare/vX.Y.Z...HEAD` and `[X.Y.Z]: .../releases/tag/vX.Y.Z`). Leave a
   fresh empty `## [Unreleased]` heading above it.
4. Commit: `git commit -am "chore(release): vX.Y.Z"`.
5. Tag: `git tag vX.Y.Z`.
6. `git push origin main --tags`.
7. `.github/workflows/release.yml` takes over from here:
   - `build` — cross-compiles `saki-backend` for darwin/linux × amd64/arm64.
   - `release` — generates `SHA256SUMS.txt` and publishes a GitHub Release for the tag with all
     4 binaries + the checksum file attached.
   - `publish` — runs `npm publish --access public --provenance` using `NPM_TOKEN`.
8. Verify: `npm view @saketek/saki-cli version` matches `X.Y.Z`, and
   `npm install -g @saketek/saki-cli && saki status` resolves a working backend on a fresh machine.

## If it goes wrong

- `publish` job fails after `release` already created the GitHub Release: fix the issue, re-run
  just the `publish` job from the Actions UI (or `npm publish --access public --provenance`
  locally from a clean `main` checkout at the tagged commit) — the release/binaries are already
  correct and don't need rebuilding.
- Never `npm unpublish` past the 72-hour window; publish a patch version instead.
