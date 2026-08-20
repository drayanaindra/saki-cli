package usecase

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

// fakeEngineProofs records every call it receives so tests can assert exactly what DoctorService.Check
// invoked — nothing more (rules 1/5: DoctorService has no other capability to exercise).
type fakeEngineProofs struct {
	binaryErr    map[domain.RunEngine]error
	profileErr   map[domain.RunEngine]error
	binaryCalls  []domain.RunEngine
	profileCalls []domain.RunEngine
}

func (f *fakeEngineProofs) BinaryCheck(engine domain.RunEngine) error {
	f.binaryCalls = append(f.binaryCalls, engine)
	return f.binaryErr[engine]
}

func (f *fakeEngineProofs) ProfileProof(engine domain.RunEngine, configDir *string) error {
	f.profileCalls = append(f.profileCalls, engine)
	return f.profileErr[engine]
}

func TestDoctorService_Check(t *testing.T) {
	t.Run("both engines ok", func(t *testing.T) {
		f := &fakeEngineProofs{}
		reports := NewDoctorService(f).Check(nil)
		if len(reports) != 3 {
			t.Fatalf("want 3 reports, got %d", len(reports))
		}
		for _, r := range reports {
			if r.Status != "ok" {
				t.Errorf("%s: status = %q, want ok", r.Engine, r.Status)
			}
		}
	})

	t.Run("codex profile proof fails, binary ok", func(t *testing.T) {
		f := &fakeEngineProofs{profileErr: map[domain.RunEngine]error{
			domain.EngineCodex: errors.New("codex profile does not resolve @saketek/saki-builder"),
		}}
		reports := NewDoctorService(f).Check(nil)
		codex, opencode := reports[0], reports[1]
		if codex.Status != "failed" || codex.Reason != "codex profile does not resolve @saketek/saki-builder" {
			t.Errorf("codex = %+v, want status=failed reason=%q", codex, "codex profile does not resolve @saketek/saki-builder")
		}
		if opencode.Status != "ok" {
			t.Errorf("opencode = %+v, want status=ok", opencode)
		}
	})

	t.Run("exactly codex then opencode, all fields present", func(t *testing.T) {
		f := &fakeEngineProofs{}
		reports := NewDoctorService(f).Check(nil)
		if reports[0].Engine != string(domain.EngineCodex) || reports[1].Engine != string(domain.EngineOpencode) || reports[2].Engine != string(domain.EngineClaude) {
			t.Fatalf("order = [%s, %s, %s], want [codex, opencode, claude]", reports[0].Engine, reports[1].Engine, reports[2].Engine)
		}
		for _, r := range reports {
			if r.Profile == "" {
				t.Errorf("%s: Profile must be set (default or the pinned dir), got empty", r.Engine)
			}
		}
	})

	t.Run("no side effects beyond BinaryCheck/ProfileProof", func(t *testing.T) {
		f := &fakeEngineProofs{}
		NewDoctorService(f).Check(nil)
		if len(f.binaryCalls) != 3 || len(f.profileCalls) != 3 {
			t.Fatalf("binaryCalls=%v profileCalls=%v — want exactly 3 of each (one per reported engine)", f.binaryCalls, f.profileCalls)
		}
	})

	t.Run("codex binary check fails — short-circuits, profile proof never called for codex", func(t *testing.T) {
		f := &fakeEngineProofs{binaryErr: map[domain.RunEngine]error{
			domain.EngineCodex: errors.New("engine binary not found on PATH (codex)"),
		}}
		reports := NewDoctorService(f).Check(nil)
		codex := reports[0]
		if codex.Status != "failed" || codex.Reason != "engine binary not found on PATH (codex)" {
			t.Errorf("codex = %+v, want status=failed reason=%q", codex, "engine binary not found on PATH (codex)")
		}
		for _, e := range f.profileCalls {
			if e == domain.EngineCodex {
				t.Fatalf("ProfileProof was called for codex after BinaryCheck already failed — must short-circuit")
			}
		}
	})

	t.Run("--profile threads through as the reported profile value", func(t *testing.T) {
		f := &fakeEngineProofs{}
		dir := "/tmp/broken-codex-home"
		reports := NewDoctorService(f).Check(&dir)
		for _, r := range reports {
			if r.Profile != dir {
				t.Errorf("%s: Profile = %q, want %q", r.Engine, r.Profile, dir)
			}
		}
	})

	// F2 slice 2, criterion 2.3: codex's Fix is populated ONLY on a ProfileProof failure — a
	// BinaryCheck failure has no authored remediation (installing the plugin doesn't fix a missing
	// binary), and opencode's Fix stays empty (F5, deferred).
	t.Run("codex profile failure populates Fix", func(t *testing.T) {
		f := &fakeEngineProofs{profileErr: map[domain.RunEngine]error{
			domain.EngineCodex: errors.New("codex profile does not resolve @saketek/saki-builder"),
		}}
		reports := NewDoctorService(f).Check(nil)
		codex := reports[0]
		if codex.Fix != CodexInstallFix {
			t.Errorf("codex.Fix = %q, want %q", codex.Fix, CodexInstallFix)
		}
	})

	t.Run("codex binary failure leaves Fix empty", func(t *testing.T) {
		f := &fakeEngineProofs{binaryErr: map[domain.RunEngine]error{
			domain.EngineCodex: errors.New("engine binary not found on PATH (codex)"),
		}}
		reports := NewDoctorService(f).Check(nil)
		codex := reports[0]
		if codex.Fix != "" {
			t.Errorf("codex.Fix = %q, want empty — a missing binary has no authored remediation yet", codex.Fix)
		}
	})

	t.Run("opencode gets the rendered Fix", func(t *testing.T) {
		f := &fakeEngineProofs{profileErr: map[domain.RunEngine]error{
			domain.EngineOpencode: errors.New("opencode profile does not resolve @saketek/saki-builder"),
		}}
		reports := NewDoctorService(f).Check(nil)
		opencode := reports[1]
		if opencode.Fix != OpencodeInstallFix {
			t.Errorf("opencode.Fix = %q, want %q — doctor names the same command init-env runs (slice 2)", opencode.Fix, OpencodeInstallFix)
		}
	})
}

func TestDoctorService_Check_ClaudeFailure(t *testing.T) {
	f := &fakeEngineProofs{profileErr: map[domain.RunEngine]error{
		domain.EngineClaude: errors.New("claude profile does not resolve saki-builder"),
	}}
	reports := NewDoctorService(f).Check(nil)
	if len(reports) != 3 {
		t.Fatalf("want 3 reports, got %d", len(reports))
	}
	claude := reports[2]
	if claude.Engine != string(domain.EngineClaude) || claude.Status != "failed" || claude.Reason != "claude profile does not resolve saki-builder" {
		t.Errorf("claude = %+v, want failed proof report", claude)
	}
	if claude.Fix != ClaudeInstallFix {
		t.Errorf("claude.Fix = %q, want %q", claude.Fix, ClaudeInstallFix)
	}
}

// F2 slice 2, criterion 2.3's own wording: "the test fails if either drifts from the script". Neither
// side is hardcoded a SECOND time here beyond these two reference lines — the installer script and
// CodexInstallFix are each checked against them independently, so a change on EITHER side alone fails
// the corresponding assertion.
func TestCodexInstallFix_MatchesInstallerScript(t *testing.T) {
	const marketplaceLine = "codex plugin marketplace add https://github.com/drayanaindra/saki-builder.git"
	const addLine = "codex plugin add saki-builder@saketek"

	// Resolves outside this Go module (backend/go.mod) into the repo root — true for every checkout
	// this repo is actually tested from, but a vendored/extracted usecase package would lack it.
	// Skip rather than fail in that case; this drift-guard only makes sense inside the full repo.
	scriptPath := filepath.Join("..", "..", "scripts", "install-codex-skills.sh")
	raw, err := os.ReadFile(scriptPath)
	if os.IsNotExist(err) {
		t.Skipf("%s not found — skipping outside a full repo checkout", scriptPath)
	}
	if err != nil {
		t.Fatalf("reading %s: %v", scriptPath, err)
	}
	script := string(raw)
	if !strings.Contains(script, marketplaceLine) {
		t.Errorf("%s no longer contains %q — CodexInstallFix is now stale", scriptPath, marketplaceLine)
	}
	if !strings.Contains(script, addLine) {
		t.Errorf("%s no longer contains %q — CodexInstallFix is now stale", scriptPath, addLine)
	}

	if !strings.Contains(CodexInstallFix, marketplaceLine) {
		t.Errorf("CodexInstallFix = %q, missing %q", CodexInstallFix, marketplaceLine)
	}
	if !strings.Contains(CodexInstallFix, addLine) {
		t.Errorf("CodexInstallFix = %q, missing %q", CodexInstallFix, addLine)
	}
}
