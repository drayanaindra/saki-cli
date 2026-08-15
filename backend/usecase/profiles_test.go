package usecase

import (
	"errors"
	"testing"
)

type fakeProfileScanner struct {
	home    string
	homeErr error
	dirs    []string
}

func (f fakeProfileScanner) Home() (string, error)    { return f.home, f.homeErr }
func (f fakeProfileScanner) DirNames(string) []string { return f.dirs }

func TestProfilesList_defaultPlusSiblings(t *testing.T) {
	svc := NewProfilesService(fakeProfileScanner{
		home: "/home/me",
		dirs: []string{".claude-work", "Projects", ".claude", ".claude-alt", ".config"},
	})
	got := svc.List()
	if len(got) != 3 {
		t.Fatalf("want 3 profiles (default + 2 siblings), got %d: %+v", len(got), got)
	}
	if got[0].Name != "default" || got[0].ConfigDir != nil {
		t.Fatalf("first profile must be the default with nil ConfigDir, got %+v", got[0])
	}
	if got[1].Name != "work" || got[1].ConfigDir == nil || *got[1].ConfigDir != "/home/me/.claude-work" {
		t.Fatalf("want work sibling with full ConfigDir, got %+v", got[1])
	}
	if got[2].Name != "alt" {
		t.Fatalf("want alt sibling, got %+v", got[2])
	}
}

func TestProfilesList_gracefulOnHomeError(t *testing.T) {
	svc := NewProfilesService(fakeProfileScanner{homeErr: errors.New("no home")})
	got := svc.List()
	if len(got) != 1 || got[0].Name != "default" {
		t.Fatalf("home error must degrade to just the default profile, got %+v", got)
	}
}

func TestProfilesList_bareClaudeIgnored(t *testing.T) {
	// `.claude` (no trailing -<name>) is NOT a profile sibling — only `.claude-*` are.
	svc := NewProfilesService(fakeProfileScanner{home: "/h", dirs: []string{".claude"}})
	if got := svc.List(); len(got) != 1 {
		t.Fatalf("bare .claude must not be listed, got %+v", got)
	}
}
