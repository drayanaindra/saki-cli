package domain

import "testing"

func TestRunKindIsBuild(t *testing.T) {
	if !RunKind("build").IsBuild() {
		t.Fatal("build must be a build kind")
	}
	for _, k := range []string{"generate", "review", "proto", "pickup", "", "rebuild"} {
		if RunKind(k).IsBuild() {
			t.Fatalf("%q must NOT be a build kind (Go owns it)", k)
		}
	}
}

func TestRunKindIsInit(t *testing.T) {
	// F7 (P6 slice 4): init is the only kind that gets --dangerously-skip-permissions; the handler
	// keys the elevated local spawn + loopback/origin guard on it.
	if !RunKind("init").IsInit() {
		t.Fatal("init must be an init kind")
	}
	for _, k := range []string{"generate", "review", "proto", "pickup", "roadmap", "rplan", "epic", "build", ""} {
		if RunKind(k).IsInit() {
			t.Fatalf("%q must NOT be an init kind (no elevated flag)", k)
		}
	}
}
