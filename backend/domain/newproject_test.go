package domain

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateName_accepts(t *testing.T) {
	for _, n := range []string{"foo", "My-App_1", "a", "a.b.c", strings.Repeat("x", 64)} {
		if got, err := ValidateName(n); err != nil || got != n {
			t.Fatalf("ValidateName(%q) = (%q,%v), want (%q,nil)", n, got, err, n)
		}
	}
}

func TestValidateName_trims(t *testing.T) {
	if got, err := ValidateName("  foo  "); err != nil || got != "foo" {
		t.Fatalf("ValidateName should trim, got (%q,%v)", got, err)
	}
}

// The traversal guard: none of these may pass — each could otherwise escape the base dir.
func TestValidateName_rejectsTraversalAndBad(t *testing.T) {
	bad := []string{"", "   ", "..", "../etc", "a/b", "a\\b", ".hidden", "-leading", "_leading", "/abs", strings.Repeat("x", 65), "a b"}
	for _, n := range bad {
		if _, err := ValidateName(n); err == nil {
			t.Fatalf("ValidateName(%q) must be rejected", n)
		} else if err.Code != CodeInvalid {
			t.Fatalf("ValidateName(%q) code = %q, want INVALID", n, err.Code)
		}
	}
}

func TestExpandBase(t *testing.T) {
	home := "/home/me"
	cases := []struct {
		base string
		want string
		bad  bool
	}{
		{"~", home, false},
		{"~/Projects", filepath.Join(home, "Projects"), false},
		{"/abs/path", "/abs/path", false},
		{"", "", true},
		{"   ", "", true},
		{"relative/path", "", true},
		{"./rel", "", true},
	}
	for _, tc := range cases {
		got, err := ExpandBase(tc.base, home)
		if tc.bad {
			if err == nil {
				t.Fatalf("ExpandBase(%q) should error", tc.base)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Fatalf("ExpandBase(%q) = (%q,%v), want (%q,nil)", tc.base, got, err, tc.want)
		}
	}
}

func TestResolveTarget(t *testing.T) {
	home := "/home/me"
	got, err := ResolveTarget("~/Projects", "app", home)
	if err != nil || got != filepath.Join(home, "Projects", "app") {
		t.Fatalf("ResolveTarget = (%q,%v)", got, err)
	}
	// A bad name is rejected before any base work.
	if _, err := ResolveTarget("~/Projects", "../escape", home); err == nil || err.Code != CodeInvalid {
		t.Fatalf("traversal name must be INVALID, got %v", err)
	}
	// A bad base is rejected too.
	if _, err := ResolveTarget("relative", "app", home); err == nil {
		t.Fatalf("relative base must be rejected")
	}
}

func TestIsTemplateKind(t *testing.T) {
	for _, k := range []string{"empty", "vite-react-ts", "nextjs"} {
		if !IsTemplateKind(k) {
			t.Fatalf("IsTemplateKind(%q) should be true", k)
		}
	}
	for _, k := range []string{"", "django", "EMPTY", "../etc"} {
		if IsTemplateKind(k) {
			t.Fatalf("IsTemplateKind(%q) should be false", k)
		}
	}
}
