package usecase

import (
	"testing"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

type spyKiller struct {
	signals, lastPid int
	kills, killPid   int
	alive            bool
}

func (k *spyKiller) SignalGroup(pid int) error { k.signals++; k.lastPid = pid; return nil }
func (k *spyKiller) KillGroup(pid int) error   { k.kills++; k.killPid = pid; return nil }
func (k *spyKiller) Alive(int) bool            { return k.alive }

func TestStop_RunningSignalsAndErrors(t *testing.T) {
	st := newMemStore()
	pid := 5000
	st.Put(domain.Run{ID: "r1", Status: domain.StatusRunning, Pid: &pid})
	k := &spyKiller{}
	if !NewStopService(st, k).Stop("r1") {
		t.Fatal("stop of a running run should return true")
	}
	if k.signals != 1 || k.lastPid != 5000 {
		t.Fatalf("process group not signalled: %+v", k)
	}
	if r, _ := st.Get("r1"); r.Status != domain.StatusError {
		t.Fatalf("stopped run status %s, want error", r.Status)
	}
}

func TestStop_UnknownOrFinishedIsNoop(t *testing.T) {
	st := newMemStore()
	st.Put(domain.Run{ID: "done", Status: domain.StatusDone})
	svc := NewStopService(st, &spyKiller{})
	if svc.Stop("nope") {
		t.Fatal("unknown id → false (404)")
	}
	if svc.Stop("done") {
		t.Fatal("finished run → false (404)")
	}
}

func TestStop_NoPidNoSignal(t *testing.T) {
	st := newMemStore()
	st.Put(domain.Run{ID: "r1", Status: domain.StatusRunning}) // pid nil
	k := &spyKiller{}
	NewStopService(st, k).Stop("r1")
	if k.signals != 0 {
		t.Fatal("a run with no pid must not signal (avoids kill(-1))")
	}
}
