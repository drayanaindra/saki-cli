package domain

// EngineStatus is EngineReport's closed status vocabulary — named so a typo can't silently drift
// from the string literal src/types.ts mirrors, the same discipline domain.RunStatus already applies.
type EngineStatus string

const (
	StatusOK     EngineStatus = "ok"
	StatusFailed EngineStatus = "failed"
	// StatusUnknown is reserved for a future engine whose provisioning state cannot be determined
	// (F4, claude) — codex/opencode in this slice only ever report StatusOK/StatusFailed.
	StatusUnknown EngineStatus = "unknown"
)

// EngineReport is one engine's pre-dispatch provisioning verdict (F2 · saki doctor). All five fields
// are always present in JSON — no omitempty — because a client parses "exactly these keys" (criterion
// 1.3); Fix and Reason are simply "" when there is nothing to report.
type EngineReport struct {
	Engine  string       `json:"engine"`
	Profile string       `json:"profile"`
	Status  EngineStatus `json:"status"`
	Reason  string       `json:"reason"`
	// Fix is a runnable remediation command, or "" when none has been authored yet (F5 for opencode;
	// codex's complete two-line remediation is populated as of F2 slice 2, usecase.CodexInstallFix).
	Fix string `json:"fix"`
}
