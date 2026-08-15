package domain

// Run statuses (mirror apps/server RunStatus).
const (
	StatusRunning = "running"
	StatusDone    = "done"
	StatusError   = "error"
)

// RunEngine is the agent runtime a run executes on (E26). "claude" is the default and the only engine
// the pre-E26 code path knows; "opencode" and "codex" are the second-class runtimes. Empty means claude.
type RunEngine string

// Engine constants (journaled on the Run — the resume/successor path re-feeds it, so an opencode run
// never silently resumes on claude — E26 §10 rule 2).
const (
	EngineClaude   RunEngine = "claude"
	EngineOpencode RunEngine = "opencode"
	// EngineCodex is the OpenAI Codex CLI (`codex exec`). It resolves a namespaced saki command
	// straight from the message like claude does — NOT through opencode's `--command` split (spiked
	// against codex-cli 0.147.0; see tasks/codex-engine-plan.md §2 S1).
	EngineCodex RunEngine = "codex"
)

// ResolveEngine maps an empty/unset engine string to the claude default.
func ResolveEngine(engine RunEngine) RunEngine {
	if engine == "" {
		return EngineClaude
	}
	return engine
}

// RunKind is the lane a run belongs to (mirrors apps/server RunKind). Only "build" is owned
// by apps/server (+ its auto-resume engine, Non-Goal 2); every other kind is Go-owned this MVP.
type RunKind string

// IsBuild reports whether this run is a build (spawned via the Go auto-resume engine rather than the
// plain run service).
func (k RunKind) IsBuild() bool { return k == "build" }

// IsInit reports whether this run is an /init-env run. As of F7 (P6 slice 4) init is Go-owned + spawned
// locally with --dangerously-skip-permissions (the only kind that gets the elevated flag), so the Go
// backend binds 127.0.0.1 and the init route is loopback+origin guarded — the elevated flag is
// unreachable off-host and cross-origin. BR5 / §10 rule 5. (Before F7 slice 4 init was reverse-proxied
// to apps/server behind its auth gate; that forward is now retired.)
func (k RunKind) IsInit() bool { return k == "init" }

// Meta is the lane tag a reloaded UI re-adopts a run by.
type Meta struct {
	Kind    RunKind `json:"kind"`
	LaneKey string  `json:"laneKey"`
}

// PendingResume is a durable record of an auto-resume scheduled to fire at ResumeAt, carrying the
// chain state its successor must inherit. Persisted in the journal so a restart during the (possibly
// hours-long) backoff/limit wait still fires it. Mirrors runManager.ts PendingResume (:101-108).
type PendingResume struct {
	SuccessorID      string `json:"successorId"`
	ResumeAt         int64  `json:"resumeAt"`
	ResumeCount      int    `json:"resumeCount"`
	NoProgressStreak int    `json:"noProgressStreak"`
	FirstStartedAt   int64  `json:"firstStartedAt"`
	SpawnAttempts    int    `json:"spawnAttempts,omitempty"` // bounded re-arm when the successor spawn throws
}

// RedriveCause is why a finished build run ended — drives the terminal/retryable/park decision.
// Kind ∈ {"exit","user-stop","interrupted","spawn-error"}; ExitCode is set only for "exit".
// Mirrors runManager.ts RedriveCause (:212-216).
type RedriveCause struct {
	Kind     string
	ExitCode *int
}

// RedriveCause kinds.
const (
	CauseExit        = "exit"
	CauseUserStop    = "user-stop"
	CauseInterrupted = "interrupted"
	CauseSpawnError  = "spawn-error"
)

// Run is a spawned claude -p run owned by the Go backend. As of F6 this includes build runs, which
// additionally carry the auto-resume chain-state (ResumeCount … PendingResume). The non-chain fields
// are shared by every kind; the chain fields are meaningful only for kind:"build".
type Run struct {
	ID        string  `json:"id"`
	Status    string  `json:"status"`
	Prompt    string  `json:"prompt"`
	Cwd       *string `json:"cwd"`
	StartedAt int64   `json:"startedAt"`
	Meta      *Meta   `json:"meta"`
	Pid       *int    `json:"pid"`
	ExitCode  *int    `json:"exitCode"`

	// Engine is the agent runtime this run executes on (E26) — journaled so a restart / auto-resume /
	// manual Continue re-feeds the same engine (never a silent re-spawn on claude). Empty = claude.
	Engine RunEngine `json:"engine,omitempty"`

	// Build auto-resume chain-state (durable; meaningful only for kind:"build").
	ConfigDir        *string        `json:"configDir,omitempty"`        // Claude profile the run spawned under (nil = process default)
	ResumeCount      int            `json:"resumeCount,omitempty"`      // how many times this build is a resume of an earlier turn (0 = first)
	NoProgressStreak int            `json:"noProgressStreak,omitempty"` // consecutive no-progress resumes (Slice 2 wires the fingerprint)
	FirstStartedAt   int64          `json:"firstStartedAt,omitempty"`   // when the FIRST run of the chain started (wall-clock budget origin)
	PendingResume    *PendingResume `json:"pendingResume,omitempty"`    // a scheduled-but-unspawned successor (durable across restart)
	PushedAt         *int64         `json:"pushedAt,omitempty"`         // when the branch was auto-pushed on completion (Slice 5; guards double-push)
	IdempotencyKey   *string        `json:"idempotencyKey,omitempty"`   // client key that launched this run (post-restart dedupe, Slice 6)

	// Transient (NOT journaled) — recomputed each process lifetime.
	StartFingerprint *string `json:"-"` // progress fingerprint at this run's start (Slice 2)
	LastActivityAt   int64   `json:"-"` // last time fresh output was observed (stall watchdog, Slice 3)
	LastOutSize      int64   `json:"-"` // last observed <id>.out byte size — growth = viewerless activity (Slice 3)
	StallKilledAt    *int64  `json:"-"` // when the stall watchdog SIGTERM'd it (Slice 3)
	StopRequested    bool    `json:"-"` // an operator Stop was requested → onExit must not auto-resume (BR2)
}
