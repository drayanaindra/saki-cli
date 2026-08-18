<!-- prd-blocking: 0 -->
<!-- slices: 3 -->
<!-- appetite: medium -->
<!-- revision-passes: 2 -->
<!-- prd-locked: @codex · 2026-08-16 · ui:none -->

# PRD: `saki init-env` — provision an engine profile

**Owner:** unassigned · **Status:** Locked · **Updated:** 2026-08-16 · **Appetite:** medium · **Item:** F6

## 1. TL;DR

Add `saki init-env --engine <claude|codex|opencode> [--profile <dir>] [--cwd <repo>]` as the supported
entry point for preparing an engine profile to resolve `/saki-builder:*` commands. The command validates its target repository,
uses an engine-specific provisioning adapter, and verifies the result through the same doctor proof
used before a run. It is idempotent, reports whether it changed anything, and never treats a child
engine's exit code alone as proof of provisioning.

The command owns setup; `saki doctor` remains the read-only verifier. The existing Claude `/init-env`
run is not silently repurposed: Claude provisioning is a dependency on F4's installed-plugin plus
enabled-plugin proof. Until that proof exists, a Claude request must fail clearly as unsupported/not
verified rather than report a false green.

## 2. Problem & Evidence

Today an operator must manually provision each non-default engine. The canonical codex setup is two
plugin commands documented in `docs/saki-cli-agent-guide.md:124-126`; the repository also has
`scripts/install-codex-skills.sh`, but it is a checker with an opt-in symlink fallback, not a CLI
provisioning contract. Opencode setup is separately documented as a global plugin plus package
install (`docs/saki-cli-agent-guide.md:120`).

The backend already has the important safety seam: `CodexSkillsProof` reads the child-visible codex
home and `OpencodePluginProof` reads the child-visible opencode config. F2's doctor consumes those
proofs. F6 should call setup adapters and then those proofs, rather than duplicate profile parsing.

The current `/init-env` route is a privileged Claude engine spawn (`SpawnInit`, `kind:init`), with
durable run state and a deduplication lane. That is a different operation from installing a CLI
plugin and must remain separate unless a future PRD explicitly changes its semantics.

## 3. Primary Job to Be Done

**J1** — As an operator or bootstrapping agent, when I select an engine for a repo, I want one
command to make its profile ready for saki-builder work and prove readiness immediately, without
hand-editing config files or memorizing engine-specific commands.

## 4. Desired Outcomes

| # | Outcome | Target | Method | JTBD |
|---|---|---|---|---|
| 5.1 | A selected supported engine is ready after setup | 0 → 1 | run `saki init-env --engine <e>` then `saki doctor --json --profile <dir>`; the selected report is `ok` | J1 |
| 5.2 | Repeat setup is harmless | unknown → 0 duplicate changes | invoke twice against the same profile; second invocation makes no duplicate config entry and remains successful | J1 |
| 5.3 | Setup failures are actionable | 0 → 1 | missing binary, invalid target, and child-command failure each produce a distinct non-zero CLI result with remediation | J1 |

## 5. Appetite & Kill Criteria

**Appetite:** medium — a few days, three vertical slices.

Stop and reopen the design if any adapter cannot provision a profile without parsing or rewriting
another engine's private state, or if post-provision doctor cannot prove the exact child-visible
profile that the run would use.

## 6. Solution Shape

Introduce a narrow backend/usecase provisioning port with one adapter per engine. The CLI validates
`--engine`, `--cwd`, and `--profile` before making a network call, then calls `POST /api/init-env` with
`{cwd, engine, profile}`. `cwd` is the normalized repository working directory; `profile` is optional
and means the same engine-profile root accepted by doctor/run. A default profile may legitimately be
outside the repository (for example `~/.codex`), so the repository-containment rule applies to `cwd`
and repo-relative inputs, not to the operator's explicitly selected home profile. The backend still
mutates only the selected engine namespace within that profile.

Adapters may invoke the engine's official installer commands, but arguments are fixed by the engine
mapping and never assembled through a shell. The route returns structured `{engine, profile, changed,
status, reason, fix}` data and then runs the shared proof. `changed` is true only when the adapter's
before/after profile fingerprint differs; an installer exit code alone never sets it true. The route
returns `200` for a completed setup attempt (including `status:failed`), `422` for invalid paths or
engine values, and leaves transport/unreachable and OriginGuard semantics to the existing CLI/client
contract.

Profile resolution must match spawn resolution exactly:

| Engine | Child-visible profile | Provisioning contract |
|---|---|---|
| codex | `<profile>/codex` or `~/.codex` | marketplace add, then `codex plugin add saki-builder@saketek`; existing registration is a no-op |
| opencode | `<profile>/opencode` or `~/.config/opencode` | install/register `@saketek/saki-builder`; existing plugin entry is a no-op |
| claude | `CLAUDE_CONFIG_DIR`/default Claude profile | depends on F4's installed + enabled plugin proof; no false success before that proof ships |

No credentials are accepted or printed. No profile is copied into the repository. A supplied profile
path must be absolute or repository-contained according to the existing CLI path policy, and all
filesystem mutations are limited to the selected engine's own namespace.

## 7. Vertical Slices

**Slice 1 — CLI contract and codex adapter.** Add `saki init-env`, `--cwd`/`--profile` parsing, the
`POST /api/init-env` loopback route, fixed argv execution, idempotent codex provisioning, structured output, exit-code
mapping, and tests for validation, missing binary, success, and repeat invocation.

**Slice 2 — opencode adapter and shared verification.** Add idempotent opencode setup, reuse the
existing opencode path/proof mapping, verify immediately after mutation, and cover malformed config,
plugin already present, and installer failure without partial success.

**Slice 3 — Claude support after F4.** Add the Claude adapter only once installed-plugin and
enabled-plugin resolution is available. The slice must prove the selected Claude profile via doctor;
before then, `--engine claude` returns a typed `NOT_VERIFIED`/unsupported result and makes no write.

## 8. Acceptance Criteria

### Slice 1

- 1.1 [auto] `saki init-env --engine codex --cwd <repo> --profile <dir>` validates input before any HTTP request;
  an empty/unknown engine or escaping profile exits `2` (`EXIT.USAGE`).
- 1.2 [auto] With a real codex binary and an empty writable profile, setup produces a doctor-verifiable
  codex profile and exits `0`; stdout JSON includes `engine`, `profile`, `changed`, and `status`.
- 1.3 [auto] Repeating the command leaves the profile semantically unchanged, does not duplicate the
  plugin registration, and exits `0` with `changed:false`.
- 1.4 [auto] A missing codex binary exits `1` with a remediation naming the binary; no profile files
  are created.
- 1.5 [auto] `POST /api/init-env` rejects a non-loopback Host with `403`, rejects an invalid engine
  or repo-relative escaping path with `422`, and never invokes an adapter for either request.

### Slice 2

- 2.1 [auto] An empty opencode profile is provisioned and then passes the shared opencode proof;
  setup and `saki doctor --json` agree on `ok`.
- 2.2 [auto] Existing plugin registration is preserved byte-for-byte where possible and setup is a
  no-op; malformed config fails without truncating or replacing the original file.
- 2.3 [auto] Installer failure is surfaced as a non-zero result and never returned as `status:ok`.
- 2.4 [auto] The adapter's before/after fingerprint makes `changed:false` observable for an already
  provisioned profile and `changed:true` only when setup actually changes the selected namespace.

### Slice 3

- 3.1 [auto] With F4's Claude proof available, Claude setup creates/enables the selected profile and
  the selected report from doctor is `ok`.
- 3.2 [auto] Before F4 lands, Claude setup exits with the explicit not-verified result, performs no
  write, and does not claim success.
- 3.3 [auto] For all three engines, a successful command followed immediately by doctor has no
  manual-edit step and uses the same profile path the next run would spawn.

## 9. Business Rules & Invariants

1. `init-env` is the only mutating setup command; `doctor` remains strictly read-only.
2. Exit `0` means the selected engine's shared proof passed. A child installer exit `0` is
   insufficient.
3. Setup is idempotent and preserves unrelated engine namespaces and credentials.
4. Engine-specific environment variables are scrubbed from child processes exactly as spawn adapters
   already require; an installer never receives another engine's token namespace. The selected
   profile is passed through the engine's own namespace variable only (`CODEX_HOME`, `XDG_CONFIG_HOME`,
   or `CLAUDE_CONFIG_DIR`).
5. Provisioning is loopback-only and profile paths cannot escape the allowed repository boundary.
6. Real engine binaries are required for engine invocation evidence; fake binaries may test argv and
   error plumbing only.

## 10. Non-goals

- Installing engine binaries, authenticating accounts, or managing credentials.
- Replacing the existing privileged Claude `/init-env` run workflow.
- Copying/symlinking the whole saki-builder plugin into every profile.
- Automatically provisioning all engines when one is selected.
- Adding Claude support to doctor as a side effect without F4's two-file enablement proof.

## 11. Dependencies & Risks

- **F4 dependency:** Claude cannot satisfy the success signal until doctor can prove enablement.
- **Installer drift:** command forms are external contracts; keep them in one engine mapping and add
  real-binary e2e coverage where available.
- **Partial writes:** write only through atomic/temp-file or engine-native commands; preserve the
  original config on parse or child failure.
- **Profile mismatch:** tests must set the same profile inputs used by the spawn environment, not only
  the parent process's inherited variables.

## 12. Compatibility

Additive CLI and HTTP surface. Existing `doctor`, run routes, exit codes, environment scrubbing,
journaling, and the privileged `/init-env` run path remain unchanged. The route must not return
credentials or expose profile contents beyond the existing doctor reason/fix contract.
