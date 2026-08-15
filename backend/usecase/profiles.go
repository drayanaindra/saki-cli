package usecase

import (
	"path/filepath"
	"strings"
)

// Profile is a discovered Claude config profile the studio's picker offers (parity ClaudeProfile,
// index.ts:315). ConfigDir is nil for the default profile (no CLAUDE_CONFIG_DIR override needed).
type Profile struct {
	Name      string  `json:"name"`
	ConfigDir *string `json:"configDir"`
}

// ProfileScanner is the read-only home-dir surface the profiles usecase needs (implemented by
// infra.OSProfileScanner). DirNames returns the SUBDIRECTORY names directly under home (symlinks
// followed, parity with statSync().isDirectory()); a scan error yields an empty slice (graceful).
type ProfileScanner interface {
	Home() (string, error)
	DirNames(home string) []string
}

// ProfilesService lists the operator's Claude profiles: the built-in `default` plus any sibling
// `~/.claude-<name>` directory. Faithful port of listClaudeProfiles (index.ts:320).
type ProfilesService struct {
	fs ProfileScanner
}

// NewProfilesService wires a read-only ProfileScanner into the service.
func NewProfilesService(fs ProfileScanner) ProfilesService {
	return ProfilesService{fs: fs}
}

// List returns the profiles newest-discovered after the always-present default. Graceful: an
// unreadable home yields just the default entry (never an error — parity with the TS try/catch).
func (s ProfilesService) List() []Profile {
	profiles := []Profile{{Name: "default", ConfigDir: nil}}
	home, err := s.fs.Home()
	if err != nil {
		return profiles
	}
	const prefix = ".claude-"
	for _, entry := range s.fs.DirNames(home) {
		if !strings.HasPrefix(entry, prefix) {
			continue
		}
		full := filepath.Join(home, entry)
		profiles = append(profiles, Profile{Name: strings.TrimPrefix(entry, prefix), ConfigDir: &full})
	}
	return profiles
}
