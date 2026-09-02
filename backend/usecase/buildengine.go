package usecase

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

// Build engine defaults — same names + values as the TS engine (runManager.ts:179-207); a faithful
// port keeps the env knobs unchanged (PRD §11 Non-Goal: don't change the budget/backoff defaults).
const (
	defaultMaxBuildResumes = 40
	defaultResumeBaseMs    = 5000
	defaultMaxBackoffMs    = 300_000            // 5 min
	defaultNoProgressLimit = 3                  // (Slice 2 wires the fingerprint that increments the streak)
	defaultMaxWallMs       = 6 * 60 * 60 * 1000 // 6 h
	defaultLimitWaitMs     = 60 * 60 * 1000     // 1 h (Slice 4)
	defaultStallMs         = 30 * 60 * 1000     // 30 min (Slice 3)
	maxSpawnRetries        = 3                  // bounded re-arm when spawning a resume successor throws
)

// BuildConfig holds the (env-tunable) budgets/backoff for the auto-resume engine.
type BuildConfig struct {
	MaxBuildResumes int
	ResumeBaseMs    int64
	MaxBackoffMs    int64
	NoProgressLimit int
	MaxWallMs       int64
	LimitWaitMs     int64
	StallMs         int64
	AutoPush        bool
}

// DefaultBuildConfig reads the env knobs (falling back to the defaults), matching runManager.ts.
func DefaultBuildConfig() BuildConfig {
	return BuildConfig{
		MaxBuildResumes: int(envInt64("SAKI_MAX_BUILD_RESUMES", defaultMaxBuildResumes)),
		ResumeBaseMs:    envInt64("SAKI_RESUME_DELAY_MS", defaultResumeBaseMs),
		MaxBackoffMs:    defaultMaxBackoffMs,
		NoProgressLimit: defaultNoProgressLimit,
		MaxWallMs:       envInt64("SAKI_BUILD_MAX_WALL_MS", defaultMaxWallMs),
		LimitWaitMs:     envInt64("SAKI_BUILD_LIMIT_WAIT_MS", defaultLimitWaitMs),
		StallMs:         envInt64("SAKI_BUILD_STALL_MS", defaultStallMs),
		AutoPush:        os.Getenv("SAKI_BUILD_AUTOPUSH") != "0",
	}
}

// envInt64 reads a non-negative int env var, falling back on absent/invalid/negative (parity with
// runManager.ts envInt :180-185).
func envInt64(name string, fallback int64) int64 {
	v := os.Getenv(name)
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return fallback
	}
	return n
}

// BuildSpawnReq is a parsed build spawn (the build path of POST /api/run).
type BuildSpawnReq struct {
	Prompt         string
	Cwd            *string
	ConfigDir      *string
	Meta           *domain.Meta // kind must be "build"
	IdempotencyKey *string
	// Engine selects the agent runtime (E26). Empty = claude (the default — today's behavior).
	Engine domain.RunEngine
}

// BuildEngineService owns build spawns + the auto-resume/retry engine on the P1 spawn/tail/journal
// substrate. It reproduces the TS single-event-loop exactly-once semantics on goroutines via three
// atomic store primitives (ReserveBuild / Update test-and-clear / Finalize-returns-transitioned) and a
// NEW periodic Sweep. Port of the build path of runManager.ts.
type BuildEngineService struct {
	spawner     Spawner
	journal     Journal
	store       RunStore
	clock       Clock
	idgen       IDGen
	killer      ProcessKiller
	output      FullOutput
	fingerprint ProgressFingerprint
	sleep       Sleeper
	pusher      Pusher
	cfg         BuildConfig

	// idempotency map (idempotencyKey→runId): a build POST carrying a key already seen returns the
	// existing runId and spawns nothing, even across a restart (rebuilt from journals in RehydrateBuilds).
	// Parity with runManager.ts recordIdempotencyKey (:592-597). BR3 / AC 6.4.
	idemMu sync.Mutex
	idem   map[string]string
}

func NewBuildEngineService(spawner Spawner, journal Journal, store RunStore, clock Clock, idgen IDGen, killer ProcessKiller, output FullOutput, fingerprint ProgressFingerprint, sleep Sleeper, pusher Pusher, cfg BuildConfig) *BuildEngineService {
	if fingerprint == nil {
		fingerprint = func(domain.Run) *string { return nil } // neutral (Slice 2 wires the real one)
	}
	return &BuildEngineService{
		spawner: spawner, journal: journal, store: store, clock: clock, idgen: idgen,
		killer: killer, output: output, fingerprint: fingerprint, sleep: sleep, pusher: pusher, cfg: cfg,
		idem: map[string]string{},
	}
}

type spawnState int

const (
	spawnStarted spawnState = iota // the OS process was reserved + started
	spawnDeduped                   // a build was already in flight for the lane → re-adopted, nothing spawned
)

// SpawnBuild starts a build locally, or re-adopts an in-flight build for the same lane (idempotent —
// a double-click / retried POST does not double-spawn). Returns the live-or-adopted run. AC 1.1, BR3.
func (e *BuildEngineService) SpawnBuild(req BuildSpawnReq) (domain.Run, error) {
	if req.Prompt == "" {
		return domain.Run{}, ErrNoPrompt
	}
	// Idempotency: a retry carrying a key we have already spawned (this process OR a prior one, via the
	// journal-rebuilt map) returns the existing run and spawns nothing — dedupes even a FINALIZED build a
	// lane re-adopt would miss, and survives a restart (AC 6.4 / BR3). Parity runManager.ts index.ts:766.
	if req.IdempotencyKey != nil {
		if existing, ok := e.lookupIdem(*req.IdempotencyKey); ok {
			return existing, nil
		}
	}
	now := e.clock.Now()
	run := e.newBuildRun(buildRunSpec{id: e.idgen.New(), prompt: req.Prompt, cwd: req.Cwd, configDir: req.ConfigDir, meta: req.Meta, idem: req.IdempotencyKey, engine: req.Engine, firstStartedAt: now, startedAt: now})
	adopted, _, err := e.reserveAndSpawn(run, laneKeyOf(req.Meta))
	if err == nil && req.IdempotencyKey != nil {
		e.recordIdem(*req.IdempotencyKey, adopted.ID) // record the (possibly lane-re-adopted) live id
	}
	return adopted, err
}

// lookupIdem returns the run a previously-seen idempotency key maps to (only when it still exists in
// the store). A recorded-but-vanished key falls through to a fresh spawn (never a phantom).
func (e *BuildEngineService) lookupIdem(key string) (domain.Run, bool) {
	e.idemMu.Lock()
	id, ok := e.idem[key]
	e.idemMu.Unlock()
	if !ok {
		return domain.Run{}, false
	}
	return e.store.Get(id)
}

func (e *BuildEngineService) recordIdem(key, id string) {
	e.idemMu.Lock()
	e.idem[key] = id
	e.idemMu.Unlock()
}

// newBuildRun assembles a build run (fresh or successor) with its chain-state + start fingerprint.
// buildRunSpec is the (named) input to newBuildRun — a params object so the two call sites read as
// labelled fields rather than 10 positional args (S107). Zero-valued fields (resumeCount/streak on a
// first run) may be omitted at the call site.
type buildRunSpec struct {
	id, prompt                string
	cwd, configDir, idem      *string
	meta                      *domain.Meta
	engine                    domain.RunEngine
	resumeCount, streak       int
	firstStartedAt, startedAt int64
}

func (e *BuildEngineService) newBuildRun(s buildRunSpec) domain.Run {
	run := domain.Run{
		ID: s.id, Status: domain.StatusRunning, Prompt: s.prompt, Cwd: s.cwd, StartedAt: s.startedAt,
		Meta: s.meta, ConfigDir: s.configDir, ResumeCount: s.resumeCount, NoProgressStreak: s.streak,
		FirstStartedAt: s.firstStartedAt, IdempotencyKey: s.idem, LastActivityAt: s.startedAt,
		Engine: domain.ResolveEngine(s.engine),
	}
	run.StartFingerprint = e.fingerprint(run) // captured at start; compared at finalize for the breaker
	return run
}

// reserveAndSpawn: EnsureWritable → atomic lane reserve → journal → detached spawn → arm onExit. The
// lane reserve (single locked check-and-Put) closes the double-spawn TOCTOU (BR3). Ordering mirrors
// RunService.Spawn: register (reserve) BEFORE spawn so a fast-exit finalize isn't lost; pid journaled
// before the wait() goroutine is armed.
func (e *BuildEngineService) reserveAndSpawn(run domain.Run, laneKey string) (domain.Run, spawnState, error) {
	if err := e.journal.EnsureWritable(); err != nil {
		return domain.Run{}, spawnStarted, ErrRunsDirUnwritable
	}
	if existingID, reserved := e.store.ReserveBuild(laneKey, run); !reserved {
		if existing, ok := e.store.Get(existingID); ok {
			return existing, spawnDeduped, nil
		}
		return domain.Run{ID: existingID}, spawnDeduped, nil
	}
	if err := e.journal.Write(run); err != nil { // durable record before spawn; failure → 500, no phantom
		e.store.Delete(run.ID)
		return domain.Run{}, spawnStarted, ErrRunsDirUnwritable
	}
	pid, wait, err := e.spawner.Spawn(SpawnSpec{ID: run.ID, Prompt: run.Prompt, Cwd: run.Cwd, ConfigDir: run.ConfigDir, Kind: "build", Engine: run.Engine})
	if err != nil {
		e.store.Delete(run.ID) // spawn failed → no phantom "running" build
		return domain.Run{}, spawnStarted, err
	}
	e.store.SetPid(run.ID, pid)
	run.Pid = &pid
	_ = e.journal.Write(run)
	go func() { e.onExit(run.ID, wait()) }()
	return run, spawnStarted, nil
}

// onExit finalizes a build when its process exits and decides the auto-resume. The Finalize-returns-
// transitioned guard means a build a Stop (or reaper) already ended is never re-classified/re-resumed
// (BR2); a run whose StopRequested flag is set classifies as user-stop → terminal.
func (e *BuildEngineService) onExit(id string, code int) {
	// Finalize and the subsequent retry/park decision are deliberately two store operations. Mark
	// this short handoff first so WorkflowService cannot observe a terminal child in between them and
	// mistake a retryable turn for workflow failure.
	if !e.beginFinalizing(id) {
		return
	}
	defer e.endFinalizing(id)
	if !e.store.Finalize(id, code) {
		return // already finalized elsewhere (stop / reaper) — that path owns the terminal decision
	}
	run, ok := e.store.Get(id)
	if !ok {
		return
	}
	e.rejournal(id)
	cause := domain.RedriveCause{Kind: domain.CauseExit, ExitCode: &code}
	if run.StopRequested {
		cause = domain.RedriveCause{Kind: domain.CauseUserStop}
	}
	e.finalizeBuild(run, cause)
}

// finalizeBuild runs the auto-push (on a successful completion) then the classify → plan → schedule
// pipeline, on the finalized run's durable output.
func (e *BuildEngineService) finalizeBuild(run domain.Run, cause domain.RedriveCause) {
	lines, _ := e.output.ReadAll(run.ID)
	e.maybePushOnComplete(run, lines)
	if d := e.planResume(run, lines, cause); d != nil {
		e.scheduleResume(run, *d)
	}
}

// maybePushOnComplete auto-pushes a build's branch the moment it finishes SUCCESSFULLY (a line-anchored
// PRD_BUILD_COMPLETE in the final spoken text) — the "end of work" counterpart to auto-branch at start.
// Fires at most once: an ATOMIC pushedAt test-and-set under the store lock guards a re-finalize (BR4),
// never --force. Fire-and-forget: the push runs on its own goroutine (bounded by the Pusher's own
// timeout) so a slow/hung push never blocks finalize (AC 5.4), and its result is appended to the DURABLE
// <id>.out (replayable to a reconnecting viewer), not the already-closed live stream (AC 5.5).
// SAKI_BUILD_AUTOPUSH=0 disables. Port of runManager.ts maybePushOnComplete (:807-832).
func (e *BuildEngineService) maybePushOnComplete(run domain.Run, lines []ParsedLine) {
	if !e.cfg.AutoPush || e.pusher == nil || kindOf(run) != "build" || run.Cwd == nil {
		return
	}
	if !IsBuildComplete(FinalSpokenText(lines)) {
		return // not a fresh PRD_BUILD_COMPLETE (a BLOCKED/HARD-STOP or mid-turn end never pushes)
	}
	now := e.clock.Now()
	pushed := false
	e.store.Update(run.ID, func(r *domain.Run) {
		if r.PushedAt == nil { // atomic test-and-set: exactly one finalize pushes (BR4)
			r.PushedAt = &now
			pushed = true
		}
	})
	if !pushed {
		return // already pushed (a re-finalize / concurrent finalize) — never double-push
	}
	e.rejournal(run.ID)
	cwd := *run.Cwd
	e.breadcrumb(run.ID, "build complete → pushing branch to origin…")
	go func() {
		e.breadcrumb(run.ID, pushResultLine(e.pusher.Push(cwd)))
	}()
}

// pushResultLine renders a PushResult as a durable breadcrumb line (parity with runManager.ts:823-828).
func pushResultLine(r PushResult) string {
	switch {
	case r.OK:
		branch := r.Branch
		if branch == "" {
			branch = "branch"
		}
		return "pushed " + branch + " to origin ✓"
	case r.Skipped != "":
		return "push skipped: " + r.Skipped
	default:
		msg := r.Error
		if msg == "" {
			msg = "unknown error"
		}
		return "push failed: " + msg
	}
}

// resumeDecision is an auto-resume plan: the successor to spawn + how the chain advances into it.
type resumeDecision struct {
	successorID     string
	delayMs         int64
	nextResumeCount int
	nextStreak      int
	firstStartedAt  int64
}

// planResume decides whether — and how — a finished build should auto-resume, appending a durable
// breadcrumb either way. The terminal/retryable/park split is classifyOutcome; here we add the turn +
// wall-clock budgets and the no-progress bail, then pick the backoff. Port of runManager.ts:839-904
// (usage-limit / auth reclassify is Slice 4; the fingerprint is neutral until Slice 2).
func (e *BuildEngineService) planResume(run domain.Run, lines []ParsedLine, cause domain.RedriveCause) *resumeDecision {
	switch ClassifyOutcome(kindOf(run), lines, cause) {
	case OutcomeRetryable:
		// fall through to the budget/backoff logic below
	case OutcomePark:
		e.breadcrumb(run.ID, "auto-resume: declined (ambiguous/failed end) — parked; use Continue to resume")
		return nil
	case OutcomeAwaiting:
		e.breadcrumb(run.ID, "paused — awaiting your decision; pick an option in the stream to continue")
		return nil
	default: // terminal
		return nil
	}
	// Turn cap — the absolute backstop for every retryable path (maxBuildResumes==0 disables auto-resume).
	if run.ResumeCount >= e.cfg.MaxBuildResumes {
		e.breadcrumb(run.ID, fmt.Sprintf("auto-resume: resume budget exhausted (%d) — parked for operator", run.ResumeCount))
		return nil
	}
	// Usage/rate cap or auth error — the CLI exits 0 on a cap WITH a message (Slice-0 spike), so detect
	// it from the text. A cap → wait until reset (a short backoff would just re-hit it); an auth error
	// is not transient → park. Checked BEFORE the wall-clock budget so idle cap-waiting isn't charged to
	// the active-time budget (firstStartedAt is pushed forward by the wait). Port of runManager.ts:855-880.
	if limit := DetectLimit(lines, e.clock.Now()); limit != nil {
		if limit.Kind == "auth-error" {
			e.breadcrumb(run.ID, "auto-resume: authentication error — parked; run /login, then Continue")
			return nil
		}
		now := e.clock.Now()
		delayMs := e.cfg.LimitWaitMs // unparseable/absent reset → the configured wait (never forever, never immediate)
		if limit.ResetAt != nil {
			delayMs = clampDelay(*limit.ResetAt, now, e.cfg.ResumeBaseMs, e.cfg.LimitWaitMs)
		}
		successorID := e.idgen.New()
		e.breadcrumb(run.ID, fmt.Sprintf("auto-resume: usage limit reached → waiting %dmin for reset → continuing as %s", delayMs/60000, successorID))
		// Push firstStartedAt forward by the wait so the WALL-CLOCK budget (active time) isn't consumed by
		// idle waiting, and do NOT penalise the no-progress streak (a cap is not a failure to progress).
		return &resumeDecision{successorID: successorID, delayMs: delayMs, nextResumeCount: run.ResumeCount + 1, nextStreak: run.NoProgressStreak, firstStartedAt: run.FirstStartedAt + delayMs}
	}
	// Wall-clock budget — active (non-limit-wait) time since the chain's first start.
	if e.clock.Now()-run.FirstStartedAt >= e.cfg.MaxWallMs {
		e.breadcrumb(run.ID, fmt.Sprintf("BLOCKED: wall-clock budget (%dmin) exhausted — parked for operator", e.cfg.MaxWallMs/60000))
		return nil
	}
	// Progress fingerprint: now vs at this run's start. nil on either side = unknown → neutral (streak
	// unchanged, fall back to the caps). Equal = no progress → +1; advanced = reset. (Neutral in Slice 1.)
	endFp := e.fingerprint(run)
	known := run.StartFingerprint != nil && endFp != nil
	madeProgress := known && *endFp != *run.StartFingerprint
	noProgress := known && *endFp == *run.StartFingerprint
	nextStreak := run.NoProgressStreak
	switch {
	case madeProgress:
		nextStreak = 0
	case noProgress:
		nextStreak = run.NoProgressStreak + 1
	}
	if nextStreak >= e.cfg.NoProgressLimit {
		e.breadcrumb(run.ID, fmt.Sprintf("BLOCKED: no verified progress after %d auto-resumes — parked for operator", nextStreak))
		return nil
	}
	delayMs := e.cfg.ResumeBaseMs
	if !madeProgress {
		delayMs = backoff(e.cfg.ResumeBaseMs, e.cfg.MaxBackoffMs, nextStreak)
	}
	successorID := e.idgen.New()
	e.breadcrumb(run.ID, fmt.Sprintf("auto-resume: build ended its turn without finishing → continuing as %s after %dms", successorID, delayMs))
	return &resumeDecision{successorID: successorID, delayMs: delayMs, nextResumeCount: run.ResumeCount + 1, nextStreak: nextStreak, firstStartedAt: run.FirstStartedAt}
}

// clampDelay = min(max(resetAt-now, base), limitWait). A parsed reset far out is capped to limitWait
// (so a mis-parse can't wait forever); a reset already near/past clamps up to base (never re-hit
// immediately). Port of the usage-limit delay math in runManager.ts:867-870.
func clampDelay(resetAt, now, base, limitWait int64) int64 {
	d := resetAt - now
	if d < base {
		d = base
	}
	if d > limitWait {
		d = limitWait
	}
	return d
}

// backoff = min(base * 2^streak, cap). Port of runManager.ts:900.
func backoff(base, cap int64, streak int) int64 {
	d := base
	for i := 0; i < streak; i++ {
		d *= 2
		if d >= cap {
			return cap
		}
	}
	if d > cap {
		return cap
	}
	return d
}

// scheduleResume persists the pending resume on the predecessor (durable across restart) AND arms a
// timer to fire it; whichever of {timer, Sweep} fires first wins the test-and-clear in firePendingResume.
// The arm is gated on !StopRequested under the store lock: a Stop that raced a natural exit (won the
// Finalize before its StopRequested flag landed) is still observed here, so a stopped build never
// arms a resume (BR2 — closes the Stop-vs-onExit race the single-threaded TS engine avoided for free).
func (e *BuildEngineService) scheduleResume(run domain.Run, d resumeDecision) {
	pr := &domain.PendingResume{
		SuccessorID: d.successorID, ResumeAt: e.clock.Now() + d.delayMs, ResumeCount: d.nextResumeCount,
		NoProgressStreak: d.nextStreak, FirstStartedAt: d.firstStartedAt,
	}
	armed := false
	e.store.Update(run.ID, func(r *domain.Run) {
		if r.StopRequested {
			return // the operator stopped it in the race window — do not arm a resume
		}
		r.PendingResume = pr
		armed = true
	})
	if !armed {
		return
	}
	e.rejournal(run.ID)
	e.sleep(d.delayMs, func() { e.firePendingResume(run.ID) })
}

// firePendingResume fires a scheduled resume exactly once. The test-and-clear runs under the store
// lock — only the caller whose closure observed pendingResume!=nil wins, so a concurrent timer + Sweep
// (or two Sweeps) can't both spawn (BR3). The successor goes through the same atomic lane reserve, so a
// manual Continue that already started a build for the lane wins and this drops the durable resume.
func (e *BuildEngineService) firePendingResume(id string) {
	var p domain.PendingResume
	won := false
	e.store.Update(id, func(r *domain.Run) {
		if r.PendingResume != nil {
			won, p = true, *r.PendingResume
			r.PendingResume = nil
		}
	})
	if !won {
		return // a concurrent timer/sweep already fired it (idempotent)
	}
	e.rejournal(id)
	pred, ok := e.store.Get(id)
	if !ok {
		return
	}
	if pred.StopRequested {
		return // the operator stopped the chain in the race window — don't spawn the successor (BR2)
	}
	now := e.clock.Now()
	succ := e.newBuildRun(buildRunSpec{id: p.SuccessorID, prompt: pred.Prompt, cwd: pred.Cwd, configDir: pred.ConfigDir, meta: pred.Meta, idem: pred.IdempotencyKey, engine: pred.Engine, resumeCount: p.ResumeCount, streak: p.NoProgressStreak, firstStartedAt: p.FirstStartedAt, startedAt: now})
	_, state, err := e.reserveAndSpawn(succ, laneKeyOf(pred.Meta))
	if err != nil {
		e.rearmOrPark(id, p, err) // a synchronous spawn throw must not silently drop the durable resume
		return
	}
	_ = state // spawnDeduped ⇒ a manual Continue already owns the lane; the resume is satisfied, drop it
}

// rearmOrPark re-arms a failed resume spawn a bounded number of times (transient EMFILE/EAGAIN), then
// parks (a permanent failure like a missing binary must not tight-loop). E26 9.1.5: a missing opencode
// binary parks IMMEDIATELY (no rearm) — retrying a binary that isn't on PATH cannot succeed.
func (e *BuildEngineService) rearmOrPark(predID string, p domain.PendingResume, spawnErr error) {
	if errors.Is(spawnErr, ErrBinaryNotFound) {
		// E26 9.1.5 — a missing engine binary is permanent: park with a DURABLE breadcrumb on the
		// predecessor's .out so the operator sees a loud, specific reason (not a silent drop).
		e.breadcrumb(predID, "auto-resume: engine binary not found — parked")
		log.Printf("auto-resume spawn failed for %s — engine binary not found, parking: %v", p.SuccessorID, spawnErr)
		return
	}
	attempts := p.SpawnAttempts + 1
	e.store.Update(predID, func(r *domain.Run) {
		if attempts <= maxSpawnRetries {
			np := p
			np.SpawnAttempts = attempts
			np.ResumeAt = e.clock.Now() + e.cfg.ResumeBaseMs
			r.PendingResume = &np
		}
	})
	if attempts <= maxSpawnRetries {
		log.Printf("auto-resume spawn failed for %s (attempt %d), re-arming: %v", p.SuccessorID, attempts, spawnErr)
	} else {
		log.Printf("auto-resume spawn failed for %s %d× — giving up (parked): %v", p.SuccessorID, attempts, spawnErr)
	}
	e.rejournal(predID)
}

// Sweep is the NEW periodic tick: kill hung builds (the stall watchdog) then fire every pending resume
// whose backoff has elapsed (the restart-safe backstop for the in-memory timers, and — from Slice 6 —
// the reloaded pendingResume records). Runs on a single ticker goroutine (no concurrent sweeps), so the
// snapshot-then-Update in sweepStalls is race-free. Port of runManager.ts pump (:676-680).
func (e *BuildEngineService) Sweep() {
	e.sweepStalls()
	e.sweepPendingResumes()
}

// sweepPendingResumes fires every pending resume whose backoff has elapsed. Port of runManager.ts
// sweepPendingResumes :959-963.
func (e *BuildEngineService) sweepPendingResumes() {
	now := e.clock.Now()
	for _, r := range e.store.List() {
		if r.PendingResume != nil && r.PendingResume.ResumeAt <= now {
			e.firePendingResume(r.ID)
		}
	}
}

// --- Slice 6: restart survival (exactly-one-live) ---

// RehydrateBuilds routes Go-owned BUILD runs across a `backend/` restart, so a build neither loses its
// auto-resume chain nor ends up with two live processes (BR2). Called ONCE at boot, AFTER the generic
// Rehydrate has Put every journaled run into the store (a build is Put as-recorded — never prematurely
// finalized there) and BEFORE serving. Policy (parity runManager.ts resume :984-1042):
//   - rebuild the idempotency map from every build carrying a key (AC 6.4);
//   - a still-RUNNING build: seed its stall-watchdog activity baseline (else a healthy survived build is
//     false-SIGTERM'd right after boot), then —
//     · <id>.exit present (finished while down)  → finalize + route through the engine (resume/push);
//     · pid still alive                           → RE-ATTACH: watch its real exit, never re-spawn (6.1);
//     · dead with no exit (interrupted)           → resume as retryable (6.1);
//   - a non-running build is left to the Sweep (it fires a due pendingResume under the same ConfigDir,
//     6.2) or left alone (a finalized build is never re-resumed/re-pushed — status-guarded, 6.3).
func (e *BuildEngineService) RehydrateBuilds(ctx context.Context, reader JournalReader, watch ExitWatcher) {
	recs, err := reader.Load()
	if err != nil {
		log.Printf("rehydrate builds: %v", err)
		return
	}
	now := e.clock.Now()
	for _, rr := range recs {
		run := rr.Run
		if kindOf(run) != "build" {
			continue
		}
		// Map the key to the ORIGINAL request's run (ResumeCount 0), never an arbitrary chain member:
		// internal resume successors inherit the key in the journal, so recording every one in readdir
		// order would leave key→a random (often finalized) successor. Deterministic, and parity with TS
		// (recordIdempotencyKey maps key→the original runId). AC 6.4.
		if run.IdempotencyKey != nil && run.ResumeCount == 0 {
			e.recordIdem(*run.IdempotencyKey, run.ID) // durable dedupe across the restart
		}
		if run.Status != domain.StatusRunning {
			continue // recovering → the Sweep fires it (6.2); finalized → left alone (6.3)
		}
		// Seed the viewerless-activity baseline so the stall watchdog gives a survived build a full new
		// window instead of SIGTERM-ing it on the first post-boot tick (Slice-3 CARRY).
		e.store.Update(run.ID, func(r *domain.Run) { r.LastActivityAt = now; r.LastOutSize = e.output.Size(run.ID) })
		switch {
		case rr.HasExit:
			code := -1
			if rr.ExitCode != nil {
				code = *rr.ExitCode
			}
			e.finalizeSurvived(run.ID, domain.RedriveCause{Kind: domain.CauseExit, ExitCode: &code}, code)
		case rr.PidAlive:
			go e.watchSurvivedBuild(ctx, run.ID, run.Pid, watch) // exactly-one-live: re-attach, never re-spawn (6.1)
		default:
			e.finalizeSurvived(run.ID, domain.RedriveCause{Kind: domain.CauseInterrupted}, -1) // dead-without-exit → resume (6.1)
		}
	}
}

// finalizeSurvived finalizes a restart-survived build (whose spawn-time wait() goroutine died with the
// previous process) then routes it through the engine's post-finalize (auto-push + classify → resume).
// The store.Finalize guard means a build a Stop / the reaper already ended is never re-resumed (BR2 /
// 6.3), and maybePushOnComplete's durable pushedAt guard means a restart re-finalize never double-pushes
// (BR4 / 6.3).
func (e *BuildEngineService) finalizeSurvived(id string, cause domain.RedriveCause, code int) {
	if !e.beginFinalizing(id) {
		return
	}
	defer e.endFinalizing(id)
	if !e.store.Finalize(id, code) {
		return // already finalized (status-guarded)
	}
	run, ok := e.store.Get(id)
	if !ok {
		return
	}
	e.rejournal(id)
	e.finalizeBuild(run, cause)
}

func (e *BuildEngineService) beginFinalizing(id string) bool {
	started := false
	e.store.Update(id, func(r *domain.Run) {
		if r.Status == domain.StatusRunning {
			r.Finalizing = true
			started = true
		}
	})
	return started
}

func (e *BuildEngineService) endFinalizing(id string) {
	e.store.Update(id, func(r *domain.Run) { r.Finalizing = false })
}

// watchSurvivedBuild watches a restart-survived build whose child is STILL ALIVE (owned by the previous
// process, so no wait() delivers its exit here). It polls the durable <id>.exit / pid-liveness and, when
// the child terminates, routes it through finalizeSurvived — so a survived build that completes (or is
// killed by the stall watchdog) still auto-resumes/pushes. Mirrors ReaperService.reap, but engine-routed
// (the plain reaper's bare Finalize skips resume/push, so it must SKIP builds). One live process holds
// throughout: re-attach never spawns.
func (e *BuildEngineService) watchSurvivedBuild(ctx context.Context, id string, pid *int, watch ExitWatcher) {
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	// Bound the poll to the wall-clock budget (guards a pid-reuse "alive forever" leak — parity
	// ReaperService.maxWait) rather than a hardcoded 6h: a build under a raised SAKI_BUILD_MAX_WALL_MS is
	// watched for its whole legitimate budget, never prematurely finalized. Floored so a tiny/zero budget
	// still lets the child be observed at least once.
	bound := time.Duration(e.cfg.MaxWallMs) * time.Millisecond
	if bound < time.Minute {
		bound = time.Minute
	}
	giveUp := time.After(bound)
	for {
		select {
		case <-ctx.Done():
			return
		case <-giveUp:
			// KILL the group FIRST so a resume can never spawn beside a still-live child (BR2
			// exactly-one-live); by now the wall budget is spent, so finalize→planResume parks anyway.
			if pid != nil && *pid > 1 {
				_ = e.killer.KillGroup(*pid)
			}
			e.finalizeSurvived(id, domain.RedriveCause{Kind: domain.CauseInterrupted}, -1)
			return
		case <-t.C:
		}
		run, ok := e.store.Get(id)
		if !ok || run.Status != domain.StatusRunning {
			return // finalized elsewhere (Stop / stall-watchdog escalation)
		}
		exit, hasExit, alive := watch.Terminal(id, pid)
		switch {
		case hasExit:
			code := -1
			if exit != nil {
				code = *exit
			}
			e.finalizeSurvived(id, domain.RedriveCause{Kind: domain.CauseExit, ExitCode: &code}, code)
			return
		case !alive:
			e.finalizeSurvived(id, domain.RedriveCause{Kind: domain.CauseInterrupted}, -1)
			return
		}
	}
}

// stallSigkillGraceMs: after SIGTERM-ing a hung build, escalate to SIGKILL if it's still alive this
// long later, so a process that ignores SIGTERM can't stay "running" forever. Parity runManager.ts:205.
const stallSigkillGraceMs = 30 * 1000 // 30 s

// sweepStalls kills a build that has produced NO output for stallMs while its process is still alive —
// the stream-json hang bug leaves a wedged run running forever otherwise. Activity is measured from
// <id>.out FILE GROWTH (not SSE reads), so an unattended headless build with no viewer is not
// false-killed (AC 3.2). The kill makes the per-run wait() goroutine fire onExit with the absent-exit
// code (-1) → retryable, so the resume engine retries it. Port of runManager.ts sweepStalls :685-710.
func (e *BuildEngineService) sweepStalls() {
	if e.cfg.StallMs <= 0 {
		return // watchdog disabled (AC 3.4)
	}
	now := e.clock.Now()
	for _, run := range e.store.List() {
		e.sweepRun(run, now)
	}
}

// sweepRun runs the stall watchdog for a single build run at time `now`. Extracted from sweepStalls
// verbatim: each `continue` in the original loop body becomes a `return` (same skip-this-run effect).
func (e *BuildEngineService) sweepRun(run domain.Run, now int64) {
	if run.Status != domain.StatusRunning || kindOf(run) != "build" {
		return
	}
	if run.Pid == nil || *run.Pid <= 1 || !e.killer.Alive(*run.Pid) {
		return // no live child to watch (its death is handled by the wait() goroutine)
	}
	// Viewerless activity: fresh bytes in <id>.out reset the watchdog (the sweep is the drain).
	// Also clear StallKilledAt so a build that recovers after a SIGTERM earns a FULL new stall
	// window rather than being SIGKILL'd on its next brief silence.
	if size := e.output.Size(run.ID); size > run.LastOutSize {
		e.store.Update(run.ID, func(r *domain.Run) { r.LastOutSize = size; r.LastActivityAt = now; r.StallKilledAt = nil })
		return
	}
	if run.StallKilledAt != nil {
		// Already SIGTERM'd but STILL alive after the grace → it ignored SIGTERM → escalate to SIGKILL.
		if now-*run.StallKilledAt < stallSigkillGraceMs {
			return
		}
		t := now
		e.store.Update(run.ID, func(r *domain.Run) { r.StallKilledAt = &t })
		_ = e.killer.KillGroup(*run.Pid) // gone → the next wait() finalizes it as interrupted
		return
	}
	if now-run.LastActivityAt < e.cfg.StallMs {
		return
	}
	t := now
	e.store.Update(run.ID, func(r *domain.Run) { r.StallKilledAt = &t })
	e.breadcrumb(run.ID, fmt.Sprintf("stall watchdog: no output for %dmin → restarting (hung)", e.cfg.StallMs/60000))
	_ = e.killer.SignalGroup(*run.Pid) // SIGTERM the group; wait() → onExit(-1) → retryable
}

// Stop stops a Go-owned build. A RUNNING build: mark StopRequested (so onExit won't auto-resume),
// SIGTERM its group, finalize. A RECOVERING build (finalized + a pending resume in a backoff/limit
// wait): cancel the pending resume and park — no successor spawns. Returns false for an unknown or
// already-terminal-and-not-recovering build (→ 404). BR2 (stop-cancels-resume).
func (e *BuildEngineService) Stop(id string) bool {
	run, ok := e.store.Get(id)
	if !ok {
		return false
	}
	if run.Status == domain.StatusRunning {
		// Set StopRequested AND clear any resume armed by a concurrent onExit (the natural-exit race):
		// scheduleResume/firePendingResume both observe StopRequested under this same lock, so the
		// stopped build can neither arm nor fire a successor (BR2).
		e.store.Update(id, func(r *domain.Run) { r.StopRequested = true; r.PendingResume = nil })
		if run.Pid != nil && *run.Pid > 1 {
			_ = e.killer.SignalGroup(*run.Pid) // already-exited → the signal errors harmlessly
		}
		e.store.Finalize(id, 143) // 128+SIGTERM; onExit sees !transitioned → no resume
		e.rejournal(id)
		return true
	}
	if run.PendingResume != nil {
		e.store.Update(id, func(r *domain.Run) { r.PendingResume = nil; r.StopRequested = true })
		e.breadcrumb(id, "auto-resume cancelled by operator — parked")
		e.rejournal(id)
		return true
	}
	return false
}

// breadcrumb appends a durable raw breadcrumb to <id>.out (best-effort). Because the Go store keeps no
// in-memory events, the durable file is the ONLY sink — a late SSE viewer replays it, and RecoveryView
// scans it for the parked reason. The child is dead at finalize, so the append has no concurrent writer.
func (e *BuildEngineService) breadcrumb(id, text string) {
	if err := e.journal.AppendOut(id, text); err != nil {
		log.Printf("breadcrumb append failed for %s: %v", id, err)
	}
}

func (e *BuildEngineService) rejournal(id string) {
	if run, ok := e.store.Get(id); ok {
		_ = e.journal.Write(run)
	}
}

func laneKeyOf(meta *domain.Meta) string {
	if meta == nil {
		return ""
	}
	return meta.LaneKey
}

func kindOf(run domain.Run) domain.RunKind {
	if run.Meta == nil {
		return ""
	}
	return run.Meta.Kind
}
