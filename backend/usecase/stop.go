package usecase

import "github.com/drayanaindra/saki-cli/backend/domain"

// StopService stops a Go-owned running run: it SIGTERMs the detached process group and marks the run
// errored (so the lane doesn't hang "running"). Mirrors apps/server RunManager.stop (runManager.ts:718).
type StopService struct {
	store  RunStore
	killer ProcessKiller
}

func NewStopService(store RunStore, killer ProcessKiller) StopService {
	return StopService{store: store, killer: killer}
}

// Stop returns false for an unknown or already-finished run (→ 404, parity with apps/server). For a
// running run it signals the process group (guarded pid>1 — kill(-1) would signal every reachable
// process) and marks it errored. Idempotent: the process's own OnExit finalize is a harmless re-mark.
func (s StopService) Stop(id string) bool {
	run, ok := s.store.Get(id)
	if !ok || run.Status != domain.StatusRunning {
		return false
	}
	if run.Pid != nil && *run.Pid > 1 {
		_ = s.killer.SignalGroup(*run.Pid) // already-exited → the signal errors harmlessly
	}
	s.store.Finalize(id, 143) // 128+SIGTERM(15) — a stopped run reads as error, never stuck "running"
	return true
}
