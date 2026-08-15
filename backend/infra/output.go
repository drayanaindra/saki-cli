package infra

import (
	"io"
	"os"

	"github.com/drayanaindra/saki-cli/backend/usecase"
)

// FileOutput reads a run's <id>.out file incrementally at a saved offset (implements
// usecase.RunOutput). A not-yet-created .out returns no new bytes (offset unchanged), not an error,
// so the tail loop keeps polling until the run writes.
type FileOutput struct{ journal FileJournal }

func NewFileOutput(journal FileJournal) FileOutput { return FileOutput{journal: journal} }

// ReadAll reads + parses the WHOLE <id>.out into stream lines (implements usecase.FullOutput) — the
// build engine's finalize needs the model's final spoken text (the Go store keeps no in-memory
// events). It Pushes all bytes then appends Flush() so the terminal result/assistant line, which
// often lacks a trailing newline, is not left buffered (mirrors the stream double-drain).
func (o FileOutput) ReadAll(id string) ([]usecase.ParsedLine, error) {
	b, err := os.ReadFile(o.journal.OutPath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // no output yet — nothing to classify
		}
		return nil, err
	}
	p := &usecase.NdjsonParser{}
	lines := p.Push(string(b))
	return append(lines, p.Flush()...), nil
}

// Size is the current byte length of <id>.out (0 when it doesn't exist yet or can't be stat'd). The
// stall watchdog uses growth in this value as the viewerless activity signal.
func (o FileOutput) Size(id string) int64 {
	fi, err := os.Stat(o.journal.OutPath(id))
	if err != nil {
		return 0
	}
	return fi.Size()
}

func (o FileOutput) ReadFrom(id string, offset int64) ([]byte, int64, error) {
	f, err := os.Open(o.journal.OutPath(id))
	if err != nil {
		return nil, offset, nil // .out not written yet — no new bytes, keep tailing
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, offset, err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, offset, err
	}
	return data, offset + int64(len(data)), nil
}
