package usecase

import (
	"errors"
	"sync"
	"testing"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

type spySpawner struct {
	calls int
	spec  SpawnSpec
	err   error
}

func (s *spySpawner) Spawn(spec SpawnSpec) (int, func() int, error) {
	s.calls++
	s.spec = spec
	if s.err != nil {
		return 0, nil, s.err
	}
	return 99, func() int { select {} }, nil // wait blocks — the run stays "running" for the test
}

type spyJournal struct {
	writable bool
	writes   int
	writeErr error // when set, Write fails (EnsureWritable still passes) — for the durable-record-fail path
}

func (j *spyJournal) EnsureWritable() error {
	if !j.writable {
		return errors.New("ro")
	}
	return nil
}
func (j *spyJournal) Write(domain.Run) error         { j.writes++; return j.writeErr }
func (j *spyJournal) OutPath(id string) string       { return id + ".out" }
func (j *spyJournal) ExitPath(id string) string      { return id + ".exit" }
func (j *spyJournal) AppendOut(string, string) error { return nil }

// memStore is a thread-safe in-memory RunStore fake (StreamService reads it concurrently with a
// finalize, so it must lock — the real infra.MemStore does too).
type memStore struct {
	mu   sync.Mutex
	runs map[string]domain.Run
}

func newMemStore() *memStore         { return &memStore{runs: map[string]domain.Run{}} }
func (m *memStore) Put(r domain.Run) { m.mu.Lock(); m.runs[r.ID] = r; m.mu.Unlock() }
func (m *memStore) Get(id string) (domain.Run, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[id]
	return r, ok
}
func (m *memStore) List() []domain.Run {
	m.mu.Lock()
	defer m.mu.Unlock()
	o := []domain.Run{}
	for _, r := range m.runs {
		o = append(o, r)
	}
	return o
}
func (m *memStore) SetPid(id string, pid int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.runs[id]; ok {
		p := pid
		r.Pid = &p
		m.runs[id] = r
	}
}
func (m *memStore) Delete(id string) { m.mu.Lock(); delete(m.runs, id); m.mu.Unlock() }
func (m *memStore) Finalize(id string, code int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[id]
	if !ok || r.Status != domain.StatusRunning { // first-write-wins (parity)
		return false
	}
	c := code
	r.ExitCode = &c
	r.Status = domain.StatusDone
	if code != 0 {
		r.Status = domain.StatusError
	}
	m.runs[id] = r
	return true
}
func (m *memStore) ReserveBuild(laneKey string, run domain.Run) (string, bool) {
	return m.reserveKind("build", laneKey, run)
}
func (m *memStore) ReserveInit(laneKey string, run domain.Run) (string, bool) {
	return m.reserveKind("init", laneKey, run)
}
func (m *memStore) reserveKind(kind domain.RunKind, laneKey string, run domain.Run) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if laneKey != "" {
		for _, r := range m.runs {
			if r.Status == domain.StatusRunning && r.Meta != nil && r.Meta.Kind == kind && r.Meta.LaneKey == laneKey {
				return r.ID, false
			}
		}
	}
	m.runs[run.ID] = run
	return "", true
}
func (m *memStore) Update(id string, mut func(r *domain.Run)) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[id]
	if !ok {
		return false
	}
	mut(&r)
	m.runs[id] = r
	return true
}

type fixedClock struct{}

func (fixedClock) Now() int64 { return 777 }

type fixedID struct{}

func (fixedID) New() string { return "id-1" }

func newSvc(sp Spawner, j Journal, st RunStore) RunService {
	return NewRunService(sp, j, st, fixedClock{}, fixedID{})
}

func TestSpawn_WritesJournalAndStores(t *testing.T) {
	sp, j, st := &spySpawner{}, &spyJournal{writable: true}, newMemStore()
	run, err := newSvc(sp, j, st).Spawn(SpawnReq{Prompt: "hi", Meta: &domain.Meta{Kind: "generate", LaneKey: "g"}})
	if err != nil {
		t.Fatal(err)
	}
	if run.ID != "id-1" || run.Status != domain.StatusRunning || run.StartedAt != 777 {
		t.Fatalf("run %+v", run)
	}
	if run.Pid == nil || *run.Pid != 99 {
		t.Fatalf("pid %v", run.Pid)
	}
	if sp.calls != 1 || j.writes < 1 {
		t.Fatalf("spawn %d, journal writes %d", sp.calls, j.writes)
	}
	if sp.spec.Kind != "generate" {
		t.Fatalf("spec kind %q", sp.spec.Kind)
	}
	if _, ok := st.Get("id-1"); !ok {
		t.Fatal("not stored")
	}
}

func TestSpawn_NoPrompt(t *testing.T) {
	sp := &spySpawner{}
	_, err := newSvc(sp, &spyJournal{writable: true}, newMemStore()).Spawn(SpawnReq{Prompt: ""})
	if !errors.Is(err, ErrNoPrompt) {
		t.Fatalf("want ErrNoPrompt, got %v", err)
	}
	if sp.calls != 0 {
		t.Fatal("must not spawn without a prompt")
	}
}

// E26 — a SpawnReq with an engine threads it onto the journaled Run AND the SpawnSpec (9.1.1), and an
// empty engine stays the claude default (9.1.2).
func TestSpawn_EngineThreadedAndJournaled(t *testing.T) {
	sp, j, st := &spySpawner{}, &spyJournal{writable: true}, newMemStore()
	run, err := newSvc(sp, j, st).Spawn(SpawnReq{Prompt: "p", Engine: domain.EngineOpencode})
	if err != nil {
		t.Fatal(err)
	}
	if run.Engine != domain.EngineOpencode {
		t.Fatalf("run engine %q, want opencode", run.Engine)
	}
	if sp.spec.Engine != domain.EngineOpencode {
		t.Fatalf("SpawnSpec engine %q, want opencode", sp.spec.Engine)
	}
	stored, _ := st.Get(run.ID)
	if stored.Engine != domain.EngineOpencode {
		t.Fatalf("stored run engine %q, want opencode", stored.Engine)
	}
}

func TestSpawn_EngineDefaultsToClaude(t *testing.T) {
	sp, j, st := &spySpawner{}, &spyJournal{writable: true}, newMemStore()
	run, err := newSvc(sp, j, st).Spawn(SpawnReq{Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if run.Engine != domain.EngineClaude || sp.spec.Engine != domain.EngineClaude {
		t.Fatalf("empty engine must resolve to claude: run=%q spec=%q", run.Engine, sp.spec.Engine)
	}
}

type fastExitSpawner struct{ code int }

// Spawn returns a wait func that reports an immediate exit with f.code — e.g. `claude` off PATH → 127.
func (f fastExitSpawner) Spawn(spec SpawnSpec) (int, func() int, error) {
	return 42, func() int { return f.code }, nil
}

func TestSpawn_FastExitFinalizes(t *testing.T) {
	// Regression for the finalize-before-Put race: a fast-exiting run must NOT be stuck "running".
	// The finalize now runs in a goroutine (armed after the pid is journaled), so poll for it.
	st := newMemStore()
	if _, err := newSvc(fastExitSpawner{code: 127}, &spyJournal{writable: true}, st).Spawn(SpawnReq{Prompt: "hi"}); err != nil {
		t.Fatal(err)
	}
	if s := waitStatus(t, st, "id-1"); s != domain.StatusError {
		t.Fatalf("fast-exit 127 must finalize to error, got %s", s)
	}
	if r, _ := st.Get("id-1"); r.ExitCode == nil || *r.ExitCode != 127 {
		t.Fatalf("fast-exit exit code %v, want 127", r.ExitCode)
	}
}

func TestSpawnInit_spawnsWithInitKind(t *testing.T) {
	sp, j, st := &spySpawner{}, &spyJournal{writable: true}, newMemStore()
	run, deduped, err := newSvc(sp, j, st).SpawnInit(SpawnReq{Prompt: "/init-env x", Meta: &domain.Meta{Kind: "init", LaneKey: "/r/prd.md"}})
	if err != nil {
		t.Fatal(err)
	}
	if deduped {
		t.Fatal("first init must NOT be deduped")
	}
	if run.ID != "id-1" || run.Status != domain.StatusRunning {
		t.Fatalf("run %+v", run)
	}
	if sp.spec.Kind != "init" {
		t.Fatalf("spawner must get Kind:init (drives --dangerously-skip-permissions), got %q", sp.spec.Kind)
	}
	if sp.calls != 1 {
		t.Fatalf("want exactly one spawn, got %d", sp.calls)
	}
}

func TestSpawnInit_dedupesConcurrentLane(t *testing.T) {
	// A second identical init POST re-adopts the in-flight run and spawns NO second privileged process.
	sp, j, st := &spySpawner{}, &spyJournal{writable: true}, newMemStore()
	svc := newSvc(sp, j, st)
	meta := &domain.Meta{Kind: "init", LaneKey: "/r/prd.md"}
	first, _, _ := svc.SpawnInit(SpawnReq{Prompt: "/init-env", Meta: meta})
	second, deduped, err := svc.SpawnInit(SpawnReq{Prompt: "/init-env", Meta: meta})
	if err != nil {
		t.Fatal(err)
	}
	if !deduped || second.ID != first.ID {
		t.Fatalf("second init must dedupe to the in-flight run, got deduped=%v id=%q", deduped, second.ID)
	}
	if sp.calls != 1 {
		t.Fatalf("dedupe must spawn exactly ONE privileged process, got %d", sp.calls)
	}
}

func TestSpawnInit_initDoesNotDedupeAgainstBuild(t *testing.T) {
	// init and build SHARE a laneKey (the PRD path); a running BUILD must not swallow an init POST.
	sp, j, st := &spySpawner{}, &spyJournal{writable: true}, newMemStore()
	st.Put(domain.Run{ID: "b1", Status: domain.StatusRunning, Meta: &domain.Meta{Kind: "build", LaneKey: "/r/prd.md"}})
	_, deduped, err := newSvc(sp, j, st).SpawnInit(SpawnReq{Prompt: "/init-env", Meta: &domain.Meta{Kind: "init", LaneKey: "/r/prd.md"}})
	if err != nil {
		t.Fatal(err)
	}
	if deduped {
		t.Fatal("a running build on the lane must NOT dedupe an init (kind-scoped reserve)")
	}
	if sp.calls != 1 {
		t.Fatalf("init must still spawn, got %d spawns", sp.calls)
	}
}

func TestSpawnInit_emptyLaneRegistersBeforeSpawn(t *testing.T) {
	// rplan-review BLOCKER: an empty-laneKey init must still be Put BEFORE spawn, so a fast-exiting init
	// finalizes instead of being stuck "running".
	st := newMemStore()
	_, deduped, err := newSvc(fastExitSpawner{code: 127}, &spyJournal{writable: true}, st).
		SpawnInit(SpawnReq{Prompt: "/init-env", Meta: &domain.Meta{Kind: "init", LaneKey: ""}})
	if err != nil || deduped {
		t.Fatalf("empty-lane init should spawn (not dedupe): deduped=%v err=%v", deduped, err)
	}
	if s := waitStatus(t, st, "id-1"); s != domain.StatusError {
		t.Fatalf("empty-lane fast-exit init must finalize (registered before spawn), got %s", s)
	}
}

func TestSpawnInit_noPrompt(t *testing.T) {
	_, _, err := newSvc(&spySpawner{}, &spyJournal{writable: true}, newMemStore()).SpawnInit(SpawnReq{Prompt: ""})
	if !errors.Is(err, ErrNoPrompt) {
		t.Fatalf("want ErrNoPrompt, got %v", err)
	}
}

func TestSpawnInit_journalWriteFailureCleansUp(t *testing.T) {
	// The durable-record write fails AFTER the reserve → the reserved init must be removed (no phantom
	// running init locking the lane) and the spawner must never be reached.
	sp := &spySpawner{}
	st := newMemStore()
	_, _, err := newSvc(sp, &spyJournal{writable: true, writeErr: errors.New("disk full")}, st).
		SpawnInit(SpawnReq{Prompt: "/init-env", Meta: &domain.Meta{Kind: "init", LaneKey: "/r/prd.md"}})
	if !errors.Is(err, ErrRunsDirUnwritable) {
		t.Fatalf("want ErrRunsDirUnwritable on journal write failure, got %v", err)
	}
	if _, ok := st.Get("id-1"); ok {
		t.Fatal("a journal write failure must delete the reserved init (no phantom lane-locking run)")
	}
	if sp.calls != 0 {
		t.Fatal("no spawn when the durable record failed")
	}
}

func TestSpawnInit_spawnErrorCleansUp(t *testing.T) {
	st := newMemStore()
	_, _, err := newSvc(errSpawner{}, &spyJournal{writable: true}, st).
		SpawnInit(SpawnReq{Prompt: "/init-env", Meta: &domain.Meta{Kind: "init", LaneKey: "/r/prd.md"}})
	if err == nil {
		t.Fatal("want spawn error")
	}
	if _, ok := st.Get("id-1"); ok {
		t.Fatal("a spawn failure must delete the reserved init (no phantom running run locking the lane)")
	}
}

type errSpawner struct{}

func (errSpawner) Spawn(SpawnSpec) (int, func() int, error) {
	return 0, nil, errors.New("start failed")
}

func TestSpawn_SpawnErrorCleansUp(t *testing.T) {
	st := newMemStore()
	if _, err := newSvc(errSpawner{}, &spyJournal{writable: true}, st).Spawn(SpawnReq{Prompt: "hi"}); err == nil {
		t.Fatal("want spawn error")
	}
	if _, ok := st.Get("id-1"); ok {
		t.Fatal("a spawn failure must delete the run (no phantom running run)")
	}
}

func TestSpawn_UnwritableDir(t *testing.T) {
	sp, j := &spySpawner{}, &spyJournal{writable: false}
	_, err := newSvc(sp, j, newMemStore()).Spawn(SpawnReq{Prompt: "hi"})
	if !errors.Is(err, ErrRunsDirUnwritable) {
		t.Fatalf("want ErrRunsDirUnwritable, got %v", err)
	}
	if sp.calls != 0 || j.writes != 0 {
		t.Fatal("no spawn, no journal write when dir unwritable (no phantom run)")
	}
}
