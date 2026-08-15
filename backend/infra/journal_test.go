package infra

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

func TestFileJournal_WriteAndPaths(t *testing.T) {
	dir := t.TempDir()
	j := NewFileJournal(dir)
	if j.OutPath("x") != filepath.Join(dir, "x.out") || j.ExitPath("x") != filepath.Join(dir, "x.exit") {
		t.Fatal("out/exit paths wrong")
	}
	if err := j.EnsureWritable(); err != nil {
		t.Fatal(err)
	}
	pid, cwd := 7, "/repo"
	run := domain.Run{ID: "r1", Status: "running", Prompt: "hi", Cwd: &cwd, StartedAt: 5,
		Meta: &domain.Meta{Kind: "generate", LaneKey: "g"}, Pid: &pid}
	if err := j.Write(run); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "r1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rec map[string]any
	if err := json.Unmarshal(b, &rec); err != nil {
		t.Fatal(err)
	}
	if rec["id"] != "r1" || rec["status"] != "running" || rec["prompt"] != "hi" {
		t.Fatalf("record %v", rec)
	}
	meta, _ := rec["meta"].(map[string]any)
	if meta["kind"] != "generate" || meta["laneKey"] != "g" {
		t.Fatalf("meta %v", rec["meta"])
	}
}

func TestFileJournal_EnsureWritable_Fails(t *testing.T) {
	// parent path is a FILE, so MkdirAll(dir) fails → "unwritable" (AC 2.4).
	f := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := NewFileJournal(filepath.Join(f, "runs")).EnsureWritable(); err == nil {
		t.Fatal("want error when the runs dir can't be created")
	}
}
