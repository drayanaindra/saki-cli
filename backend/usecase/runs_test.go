package usecase

import (
	"testing"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

type stubProxy struct{ runs []map[string]any }

func (s stubProxy) Forward(string, string, string, []byte) (int, []byte, error) { return 0, nil, nil }
func (s stubProxy) GetRuns(string) ([]map[string]any, error)                    { return s.runs, nil }

func TestList_UnionDedupSort(t *testing.T) {
	st := newMemStore()
	repo := "/repo"
	st.Put(domain.Run{ID: "local", Status: "running", StartedAt: 2000, Cwd: &repo})
	px := stubProxy{runs: []map[string]any{
		{"id": "up", "status": "running", "startedAt": float64(3000)},
		{"id": "local", "status": "stale", "startedAt": float64(1)}, // dup id → dropped, local wins
	}}
	out := NewListService(st, px, nil).List("", "")
	if len(out) != 2 {
		t.Fatalf("want 2, got %d", len(out))
	}
	if out[0]["id"] != "up" || out[1]["id"] != "local" { // newest-first
		t.Fatalf("order wrong: %v, %v", out[0]["id"], out[1]["id"])
	}
	if out[1]["status"] != "running" {
		t.Fatalf("local copy should win dedup, got %v", out[1]["status"])
	}
}

func TestList_CwdFilter(t *testing.T) {
	st := newMemStore()
	a, b := "/a", "/b"
	st.Put(domain.Run{ID: "ra", StartedAt: 1, Cwd: &a})
	st.Put(domain.Run{ID: "rb", StartedAt: 2, Cwd: &b})
	out := NewListService(st, stubProxy{}, nil).List("/a", "")
	if len(out) != 1 || out[0]["id"] != "ra" {
		t.Fatalf("cwd filter failed: %v", out)
	}
}

func TestList_UpstreamErrorDegrades(t *testing.T) {
	st := newMemStore()
	st.Put(domain.Run{ID: "local", StartedAt: 1})
	// a nil-returning proxy (as if apps/server 401'd) → list still returns local runs
	out := NewListService(st, stubProxy{runs: nil}, nil).List("", "")
	if len(out) != 1 || out[0]["id"] != "local" {
		t.Fatalf("want local-only on empty upstream, got %v", out)
	}
}
