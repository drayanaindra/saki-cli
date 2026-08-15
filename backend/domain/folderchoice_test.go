package domain

import "testing"

func TestParseFolderChoice(t *testing.T) {
	cases := []struct {
		name   string
		code   int
		stdout string
		stderr string
		want   FolderChoice
	}{
		{"success trims path", 0, "  /Users/me/repo\n", "", FolderChoice{Path: "/Users/me/repo"}},
		{"success empty stdout → error", 0, "  \n", "", FolderChoice{Error: "empty selection"}},
		{"user canceled text → canceled", 1, "", "execution error: User canceled. (-128)", FolderChoice{Canceled: true}},
		{"bare -128 → canceled", 1, "", "something -128 here", FolderChoice{Canceled: true}},
		{"other failure → stderr message", 1, "", "  boom\n", FolderChoice{Error: "boom"}},
		{"failure empty stderr → exit fallback", 2, "", "", FolderChoice{Error: "osascript exit 2"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseFolderChoice(tc.code, tc.stdout, tc.stderr)
			if got != tc.want {
				t.Fatalf("ParseFolderChoice = %+v, want %+v", got, tc.want)
			}
		})
	}
}
