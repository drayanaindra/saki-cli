package infra

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/drayanaindra/saki-cli/backend/domain"
	"github.com/drayanaindra/saki-cli/backend/usecase"
)

func TestFileJournalReader_Load(t *testing.T) {
	dir := t.TempDir()
	j := NewFileJournal(dir)
	deadPid := 999999 // out of macOS/Linux PID range → not alive
	// finished-while-down: journal says running, .exit present
	_ = j.Write(domain.Run{ID: "r1", Status: "running", Prompt: "hi", StartedAt: 1, Pid: &deadPid})
	_ = os.WriteFile(j.ExitPath("r1"), []byte("0\n"), 0o644)
	// interrupted: running, no .exit, dead pid
	_ = j.Write(domain.Run{ID: "r2", Status: "running", StartedAt: 2, Pid: &deadPid})

	recs, err := NewFileJournalReader(j).Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("want 2 records, got %d", len(recs))
	}
	byID := map[string]usecase.RehydratedRun{}
	for _, r := range recs {
		byID[r.Run.ID] = r
	}
	if !byID["r1"].HasExit || byID["r1"].ExitCode == nil || *byID["r1"].ExitCode != 0 {
		t.Fatalf("r1 should have .exit 0: %+v", byID["r1"])
	}
	if byID["r2"].HasExit {
		t.Fatal("r2 should have no .exit")
	}
	if byID["r1"].PidAlive || byID["r2"].PidAlive {
		t.Fatal("pid 999999 must not read as alive")
	}
}

func TestFileJournalReader_MissingDir(t *testing.T) {
	recs, err := NewFileJournalReader(NewFileJournal(filepath.Join(t.TempDir(), "nope"))).Load()
	if err != nil || recs != nil {
		t.Fatalf("missing dir → (nil,nil), got %v %v", recs, err)
	}
}
