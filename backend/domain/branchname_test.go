package domain

import (
	"strings"
	"testing"
)

func TestValidateBranchName_rejects(t *testing.T) {
	cases := []struct {
		name, in, wantSub string
	}{
		{"empty", "", "non-empty"},
		{"space", "feat x", "whitespace"},
		{"tab", "feat\tx", "whitespace"},
		{"leading dash", "-rf", `start with "-"`},
		{"leading slash", "/feat", `start or end with "/"`},
		{"trailing slash", "feat/", `start or end with "/"`},
		{"dotdot", "a..b", `contain ".."`},
		{"tilde", "fe~at", "invalid character"},
		{"caret", "fe^at", "invalid character"},
		{"colon", "fe:at", "invalid character"},
		{"question", "fe?at", "invalid character"},
		{"star", "fe*at", "invalid character"},
		{"bracket", "fe[at", "invalid character"},
		{"backslash", `fe\at`, "invalid character"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateBranchName(c.in)
			if err == nil {
				t.Fatalf("want reject for %q, got nil", c.in)
			}
			if !strings.Contains(err.Error(), c.wantSub) {
				t.Fatalf("msg %q missing %q", err.Error(), c.wantSub)
			}
		})
	}
}

func TestValidateBranchName_accepts(t *testing.T) {
	for _, ok := range []string{"feature/go-git-ops", "fix-123", "main", "a.b", "release_2"} {
		if err := ValidateBranchName(ok); err != nil {
			t.Fatalf("want accept %q, got %v", ok, err)
		}
	}
}
