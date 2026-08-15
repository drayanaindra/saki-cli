package domain

import "testing"

func TestRepoValid(t *testing.T) {
	if !(Repo{Dir: "/x"}).Valid() {
		t.Fatal("non-empty dir should be valid")
	}
	if (Repo{Dir: ""}).Valid() {
		t.Fatal("empty dir should be invalid (cwd required)")
	}
}
