package infra

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/drayanaindra/saki-cli/backend/domain"
	"github.com/drayanaindra/saki-cli/backend/usecase"
)

// FileJournalReader loads the Go backend's journaled runs from its runs dir (the <id>.json records +
// the <id>.exit / pid-alive disk facts). Implements usecase.JournalReader.
type FileJournalReader struct{ journal FileJournal }

func NewFileJournalReader(journal FileJournal) FileJournalReader {
	return FileJournalReader{journal: journal}
}

func (r FileJournalReader) Load() ([]usecase.RehydratedRun, error) {
	entries, err := os.ReadDir(r.journal.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // no runs dir yet — nothing to rehydrate
		}
		return nil, err
	}
	var out []usecase.RehydratedRun
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(r.journal.dir, e.Name()))
		if err != nil {
			continue
		}
		var rec runRecord
		if err := json.Unmarshal(b, &rec); err != nil {
			continue // a half-written/corrupt journal is skipped, not fatal
		}
		firstStarted := rec.FirstStartedAt
		if firstStarted == 0 {
			firstStarted = rec.StartedAt // pre-F6 record (or a non-build run) — default to startedAt so the
			// wall-clock budget isn't measured from epoch 0 (mirror runManager.ts:1003).
		}
		run := domain.Run{
			ID: rec.ID, Status: rec.Status, ExitCode: rec.ExitCode, Prompt: rec.Prompt,
			Cwd: rec.Cwd, StartedAt: rec.StartedAt, Meta: rec.Meta, Pid: rec.Pid,
			ConfigDir: rec.ConfigDir, ResumeCount: rec.ResumeCount, NoProgressStreak: rec.NoProgressStreak,
			FirstStartedAt: firstStarted, PendingResume: rec.PendingResume, PushedAt: rec.PushedAt,
			IdempotencyKey: rec.IdempotencyKey,
		}
		exit, hasExit := readExitFile(r.journal.ExitPath(rec.ID))
		out = append(out, usecase.RehydratedRun{
			Run:      run,
			PidAlive: rec.Pid != nil && pidAlive(*rec.Pid),
			ExitCode: exit,
			HasExit:  hasExit,
		})
	}
	return out, nil
}

// readExitFile reads the <id>.exit sentinel: (code, true) if present + parseable, (nil, false) else.
func readExitFile(path string) (*int, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return nil, true // .exit exists but is unparseable — treat as finished (unknown code)
	}
	return &n, true
}

// pidAlive reports whether pid is a live process (signal 0 = existence check).
func pidAlive(pid int) bool {
	if pid <= 1 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}
