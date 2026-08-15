package usecase

import (
	"errors"
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
		if len(reports) != 2 {
			t.Fatalf("want 2 reports, got %d", len(reports))
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
		if reports[0].Engine != string(domain.EngineCodex) || reports[1].Engine != string(domain.EngineOpencode) {
			t.Fatalf("order = [%s, %s], want [codex, opencode]", reports[0].Engine, reports[1].Engine)
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
		if len(f.binaryCalls) != 2 || len(f.profileCalls) != 2 {
			t.Fatalf("binaryCalls=%v profileCalls=%v — want exactly 2 of each (one per reported engine)", f.binaryCalls, f.profileCalls)
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

	t.Run("opencode never gets a Fix", func(t *testing.T) {
		f := &fakeEngineProofs{profileErr: map[domain.RunEngine]error{
			domain.EngineOpencode: errors.New("opencode profile does not resolve @saketek/saki-builder"),
		}}
		reports := NewDoctorService(f).Check(nil)
		opencode := reports[1]
		if opencode.Fix != "" {
			t.Errorf("opencode.Fix = %q, want empty — opencode remediation is deferred (F5)", opencode.Fix)
		}
	})
}
