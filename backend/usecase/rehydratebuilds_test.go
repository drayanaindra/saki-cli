package usecase

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

// stubExitWatcher reports a fixed terminal/liveness fact for the survived-build watch.
type stubExitWatcher struct {
	exit    *int
	hasExit bool
	alive   bool
}

func (w stubExitWatcher) Terminal(string, *int) (*int, bool, bool) { return w.exit, w.hasExit, w.alive }

// mutExitWatcher is a settable ExitWatcher — a survived child that starts alive and later exits, so a
// test can drive the re-attach → exit → engine-routing path.
type mutExitWatcher struct {
	mu      sync.Mutex
	exit    *int
	hasExit bool
	alive   bool
}

func (w *mutExitWatcher) Terminal(string, *int) (*int, bool, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.exit, w.hasExit, w.alive
}
func (w *mutExitWatcher) set(exit *int, hasExit, alive bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.exit, w.hasExit, w.alive = exit, hasExit, alive
}

// --- AC 6.4 · idempotency (in-process + rebuilt-from-journal across a restart) ---

// A build POST carrying an already-seen idempotency key returns the SAME run and spawns nothing — even
// on a different lane (proving it's the key, not the lane re-adopt, that dedupes).
func TestIdempotency_SameKeyDifferentLaneDedupes(t *testing.T) {
	st := newMemStore()
	sp := &countingSpawner{}
	e := newEngineWith(sp, st, (&recordingSleeper{}).sleep, stubOutput{}, testCfg())
	key := "k"
	r1, err := e.SpawnBuild(BuildSpawnReq{Prompt: "/build", Meta: &domain.Meta{Kind: "build", LaneKey: "L"}, IdempotencyKey: &key})
	if err != nil {
		t.Fatal(err)
	}
	r2, _ := e.SpawnBuild(BuildSpawnReq{Prompt: "/build", Meta: &domain.Meta{Kind: "build", LaneKey: "M"}, IdempotencyKey: &key})
	if r2.ID != r1.ID {
		t.Fatalf("same idempotency key must return the same run id, got %s vs %s", r2.ID, r1.ID)
	}
	if sp.count() != 1 {
		t.Fatalf("same idempotency key must spawn exactly once (even across lanes), got %d", sp.count())
	}
}

// AC 6.4 — the idempotency map is rebuilt from the journals on boot, so a client retry carrying a
// PERSISTED key after a restart returns the existing runId and spawns no second process.
func TestIdempotency_RebuiltFromJournalDedupesPostRestart(t *testing.T) {
	st := newMemStore()
	sp := &countingSpawner{}
	e := newEngineWith(sp, st, (&recordingSleeper{}).sleep, stubOutput{}, testCfg())
	key := "k"
	// a build that finished before the restart, carrying its key, already Put back by generic Rehydrate
	run := buildRun("b", "L", 0, 0, 777)
	run.Status = domain.StatusDone
	run.IdempotencyKey = &key
	st.Put(run)

	e.RehydrateBuilds(context.Background(), stubReader{recs: []RehydratedRun{{Run: run}}}, stubExitWatcher{})

	got, err := e.SpawnBuild(BuildSpawnReq{Prompt: "/build", Meta: &domain.Meta{Kind: "build", LaneKey: "L"}, IdempotencyKey: &key})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "b" {
		t.Fatalf("a post-restart retry with a persisted key must return the existing run id, got %s", got.ID)
	}
	if sp.count() != 0 {
		t.Fatalf("a post-restart idempotent retry must spawn nothing, got %d", sp.count())
	}
}

// --- AC 6.1 · restart routing (exactly-one-live) ---

// A dead-without-exit survived build is interrupted → resumes (never left running, never a spurious done).
func TestRehydrateBuilds_DeadWithoutExitResumes(t *testing.T) {
	st := newMemStore()
	sp := &countingSpawner{}
	e := newEngineWith(sp, st, (&recordingSleeper{}).sleep, stubOutput{}, testCfg())
	run := buildRun("b", "L", 0, 0, 777) // running, pid nil, no .exit
	st.Put(run)

	e.RehydrateBuilds(context.Background(), stubReader{recs: []RehydratedRun{{Run: run}}}, stubExitWatcher{})

	got, _ := st.Get("b")
	if got.Status == domain.StatusRunning {
		t.Fatal("a dead-without-exit survived build must be finalized, not left running")
	}
	if got.PendingResume == nil {
		t.Fatal("a dead-without-exit survived build must schedule a retryable resume")
	}
}

// AC 6.1 — a survived build whose child is STILL ALIVE is re-attached (kept running, never re-spawned);
// its real exit is later routed through the engine by the watch goroutine.
func TestRehydrateBuilds_AliveReattachesNoRespawn(t *testing.T) {
	st := newMemStore()
	sp := &countingSpawner{}
	e := newEngineWith(sp, st, (&recordingSleeper{}).sleep, stubOutput{}, testCfg())
	pid := 4242
	run := buildRun("b", "L", 0, 0, 777)
	run.Pid = &pid
	st.Put(run)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // stop the watch goroutine at test end

	e.RehydrateBuilds(ctx, stubReader{recs: []RehydratedRun{{Run: run, PidAlive: true}}}, stubExitWatcher{alive: true})

	got, _ := st.Get("b")
	if got.Status != domain.StatusRunning {
		t.Fatalf("an alive survived build must stay running (re-attached), got %s", got.Status)
	}
	if sp.count() != 0 {
		t.Fatalf("re-attach must never re-spawn, got %d", sp.count())
	}
}

// AC 6.1 (routing) — a re-attached alive build that later EXITS successfully is routed through the
// engine by the watch goroutine (finalized done + auto-pushed), never plain-reaped.
func TestRehydrateBuilds_AliveThenExitCompletesAndPushes(t *testing.T) {
	st := newMemStore()
	p := &spyPusher{result: PushResult{OK: true, Branch: "feat"}}
	cwd := "/repo"
	pid := 4242
	run := buildRun("b", "L", 0, 0, 777)
	run.Cwd = &cwd
	run.Pid = &pid
	st.Put(run)
	out := stubOutput{lines: []ParsedLine{resultLine(t, "PRD_BUILD_COMPLETE")}}
	e := NewBuildEngineService(&countingSpawner{}, &spyJournal{writable: true}, st, fixedClock{}, &seqID{}, &spyKiller{}, out, nil, (&recordingSleeper{}).sleep, p, testCfg())
	w := &mutExitWatcher{alive: true}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	e.RehydrateBuilds(ctx, stubReader{recs: []RehydratedRun{{Run: run, PidAlive: true}}}, w)
	if got, _ := st.Get("b"); got.Status != domain.StatusRunning {
		t.Fatalf("must re-attach as running, got %s", got.Status)
	}

	// the survived child now exits successfully → the ~500ms watch poll must route it through the engine
	code0 := 0
	w.set(&code0, true, false)
	for i := 0; i < 3000; i++ { // up to ~3s (the poll ticks every 500ms)
		if got, _ := st.Get("b"); got.Status == domain.StatusDone && p.count() == 1 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	got, _ := st.Get("b")
	t.Fatalf("a re-attached build that exits 0 must finalize done + auto-push once, got status=%s pushes=%d", got.Status, p.count())
}

// --- AC 6.3 · auto-push + status-guard across a restart re-finalize ---

func newSurvivedPushEngine(st RunStore, p Pusher, out FullOutput) *BuildEngineService {
	return NewBuildEngineService(&countingSpawner{}, &spyJournal{writable: true}, st, fixedClock{}, &seqID{}, &spyKiller{}, out, nil, (&recordingSleeper{}).sleep, p, testCfg())
}

// A build that COMPLETED while the server was down (has .exit, output carries PRD_BUILD_COMPLETE) is
// finalized on boot and auto-pushed exactly once.
func TestRehydrateBuilds_SurvivedCompletePushesOnce(t *testing.T) {
	st := newMemStore()
	p := &spyPusher{result: PushResult{OK: true, Branch: "feat"}}
	cwd := "/repo"
	run := buildRun("b", "L", 0, 0, 777)
	run.Cwd = &cwd
	st.Put(run)
	out := stubOutput{lines: []ParsedLine{resultLine(t, "PRD_BUILD_COMPLETE")}}
	e := newSurvivedPushEngine(st, p, out)
	code0 := 0

	e.RehydrateBuilds(context.Background(), stubReader{recs: []RehydratedRun{{Run: run, HasExit: true, ExitCode: &code0}}}, stubExitWatcher{})

	waitCalls(t, p, 1)
	if got, _ := st.Get("b"); got.PushedAt == nil {
		t.Fatal("a survived-and-completed build must auto-push + stamp pushedAt")
	}
}

// AC 6.3 — a build ALREADY pushed before the restart (durable pushedAt) never double-pushes on the
// restart re-finalize.
func TestRehydrateBuilds_AlreadyPushedNoDoublePush(t *testing.T) {
	st := newMemStore()
	p := &spyPusher{result: PushResult{OK: true}}
	cwd := "/repo"
	pushedAt := int64(700)
	run := buildRun("b", "L", 0, 0, 777)
	run.Cwd = &cwd
	run.PushedAt = &pushedAt // durable: pushed before the restart
	st.Put(run)
	out := stubOutput{lines: []ParsedLine{resultLine(t, "PRD_BUILD_COMPLETE")}}
	e := newSurvivedPushEngine(st, p, out)
	code0 := 0

	e.RehydrateBuilds(context.Background(), stubReader{recs: []RehydratedRun{{Run: run, HasExit: true, ExitCode: &code0}}}, stubExitWatcher{})

	time.Sleep(10 * time.Millisecond) // give any (erroneous) fire-and-forget push a chance to land
	if p.count() != 0 {
		t.Fatalf("a build already pushed before the restart must not double-push, got %d", p.count())
	}
}

// AC 6.3 — a build already FINALIZED (terminal, no pending resume) is never re-resumed on restart.
func TestRehydrateBuilds_FinalizedNotResumed(t *testing.T) {
	st := newMemStore()
	sp := &countingSpawner{}
	e := newEngineWith(sp, st, (&recordingSleeper{}).sleep, stubOutput{}, testCfg())
	run := buildRun("b", "L", 5, 0, 777)
	run.Status = domain.StatusError // finalized before the restart, no pendingResume
	st.Put(run)

	e.RehydrateBuilds(context.Background(), stubReader{recs: []RehydratedRun{{Run: run}}}, stubExitWatcher{})

	got, _ := st.Get("b")
	if got.PendingResume != nil {
		t.Fatal("a finalized build must not be re-resumed on restart")
	}
	if sp.count() != 0 {
		t.Fatalf("a finalized build must spawn nothing, got %d", sp.count())
	}
}

// --- AC 6.2 · a recovering build's pending resume re-arms + fires under the same CLAUDE_CONFIG_DIR ---

func TestRehydrateBuilds_RecoveringResumesUnderSameConfigDir(t *testing.T) {
	st := newMemStore()
	sp := &spySpawner{} // captures the last spawn spec (its ConfigDir)
	e := newEngineWith(sp, st, (&recordingSleeper{}).sleep, stubOutput{}, testCfg())
	cfgDir := "/home/op/.claude-profileA"
	run := buildRun("b", "L", 1, 0, 777)
	run.Status = domain.StatusError
	run.ConfigDir = &cfgDir
	run.PendingResume = &domain.PendingResume{SuccessorID: "succ", ResumeAt: 0, ResumeCount: 2, FirstStartedAt: 777}
	st.Put(run)

	// boot: rehydrate leaves the recovering build for the Sweep (records idem, no finalize)
	e.RehydrateBuilds(context.Background(), stubReader{recs: []RehydratedRun{{Run: run}}}, stubExitWatcher{})
	e.Sweep() // the periodic tick fires the due pending resume

	if _, ok := st.Get("succ"); !ok {
		t.Fatal("a persisted pending resume must fire its successor on restart")
	}
	if sp.spec.ConfigDir == nil || *sp.spec.ConfigDir != cfgDir {
		t.Fatalf("the resumed build must re-spawn under the same CLAUDE_CONFIG_DIR profile, got %v", sp.spec.ConfigDir)
	}
	if sp.calls != 1 {
		t.Fatalf("exactly one successor spawn, got %d", sp.calls)
	}
}
