package infra

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

// FileWorkflowJournal is separate from the flat run journal.  It lives below GoRunsDir, so the
// studio's other run owner cannot mistake workflow metadata for a child Run record.
type FileWorkflowJournal struct{ dir string }

func NewFileWorkflowJournal(dir string) FileWorkflowJournal {
	if dir == "" {
		dir = filepath.Join(GoRunsDir(), "workflows")
	}
	return FileWorkflowJournal{dir: dir}
}

func (j FileWorkflowJournal) EnsureWritable() error {
	if err := os.MkdirAll(j.dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(j.dir, ".write-probe-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	return os.Remove(name)
}

func (j FileWorkflowJournal) Write(w domain.Workflow) error {
	// Write is also used by small embedders and tests that do not call EnsureWritable first.
	// Creating the owned directory here keeps the journal's atomic-write guarantee true on the
	// first state transition after a clean install.
	if err := os.MkdirAll(j.dir, 0o755); err != nil {
		return err
	}
	if w.ID == "" || strings.ContainsAny(w.ID, `/\\`) || w.ID == "." || w.ID == ".." {
		return os.ErrInvalid
	}
	b, err := json.Marshal(w)
	if err != nil {
		return err
	}
	// Rename makes a crash produce either the previous complete record or the new complete record,
	// never a truncated JSON document that could be interpreted as success.
	tmp, err := os.CreateTemp(j.dir, ".workflow-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err = tmp.Write(b); err == nil {
		err = tmp.Sync()
	}
	_ = tmp.Close()
	if err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, filepath.Join(j.dir, w.ID+".json")); err != nil {
		_ = os.Remove(name)
		return err
	}
	// The file is synced above; syncing the directory makes the rename itself survive a crash on
	// filesystems that do not journal directory metadata eagerly.
	dir, err := os.Open(j.dir)
	if err != nil {
		return err
	}
	err = dir.Sync()
	_ = dir.Close()
	return err
}

func (j FileWorkflowJournal) Load() ([]domain.Workflow, error) {
	entries, err := os.ReadDir(j.dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	result := []domain.Workflow{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(j.dir, entry.Name()))
		if err != nil {
			continue
		}
		var w domain.Workflow
		// The filename is part of the record's identity. Reject a copied/renamed journal entry so
		// rehydrate cannot load two records under one id or silently trust foreign state.
		if json.Unmarshal(b, &w) == nil && w.ID == strings.TrimSuffix(entry.Name(), ".json") && w.ID != "" && !strings.ContainsAny(w.ID, `/\\`) {
			result = append(result, w)
		}
	}
	return result, nil
}
