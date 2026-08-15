package infra

import (
	"testing"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

func runningRun(id, kind, lane string) domain.Run {
	return domain.Run{ID: id, Status: domain.StatusRunning, Meta: &domain.Meta{Kind: domain.RunKind(kind), LaneKey: lane}}
}

func TestReserveInit_dedupesRunningInitOnLane(t *testing.T) {
	m := NewMemStore()
	m.Put(runningRun("i1", "init", "/r/prd.md"))
	existing, reserved := m.ReserveInit("/r/prd.md", runningRun("i2", "init", "/r/prd.md"))
	if reserved || existing != "i1" {
		t.Fatalf("a running init on the lane must dedupe to i1, got existing=%q reserved=%v", existing, reserved)
	}
	if _, ok := m.Get("i2"); ok {
		t.Fatal("deduped init must NOT be Put into the store")
	}
}

func TestReserveInit_kindScoped_ignoresBuildOnSameLane(t *testing.T) {
	// init + build share the PRD-path laneKey; a running build must not swallow an init reserve.
	m := NewMemStore()
	m.Put(runningRun("b1", "build", "/r/prd.md"))
	existing, reserved := m.ReserveInit("/r/prd.md", runningRun("i1", "init", "/r/prd.md"))
	if !reserved || existing != "" {
		t.Fatalf("a build on the lane must NOT dedupe an init, got existing=%q reserved=%v", existing, reserved)
	}
	if _, ok := m.Get("i1"); !ok {
		t.Fatal("the init should have been reserved (Put) into the store")
	}
}

func TestReserveInit_emptyLaneAlwaysReserves(t *testing.T) {
	m := NewMemStore()
	m.Put(runningRun("i1", "init", ""))
	_, reserved := m.ReserveInit("", runningRun("i2", "init", ""))
	if !reserved {
		t.Fatal("an empty laneKey must always reserve (never dedupe two lane-less inits together)")
	}
}

func TestReserveInit_finishedInitDoesNotDedupe(t *testing.T) {
	m := NewMemStore()
	done := runningRun("i1", "init", "/r/prd.md")
	done.Status = domain.StatusDone
	m.Put(done)
	_, reserved := m.ReserveInit("/r/prd.md", runningRun("i2", "init", "/r/prd.md"))
	if !reserved {
		t.Fatal("a finished init must not block a new one on the same lane")
	}
}
