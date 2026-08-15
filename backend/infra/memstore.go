package infra

import (
	"sync"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

// MemStore is the in-memory store of Go-owned runs (non-build + build). The journal keeps the durable
// copy on disk; build rehydrate-and-route across a restart lands in F6 Slice 6.
type MemStore struct {
	mu   sync.RWMutex
	runs map[string]domain.Run
}

func NewMemStore() *MemStore { return &MemStore{runs: map[string]domain.Run{}} }

func (m *MemStore) Put(run domain.Run) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runs[run.ID] = run
}

func (m *MemStore) Get(id string) (domain.Run, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.runs[id]
	return r, ok
}

func (m *MemStore) List() []domain.Run {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]domain.Run, 0, len(m.runs))
	for _, r := range m.runs {
		out = append(out, r)
	}
	return out
}

// SetPid records a run's pid in memory without touching its status/exitCode, so it can't clobber
// a concurrent Finalize.
func (m *MemStore) SetPid(id string, pid int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.runs[id]; ok {
		p := pid
		r.Pid = &p
		m.runs[id] = r
	}
}

// Delete removes a run (spawn-failure cleanup, so a run that never started isn't listed).
func (m *MemStore) Delete(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.runs, id)
}

// Finalize sets a run's terminal status from its exit code (0 → done, else error). First-write-wins:
// a run that is no longer "running" is never re-finalized, so the stop path's Finalize(143) and the
// process's own OnExit finalize can race harmlessly (whichever lands first wins) — and the reaper
// can't clobber a run a stop already finalized. Returns true only when THIS call transitioned the run
// running→terminal, so the build engine's onExit can skip re-classifying a run a Stop already ended (BR2).
func (m *MemStore) Finalize(id string, exitCode int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[id]
	if !ok || r.Status != domain.StatusRunning {
		return false
	}
	code := exitCode
	r.ExitCode = &code
	if exitCode == 0 {
		r.Status = domain.StatusDone
	} else {
		r.Status = domain.StatusError
	}
	m.runs[id] = r
	return true
}

// ReserveBuild atomically dedups a build lane: under ONE write lock it scans for a running build on
// laneKey and, if none, Puts run. When one is already in flight it returns (existingID, false) so the
// caller re-adopts it and spawns nothing — closing the check-then-spawn TOCTOU two concurrent build
// POSTs (or a POST racing the resume sweep) would otherwise slip through (BR3, PRD 5.3).
func (m *MemStore) ReserveBuild(laneKey string, run domain.Run) (string, bool) {
	return m.reserveKind("build", laneKey, run)
}

// ReserveInit atomically dedups an init lane (F7 · P6 slice 4): a double-submit init for the same
// target (laneKey = the PRD path) re-adopts the in-flight run and spawns NO second privileged process
// (AC 4.3). It scans kind-scoped — init and build SHARE the same laneKey, so a laneKey-only scan would
// let a running build swallow an init POST.
func (m *MemStore) ReserveInit(laneKey string, run domain.Run) (string, bool) {
	return m.reserveKind("init", laneKey, run)
}

// reserveKind is the shared atomic lane reserve: under ONE write lock, scan for a RUNNING run of the
// same kind on laneKey and, if found, return (existingID, false) without touching the store. Otherwise
// Put run and return ("", true). An EMPTY laneKey skips the scan and always Puts (register-before-spawn
// must hold even for a lane-less run — never dedupe two lane-less runs together).
func (m *MemStore) reserveKind(kind domain.RunKind, laneKey string, run domain.Run) (string, bool) {
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

// Update applies mut to run id under the write lock and reports whether the run existed. mut mutates a
// pointer to the stored value in place; the whole read-modify-write is atomic, so the build engine's
// fire-once test-and-clear (only the caller whose mut saw pendingResume!=nil wins) is race-free.
func (m *MemStore) Update(id string, mut func(r *domain.Run)) bool {
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
