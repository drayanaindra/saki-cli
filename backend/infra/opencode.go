package infra

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/drayanaindra/saki-cli/backend/usecase"
)

// ErrOpencodePluginMissing is returned by OpencodePluginProof when the opencode profile does not
// carry the saki-builder plugin (E26 9.4.2) — a loud refusal, never a silent no-op run.
var ErrOpencodePluginMissing = errors.New("opencode profile does not resolve @saketek/saki-builder")

// ErrUnresolvableCommand is returned when a prompt OPENS with `/` (so the operator meant a command)
// but does not parse as one. On opencode the fallback — sending it as a plain message — is a
// known-dead path: the raw slash text reaches the model, which answers "isn't a recognized command",
// and the run no-ops on a clean exit 0. Refusing loudly keeps that failure visible instead of
// re-creating the very defect the `--command` invocation closes.
var ErrUnresolvableCommand = errors.New("prompt opens with '/' but does not parse as an opencode command")

// sakiBuilderPlugin is the opencode plugin the studio's /saki-builder:* commands resolve through
// (declared in the profile's opencode.json `plugin` array — the operator's own profile does).
const sakiBuilderPlugin = "@saketek/saki-builder"

// OpencodePluginProof proves, by reading config/plugin presence (NEVER a run exit code — rule 4),
// that the opencode profile an opencode spawn will use carries the saki-builder plugin. The config
// path is resolved EXACTLY the way the spawned child resolves it (buildSpawnEnv): a pinned
// configDir becomes XDG_CONFIG_HOME=<dir> → opencode reads <dir>/opencode/opencode.json(c); with no
// pinned profile the child drops any inherited OPENCODE_CONFIG/XDG_CONFIG_HOME → opencode reads
// ~/.config/opencode/opencode.json(c). The proof therefore ignores the parent process's inherited
// opencode env vars too — validating the SAME profile that actually runs, never a foreign one.
func OpencodePluginProof(configDir *string) error {
	path := opencodeConfigPath(configDir)
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%w: %w: config %s unreadable: %v — run:\n%s", usecase.ErrEngineNotProvisioned, ErrOpencodePluginMissing, path, err, usecase.OpencodeInstallFix)
	}
	var cfg struct {
		Plugin []string `json:"plugin"`
	}
	// Try strict JSON first (the common shape — including the standard `$schema` URL, which a naive
	// comment-stripper would truncate at its `//`); fall back to JSONC-tolerant parsing only when
	// strict parse fails (a config that actually carries comments).
	if err := json.Unmarshal(raw, &cfg); err != nil {
		if err := json.Unmarshal(stripJSONC(raw), &cfg); err != nil {
			return fmt.Errorf("%w: %w: config %s unparseable: %v — run:\n%s", usecase.ErrEngineNotProvisioned, ErrOpencodePluginMissing, path, err, usecase.OpencodeInstallFix)
		}
	}
	for _, p := range cfg.Plugin {
		if strings.TrimSpace(p) == sakiBuilderPlugin {
			return nil
		}
	}
	return fmt.Errorf("%w: %w: %s lists plugins %v — run:\n%s", usecase.ErrEngineNotProvisioned, ErrOpencodePluginMissing, path, cfg.Plugin, usecase.OpencodeInstallFix)
}

// opencodeConfigPath resolves the opencode config file the SPAWNED CHILD reads (buildSpawnEnv sets
// XDG_CONFIG_HOME=<configDir> for a pinned profile, and drops inherited opencode env vars otherwise,
// so opencode always ends at <configDir>/opencode or ~/.config/opencode).
func opencodeConfigPath(configDir *string) string {
	var base string
	if configDir != nil && *configDir != "" {
		base = filepath.Join(*configDir, "opencode")
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return "opencode.json" // best-effort; ReadFile fails loudly downstream
		}
		base = filepath.Join(home, ".config", "opencode")
	}
	for _, name := range []string{"opencode.json", "opencode.jsonc"} {
		p := filepath.Join(base, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return filepath.Join(base, "opencode.json")
}

// stripJSONC removes `//` and `/* */` comments from JSONC, STRING-AWARE: a `//` or `/*` inside a
// double-quoted string literal is data, not a comment (the standard `"$schema": "https://…/…"`
// would otherwise be truncated mid-URL). Only reached when strict JSON parse failed.
func stripJSONC(raw []byte) []byte {
	out := make([]byte, 0, len(raw))
	inBlock, inLine, inString := false, false, false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		next := byte(0)
		if i+1 < len(raw) {
			next = raw[i+1]
		}
		switch {
		case inString:
			out = append(out, c)
			if c == '\\' && i+1 < len(raw) {
				out = append(out, raw[i+1])
				i++
			} else if c == '"' {
				inString = false
			}
		case inBlock:
			if c == '*' && next == '/' {
				inBlock = false
				i++
			}
		case inLine:
			if c == '\n' {
				inLine = false
				out = append(out, c)
			}
		case c == '"':
			inString = true
			out = append(out, c)
		case c == '/' && next == '*':
			inBlock = true
			i++
		case c == '/' && next == '/':
			inLine = true
			i++
		default:
			out = append(out, c)
		}
	}
	return out
}
