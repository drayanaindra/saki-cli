package infra

import "syscall"

// OSKiller signals a detached run's process GROUP. The child was spawned with Setpgid, so `sh` is the
// group leader (pgid == pid); signalling the NEGATIVE pid takes the whole group (the `claude` child
// too). Implements usecase.ProcessKiller.
type OSKiller struct{}

// SignalGroup SIGTERMs the group (stop + the stall watchdog's first strike).
func (OSKiller) SignalGroup(pid int) error {
	return syscall.Kill(-pid, syscall.SIGTERM)
}

// KillGroup SIGKILLs the group — the watchdog's escalation when a hung child ignored SIGTERM (so it
// can't stay "running" forever, blocking finalize → blocking resume).
func (OSKiller) KillGroup(pid int) error {
	return syscall.Kill(-pid, syscall.SIGKILL)
}

// Alive reports whether pid is a live process (signal 0 = existence check). pid<=1 is never alive
// (0/1 would broadcast/hit init — guarded like the stop path).
func (OSKiller) Alive(pid int) bool {
	if pid <= 1 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}
