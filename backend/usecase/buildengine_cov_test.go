package usecase

import (
	"strings"
	"testing"
)

func TestDefaultBuildConfig(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		t.Setenv("SAKI_MAX_BUILD_RESUMES", "")
		t.Setenv("SAKI_RESUME_DELAY_MS", "")
		t.Setenv("SAKI_BUILD_MAX_WALL_MS", "")
		t.Setenv("SAKI_BUILD_LIMIT_WAIT_MS", "")
		t.Setenv("SAKI_BUILD_STALL_MS", "")
		t.Setenv("SAKI_BUILD_AUTOPUSH", "")

		cfg := DefaultBuildConfig()
		if cfg.MaxBuildResumes != 40 {
			t.Fatalf("MaxBuildResumes=%d, want 40", cfg.MaxBuildResumes)
		}
		if cfg.ResumeBaseMs != 5000 {
			t.Fatalf("ResumeBaseMs=%d, want 5000", cfg.ResumeBaseMs)
		}
		if cfg.MaxBackoffMs != 300000 {
			t.Fatalf("MaxBackoffMs=%d, want 300000", cfg.MaxBackoffMs)
		}
		if cfg.NoProgressLimit != 3 {
			t.Fatalf("NoProgressLimit=%d, want 3", cfg.NoProgressLimit)
		}
		if cfg.MaxWallMs != 21600000 {
			t.Fatalf("MaxWallMs=%d, want 21600000", cfg.MaxWallMs)
		}
		if !cfg.AutoPush {
			t.Fatalf("AutoPush=false, want true")
		}
	})

	t.Run("env overrides", func(t *testing.T) {
		t.Setenv("SAKI_MAX_BUILD_RESUMES", "10")
		t.Setenv("SAKI_BUILD_MAX_WALL_MS", "5000")
		t.Setenv("SAKI_BUILD_AUTOPUSH", "0")

		cfg := DefaultBuildConfig()
		if cfg.MaxBuildResumes != 10 {
			t.Fatalf("MaxBuildResumes=%d, want 10", cfg.MaxBuildResumes)
		}
		if cfg.MaxWallMs != 5000 {
			t.Fatalf("MaxWallMs=%d, want 5000", cfg.MaxWallMs)
		}
		if cfg.AutoPush {
			t.Fatalf("AutoPush=true, want false")
		}
	})

	t.Run("invalid env falls back", func(t *testing.T) {
		t.Setenv("SAKI_MAX_BUILD_RESUMES", "abc")
		cfg := DefaultBuildConfig()
		if cfg.MaxBuildResumes != 40 {
			t.Fatalf("MaxBuildResumes=%d, want 40", cfg.MaxBuildResumes)
		}
	})

	t.Run("negative env falls back", func(t *testing.T) {
		t.Setenv("SAKI_RESUME_DELAY_MS", "-1")
		cfg := DefaultBuildConfig()
		if cfg.ResumeBaseMs != 5000 {
			t.Fatalf("ResumeBaseMs=%d, want 5000", cfg.ResumeBaseMs)
		}
	})
}

func TestParkedReasonOf_coverage(t *testing.T) {
	t.Run("no output returns empty", func(t *testing.T) {
		r := parkedReasonOf(nil)
		if r != "" {
			t.Fatalf("expected empty, got %q", r)
		}
	})

	t.Run("blank line returns empty", func(t *testing.T) {
		r := parkedReasonOf([]ParsedLine{{Kind: "raw", Text: "some random text"}})
		if r != "" {
			t.Fatalf("expected empty, got %q", r)
		}
	})

	t.Run("parked reason line returns the matched text", func(t *testing.T) {
		lines := []ParsedLine{
			{Kind: "raw", Text: "auto-resume: resume budget exhausted (40) — parked for operator"},
		}
		r := parkedReasonOf(lines)
		if r == "" {
			t.Fatal("expected a parked reason")
		}
	})

	t.Run("BLOCKED line returns the matched text", func(t *testing.T) {
		lines := []ParsedLine{
			{Kind: "raw", Text: "BLOCKED: no verified progress after 3 auto-resumes — parked for operator"},
		}
		r := parkedReasonOf(lines)
		if !strings.Contains(r, "BLOCKED") {
			t.Fatalf("expected BLOCKED in reason, got %q", r)
		}
	})

	t.Run("park line returns the matched text", func(t *testing.T) {
		lines := []ParsedLine{
			{Kind: "raw", Text: "auto-resume: authentication error — parked; use Continue to resume"},
		}
		r := parkedReasonOf(lines)
		if r == "" {
			t.Fatal("expected a park reason")
		}
	})
}
