package domain

import (
	"path/filepath"
	"regexp"
	"strings"
)

// TemplateKind is a supported new-project starting point (parity newProject.ts TEMPLATES). The zero
// value is invalid — callers must go through IsTemplateKind / the handler's allowlist.
type TemplateKind string

const (
	TemplateEmpty       TemplateKind = "empty"
	TemplateViteReactTS TemplateKind = "vite-react-ts"
	TemplateNextJS      TemplateKind = "nextjs"
)

// IsTemplateKind reports whether x names a supported template (parity isTemplateKind — an allowlist,
// so no arbitrary path from the request body can reach a template lookup).
func IsTemplateKind(x string) bool {
	switch TemplateKind(x) {
	case TemplateEmpty, TemplateViteReactTS, TemplateNextJS:
		return true
	default:
		return false
	}
}

// ProjectError carries a machine code (INVALID | EXISTS) alongside the operator-facing message, so the
// handler can map it to 400 / 409 without string-matching (parity newProject.ts ProjectError).
type ProjectError struct {
	Code string
	Msg  string
}

func (e *ProjectError) Error() string { return e.Msg }

const (
	CodeInvalid = "INVALID"
	CodeExists  = "EXISTS"
)

func invalid(msg string) *ProjectError { return &ProjectError{Code: CodeInvalid, Msg: msg} }

// nameRE is the load-bearing path-traversal guard: a project name must be a single safe path segment —
// starts alphanumeric, then [A-Za-z0-9._-], so it rejects "..", "a/b", "a\b", and leading-dot names,
// and the resolved target can never escape the base dir (parity newProject.ts:31).
var nameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ValidateName trims + validates a project name (parity validateName). Returns INVALID on empty, >64,
// or a name that isn't a single safe segment.
func ValidateName(name string) (string, *ProjectError) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", invalid("project name is required")
	}
	if len(trimmed) > 64 {
		return "", invalid("project name must be 64 characters or fewer")
	}
	if !nameRE.MatchString(trimmed) {
		return "", invalid("invalid project name — use letters, digits, dot, dash, underscore (no slashes)")
	}
	return trimmed, nil
}

// ExpandBase expands a leading ~ to home and requires the result to be absolute (parity expandBase).
// home is injected (os.UserHomeDir in production) so the expansion stays pure + testable.
func ExpandBase(base, home string) (string, *ProjectError) {
	trimmed := strings.TrimSpace(base)
	if trimmed == "" {
		return "", invalid("base directory is required")
	}
	expanded := trimmed
	if trimmed == "~" {
		expanded = home
	} else if strings.HasPrefix(trimmed, "~/") {
		expanded = filepath.Join(home, trimmed[2:])
	}
	if !filepath.IsAbs(expanded) {
		return "", invalid("base directory must be an absolute path")
	}
	return expanded, nil
}

// ResolveTarget composes ValidateName + ExpandBase into the safe absolute target dir. Defense-in-depth
// (parity resolveTarget:57): assert the resolved parent IS the expanded base and the leaf IS the name,
// so a name that somehow slipped past nameRE still can't traverse out.
func ResolveTarget(base, name, home string) (string, *ProjectError) {
	nm, err := ValidateName(name)
	if err != nil {
		return "", err
	}
	bs, err := ExpandBase(base, home)
	if err != nil {
		return "", err
	}
	path := filepath.Join(bs, nm)
	if filepath.Dir(path) != bs || filepath.Base(path) != nm {
		return "", invalid("invalid project path")
	}
	return path, nil
}
