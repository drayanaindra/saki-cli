package usecase

import (
	"errors"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

// ErrCwdRequired is returned when the repo has no working dir (the "cwd is required"
// domain rule). The adapter maps it to HTTP 422 (parity with apps/server /api/branch).
var ErrCwdRequired = errors.New("cwd is required")

// BranchService reads the current branch of a repo through a BranchReader port.
type BranchService struct {
	reader BranchReader
}

// NewBranchService wires a BranchReader into the service.
func NewBranchService(reader BranchReader) BranchService {
	return BranchService{reader: reader}
}

// Current returns the repo's current branch (nil when there is none). It enforces the
// cwd-required rule before touching the reader.
func (s BranchService) Current(r domain.Repo) (*string, error) {
	if !r.Valid() {
		return nil, ErrCwdRequired
	}
	return s.reader.CurrentBranch(r.Dir)
}
