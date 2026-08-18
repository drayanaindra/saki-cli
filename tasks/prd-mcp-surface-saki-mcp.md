<!-- prd-blocking: 0 -->
<!-- slices: 4 -->
<!-- appetite: medium -->
<!-- revision-passes: 2 -->
<!-- prd-locked: @drayanaindra · 2026-08-16 · ui:none -->

# PRD: MCP surface (`saki mcp`)

**Owner:** unassigned · **Status:** Locked · **Updated:** 2026-08-16 · **Appetite:** medium — a few days · **Item:** F3

⚠ DISCOVERY-RISK: the core demand assumption — that agents driving `saki` via MCP tool calls (instead
of a shell) is worth building for — is `assumed`, not validated by usage data. `saki` is a local,
single-operator tool with no analytics or ticket history to draw on. The assumption is grounded in the
maintainer's own stated intent (`README.md:102` "No MCP surface (`saki mcp`) for agents that prefer
tools over a shell", `tasks/roadmap.md` F3), not in observed agent demand.

**Bet:** accepted — proceeding on stated maintainer intent alone, with no external validation. The item
being on the roadmap already is prior intent, not new evidence, and is not being treated as such; the
warrant for proceeding through the PRD/review front half is `/saki-builder:pickup F3`'s own design (its
front half runs with no human gate) plus the explicit invocation of it now. The DISCOVERY-RISK above
stays visible rather than being resolved away, and is re-surfaced at the `/saki-builder:proto` human
gate — the workflow's single human checkpoint before build — for final accept/reject. AUTO-RESOLVED.

## 1. TL;DR

Add `saki mcp` — a stdio MCP server that exposes `saki`'s core journey commands (status, doctor,
roadmap visibility, PRD show/lock, the full run lifecycle, branch/MR) as typed MCP tools, so an agent
whose harness prefers tool calls over a shell can drive the same PRD → plan → build → QA → review
journey without forking the CLI's exit-code contract. Every tool handler calls the exact `cmd*`
function the CLI dispatcher already calls — the MCP layer only translates the CLI's success/failure
signal into an MCP tool result, it never re-implements command logic.

## 2. Problem & Evidence

Today, an agent that wants to drive `saki` must shell out and parse either a human table or a `--json`
line, then re-derive success/failure from the process exit code (`src/exit.ts:8`) — a text-parsing step
that agent harnesses built around typed tool-calling do more reliably as a structured tool result than
as shell-output parsing (`assumed` — this specific reliability claim is not independently cited; it is
the premise the load-bearing assumption below rests on). The CLI itself already draws this exact
distinction for HTTP: two studio routes return failure inside an HTTP 200 body, and the CLI has to
explicitly re-map that to a non-zero exit so a shell caller doesn't misread it as success
(`src/exit.ts:6-9`). An MCP surface has the same translation problem one level up: whatever encodes
"this failed" for the CLI must also make an MCP tool result read as failed, or the same false-success
bug reappears for tool-calling agents instead of shell callers.

This is a capability bet, not a harm being fixed: there is no measured incident, error rate, or time
lost being cited — the CLI's shell interface works today. The claim is that a typed tool surface would
serve tool-calling agents more reliably than shell-parsing does, not that shell-parsing is currently
failing anyone.

Grounding for the technical claims above is code-observed: `src/exit.ts` (`observed`, the exit-code
contract), `src/routes.ts` + `src/client.ts` (`observed`, the CLI is a thin client with no routes of its
own), `package.json` (`observed`, zero runtime `dependencies` today — an MCP SDK would be the CLI's
first production dependency), and a repo-wide grep for "mcp" (`observed`, confirms no existing MCP
surface — this is greenfield). The demand for the feature itself — that this population of MCP-first
agents exists and is worth serving now — is not grounded in usage data; see the DISCOVERY-RISK banner
above.

**Load-bearing assumption:** agents whose harness exposes MCP tool calls (rather than, or in addition
to, a shell) want to drive `saki`'s journey the same way shell-based agents do today — `assumed`.
**Spike:** searched for 2026 precedent on CLIs exposing an `mcp` subcommand for agent tool-calling →
the CLI↔MCP bridging pattern is established (`mcp2cli` generates a full CLI from an MCP server's tool
catalog — the inverse bridge, confirming interop between the two is a live pattern), but active 2026
commentary also argues shell/CLI tool-calling now *beats* MCP for many agents precisely because loading
many MCP tool schemas up front bloats context (one cited benchmark: ~47k → ~400 tokens after moving off
upfront MCP tool-loading, mcp-cli). Inconclusive on saki-specific demand — kept `assumed` — but it
directly motivated the scope cut in §8 below (17 candidate tools → 13 in v1; §12 records the 4
deferred). (source: WebSearch "CLI tools expose commands as MCP server... 2026" — oneuptime.com/blog
"Why CLI is the New MCP for AI Agents"; jannikreinhard.com "CLI Tools vs MCP: Better AI Agents With Less
Context"; `agent-clis.sh/mcp2cli`)

## 3. Primary Job to be Done

**J1** — When my coding-agent harness needs to know whether a `saki`-driven action (a run, a PRD
operation, a branch/MR action) succeeded or failed, I want a reliable, structured signal I don't have
to hand-write a parser for, so I can automate the PRD → plan → build → QA → review journey without a
custom text/exit-code parser per harness.

## 4. Related Jobs

**J2** — When I'm debugging why an agent's `saki`-driven run failed, I want the MCP tool result to
carry the same fidelity as the CLI's exit code and hint, so I can diagnose without dropping back to the
CLI.

## 5. Desired Outcomes / Success Metrics

| # | Outcome (Minimize/Maximize [metric] when [context]) | Target | Basis | Method | JTBD |
|---|---|---|---|---|---|
| 5.1 | Maximize the share of the in-scope journey command set (13 tools, §8) exposed as an MCP tool under `saki mcp` | 100% of the 13 in-scope tools | baseline 0→100% (`observed`: repo-wide grep for "mcp" today returns no existing surface) | query: count registered MCP tools vs. the 13-tool in-scope list in a coverage test | J1 |
| 5.2 | Maximize the share of CLI failure paths (non-`EXIT.OK` codes, and the two `{ok:false}` 200-body routes) that surface as an MCP tool result with `isError:true` | 100% across the exit-code/verdict matrix enumerated in §13 | baseline 0→100% (0 today — no MCP tools exist to translate anything) | query: a parametrized test asserting each `EXIT` code / verdict path maps to `isError:true` | J1 · J2 |
| 5.3 | Maximize the share of the core build loop (start a run → tail it to a terminal verdict) completable via MCP tools alone, with no shell fallback | 100% (loop completes in-tools) | aspirational (new capability, no prior baseline) | query: the same automated test suite as 5.2/5.4 runs a scripted `saki_run_start` → `saki_run_tail` integration case end-to-end | J1 |
| 5.4 | Minimize translation-layer errors where a command that FAILED (per its exit code, a thrown `CliError`, or a `{ok:false}` body) is reported to the MCP caller as `isError:false` | 0 across the exit-code/verdict acceptance matrix — guards 5.1, 5.2, 5.3 | aspirational (new invariant; the failure mode cannot exist before the surface does) | query: the same exit-code/verdict-matrix test as 5.2 flags any success-on-failure case | guards 5.1, 5.2, 5.3 |

## 6. Appetite & Kill Criteria

**Appetite:** medium — a few days (4 slices, at the medium-band cap).

**Kill Criteria:**
1. If, once slice 3 ships, the exit-code/verdict acceptance matrix (§13) cannot be driven to 100%
   (5.2/5.4) within this appetite — i.e., some CLI failure mode cannot be faithfully represented as an
   MCP `isError` result without a second, forked notion of "failed" — stop and re-scope rather than
   ship a tool surface that silently drops failure fidelity for the sake of coverage (5.1).
2. **Demand checkpoint (directly answers the §2 spike's caution against building the full surface on
   an unvalidated bet):** if, two weeks after Slice 1 ships, `saki mcp` has not been invoked from a real
   MCP client across at least 3 separate sessions (not one manual smoke-test call — a single invocation
   proves the mechanism works, not that it's wanted), pause before starting Slice 2 and re-open the
   DISCOVERY-RISK bet rather than continuing to build out the remaining slices on faith.

## 7. Solution Shape

Add `saki mcp` as a new top-level CLI command (alongside `status`/`doctor` in `src/index.ts`'s command
table) that starts a stdio MCP server. **Each capability gets its own MCP tool with its own typed
schema** (not one generic `saki_exec`-style wrapper taking a free-form args bag) — a single generic tool
would just relocate the untyped-parsing problem J1 exists to remove back onto the caller. Each MCP
tool's handler calls the exact `cmd*` function the CLI
dispatcher already calls (e.g. `cmdDoctor`, `src/commands/doctor.ts:18`) with `ctx.json = true` and a
`write` that captures the single JSON line instead of printing it. **The CLI's failure signal is not a
single return value** — `ctx.client.get`/`post` throw a `CliError` on transport failure (`UNREACHABLE`; `src/client.ts:125,157`)
and, separately, on an HTTP 401/403 auth rejection (`AUTH_REQUIRED`; `src/client.ts:183-185`, via
`codeForStatus`) — a distinct failure class from transport loss, not the same thing. Command bodies also
call `fail()`, which throws `CliError` too (`src/exit.ts:35`; e.g. `src/commands/run.ts:134,170,173,207`,
`src/commands/runs.ts:37,57`)
— today caught exactly once, in `main()`'s try/catch (`src/index.ts:351-360`). So each MCP tool handler
wraps its `cmd*` call in its own try/catch: a normal `ExitCode` return maps directly; a caught
`CliError` maps by its carried `.code`. Either path funnels through one shared helper —
`EXIT.OK` → `{isError: false, content: [...json]}`; anything else (returned or thrown) →
`{isError: true, content: [...json, exit code, hint]}`. Uses the official `@modelcontextprotocol/sdk`
(TypeScript) for the stdio transport and tool registration.

### Alternatives considered / Decision

- **Chosen: thin in-process MCP wrapper over the existing `src/commands/*.ts` functions.** Reuses 100%
  of existing command logic, adds zero new backend routes, and keeps the exit-code contract defined in
  exactly one place — a behavior change to a command updates both the CLI and MCP surfaces from the same
  function, so the two surfaces cannot drift apart.
- **Rejected: a Go-native `/mcp` endpoint in `backend/`, bypassing the Node CLI.** Would duplicate the
  exit-code-to-result translation logic in a second language — the backend today speaks HTTP status /
  `{ok}` bodies, not the CLI's `ExitCode` contract, so this option re-derives the mapping instead of
  reusing it. Directly the "forking the exit-code contract" the item's Goal explicitly rules out.
- **Rejected: a standalone MCP server package that spawns `saki` as a subprocess and parses its stdout.**
  Reintroduces the exact shell/text-parsing friction MCP is meant to remove, and loses the direct,
  typed exit-code contract in favor of parsing output a second time.

## 8. Vertical Slices

**v1 scope is 13 tools, not the full CLI command set.** The candidate pool was the CLI's 18 total journey
commands; `saki_artifacts` was excluded up front as blocked on I2 (§11) — not part of this cut — leaving
17 real candidates, down from which 13 shipped and 4 were deferred (§12). The §2 spike found real
2026-era evidence that loading many MCP tool schemas up front has a real context cost for agents, and
the demand for this surface at all is `assumed`; cutting to the highest-value 13 tools (the read/status
loop + the full run lifecycle + branch/MR) is the direct response to both signals, not just a "keep it
lean" aspiration.

**Slice 1 — MCP server skeleton + `saki_status`** (walking skeleton). Tools: `saki_status`.
Serves: J1 · 5.1 · 5.4

**Slice 2 — Read-only journey tools.** Tools: `saki_doctor`, `saki_roadmap_list`, `saki_prd_show`,
`saki_runs`. Serves: J1 · 5.1 · 5.2
Assumes: automated test fixtures for these ACs' `Given` clauses (a roadmap item, a PRD) are seeded via
the real CLI/API commands (`saki roadmap add`, the `prd` skill), never a direct DB/file write — the
seed path must exercise the same validation/side-effects a real caller would trigger.

**Slice 3 — Run lifecycle tools.** Tools: `saki_run_start`, `saki_run_tail`, `saki_run_stop` — the
core build loop. Serves: J1 · J2 · 5.2 · 5.3 · 5.4
Assumes: `saki_run_tail`'s handler holds the MCP tool call open for the run's duration (streaming the
same SSE `saki run tail` consumes) rather than returning immediately — a materially different call
shape from slices 1-2's fast request/response tools. Two behaviors this implies are stated but shipped
**untested** this round (§9's ≤5-per-slice cap left no room; tracked as a scoped-out risk with a trigger
in §12, not silently assumed-safe):
- a second tool call (e.g. `saki_status`) arriving while `saki_run_tail` is in-flight must still be
  served — the shared `ctx`/client must not serialize every other tool behind the open call;
- if the MCP client disconnects or cancels mid-`saki_run_tail`, the underlying run keeps executing
  (it is not owned by the MCP call), and a fresh `saki_run_tail`/`saki_runs` call afterward reports its
  real state — the tool result stream ending early must never be read as the run itself stopping.

**Slice 4 — Repo/git + PRD lock tools.** Tools: `saki_branch`, `saki_branch_list`, `saki_branch_switch`,
`saki_mr_create`, `saki_prd_lock` — closes out the branch/MR + PRD-lock loop. Serves: J1 · 5.1 · 5.2 · 5.4

## 9. Acceptance Criteria per Slice

**Slice 1**
- 1.1 [auto] Given the Go backend is reachable, when an MCP client calls the `saki_status` tool over
  `saki mcp`'s stdio transport, then the tool result is `isError:false` and its content matches the
  same JSON body `saki status --json` prints for the identical repo state. → 5.1
- 1.2 [auto] Given the Go backend is unreachable, when an MCP client calls `saki_status`, then the tool
  result is `isError:true` and its content carries the same `UNREACHABLE` diagnosis the CLI would exit
  with (via the thrown `CliError` path in §7) — not a silently-successful result and not an uncaught
  exception that kills the tool call. → 5.2 · 5.4
- 1.3 [auto] Given `saki mcp` has started, when its registered tool list (`tools/list`) is queried,
  then it contains exactly `saki_status` and no other tool. → 5.1
- 1.4 [auto] Given a full session — `tools/list` followed by one `saki_status` call — completes, when
  every byte written to the process's stdout is inspected, then all of it parses as well-formed MCP
  JSON-RPC frames (no stray non-protocol output from the SDK, a transitive dependency, or a stray
  `console.log`). → 5.4
- 1.5 [auto] Given the stdio MCP transport itself fails to initialize (e.g. the SDK's handshake setup
  throws), when `saki mcp` is invoked, then the process exits non-zero with a clear message on stderr,
  rather than hanging or crashing without diagnosis. → 5.4

**Slice 2**
- 2.1 [auto] Given the backend has ≥1 roadmap item, when an MCP client calls `saki_roadmap_list`, then
  the returned content matches `saki roadmap list --json`'s body for the same repo. → 5.1
- 2.2 [auto] Given an engine profile is missing or misconfigured, when an MCP client calls
  `saki_doctor`, then the tool result is `isError:true` with the same per-engine `fix` hints `saki
  doctor` prints on a failed engine — not a bare "ok" result. → 5.2 · 5.4
- 2.3 [auto] Given a PRD exists for this repo, when an MCP client calls `saki_prd_show` directly (no
  prior tool call in this test), then its content matches `saki prd show --json`'s body. → 5.1
- 2.4 [auto] Given the backend has ≥1 run recorded, when an MCP client calls `saki_runs` directly (no
  prior tool call in this test), then its content matches `saki runs --json`'s body. → 5.1
- 2.5 [auto] Given the roadmap has zero items, when an MCP client calls `saki_roadmap_list`, then the
  tool result is `isError:false` with an empty list — not an error. → 5.1

**Slice 3**
- 3.1 [auto] Given the backend accepts a new run, when an MCP client calls `saki_run_start` with a
  valid verb (one of `RUN_VERBS`, `src/commands/run.ts:13`) and target, then the tool result is
  `isError:false` and its content carries the same `runId` `saki run <verb> --json` would print. → 5.1 · 5.3
- 3.2 [auto] Given an MCP client calls `saki_run_start` with a verb not in `RUN_VERBS`, then the tool
  result is `isError:true` and carries the same validation error the CLI emits for an unknown verb. → 5.2 · 5.4
- 3.3 [auto] Given a started run reaches a terminal FAILED verdict, when an MCP client calls
  `saki_run_tail` with that run's id, then the tool result is `isError:true` and its content carries
  the run's failure verdict — not `isError:false` with the failure buried in a field. → 5.2 · 5.4
- 3.4 [auto] Given a started run reaches a terminal SUCCESS verdict, when an MCP client calls
  `saki_run_start` then `saki_run_tail`, then the tool result is `isError:false` and the loop completes
  with no shell command invoked by the test harness. → 5.3
- 3.5 [auto] Given a running run, when an MCP client calls `saki_run_stop` with its id, then the result
  matches `saki run stop --json`'s body and a subsequent `saki_runs` call shows it stopped. → 5.1

**Slice 4**
- 4.1 [auto] Given a repo on a known branch, when an MCP client calls `saki_branch`, then its content
  matches `saki branch --json`'s body. → 5.1
- 4.2 [auto] Given `switch-branch` or `create-mr` reports failure inside its HTTP 200 body
  (`{ok:false, error}`, `src/commands/repo.ts:57` — the exact motivating case from §2), when an MCP
  client calls `saki_branch_switch` or `saki_mr_create` for that operation, then the tool result is
  `isError:true` carrying the same `REMOTE_FAILED` diagnosis the CLI produces — not the false-success
  this PRD exists to prevent. → 5.2 · 5.4
- 4.3 [auto] Given a repo with ≥2 local branches, when an MCP client calls `saki_branch_list` then
  `saki_branch_switch` to a real branch, then each result matches its CLI `--json` counterpart body. → 5.1
- 4.4 [auto] Given a repo with no open MR for the current branch, when an MCP client calls
  `saki_mr_create`, then its content matches `saki mr create --json`'s body. → 5.1
- 4.5 [auto] Given a PRD exists but is not yet locked, when an MCP client calls `saki_prd_lock` then
  `saki_prd_show`, then the second call's content reflects the lock, matching `saki prd show --json`
  after `saki prd lock`. → 5.1

## 10. Business Rules & Invariants

1. 🔒 INVARIANT — An MCP tool result MUST reflect the wrapped command's true outcome — a returned
   `ExitCode !== EXIT.OK` **or a thrown `CliError`** (§7; both paths exist in the real CLI) — including
   the two routes that report failure inside an HTTP 200 body, which the wrapped `cmd*` function already
   normalizes to a thrown/returned non-zero exit — maps to `isError:true`. Tested by 1.2 · 1.5 (transport
   + transport-init failure paths, Slice 1), 2.2 (engine-failure path, Slice 2), 3.2 · 3.3 (invalid-input
   + run-failure paths, Slice 3), 4.2 (the HTTP-200-body-false-success case, Slice 4) — six failure/edge
   criteria across all four slices, not merely a happy-path spot check.

## 11. Non-Goals

- ✗ No `saki_workitems`, `saki_roadmap_add`, `saki_proto`, or `saki_screenshots` MCP tools in v1.
  Deferred (§12) as part of the scope cut the §2 spike motivated — not because they're hard, but
  because the 13 kept tools already cover the primary job (J1) and the spike found real reason not to
  maximize tool count by default.
- ✗ No `saki_artifacts` MCP tool. `saki artifacts` depends on a companion orchestrator not in this repo
  (I2, `Blocked`); revisit once I2 unblocks.
- ✗ No daemon lifecycle or auto-start of the Go backend from `saki mcp`. That is F1; `saki mcp` requires
  the backend already running, the same precondition every other `saki` command has today. **This is a
  real, accepted limitation, not just a boundary**: an MCP-only agent whose backend is down (e.g. `saki_status`
  returns `UNREACHABLE`, criterion 1.2) has no MCP-native way to start it — it still needs one shell/manual
  step until F1 ships. J1's "without shelling out" holds for the steady-state journey, not for backend
  recovery.
- ✗ No remote/HTTP MCP transport (SSE or WebSocket). Stdio only for v1, matching the backend's
  loopback-only, local-single-operator trust model (`docs/project-context.md` § Invariants).
- ✗ No MCP resources or prompts primitives — tools only. Every capability in this PRD is a tool call;
  adding resources/prompts is new surface, not covered by any slice here.
- ✗ No new Go backend HTTP routes. The MCP layer is a pure TypeScript translation layer over the
  existing CLI command functions, adding zero backend surface — the same "thin client, no routes of its
  own" posture the CLI itself already has (`docs/cli-reference.md`).
- ✗ No auto-discovery of new commands. Every tool is hand-registered from the 13-tool list in §16 — a
  new CLI command does NOT automatically get an MCP tool; adding one is a deliberate follow-up PRD/slice.

## 12. Rabbit Holes & Open Questions

- **Scoped-out risk, shipped untested (Slice 3):** concurrent tool calls while `saki_run_tail` blocks,
  and client-disconnect/cancel mid-`run_tail`, are both stated as required behavior (§8 Slice 3 Assumes)
  but have no acceptance criterion this round — the ≤5-per-slice cap left no room once the core happy/
  failure paths were covered. Trigger to promote either to a real `[auto]` criterion: the first real
  report (from the maintainer's own dogfooding, per the §6 demand checkpoint) of a stuck/duplicate tool
  call during a run, or of a run's state going stale after a client disconnect.
- **Residual gap, shipped untested (Slice 2/4):** `saki_runs` and `saki_branch_list`'s empty-state
  behavior, and `saki_run_stop`/`saki_prd_lock`'s failure paths (stopping an already-stopped run,
  locking an already-locked PRD), are not covered this round — the same per-slice AC cap. Trigger: add
  when `/saki-builder:rplan` or `/saki-builder:qa` surfaces a concrete bug on one of these paths.
- **Deferred tools (not built in this PRD) and their triggers**, following this repo's own F2→F4/F5
  phase-chain precedent (`tasks/roadmap.md`):
  - `saki_workitems` — trigger: an MCP-driven agent needs a cross-cutting "what's open" view that
    `saki_roadmap_list` + `saki_prd_show` don't already answer.
  - `saki_roadmap_add` — trigger: a real MCP workflow needs to create roadmap items without shelling
    out, observed at least once.
  - `saki_proto` — trigger: same as above, AND its `--open` flag's GUI side-effect (`src/commands/proto.ts:14,48`
    spawns a detached `open`/`start`/`xdg-open` process) has a resolved decision on whether a headless
    MCP caller can trigger it at all.
  - `saki_screenshots` — trigger: an MCP-driven QA workflow needs it, observed at least once.
- Rabbit hole: a generic/reflective "auto-register every `CommandDef` as a tool" mechanism. Resist —
  hand-map each of the 13 tools explicitly across the 4 slices so each tool's JSON-schema input stays
  hand-reviewed rather than derived from `FlagSpec` by magic (§11).
- Rabbit hole: bidirectional streaming / progress notifications for `saki_run_tail` (MCP supports
  progress notifications on long-running calls). Out of scope for v1 — the tool blocks until the run
  reaches a terminal state and returns once, mirroring `run tail`'s own blocking CLI behavior;
  incremental progress notifications are a possible future enhancement.
- Open question (non-blocking, noted for `/saki-builder:rplan`): `saki_run_tail`'s MCP call can stay
  open for as long as a build takes. Lean: no special timeout — mirror the CLI's own untimed blocking
  behavior, since MCP clients such as Claude Code already tolerate long-running tool calls (unverified —
  route to `/saki-builder:rplan` to confirm against the specific client(s) targeted).

## 13. Technical Constraints

- Adds `@modelcontextprotocol/sdk` as the CLI's first production dependency — `package.json` currently
  declares zero `dependencies`, only `devDependencies`.
- Requires the Go backend already running (`SAKI_BACKEND_URL`) — `saki mcp` does not start it; identical
  precondition to every other `saki` command today (§11).
- Stdio transport only — no HTTP/SSE MCP listener in v1 (§11).
- **Exit-code/verdict matrix** that 5.2/5.4 and the §6 Kill Criterion are measured against (the closed
  checklist a test suite enumerates): `EXIT.OK`, `EXIT.ERROR`, `EXIT.USAGE`, `EXIT.UNREACHABLE`,
  `EXIT.NOT_FOUND`, `EXIT.REMOTE_FAILED`, `EXIT.AUTH_REQUIRED` (`src/exit.ts:8`), plus the two routes
  that report failure inside an HTTP 200 body (`switch-branch`, `create-mr`) that the CLI already
  normalizes to `REMOTE_FAILED`.
- List-returning tools (`saki_roadmap_list`, `saki_runs`, `saki_branch_list`) return the CLI's `--json`
  body unmodified inside the MCP tool-result envelope; no size ceiling or truncation for large-N repos
  is implemented in v1 — out of scope until a real large-N repo surfaces an MCP client response-size
  problem.
- `saki_run_start`'s single MCP tool schema covers all 10 `RUN_VERBS` (`src/commands/run.ts:13-23`:
  build/pickup/proto/rplan/prd-review/rplan-review/approved/qa/reviewer/wrap) via one enum-typed `verb`
  parameter — hand-reviewed as one schema, not ten, because the CLI itself already dispatches all ten
  through one unified `startRun` helper (`src/index.ts`); the schema does not hide ten separately-varying
  argument shapes.

## 14. Dependencies

None beyond the new npm dependency noted in §13. Not blocked on I1 (npm publish) or F1 (daemon
lifecycle) — `saki mcp` assumes the backend is already up, the same assumption the CLI itself makes.

## 16. Technical Contract (thin)

**Endpoints (API):**

| Method + path — purpose | Reuse / Change / New | Evidence or note | Serves |
|---|---|---|---|
| `saki mcp` — CLI command starting the stdio MCP server | NEW | new entry in the command table at `src/index.ts` (pattern: `status`/`doctor` entries) | Slice 1 · 5.1 |
| MCP tool `saki_status` — wraps `cmdStatus` | REUSE (wraps existing fn) | `src/commands/status.ts:53` | Slice 1 · 5.1 |
| MCP tool `saki_doctor` — wraps `cmdDoctor` | REUSE (wraps existing fn) | `src/commands/doctor.ts:18` | Slice 2 · 5.2 |
| MCP tool `saki_roadmap_list` — wraps `cmdRoadmapList` | REUSE (wraps existing fn) | `src/commands/roadmap.ts:43` | Slice 2 · 5.1 |
| MCP tool `saki_prd_show` — wraps `cmdPrdShow` | REUSE (wraps existing fn) | `src/commands/prd.ts:45` | Slice 2 · 5.1 |
| MCP tool `saki_prd_lock` — wraps `cmdPrdLock` | REUSE (wraps existing fn) | `src/commands/prd.ts:53` | Slice 4 · 5.1 |
| MCP tool `saki_runs` — wraps `cmdRuns` | REUSE (wraps existing fn) | `src/commands/runs.ts:21` | Slice 2 · 5.1 |
| MCP tool `saki_run_start` — wraps `cmdRunStart` | REUSE (wraps existing fn) | `src/commands/run.ts:163` | Slice 3 · 5.1 · 5.3 |
| MCP tool `saki_run_tail` — wraps `cmdRunTail` | REUSE (wraps existing fn) | `src/commands/runs.ts:56` | Slice 3 · 5.3 · 5.4 |
| MCP tool `saki_run_stop` — wraps `cmdRunStop` | REUSE (wraps existing fn) | `src/commands/runs.ts:36` | Slice 3 · 5.1 |
| MCP tool `saki_branch` — wraps `cmdBranch` | REUSE (wraps existing fn) | `src/commands/repo.ts:28` | Slice 4 · 5.1 |
| MCP tool `saki_branch_list` — wraps `cmdBranchList` | REUSE (wraps existing fn) | `src/commands/repo.ts:40` | Slice 4 · 5.1 |
| MCP tool `saki_branch_switch` — wraps `cmdBranchSwitch` | REUSE (wraps existing fn) | `src/commands/repo.ts:59` | Slice 4 · 5.1 · 5.2 |
| MCP tool `saki_mr_create` — wraps `cmdMrCreate` | REUSE (wraps existing fn) | `src/commands/repo.ts:82` | Slice 4 · 5.1 · 5.2 |

**Architecture decision (one, load-bearing):**

- Wrap each existing `cmd*` function (unchanged) inside a new MCP tool handler that catches BOTH a
  returned `ExitCode` and a thrown `CliError` (§7 — the CLI's real failure signal is not a single return
  value: `ctx.client.get`/`post` throw on transport failure, and `fail()` throws too, `src/exit.ts:35`),
  translating either path into an MCP tool result at exactly one seam (a single `exitCodeToToolResult`
  helper, NEW, taking either an `ExitCode` or a caught `CliError`). Serves 5.2 · 5.4. **Alternative
  rejected:** a Go-native `/mcp` endpoint in `backend/` — would re-derive the exit-code mapping in a
  second language instead of reusing the one place it is already defined, the exact "forked contract"
  the item's Goal rules out (see §7 Decision Log).
