package usecase

import (
	"testing"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

type stubReader struct{ recs []RehydratedRun }

func (s stubReader) Load() ([]RehydratedRun, error) { return s.recs, nil }

func TestRehydrate_Policy(t *testing.T) {
	code0, code2 := 0, 2
	recs := []RehydratedRun{
		{Run: domain.Run{ID: "terminal", Status: domain.StatusDone}},                                     // already terminal → keep
		{Run: domain.Run{ID: "finished", Status: domain.StatusRunning}, HasExit: true, ExitCode: &code0}, // .exit 0 → done
		{Run: domain.Run{ID: "failed", Status: domain.StatusRunning}, HasExit: true, ExitCode: &code2},   // .exit 2 → error
		{Run: domain.Run{ID: "alive", Status: domain.StatusRunning}, PidAlive: true},                     // pid alive → keep running
		{Run: domain.Run{ID: "interrupted", Status: domain.StatusRunning}},                               // dead, no exit → error
	}
	st := newMemStore()
	n, err := Rehydrate(st, stubReader{recs: recs})
	if err != nil || n != 5 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	want := map[string]string{
		"terminal": domain.StatusDone, "finished": domain.StatusDone, "failed": domain.StatusError,
		"alive": domain.StatusRunning, "interrupted": domain.StatusError,
	}
	for id, ws := range want {
		r, ok := st.Get(id)
		if !ok || r.Status != ws {
			t.Fatalf("%s: want %s, got %q (ok=%v)", id, ws, r.Status, ok)
		}
	}
	if r, _ := st.Get("finished"); r.ExitCode == nil || *r.ExitCode != 0 {
		t.Fatalf("finished exitCode %v", r.ExitCode)
	}
}
