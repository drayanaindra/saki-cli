package infra

import (
	"regexp"
	"strings"
)

// ParseBranchList splits `git branch --format=%(refname:short)` output into trimmed,
// non-empty names (parity with apps/server/src/git.ts parseBranchList).
func ParseBranchList(out string) []string {
	names := []string{}
	for _, line := range strings.Split(out, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			names = append(names, t)
		}
	}
	return names
}

// SwitchArgs builds the argv for `git switch` (parity with git.ts buildSwitchArgs). create →
// `-C <dir> switch -c <branch>` (NO `--`: `switch -c -- <name>` mis-parses); existing →
// `-C <dir> switch -- <branch>` (the `--` guards a branch name that looks like a flag).
func SwitchArgs(dir, branch string, create bool) []string {
	if create {
		return []string{"-C", dir, "switch", "-c", branch}
	}
	return []string{"-C", dir, "switch", "--", branch}
}

// MrArgs builds the argv for `glab mr create` (parity with git.ts buildMrArgs). --fill pushes the
// branch + fills title/body from commits; --yes skips all prompts → fully non-interactive.
func MrArgs(source, target string) []string {
	return []string{"mr", "create", "--fill", "--yes", "--source-branch", source, "--target-branch", target}
}

// MergeArgs builds the argv for a LOCAL merge (parity with git.ts buildMergeArgs): `-C <dir> merge
// --no-ff <source>`. --no-ff forces a merge commit so the feature's integration is recorded (never
// fast-forwarded away). <source> is resolved from the current branch, never operator free-text.
func MergeArgs(dir, source string) []string {
	return []string{"-C", dir, "merge", "--no-ff", source}
}

var urlRe = regexp.MustCompile(`https?://\S+`)

// FirstURL returns the first http(s) URL in text (glab prints the created MR URL), else "".
func FirstURL(text string) string { return urlRe.FindString(text) }
