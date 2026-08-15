package infra

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

// runRecord is the on-disk metadata journal (one <id>.json per run) — matches apps/server
// RunRecord (runManager.ts:1057). Events live in the sibling <id>.out. The build auto-resume
// chain-state fields (ConfigDir … IdempotencyKey) are omitempty + optional, so an OLD record written
// before F6 still rehydrates (their zero values are the correct defaults, except FirstStartedAt which
// FileJournalReader defaults to StartedAt — journalreader.go).
type runRecord struct {
	ID        string       `json:"id"`
	Status    string       `json:"status"`
	ExitCode  *int         `json:"exitCode"`
	Prompt    string       `json:"prompt"`
	Cwd       *string      `json:"cwd"`
	StartedAt int64        `json:"startedAt"`
	Meta      *domain.Meta `json:"meta"`
	Pid       *int         `json:"pid"`

	// Build auto-resume chain-state (F6; omitempty → back-compat with pre-F6 records).
	ConfigDir        *string               `json:"configDir,omitempty"`
	ResumeCount      int                   `json:"resumeCount,omitempty"`
	NoProgressStreak int                   `json:"noProgressStreak,omitempty"`
	FirstStartedAt   int64                 `json:"firstStartedAt,omitempty"`
	PendingResume    *domain.PendingResume `json:"pendingResume,omitempty"`
	PushedAt         *int64                `json:"pushedAt,omitempty"`
	IdempotencyKey   *string               `json:"idempotencyKey,omitempty"`
}

// FileJournal writes the durable run triplet into the runs dir (SAKI_RUNS_DIR or
// ~/.saki-pipeline/runs), shared with apps/server.
type FileJournal struct{ dir string }

func NewFileJournal(dir string) FileJournal {
	if dir == "" {
		dir = defaultRunsDir()
	}
	return FileJournal{dir: dir}
}

func defaultRunsDir() string {
	if d := os.Getenv("SAKI_RUNS_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".saki-pipeline", "runs")
}

// GoRunsDir is the Go backend's OWN journal subdir (<runsDir>/go). apps/server's resume() does a
// FLAT readdir of <runsDir>, so it never sees — and never adopts / double-writes — a Go run's
// journal (the coexistence journal-ownership fix, no apps/server edit needed; Inv-1).
func GoRunsDir() string {
	return filepath.Join(defaultRunsDir(), "go")
}

func (j FileJournal) OutPath(id string) string  { return filepath.Join(j.dir, id+".out") }
func (j FileJournal) ExitPath(id string) string { return filepath.Join(j.dir, id+".exit") }

// EnsureWritable creates the runs dir and probes writability; an error means "unwritable" (→ 500,
// no phantom run — AC 2.4). The probe file is UNIQUE per call (os.CreateTemp) so two concurrent
// spawns can't remove each other's probe and spuriously fail on a writable dir.
func (j FileJournal) EnsureWritable() error {
	if err := os.MkdirAll(j.dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(j.dir, ".write-probe-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	return os.Remove(name)
}

func (j FileJournal) Write(run domain.Run) error {
	rec := runRecord{
		ID: run.ID, Status: run.Status, ExitCode: run.ExitCode, Prompt: run.Prompt,
		Cwd: run.Cwd, StartedAt: run.StartedAt, Meta: run.Meta, Pid: run.Pid,
		ConfigDir: run.ConfigDir, ResumeCount: run.ResumeCount, NoProgressStreak: run.NoProgressStreak,
		FirstStartedAt: run.FirstStartedAt, PendingResume: run.PendingResume, PushedAt: run.PushedAt,
		IdempotencyKey: run.IdempotencyKey,
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(j.dir, run.ID+".json"), b, 0o644)
}

// AppendOut appends a raw breadcrumb line to <id>.out so the build engine's park/resume breadcrumbs
// are durable (a late SSE viewer replays them, the PARKED_RE scan sees them). Called at finalize, when
// the detached child has already exited, so there is no concurrent writer to the file.
func (j FileJournal) AppendOut(id, line string) error {
	f, err := os.OpenFile(j.OutPath(id), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line + "\n")
	return err
}
