package usecase

import (
	"testing"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

// codexAgentMessage builds a codex `exec --json` spoken-text frame. Shape captured from the REAL
// codex-cli 0.147.0: {"type":"item.completed","item":{"id":…,"type":"agent_message","text":…}}.
func codexAgentMessage(t *testing.T, text string) ParsedLine {
	t.Helper()
	return jsonLine(t, map[string]any{
		"type": "item.completed",
		"item": map[string]any{"id": "item_0", "type": "agent_message", "text": text},
	})
}

// codexToolItem builds a codex tool frame — the SAME outer type as spoken text, which is exactly why
// the inner item.type must be checked.
func codexToolItem(t *testing.T, itemType, text string) ParsedLine {
	t.Helper()
	return jsonLine(t, map[string]any{
		"type": "item.completed",
		"item": map[string]any{"id": "item_1", "type": itemType, "text": text},
	})
}

func TestFinalSpokenText_CodexAgentMessage(t *testing.T) {
	got := FinalSpokenText([]ParsedLine{codexAgentMessage(t, "PRD_BUILD_COMPLETE")})
	if got == nil || *got != "PRD_BUILD_COMPLETE" {
		t.Fatalf("want the codex agent_message text, got %v", got)
	}
}

// Each codex agent_message is a COMPLETE model message (unlike opencode's `text` chunks), so the last
// one wins — parity with claude's lastAssistant. A codex run really does emit a preamble message
// before the final one (observed in the spike), so appending would corrupt the final spoken text.
func TestFinalSpokenText_CodexLastAgentMessageWins(t *testing.T) {
	lines := []ParsedLine{
		codexAgentMessage(t, "I'm using the build skill to resume slice 2."),
		codexAgentMessage(t, "PRD_BUILD_COMPLETE"),
	}
	got := FinalSpokenText(lines)
	if got == nil || *got != "PRD_BUILD_COMPLETE" {
		t.Fatalf("want only the LAST agent_message, got %v", got)
	}
}

// 🔒 Tool frames share the `item.completed` type. If the inner type were ignored, a command_execution
// whose aggregated output happened to contain a sentinel would be read as the model's spoken text —
// precisely the "the model narrated it" false-positive the line-anchored classifier exists to avoid.
func TestFinalSpokenText_CodexIgnoresNonAgentItems(t *testing.T) {
	lines := []ParsedLine{
		codexAgentMessage(t, "PRD_BUILD_COMPLETE"),
		codexToolItem(t, "command_execution", "BLOCKED: this is tool output, not speech"),
		codexToolItem(t, "file_change", "HARD STOP"),
	}
	got := FinalSpokenText(lines)
	if got == nil || *got != "PRD_BUILD_COMPLETE" {
		t.Fatalf("tool frames must not become spoken text, got %v", got)
	}
}

// End-to-end through the real classifier: a codex build that finished cleanly is TERMINAL (never
// auto-resumed), and one that merely ended its turn is RETRYABLE — the same contract as the other two
// engines, now reached through the codex frame.
func TestClassifyOutcome_CodexBuild(t *testing.T) {
	exit0 := 0
	cause := domain.RedriveCause{Kind: domain.CauseExit, ExitCode: &exit0}

	done := ClassifyOutcome("build", []ParsedLine{codexAgentMessage(t, "PRD_BUILD_COMPLETE")}, cause)
	if done != OutcomeTerminal {
		t.Errorf("a completed codex build = %v, want %v", done, OutcomeTerminal)
	}
	mid := ClassifyOutcome("build", []ParsedLine{codexAgentMessage(t, "Finished slice 1, continuing.")}, cause)
	if mid != OutcomeRetryable {
		t.Errorf("a mid-turn codex build = %v, want %v", mid, OutcomeRetryable)
	}
}

// The classifier stays LINE-ANCHORED on codex too: the model narrating a sentinel mid-sentence must
// not end the chain (project rule — the two engines share this parser, so a codex-only regression here
// would be invisible to the claude tests).
func TestClassifyOutcome_CodexNarratedSentinelIsNotTerminal(t *testing.T) {
	exit0 := 0
	cause := domain.RedriveCause{Kind: domain.CauseExit, ExitCode: &exit0}
	lines := []ParsedLine{codexAgentMessage(t, "Next I will print PRD_BUILD_COMPLETE once slice 3 lands.")}
	if got := ClassifyOutcome("build", lines, cause); got != OutcomeRetryable {
		t.Errorf("a narrated sentinel = %v, want %v", got, OutcomeRetryable)
	}
}
