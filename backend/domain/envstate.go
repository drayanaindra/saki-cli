package domain

// EnvState classifies a repo's Claude working-environment readiness so the studio can decide whether
// to run /init-env before a first build. Faithful port of apps/server/src/envState.ts.
type EnvState string

const (
	// EnvNone — no .claude/ AND no CLAUDE.md (uninitialized).
	EnvNone EnvState = "none"
	// EnvForeign — a .claude/ or CLAUDE.md exists but no marker matching THIS config.
	EnvForeign EnvState = "foreign"
	// EnvMine — a marker present whose config === this home (initialized by THIS config).
	EnvMine EnvState = "mine"
)

// EnvMarker is the parsed `.claude/.env-init.json` written by /init-env (config = the initializing
// machine's $HOME). Fields are optional — only Config is load-bearing for classification.
type EnvMarker struct {
	Tool      string `json:"tool"`
	Config    string `json:"config"`
	Version   int    `json:"version"`
	CreatedAt string `json:"createdAt"`
}

// ClassifyEnvState is the pure classifier (parity envState.ts:32): a marker whose config matches this
// home is 'mine'; no .claude/ and no CLAUDE.md is 'none'; anything else (a foreign .claude/ carried in
// from another config) is 'foreign'. marker is nil when the marker file is absent or unparseable.
func ClassifyEnvState(marker *EnvMarker, home string, hasClaudeDir, hasClaudeMd bool) EnvState {
	// home != "" guards the degenerate no-$HOME case: an empty-config marker must not match an empty
	// home and spuriously classify as 'mine' (Node's homedir() never returns "" — this only bites if
	// os.UserHomeDir errors).
	if marker != nil && home != "" && marker.Config == home {
		return EnvMine
	}
	if !hasClaudeDir && !hasClaudeMd {
		return EnvNone
	}
	return EnvForeign
}
