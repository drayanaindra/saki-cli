package infra

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

func TestFileWorkflowJournal_WriteCreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "workflows")
	j := NewFileWorkflowJournal(dir)
	if err := j.Write(domain.Workflow{ID: "w1", LaneKey: "/repo::F7", Cwd: "/repo", Target: "F7", Track: "PRD", Phase: domain.PhaseResolve, Status: domain.WorkflowRunning}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "w1.json")); err != nil {
		t.Fatal(err)
	}
}

func TestFileWorkflowJournal_LoadRejectsRenamedRecord(t *testing.T) {
	dir := t.TempDir()
	j := NewFileWorkflowJournal(dir)
	if err := j.Write(domain.Workflow{ID: "w1", LaneKey: "/repo::F7", Cwd: "/repo", Target: "F7", Track: "PRD", Phase: domain.PhaseResolve, Status: domain.WorkflowRunning}); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "w1.json"), filepath.Join(dir, "foreign.json")); err != nil {
		t.Fatal(err)
	}
	records, err := j.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("renamed workflow journal must be ignored, got %#v", records)
	}
}

func TestFileWorkflowJournal_WriteRejectsPathTraversalID(t *testing.T) {
	if err := NewFileWorkflowJournal(t.TempDir()).Write(domain.Workflow{ID: "../escape"}); err == nil {
		t.Fatal("workflow id containing a path separator must be rejected")
	}
}
