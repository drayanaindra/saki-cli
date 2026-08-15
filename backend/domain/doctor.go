package domain

// EngineReport is one engine's pre-dispatch provisioning verdict (F2 · saki doctor). All five fields
// are always present in JSON — no omitempty — because a client parses "exactly these keys" (criterion
// 1.3); Fix and Reason are simply "" when there is nothing to report.
type EngineReport struct {
	Engine  string `json:"engine"`
	Profile string `json:"profile"`
	// Status is one of "ok" | "failed" | "unknown" (mirrors the tri-state precedent in
	// src/commands/status.ts). "unknown" is reserved for a future engine whose provisioning state
	// cannot be determined (F4, claude) — codex/opencode in this slice only ever report ok/failed.
	Status string `json:"status"`
	Reason string `json:"reason"`
	// Fix is a runnable remediation command, or "" when none has been authored yet (F5, and the
	// completed codex remediation in F2 slice 2 — this slice never populates it).
	Fix string `json:"fix"`
}
