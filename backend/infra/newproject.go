package infra

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

// projectTemplates bundles the new-project starting points into the binary. The `all:` prefix is
// load-bearing — it includes dotfiles (.gitignore), which a bare //go:embed would skip. Because they
// are embedded, Go does NOT depend on apps/server/src/templates (deleted in Slice 6).
//
//go:embed all:projecttemplates
var projectTemplates embed.FS

// OSProjectFS implements usecase.ProjectFS over the real filesystem + git CLI.
type OSProjectFS struct{}

// Exists reports whether path exists (any type — parity existsSync).
func (OSProjectFS) Exists(path string) bool { return exists(path) }

// MkdirAll creates path (+ parents), parity mkdirSync{recursive:true}.
func (OSProjectFS) MkdirAll(path string) error { return os.MkdirAll(path, 0o755) }

// RemoveAll removes path recursively (best-effort rollback), parity rmSync{recursive,force}.
func (OSProjectFS) RemoveAll(path string) error { return os.RemoveAll(path) }

// CopyTemplate writes every file of the embedded template kind into dst, recreating its dir structure
// (parity cpSync{recursive}). Only reads the embed FS — never the caller's filesystem.
func (OSProjectFS) CopyTemplate(kind domain.TemplateKind, dst string) error {
	root := filepath.ToSlash(filepath.Join("projecttemplates", string(kind)))
	return fs.WalkDir(projectTemplates, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		outPath := filepath.Join(dst, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(outPath, 0o755)
		}
		data, err := projectTemplates.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return err
		}
		return os.WriteFile(outPath, data, 0o644)
	})
}

// pkgNameRE matches the top-level `"name": "<value>"` field (value may contain JSON escapes). Group 1
// is the `"name":` prefix + whitespace; the quoted value follows.
var pkgNameRE = regexp.MustCompile(`("name"\s*:\s*)"(?:[^"\\]|\\.)*"`)

// StampPackageName sets package.json's `name` to the (trimmed) folder name (parity newProject.ts:119-124).
// No-op when the template ships no package.json (e.g. `empty`). It replaces ONLY the name value in place —
// so the file's exact key order + formatting stay byte-identical to the template — rather than a
// map round-trip, which would sort keys and HTML-escape `&` → `&` (a visible divergence in every
// scaffolded project's committed package.json). Mirrors the TS JSON.parse→set→stringify, which likewise
// preserves insertion order.
func (OSProjectFS) StampPackageName(dst, name string) error {
	pkgPath := filepath.Join(dst, "package.json")
	raw, err := os.ReadFile(pkgPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !json.Valid(raw) {
		return fmt.Errorf("package.json is not valid JSON") // parity: TS JSON.parse would throw → 500
	}
	loc := pkgNameRE.FindSubmatchIndex(raw)
	if loc == nil {
		return nil // no name field to stamp (templates always ship one; guard against reformatting)
	}
	quoted, err := json.Marshal(strings.TrimSpace(name)) // safely quotes/escapes the new value
	if err != nil {
		return err
	}
	// loc = [matchStart, matchEnd, group1Start(=matchStart), group1End]. Keep everything up to the end of
	// the `"name":` prefix, splice the new quoted value, then keep the rest verbatim.
	out := make([]byte, 0, len(raw)+len(quoted))
	out = append(out, raw[:loc[3]]...)
	out = append(out, quoted...)
	out = append(out, raw[loc[1]:]...)
	return os.WriteFile(pkgPath, out, 0o644)
}

// GitInitCommit runs `git init -b main`, `git add -A`, and one initial commit in dst with a fixed
// studio identity and gpg signing off, using a CLEAN git env (drop GIT_DIR/GIT_WORK_TREE so the studio
// repo's env never leaks into the new repo). Parity newProject.ts:126-133. Each step is arg-sliced
// through exec (no shell) — no command injection.
func (OSProjectFS) GitInitCommit(dst string, kind domain.TemplateKind) error {
	steps := [][]string{
		{"init", "-b", "main"},
		{"add", "-A"},
		{
			"-c", "user.name=Pipeline Studio",
			"-c", "user.email=studio@localhost",
			"-c", "commit.gpgsign=false",
			"commit", "-m", fmt.Sprintf("Initial commit (pipeline-studio: new %s project)", kind),
		},
	}
	for _, args := range steps {
		if err := scaffoldGitStep(dst, args...); err != nil {
			return err
		}
	}
	return nil
}

// scaffoldGitStep runs `git <args>` in dir with a clean git env, bounded by a timeout (BR6). Returns
// the execErr message (git's stderr) on failure.
func scaffoldGitStep(dir string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Stdin = nil
	cmd.WaitDelay = time.Second
	cmd.Env = cleanGitEnv()
	var errb strings.Builder
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s", execErr(err, errb.String()))
	}
	return nil
}

// cleanGitEnv is the process env minus GIT_DIR / GIT_WORK_TREE (parity newProject.ts gitEnv:84).
func cleanGitEnv() []string {
	env := os.Environ()
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, "GIT_DIR=") || strings.HasPrefix(kv, "GIT_WORK_TREE=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}
