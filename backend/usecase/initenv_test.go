package usecase

import (
	"path/filepath"
	"testing"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

type initProvisioner struct {
	changed bool
	err     error
}

func (p initProvisioner) Provision(ProvisionRequest) (bool, error) { return p.changed, p.err }

type initProofs struct{ binaryErr, profileErr error }

func (p initProofs) BinaryCheck(domain.RunEngine) error           { return p.binaryErr }
func (p initProofs) ProfileProof(domain.RunEngine, *string) error { return p.profileErr }

func TestInitEnvServiceClaudeIsNotVerifiedWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	svc := NewInitEnvService(initProvisioner{changed: true}, initProofs{})
	status, body := svc.Provision(ProvisionRequest{Cwd: dir, Engine: domain.EngineClaude})
	if status != 200 || body["status"] != "failed" || body["changed"] != false {
		t.Fatalf("status=%d body=%v", status, body)
	}
}

func TestInitEnvServiceProvesAfterProvision(t *testing.T) {
	dir := t.TempDir()
	profile := filepath.Join(dir, "profile")
	svc := NewInitEnvService(initProvisioner{changed: true}, initProofs{})
	status, body := svc.Provision(ProvisionRequest{Cwd: dir, Engine: domain.EngineCodex, Profile: &profile})
	if status != 200 || body["status"] != "ok" || body["changed"] != true {
		t.Fatalf("status=%d body=%v", status, body)
	}
}

func TestInitEnvServiceRejectsBadCwdBeforeAdapter(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	called := false
	provisioner := initProvisionerFunc(func(ProvisionRequest) (bool, error) { called = true; return false, nil })
	svc := NewInitEnvService(provisioner, initProofs{})
	status, body := svc.Provision(ProvisionRequest{Cwd: dir, Engine: domain.EngineCodex})
	if status != 422 || called || body["error"] == nil {
		t.Fatalf("status=%d called=%v body=%v", status, called, body)
	}
}

type initProvisionerFunc func(ProvisionRequest) (bool, error)

func (f initProvisionerFunc) Provision(r ProvisionRequest) (bool, error) { return f(r) }
