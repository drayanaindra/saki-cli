package usecase

import (
	"errors"
	"testing"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

type fakeReader struct {
	branch *string
	err    error
}

func (f fakeReader) CurrentBranch(string) (*string, error) { return f.branch, f.err }

func strp(s string) *string { return &s }

func TestBranchService_Current(t *testing.T) {
	t.Run("returns branch", func(t *testing.T) {
		svc := NewBranchService(fakeReader{branch: strp("main")})
		got, err := svc.Current(domain.Repo{Dir: "/x"})
		if err != nil || got == nil || *got != "main" {
			t.Fatalf("want main, got %v err %v", got, err)
		}
	})
	t.Run("nil branch (detached/empty)", func(t *testing.T) {
		svc := NewBranchService(fakeReader{branch: nil})
		got, err := svc.Current(domain.Repo{Dir: "/x"})
		if err != nil || got != nil {
			t.Fatalf("want nil,nil got %v err %v", got, err)
		}
	})
	t.Run("cwd required", func(t *testing.T) {
		svc := NewBranchService(fakeReader{branch: strp("main")})
		_, err := svc.Current(domain.Repo{Dir: ""})
		if !errors.Is(err, ErrCwdRequired) {
			t.Fatalf("want ErrCwdRequired, got %v", err)
		}
	})
	t.Run("reader error propagates", func(t *testing.T) {
		svc := NewBranchService(fakeReader{err: errors.New("boom")})
		_, err := svc.Current(domain.Repo{Dir: "/x"})
		if err == nil {
			t.Fatal("want reader error")
		}
	})
}
