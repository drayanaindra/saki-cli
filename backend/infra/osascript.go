package infra

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"
)

// OSAScript implements usecase.OSAScriptRunner via the macOS `osascript` CLI. Only invoked on darwin
// (the service gates the platform), but compiles + is inert on any OS.
type OSAScript struct{}

// chooseFolderScript is the AppleScript that pops the native folder picker and prints the POSIX path
// (parity index.ts:842).
const chooseFolderScript = `POSIX path of (choose folder with prompt "Select a repo for Saki Studio")`

// ChooseFolder execs the picker bounded by a 120 s timeout (parity with the TS execFile timeout) and
// returns its exit code + stdout + stderr for domain.ParseFolderChoice. A non-exit failure (binary
// missing, timeout) surfaces as code 1 with the error text on stderr, mirroring the TS `err ? 1 : 0`.
func (OSAScript) ChooseFolder() (int, string, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "osascript", "-e", chooseFolderScript)
	cmd.Stdin = nil
	var outb, errb strings.Builder
	cmd.Stdout = &outb
	cmd.Stderr = &errb
	err := cmd.Run()
	if err == nil {
		return 0, outb.String(), errb.String()
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), outb.String(), errb.String()
	}
	// Non-exit failure (spawn error / timeout): mirror the TS `err ? 1 : 0`, surfacing the message.
	stderr := errb.String()
	if stderr == "" {
		stderr = err.Error()
	}
	return 1, outb.String(), stderr
}
