package usecase

import (
	"encoding/json"
	"testing"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

func jsonLine(t *testing.T, obj any) ParsedLine {
	t.Helper()
	b, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	return ParsedLine{Kind: "json", Value: json.RawMessage(b)}
}

func rawLine(text string) ParsedLine { return ParsedLine{Kind: "raw", Text: text} }

func resultLine(t *testing.T, text string) ParsedLine {
	return jsonLine(t, map[string]any{"type": "result", "result": text})
}

func assistantLine(t *testing.T, text string) ParsedLine {
	return jsonLine(t, map[string]any{
		"type":    "assistant",
		"message": map[string]any{"content": []map[string]any{{"type": "text", "text": text}}},
	})
}

func ptrInt(n int) *int { return &n }

func TestFinalSpokenText_ResultWinsOverAssistant(t *testing.T) {
	lines := []ParsedLine{assistantLine(t, "working on it"), resultLine(t, "PRD_BUILD_COMPLETE")}
	got := FinalSpokenText(lines)
	if got == nil || *got != "PRD_BUILD_COMPLETE" {
		t.Fatalf("want result text, got %v", got)
	}
}

func TestFinalSpokenText_LastAssistantWhenNoResult(t *testing.T) {
	lines := []ParsedLine{assistantLine(t, "first"), assistantLine(t, "second")}
	got := FinalSpokenText(lines)
	if got == nil || *got != "second" {
		t.Fatalf("want last assistant text, got %v", got)
	}
}

func TestFinalSpokenText_StringContent(t *testing.T) {
	lines := []ParsedLine{jsonLine(t, map[string]any{"type": "assistant", "message": map[string]any{"content": "plain"}})}
	got := FinalSpokenText(lines)
	if got == nil || *got != "plain" {
		t.Fatalf("want plain string content, got %v", got)
	}
}

func TestFinalSpokenText_NoneWhenNoSpokenText(t *testing.T) {
	if got := FinalSpokenText([]ParsedLine{rawLine("some stderr noise")}); got != nil {
		t.Fatalf("want nil, got %v", got)
	}
}

// AC 1.5 — a line-anchored terminal sentinel in the FINAL spoken text → terminal (no resume), and it
// wins over a non-zero exit.
func TestClassify_TerminalSentinelLineAnchored(t *testing.T) {
	for _, s := range []string{"PRD_BUILD_COMPLETE", "HARD STOP — missing PRD", "BLOCKED: slice 3 — API down"} {
		lines := []ParsedLine{resultLine(t, s)}
		if got := ClassifyOutcome("build", lines, domain.RedriveCause{Kind: domain.CauseExit, ExitCode: ptrInt(1)}); got != OutcomeTerminal {
			t.Fatalf("%q must be terminal (wins over exit≠0), got %s", s, got)
		}
	}
}

// AC 1.5 — the model NARRATING a sentinel mid-sentence must NOT mark a still-incomplete build terminal.
func TestClassify_NarratedSentinelIsNotTerminal(t *testing.T) {
	lines := []ParsedLine{resultLine(t, "I considered whether slice 4 is BLOCKED: on an API but proceeded")}
	if got := ClassifyOutcome("build", lines, domain.RedriveCause{Kind: domain.CauseExit, ExitCode: ptrInt(0)}); got != OutcomeRetryable {
		t.Fatalf("a narrated mid-sentence sentinel must stay retryable, got %s", got)
	}
}

// AC 1.5 — a mid-turn exit-0-WITH-text (no terminal sentinel) is retryable; any non-zero exit is retryable.
func TestClassify_MidTurnAndNonZeroRetryable(t *testing.T) {
	withText := []ParsedLine{resultLine(t, "finished slice 2, continuing")}
	if got := ClassifyOutcome("build", withText, domain.RedriveCause{Kind: domain.CauseExit, ExitCode: ptrInt(0)}); got != OutcomeRetryable {
		t.Fatalf("mid-turn exit-0-with-text must be retryable, got %s", got)
	}
	if got := ClassifyOutcome("build", withText, domain.RedriveCause{Kind: domain.CauseExit, ExitCode: ptrInt(2)}); got != OutcomeRetryable {
		t.Fatalf("non-zero exit must be retryable, got %s", got)
	}
}

// classifyOutcome order (:316-328): non-build→terminal; user-stop→terminal; awaiting; spawn-error→park;
// exit-0-no-text→park.
func TestClassify_OrderAndParkCases(t *testing.T) {
	if got := ClassifyOutcome("generate", nil, domain.RedriveCause{Kind: domain.CauseExit, ExitCode: ptrInt(1)}); got != OutcomeTerminal {
		t.Fatalf("non-build must be terminal, got %s", got)
	}
	if got := ClassifyOutcome("build", nil, domain.RedriveCause{Kind: domain.CauseUserStop}); got != OutcomeTerminal {
		t.Fatalf("user-stop must be terminal, got %s", got)
	}
	if got := ClassifyOutcome("build", nil, domain.RedriveCause{Kind: domain.CauseSpawnError}); got != OutcomePark {
		t.Fatalf("spawn-error must park, got %s", got)
	}
	if got := ClassifyOutcome("build", nil, domain.RedriveCause{Kind: domain.CauseExit, ExitCode: ptrInt(0)}); got != OutcomePark {
		t.Fatalf("clean exit-0 with no spoken text must park, got %s", got)
	}
}

func TestClassify_ParseableDecisionAwaiting(t *testing.T) {
	lines := []ParsedLine{resultLine(t, `NEEDS_DECISION: {"slice":3,"question":"Which store?","options":["A","B"]}`)}
	if got := ClassifyOutcome("build", lines, domain.RedriveCause{Kind: domain.CauseExit, ExitCode: ptrInt(0)}); got != OutcomeAwaiting {
		t.Fatalf("parseable NEEDS_DECISION must be awaiting, got %s", got)
	}
}

func TestParseDecision_MalformedDegradesToNil(t *testing.T) {
	bad := "NEEDS_DECISION: {not json}"
	if got := ParseDecision(&bad); got != nil {
		t.Fatalf("malformed payload must degrade to nil (retryable), got %+v", got)
	}
}

func TestParseDecision_DropsEmptyOptionsAndParsesSlice(t *testing.T) {
	txt := `NEEDS_DECISION: {"kind":"fork","question":"pick","options":["A","","  ","B"],"slice":5}`
	d := ParseDecision(&txt)
	if d == nil || d.Kind != "fork" || d.Question != "pick" {
		t.Fatalf("decision %+v", d)
	}
	if len(d.Options) != 2 || d.Options[0] != "A" || d.Options[1] != "B" {
		t.Fatalf("empty options must be dropped, got %v", d.Options)
	}
	if d.Slice == nil || *d.Slice != 5 {
		t.Fatalf("slice %v", d.Slice)
	}
}

func TestIsBuildComplete(t *testing.T) {
	ok := "PRD_BUILD_COMPLETE"
	no := "BLOCKED: gave up"
	narrated := "the build will print PRD_BUILD_COMPLETE when done"
	if !IsBuildComplete(&ok) {
		t.Fatal("PRD_BUILD_COMPLETE at line-start must be complete")
	}
	if IsBuildComplete(&no) || IsBuildComplete(nil) {
		t.Fatal("non-success / nil must not be complete")
	}
	if IsBuildComplete(&narrated) {
		t.Fatal("a narrated mention mid-sentence must not count as complete")
	}
}

func TestParkedReasonOf(t *testing.T) {
	lines := []ParsedLine{rawLine("auto-resume: resume budget exhausted (40) — parked for operator")}
	if r := parkedReasonOf(lines); r == "" {
		t.Fatal("a parked-for-operator breadcrumb must surface as the parked reason")
	}
	if r := parkedReasonOf([]ParsedLine{rawLine("just some prose about being blocked")}); r != "" {
		t.Fatalf("non-sentinel prose must not fabricate a parked reason, got %q", r)
	}
}

// --- E26 slice 3: opencode text frames reach FinalSpokenText / classification / DetectLimit ---

func opencodeTextFrame(t *testing.T, text string) ParsedLine {
	return jsonLine(t, map[string]any{"type": "text", "part": map[string]any{"type": "text", "text": text}})
}

// 9.3.3 — a single opencode text frame's content becomes the final spoken text (real spike shape:
// content at part.text), and a sentinel split across frames still matches line-anchored.
func TestFinalSpokenText_OpencodeTextFrames(t *testing.T) {
	single := []ParsedLine{opencodeTextFrame(t, "Slice 3 green. PRD_BUILD_COMPLETE")}
	if got := FinalSpokenText(single); got == nil || *got != "Slice 3 green. PRD_BUILD_COMPLETE" {
		t.Fatalf("a single opencode text frame must become the final spoken text, got %v", got)
	}

	split := []ParsedLine{
		opencodeTextFrame(t, "All slices verified."),
		opencodeTextFrame(t, "PRD_BUILD_COMPLETE"),
	}
	if got := FinalSpokenText(split); got == nil || !IsBuildComplete(got) {
		t.Fatalf("frames split across messages must accumulate line-anchored and complete, got %v", got)
	}
}

// 9.3.1 — an exit-0 opencode build whose accumulated text carries PRD_BUILD_COMPLETE is TERMINAL
// (never OutcomePark), and IsBuildComplete is true (the auto-push condition).
func TestClassifyOutcome_OpencodeComplete(t *testing.T) {
	lines := []ParsedLine{opencodeTextFrame(t, "done"), opencodeTextFrame(t, "PRD_BUILD_COMPLETE")}
	exit0 := domain.RedriveCause{Kind: domain.CauseExit, ExitCode: ptrInt(0)}
	if got := ClassifyOutcome(domain.RunKind("build"), lines, exit0); got != OutcomeTerminal {
		t.Fatalf("an opencode build with PRD_BUILD_COMPLETE must be terminal, got %s", got)
	}
	if final := FinalSpokenText(lines); !IsBuildComplete(final) {
		t.Fatal("IsBuildComplete must be true for the accumulated opencode text")
	}
}

// 9.3.4 — an auth/usage-limit signal inside an opencode text frame is still detected (the resume
// chain awaits instead of hot-retrying).
func TestDetectLimit_OpencodeAuthSignal(t *testing.T) {
	lines := []ParsedLine{opencodeTextFrame(t, "authentication_error: your API key is invalid")}
	if got := DetectLimit(lines, 0); got == nil || got.Kind != "auth-error" {
		t.Fatalf("an opencode auth signal must surface via DetectLimit, got %+v", got)
	}
}

func ompMessageEndFrame(t *testing.T, role, text string) ParsedLine {
	return jsonLine(t, map[string]any{
		"type": "message_end",
		"message": map[string]any{
			"role":    role,
			"content": []map[string]any{{"type": "text", "text": text}},
		},
	})
}

func TestFinalSpokenText_OMPMessageEnd(t *testing.T) {
	lines := []ParsedLine{
		jsonLine(t, map[string]any{"type": "message_start", "message": map[string]any{"role": "assistant"}}),
		ompMessageEndFrame(t, "assistant", "verified\nPRD_BUILD_COMPLETE"),
		jsonLine(t, map[string]any{"type": "turn_end"}),
	}
	got := FinalSpokenText(lines)
	if got == nil || *got != "verified\nPRD_BUILD_COMPLETE" {
		t.Fatalf("OMP message_end must provide the complete assistant text, got %v", got)
	}
	if !IsBuildComplete(got) {
		t.Fatalf("OMP message_end sentinel must classify as complete, got %q", *got)
	}
}

func TestFinalSpokenText_OMPAgentEndFallback(t *testing.T) {
	lines := []ParsedLine{jsonLine(t, map[string]any{
		"type": "agent_end",
		"messages": []map[string]any{
			{"role": "user", "content": []map[string]any{{"type": "text", "text": "ignore"}}},
			{"role": "assistant", "content": []map[string]any{{"type": "text", "text": "fallback"}}},
		},
	})}
	got := FinalSpokenText(lines)
	if got == nil || *got != "fallback" {
		t.Fatalf("OMP agent_end assistant fallback must be spoken text, got %v", got)
	}
}
