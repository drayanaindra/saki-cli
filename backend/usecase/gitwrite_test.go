package usecase

import (
	"errors"
	"strings"
	"testing"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

type fakeGitWriter struct {
	branches    []string
	switchOK    bool
	switchErr   string
	switchCalls int
	lastCreate  bool

	remotes    []string
	defBranch  string
	curBranch  string
	mrURL      string
	mrErr      string
	mrCalls    int
	mrSrc, mrT string

	mergeOK    bool
	mergeErr   string
	mergeCalls int
	abortCalls int
	switchDirs []string // branches passed to Switch, in order (for the merge switch->merge->restore trace)
}

func (f *fakeGitWriter) ListBranches(string) ([]string, error) { return f.branches, nil }
func (f *fakeGitWriter) Switch(_, branch string, create bool) (bool, string) {
	f.switchCalls++
	f.lastCreate = create
	f.switchDirs = append(f.switchDirs, branch)
	return f.switchOK, f.switchErr
}
func (f *fakeGitWriter) Remotes(string) []string         { return f.remotes }
func (f *fakeGitWriter) DefaultBranch(string) string     { return f.defBranch }
func (f *fakeGitWriter) CurrentBranchName(string) string { return f.curBranch }
func (f *fakeGitWriter) CreateMR(_, source, target string) (string, string) {
	f.mrCalls++
	f.mrSrc, f.mrT = source, target
	return f.mrURL, f.mrErr
}
func (f *fakeGitWriter) Merge(_, _ string) (bool, string) {
	f.mergeCalls++
	return f.mergeOK, f.mergeErr
}
func (f *fakeGitWriter) MergeAbort(string) { f.abortCalls++ }

func TestGitWrite_Branches(t *testing.T) {
	svc := NewGitWriteService(&fakeGitWriter{branches: []string{"main", "dev"}})
	if _, err := svc.Branches(domain.Repo{Dir: ""}); !errors.Is(err, ErrCwdRequired) {
		t.Fatalf("missing cwd want ErrCwdRequired, got %v", err)
	}
	got, err := svc.Branches(domain.Repo{Dir: "/r"})
	if err != nil || len(got) != 2 {
		t.Fatalf("want 2 branches, got %v err %v", got, err)
	}
}

func TestGitWrite_Switch_requiresCwdAndBranch(t *testing.T) {
	w := &fakeGitWriter{switchOK: true}
	svc := NewGitWriteService(w)
	for _, c := range []struct{ dir, branch string }{{"", "feat"}, {"/r", ""}} {
		_, err := svc.Switch(domain.Repo{Dir: c.dir}, c.branch, false)
		var ve ValidationError
		if !errors.As(err, &ve) || ve.Msg != "cwd and branch (strings) are required" {
			t.Fatalf("dir=%q branch=%q want required-422, got %v", c.dir, c.branch, err)
		}
	}
	if w.switchCalls != 0 {
		t.Fatalf("writer must not run on a required-field error; calls=%d", w.switchCalls)
	}
}

// BR5: on the create path an invalid branch name is rejected BEFORE the writer runs.
func TestGitWrite_Switch_invalidCreateName_noGitRun(t *testing.T) {
	w := &fakeGitWriter{switchOK: true}
	svc := NewGitWriteService(w)
	_, err := svc.Switch(domain.Repo{Dir: "/r"}, "-rf", true)
	var ve ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("want ValidationError for '-rf', got %v", err)
	}
	if w.switchCalls != 0 {
		t.Fatalf("BR5 violated: writer ran on an invalid create name; calls=%d", w.switchCalls)
	}
}

func TestGitWrite_Switch_happy(t *testing.T) {
	w := &fakeGitWriter{switchOK: true}
	res, err := NewGitWriteService(w).Switch(domain.Repo{Dir: "/r"}, "feat", false)
	if err != nil || !res.OK || res.Branch != "feat" {
		t.Fatalf("happy: res=%+v err=%v", res, err)
	}
}

func TestGitWrite_Switch_gracefulFailure(t *testing.T) {
	w := &fakeGitWriter{switchOK: false, switchErr: "would be overwritten"}
	res, err := NewGitWriteService(w).Switch(domain.Repo{Dir: "/r"}, "feat", false)
	if err != nil {
		t.Fatalf("graceful failure must not error: %v", err)
	}
	if res.OK || res.Error != "would be overwritten" {
		t.Fatalf("want ok:false + git msg, got %+v", res)
	}
}

func TestGitWrite_CreateMR_cwdRequired(t *testing.T) {
	_, err := NewGitWriteService(&fakeGitWriter{}).CreateMR(domain.Repo{Dir: ""})
	var ve ValidationError
	if !errors.As(err, &ve) || ve.Msg != "cwd (string) is required" {
		t.Fatalf("want cwd-required 422, got %v", err)
	}
}

func TestGitWrite_CreateMR_gracefulGuards(t *testing.T) {
	cases := []struct {
		name    string
		w       *fakeGitWriter
		wantSub string
	}{
		{"no remote", &fakeGitWriter{remotes: nil, curBranch: "feat"}, "no git remote configured"},
		{"detached HEAD", &fakeGitWriter{remotes: []string{"origin"}, defBranch: "main", curBranch: ""}, "detached HEAD"},
		{"source==target", &fakeGitWriter{remotes: []string{"origin"}, defBranch: "main", curBranch: "main"}, "already on the target"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := NewGitWriteService(c.w).CreateMR(domain.Repo{Dir: "/r"})
			if err != nil {
				t.Fatalf("guard must not error: %v", err)
			}
			if res.OK || !strings.Contains(res.Error, c.wantSub) {
				t.Fatalf("want ok:false containing %q, got %+v", c.wantSub, res)
			}
			if c.w.mrCalls != 0 {
				t.Fatalf("glab must not run when a guard trips; calls=%d", c.w.mrCalls)
			}
		})
	}
}

func TestGitWrite_CreateMR_fallbackTargetAndHappy(t *testing.T) {
	// no origin/HEAD → target falls back to "main"; source is the current feature branch.
	w := &fakeGitWriter{remotes: []string{"origin"}, defBranch: "", curBranch: "feat", mrURL: "https://gl/mr/1"}
	res, err := NewGitWriteService(w).CreateMR(domain.Repo{Dir: "/r"})
	if err != nil || !res.OK || res.URL != "https://gl/mr/1" {
		t.Fatalf("happy: res=%+v err=%v", res, err)
	}
	if w.mrSrc != "feat" || w.mrT != "main" {
		t.Fatalf("want source=feat target=main, got src=%q target=%q", w.mrSrc, w.mrT)
	}
}

func TestGitWrite_CreateMR_glabFailure(t *testing.T) {
	w := &fakeGitWriter{remotes: []string{"origin"}, defBranch: "main", curBranch: "feat", mrErr: "glab: unauthorized"}
	res, err := NewGitWriteService(w).CreateMR(domain.Repo{Dir: "/r"})
	if err != nil {
		t.Fatalf("glab failure must not error: %v", err)
	}
	if res.OK || res.Error != "glab: unauthorized" {
		t.Fatalf("want ok:false + glab stderr, got %+v", res)
	}
}

func TestGitWrite_MergeToMain_cwdRequired(t *testing.T) {
	_, err := NewGitWriteService(&fakeGitWriter{}).MergeToMain(domain.Repo{Dir: ""})
	var ve ValidationError
	if !errors.As(err, &ve) || ve.Msg != "cwd (string) is required" {
		t.Fatalf("want cwd-required 422, got %v", err)
	}
}

func TestGitWrite_MergeToMain_guards(t *testing.T) {
	cases := []struct {
		name    string
		w       *fakeGitWriter
		wantSub string
	}{
		{"detached HEAD", &fakeGitWriter{curBranch: "", branches: []string{"main"}}, "detached HEAD"},
		{"no local base", &fakeGitWriter{curBranch: "feat", branches: []string{"feat", "dev"}}, "no local main or master"},
		{"source==target", &fakeGitWriter{curBranch: "main", branches: []string{"main"}}, `already on "main"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := NewGitWriteService(c.w).MergeToMain(domain.Repo{Dir: "/r"})
			if err != nil {
				t.Fatalf("guard must not error: %v", err)
			}
			if res.OK || !strings.Contains(res.Error, c.wantSub) {
				t.Fatalf("want ok:false containing %q, got %+v", c.wantSub, res)
			}
			if c.w.mergeCalls != 0 {
				t.Fatalf("merge must not run when a guard trips; calls=%d", c.w.mergeCalls)
			}
		})
	}
}

// BR1: a dirty-tree switch-to-target fails → nothing is merged (merge never runs).
func TestGitWrite_MergeToMain_dirtySwitchFail(t *testing.T) {
	w := &fakeGitWriter{curBranch: "feat", branches: []string{"main", "feat"}, switchOK: false, switchErr: "would be overwritten"}
	res, err := NewGitWriteService(w).MergeToMain(domain.Repo{Dir: "/r"})
	if err != nil || res.OK || res.Error != "would be overwritten" {
		t.Fatalf("want ok:false + switch stderr, got %+v err %v", res, err)
	}
	if w.mergeCalls != 0 {
		t.Fatalf("BR1: nothing must be merged when the switch fails; mergeCalls=%d", w.mergeCalls)
	}
}

func TestGitWrite_MergeToMain_happy(t *testing.T) {
	w := &fakeGitWriter{curBranch: "feat", branches: []string{"main", "feat"}, switchOK: true, mergeOK: true}
	res, err := NewGitWriteService(w).MergeToMain(domain.Repo{Dir: "/r"})
	if err != nil || !res.OK || res.Branch != "main" || res.Source != "feat" {
		t.Fatalf("happy: res=%+v err=%v", res, err)
	}
	if w.abortCalls != 0 {
		t.Fatalf("no abort on a clean merge; abortCalls=%d", w.abortCalls)
	}
}

// BR1: a merge conflict → abort the half-merge + switch back to the feature branch (tree restored).
func TestGitWrite_MergeToMain_conflictAbortsAndRestores(t *testing.T) {
	w := &fakeGitWriter{curBranch: "feat", branches: []string{"main", "feat"}, switchOK: true, mergeOK: false, mergeErr: "CONFLICT (content)"}
	res, err := NewGitWriteService(w).MergeToMain(domain.Repo{Dir: "/r"})
	if err != nil || res.OK || res.Error != "CONFLICT (content)" {
		t.Fatalf("want ok:false + merge stderr, got %+v err %v", res, err)
	}
	if w.abortCalls != 1 {
		t.Fatalf("conflict must abort the half-merge once; abortCalls=%d", w.abortCalls)
	}
	// switch order: to target (main) then BACK to the feature branch (feat) — the restore.
	if len(w.switchDirs) != 2 || w.switchDirs[0] != "main" || w.switchDirs[1] != "feat" {
		t.Fatalf("want switch main then restore feat, got %v", w.switchDirs)
	}
}
