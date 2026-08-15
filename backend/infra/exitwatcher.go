package infra

// FileExitWatcher reads a run's terminal disk state — the <id>.exit sentinel + whether its pid is
// still alive. Implements usecase.ExitWatcher (for the restart-survived-run reaper).
type FileExitWatcher struct{ journal FileJournal }

func NewFileExitWatcher(journal FileJournal) FileExitWatcher {
	return FileExitWatcher{journal: journal}
}

func (w FileExitWatcher) Terminal(id string, pid *int) (*int, bool, bool) {
	exit, hasExit := readExitFile(w.journal.ExitPath(id))
	alive := pid != nil && pidAlive(*pid)
	return exit, hasExit, alive
}
