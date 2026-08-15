package infra

import (
	"os"
	"path/filepath"
)

// OSProfileScanner implements usecase.ProfileScanner over the real filesystem: the operator's home
// dir + its immediate subdirectories. Read-only.
type OSProfileScanner struct{}

// Home returns the operator's home dir (os.UserHomeDir — parity with Node os.homedir()).
func (OSProfileScanner) Home() (string, error) { return os.UserHomeDir() }

// DirNames returns the names of entries directly under home that resolve to directories. os.Stat
// (not the DirEntry) is used so a symlink-to-dir counts as a dir — parity with the TS statSync().
// isDirectory(). Unreadable home or unstattable entries are skipped (graceful, never errors).
func (OSProfileScanner) DirNames(home string) []string {
	entries, err := os.ReadDir(home)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		info, err := os.Stat(filepath.Join(home, e.Name()))
		if err != nil || !info.IsDir() {
			continue
		}
		names = append(names, e.Name())
	}
	return names
}
