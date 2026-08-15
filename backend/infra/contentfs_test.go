package infra

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/drayanaindra/saki-cli/backend/domain"
	"github.com/drayanaindra/saki-cli/backend/usecase"
)

// falseVerifier resolves no commit — matching the golden capture, whose fixture SHA "abc1234" does
// not resolve, so the beta resume target lands on slice 1 / approved. Deterministic (no ambient git).
type falseVerifier struct{}

func (falseVerifier) CommitExists(string, string) bool { return false }

// TestFindWorkItemsGolden (AC 1.2 · 5.1): FindWorkItems over the real fixture repo, through ContentFS
// (the real fs walk) + a deterministic verifier, deep-equals the golden captured from the TS
// findWorkItems oracle (backend/testdata/content/workitems-repo.golden.json). Paths are relativised
// to cwd exactly as the capture harness does, so the committed golden is machine-portable.
func TestFindWorkItemsGolden(t *testing.T) {
	cwd, err := filepath.Abs(filepath.Join("..", "testdata", "content", "workitems-repo"))
	if err != nil {
		t.Fatal(err)
	}
	svc := usecase.NewWorkItemsService(ContentFS{}, falseVerifier{})
	prds, plans := svc.FindWorkItems(cwd)

	rel := func(items []domain.WorkItem) []domain.WorkItem {
		out := make([]domain.WorkItem, len(items))
		for i, it := range items {
			r, err := filepath.Rel(cwd, it.Path)
			if err != nil {
				t.Fatal(err)
			}
			it.Path = r
			out[i] = it
		}
		return out
	}
	got := roundtrip(t, map[string]any{"prds": rel(prds), "plans": rel(plans)})

	goldenBytes, err := os.ReadFile(filepath.Join("..", "testdata", "content", "workitems-repo.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	var want any
	if err := json.Unmarshal(goldenBytes, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("workitems parity mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

// TestReadRoadmap_readOnly_realFS (AC 1.4 · R1 INVARIANT): a read never mutates the file it parses —
// the roadmap.md bytes are identical before and after two ReadRoadmap calls through the real ContentFS.
func TestReadRoadmap_readOnly_realFS(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "testdata", "content", "roadmap-allkinds.md"))
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(cwd, "tasks", "roadmap.md")
	if err := os.WriteFile(target, src, 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}

	svc := usecase.NewRoadmapService(ContentFS{})
	for i := 0; i < 2; i++ {
		if status, _ := svc.ReadRoadmap(cwd); status != 200 {
			t.Fatalf("read %d: status %d, want 200", i, status)
		}
	}

	after, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("roadmap.md mutated by a read — R1 (read-only) violated")
	}
}

// roundtrip marshals v through JSON and back to a generic any, so the WorkItem custom MarshalJSON
// shape compares field-for-field against the golden without struct/interface type differences.
func roundtrip(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
