package usecase

import (
	"context"
	"testing"
	"time"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

type stubWatch struct {
	exit    *int
	hasExit bool
	alive   bool
}

func (w *stubWatch) Terminal(string, *int) (*int, bool, bool) { return w.exit, w.hasExit, w.alive }

// waitStatus polls the store until the run leaves "running" (or times out).
func waitStatus(t *testing.T, st RunStore, id string) string {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if r, ok := st.Get(id); ok && r.Status != domain.StatusRunning {
			return r.Status
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("run %s never left running", id)
	return ""
}

func TestReaper_FinalizesSurvivedRunOnExit(t *testing.T) {
	st := newMemStore()
	pid := 12345
	st.Put(domain.Run{ID: "r1", Status: domain.StatusRunning, Pid: &pid})
	watch := &stubWatch{exit: intPtr(0), hasExit: true, alive: true} // .exit appeared
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	NewReaperService(st, &spyJournal{writable: true}, watch, time.Millisecond).ReapExisting(ctx)
	if s := waitStatus(t, st, "r1"); s != domain.StatusDone {
		t.Fatalf("want done, got %s", s)
	}
}

func TestReaper_FinalizesDeadNoExit(t *testing.T) {
	st := newMemStore()
	pid := 12345
	st.Put(domain.Run{ID: "r1", Status: domain.StatusRunning, Pid: &pid})
	watch := &stubWatch{hasExit: false, alive: false} // dead without an exit code
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	NewReaperService(st, &spyJournal{writable: true}, watch, time.Millisecond).ReapExisting(ctx)
	if s := waitStatus(t, st, "r1"); s != domain.StatusError {
		t.Fatalf("dead-no-exit → error, got %s", s)
	}
}

func TestReaper_GivesUpBounded(t *testing.T) {
	// a survived run that never writes .exit but whose pid reads alive forever must still finalize
	// (to error) at the maxWait bound — not poll + leak forever.
	st := newMemStore()
	pid := 12345
	st.Put(domain.Run{ID: "r1", Status: domain.StatusRunning, Pid: &pid})
	watch := &stubWatch{hasExit: false, alive: true} // never exits, pid always "alive"
	svc := NewReaperService(st, &spyJournal{writable: true}, watch, time.Millisecond)
	svc.maxWait = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.ReapExisting(ctx)
	if s := waitStatus(t, st, "r1"); s != domain.StatusError {
		t.Fatalf("bounded give-up → error, got %s", s)
	}
}

func TestReaper_UnparseableExitIsError(t *testing.T) {
	st := newMemStore()
	pid := 12345
	st.Put(domain.Run{ID: "r1", Status: domain.StatusRunning, Pid: &pid})
	watch := &stubWatch{exit: nil, hasExit: true, alive: false} // .exit present but unparseable
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	NewReaperService(st, &spyJournal{writable: true}, watch, time.Millisecond).ReapExisting(ctx)
	if s := waitStatus(t, st, "r1"); s != domain.StatusError {
		t.Fatalf("unparseable .exit → error, got %s", s)
	}
}
