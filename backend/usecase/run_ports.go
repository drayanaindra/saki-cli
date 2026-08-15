package usecase

import "github.com/drayanaindra/saki-cli/backend/domain"

// SpawnSpec is the data infra needs to start a detached run.
type SpawnSpec struct {
	ID        string
	Prompt    string
	Cwd       *string
	ConfigDir *string
	Kind      domain.RunKind
	// Engine selects the agent runtime to spawn (E26). Empty = claude (the default — a SpawnSpec
	// without an engine behaves byte-for-byte like today).
	Engine domain.RunEngine
}

// Spawner starts a detached claude -p process (the sh -c + journal-redirect contract). It returns
// the pid + a `wait` func that blocks until the process exits and returns its exit code. Spawn does
// NOT finalize — the caller journals the pid FIRST, then arms wait in a goroutine, so a fast-exit
// can never finalize (and re-journal a terminal state) before the pid reaches the journal.
type Spawner interface {
	Spawn(spec SpawnSpec) (pid int, wait func() int, err error)
}

// Journal persists a run's on-disk record + owns the out/exit paths (the durable triplet).
type Journal interface {
	EnsureWritable() error // MkdirAll + a probe; a failure means "unwritable runs dir" → 500
	Write(run domain.Run) error
	OutPath(id string) string
	ExitPath(id string) string
	// AppendOut appends a raw breadcrumb line to <id>.out (parsed kind:"raw"). Used by the build
	// engine to make its park/resume breadcrumbs durable — so a late SSE viewer, the PARKED_RE scan,
	// and GET /api/runs all see them. Safe: it runs at finalize, when the child is already dead.
	AppendOut(id, line string) error
}

// RunStore holds the Go backend's in-memory runs (non-build + build). Build auto-resume needs three
// atomic primitives the TS single-event-loop got for free: ReserveBuild (lane-atomic dedup), Update
// (test-and-clear under the lock), and a Finalize that reports whether IT transitioned the run.
type RunStore interface {
	Put(run domain.Run)
	List() []domain.Run
	Get(id string) (domain.Run, bool)
	SetPid(id string, pid int)             // in-memory pid update (preserves status/exitCode)
	Delete(id string)                      // remove a run (spawn-failure cleanup)
	Finalize(id string, exitCode int) bool // status done(0)/error(≠0); returns true only when IT transitioned running→terminal (BR2 guard)
	// ReserveBuild atomically dedups a build lane: under ONE write lock it scans for a running build
	// on laneKey and, if none, Puts run (status running). reserved==false with existingID set means a
	// build is already in flight for the lane → re-adopt it, spawn nothing (BR3 — closes the
	// check-then-spawn TOCTOU that a separate activeBuild()+Put() would leave open).
	ReserveBuild(laneKey string, run domain.Run) (existingID string, reserved bool)
	// ReserveInit is ReserveBuild's counterpart for the privileged init lane (F7 · P6 slice 4): same
	// atomic single-locked reserve, scanning kind-scoped for a RUNNING init so a double-submit re-adopts
	// the in-flight run and spawns no second --dangerously-skip-permissions process (AC 4.3). An empty
	// laneKey always Puts (register-before-spawn) and never dedupes.
	ReserveInit(laneKey string, run domain.Run) (existingID string, reserved bool)
	// Update applies mut to run id under the write lock and returns whether the run existed. mut sees a
	// pointer to a private copy stored back atomically — used for the fire-once test-and-clear
	// (only the caller whose mut observed pendingResume!=nil under the lock wins the resume).
	Update(id string, mut func(r *domain.Run)) bool
}

// Proxy reverse-proxies to apps/server for run-kinds Go does not own (build POSTs, the hosted
// POST /api/runs) and fetches its run list for the union. cookie carries the operator session.
type Proxy interface {
	Forward(method, path, cookie string, body []byte) (status int, respBody []byte, err error)
	GetRuns(cookie string) ([]map[string]any, error)
}

type Clock interface{ Now() int64 }
type IDGen interface{ New() string }

// ProcessKiller signals a run's detached process GROUP (negative-pid semantics), so a signal takes the
// `sh` group leader AND its `claude` child. SignalGroup is SIGTERM (stop + the stall watchdog's first
// strike); KillGroup is SIGKILL (the watchdog's escalation when SIGTERM was ignored); Alive is a
// liveness probe (signal 0) the stall watchdog uses to tell a hung-but-alive child from a dead one.
type ProcessKiller interface {
	SignalGroup(pid int) error // SIGTERM the group
	KillGroup(pid int) error   // SIGKILL the group (escalation after the grace)
	Alive(pid int) bool        // is the process still alive? (signal-0 probe)
}

// RehydratedRun is one journaled run reconstructed on boot, with the two disk facts the rehydrate
// policy needs: whether its pid is still alive, and its exit code if the <id>.exit sentinel exists.
type RehydratedRun struct {
	Run      domain.Run
	PidAlive bool
	ExitCode *int
	HasExit  bool
}

// JournalReader loads all journaled runs from the Go runs dir (for restart-rehydrate).
type JournalReader interface {
	Load() ([]RehydratedRun, error)
}

// FullOutput reads + parses the WHOLE <id>.out file into stream lines — the build engine needs the
// model's final spoken text at finalize (the Go store keeps no in-memory events, unlike the TS
// engine). The impl must Push(all) then append Flush() so a final line with no trailing newline (the
// terminal result/assistant text) is not dropped.
type FullOutput interface {
	ReadAll(id string) ([]ParsedLine, error)
	// Size is the current byte length of <id>.out (0 on absent/error). The stall watchdog compares it
	// against the run's last-observed size to detect fresh output WITHOUT an SSE viewer — a headless
	// build must register activity from file growth, else the 30-min watchdog false-kills it.
	Size(id string) int64
}

// ProgressFingerprint computes a build run's progress fingerprint (verified-complete slices + next
// resume target). Two equal fingerprints across consecutive resumes = no progress → the circuit
// breaker. Injectable so the engine stays decoupled from /build manifest parsing; the default returns
// nil (unknown → neutral, no streak change). Slice 2 wires the real submodule. Port of
// runManager.ts ProgressFingerprint (:438).
type ProgressFingerprint func(run domain.Run) *string

// Sleeper schedules an auto-resume successor after a backoff delay. Injectable so tests fire it
// synchronously; the default is a non-process-blocking timer. Port of runManager.ts Sleeper (:428).
type Sleeper func(delayMs int64, fire func())

// PushResult is the outcome of an auto-push (Slice 5). Skipped = a non-error no-op (no remote /
// detached HEAD); Error = the push was attempted and failed. Mirrors runManager.ts PushResult (:445-450).
type PushResult struct {
	OK      bool
	Branch  string
	Error   string
	Skipped string
}

// Pusher pushes a repo's current branch to origin on successful build completion — the "end of work"
// counterpart to auto-branch at start. NEVER --force. The impl (infra.GitCLI) applies its own bounded
// timeout, so a hung push is abandoned rather than blocking finalize. Injectable so the engine stays
// git-free. Port of runManager.ts Pusher (:451).
type Pusher interface {
	Push(cwd string) PushResult
}

// ExitWatcher reads a run's terminal disk state — used to reap a restart-survived run whose
// spawn-time finalize goroutine this process does NOT own (its child belongs to the previous
// server process, so only the durable <id>.exit / pid can tell us it finished).
type ExitWatcher interface {
	Terminal(id string, pid *int) (exitCode *int, hasExit bool, pidAlive bool)
}
