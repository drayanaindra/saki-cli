package domain

import "testing"

func TestClassifyEnvState(t *testing.T) {
	home := "/home/me"
	cases := []struct {
		name         string
		marker       *EnvMarker
		hasClaudeDir bool
		hasClaudeMd  bool
		want         EnvState
	}{
		{"marker matches home → mine", &EnvMarker{Config: home}, true, true, EnvMine},
		{"marker config mismatch → foreign", &EnvMarker{Config: "/other"}, true, false, EnvForeign},
		{"no marker, .claude/ present → foreign", nil, true, false, EnvForeign},
		{"no marker, CLAUDE.md present → foreign", nil, false, true, EnvForeign},
		{"no marker, nothing present → none", nil, false, false, EnvNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyEnvState(tc.marker, home, tc.hasClaudeDir, tc.hasClaudeMd); got != tc.want {
				t.Fatalf("ClassifyEnvState = %q, want %q", got, tc.want)
			}
		})
	}

	// Degenerate no-$HOME edge: an empty-config marker must NOT match an empty home as 'mine'.
	t.Run("empty home + empty-config marker → foreign (not mine)", func(t *testing.T) {
		if got := ClassifyEnvState(&EnvMarker{Config: ""}, "", true, false); got != EnvForeign {
			t.Fatalf("empty home must not classify as mine, got %q", got)
		}
	})
}
