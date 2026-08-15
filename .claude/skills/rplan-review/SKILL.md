---
name: rplan-review
description: Adversarial plan review for saki-cli — structural completeness scan, then parallel domain expert agents tuned to this repo (Go hexagonal backend · TS CLI contract · the run/retry engine · loopback security), then synthesis. Blocks on missing sections. Run after /saki-builder:rplan before /saki-builder:approved.
user-invocable: true
---

# Plan Review — saki-cli

Project override of the global `rplan-review`. **Phases 0/1/3/4 are unchanged from the global
skill** — same structural gate, same synthesis ledger, same verdict rules. What this file replaces
is **Phase 2: the expert roster and their prompts**, tuned to this repo's stack and failure modes.

**Priority order: ① surface implementation reality → ② keep every finding grounded → ③ prescribe,
don't lecture.** Uncited findings are discarded. Every blocker is verified against the actual code
before it enters the ledger — subagents in this repo have flagged correct APIs as bugs.

---

## Phase 0/1 — unchanged

Load the plan (use the caller's path if one was passed), read the attempt counter, run the
structural completeness scan. Phase 1 is a hard gate; failure stops the review.

**One project addition to the Phase 1 scan table:**

| # | Required Section | Rule |
|---|---|---|
| 9 | **Invariant Impact** | A plan whose Steps touch `backend/{usecase,infra}/` spawn·journal·rehydrate·reaper, `backend/cmd/server/`, `backend/adapter/originguard.go`, or `src/exit.ts` MUST state Inv-1 / Inv-2 / loopback / exit-code impact explicitly. Absent ⇒ ❌ MISSING. "No impact" is a complete answer; silence is not. |

---

## Phase 2: Parallel Domain Expert Review — PROJECT ROSTER

Run only if Phase 1 passed. Launch every applicable agent **in parallel**.

### Domain detection

| Expert | Launch if a step touches... |
|---|---|
| Go Backend | any `backend/**/*.go` |
| CLI Contract | any `src/**/*.ts`, or any new/changed command, flag, or exit code |
| Run Engine | `spawn`, `journal`, `rehydrate`, `reaper`, `stream`, `stop`, retry/backoff/budget, or `run.go` |
| Security | the bind/`Host` guard, env scrubbing, engine argv, `--dangerously-*` flags, or anything reading a token |
| QA | **always** |
| Product | **always** |

**No Frontend and no Database expert exist for this repo** — there is no UI and no datastore. State
persists in NDJSON journal files on disk; a plan proposing a DB is proposing a new deployable and
should be routed to Architecture review first.

### Shared contract — prepend to EVERY expert prompt

```
Priority order: ① implementation reality first · ② grounded · ③ prescribe.
- LEAD with implementation reality: for each step you own, name (a) the failure/edge paths the
  happy path leaves untested, and (b) the build work the step IMPLIES but the Steps table OMITS.
- CITE EVERY FINDING: quote the exact step # / section text you object to. Uncited = DISCARDED.
- PRESCRIBE: each blocker names the exact plan edit that fixes it (step + file + function/criterion).
- DEFAULT TO BLOCKER for a state-changing or 🔒 step whose failure path is untested.
- Do NOT propose a numeric confidence adjustment — Phase 3 owns the ledger.
- Read docs/project-context.md before judging. A step that crosses a documented non-goal is a
  BLOCKER even if every other detail is correct.
```

### Go Backend Expert

```
You are a senior Go engineer reviewing a plan for a hexagonal (domain/usecase/adapter/infra) backend.

Identify:
1. LAYERING BREAKS — the top blocker class here. `backend/domain/` must have ZERO outbound deps:
   a step putting net/http, os/exec, encoding/json-over-the-wire, or an `infra` import into
   `domain` is a blocker. `adapter` reaching `infra` without a `usecase` port is a blocker.
2. Missing port declaration — a new external dependency needs an interface in usecase/ports.go,
   run_ports.go or content_ports.go, plus a fake for tests. A step naming a concrete infra type in
   a usecase signature is a blocker.
3. context.Context not threaded through a call that can block or spawn.
4. Unchecked error returns; a resource opened without `defer` close; a goroutine with no
   shutdown path (this backend spawns and streams — leaked goroutines are real here).
5. Any step saying "add endpoint" without naming the HTTP method, path, handler func, AND the
   mux.HandleFunc registration in backend/adapter/http.go.
6. Concurrency on shared state (the run store, the journal) without a stated locking strategy.

Plan: [paste full plan text]

Output:
GO BACKEND REVIEW
Blockers: / Warnings:
```

### CLI Contract Expert

```
You are a senior TypeScript engineer reviewing a plan for a headless CLI whose EXIT CODE IS ITS API.

Identify:
1. EXIT-CODE CONTRACT VIOLATIONS — the top blocker class. src/exit.ts codes (0 OK, 1 ERROR,
   2 USAGE, 3 UNREACHABLE, 4 NOT_FOUND, 5 REMOTE_FAILED, 6 AUTH_REQUIRED) must never be
   renumbered or repurposed. Any new failure mode must map to an EXISTING code or justify a new
   one. A step adding a command without stating its exit codes is a blocker.
2. A route that can fail inside an HTTP 200 body mapped to OK instead of REMOTE_FAILED. Two
   upstream routes already do this — assume any new forwarded route might.
3. Missing `--json` shape. Every command takes --json; a plan that adds one without specifying
   the JSON output is incomplete.
4. A new command absent from docs/cli-reference.md — it is unshipped. Blocker.
5. Cross-command imports (src/commands/* must never import each other) or shared logic that
   belongs in src/{args,client,ctx,exit,output,sse,resolve,routes}.ts.
6. ESM/NodeNext breakage: a relative import missing its .js extension.

Plan: [paste full plan text]

Output:
CLI CONTRACT REVIEW
Blockers: / Warnings:
```

### Run Engine Expert

```
You are reviewing a plan touching a process-spawning, journalling, auto-retrying supervisor.
This is the crown jewel of the codebase and its safety mechanisms are NOT deferrable.

Identify:
1. THE FOUR SAFETY MECHANISMS. Any change to the retry/spawn loop must keep ALL of:
   (a) exponential backoff before each retry
   (b) a circuit breaker tied to a PROGRESS signal (park after K attempts with no verified progress)
   (c) a hard budget (wall-clock or attempt count) that even "made progress" cannot reset
   (d) a dedupe/idempotency gate so the retry path and any manual path cannot double-fire
   A plan deferring any of these to "a later slice" is MIS-SLICED. Blocker, and say so plainly.
2. Inv-1 — Go journals must stay in <runsDir>/go (backend/infra/journal.go:58). A flat write lets
   apps/server's readdir adopt the run.
3. Inv-2 — a restart must never lose or mis-report an in-flight run. ANY change to spawn, journal,
   rehydrate or the reaper needs a RESTART-PATH test in the plan. Absent = blocker.
4. Double-spawn races: an in-memory timer, the poll sweep, and a manual path all acting on one
   item. Name the specific interleaving.
5. Classification holes — a false-terminal (real failure read as done) or false-retry (terminal
   error retried forever). Note that the model's own narration shares the output stream with the
   tool's sentinels: any new pattern match MUST be anchored to line-start.
6. Durable state assumed to survive a restart that is actually only in memory.

Plan: [paste full plan text]

Output:
RUN ENGINE REVIEW
Blockers: / Warnings:
```

### Security Expert

```
You are reviewing a plan for a service that spawns coding agents with their SANDBOXING DISABLED.
That fact sets the threat model: any reachability the backend gains is arbitrary code execution.

Identify:
1. LOOPBACK RELAXATION — the top blocker class. The backend binds 127.0.0.1
   (backend/cmd/server/main.go:141) AND rejects non-loopback Host headers
   (backend/adapter/originguard.go:48). Both are load-bearing. A step adding a HOST/--bind flag,
   a 0.0.0.0 bind, a CORS relaxation, or an exemption to the Host check is a BLOCKER — it is a
   documented non-goal, not a trade-off to weigh.
2. Env leakage across engines — a non-claude spawn must stay scrubbed of the other engines'
   namespaces (backend/infra/spawner.go:263). A new runtime is one case in scrubProfileEnv +
   engineProfileEnv, never a new branch.
3. A secret, token, or credential placed in argv (visible in ps), in a log line, or in a journal
   entry. The journal is written to disk unencrypted.
4. Unvalidated external input reaching exec.Command, a shell string, or a file path
   (path traversal into the journal dir or the repo).
5. A spawn whose provisioning check is bypassed — usecase/spawn.go:27 refuses an unprovisioned
   engine on purpose; removing that turns a silent no-op run into a parked build.

Plan: [paste full plan text]

Output:
SECURITY REVIEW
Blockers: / Warnings:
```

### QA Expert (always)

```
You are a QA lead reviewing a plan's testability.

Identify:
1. FAKE-BINARY PROOF — this repo's standing rule (e2e/codex-spawn.spec.ts:10-17): a fake-binary
   test CANNOT prove an engine invocation. The opencode command form shipped green against fakes
   while every real run no-opped. Any criterion asserting engine behaviour that a plan proposes to
   verify with a fake/stub binary is a BLOCKER — it must use the real binary or prove nothing.
2. Criteria that are not machine-executable. Rewrite each as Given/When/Then with the exact
   command and expected outcome. For this CLI, an exit-code criterion is verified by running the
   command and reading $? — never by matching stdout.
3. Missing restart-path test for anything touching spawn/journal/resume (see Inv-2).
4. New src/ or backend/ code with no test — 80% coverage floor.
5. A criterion placed in e2e/ that vitest would try to collect (vitest.config.ts excludes e2e/ on
   purpose), or a unit test placed outside src/**/*.test.ts where vitest will never find it.
6. Dual-stack gaps: a plan changing both sides but testing only one.

Plan: [paste full plan text]

Output:
QA REVIEW
Blockers: / Warnings:
```

### Product Expert (always)

```
You are a senior PM reviewing plan scope and coverage for a developer tool whose users are AGENTS.

Identify:
1. Consumer coverage — the consumers here are (a) a human at a terminal, (b) an agent branching
   on exit codes and --json, (c) /saki-builder:build driving it headless. A plan serving only the
   human path is incomplete. Name the missing one.
2. Scope vs appetite — steps far beyond the stated goal, or a step that is really a separate item.
3. Backward compatibility for anything already in docs/cli-reference.md — that document is the
   published contract.
4. A change that belongs in the saki-builder (workflow) repo rather than this runtime repo.
5. Acceptance criteria that assert implementation rather than observable outcome.

Plan: [paste full plan text]

Output:
PRODUCT REVIEW
Blockers: / Warnings:
```

---

## Phase 3/4 — unchanged

Collect all results, print each in full, then apply the global synthesis rules verbatim:

1. **Discard uncited findings.**
2. **VERIFY every blocker against the actual code** before it enters the ledger. Read the cited
   file. A blocker that does not survive reading the code is dropped and noted as dropped.
3. Dedupe across experts; classify each surviving finding Blocking or Advisory.
4. A blocker from any agent ⇒ the plan is NOT ready. "I'll handle it during implementation" =
   BLOCKER.
5. Annotate the plan file with resolved findings under "Annotation Space".

Verdict rules, the Blocking Set gate, and the Phase 1 self-route bound are the global skill's —
this override does not change them.
