package domain

import (
	"path/filepath"
	"testing"
)

func TestResolveProtoRel(t *testing.T) {
	cwd := "/repo"
	cases := []struct {
		name       string
		rel        string
		wantStatus int
		wantAbs    string
	}{
		{"html at repo tasks", "tasks/proto-foo/preview.html", 0, "/repo/tasks/proto-foo/preview.html"},
		{"png sibling", "tasks/proto-foo/slice2-desktop.png", 0, "/repo/tasks/proto-foo/slice2-desktop.png"},
		{"monorepo subpkg", "edbot-panel/tasks/proto-x/preview.html", 0, "/repo/edbot-panel/tasks/proto-x/preview.html"},
		{"no extension", "tasks/proto-foo/preview", 422, ""},
		{"not a proto path", "tasks/other/file.html", 422, ""},
		{"nested file segment forbidden", "tasks/proto-foo/sub/file.png", 422, ""},
		{"leading ../ escapes cwd", "../tasks/proto-foo/preview.html", 422, ""},
		{"absolute rel", "/etc/passwd", 422, ""},
		{"empty rel", "", 422, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			abs, status := ResolveProtoRel(cwd, c.rel)
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

func TestResolveProtoRel_emptyCwd(t *testing.T) {
	if _, status := ResolveProtoRel("", "tasks/proto-foo/preview.html"); status != 422 {
		t.Fatalf("empty cwd status = %d, want 422", status)
	}
}
