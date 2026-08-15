package usecase

import (
	"net/http"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

// OSAScriptRunner execs the native macOS "choose folder" osascript, returning its exit code + streams
// (implemented by infra.OSAScript). Only invoked on darwin — the service gates the platform first.
type OSAScriptRunner interface {
	ChooseFolder() (code int, stdout, stderr string)
}

// PickFolderService serves GET /api/pick-folder (parity index.ts:836): a native folder picker that is
// macOS-only. goos is injected (runtime.GOOS in production) so the 501 non-darwin path is unit-testable.
type PickFolderService struct {
	runner OSAScriptRunner
	goos   string
}

// NewPickFolderService wires the osascript runner + the target platform into the service.
func NewPickFolderService(runner OSAScriptRunner, goos string) PickFolderService {
	return PickFolderService{runner: runner, goos: goos}
}

// Pick runs the picker and returns the HTTP (status, body) verbatim for the handler: 501 off darwin,
// else 200 {path} / 200 {canceled} / 500 {error} (parity index.ts:837-849).
func (s PickFolderService) Pick() (int, map[string]any) {
	if s.goos != "darwin" {
		return http.StatusNotImplemented, map[string]any{"error": "native folder picker is macOS-only — type the path"}
	}
	choice := domain.ParseFolderChoice(s.runner.ChooseFolder())
	switch {
	case choice.Path != "":
		return http.StatusOK, map[string]any{"path": choice.Path}
	case choice.Canceled:
		return http.StatusOK, map[string]any{"canceled": true}
	default:
		return http.StatusInternalServerError, map[string]any{"error": choice.Error}
	}
}
