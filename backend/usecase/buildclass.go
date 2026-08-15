package usecase

import (
	"encoding/json"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

// Outcome is how a finished build run should be treated. Port of the classifyOutcome return union
// (runManager.ts:319).
type Outcome string

const (
	OutcomeTerminal  Outcome = "terminal"  // done / gave up — never auto-resume
	OutcomeRetryable Outcome = "retryable" // ended its turn early / crashed — auto-resume under the caps
	OutcomePark      Outcome = "park"      // ambiguous or persistent failure — stop, needs the operator
	OutcomeAwaiting  Outcome = "awaiting"  // paused on a human decision — resumable once answered
)

// Line-anchored sentinel matchers — matched ONLY at line-start (multiline) against the model's FINAL
// spoken text, so a sentinel the /build skill prints as its OWN line counts but the model merely
// NARRATING one mid-sentence does not. Faithful port of runManager.ts:165, :169, :275, :386.
var (
	buildTerminalRe     = regexp.MustCompile(`(?m)^\s*(PRD_BUILD_COMPLETE|HARD STOP|BLOCKED:)`)
	buildSuccessRe      = regexp.MustCompile(`(?m)^\s*PRD_BUILD_COMPLETE`)
	needsDecisionLineRe = regexp.MustCompile(`(?m)^\s*NEEDS_DECISION:\s*(\{.*\})\s*$`)
	parkedRe            = regexp.MustCompile(`(?im)^\s*(BLOCKED:|HARD STOP)|parked for operator|parked;`)
)

// AwaitingDecision is a human-decision gate a build paused on (a NEEDS_DECISION sentinel). Rendered
// as the stream picker. Mirrors runManager.ts AwaitingDecision (:264-269).
type AwaitingDecision struct {
	Kind     string   `json:"kind"` // "fork" | "action"
	Question string   `json:"question"`
	Options  []string `json:"options"`
	Slice    *int     `json:"slice,omitempty"`
}

// streamMsg is the minimal stream-json shape FinalSpokenText inspects. Mirrors runManager.ts StreamMsg.
// E26: `Part` carries opencode's `text`-frame content (spike-captured in the PRD §2 — content at
// `part.text`).
type streamMsg struct {
	Type    string          `json:"type"`
	Result  json.RawMessage `json:"result"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	Content json.RawMessage `json:"content"`
	Part    struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"part"`
	// Item carries codex's `item.completed` payload. Codex `exec --json` speaks a THIRD dialect: the
	// model's spoken text arrives as an `item.completed` frame whose `item.type` is "agent_message"
	// (tool work arrives on the same frame type as "command_execution"/"file_change", which is why the
	// inner type must be checked and not just the outer one).
	Item struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"item"`
}

// FinalSpokenText returns the model's final spoken text for a run: the stream-json `result` event's
// text if present, else the last assistant `text` block; nil when the run produced no spoken text.
// We look ONLY at what the model SAID — never thinking/tool noise, which can mention a sentinel
// without it being real. Faithful port of runManager.ts finalSpokenText (:236-258).
func FinalSpokenText(lines []ParsedLine) *string {
	var result, lastAssistant *string
	for _, l := range lines {
		if l.Kind != "json" {
			continue
		}
		var v streamMsg
		if json.Unmarshal(l.Value, &v) != nil {
			continue
		}
		result, lastAssistant = applyStreamMsg(v, result, lastAssistant)
	}
	if result != nil {
		return result
	}
	return lastAssistant
}

// applyStreamMsg folds one stream message into the running (result, lastAssistant): a `result` event
// sets the final result text; an `assistant` event remembers its latest text block; an opencode `text`
// event ACCUMULATES its content line-oriented (a sentinel split across frames still matches
// line-anchored — E26 9.3.3). Extracted from FinalSpokenText verbatim.
func applyStreamMsg(v streamMsg, result, lastAssistant *string) (*string, *string) {
	switch v.Type {
	case "result":
		if s, ok := asString(v.Result); ok {
			result = &s
		}
	case "assistant":
		content := v.Message.Content
		if len(content) == 0 {
			content = v.Content
		}
		if s := assistantText(content); s != nil {
			lastAssistant = s
		}
	case "text":
		// E26 — opencode `--format json` `text` frames ARE the model's spoken text (content at
		// `part.text`). Accumulate newline-separated so line-anchored sentinels still match across
		// frames. Claude never emits a `text` type, so this path is opencode-only (rule 5).
		if t := v.Part.Text; t != "" {
			if lastAssistant != nil {
				t = *lastAssistant + "\n" + t
			}
			lastAssistant = &t
		}
	case "item.completed":
		// codex `exec --json` — an `agent_message` item IS a COMPLETE model message (unlike opencode's
		// `text` chunks), so each one REPLACES the previous rather than accumulating. That matches
		// claude's lastAssistant semantics: the final spoken message is what carries the sentinel, and a
		// build's PRD_BUILD_COMPLETE lands in the last agent_message (verified against codex-cli 0.147.0).
		// Neither claude nor opencode emits `item.completed`, so this path is codex-only (rule 5).
		if v.Item.Type == "agent_message" && v.Item.Text != "" {
			t := v.Item.Text
			lastAssistant = &t
		}
	}
	return result, lastAssistant
}

// assistantText extracts an assistant message's text: a plain string, or the joined text blocks of a
// content array (mirrors runManager.ts:245-254). Returns nil when there is no text.
func assistantText(content json.RawMessage) *string {
	if len(content) == 0 {
		return nil
	}
	if s, ok := asString(content); ok {
		return &s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &blocks) != nil {
		return nil
	}
	var texts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			texts = append(texts, b.Text)
		}
	}
	if len(texts) == 0 {
		return nil
	}
	joined := strings.Join(texts, "\n")
	return &joined
}

func asString(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, true
	}
	return "", false
}

// ParseDecision parses the NEEDS_DECISION payload from the model's final spoken text, or nil when
// absent/malformed. A malformed gate degrades to no gate (the build classifies as retryable and
// self-heals) — but is logged, never silently swallowed. Port of runManager.ts parseDecision (:279-300).
func ParseDecision(text *string) *AwaitingDecision {
	if text == nil {
		return nil
	}
	m := needsDecisionLineRe.FindStringSubmatch(*text)
	if m == nil {
		return nil
	}
	var o struct {
		Kind     string   `json:"kind"`
		Question string   `json:"question"`
		Options  []string `json:"options"`
		Slice    *int     `json:"slice"`
	}
	if err := json.Unmarshal([]byte(m[1]), &o); err != nil {
		// Degrade to no-gate (the build self-heals as retryable), but log it — a silent miss makes
		// "why didn't my gate show?" undiagnosable (house pattern: empty catch = invisible bug).
		log.Printf("ParseDecision: malformed NEEDS_DECISION payload, ignoring gate: %v", err)
		return nil
	}
	question := strings.TrimSpace(o.Question)
	if question == "" {
		return nil
	}
	kind := "fork"
	if o.Kind == "action" {
		kind = "action"
	}
	// Drop empty/whitespace options — they'd render as un-confirmable buttons; all-empty → [].
	options := []string{}
	for _, x := range o.Options {
		if strings.TrimSpace(x) != "" {
			options = append(options, x)
		}
	}
	return &AwaitingDecision{Kind: kind, Question: question, Options: options, Slice: o.Slice}
}

// ClassifyOutcome decides how a finished build run should be treated. The order is load-bearing
// (faithful port of runManager.ts classifyOutcome :316-328):
//  1. non-build runs never auto-resume;
//  2. a user Stop is a deliberate end → terminal;
//  3. a PARSEABLE NEEDS_DECISION → awaiting (paused for a human, resumable once answered);
//  4. a terminal sentinel in the FINAL spoken text (PRD_BUILD_COMPLETE / HARD STOP / BLOCKED:) wins
//     over the exit code;
//  5. a spawn failure → park (a persistent ENOENT must not burn the resume budget);
//  6. a clean exit-0 with NO spoken text → park (ambiguous silent end);
//  7. everything else (mid-turn exit-0-with-text, any non-zero exit, interrupted/killed) → retryable.
func ClassifyOutcome(kind domain.RunKind, lines []ParsedLine, cause domain.RedriveCause) Outcome {
	if kind != "build" {
		return OutcomeTerminal
	}
	if cause.Kind == domain.CauseUserStop {
		return OutcomeTerminal
	}
	finalText := FinalSpokenText(lines)
	if ParseDecision(finalText) != nil {
		return OutcomeAwaiting
	}
	if finalText != nil && buildTerminalRe.MatchString(*finalText) {
		return OutcomeTerminal
	}
	if cause.Kind == domain.CauseSpawnError {
		return OutcomePark
	}
	if cause.Kind == domain.CauseExit && cause.ExitCode != nil && *cause.ExitCode == 0 && finalText == nil {
		return OutcomePark
	}
	return OutcomeRetryable
}

// LimitSignal is a usage/rate-limit or auth signal detected in a finished build's output. Per the
// Slice-0 spike, `claude -p` exits 0 (NOT non-zero) on a cap with a terminal message, so a cap can't be
// inferred from the exit code — only the text. An auth error is NOT transient. Port of runManager.ts
// LimitSignal (:334-337).
type LimitSignal struct {
	Kind    string // "usage-limit" | "auth-error"
	ResetAt *int64 // epoch ms parsed from a "resets <time>" message, when present
}

// Tight to the CLI's literal cap phrasing AND line-anchored (the CLI prints the cap as its own line)
// so a build whose OWN summary mentions usage limits mid-sentence doesn't trip a false wait. Faithful
// port of runManager.ts USAGE_LIMIT_RE (:341), AUTH_ERROR_RE (:342), RESET_TIME_RE (:343).
var (
	usageLimitRe = regexp.MustCompile(`(?im)^\s*(you(?:'ve| have) hit your (?:session|usage|account) limit|claude usage limit reached|usage limit reached)`)
	authErrorRe  = regexp.MustCompile(`(?i)authentication_error|invalid x-api-key|invalid api key|oauth token (?:revoked|expired)|please run\s+/login`)
	resetTimeRe  = regexp.MustCompile(`(?i)reset[s]?\s+(?:at\s+)?(\d{1,2})(?::(\d{2}))?\s*(am|pm)?`)
)

// limitScanText gathers the run's raw (non-JSON) output lines plus its final spoken text — the cap
// message may arrive either way. Port of runManager.ts limitScanText (:364-372).
func limitScanText(lines []ParsedLine) string {
	var parts []string
	for _, l := range lines {
		if l.Kind == "raw" {
			parts = append(parts, l.Text)
		}
	}
	if final := FinalSpokenText(lines); final != nil {
		parts = append(parts, *final)
	}
	return strings.Join(parts, "\n")
}

// DetectLimit finds a usage-cap or auth signal in a finished build's output (auth checked first, as it
// is not transient). Port of runManager.ts detectLimit (:374-379).
func DetectLimit(lines []ParsedLine, now int64) *LimitSignal {
	text := limitScanText(lines)
	if authErrorRe.MatchString(text) {
		return &LimitSignal{Kind: "auth-error"}
	}
	if usageLimitRe.MatchString(text) {
		return &LimitSignal{Kind: "usage-limit", ResetAt: parseResetAt(text, now)}
	}
	return nil
}

// parseResetAt parses a "resets <time>" clock time into the NEXT future epoch ms (best-effort — a miss
// falls back to the configured wait). Local-time based, matching the TS new Date(now).setHours. Port of
// runManager.ts parseResetAt (:347-360).
func parseResetAt(text string, now int64) *int64 {
	m := resetTimeRe.FindStringSubmatch(text)
	if m == nil {
		return nil
	}
	hour, _ := strconv.Atoi(m[1])
	minute := 0
	if m[2] != "" {
		minute, _ = strconv.Atoi(m[2])
	}
	switch strings.ToLower(m[3]) {
	case "pm":
		if hour < 12 {
			hour += 12
		}
	case "am":
		if hour == 12 {
			hour = 0
		}
	}
	if hour > 23 || minute > 59 {
		return nil
	}
	t := time.UnixMilli(now)
	target := time.Date(t.Year(), t.Month(), t.Day(), hour, minute, 0, 0, t.Location())
	if target.UnixMilli() <= now {
		target = target.AddDate(0, 0, 1) // already passed today → tomorrow
	}
	r := target.UnixMilli()
	return &r
}

// IsBuildComplete reports whether a build's final spoken text declares a successful finish
// (line-anchored PRD_BUILD_COMPLETE) — the only condition that auto-pushes the branch (Slice 5).
// Port of runManager.ts isBuildComplete (:172-174).
func IsBuildComplete(finalText *string) bool {
	return finalText != nil && buildSuccessRe.MatchString(*finalText)
}

// parkedReasonOf returns the last park/blocked breadcrumb in a finalized build's output (why it gave
// up), or "" when none. Scans the durable <id>.out lines (engine breadcrumbs are appended there, so
// this + the SSE tail + a late viewer all see them). Port of runManager.ts parkedReasonOf (:387-394).
func parkedReasonOf(lines []ParsedLine) string {
	for i := len(lines) - 1; i >= 0; i-- {
		l := lines[i]
		if l.Kind == "raw" && parkedRe.MatchString(l.Text) {
			return l.Text
		}
	}
	return ""
}
