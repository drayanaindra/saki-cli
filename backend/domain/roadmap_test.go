package domain

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestParseRoadmapGolden (AC 1.1 · 5.2): Go ParseRoadmap over each fixture deep-equals the golden
// JSON captured from the TS parseRoadmap (the parity oracle, backend/testdata/content/*.golden.json).
// Compared SEMANTICALLY (both sides round-tripped through JSON to any) so key ORDER is not the bar,
// but key presence + values are. Covers roadmap-allkinds (every type × status, Child PRD/plan/`—`/
// missing, phase chain), roadmap-malformed (no-status / invalid-status / truncated blocks skipped),
// and roadmap-empty (productName null, epics []).
func TestParseRoadmapGolden(t *testing.T) {
	for _, name := range []string{"roadmap-allkinds", "roadmap-malformed", "roadmap-empty"} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join("..", "testdata", "content")
			content, err := os.ReadFile(filepath.Join(dir, name+".md"))
			if err != nil {
				t.Fatal(err)
			}
			productName, epics := ParseRoadmap(string(content))
			got := roundtrip(t, map[string]any{"productName": productName, "epics": epics})

			goldenBytes, err := os.ReadFile(filepath.Join(dir, name+".golden.json"))
			if err != nil {
				t.Fatal(err)
			}
			var want any
			if err := json.Unmarshal(goldenBytes, &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("parity mismatch for %s\n got: %#v\nwant: %#v", name, got, want)
			}
		})
	}
}

// roundtrip marshals v to JSON and back to a generic any, so a []RoadmapItem compares field-for-field
// against the golden without struct/interface type differences (both become map/float64/etc).
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
