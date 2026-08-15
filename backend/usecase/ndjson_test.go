package usecase

import (
	"strings"
	"testing"
)

func TestNdjsonParser_SplitAcrossReads(t *testing.T) {
	p := &NdjsonParser{}
	if got := p.Push(`{"a":1`); len(got) != 0 {
		t.Fatalf("a partial line must emit nothing, got %v", got)
	}
	got := p.Push("}\n") // the rest of the object arrives in a second read
	if len(got) != 1 || got[0].Kind != "json" {
		t.Fatalf("want exactly 1 json line after the split completes, got %v", got)
	}
}

func TestNdjsonParser_RawAndBlankLines(t *testing.T) {
	p := &NdjsonParser{}
	got := p.Push("not json\n\n{\"x\":1}\n") // raw · blank(skipped) · json
	if len(got) != 2 {
		t.Fatalf("want 2 (blank skipped), got %d: %v", len(got), got)
	}
	if got[0].Kind != "raw" || got[0].Text != "not json" {
		t.Fatalf("first must be raw 'not json', got %+v", got[0])
	}
	if got[1].Kind != "json" {
		t.Fatalf("second must be json, got %+v", got[1])
	}
}

func TestNdjsonParser_Flush(t *testing.T) {
	p := &NdjsonParser{}
	p.Push("trailing line no newline")
	got := p.Flush()
	if len(got) != 1 || got[0].Kind != "raw" || got[0].Text != "trailing line no newline" {
		t.Fatalf("flush must emit the trailing line, got %v", got)
	}
}

// E26 9.2.1 — opencode `--format json` frames (step_start/text/step_finish) pass through the generic
// engine-agnostic parser as json ParsedLines — the SSE emission path needs NO claude-shape knowledge.
func TestNdjsonParser_OpencodeFramesGeneric(t *testing.T) {
	p := &NdjsonParser{}
	got := p.Push(`{"type":"step_start","part":{"type":"step-start"}}` + "\n" +
		`{"type":"text","part":{"type":"text","text":"Slice 3 green. PRD_BUILD_COMPLETE"}}` + "\n" +
		`{"type":"step_finish","part":{"type":"step-finish","reason":"stop"}}` + "\n")
	if len(got) != 3 {
		t.Fatalf("want 3 opencode frames parsed, got %d: %v", len(got), got)
	}
	for i, pl := range got {
		if pl.Kind != "json" {
			t.Fatalf("frame %d must parse as kind=json, got %q (%s)", i, pl.Kind, string(pl.Value))
		}
	}
	if !strings.Contains(string(got[1].Value), "PRD_BUILD_COMPLETE") {
		t.Fatalf("the text frame's content must reach the stream value: %s", got[1].Value)
	}
}
