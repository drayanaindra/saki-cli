// Package infra: shared bounded git exec (BR6) + graceful-failure message extraction (BR2).
package infra

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// execErr extracts a graceful-failure message from a failed exec, mirroring apps/server
// index.ts:127 precedence: stderr → the error string → "command failed". Go's exec does not
// surface stderr on the error the way Node does, so the caller passes it explicitly.
func execErr(err error, stderr string) string {
	if s := strings.TrimSpace(stderr); s != "" {
		return s
	}
	if err != nil {
		if s := strings.TrimSpace(err.Error()); s != "" {
			return s
		}
	}
	return "command failed"
}

// run executes name+args bounded by timeout with Stdin=nil, so a subprocess can never block on a
// prompt (BR6) — a hung/prompting tool is killed at the deadline, never hangs the request. dir=""
// inherits the process cwd (git uses -C; glab has no -C so the caller passes dir). Returns stdout,
// the combined stdout+"\n"+stderr (some tools — glab — print the result URL to stderr), and the
// execErr message on failure (else ""). WaitDelay ensures pipes close promptly after SIGKILL
// (Go 1.20+), preventing orphaned grandchildren from blocking Run().
func run(dir string, timeout time.Duration, name string, args ...string) (stdout, combined, errMsg string) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = nil
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.WaitDelay = time.Second // close pipes promptly after SIGKILL (BR6)
	var outb, errb strings.Builder
	cmd.Stdout = &outb
	cmd.Stderr = &errb
	err := cmd.Run()
	combined = outb.String() + "\n" + errb.String()
	if err != nil {
		return "", combined, execErr(err, errb.String())
	}
	return outb.String(), combined, ""
}

// runGit runs `git <args>` bounded (BR6). Returns stdout on success (errMsg==""), else the execErr
// message (git's stderr). git addresses the repo via -C, so cwd is inherited.
func runGit(timeout time.Duration, args ...string) (stdout string, errMsg string) {
	out, _, e := run("", timeout, "git", args...)
	return out, e
}
