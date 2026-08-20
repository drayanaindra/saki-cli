# F6 slice 4 research context — Claude provisioning after F4

**Date:** 2026-08-20
**Source branch:** `feature/saki-init-env-provision-engine-profile`
**Scope:** Finish the F6 Claude path after F4 proof shipped; reconcile the default profile root before adding any installer adapter.

## Verified shipped state

- F4 is shipped. `DoctorEngines` reports `codex`, `opencode`, and `claude` in fixed order (`backend/usecase/doctor.go:5-8`).
- `EngineProfileProof` dispatches Claude to `ClaudeProfileProof` (`backend/infra/spawner.go:194-212`). The same proof is used by doctor and spawn preflight.
- `ClaudeProfileProof` reads `<configDir>/plugins/installed_plugins.json` and `<configDir>/settings.json`; unpinned resolution defaults to `$HOME/.claude` (`backend/infra/claude.go:35-86`).
- Claude plugin resolution has fixed precedence: `saketek@saki-builder`, then `saki-builder@saketek`. The selected identity must have a non-empty installed version and an exact `enabledPlugins` entry set to `true` (`backend/infra/claude.go:62-75`).
- The proof is read-only and fails closed on missing files, malformed JSON, unsupported identities, empty records, missing versions, disabled higher-precedence identities, spelling mismatch, unrelated settings, and wrong JSON types (`backend/infra/claude_test.go:46-86`).
- Explicit Claude profile spawning sets `CLAUDE_CONFIG_DIR=<profile>` through the shared environment helper (`backend/infra/spawner.go:215-260,272-329`). Unpinned Claude removes inherited `CLAUDE_CONFIG_DIR`, so the child uses Claude's default profile.
- `InitEnvService.Provision` still returns `status:"not_verified"` for Claude before binary lookup, profile locking, proof, or adapter invocation (`backend/usecase/initenv.go:67-83`). This is the pre-F4 boundary and must be replaced only by the verified positive path.
- `EngineProvisioner` currently maps only Codex and OpenCode argv, fingerprints only their proof files, and creates only the Codex home (`backend/infra/initenv.go:33-89,145-169`).

## Default-profile mismatch and correction

Before this continuation, the service lock key used `$HOME/.config/claude` for unpinned Claude (`backend/usecase/initenv.go:230-254`), while the proof and spawn semantics used `$HOME/.claude`. That allowed the provisioning gate to serialize a different namespace from the one Claude proof would read.

The helper now returns `$HOME/.claude` for `domain.EngineClaude`, matching `claudeProfilePaths(nil)` and Claude's unpinned child behavior (`backend/usecase/initenv.go:230-255`, current working-tree change). The regression test requires unpinned Claude and explicit `$HOME/.claude` to share a lock key and rejects the legacy `.config/claude` root (`backend/usecase/initenv_test.go:323-339`, current working-tree change).

This is a prerequisite contract, not the Claude adapter itself. It must be formatted and tested from `backend/`; the repository root is not a Go module.

## Verified profile contract

| Input | Proof reads | Spawn environment | Provisioning lock must use |
|---|---|---|---|
| `profile == nil` | `$HOME/.claude/plugins/installed_plugins.json` and `$HOME/.claude/settings.json` | no inherited `CLAUDE_CONFIG_DIR`; Claude default | `$HOME/.claude` |
| `profile == <dir>` | `<dir>/plugins/installed_plugins.json` and `<dir>/settings.json` | `CLAUDE_CONFIG_DIR=<dir>` | `<dir>` |

An explicit profile is the profile root, not `<profile>/.claude`. No profile copying, symlinking, or repository-local fallback is allowed.

## External installer contract research status

Official Claude documentation confirms:

- Shell installation syntax: `claude plugin install <plugin> [options]`.
- The plugin identifier can be `plugin-name@marketplace-name`.
- `--scope user` is the default and is the required scope for user-profile provisioning.
- Interactive marketplace registration is documented as `/plugin marketplace add <source>`.
- The marketplace documentation lists only the interactive `/plugin marketplace add` form; it does not document a non-interactive `claude plugin marketplace add` command.

Official settings documentation confirms that `CLAUDE_CONFIG_DIR` replaces the default user root `~/.claude` for the process. With `CLAUDE_CONFIG_DIR=<profile>` and user scope, the installer writes user plugin data under `<profile>/plugins/` and user settings under `<profile>/settings.json`; those are exactly the paths consumed by `ClaudeProfileProof` (`https://code.claude.com/docs/en/settings`, `https://code.claude.com/docs/en/plugins-reference`). B2 is therefore resolved.

B1 is resolved by the installed Claude Code `2.1.237` CLI help and the verified local marketplace manifest. The fixed user-scope argv is:

```text
claude plugin marketplace add https://gitlab.com/drayanaindra/saki-builder.git --scope user
claude plugin install saki-builder@saketek --scope user
```

The marketplace name `saketek` and plugin name `saki-builder` come from the installed `.claude-plugin/marketplace.json` and `.claude-plugin/plugin.json`; the GitLab source URL comes from the plugin manifest repository field. The CLI help confirms `marketplace add [options] <source>` accepts a URL and `--scope user`; official plugin reference confirms `claude plugin install <plugin> --scope user`. With `CLAUDE_CONFIG_DIR=<profile>`, user scope targets the same profile root used by `ClaudeProfileProof`.

The adapter must preserve this exact argv, use no shell interpolation, and pass `--scope user` explicitly so profile routing cannot drift with a future CLI default.

## Required positive-path shape

1. Normalize and validate `cwd`, engine, and absolute profile.
2. Run no Claude binary preflight; Claude has no PATH requirement in the shared doctor contract.
3. Acquire the corrected per-engine profile lock.
4. Run `ClaudeProfileProof` through the shared proof port. If it passes, return `ok` with `changed:false` and never invoke the installer.
5. Run the verified fixed Claude installer argv with `CLAUDE_CONFIG_DIR` set only for an explicit profile and all foreign engine namespaces scrubbed.
6. Fingerprint exactly the two files Claude proof reads; do not expose contents or credentials.
7. Run the same shared proof again. Only a passing proof returns `ok`; installer exit status alone never decides success.
8. Immediately run doctor against the same profile in a real-binary end-to-end test and assert Claude is `ok`.

## Required tests

- Default Claude lock key matches `$HOME/.claude`; explicit `$HOME/.claude` shares it.
- Fixed Claude argv and exact `CLAUDE_CONFIG_DIR` routing through a fake binary; assert no Codex/OpenCode namespace reaches the child.
- Claude fingerprint changes only when `installed_plugins.json` or `settings.json` changes.
- Missing binary behavior is not applicable to Claude; the service must not introduce a Claude PATH check.
- Already-proven Claude profile skips the adapter and reports `changed:false`.
- Installer failure followed by a passing proof is accepted; installer success followed by a failing proof is failed.
- Profile files and unrelated engine namespaces remain untouched on failure.
- Real Claude binary e2e provisions a fresh explicit profile, then runs doctor against the same profile; repeat setup is a no-op.
- HTTP and CLI response contracts retain `{engine, profile, changed, status, reason, fix}` and existing exit codes.

## Non-goals and invariants

- Doctor remains read-only and never invokes provisioning or an engine.
- No credentials are accepted, printed, copied, or passed through chat.
- No engine binary installation or authentication.
- No change to the privileged Claude `/init-env` run workflow.
- No shell interpolation; installer arguments remain fixed vectors.
- Backend remains loopback-only with `OriginGuard`.
- No DB, journal, schema, or migration changes.
- Fake binaries prove argv/environment/error plumbing only; real Claude invocation requires the real binary.

## Graphify

`graphify-out/GRAPH_REPORT.md` is absent. Fewer than 20 targeted files are required, so graph research is skipped. `docs/project-context.md` remains the source for loopback, credential, journal, and cross-process invariants.
