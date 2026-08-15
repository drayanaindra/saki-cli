package infra

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestOSKiller_SignalGroup(t *testing.T) {
	// spawn a detached `sleep` in its OWN process group (like a real run's sh), then SIGTERM the
	// group and confirm the process dies.
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	go func() { _ = cmd.Wait() }() // reap so it doesn't zombie

	if err := (OSKiller{}).SignalGroup(pid); err != nil {
		t.Fatalf("SignalGroup: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil { // ESRCH → dead
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("process was not killed by SignalGroup")
}
