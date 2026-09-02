---
name: saki-cli
description: "Operate the saki-cli headless build orchestrator from an agent: preflight engines, launch durable PRD or Plan workflows, follow or reattach safely, handle parked decisions, and verify completion."
---

# saki-cli operation

Use this skill when an agent must drive a repository through `saki` rather than implement the
repository directly. `saki-cli` is the runtime supervisor; `/saki-builder:*` is the workflow
installed in the selected engine profile. The backend owns workflow state and journals it to disk.

## Operating contract

- Treat the exit code as authoritative. Parse `--json` for IDs and state; never infer success from
  human output or an HTTP 200 alone.
- A build is successful only when the workflow is durably `done` with verified completion evidence.
  A child engine turn exiting zero is not enough.
- Use the existing `--follow` flag for a synchronous agent run. No extra Hermes-specific parameter
  is required, and do not invent one.
- Retrying the same target is safe: the backend deduplicates the workflow lane. Do not start a
  second command with a different ad-hoc id merely because the first command is still running.
- Keep the backend loopback-only. Never add a bind-address, CORS, or remote-access override.
- Runs can execute with engine sandboxing disabled. Operate only on a repository and machine the
  user has authorized; never run as a privileged user.

## Preflight

Run from the target repository. Confirm the target exists and identify its track before dispatch:

```bash
saki status --json
saki doctor --json
saki roadmap list --json
```

For an engine-specific run, use a provisioned profile and verify the selected engine resolves the
`saki-builder` commands. Prefer an explicit profile when the agent has isolated configuration:

```bash
saki doctor --engine codex --profile /absolute/profile --json
```

If preflight exits `3`, repair backend reachability. If an engine is unprovisioned or cannot
resolve `saki-builder`, stop and report the exact provisioning command; do not claim a build ran.

## Main workflow

For a PRD-track roadmap item (`E<n>` or `F<n>`), dispatch one durable workflow:

```bash
saki build F7 --engine codex --follow --json
```

The backend drives the required sequence: resolve target, pickup or adopt the PRD, proto/lock,
build, and verified completion. For a Plan-track item (`I<n>` or `B<n>`), the same command drives
the plan/review/approved/QA/reviewer/wrap sequence. A PRD path may be used instead of its roadmap
id when it stays inside the target repository:

```bash
saki build tasks/prd-feature.md --follow --json
```

For long-running work where the agent must detach, start without `--follow`, save the returned
`workflowId`, and reattach using the workflow follow endpoint/CLI supported by the installed
version. The preferred hands-off path is still one `saki build <target> --follow --json` call.

## Continuation and failures

Read the returned JSON fields `workflowId`, `status`, `phase`, `deduped`, `reason`, and
`completionEvidence`.

- `done`: verify `completionEvidence` exists, then report success.
- `running`: wait or re-run the same target; dedupe means this does not create another child.
- `parked` or `awaiting-decision`: inspect the reason. Resolve only the decision the user has
  authorized, then continue:

  ```bash
  saki run continue <workflowId> --option <validated-option> --json
  ```

- `failed` or `stopped`: read the durable reason and child run output before retrying. Retry the
  same target only after addressing the reason; do not blindly hammer a usage limit, auth failure,
  missing skill, or invalid target.
- If work must be cancelled, stop the workflow/run explicitly and record its id and reason:

  ```bash
  saki run stop <workflowId>
  ```

`--follow` must return non-zero for parked, awaiting-decision, failed, stopped, dropped, or
unreachable workflows. Only exit `0` means the verified workflow completed.

## Agent handoff checklist

Before saying “shipped”, capture:

1. the exact target and engine/profile;
2. the workflow id and final JSON status;
3. completion evidence and checked paths/artifacts;
4. the exit code;
5. any explicit engine limitation or skipped real-binary proof.

For code changes in this repository, also run the project gates before handoff:

```bash
npm run typecheck
npm test
npm run backend:test
npm run backend:build
```

Do not publish npm or Homebrew artifacts from this operational skill unless the user separately
authorizes a release and the release checklist has been completed.
