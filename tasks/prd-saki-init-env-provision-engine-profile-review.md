# PRD Review: `saki init-env` — provision an engine profile

**Verdict:** SHIP · **Readiness:** READY · **Review rounds:** 2 · **Blocking findings:** 0

## Review summary

The second pass is slice-coherent and startable. It separates mutation (`init-env`) from verification
(`doctor`), pins child-visible profile resolution, preserves the existing privileged Claude
`/init-env` run semantics, and makes the F4 dependency explicit rather than promising a false Claude
green. Each mutating slice has failure-path and idempotency criteria, and the loopback/path/env
security boundaries are stated.

## Findings and dispositions

| ID | Finding | Disposition |
|---|---|---|
| R1 | The roadmap names three engines, but F2 doctor currently proves only codex/opencode. | Fixed in PRD: Claude is a gated Slice 3 dependency on F4; before F4, the command returns not-verified and writes nothing. |
| R2 | Existing `/init-env` is a privileged Claude run, not a local installer. | Fixed in PRD: explicitly preserved and excluded from silent repurposing. |
| R3 | Installer success alone can be a false green. | Fixed in solution shape and ACs: every successful mutation is followed by the shared proof. |
| R4 | A profile path could affect the wrong engine namespace or escape the repo. | Fixed in rules: exact child-visible resolution, input validation before HTTP, namespace isolation, and boundary checks. |
| R5 | Re-running setup could duplicate plugin entries or overwrite malformed config. | Fixed with idempotency and malformed-config preservation criteria. |
| R6 | The CLI contract named `--profile` but did not define `--cwd` or distinguish repository containment from a default home profile. | Fixed in §6 and Slice 1: normalized `{cwd, engine, profile}` request; default homes may be outside the repo. |
| R7 | The backend route and HTTP status/body contract were implicit. | Fixed in §6: `POST /api/init-env`, structured response, `200` attempt / `422` validation, existing loopback errors. |
| R8 | `changed` was undefined for installer-backed setup and could be inferred incorrectly from a zero exit. | Fixed in §6 and AC 2.4: adapter-owned before/after fingerprint; zero exit is never proof. |

## Readiness matrix

| Check | Result |
|---|---|
| Primary job and target user | Pass |
| Appetite and kill criterion | Pass |
| Vertical slices | Pass — 3 slices, each independently testable |
| Acceptance criteria | Pass — success, failure, security, idempotency, and dependency-gated Claude paths |
| Compatibility and bounded contexts | Pass |
| Exit-code contract | Pass — usage `2`, setup/provisioning error `1`, unreachable `3`, guarded route `403` |
| Credential/environment safety | Pass |
| Real-engine evidence boundary | Pass |

**Implementation handoff:** begin Slice 1 with the CLI parser/validation, a narrow provisioning
port, and the codex adapter. Do not implement Claude support in this PRD until F4's proof seam is
available.
