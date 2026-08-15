package usecase

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

// --- engine test fakes ---

// countingSpawner counts Spawn calls thread-safely; wait() blocks so a spawned run stays "running"
// (no onExit cascade) unless exitCode is set. err makes every Spawn throw (spawn-failure path).
type countingSpawner struct {
	calls    int64
	err      error
	exitCode *int
}

func (s *countingSpawner) Spawn(SpawnSpec) (int, func() int, error) {
	atomic.AddInt64(&s.calls, 1)
	if s.err != nil {
		return 0, nil, s.err
	}
	code := s.exitCode
	return 100, func() int {
		if code != nil {
			return *code
		}
		select {} // block: the run stays running
	}, nil
}
func (s *countingSpawner) count() int { return int(atomic.LoadInt64(&s.calls)) }

// spyKiller is shared from stop_test.go (signals, lastPid int).

// stubOutput returns fixed parsed lines for any id (the finalized-build classification input) and a
// fixed byte size (the stall watchdog's viewerless activity source).
type stubOutput struct {
	lines []ParsedLine
	size  int64
}

func (o stubOutput) ReadAll(string) ([]ParsedLine, error) { return o.lines, nil }
func (o stubOutput) Size(string) int64                    { return o.size }

// mutClock is a settable Clock for the time-based stall tests.
type mutClock struct{ v int64 }

func (c *mutClock) Now() int64 { return c.v }

// recordingSleeper captures the scheduled resume without firing it (so a test asserts "a resume was
// scheduled" without the successor actually spawning).
type recordingSleeper struct {
	mu     sync.Mutex
	delays []int64
}

func (s *recordingSleeper) sleep(delayMs int64, _ func()) {
	s.mu.Lock()
	s.delays = append(s.delays, delayMs)
	s.mu.Unlock()
}

type seqID struct{ n int64 }

func (g *seqID) New() string { return fmt.Sprintf("id-%d", atomic.AddInt64(&g.n, 1)) }

func testCfg() BuildConfig {
	return BuildConfig{MaxBuildResumes: 40, ResumeBaseMs: 5000, MaxBackoffMs: 300_000, NoProgressLimit: 3, MaxWallMs: 6 * 60 * 60 * 1000, LimitWaitMs: 60 * 60 * 1000, StallMs: 30 * 60 * 1000, AutoPush: true}
}

func newEngineWith(sp Spawner, st RunStore, sleep Sleeper, out FullOutput, cfg BuildConfig) *BuildEngineService {
	return NewBuildEngineService(sp, &spyJournal{writable: true}, st, fixedClock{}, &seqID{}, &spyKiller{}, out, nil, sleep, nil, cfg)
}

func buildRun(id, lane string, resumeCount, streak int, firstStartedAt int64) domain.Run {
	return domain.Run{
		ID: id, Status: domain.StatusRunning, Prompt: "/build", Meta: &domain.Meta{Kind: "build", LaneKey: lane},
		ResumeCount: resumeCount, NoProgressStreak: streak, FirstStartedAt: firstStartedAt,
	}
}

// --- AC 1.2 · BR1 hard-budget matrix + backoff ---

func TestPlanResume_TurnCapParks(t *testing.T) {
	e := newEngineWith(&countingSpawner{}, newMemStore(), (&recordingSleeper{}).sleep, stubOutput{}, testCfg())
	run := buildRun("b", "L", 40, 0, 777) // resumeCount == maxBuildResumes
	if d := e.planResume(run, nil, domain.RedriveCause{Kind: domain.CauseExit, ExitCode: ptrInt(1)}); d != nil {
		t.Fatalf("at the turn cap the build must park (no successor), got %+v", d)
	}
}

func TestPlanResume_ZeroBudgetParks(t *testing.T) {
	cfg := testCfg()
	cfg.MaxBuildResumes = 0 // auto-resume disabled
	e := newEngineWith(&countingSpawner{}, newMemStore(), (&recordingSleeper{}).sleep, stubOutput{}, cfg)
	if d := e.planResume(buildRun("b", "L", 0, 0, 777), nil, domain.RedriveCause{Kind: domain.CauseExit, ExitCode: ptrInt(1)}); d != nil {
		t.Fatalf("maxBuildResumes==0 must disable auto-resume (park), got %+v", d)
	}
}

func TestPlanResume_WallClockParks(t *testing.T) {
	cfg := testCfg()
	cfg.MaxWallMs = 100
	e := newEngineWith(&countingSpawner{}, newMemStore(), (&recordingSleeper{}).sleep, stubOutput{}, cfg)
	run := buildRun("b", "L", 1, 0, 777-200) // now(777) - firstStartedAt(577) = 200 >= 100
	if d := e.planResume(run, nil, domain.RedriveCause{Kind: domain.CauseExit, ExitCode: ptrInt(1)}); d != nil {
		t.Fatalf("past the wall-clock budget the build must park, got %+v", d)
	}
}

func TestPlanResume_BackoffEscalatesAndCaps(t *testing.T) {
	e := newEngineWith(&countingSpawner{}, newMemStore(), (&recordingSleeper{}).sleep, stubOutput{}, testCfg())
	// neutral fingerprint → nextStreak == run.NoProgressStreak; delay = base * 2^streak.
	for _, tc := range []struct {
		streak int
		want   int64
	}{{0, 5000}, {1, 10000}, {2, 20000}} {
		d := e.planResume(buildRun("b", "L", 1, tc.streak, 777), nil, domain.RedriveCause{Kind: domain.CauseExit, ExitCode: ptrInt(1)})
		if d == nil || d.delayMs != tc.want {
			t.Fatalf("streak %d: want delay %d, got %+v", tc.streak, tc.want, d)
		}
	}
	// cap: a small ceiling clamps the escalation.
	cfg := testCfg()
	cfg.MaxBackoffMs = 8000
	capped := newEngineWith(&countingSpawner{}, newMemStore(), (&recordingSleeper{}).sleep, stubOutput{}, cfg)
	d := capped.planResume(buildRun("b", "L", 1, 2, 777), nil, domain.RedriveCause{Kind: domain.CauseExit, ExitCode: ptrInt(1)}) // 5000*4=20000 → cap 8000
	if d == nil || d.delayMs != 8000 {
		t.Fatalf("backoff must clamp to the ceiling, got %+v", d)
	}
}

// A retryable build schedules a durable resume (pendingResume set + the sleeper armed).
func TestFinalize_SchedulesResume(t *testing.T) {
	st := newMemStore()
	sl := &recordingSleeper{}
	e := newEngineWith(&countingSpawner{}, st, sl.sleep, stubOutput{}, testCfg())
	run := buildRun("b", "L", 0, 0, 777)
	st.Put(run)
	st.Finalize("b", 1) // running → error
	e.finalizeBuild(run, domain.RedriveCause{Kind: domain.CauseExit, ExitCode: ptrInt(1)})
	got, _ := st.Get("b")
	if got.PendingResume == nil {
		t.Fatal("a retryable build must persist a pendingResume (durable across restart)")
	}
	if len(sl.delays) != 1 || sl.delays[0] != 5000 {
		t.Fatalf("the backoff sleeper must be armed once at base, got %v", sl.delays)
	}
}

// --- AC 2.1–2.3 · progress-tied circuit-breaker (injected fingerprint) ---

func newEngineFP(fp ProgressFingerprint, cfg BuildConfig) *BuildEngineService {
	return NewBuildEngineService(&countingSpawner{}, &spyJournal{writable: true}, newMemStore(), fixedClock{}, &seqID{}, &spyKiller{}, stubOutput{}, fp, (&recordingSleeper{}).sleep, nil, cfg)
}

func constFP(v string) ProgressFingerprint { return func(domain.Run) *string { return &v } }

func retryableCause() domain.RedriveCause {
	return domain.RedriveCause{Kind: domain.CauseExit, ExitCode: ptrInt(1)}
}

// AC 2.1 — a stable fingerprint across turns increments the streak and parks at noProgressLimit.
func TestBreaker_NoProgressParksAtLimit(t *testing.T) {
	e := newEngineFP(constFP("1:none"), testCfg())
	start := "1:none"
	// below the limit → increments, keeps resuming
	run := buildRun("b", "L", 1, 1, 777)
	run.StartFingerprint = &start
	d := e.planResume(run, nil, retryableCause())
	if d == nil || d.nextStreak != 2 {
		t.Fatalf("stable fingerprint must bump the streak to 2, got %+v", d)
	}
	// at the limit → park
	run.NoProgressStreak = 2 // next would be 3 == noProgressLimit
	if d := e.planResume(run, nil, retryableCause()); d != nil {
		t.Fatalf("at noProgressLimit the build must park, got %+v", d)
	}
}

// AC 2.2 — an advancing fingerprint resets the streak and resumes at base backoff.
func TestBreaker_ProgressResetsStreak(t *testing.T) {
	e := newEngineFP(constFP("2:3.qa"), testCfg()) // end fingerprint differs from start
	start := "1:none"
	run := buildRun("b", "L", 1, 2, 777)
	run.StartFingerprint = &start
	d := e.planResume(run, nil, retryableCause())
	if d == nil || d.nextStreak != 0 || d.delayMs != 5000 {
		t.Fatalf("advancing fingerprint must reset the streak to 0 at base backoff, got %+v", d)
	}
}

// AC 2.3 — an unknown (nil) fingerprint at start OR finalize is neutral: streak unchanged, no park.
func TestBreaker_UnknownFingerprintIsNeutral(t *testing.T) {
	// nil at finalize
	e := newEngineFP(func(domain.Run) *string { return nil }, testCfg())
	start := "1:none"
	run := buildRun("b", "L", 1, 2, 777)
	run.StartFingerprint = &start
	d := e.planResume(run, nil, retryableCause())
	if d == nil || d.nextStreak != 2 {
		t.Fatalf("nil end fingerprint must be neutral (streak unchanged at 2), got %+v", d)
	}
	// nil at start (even with a known end) is neutral too
	e2 := newEngineFP(constFP("5:none"), testCfg())
	run2 := buildRun("b", "L", 1, 2, 777) // StartFingerprint nil
	d2 := e2.planResume(run2, nil, retryableCause())
	if d2 == nil || d2.nextStreak != 2 {
		t.Fatalf("nil start fingerprint must be neutral (streak unchanged at 2), got %+v", d2)
	}
}

// --- AC 5.1–5.5 · auto-push-on-complete ---

type spyPusher struct {
	mu     sync.Mutex
	calls  int
	result PushResult
}

func (p *spyPusher) Push(string) PushResult { p.mu.Lock(); p.calls++; p.mu.Unlock(); return p.result }
func (p *spyPusher) count() int             { p.mu.Lock(); defer p.mu.Unlock(); return p.calls }

func newPushEngine(st RunStore, p Pusher, cfg BuildConfig) *BuildEngineService {
	return NewBuildEngineService(&countingSpawner{}, &spyJournal{writable: true}, st, fixedClock{}, &seqID{}, &spyKiller{}, stubOutput{}, nil, (&recordingSleeper{}).sleep, p, cfg)
}

func waitCalls(t *testing.T, p *spyPusher, want int) {
	t.Helper()
	for i := 0; i < 500; i++ {
		if p.count() == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("pusher calls: want %d, got %d", want, p.count())
}

func completeBuild(st RunStore) domain.Run {
	cwd := "/repo"
	run := buildRun("b", "L", 0, 0, 777)
	run.Cwd = &cwd
	st.Put(run)
	return run
}

// AC 5.1 — a build finalizing with a line-anchored PRD_BUILD_COMPLETE pushes once + stamps pushedAt.
func TestAutoPush_CompletePushesOnceAndStamps(t *testing.T) {
	st := newMemStore()
	p := &spyPusher{result: PushResult{OK: true, Branch: "feat"}}
	e := newPushEngine(st, p, testCfg())
	run := completeBuild(st)
	e.maybePushOnComplete(run, []ParsedLine{resultLine(t, "PRD_BUILD_COMPLETE")})
	got, _ := st.Get("b")
	if got.PushedAt == nil {
		t.Fatal("a successful build must stamp pushedAt")
	}
	waitCalls(t, p, 1)
}

// AC 5.2 — a non-complete finalize does not push; a re-finalize (pushedAt already set) pushes 0 times.
func TestAutoPush_NoCompleteAndOnceGuard(t *testing.T) {
	st := newMemStore()
	p := &spyPusher{result: PushResult{OK: true}}
	e := newPushEngine(st, p, testCfg())
	run := completeBuild(st)

	e.maybePushOnComplete(run, []ParsedLine{resultLine(t, "BLOCKED: gave up")})
	if got, _ := st.Get("b"); got.PushedAt != nil {
		t.Fatal("a non-complete build must not push / stamp")
	}
	// double-call with a complete build → exactly one push (atomic pushedAt guard)
	complete := []ParsedLine{resultLine(t, "PRD_BUILD_COMPLETE")}
	e.maybePushOnComplete(run, complete)
	e.maybePushOnComplete(run, complete)
	waitCalls(t, p, 1)
	if p.count() != 1 {
		t.Fatalf("a re-finalize must never double-push, got %d", p.count())
	}
}

// AC 5.5 — SAKI_BUILD_AUTOPUSH=0 (AutoPush false) → no push fires.
func TestAutoPush_DisabledByConfig(t *testing.T) {
	st := newMemStore()
	p := &spyPusher{result: PushResult{OK: true}}
	cfg := testCfg()
	cfg.AutoPush = false
	e := newPushEngine(st, p, cfg)
	run := completeBuild(st)
	e.maybePushOnComplete(run, []ParsedLine{resultLine(t, "PRD_BUILD_COMPLETE")})
	if got, _ := st.Get("b"); got.PushedAt != nil {
		t.Fatal("autoPush disabled must not push / stamp")
	}
	if p.count() != 0 {
		t.Fatalf("no push when disabled, got %d", p.count())
	}
}

func TestPushResultLine(t *testing.T) {
	if s := pushResultLine(PushResult{OK: true, Branch: "feat"}); s != "pushed feat to origin ✓" {
		t.Fatalf("ok line: %q", s)
	}
	if s := pushResultLine(PushResult{Skipped: "no remote"}); s != "push skipped: no remote" {
		t.Fatalf("skip line: %q", s)
	}
	if s := pushResultLine(PushResult{Error: "rejected"}); s != "push failed: rejected" {
		t.Fatalf("fail line: %q", s)
	}
}

// --- AC 4.1–4.4 · reclassify: usage-limit / auth / decision ---

func exit0Cause() domain.RedriveCause {
	return domain.RedriveCause{Kind: domain.CauseExit, ExitCode: ptrInt(0)}
}

func TestDetectLimit(t *testing.T) {
	if s := DetectLimit([]ParsedLine{resultLine(t, "you've hit your session limit · resets 3:45pm")}, 0); s == nil || s.Kind != "usage-limit" {
		t.Fatalf("usage cap must be detected from text, got %+v", s)
	}
	if s := DetectLimit([]ParsedLine{resultLine(t, "authentication_error: invalid api key")}, 0); s == nil || s.Kind != "auth-error" {
		t.Fatalf("auth error must be detected, got %+v", s)
	}
	// auth is checked first (not transient)
	if s := DetectLimit([]ParsedLine{rawLine("you've hit your usage limit"), rawLine("please run /login")}, 0); s == nil || s.Kind != "auth-error" {
		t.Fatalf("auth must win over usage, got %+v", s)
	}
	// a narrated usage phrase mid-sentence must NOT match (line-anchored usage regex)
	if s := DetectLimit([]ParsedLine{resultLine(t, "the banner shows when you have hit your account limit")}, 0); s != nil {
		t.Fatalf("narrated usage phrase must not trip a cap, got %+v", s)
	}
	if s := DetectLimit([]ParsedLine{resultLine(t, "all good, continuing")}, 0); s != nil {
		t.Fatalf("benign output must not detect a limit, got %+v", s)
	}
}

func TestClampDelay(t *testing.T) {
	base, lim := int64(5000), int64(3600000)
	if got := clampDelay(600000, 0, base, lim); got != 600000 { // 10min, in range
		t.Fatalf("in-range delay: got %d", got)
	}
	if got := clampDelay(7200000, 0, base, lim); got != lim { // 2h → clamped to 1h
		t.Fatalf("far reset must clamp to limitWait, got %d", got)
	}
	if got := clampDelay(1000, 0, base, lim); got != base { // 1s → clamped up to base
		t.Fatalf("near reset must clamp up to base, got %d", got)
	}
}

// AC 4.1 + 4.2 — a usage cap waits (limitWaitMs when the reset is unparseable), pushes firstStartedAt
// forward, and does NOT penalise the no-progress streak.
func TestReclassify_UsageLimitWaitsPreservesStreakPushesClock(t *testing.T) {
	cfg := testCfg()
	e := newEngineWith(&countingSpawner{}, newMemStore(), (&recordingSleeper{}).sleep, stubOutput{}, cfg)
	run := buildRun("b", "L", 1, 2, 777)
	lines := []ParsedLine{resultLine(t, "you've hit your session limit")} // no parseable reset
	d := e.planResume(run, lines, exit0Cause())
	if d == nil {
		t.Fatal("a usage cap must schedule a wait, not park")
	}
	if d.delayMs != cfg.LimitWaitMs { // AC 4.2 — unparseable reset → limitWaitMs
		t.Fatalf("no-reset wait must be limitWaitMs, got %d", d.delayMs)
	}
	if d.nextStreak != 2 { // AC 4.1 — cap is not a no-progress failure
		t.Fatalf("a usage cap must not penalise the streak, got %d", d.nextStreak)
	}
	if d.firstStartedAt != 777+cfg.LimitWaitMs { // AC 4.1 — idle wait not charged to the wall budget
		t.Fatalf("firstStartedAt must be pushed forward by the wait, got %d", d.firstStartedAt)
	}
}

// AC 4.3 — an auth error parks (a human must act), never auto-resumes.
func TestReclassify_AuthErrorParks(t *testing.T) {
	e := newEngineWith(&countingSpawner{}, newMemStore(), (&recordingSleeper{}).sleep, stubOutput{}, testCfg())
	lines := []ParsedLine{resultLine(t, "authentication_error — please run /login")}
	if d := e.planResume(buildRun("b", "L", 1, 0, 777), lines, exit0Cause()); d != nil {
		t.Fatalf("an auth error must park, got %+v", d)
	}
}

// AC 4.4 — a parseable NEEDS_DECISION awaits (no resume); a malformed one degrades to retryable.
func TestReclassify_DecisionAwaitingElseRetryable(t *testing.T) {
	e := newEngineWith(&countingSpawner{}, newMemStore(), (&recordingSleeper{}).sleep, stubOutput{}, testCfg())
	awaiting := []ParsedLine{resultLine(t, `NEEDS_DECISION: {"question":"which store?","options":["a","b"]}`)}
	if d := e.planResume(buildRun("b", "L", 0, 0, 777), awaiting, exit0Cause()); d != nil {
		t.Fatalf("a parseable decision must await (no resume), got %+v", d)
	}
	malformed := []ParsedLine{resultLine(t, "NEEDS_DECISION: {broken")}
	if d := e.planResume(buildRun("b", "L", 0, 0, 777), malformed, exit0Cause()); d == nil {
		t.Fatal("a malformed decision must degrade to retryable (resume), not wedge on awaiting")
	}
}

// --- AC 3.1–3.4 · stall watchdog + viewerless activity ---

func newStallEngine(st RunStore, k ProcessKiller, out FullOutput, clk Clock, cfg BuildConfig) *BuildEngineService {
	return NewBuildEngineService(&countingSpawner{}, &spyJournal{writable: true}, st, clk, &seqID{}, k, out, nil, (&recordingSleeper{}).sleep, nil, cfg)
}

func putStallRun(st RunStore, pid int) {
	run := buildRun("b", "L", 0, 0, 0)
	run.Pid = &pid
	run.LastActivityAt = 0
	run.LastOutSize = 0
	st.Put(run)
}

// AC 3.1 — silent ≥ stallMs while alive → SIGTERM; still alive after the grace → SIGKILL.
func TestStallWatchdog_SigtermThenSigkill(t *testing.T) {
	st := newMemStore()
	k := &spyKiller{alive: true}
	clk := &mutClock{}
	cfg := testCfg()
	e := newStallEngine(st, k, stubOutput{size: 0}, clk, cfg)
	putStallRun(st, 4242)

	clk.v = cfg.StallMs + 1 // no activity since t=0, now past the stall window
	e.sweepStalls()
	if k.signals != 1 || k.lastPid != 4242 {
		t.Fatalf("a silent alive build must be SIGTERM'd, got signals=%d pid=%d", k.signals, k.lastPid)
	}
	got, _ := st.Get("b")
	if got.StallKilledAt == nil {
		t.Fatal("stallKilledAt must be stamped on the SIGTERM")
	}
	clk.v = *got.StallKilledAt + stallSigkillGraceMs + 1 // still alive after the grace
	e.sweepStalls()
	if k.kills != 1 {
		t.Fatalf("a build that ignored SIGTERM must be SIGKILL'd after the grace, got kills=%d", k.kills)
	}
}

// AC 3.2 — a build producing output (file growth) is NOT killed, even with no SSE viewer.
func TestStallWatchdog_ViewerlessActivityNotKilled(t *testing.T) {
	st := newMemStore()
	k := &spyKiller{alive: true}
	clk := &mutClock{}
	cfg := testCfg()
	e := newStallEngine(st, k, stubOutput{size: 500}, clk, cfg) // <id>.out grew to 500 bytes
	putStallRun(st, 4242)

	clk.v = cfg.StallMs + 1 // would be past the stall window IF there were no activity
	e.sweepStalls()
	if k.signals != 0 || k.kills != 0 {
		t.Fatalf("a healthy growing build must never be killed (viewerless), got signals=%d kills=%d", k.signals, k.kills)
	}
	got, _ := st.Get("b")
	if got.LastOutSize != 500 || got.LastActivityAt != clk.v {
		t.Fatalf("file growth must register activity, got size=%d lastActivity=%d", got.LastOutSize, got.LastActivityAt)
	}
}

// AC 3.4 — SAKI_BUILD_STALL_MS=0 disables the watchdog.
func TestStallWatchdog_DisabledWhenZero(t *testing.T) {
	st := newMemStore()
	k := &spyKiller{alive: true}
	cfg := testCfg()
	cfg.StallMs = 0
	e := newStallEngine(st, k, stubOutput{size: 0}, &mutClock{v: 1 << 40}, cfg)
	putStallRun(st, 4242)
	e.sweepStalls()
	if k.signals != 0 || k.kills != 0 {
		t.Fatalf("stallMs==0 must disable the watchdog, got signals=%d kills=%d", k.signals, k.kills)
	}
}

// AC 3.3 — a dead-without-exit build (onExit with the absent-exit code -1) resumes (interrupted →
// retryable), not a spurious done. In the Go model the per-run wait() goroutine delivers this.
func TestDeadWithoutExit_Retryable(t *testing.T) {
	st := newMemStore()
	sl := &recordingSleeper{}
	e := NewBuildEngineService(&countingSpawner{}, &spyJournal{writable: true}, st, fixedClock{}, &seqID{}, &spyKiller{}, stubOutput{}, nil, sl.sleep, nil, testCfg())
	st.Put(buildRun("b", "L", 0, 0, 777))
	e.onExit("b", -1) // absent .exit → readExitCode returns -1
	got, _ := st.Get("b")
	if got.Status == domain.StatusDone {
		t.Fatal("a dead-without-exit build must not be marked done")
	}
	if got.PendingResume == nil {
		t.Fatal("a dead-without-exit build must schedule a retryable resume")
	}
}

// --- AC 1.3 · BR3 dedup + fire-once + spawn-fail (REAL concurrency, run under -race) ---

func TestSpawnBuild_ConcurrentSameLaneDedupes(t *testing.T) {
	st := newMemStore()
	sp := &countingSpawner{}
	e := newEngineWith(sp, st, (&recordingSleeper{}).sleep, stubOutput{}, testCfg())
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = e.SpawnBuild(BuildSpawnReq{Prompt: "/build", Meta: &domain.Meta{Kind: "build", LaneKey: "L"}})
		}()
	}
	wg.Wait()
	if sp.count() != 1 {
		t.Fatalf("concurrent build POSTs for one lane must spawn exactly once, got %d", sp.count())
	}
	running := 0
	for _, r := range st.List() {
		if r.Status == domain.StatusRunning {
			running++
		}
	}
	if running != 1 {
		t.Fatalf("exactly one live build for the lane, got %d", running)
	}
}

func TestFirePendingResume_FireOnceUnderConcurrency(t *testing.T) {
	st := newMemStore()
	sp := &countingSpawner{}
	e := newEngineWith(sp, st, (&recordingSleeper{}).sleep, stubOutput{}, testCfg())
	// a finalized predecessor carrying a due pending resume
	pred := buildRun("pred", "L", 0, 0, 777)
	pred.Status = domain.StatusError
	pred.PendingResume = &domain.PendingResume{SuccessorID: "succ", ResumeAt: 0, ResumeCount: 1, FirstStartedAt: 777}
	st.Put(pred)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		// half fire directly, half via the Sweep — both must not double-spawn the successor
		go func(viaSweep bool) {
			defer wg.Done()
			if viaSweep {
				e.Sweep()
			} else {
				e.firePendingResume("pred")
			}
		}(i%2 == 0)
	}
	wg.Wait()
	if sp.count() != 1 {
		t.Fatalf("a scheduled resume must fire exactly once, got %d spawns", sp.count())
	}
	if _, ok := st.Get("succ"); !ok {
		t.Fatal("the single successor must have been spawned")
	}
}

func TestFirePendingResume_SpawnThrowReArmsThenParks(t *testing.T) {
	st := newMemStore()
	sp := &countingSpawner{err: errors.New("EAGAIN")}
	e := newEngineWith(sp, st, (&recordingSleeper{}).sleep, stubOutput{}, testCfg())
	pred := buildRun("pred", "L", 0, 0, 777)
	pred.Status = domain.StatusError
	pred.PendingResume = &domain.PendingResume{SuccessorID: "succ", ResumeAt: 0, ResumeCount: 1, FirstStartedAt: 777}
	st.Put(pred)

	// Each fire clears + retries the throwing spawn; bounded re-arm (≤3) then park.
	for i := 0; i < maxSpawnRetries; i++ {
		e.firePendingResume("pred")
		if r, _ := st.Get("pred"); r.PendingResume == nil {
			t.Fatalf("attempt %d: must re-arm (pendingResume kept) while under the retry cap", i+1)
		}
	}
	e.firePendingResume("pred") // the (maxSpawnRetries+1)th attempt → park
	if r, _ := st.Get("pred"); r.PendingResume != nil {
		t.Fatal("after the bounded re-arms the resume must park (pendingResume cleared), not tight-loop")
	}
}

// --- AC 1.4 · BR2 stop-cancels-resume + the recovery summary ---

func TestStop_RunningBuild_NoResume(t *testing.T) {
	st := newMemStore()
	sp := &countingSpawner{}
	k := &spyKiller{}
	e := NewBuildEngineService(sp, &spyJournal{writable: true}, st, fixedClock{}, &seqID{}, k, stubOutput{}, nil, (&recordingSleeper{}).sleep, nil, testCfg())
	run := buildRun("b", "L", 0, 0, 777)
	pid := 4242
	run.Pid = &pid
	st.Put(run)

	if !e.Stop("b") {
		t.Fatal("stop of a running build must return true")
	}
	if k.signals != 1 {
		t.Fatal("stop must signal the process group")
	}
	// onExit racing in from the killed process must NOT schedule a resume (finalize already transitioned).
	e.onExit("b", 143)
	got, _ := st.Get("b")
	if got.PendingResume != nil {
		t.Fatal("a stopped running build must never auto-resume")
	}
	if sp.count() != 0 {
		t.Fatalf("no successor may spawn for a stopped build, got %d", sp.count())
	}
}

// BR2 (Stop-vs-natural-exit race): a resume must never arm for a build the operator stopped, even if
// onExit wins the finalize and reaches scheduleResume.
func TestScheduleResume_SkipsWhenStopRequested(t *testing.T) {
	st := newMemStore()
	sl := &recordingSleeper{}
	e := newEngineWith(&countingSpawner{}, st, sl.sleep, stubOutput{}, testCfg())
	run := buildRun("b", "L", 0, 0, 777)
	run.Status = domain.StatusError
	run.StopRequested = true // the operator's Stop already flagged it
	st.Put(run)
	e.scheduleResume(run, resumeDecision{successorID: "s", delayMs: 5000, nextResumeCount: 1, firstStartedAt: 777})
	got, _ := st.Get("b")
	if got.PendingResume != nil {
		t.Fatal("a stopped build must not arm a resume even if onExit reaches scheduleResume")
	}
	if len(sl.delays) != 0 {
		t.Fatalf("no backoff timer may be armed for a stopped build, got %v", sl.delays)
	}
}

// BR2: Stop of a running build clears a resume a concurrent onExit may have already armed, and the
// subsequent sweep/fire finds nothing to spawn.
func TestStop_RunningClearsArmedResume(t *testing.T) {
	st := newMemStore()
	sp := &countingSpawner{}
	e := newEngineWith(sp, st, (&recordingSleeper{}).sleep, stubOutput{}, testCfg())
	run := buildRun("b", "L", 0, 0, 777)
	pid := 4242
	run.Pid = &pid
	run.PendingResume = &domain.PendingResume{SuccessorID: "succ", ResumeAt: 0, ResumeCount: 1, FirstStartedAt: 777}
	st.Put(run)

	if !e.Stop("b") {
		t.Fatal("stop must return true")
	}
	got, _ := st.Get("b")
	if got.PendingResume != nil {
		t.Fatal("stop of a running build must clear an armed resume")
	}
	e.Sweep()
	e.firePendingResume("b")
	if sp.count() != 0 {
		t.Fatalf("a stopped build's cleared resume must never spawn, got %d", sp.count())
	}
}

func TestFirePendingResume_SkipsStoppedChain(t *testing.T) {
	st := newMemStore()
	sp := &countingSpawner{}
	e := newEngineWith(sp, st, (&recordingSleeper{}).sleep, stubOutput{}, testCfg())
	pred := buildRun("pred", "L", 0, 0, 777)
	pred.Status = domain.StatusError
	pred.StopRequested = true
	pred.PendingResume = &domain.PendingResume{SuccessorID: "succ", ResumeAt: 0, ResumeCount: 1, FirstStartedAt: 777}
	st.Put(pred)
	e.firePendingResume("pred")
	if sp.count() != 0 {
		t.Fatalf("firePendingResume must not spawn a successor for a stopped chain, got %d", sp.count())
	}
}

func TestStop_RecoveringBuild_CancelsPendingResume(t *testing.T) {
	st := newMemStore()
	sp := &countingSpawner{}
	e := newEngineWith(sp, st, (&recordingSleeper{}).sleep, stubOutput{}, testCfg())
	run := buildRun("b", "L", 1, 0, 777)
	run.Status = domain.StatusError
	run.PendingResume = &domain.PendingResume{SuccessorID: "succ", ResumeAt: 777 + 60000, ResumeCount: 2, FirstStartedAt: 777}
	st.Put(run)

	if !e.Stop("b") {
		t.Fatal("stop of a recovering build must return true")
	}
	got, _ := st.Get("b")
	if got.PendingResume != nil {
		t.Fatal("stop must cancel a recovering build's pending resume (park, no successor)")
	}
	// a later sweep must find nothing to fire
	e.Sweep()
	if sp.count() != 0 {
		t.Fatalf("cancelled resume must not spawn, got %d", sp.count())
	}
}

func TestRunView_ReportsLiveRecoveryFields(t *testing.T) {
	st := newMemStore()
	// a recovering build
	rec := buildRun("rec", "L", 2, 1, 777)
	rec.Status = domain.StatusError
	rec.PendingResume = &domain.PendingResume{SuccessorID: "s", ResumeAt: 9999, ResumeCount: 3, FirstStartedAt: 777}
	st.Put(rec)
	// a parked build (finalized, no pending resume) whose durable output holds the park breadcrumb
	parked := buildRun("park", "M", 3, 3, 777)
	parked.Status = domain.StatusError
	st.Put(parked)

	out := stubOutput{lines: []ParsedLine{rawLine("BLOCKED: no verified progress after 3 auto-resumes — parked for operator")}}
	views := NewListService(st, stubProxy{}, out).List("", "")
	byID := map[string]map[string]any{}
	for _, v := range views {
		byID[v["id"].(string)] = v
	}
	if byID["rec"]["recovering"] != true || byID["rec"]["resumeAt"] != int64(9999) || byID["rec"]["resumeCount"] != 2 {
		t.Fatalf("recovering view wrong: %+v", byID["rec"])
	}
	if _, ok := byID["park"]["parkedReason"]; !ok {
		t.Fatalf("a finalized build must surface its parkedReason, got %+v", byID["park"])
	}
}

// --- E26 9.1.4 / 9.1.5: resume carries the engine; a missing engine binary parks immediately ---

// engineRecSpawner records each Spawn's engine so a test can assert the successor carried it.
type engineRecSpawner struct {
	engines []domain.RunEngine
	err     error
}

func (s *engineRecSpawner) Spawn(spec SpawnSpec) (int, func() int, error) {
	s.engines = append(s.engines, spec.Engine)
	if s.err != nil {
		return 0, nil, s.err
	}
	return 100, func() int { select {} }, nil
}

func TestResume_CarriesEngine(t *testing.T) {
	st := newMemStore()
	sp := &engineRecSpawner{}
	e := newEngineWith(sp, st, (&recordingSleeper{}).sleep, stubOutput{}, testCfg())
	pred := buildRun("pred", "L", 0, 0, 777)
	pred.Engine = domain.EngineOpencode
	pred.Status = domain.StatusError
	pred.PendingResume = &domain.PendingResume{SuccessorID: "succ", ResumeAt: 0, ResumeCount: 1, FirstStartedAt: 777}
	st.Put(pred)

	e.firePendingResume("pred")
	if len(sp.engines) != 1 {
		t.Fatalf("expected exactly one successor spawn, got %d", len(sp.engines))
	}
	if sp.engines[0] != domain.EngineOpencode {
		t.Fatalf("successor must carry the predecessor's engine (opencode), got %q", sp.engines[0])
	}
	if succ, ok := st.Get("succ"); !ok || succ.Engine != domain.EngineOpencode {
		t.Fatalf("successor run must carry engine=opencode, got %+v", succ)
	}
}

// breadcrumbJournal records AppendOut calls so a test can assert a durable park reason reached .out.
type breadcrumbJournal struct {
	spyJournal
	lines []string
}

func (j *breadcrumbJournal) AppendOut(_ string, line string) error { j.lines = append(j.lines, line); return nil }

func TestResume_BinaryNotFoundParksImmediately(t *testing.T) {
	st := newMemStore()
	sp := &engineRecSpawner{err: ErrBinaryNotFound}
	j := &breadcrumbJournal{spyJournal: spyJournal{writable: true}}
	e := NewBuildEngineService(sp, j, st, fixedClock{}, &seqID{}, &spyKiller{}, stubOutput{}, nil, (&recordingSleeper{}).sleep, nil, testCfg())
	pred := buildRun("pred", "L", 0, 0, 777)
	pred.Engine = domain.EngineOpencode
	pred.Status = domain.StatusError
	pred.PendingResume = &domain.PendingResume{SuccessorID: "succ", ResumeAt: 0, ResumeCount: 1, FirstStartedAt: 777}
	st.Put(pred)

	// A missing engine binary is permanent — fire ONCE and the resume must park (cleared), no re-arm,
	// AND leave a durable breadcrumb on the predecessor's .out (the UI's park-reason source).
	e.firePendingResume("pred")
	if r, _ := st.Get("pred"); r.PendingResume != nil {
		t.Fatal("a missing engine binary must park immediately (pendingResume cleared), not re-arm")
	}
	if len(sp.engines) != 1 {
		t.Fatalf("exactly one spawn attempt expected, got %d", len(sp.engines))
	}
	if len(j.lines) == 0 || !strings.Contains(j.lines[len(j.lines)-1], "engine binary not found") {
		t.Fatalf("a durable binary-not-found park reason must reach the predecessor's .out, got %v", j.lines)
	}
}

// --- E26 slice 5: 9.5.4 (claude resume byte-identical), 9.5.5 (dedup surfaces in-flight engine),
//     9.5.2 (the run chain carries engine) ---

// 9.5.4 — a claude build's auto-resume successor spawns with the claude engine (its command stays
// claude — the byte-identical command itself is asserted by TestBuildRunScript_ClaudeDefaultByteIdentical).
func TestResume_ClaudeDefaultByteIdentical(t *testing.T) {
	st := newMemStore()
	sp := &engineRecSpawner{}
	e := newEngineWith(sp, st, (&recordingSleeper{}).sleep, stubOutput{}, testCfg())
	pred := buildRun("pred", "L", 0, 0, 777)
	pred.Engine = domain.EngineClaude
	pred.Status = domain.StatusError
	pred.PendingResume = &domain.PendingResume{SuccessorID: "succ", ResumeAt: 0, ResumeCount: 1, FirstStartedAt: 777}
	st.Put(pred)

	e.firePendingResume("pred")
	if len(sp.engines) != 1 || sp.engines[0] != domain.EngineClaude {
		t.Fatalf("a claude build's successor must stay on claude, got %v", sp.engines)
	}
}

// 9.5.5 — a lane-deduped launch whose requested engine differs from the in-flight run re-adopts the
// in-flight run and surfaces ITS engine (never a silent re-selection).
func TestReserveBuild_DedupSurfacesInFlightEngine(t *testing.T) {
	st := newMemStore()
	sp := &engineRecSpawner{}
	e := newEngineWith(sp, st, (&recordingSleeper{}).sleep, stubOutput{}, testCfg())
	inFlight := buildRun("live", "L", 0, 0, 777)
	inFlight.Engine = domain.EngineClaude // an opencode launch must NOT switch an existing claude build
	st.Put(inFlight)

	run, err := e.SpawnBuild(BuildSpawnReq{Prompt: "/build", Meta: &domain.Meta{Kind: "build", LaneKey: "L"}, Engine: domain.EngineOpencode})
	if err != nil {
		t.Fatal(err)
	}
	if run.ID != "live" {
		t.Fatalf("the deduped launch must re-adopt the in-flight run, got %s", run.ID)
	}
	if run.Engine != domain.EngineClaude {
		t.Fatalf("the surfaced run must carry the IN-FLIGHT engine (claude), got %q", run.Engine)
	}
	if len(sp.engines) != 0 {
		t.Fatalf("a deduped launch must spawn nothing, got %d spawn(s)", len(sp.engines))
	}
}

// 9.5.2 — the full run chain (spawn → resume successor) carries engine=opencode end to end.
func TestResume_ChainCarriesEngine(t *testing.T) {
	st := newMemStore()
	sp := &engineRecSpawner{}
	e := newEngineWith(sp, st, (&recordingSleeper{}).sleep, stubOutput{}, testCfg())
	first, err := e.SpawnBuild(BuildSpawnReq{Prompt: "/build", Meta: &domain.Meta{Kind: "build", LaneKey: "L"}, Engine: domain.EngineOpencode})
	if err != nil {
		t.Fatal(err)
	}
	if first.Engine != domain.EngineOpencode {
		t.Fatalf("the first chain run must carry opencode, got %q", first.Engine)
	}
	// Drive the successor via a pending resume on the first run (the engine inherits the predecessor).
	first.Status = domain.StatusError
	first.PendingResume = &domain.PendingResume{SuccessorID: "succ", ResumeAt: 0, ResumeCount: 1, FirstStartedAt: first.FirstStartedAt}
	st.Put(first)
	e.firePendingResume(first.ID)
	succ, ok := st.Get("succ")
	if !ok {
		t.Fatal("the successor must have spawned")
	}
	if succ.Engine != domain.EngineOpencode {
		t.Fatalf("the chain successor must carry opencode, got %q", succ.Engine)
	}
}
