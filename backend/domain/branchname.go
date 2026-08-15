package domain

import (
	"errors"
	"strings"
	"unicode"
)

// ValidateBranchName is the BR5 injection guard for a NEW branch name (the create path).
// It runs BEFORE any git command, so a name git would refuse — or, critically, a leading
// "-" that exec would pass to git as a flag — never reaches the shell. Messages mirror
// apps/server/src/git.ts:19-25 so operator feedback is unchanged. Returns nil when valid.
func ValidateBranchName(name string) error {
	switch {
	case name == "" || hasWhitespace(name):
		return errors.New("branch name must be non-empty and contain no whitespace")
	case strings.HasPrefix(name, "-"):
		return errors.New(`branch name must not start with "-"`)
	case strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/"):
		return errors.New(`branch name must not start or end with "/"`)
	case strings.Contains(name, ".."):
		return errors.New(`branch name must not contain ".."`)
	case strings.ContainsAny(name, `~^:?*[\`):
		return errors.New(`branch name contains an invalid character (~ ^ : ? * [ \)`)
	}
	return nil
}

func hasWhitespace(s string) bool {
	for _, r := range s {
		if unicode.IsSpace(r) {
			return true
		}
	}
	return false
}
