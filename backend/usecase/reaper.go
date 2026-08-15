package usecase

import (
	"context"
	"time"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

// ReaperService finalizes runs that SURVIVED a restart (rehydrated as running): their process is a
// child of the PREVIOUS server, so no spawn-time OnExit goroutine in this process will finalize
// them. Without this a completed-after-restart run hangs "running" forever (the bug Inv-2 forbids).
// The reaper polls each such run's <id>.exit / pid until it can finalize + re-journal it.
type ReaperService struct {
	store   RunStore
	journal Journal
	watch   ExitWatcher
	poll    time.Duration
	// maxWait bounds the poll so a survived run that never writes .exit yet whose pid reads alive
	// forever (SIGKILL-before-.exit + an OS pid reuse) finalizes to error instead of polling +
	// leaking its goroutine indefinitely.
	maxWait time.Duration
}

func NewReaperService(store RunStore, journal Journal, watch ExitWatcher, poll time.Duration) ReaperService {
	if poll <= 0 {
		poll = 500 * time.Millisecond
	}
	return ReaperService{store: store, journal: journal, watch: watch, poll: poll, maxWait: 6 * time.Hour}
}

// ReapExisting starts a background poller for every run currently "running" in the store. Call it
// ONCE after rehydrate + before serving, so it reaps only restart-survived runs — a NEW local spawn
// has its own OnExit finalize and must not be double-watched.
func (s ReaperService) ReapExisting(ctx context.Context) {
	for _, r := range s.store.List() {
		// Builds are reaped by the engine's watchSurvivedBuild (which routes a terminal survived build
		// through the auto-resume/push engine); the plain reaper's bare Finalize would skip that, so it
		// must SKIP builds to avoid double-managing one (BR2).
		if r.Meta != nil && r.Meta.Kind.IsBuild() {
			continue
		}
		if r.Status == domain.StatusRunning {
			go s.reap(ctx, r.ID, r.Pid)
		}
	}
}

func (s ReaperService) reap(ctx context.Context, id string, pid *int) {
	t := time.NewTicker(s.poll)
	defer t.Stop()
	giveUp := time.After(s.maxWait)
	for {
		select {
		case <-ctx.Done():
			return
		case <-giveUp:
			s.store.Finalize(id, -1) // never produced .exit within the bound → error (no forever-poll)
			s.rejournal(id)
			return
		case <-t.C:
		}
		run, ok := s.store.Get(id)
		if !ok || run.Status != domain.StatusRunning {
			return // finalized elsewhere, or gone
		}
		exit, hasExit, alive := s.watch.Terminal(id, pid)
		switch {
		case hasExit:
			code := -1 // an unparseable .exit reads as error (parity with apps/server readExitCode → -1)
			if exit != nil {
				code = *exit
			}
			s.store.Finalize(id, code)
			s.rejournal(id)
			return
		case !alive:
			s.store.Finalize(id, -1) // dead without an exit code = interrupted → error
			s.rejournal(id)
			return
		}
	}
}

func (s ReaperService) rejournal(id string) {
	if run, ok := s.store.Get(id); ok {
		_ = s.journal.Write(run)
	}
}
