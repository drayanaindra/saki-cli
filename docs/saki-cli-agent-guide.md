# `saki` CLI — Agent Operating Guide

Everything needed to set up, run, and drive **saki studio** from a terminal. Written for an
autonomous agent: branch on **exit codes**, parse `--json`, never scrape human output.

---

## 0. What this is (read once)

`saki` is a **thin HTTP client** over the saki studio orchestrator. It does no work itself — the
studio spawns the agent (`claude -p` by default, or `opencode run` / `codex exec` — see
`--engine` in §4), tracks runs, and performs git operations. The CLI calls the same API the
web UI calls, so the two can never disagree.

**Consequence: the studio must be running.** With no studio, every command except `--help` fails
with exit `3`.

```
saki  ──HTTP──▶  studio orchestrator (:8787)  ──spawns──▶  claude -p (detached)
                                                           └─ or opencode run / codex exec (--engine)
```

**One studio serves every project.** Do not start a studio per repo. A single instance — run out of
the `pipeline-studio` repo, where the server code lives — handles all of them, because every
command sends the target repo's path along with the request:

```
~/Project/alpha   $ saki roadmap list  ─┐
~/Project/beta    $ saki roadmap list  ─┼──▶  the one studio on :8787
~/Project/gamma   $ saki roadmap list  ─┘
```

So: **the studio stays running in `pipeline-studio`; `saki` runs wherever the work is.**

---

## 1. Setup (once per machine)

### 1.1 Build and link the binary

From the repo root:

```bash
npm run build -w @saki/cli    # emits apps/cli/dist — the bin link needs this to exist FIRST
npm install                   # links node_modules/.bin/saki -> apps/cli/dist/index.js
```

Order matters: `npm` skips the bin symlink if `dist/index.js` is absent.

### 1.2 Put it on PATH — use an ABSOLUTE path

The binary lives inside the studio repo's `node_modules`, but you will run it from **other**
projects. So the PATH entry must be absolute:

Generate the line rather than typing it — this prints the exact `export` for **this** machine, with
the placeholder already substituted:

```bash
# run from the pipeline-studio repo root:
echo "export PATH=\"$(git rev-parse --show-toplevel)/node_modules/.bin:\$PATH\"" >> ~/.zshrc
```

It produces something like:

```bash
export PATH="/Users/you/Project/pipeline-studio/node_modules/.bin:$PATH"
```

> ⚠ Do not paste a path containing `/absolute/path/to/...` — that is a placeholder. If the directory
> in your PATH entry does not exist, `saki` silently will not resolve. Check with:
> `test -d "<the path you used>" && echo ok || echo "wrong path"`.

```bash
# ✗ WRONG — $PWD resolves at the moment you run it, so `saki` vanishes
#   the instant you cd into another project
export PATH="$PWD/node_modules/.bin:$PATH"
```

Verify from somewhere else entirely — in an **interactive** shell:

```bash
cd /tmp && zsh -ic 'which saki'     # prints the path → PATH is correct
```

> ⚠ `zsh -lc` (non-interactive login) does **not** source `~/.zshrc`, so it reports "saki not found"
> even when the setup is correct. Use `zsh -ic`, or just open a new terminal tab.
> An already-open shell keeps its old PATH — `source ~/.zshrc` or start a new one.

### 1.3 Enable `DEV_MODE` — REQUIRED

The CLI holds no browser session. `DEV_MODE=1` is what exempts it from the studio's session gate.
**Without it every command except `status` fails with exit `6`.**

In `apps/server/.env`, uncomment:

```
DEV_MODE=1
```

> That file contains real credentials. Edit only this line; never print or commit the file
> (it is gitignored).

### 1.4 Verify

```bash
saki --help     # exit 0
```

### 1.5 Engines — only if you will use `--engine`

**Skip this section entirely if you only ever use claude.** It is the default, and nothing here is
needed for it.

A run executes on one agent runtime. All three resolve the studio's `/saki-builder:*` commands, but
each has to be provisioned to do so:

| `--engine` | Binary | How it resolves a saki command | Provision with |
|---|---|---|---|
| `claude` *(default)* | `claude` | from the message | already true if the studio works |
| `opencode` | `opencode` | via `--command` (its `run` never expands a slash command in the message), skills installed **bare** | `opencode plugin @saketek/saki-builder --global` + `npx @saketek/saki-builder install --global` |
| `codex` | `codex` | from the message, like claude — via the saki-builder plugin's skills | `codex plugin add saki-builder@saketek` |

```bash
codex plugin marketplace add https://gitlab.com/drayanaindra/saki-builder.git
codex plugin add saki-builder@saketek
bash scripts/install-codex-skills.sh    # verifies the above; prints the fix if unprovisioned
```

The plugin carries the skills, agents and hooks. **Do not also symlink them** — that creates a second,
version-skewed copy of every skill. `scripts/install-codex-skills.sh` is a checker; its `--symlink`
fallback exists only for an ephemeral pinned profile where a marketplace plugin is impractical.

**A run whose engine is not provisioned is refused at the spawn**, before anything is launched — exit
`1`, with the fix in the error text:

```console
$ saki build E22 --engine codex
error: engine profile cannot resolve the saki-builder commands: codex profile does not resolve
@saketek/saki-builder: /Users/me/.codex/config.toml registers no enabled saki-builder plugin and
/Users/me/.codex/skills/build/SKILL.md is absent — run `codex plugin add saki-builder@saketek`
(or bash scripts/install-codex-skills.sh to check)
```

That refusal is deliberate and load-bearing. Left to run, an unprovisioned engine **exits 0** — the
model simply answers that it cannot find the command — so the studio would park a build that never
started. The studio therefore proves the install by *reading* the profile, never by inspecting a run's
exit code.

---

## 2. Run the studio

From the repo root:

```bash
./run.sh
```

Boots three processes: web `:5180`, orchestrator `:8787`, Go backend `:8788`. It blocks; run it in
a background shell or a separate terminal. `Ctrl-C` stops all three.

**Then confirm — this is the mandatory first check of any session:**

```bash
saki status
```

Required output before doing anything else. **The default is one backend** — the Go server serves
every journey command, and Express is opt-in via `SAKI_STUDIO_URL`:

```
backend   http://127.0.0.1:8788
reachable yes (pipeline-studio-backend)   <- MUST be "yes" — it serves runs/roadmap/prd/branch/proto
express   not configured (set SAKI_STUDIO_URL to include it)   <- normal; NOT an error
```

With Express configured (the full dev studio) you additionally need `devMode on` and `runs allowed`,
because Express gates those routes:

```
backend   http://127.0.0.1:8788
reachable yes (pipeline-studio-backend)
studio    http://localhost:8787
reachable yes (pipeline-studio-server)
devMode   on              <- MUST be "on"
auth      authenticated
runs      allowed         <- MUST be "allowed"
```

| `saki status` says | Meaning | Action |
|---|---|---|
| exit 3, `express is not answering` | Express (`:8787`) down | run `./run.sh`, wait, retry |
| exit 3, `the go backend is not answering` | Go backend (`:8788`) down | `npm run dev:backend` (or `./run.sh`), retry |
| `devMode off` | `DEV_MODE=1` not set | do §1.3, restart the studio |
| `runs BLOCKED` | `POST /api/run` will 403 | same as above |
| `devMode unknown` | session read failed | studio is up but unhealthy — check its logs |

---

## 2.5 Working on a project — which repo does a command act on?

**Every command targets one repo.** It is chosen in one of two ways:

| Form | Repo acted on | Use when |
|---|---|---|
| `cd ~/Project/alpha && saki roadmap list` | the current directory | interactive |
| `saki roadmap list --cwd ~/Project/alpha` | the path you name | **scripts and agents — prefer this** |

`--cwd` is better for an agent: it does not depend on where the process happens to be standing, so
it cannot silently act on the wrong repo after a `cd` that did not happen.

### Setup checklist for a NEW project

```bash
# 1. studio running? (once per machine — NOT per project)
saki status                      # need: backend reachable (devMode/runs only if express is configured)

# 2. does the target repo have a roadmap?
saki roadmap list --cwd ~/Project/alpha
#    exit 0 -> ready
#    exit 4 -> no tasks/roadmap.md yet; scaffold it in that repo first
#              (run `/saki-builder:roadmap init` there, then re-check)

# 3. work it
saki run build E1 --cwd ~/Project/alpha --follow
```

### What the target repo needs

| Command group | Requires in the target repo |
|---|---|
| `roadmap`, `prd`, `proto`, `workitems` | `tasks/roadmap.md` (and PRDs under `tasks/`) |
| `branch`, `mr create` | a git repo (and a remote, for `mr create`) |
| `run <verb>` | whatever that skill needs — normally a roadmap and/or a PRD |
| `status` | nothing — studio-level, not repo-level |
| `runs` | nothing, **but it is cwd-scoped** — see below |

⚠ **`saki runs` only lists runs for the current repo.** The CLI always sends the cwd, so the same
command returns different results depending on where you run it:

```console
/tmp                    $ saki runs --json | jq length     # 0
~/Project/pipeline-studio $ saki runs --json | jq length   # 4
```

A run started for repo A is invisible from repo B. To see a specific repo's runs, target it:
`saki runs --cwd ~/Project/alpha`. There is currently **no way to list every run across all repos**
from the CLI — use the studio UI for that.

`saki run tail <runId>` and `saki run stop <runId>` are **not** scoped — a run id is global, so
those work from anywhere.

### Common mistakes

- Running `saki` from a **subdirectory** of the target repo. Relative PRD paths resolve against the
  cwd, so `tasks/prd-x.md` from `repo/sub/` becomes `repo/sub/tasks/prd-x.md` → exit `4`.
  **Use a roadmap id, or pass `--cwd <repo-root>`.**
- Assuming a fresh repo is ready. No `tasks/roadmap.md` → exit `4` on every roadmap command.
- Starting a second studio for a second project. Unnecessary; it will also fight for `:8787`.

---

## 3. Exit codes — the machine contract

**Branch on these. Never on stdout.**

| Code | Name | Meaning | What an agent should do |
|---|---|---|---|
| `0` | OK | Succeeded. For `run tail`: the run finished `done` | continue |
| `1` | ERROR | Unexpected failure, **or** a run that ended `error` (incl. stopped) | read stderr; do not retry blindly |
| `2` | USAGE | Bad arguments — unknown command/flag, missing or extra positional | fix the command; retrying identically will fail again |
| `3` | UNREACHABLE | Studio not answering | start the studio, then retry |
| `4` | NOT_FOUND | Unknown run, no roadmap, item has no PRD, PRD not on disk | check the id/path; do not retry |
| `5` | REMOTE_FAILED | Studio reached, operation refused (git/glab stderr in the message) | read stderr; usually a dirty tree or missing remote |
| `6` | AUTH_REQUIRED | Studio gated the route (401/403) | almost always `DEV_MODE` off — see §1.3 |

**Why this matters:** `POST /api/switch-branch` and `POST /api/create-mr` return **HTTP 200** with
`{ok:false}` when git fails. An agent branching on HTTP status would read a failed `git switch` as
success. The CLI maps those to exit `5`. **Trust the exit code, not the transport.**

Diagnostics go to **stderr**; results go to **stdout**. `2>/dev/null` leaves clean parseable data.

---

## 4. Command reference

Add `--json` to any read command for one compact machine-readable line.

### Global flags (accepted by every command)

| Flag | Meaning |
|---|---|
| `--json` | machine-readable output, one line |
| `--cwd <dir>` | which repo to act on (**default: current directory**) |

### Run-start flags (accepted by every `run` verb and its alias)

| Flag | Meaning |
|---|---|
| `--follow` | block until the run settles and adopt **the run's own** exit code (so `&&` chaining works) |
| `--engine claude\|opencode\|codex` | which agent runtime executes the run. Default `claude`. See §1.5 |
| `--profile <dir>` | pin that run's engine config dir. Default = the engine's own default profile |

`--profile` means a different variable per engine — `CLAUDE_CONFIG_DIR=<dir>` (claude),
`XDG_CONFIG_HOME=<dir>` (opencode, which then reads `<dir>/opencode/`), `CODEX_HOME=<dir>/codex`
(codex). One profile dir can therefore carry all three engines side by side.

| Env var | Default | Notes |
|---|---|---|
| `SAKI_STUDIO_URL` | `http://localhost:8787` | **loopback only** — the studio rejects non-localhost hosts |

### Commands

```
saki build  <id|path> [--follow] [--engine claude|opencode|codex] [--profile <dir>]   alias for `saki run build`
saki pickup <id|path> [--follow] [--engine claude|opencode|codex] [--profile <dir>]   alias for `saki run pickup`
saki rplan  <id|path> [--follow] [--engine claude|opencode|codex] [--profile <dir>]   alias for `saki run rplan`
saki status                                        is the studio up, and will it let me in
saki roadmap list                                  work items in this repo
saki roadmap add "<intent>" --epic|--feature|--improvement|--bug
saki run <build|pickup|proto|rplan> <id|path> [--follow] [--engine <e>] [--profile <dir>]
saki run tail <runId>                              stream a run, exit with its verdict
saki run stop <runId>                              stop a running run
saki runs                                          runs the studio still holds
saki prd show <id|path>                            print a PRD
saki prd lock <id|path>                            freeze a PRD before build
saki proto <id|path> [--open]                      URL of an ALREADY-RENDERED proto gallery
saki workitems                                     open PRDs and plans
saki branch                                        current branch
saki branch list                                   local branches
saki branch switch <name> [--create]               switch branch (or create one)
saki mr create                                     push the branch and open a merge request
saki artifacts <runId>                             run artifacts — ALWAYS exits 6, see §7
saki screenshots                                   /qa screenshots in this repo
```

> **Aliases.** `saki build X` == `saki run build X` (same lane, same dedupe). Also `saki pickup`,
> `saki rplan`. There is deliberately **no** `saki proto` alias — see the next note.

> **`saki proto` vs `saki run proto` — do not confuse these.**
> `saki run proto <id>` **renders** the preview (spawns a skill run, takes minutes).
> `saki proto <id>` only **prints the URL** of one already rendered (instant; exit 4 if none).

### Work-item targeting

`<id|path>` accepts either:
- a **roadmap id** — `E22`, `F16`, `I7` (case-insensitive). Resolved via the roadmap's Child PRD.
- a **`.md` path** — relative (to `--cwd`) or absolute.

Item types and their track:

| Prefix | Type | Track | Route |
|---|---|---|---|
| `E` | Epic | PRD | `pickup` → `proto` → `build` |
| `F` | Feature | PRD | `pickup` → `proto` → `build` |
| `I` | Improvement | Plan | `rplan` |
| `B` | Bug | Plan | `rplan` |

---

## The chains — pick one before you start

There are two tracks, and for each a **hands-off** and a **step-by-step** way to run it. Choosing
wrongly wastes a long run or leaves you babysitting something that did not need it.

| | Hands-off | Step-by-step |
|---|---|---|
| **PRD track** (E/F) | `saki build <id> --follow` | `pickup` → `run proto` → `build` |
| **Plan track** (I/B) | `saki build <id> --follow` | `rplan` → `rplan-review` → `approved` → `qa` → `reviewer` → `wrap` |

**`build` already runs the chain for you.** Its own description: *"the hands-off equivalent of
running rplan → rplan-review → approved → qa → reviewer → wrap by hand"* — per slice for a PRD,
once for a plan-track item.

**So run the manual chain only when you need to stop between steps** — inspect the plan before it is
implemented, fix a review finding by hand, re-run one stage without redoing the others. If you just
want the work done, use `build` and let it drive.

### Plan track, step by step

Each step is a separate run. `--follow` blocks until it settles and exits with the run's verdict, so
`&&` stops the chain at the first failure.

| # | Command | Does | Target | Produces | If it fails |
|---|---|---|---|---|---|
| 1 | `saki rplan I7 --follow` | writes a structured plan with a readiness gate | item id | `tasks/*-plan.md` | read the run output; the item may be too vague to plan |
| 2 | `saki rplan-review --follow` | adversarial review of that plan (parallel domain experts) | optional — newest plan | review verdict + edits | it names blockers; fix the plan, re-run step 2 |
| 3 | `saki approved --follow` | implements the plan under TDD, commit per step | optional — newest plan | code + commits | a failing step stops it; read the output before re-running |
| 4 | `saki qa --follow` | verifies the plan's acceptance criteria | optional — newest plan | pass/fail per criterion | fix the code, re-run step 4 — do **not** skip ahead |
| 5 | `saki reviewer --follow` | fresh-context review of the git diff | **none** | findings by severity | fix HIGH findings, re-run step 5 |
| 6 | `saki wrap --follow` | DoD gate (build · tests · coverage ≥80% · security · Sonar) then commit/push/clean | **none** | clean tree, pushed | it names the failing gate; fix and re-run |

Whole chain, stopping at the first failure:

```bash
saki rplan I7 --follow \
  && saki rplan-review --follow \
  && saki approved --follow \
  && saki qa --follow \
  && saki reviewer --follow \
  && saki wrap --follow
```

`saki wrap --heal --follow` makes step 6 autonomous: a failing DoD gate is auto-fixed and re-run
instead of stopping. Use it only when nobody is watching — it is what `build` uses internally.

### PRD track, step by step

| # | Command | Does | Note |
|---|---|---|---|
| 1 | `saki pickup E22 --follow` | writes the PRD and loops prd ↔ prd-review until green | stops ready for proto |
| 2 | `saki run proto E22 --follow` | renders the UI preview gallery | **also LOCKS the PRD** — the freeze before build |
| 3 | `saki proto E22` | prints the gallery URL to look at it | instant; no run |
| 4 | `saki build E22 --follow` | implements every slice, running the whole chain per slice | needs a locked PRD |

`saki prd-review --follow` is available standalone for re-reviewing a PRD you edited by hand;
`pickup` already runs it internally.

### Resuming after a stop

Every step past 1 defaults to the newest plan in `tasks/`, so re-running a step needs no argument:

```bash
saki qa --follow          # re-run just QA against the same plan
```

Pass an explicit target only when several plans are in flight and the newest is not the one you
mean: `saki qa I7` (resolves the item's Child plan) or `saki qa tasks/i7-fix-plan.md`.

---

## 5. Workflows

### 5.1 Mandatory preflight

```bash
saki status --json | jq -e '.reachable and .backendReachable and .devMode and .claudeCodeAccess' >/dev/null \
  || { echo "studio not ready"; exit 1; }
```

### 5.2 Ship a PRD-track item that already has a PRD

```bash
saki prd show E22                     # confirm it's the right PRD
saki prd lock E22                     # freeze requirements (idempotent — already-locked is exit 0)
saki build E22 --follow               # blocks; exits with the RUN's verdict
saki mr create                        # only on success
```

Chained:

```bash
saki prd lock E22 && saki build E22 --follow && saki mr create
```

### 5.3 Take an item from nothing to built

```bash
saki run pickup F16 --follow          # writes + reviews the PRD
saki run proto  F16 --follow          # renders the UI preview, LOCKS the PRD
saki run build  F16 --follow          # implements every slice
saki mr create
```

### 5.4 Plan-track (Improvement / Bug)

```bash
saki run rplan I7 --follow            # writes the plan; no PRD involved
```

### 5.5 Background + reattach

```bash
RUN=$(saki run build E22 --json | jq -r .runId)
saki runs                             # what's in flight
saki run tail "$RUN"                  # attach later — replays from the beginning
saki run stop "$RUN"                  # kill it
```

### 5.6 Add work

```bash
saki roadmap add "let buyers save a cart" --feature
```

Exactly one type flag is required. Zero or two → exit `2` **and nothing is spawned**.

### 5.7 Pick the next actionable item

```bash
saki roadmap list --json \
  | jq -r '.epics[] | select(.status=="Planned" and .track=="PRD") | .id' | head -1
```

---

## 6. Idempotency — safe to retry

`saki run build` is **de-duplicated by the studio**. Re-running it for the same PRD returns the
in-flight run instead of starting a second build:

```console
$ saki run build E22 --json
{"runId":"9f3c1a2e-…","deduped":false}

$ saki run build E22 --json        # retry
{"runId":"9f3c1a2e-…","deduped":true}     # same run, nothing new spawned
```

An agent may retry `run build` freely. `deduped:true` means "already running" — **not** an error.

The lane key is the **absolute PRD path**, shared with the web UI, so a build started in the UI and
a `saki run build` for the same PRD are one run, not two.

Other verbs (`pickup`, `proto`, `rplan`) are **not** de-duplicated — repeating them starts another
run. Check `saki runs` first.

---

## 7. Known limitations

1. **`saki artifacts` always exits `6`.** That route reads a browser session directly, which
   `DEV_MODE` does not provide. This is deliberate — the guard prevents cross-user artifact leaks
   and was not weakened. Use the web UI for artifacts.
2. **Loopback only.** `SAKI_STUDIO_URL` must be `localhost` / `127.0.0.1` / `::1`; the studio
   rejects other hosts with a cross-origin error.
3. **`saki workitems`** prints raw JSON per item even in human mode — use `--json | jq`.
4. **Relative paths resolve against `--cwd`** (default: current directory). Running from a
   subdirectory gives exit `4`. Prefer roadmap ids, or pass `--cwd <repo-root>`.
5. **No hosted/multi-tenant support.** No login, no token storage. Local single-operator only.

---

## 8. Failure playbook

| Symptom | Exit | Cause | Fix |
|---|---|---|---|
| `cannot reach the studio at …` | 3 | studio down | `./run.sh`, wait for `saki status` |
| `authentication required` | 6 | `DEV_MODE` off | §1.3, restart studio |
| `runs BLOCKED` in status | — | claude-code gate | same as above |
| `no tasks/roadmap.md found in <dir>` | 4 | wrong cwd, or repo has no roadmap | `--cwd <repo-root>` |
| `<id> has no Child PRD yet` | 4 | PRD not written | `saki run pickup <id> --follow` |
| `no PRD file at <path>` | 4 | typo, or ran from a subdirectory | use the roadmap id, or `--cwd` |
| `no proto preview rendered` | 4 | preview not built | `saki run proto <id> --follow` |
| `Your local changes would be overwritten` | 5 | dirty tree | commit/stash first |
| `no git remote configured` | 5 | no remote | add one before `mr create` |
| `unknown flag: --x` | 2 | bad invocation | `saki <cmd> --help` |
| `run … takes exactly one argument` | 2 | unquoted multi-word arg | quote it |
| `run error` after tail | 1 | the run itself failed | read the streamed output |

**Rules for an agent:**
- exit `2` or `4` → **do not retry** the same command; it will fail identically.
- exit `3` → start the studio, then retry.
- exit `6` → fix `DEV_MODE`; retrying will not help.
- exit `5` → read stderr; it contains git's own message.
- exit `1` after `run tail` → the *run* failed, not the CLI. Inspect the run's output.

---

## 9. Quick reference card

```bash
# preflight (always)
saki status

# read
saki roadmap list [--json]
saki prd show <id> [--json]
saki runs [--json]
saki workitems --json
saki branch

# act
saki run build  <id> --follow      # && chains on success
saki run pickup <id> --follow
saki run proto  <id> --follow
saki run rplan  <id> --follow
saki prd lock <id>
saki mr create

# control
saki run tail <runId>
saki run stop <runId>

# scope to another repo (preferred form for agents)
saki <any command> --cwd /path/to/repo
```

**Multi-project in three lines:**

```bash
export PATH="/absolute/path/to/pipeline-studio/node_modules/.bin:$PATH"  # once, absolute
saki status                                                             # once per machine
saki roadmap list --cwd ~/Project/whatever                              # per project
```

---

*Source: `apps/cli/`. Fuller human-oriented notes: `apps/cli/README.md`.*
