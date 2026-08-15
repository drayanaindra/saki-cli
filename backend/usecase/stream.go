package usecase

import (
	"context"
	"time"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

// RunOutput reads new bytes from a run's <id>.out file, starting at a saved offset (the tail).
type RunOutput interface {
	ReadFrom(id string, offset int64) (data []byte, newOffset int64, err error)
}

// StreamService tails a Go-owned run's output and drives the SSE stream: it emits a StreamEvent per
// parsed line (buffered-then-live), then an end signal once the run finalizes and all output is
// drained. The HTTP framing/flushing lives in the adapter; this owns the tail loop (testable).
type StreamService struct {
	store  RunStore
	output RunOutput
	clock  Clock
	poll   time.Duration
}

func NewStreamService(store RunStore, output RunOutput, clock Clock, poll time.Duration) StreamService {
	if poll <= 0 {
		poll = 150 * time.Millisecond
	}
	return StreamService{store: store, output: output, clock: clock, poll: poll}
}

// Stream tails run id: emit is called for every event (replay + live), end once when the run
// finalizes and the output is fully drained. It returns on end OR when ctx is cancelled (the client
// disconnected) — the caller cleans up. A double-drain at finalize catches bytes written between the
// last read and the exit sentinel; Flush emits a trailing line with no newline.
func (s StreamService) Stream(ctx context.Context, id string, emit func(StreamEvent), end func(status string, exit *int)) {
	parser := &NdjsonParser{}
	var offset int64
	seq := 0
	drain := func() {
		data, newOff, err := s.output.ReadFrom(id, offset)
		if err != nil || len(data) == 0 {
			return
		}
		offset = newOff
		for _, pl := range parser.Push(string(data)) {
			emit(StreamEvent{Seq: seq, Ts: s.clock.Now(), Line: pl})
			seq++
		}
	}
	ticker := time.NewTicker(s.poll)
	defer ticker.Stop()
	for {
		drain()
		run, ok := s.store.Get(id)
		if !ok || run.Status != domain.StatusRunning {
			drain() // catch bytes written between the last read and finalize
			for _, pl := range parser.Flush() {
				emit(StreamEvent{Seq: seq, Ts: s.clock.Now(), Line: pl})
				seq++
			}
			status, exit := domain.StatusDone, (*int)(nil)
			if ok {
				status, exit = run.Status, run.ExitCode
			}
			end(status, exit)
			return
		}
		select {
		case <-ctx.Done():
			return // client disconnected
		case <-ticker.C:
		}
	}
}
