package domain

import (
	"fmt"
	"regexp"
	"strings"
)

// FolderChoice is the outcome of a native macOS folder picker: exactly one of Path / Canceled / Error
// is meaningful. Faithful port of apps/server/src/folderPicker.ts.
type FolderChoice struct {
	Path     string
	Canceled bool
	Error    string
}

// userCanceledRe matches osascript's cancel signal on stderr (parity with the TS /User canceled|-128/i).
var userCanceledRe = regexp.MustCompile(`(?i)User canceled|-128`)

// ParseFolderChoice maps an `osascript "choose folder"` result into a FolderChoice (parity
// folderPicker.ts:8): exit 0 with a path → Path (empty stdout → "empty selection" error); a cancel
// signal on stderr → Canceled; otherwise the trimmed stderr (or an "osascript exit N" fallback) as Error.
func ParseFolderChoice(code int, stdout, stderr string) FolderChoice {
	if code == 0 {
		if p := strings.TrimSpace(stdout); p != "" {
			return FolderChoice{Path: p}
		}
		return FolderChoice{Error: "empty selection"}
	}
	if userCanceledRe.MatchString(stderr) {
		return FolderChoice{Canceled: true}
	}
	if msg := strings.TrimSpace(stderr); msg != "" {
		return FolderChoice{Error: msg}
	}
	return FolderChoice{Error: fmt.Sprintf("osascript exit %d", code)}
}
