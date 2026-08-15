package usecase

import (
	"testing"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

type fakeEnvStateFS struct {
	home         string
	marker       *domain.EnvMarker
	hasClaudeDir bool
	hasClaudeMd  bool
}

func (f fakeEnvStateFS) Home() string                        { return f.home }
func (f fakeEnvStateFS) ReadMarker(string) *domain.EnvMarker { return f.marker }
func (f fakeEnvStateFS) HasClaudeDir(string) bool            { return f.hasClaudeDir }
func (f fakeEnvStateFS) HasClaudeMd(string) bool             { return f.hasClaudeMd }

func TestEnvStateClassify(t *testing.T) {
	home := "/home/me"
	cases := []struct {
		name string
		fs   fakeEnvStateFS
		want domain.EnvState
	}{
		{"marker matches → mine", fakeEnvStateFS{home: home, marker: &domain.EnvMarker{Config: home}, hasClaudeDir: true}, domain.EnvMine},
		{"foreign .claude → foreign", fakeEnvStateFS{home: home, hasClaudeDir: true}, domain.EnvForeign},
		{"nothing → none", fakeEnvStateFS{home: home}, domain.EnvNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NewEnvStateService(tc.fs).Classify("/repo"); got != tc.want {
				t.Fatalf("Classify = %q, want %q", got, tc.want)
			}
		})
	}
}
