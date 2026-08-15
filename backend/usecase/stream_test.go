package usecase

import (
	"context"
	"testing"
	"time"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

func intPtr(n int) *int { return &n }

// scriptOutput yields a canned sequence of read chunks, then EOF (empty).
type scriptOutput struct {
	chunks []string
	i      int
}

func (o *scriptOutput) ReadFrom(_ string, offset int64) ([]byte, int64, error) {
	if o.i >= len(o.chunks) {
		return nil, offset, nil
	}
	c := o.chunks[o.i]
	o.i++
	return []byte(c), offset + int64(len(c)), nil
}

func TestStream_ReplayAndEndFinalizedRun(t *testing.T) {
	st := newMemStore()
	st.Put(domain.Run{ID: "r1", Status: domain.StatusDone, ExitCode: intPtr(0)})
	ss := NewStreamService(st, &scriptOutput{chunks: []string{"{\"a\":1}\n{\"b\":2}\n"}}, fixedClock{}, time.Millisecond)

	var events []StreamEvent
	var endStatus string
	ss.Stream(context.Background(), "r1",
		func(e StreamEvent) { events = append(events, e) },
		func(status string, _ *int) { endStatus = status })

	if len(events) != 2 {
		t.Fatalf("want 2 events, got %d", len(events))
	}
	if events[0].Seq != 0 || events[1].Seq != 1 {
		t.Fatalf("seq not monotonic: %d, %d", events[0].Seq, events[1].Seq)
	}
	if endStatus != domain.StatusDone {
		t.Fatalf("end status %q", endStatus)
	}
}

func TestStream_LiveThenFinalize(t *testing.T) {
	st := newMemStore()
	st.Put(domain.Run{ID: "r1", Status: domain.StatusRunning})
	ss := NewStreamService(st, &scriptOutput{chunks: []string{"{\"a\":1}\n", "{\"b\":2}\n"}}, fixedClock{}, time.Millisecond)

	go func() { time.Sleep(5 * time.Millisecond); st.Finalize("r1", 0) }() // run finalizes mid-stream

	var events []StreamEvent
	ended := false
	ss.Stream(context.Background(), "r1",
		func(e StreamEvent) { events = append(events, e) },
		func(string, *int) { ended = true })

	if len(events) < 2 || !ended {
		t.Fatalf("want ≥2 events + end, got %d events ended=%v", len(events), ended)
	}
}

func TestStream_ClientDisconnectReturns(t *testing.T) {
	st := newMemStore()
	st.Put(domain.Run{ID: "r1", Status: domain.StatusRunning}) // never finalizes
	ss := NewStreamService(st, &scriptOutput{chunks: []string{"{\"a\":1}\n"}}, fixedClock{}, time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(10 * time.Millisecond); cancel() }() // client disconnects

	done := make(chan bool, 1)
	go func() {
		ss.Stream(ctx, "r1", func(StreamEvent) {}, func(string, *int) {})
		done <- true
	}()
	select {
	case <-done: // returned on ctx cancel — no leaked goroutine
	case <-time.After(2 * time.Second):
		t.Fatal("Stream did not return on client disconnect")
	}
}
