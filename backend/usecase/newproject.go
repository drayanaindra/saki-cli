package usecase

import "github.com/drayanaindra/saki-cli/backend/domain"

// ProjectFS is the filesystem + git surface the new-project scaffold needs, split fine-grained so the
// usecase owns the ORDER + the rollback (both testable with a fake). Implemented by infra.OSProjectFS.
type ProjectFS interface {
	Exists(path string) bool
	MkdirAll(path string) error
	CopyTemplate(kind domain.TemplateKind, dst string) error
	StampPackageName(dst, name string) error
	GitInitCommit(dst string, kind domain.TemplateKind) error
	RemoveAll(path string) error
}

// NewProjectService serves POST /api/new-project (parity newProject.ts createProject): resolve a safe
// target, scaffold the template, and git-init with one initial commit — removing the half-built dir on
// any post-mkdir failure so a retry with the same name isn't blocked by the EXISTS guard.
type NewProjectService struct {
	fs   ProjectFS
	home string
}

// NewNewProjectService wires the ProjectFS + the operator's home dir (for ~ expansion) into the service.
func NewNewProjectService(fs ProjectFS, home string) NewProjectService {
	return NewProjectService{fs: fs, home: home}
}

// Create scaffolds base/name from the template. Returns the created path, or a *domain.ProjectError
// whose Code (INVALID/EXISTS) the handler maps to 400/409; a generic error (Code "") maps to 500. The
// mkdir runs OUTSIDE the rollback window (parity: mkdir-before-try), so an unwritable base is a plain
// 500 with nothing to clean up; every later step rolls back the dir.
func (s NewProjectService) Create(base, name string, kind domain.TemplateKind) (string, *domain.ProjectError) {
	target, perr := domain.ResolveTarget(base, name, s.home)
	if perr != nil {
		return "", perr
	}
	if s.fs.Exists(target) {
		return "", &domain.ProjectError{Code: domain.CodeExists, Msg: "directory already exists: " + target}
	}
	if err := s.fs.MkdirAll(target); err != nil {
		return "", &domain.ProjectError{Msg: err.Error()}
	}
	if err := s.scaffold(target, name, kind); err != nil {
		s.fs.RemoveAll(target) // best-effort rollback; the original error is what the operator sees
		return "", &domain.ProjectError{Msg: err.Error()}
	}
	return target, nil
}

// scaffold performs the rollback-guarded steps: copy the template, stamp the folder name into
// package.json, then git-init + one initial commit (parity newProject.ts:115-133).
func (s NewProjectService) scaffold(target, name string, kind domain.TemplateKind) error {
	if err := s.fs.CopyTemplate(kind, target); err != nil {
		return err
	}
	if err := s.fs.StampPackageName(target, name); err != nil {
		return err
	}
	return s.fs.GitInitCommit(target, kind)
}
