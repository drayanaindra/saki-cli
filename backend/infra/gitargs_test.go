package infra

import (
	"reflect"
	"testing"
)

func TestParseBranchList(t *testing.T) {
	got := ParseBranchList("main\n  feature/x \n\n\ttrunk\n")
	want := []string{"main", "feature/x", "trunk"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if l := ParseBranchList(""); len(l) != 0 {
		t.Fatalf("empty input want [], got %v", l)
	}
}

func TestSwitchArgs(t *testing.T) {
	if got := SwitchArgs("/r", "feat", true); !reflect.DeepEqual(got, []string{"-C", "/r", "switch", "-c", "feat"}) {
		t.Fatalf("create: %v", got)
	}
	if got := SwitchArgs("/r", "feat", false); !reflect.DeepEqual(got, []string{"-C", "/r", "switch", "--", "feat"}) {
		t.Fatalf("existing: %v", got)
	}
}

func TestMrArgs(t *testing.T) {
	got := MrArgs("feat", "main")
	want := []string{"mr", "create", "--fill", "--yes", "--source-branch", "feat", "--target-branch", "main"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
}

func TestMergeArgs(t *testing.T) {
	got := MergeArgs("/r", "feat")
	want := []string{"-C", "/r", "merge", "--no-ff", "feat"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
}

func TestFirstURL(t *testing.T) {
	if got := FirstURL("done: https://gitlab.com/x/y/-/merge_requests/3 (open)"); got != "https://gitlab.com/x/y/-/merge_requests/3" {
		t.Fatalf("%q", got)
	}
	if got := FirstURL("no url here"); got != "" {
		t.Fatalf("want empty, got %q", got)
	}
}
