package usecase

import "github.com/drayanaindra/saki-cli/backend/domain"

// Rehydrate rebuilds the in-memory run store from the durable journals on boot, so a server restart
// never loses or mis-reports an in-flight run (Inv-2). Policy mirrors apps/server resume
// (runManager.ts:1021-1038), minus the build auto-resume engine (Go owns non-build runs):
//   - already-terminal in the journal      → keep as-is
//   - status running + <id>.exit present    → finished-while-down → finalize with the exit code
//   - status running + pid still alive       → keep running (the SSE tail re-attaches on connect)
//   - status running + dead, no exit         → interrupted → error (a non-build run just errors)
func Rehydrate(store RunStore, reader JournalReader) (int, error) {
	recs, err := reader.Load()
	if err != nil {
		return 0, err
	}
	for _, rr := range recs {
		run := rr.Run
		// A build's restart policy (re-attach / finalize-and-resume / interrupt-resume) is owned by the
		// engine's RehydrateBuilds — Put it as-recorded here and never finalize it, so the two paths can't
		// double-manage one build (BR2). A running build stays running until the engine claims it.
		if run.Meta != nil && run.Meta.Kind.IsBuild() {
			store.Put(run)
			continue
		}
		switch {
		case run.Status != domain.StatusRunning:
			// keep the recorded terminal status
		case rr.HasExit:
			code := -1 // an existing-but-unparseable .exit reads as error (parity with apps/server → -1)
			if rr.ExitCode != nil {
				code = *rr.ExitCode
			}
			run.Status = domain.StatusDone
			if code != 0 {
				run.Status = domain.StatusError
			}
			run.ExitCode = rr.ExitCode
		case rr.PidAlive:
			// still running — leave status running; the tail re-reads .out on the next SSE connect
		default:
			run.Status = domain.StatusError // dead without an exit code = interrupted
		}
		store.Put(run)
	}
	return len(recs), nil
}
