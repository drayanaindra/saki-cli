package domain

import (
	"path/filepath"
	"testing"
)

func TestResolveScreenshotRel(t *testing.T) {
	cwd := "/repo"
	cases := []struct {
		name       string
		rel        string
		wantStatus int
		wantAbs    string
	}{
		{"png at repo test-results", "test-results/a/shot.png", 0, "/repo/test-results/a/shot.png"},
		{"png case-insensitive suffix", "screenshots/UP.PNG", 0, "/repo/screenshots/UP.PNG"},
		{"not a png", "test-results/a/shot.jpg", 422, ""},
		{"no extension", "test-results/a/shot", 422, ""},
		{"leading ../ escapes cwd", "../secrets/shot.png", 422, ""},
		{"absolute rel escapes", "/etc/shot.png", 422, ""},
		{"empty rel", "", 422, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			abs, status := ResolveScreenshotRel(cwd, c.rel)
			if status != c.wantStatus {
				t.Fatalf("status = %d, want %d (rel=%q)", status, c.wantStatus, c.rel)
			}
			if c.wantAbs != "" && abs != filepath.Clean(c.wantAbs) {
				t.Fatalf("abs = %q, want %q", abs, c.wantAbs)
			}
			if c.wantStatus != 0 && abs != "" {
				t.Fatalf("rejected rel must yield empty abs, got %q", abs)
			}
		})
	}
}

func TestResolveScreenshotRel_emptyCwd(t *testing.T) {
	if _, status := ResolveScreenshotRel("", "screenshots/shot.png"); status != 422 {
		t.Fatalf("empty cwd status = %d, want 422", status)
	}
}
