package infra

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestExecErr_precedence(t *testing.T) {
	if got := execErr(errors.New("boom"), "  stderr text \n"); got != "stderr text" {
		t.Fatalf("stderr wins: %q", got)
	}
	if got := execErr(errors.New("boom"), "   "); got != "boom" {
		t.Fatalf("err msg fallback: %q", got)
	}
	if got := execErr(nil, ""); got != "command failed" {
		t.Fatalf("final fallback: %q", got)
	}
}

// BR6: a blocking git exec is bounded by the timeout — it returns an errMsg quickly rather
// than hanging. `git help --all` on a tiny timeout is a stand-in for a slow subprocess.
func TestRunGit_boundedByTimeout(t *testing.T) {
	start := time.Now()
	_, errMsg := runGit(1*time.Millisecond, "-C", t.TempDir(), "branch", "--format=%(refname:short)")
	// Either it finishes (fast, errMsg from a non-repo) or it's killed by the deadline; in BOTH
	// cases it returns promptly with a non-empty errMsg and never hangs.
	if time.Since(start) > 5*time.Second {
		t.Fatal("runGit did not respect the timeout bound")
	}
	if errMsg == "" {
		t.Fatalf("want a non-empty errMsg on a non-repo/timeout, got empty")
	}
	_ = strings.TrimSpace(errMsg)
}
