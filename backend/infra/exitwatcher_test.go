package infra

import (
	"os"
	"testing"
)

func TestFileExitWatcher_Terminal(t *testing.T) {
	j := NewFileJournal(t.TempDir())
	w := NewFileExitWatcher(j)

	// no .exit + nil pid → no exit, not alive.
	exit, has, alive := w.Terminal("r1", nil)
	if has || alive || exit != nil {
		t.Fatalf("empty: exit=%v has=%v alive=%v", exit, has, alive)
	}

	// .exit present + parseable → hasExit, code.
	if err := os.WriteFile(j.ExitPath("r1"), []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exit, has, _ = w.Terminal("r1", nil)
	if !has || exit == nil || *exit != 0 {
		t.Fatalf(".exit 0: exit=%v has=%v", exit, has)
	}

	// this test process's own pid reads as alive.
	self := os.Getpid()
	if _, _, alive = w.Terminal("r2", &self); !alive {
		t.Fatal("self pid should read as alive")
	}
}
