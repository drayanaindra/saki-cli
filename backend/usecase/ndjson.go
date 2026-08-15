package usecase

import (
	"encoding/json"
	"strings"
)

// ParsedLine mirrors apps/server streamParser.ts ParsedLine — a stream line is either valid JSON or
// raw text (claude stderr/warnings aren't JSON but must still reach the UI, never dropped).
type ParsedLine struct {
	Kind  string          `json:"kind"`            // "json" | "raw"
	Value json.RawMessage `json:"value,omitempty"` // when kind == "json"
	Text  string          `json:"text,omitempty"`  // when kind == "raw"
}

// StreamEvent mirrors apps/server RunEvent (runManager.ts:35) — the shape each SSE `data:` frame
// carries, consumed by the browser's useRunStream.
type StreamEvent struct {
	Seq  int        `json:"seq"`
	Ts   int64      `json:"ts"`
	Line ParsedLine `json:"line"`
}

// NdjsonParser parses newline-delimited JSON, tolerating chunks that split a line across two reads
// (the cross-read buffering). Port of streamParser.ts:NdjsonParser.
type NdjsonParser struct{ buf string }

// Push appends a chunk and returns the complete lines it now contains (a trailing partial line stays
// buffered until the next Push or Flush).
func (p *NdjsonParser) Push(chunk string) []ParsedLine {
	p.buf += chunk
	var out []ParsedLine
	for {
		nl := strings.IndexByte(p.buf, '\n')
		if nl == -1 {
			break
		}
		line := strings.TrimSuffix(p.buf[:nl], "\r")
		p.buf = p.buf[nl+1:]
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, parseLine(line))
	}
	return out
}

// Flush emits any trailing buffered content (a final line with no newline).
func (p *NdjsonParser) Flush() []ParsedLine {
	line := p.buf
	p.buf = ""
	if strings.TrimSpace(line) == "" {
		return nil
	}
	return []ParsedLine{parseLine(line)}
}

func parseLine(line string) ParsedLine {
	if json.Valid([]byte(line)) {
		return ParsedLine{Kind: "json", Value: json.RawMessage(line)}
	}
	return ParsedLine{Kind: "raw", Text: line}
}
