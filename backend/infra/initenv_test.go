package infra

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drayanaindra/saki-cli/backend/domain"
	"github.com/drayanaindra/saki-cli/backend/usecase"
)

// writeFakeCodex installs an executable `codex` on PATH whose BODY the caller supplies. Permitted by
// PRD §9 rule 6: these tests assert argv, env and error plumbing ONLY — never that a plugin was
// really registered, which no fake can prove (that claim belongs to e2e/codex-init-env.spec.ts).
func writeFakeCodex(t *testing.T, body string) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// Criterion 1.4's filesystem half — the claim a usecase test with a fake provisioner CANNOT make,
// because the fake touches no disk and the assertion would be vacuously true. This drives the REAL
// EngineProvisioner with an empty PATH, so it is the component that could create files being tested.
// Same construction as TestDoctorService_Check_LeavesFilesystemUntouched.
func TestEngineProvisionerMissingBinaryWritesNothing(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no codex anywhere
	profile := filepath.Join(t.TempDir(), "p2")

	changed, err := EngineProvisioner{}.Provision(usecase.ProvisionRequest{
		Cwd: t.TempDir(), Engine: domain.EngineCodex, Profile: &profile,
	})

	if !errors.Is(err, usecase.ErrBinaryNotFound) {
		t.Fatalf("err = %v, want it to wrap ErrBinaryNotFound", err)
	}
	if !strings.Contains(err.Error(), "(codex)") {
		t.Errorf("err = %q, want the binary NAMED so the operator knows what to install", err)
	}
	if changed {
		t.Error("changed = true with no binary; nothing ran")
	}
	if _, statErr := os.Stat(profile); !os.IsNotExist(statErr) {
		t.Fatalf("%s exists after a binary-less provision; want nothing created", profile)
	}
}

// 🔒 BR4 + the fixed-argv rule. Asserts the child sees the EXACT argv from usecase's single engine
// mapping, that its EFFECTIVE CODEX_HOME is the one CodexSkillsProof will read, and that the other
// engines' namespaces were shed. The foreign vars are PLANTED first — an absence assertion on a clean
// shell would pass even with scrubProfileEnv deleted entirely.
func TestEngineProvisionerUsesFixedArgvAndScrubbedEnv(t *testing.T) {
	out := filepath.Join(t.TempDir(), "record")
	t.Setenv("SAKI_TEST_RECORD", out)
	// Foreign engine markers + a poisoned CODEX_HOME that must NOT win over the selected profile.
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("CLAUDE_CODE_MESSAGING_TOKEN", "must-not-leak")
	t.Setenv("OPENCODE", "1")
	t.Setenv("CODEX_HOME", "/should-not-win")
	// Record argv, then the child's EFFECTIVE view of the three vars that matter.
	writeFakeCodex(t, `printf 'argv: %s\n' "$*" >> "$SAKI_TEST_RECORD"
printf 'CODEX_HOME=%s\n' "$CODEX_HOME" >> "$SAKI_TEST_RECORD"
printf 'CLAUDECODE=%s|CLAUDE_CODE_MESSAGING_TOKEN=%s|OPENCODE=%s\n' "$CLAUDECODE" "$CLAUDE_CODE_MESSAGING_TOKEN" "$OPENCODE" >> "$SAKI_TEST_RECORD"
exit 0
`)
	profile := t.TempDir()

	if _, err := (EngineProvisioner{}).Provision(usecase.ProvisionRequest{
		Cwd: t.TempDir(), Engine: domain.EngineCodex, Profile: &profile,
	}); err != nil {
		t.Fatalf("provision failed: %v", err)
	}

	recorded, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	got := string(recorded)

	for _, vec := range usecase.CodexProvisionArgv {
		want := "argv: " + strings.Join(vec[1:], " ")
		if !strings.Contains(got, want) {
			t.Errorf("child argv missing %q\nrecorded:\n%s", want, got)
		}
	}
	// The profile the installer writes to must be byte-identical to the one the proof reads — the
	// PRD's kill criterion is setup and proof disagreeing about which home ran.
	wantHome := "CODEX_HOME=" + codexHomePath(&profile)
	if !strings.Contains(got, wantHome) {
		t.Errorf("child CODEX_HOME is not the proven home; want %q\nrecorded:\n%s", wantHome, got)
	}
	if strings.Contains(got, "/should-not-win") {
		t.Error("an inherited CODEX_HOME overrode the selected profile")
	}
	if !strings.Contains(got, "CLAUDECODE=|CLAUDE_CODE_MESSAGING_TOKEN=|OPENCODE=") {
		t.Errorf("foreign engine namespaces reached the codex child\nrecorded:\n%s", got)
	}
}

// An UNPINNED provision must shed an inherited CODEX_HOME, so the operator's environment cannot
// redirect the write to a profile other than the one the proof then validates (~/.codex).
func TestEngineProvisionerUnpinnedShedsInheritedCodexHome(t *testing.T) {
	out := filepath.Join(t.TempDir(), "record")
	t.Setenv("SAKI_TEST_RECORD", out)
	t.Setenv("CODEX_HOME", "/hijacked")
	writeFakeCodex(t, `printf 'CODEX_HOME=[%s]\n' "$CODEX_HOME" >> "$SAKI_TEST_RECORD"`+"\nexit 0\n")

	if _, err := (EngineProvisioner{}).Provision(usecase.ProvisionRequest{
		Cwd: t.TempDir(), Engine: domain.EngineCodex,
	}); err != nil {
		t.Fatalf("provision failed: %v", err)
	}

	recorded, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(recorded), "/hijacked") {
		t.Fatalf("unpinned provision inherited CODEX_HOME: %s", recorded)
	}
}

// Every command runs even after an earlier one fails, and the FIRST error is the one reported — the
// repeat-run shape where `marketplace add` says "already added" but `plugin add` still must run.
func TestEngineProvisionerRunsEveryCommandAndKeepsTheFirstError(t *testing.T) {
	out := filepath.Join(t.TempDir(), "record")
	t.Setenv("SAKI_TEST_RECORD", out)
	writeFakeCodex(t, `printf '%s\n' "$2" >> "$SAKI_TEST_RECORD"
if [ "$2" = "marketplace" ]; then echo "already added" >&2; exit 1; fi
echo "second failed too" >&2; exit 3
`)
	profile := t.TempDir()

	_, err := EngineProvisioner{}.Provision(usecase.ProvisionRequest{
		Cwd: t.TempDir(), Engine: domain.EngineCodex, Profile: &profile,
	})

	if err == nil || !strings.Contains(err.Error(), "already added") {
		t.Fatalf("err = %v, want the FIRST command's message", err)
	}
	recorded, readErr := os.ReadFile(out)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if n := strings.Count(string(recorded), "\n"); n != len(usecase.CodexProvisionArgv) {
		t.Fatalf("ran %d commands, want all %d even after the first failed\n%s",
			n, len(usecase.CodexProvisionArgv), recorded)
	}
}

// BR6: a child that never returns is KILLED at the deadline rather than hanging the request goroutine
// for the daemon's lifetime — `codex plugin marketplace add` does network I/O and there is no
// `saki init-env --stop`.
func TestEngineProvisionerKillsAHangingChildAtTheDeadline(t *testing.T) {
	original := provisionTimeout
	provisionTimeout = 300 * time.Millisecond
	t.Cleanup(func() { provisionTimeout = original })
	writeFakeCodex(t, "sleep 60\n")
	profile := t.TempDir()

	start := time.Now()
	_, err := EngineProvisioner{}.Provision(usecase.ProvisionRequest{
		Cwd: t.TempDir(), Engine: domain.EngineCodex, Profile: &profile,
	})
	elapsed := time.Since(start)

	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want a timeout", err)
	}
	// Both commands time out in sequence, so allow two deadlines plus WaitDelay slack.
	if budget := 3 * time.Second; elapsed > budget {
		t.Fatalf("took %s, want the child killed well inside %s", elapsed, budget)
	}
}

// PRD §12: a third-party child's output must not flow unbounded into an HTTP response — it can carry
// profile contents or a credentialed registry URL, unlike doctor's fixed-message contract.
func TestSummarizeChildOutputIsBoundedToOneLine(t *testing.T) {
	secret := strings.Repeat("A", maxProvisionMessage*4) + "AUTH_TOKEN=hunter2"
	got := summarizeChildOutput("\n\nfirst line\n"+secret, errors.New("exit 1"))

	if got != "first line" {
		t.Fatalf("got %q, want only the first non-empty line", got)
	}

	long := summarizeChildOutput(secret, errors.New("exit 1"))
	if len(long) > maxProvisionMessage+len("… (truncated)") {
		t.Fatalf("message is %d bytes, want it capped near %d", len(long), maxProvisionMessage)
	}
	if strings.Contains(long, "AUTH_TOKEN") {
		t.Fatal("a trailing secret survived truncation")
	}

	if fallback := summarizeChildOutput("   \n\n", errors.New("boom")); fallback != "boom" {
		t.Fatalf("empty output should fall back to the error, got %q", fallback)
	}
}

// The non-codex branch is unreachable through the service (slice 1 gates it), but the adapter keeps
// its own guard so a future caller cannot reach an engine mapping that does not exist.
func TestEngineProvisionerRefusesEnginesWithoutAMapping(t *testing.T) {
	writeFakeCodex(t, "exit 0\n")
	for _, engine := range []domain.RunEngine{domain.EngineOpencode, domain.EngineClaude} {
		if _, err := (EngineProvisioner{}).Provision(usecase.ProvisionRequest{
			Cwd: t.TempDir(), Engine: engine,
		}); !errors.Is(err, usecase.ErrInitEnvUnsupported) {
			t.Errorf("%s: err = %v, want ErrInitEnvUnsupported", engine, err)
		}
	}
}
