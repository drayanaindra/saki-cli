package usecase

import (
	"errors"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

// ErrNoPrompt is returned when a spawn request has no prompt (→ HTTP 422, parity with
// apps/server index.ts:757).
var ErrNoPrompt = errors.New("prompt (string) is required")

// ErrRunsDirUnwritable is returned when the runs dir can't be created/written (→ HTTP 500,
// with no phantom run left behind — AC 2.4).
var ErrRunsDirUnwritable = errors.New("runs dir is not writable")

// ErrBinaryNotFound is returned by the Spawner when the requested engine's CLI is not on its PATH
// (E26 9.1.5). The build path parks immediately on it — retrying a binary that isn't installed
// cannot succeed — and the plain run path surfaces it as a loud spawn error.
var ErrBinaryNotFound = errors.New("engine binary not found on PATH")

// ErrEngineNotProvisioned is returned by the Spawner when the engine's CLI is present but its PROFILE
// cannot resolve the saki-builder commands (an opencode profile missing the plugin; a codex home
// missing the skills). Infra wraps its specific diagnosis in this sentinel so the HTTP layer can
// surface the REASON — the whole point of these proofs is that the operator learns why a run was
// refused, and a generic "spawn failed" would hide exactly the actionable half.
var ErrEngineNotProvisioned = errors.New("engine profile cannot resolve the saki-builder commands")

// SpawnReq is the parsed POST /api/run body (non-build path only; build is forwarded upstream).
type SpawnReq struct {
	Prompt    string
	Cwd       *string
	ConfigDir *string
	Meta      *domain.Meta
	// Engine selects the agent runtime (E26). Empty = claude (the default — today's behavior).
	Engine domain.RunEngine
}

// RunService spawns and tracks non-build runs.
type RunService struct {
	spawner Spawner
	journal Journal
	store   RunStore
	clock   Clock
	idgen   IDGen
}

func NewRunService(spawner Spawner, journal Journal, store RunStore, clock Clock, idgen IDGen) RunService {
	return RunService{spawner: spawner, journal: journal, store: store, clock: clock, idgen: idgen}
}

// Spawn starts a non-build run: it ensures the runs dir is writable (else ErrRunsDirUnwritable),
// writes the journal + registers the run, THEN spawns the detached process. Ordering matters: the
// run is Put into the store BEFORE the spawner arms its finalize goroutine, so a fast-exiting run
// (e.g. `claude` off PATH → exit 127) can't finalize into an absent id and get stuck "running".
// The pid is applied in-memory afterward (SetPid) rather than a full re-Put/re-journal, so it can't
// clobber a concurrent finalize. The caller must have already routed build/init runs upstream.
func (s RunService) Spawn(req SpawnReq) (domain.Run, error) {
	if req.Prompt == "" {
		return domain.Run{}, ErrNoPrompt
	}
	if err := s.journal.EnsureWritable(); err != nil {
		return domain.Run{}, ErrRunsDirUnwritable
	}
	id := s.idgen.New()
	run := domain.Run{
		ID:        id,
		Status:    domain.StatusRunning,
		Prompt:    req.Prompt,
		Cwd:       req.Cwd,
		StartedAt: s.clock.Now(),
		Meta:      req.Meta,
		ConfigDir: req.ConfigDir,
		Engine:    domain.ResolveEngine(req.Engine),
	}
	if err := s.journal.Write(run); err != nil { // durable record first; failure → 500, no spawn
		return domain.Run{}, ErrRunsDirUnwritable
	}
	s.store.Put(run) // register BEFORE spawn so a fast-exit finalize isn't lost
	kind := domain.RunKind("")
	if req.Meta != nil {
		kind = req.Meta.Kind
	}
	pid, wait, err := s.spawner.Spawn(SpawnSpec{ID: id, Prompt: req.Prompt, Cwd: req.Cwd, ConfigDir: req.ConfigDir, Kind: kind, Engine: run.Engine})
	if err != nil {
		s.store.Delete(id) // spawn failed → no phantom "running" run in the store
		return domain.Run{}, err
	}
	// Journal the pid BEFORE arming the finalize goroutine, so a restart-rehydrate can see the run is
	// alive (the pid was the missing piece) AND a fast exit can't finalize before the pid is durable.
	s.store.SetPid(id, pid)
	run.Pid = &pid
	_ = s.journal.Write(run)
	go func() {
		code := wait()
		s.store.Finalize(id, code)
		s.rejournal(id)
	}()
	return run, nil
}

// SpawnInit starts a privileged /init-env run locally (F7 · P6 slice 4): the spawner applies
// --dangerously-skip-permissions for kind:"init" only. It is double-submit deduped by lane (the PRD
// path) so a second identical request re-adopts the in-flight run and spawns NO second privileged
// process (AC 4.3). Ordering mirrors the build engine's reserveAndSpawn: reserve/Put BEFORE journal
// (a deduped init leaves no phantom journal entry) and before spawn (register-before-spawn, so a
// fast-exiting init can't finalize into an absent id). It arms the PLAIN finalize goroutine — init is
// one-shot, never the build auto-resume path. deduped==true means no new process was started.
func (s RunService) SpawnInit(req SpawnReq) (run domain.Run, deduped bool, err error) {
	if req.Prompt == "" {
		return domain.Run{}, false, ErrNoPrompt
	}
	if err := s.journal.EnsureWritable(); err != nil {
		return domain.Run{}, false, ErrRunsDirUnwritable
	}
	id := s.idgen.New()
	run = domain.Run{
		ID:        id,
		Status:    domain.StatusRunning,
		Prompt:    req.Prompt,
		Cwd:       req.Cwd,
		StartedAt: s.clock.Now(),
		Meta:      req.Meta,
		ConfigDir: req.ConfigDir,
		Engine:    domain.ResolveEngine(req.Engine),
	}
	laneKey := ""
	if req.Meta != nil {
		laneKey = req.Meta.LaneKey
	}
	// Atomic lane reserve BEFORE spawn: an in-flight init on this lane → re-adopt it, spawn nothing.
	if existingID, reserved := s.store.ReserveInit(laneKey, run); !reserved {
		if existing, ok := s.store.Get(existingID); ok {
			return existing, true, nil
		}
		return domain.Run{ID: existingID}, true, nil
	}
	if err := s.journal.Write(run); err != nil { // durable record before spawn; failure → 500, no spawn
		s.store.Delete(id)
		return domain.Run{}, false, ErrRunsDirUnwritable
	}
	pid, wait, err := s.spawner.Spawn(SpawnSpec{ID: id, Prompt: req.Prompt, Cwd: req.Cwd, ConfigDir: req.ConfigDir, Kind: "init", Engine: run.Engine})
	if err != nil {
		s.store.Delete(id) // spawn failed → no phantom "running" init reserving the lane forever
		return domain.Run{}, false, err
	}
	s.store.SetPid(id, pid)
	run.Pid = &pid
	_ = s.journal.Write(run)
	go func() {
		code := wait()
		s.store.Finalize(id, code)
		s.rejournal(id)
	}()
	return run, false, nil
}

// Owned reports whether Go owns run id (it's in the local store — a non-build run Go spawned).
// The events handler uses it to decide tail (owned) vs passthrough (un-owned build run).
func (s RunService) Owned(id string) (domain.Run, bool) {
	return s.store.Get(id)
}

// rejournal re-persists a run after its status changed (finalize), best-effort.
func (s RunService) rejournal(id string) {
	if run, ok := s.store.Get(id); ok {
		_ = s.journal.Write(run)
	}
}
