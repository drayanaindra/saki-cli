package infra

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

// errIsDir is returned by Open for a directory path (the usecase maps it to 404, never a byte stream).
var errIsDir = errors.New("is a directory")

// ContentFS implements the F4 content ports against the real filesystem — the read ports
// (usecase.RoadmapFS + usecase.WorkItemsFS) plus the WriteFile of usecase.ContentWriter (slice 3).
// Faithful port of the fs helpers in apps/server/src/workitems.ts (walkMarkdown bounds + skip rules)
// and roadmap.ts (exists/read). R1 holds by interface SEGREGATION, not by absence: the board read
// services take the read-only ports (which have no WriteFile), so they can never mutate a file; only
// LockService takes the ContentWriter.
type ContentFS struct{}

// Exists reports whether path exists (a dir OR a file), mirroring existsSync.
func (ContentFS) Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Read returns (content, true) or ("", false) when unreadable — mirrors the TS safeRead try/catch.
func (ContentFS) Read(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(b), true
}

// WriteFile writes content to path (create/truncate, 0644), satisfying usecase.ContentWriter — the
// slice-3 lock write. Mirrors apps/server's writeFileSync(absPath, next, 'utf8'). The caller
// (LockService) has already cwd-contained the path via domain.ValidateLockRequest (R3).
func (ContentFS) WriteFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0644)
}

// Open opens path for binary streaming, returning an io.ReadSeekCloser + its mod time — the NEW
// byte-serving path the proto/screenshot serve routes need (Read returns a UTF-8 string and cannot
// stream binary .png faithfully). A directory or unreadable path yields an error, which the usecase
// maps to 404 (never 500). The caller MUST Close the returned reader. Satisfies usecase.ProtoServeFS.
func (ContentFS) Open(path string) (io.ReadSeekCloser, time.Time, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, time.Time{}, err
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, time.Time{}, err
	}
	if fi.IsDir() {
		_ = f.Close()
		return nil, time.Time{}, errIsDir
	}
	return f, fi.ModTime(), nil
}

// MTimeMs returns the file's mod time in millis since epoch, or 0 on error (mirrors safeMtimeMs).
func (ContentFS) MTimeMs(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.ModTime().UnixMilli()
}

// ListDir returns the base names directly under dir (empty on any error); never recurses.
func (ContentFS) ListDir(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// Walk bounds + skip rules (parity with workitems.ts walkMarkdown).
const (
	walkMaxDepth = 6
	walkMaxFiles = 2000
)

var walkIgnoreDirs = map[string]bool{
	"node_modules": true, "dist": true, "build": true, "out": true, "coverage": true,
	"vendor": true, "venv": true, "__pycache__": true, "target": true, "tmp": true,
}

type walkFrame struct {
	dir   string
	depth int
}

// WalkMarkdown recursively collects *.md paths under cwd — skips noise/dot dirs, caps depth + count,
// and never follows symlinked directories (cycle/escape guard). Absolute paths, matching join(cwd,…).
func (ContentFS) WalkMarkdown(cwd string) []string {
	out := []string{}
	stack := []walkFrame{{dir: cwd, depth: 0}}
	for len(stack) > 0 && len(out) < walkMaxFiles {
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		entries, err := os.ReadDir(top.dir)
		if err != nil {
			continue
		}
		stack, out = walkDir(top, entries, stack, out)
	}
	return out
}

// walkDir folds one directory's entries into the walk: pushes each walkable child dir onto stack and
// collects each .md file into out (capped at walkMaxFiles). Extracted from WalkMarkdown verbatim.
func walkDir(top walkFrame, entries []os.DirEntry, stack []walkFrame, out []string) ([]walkFrame, []string) {
	for _, e := range entries {
		if e.Type()&os.ModeSymlink != 0 {
			continue
		}
		if e.IsDir() {
			if child := walkableChildDir(top, e); child != nil {
				stack = append(stack, *child)
			}
		} else if strings.HasSuffix(e.Name(), ".md") {
			out = append(out, filepath.Join(top.dir, e.Name()))
			if len(out) >= walkMaxFiles {
				break
			}
		}
	}
	return stack, out
}

// walkableChildDir returns the frame to descend into for a directory entry, or nil to skip it (past
// max depth, a dot dir, or a walkIgnoreDirs name). Extracted from WalkMarkdown verbatim.
func walkableChildDir(top walkFrame, e os.DirEntry) *walkFrame {
	if top.depth >= walkMaxDepth {
		return nil
	}
	name := e.Name()
	if strings.HasPrefix(name, ".") || walkIgnoreDirs[name] {
		return nil
	}
	return &walkFrame{dir: filepath.Join(top.dir, name), depth: top.depth + 1}
}

// shotDirs are the screenshot roots /qa-generated Playwright tests write to (parity with
// screenshots.ts:5 SHOT_DIRS). The screenshot walk is scoped to these three, NOT the whole repo.
var shotDirs = []string{"test-results", "playwright-report", "screenshots"}

// walkShotsDepth is the screenshot walk's max subdirectory depth below a SHOT_DIR (parity with
// screenshots.ts:41 `if (depth > 4) return`).
const walkShotsDepth = 4

// WalkShots collects every .png under cwd's three SHOT_DIRS, capped at depth 4, each with its mod
// time. This is a NEW depth-4 / .png / NO-ignore-list walk — deliberately NOT WalkMarkdown (depth-6 /
// .md / walkIgnoreDirs), which would be a parity bug (screenshots must be found under test-results/
// even though it's an ignore-dir for markdown). Directory-symlinks are not followed (os.DirEntry
// IsDir is lstat-based, mirroring TS Dirent.isDirectory()). Faithful port of screenshots.ts findScreenshots' walk.
func (ContentFS) WalkShots(cwd string) []domain.ShotFile {
	root := filepath.Clean(cwd)
	out := []domain.ShotFile{}
	for _, d := range shotDirs {
		walkShots(filepath.Join(root, d), 0, &out)
	}
	return out
}

func walkShots(dir string, depth int, out *[]domain.ShotFile) {
	if depth > walkShotsDepth {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		full := filepath.Join(dir, e.Name())
		if e.IsDir() {
			walkShots(full, depth+1, out)
		} else if e.Type().IsRegular() && strings.HasSuffix(strings.ToLower(e.Name()), ".png") {
			fi, err := os.Stat(full)
			if err != nil {
				continue // skip unreadable, mirroring the TS statSync try/catch
			}
			*out = append(*out, domain.ShotFile{Path: full, MTimeMs: fi.ModTime().UnixMilli()})
		}
	}
}

// GitCommitVerifier implements usecase.CommitVerifier: a SHA resolves in cwd OR an immediate child
// repo (cheap `git cat-file -e <sha>^{commit}`). Faithful port of workitems.ts realVerifiers +
// gitReposUnder. Never throws — a non-repo dir just yields false.
type GitCommitVerifier struct{}

// CommitExists reports whether sha resolves to a commit in cwd or a child repo.
func (GitCommitVerifier) CommitExists(cwd, sha string) bool {
	for _, r := range gitReposUnder(cwd) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := exec.CommandContext(ctx, "git", "-C", r, "cat-file", "-e", sha+"^{commit}")
		cmd.Stdin = nil
		err := cmd.Run()
		cancel()
		if err == nil {
			return true
		}
	}
	return false
}

// gitReposUnder is cwd plus its immediate child dirs holding a `.git` entry (dir OR file — a
// worktree's .git is a file). Skips node_modules/dotdirs; capped at 60 scanned entries.
func gitReposUnder(cwd string) []string {
	repos := []string{cwd}
	entries, err := os.ReadDir(cwd)
	if err != nil {
		return repos
	}
	scanned := 0
	for _, e := range entries {
		if scanned >= 60 {
			break
		}
		if !e.IsDir() || e.Name() == "node_modules" || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		scanned++
		if _, err := os.Stat(filepath.Join(cwd, e.Name(), ".git")); err == nil {
			repos = append(repos, filepath.Join(cwd, e.Name()))
		}
	}
	return repos
}
