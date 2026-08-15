package domain

import "testing"

func TestIsLocked(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"col0 marker", "<!-- prd-locked: @a · 2026-07-15 · ui:none -->\n# PRD", true},
		{"marker after body", "# PRD\n\n<!-- prd-locked: @a · d · ui:none -->\nx", true},
		{"mid-prose mention", "The build greps for `<!-- prd-locked:` at column 0.", false},
		{"indented (not col0)", "  <!-- prd-locked: x -->", false},
		{"no marker", "# PRD\n**Status:** Draft", false},
	}
	for _, c := range cases {
		if got := IsLocked(c.in); got != c.want {
			t.Errorf("%s: IsLocked = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestBuildLockMarker(t *testing.T) {
	got := BuildLockMarker("@alice", "2026-07-15", "tasks/proto-foo/")
	want := "<!-- prd-locked: @alice · 2026-07-15 · ui:tasks/proto-foo/ -->"
	if got != want {
		t.Errorf("BuildLockMarker = %q, want %q", got, want)
	}
}

func TestLockUiValue(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "none"},
		{"tasks/proto-foo/preview.html", "tasks/proto-foo/"},
		{"apps/web/tasks/proto-bar/preview.html", "apps/web/tasks/proto-bar/"},
	}
	for _, c := range cases {
		if got := LockUiValue(c.in); got != c.want {
			t.Errorf("LockUiValue(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestResolveApprover(t *testing.T) {
	cases := []struct {
		name    string
		content string
		gitUser string
		want    string
	}{
		{"named owner gets @", "**Owner:** Alice · **Status:** Draft", "", "@Alice"},
		{"owner already @", "**Owner:** @alice · **Status:** Draft", "", "@alice"},
		{"unassigned falls to git user", "**Owner:** unassigned · **Status:** Draft", "Bob", "@Bob"},
		{"unassigned no git → unassigned", "**Owner:** unassigned", "", "unassigned"},
		{"no owner line → git user", "# PRD\n**Status:** Draft", "Bob", "@Bob"},
		{"no owner + no git → unassigned", "# PRD", "", "unassigned"},
		{"no-space separator ` ·x`", "**Owner:** Bob ·x", "", "@Bob"},
		{"owner is last field (EOL)", "**Owner:** Carol", "", "@Carol"},
		{"UNASSIGNED case-insensitive", "**Owner:** UNASSIGNED · x", "Dan", "@Dan"},
	}
	for _, c := range cases {
		if got := ResolveApprover(c.content, c.gitUser); got != c.want {
			t.Errorf("%s: ResolveApprover = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestApplyLock(t *testing.T) {
	const approver, date, ui = "@alice", "2026-07-15", "none"
	marker := BuildLockMarker(approver, date, ui)

	t.Run("fresh flips Status + prepends marker", func(t *testing.T) {
		in := "# PRD\n**Owner:** unassigned · **Status:** Draft · **Updated:** x"
		out, already := ApplyLock(in, approver, date, ui)
		if already {
			t.Fatal("alreadyLocked = true on a fresh PRD")
		}
		want := marker + "\n# PRD\n**Owner:** unassigned · **Status:** Locked · **Updated:** x"
		if out != want {
			t.Errorf("out = %q, want %q", out, want)
		}
	})

	t.Run("already-locked is byte-identical no-op", func(t *testing.T) {
		in := marker + "\n# PRD\n**Status:** Locked"
		out, already := ApplyLock(in, "@other", "1999-01-01", "x")
		if !already || out != in {
			t.Errorf("want byte-identical no-op; already=%v out=%q", already, out)
		}
	})

	t.Run("no Status field → marker-only", func(t *testing.T) {
		in := "# PRD\nno status here"
		out, already := ApplyLock(in, approver, date, ui)
		if already {
			t.Fatal("alreadyLocked true")
		}
		if out != marker+"\n"+in {
			t.Errorf("out = %q, want marker-only prepend", out)
		}
	})

	t.Run("Status ` · ` trailer preserved", func(t *testing.T) {
		out, _ := ApplyLock("**Status:** Draft · **Updated:** 2026", approver, date, ui)
		want := marker + "\n**Status:** Locked · **Updated:** 2026"
		if out != want {
			t.Errorf("out = %q, want %q", out, want)
		}
	})

	t.Run("multi-word status flips wholesale", func(t *testing.T) {
		out, _ := ApplyLock("**Status:** In Review · **X:** y", approver, date, ui)
		want := marker + "\n**Status:** Locked · **X:** y"
		if out != want {
			t.Errorf("out = %q, want %q", out, want)
		}
	})

	t.Run("trailing whitespace preserved (EOL)", func(t *testing.T) {
		out, _ := ApplyLock("**Status:** Active   ", approver, date, ui)
		want := marker + "\n**Status:** Locked   "
		if out != want {
			t.Errorf("out = %q, want %q", out, want)
		}
	})

	t.Run("no-space separator ` ·note`", func(t *testing.T) {
		out, _ := ApplyLock("**Status:** Draft ·note", approver, date, ui)
		want := marker + "\n**Status:** Locked ·note"
		if out != want {
			t.Errorf("out = %q, want %q", out, want)
		}
	})

	t.Run("only the FIRST Status flips", func(t *testing.T) {
		in := "**Status:** Draft\nbody\n**Status:** Draft"
		out, _ := ApplyLock(in, approver, date, ui)
		want := marker + "\n**Status:** Locked\nbody\n**Status:** Draft"
		if out != want {
			t.Errorf("out = %q, want only-first-flipped %q", out, want)
		}
	})
}

func TestValidateLockRequest(t *testing.T) {
	always := func(string) bool { return true }
	never := func(string) bool { return false }

	t.Run("escapes and bad paths → 422", func(t *testing.T) {
		bad := []struct{ path, cwd string }{
			{"../secret.md", "/a/proj"},
			{"../../etc/passwd.md", "/a/proj"},
			{"a/b/../../../x.md", "/a/proj"},
			{"/etc/passwd.md", "/a/proj"},    // absolute outside cwd
			{"../proj-evil/x.md", "/a/proj"}, // sibling-prefix escape
			{"foo.txt", "/a/proj"},           // non-.md
			{"prd.md", ""},                   // missing cwd
			{"", "/a/proj"},                  // empty path
		}
		for _, b := range bad {
			r := ValidateLockRequest(b.path, b.cwd, always)
			if r.OK || r.Status != 422 {
				t.Errorf("path=%q cwd=%q: want 422 !OK, got OK=%v status=%d", b.path, b.cwd, r.OK, r.Status)
			}
		}
	})

	t.Run("valid relative path resolves under cwd", func(t *testing.T) {
		r := ValidateLockRequest("sub/prd.md", "/a/proj", always)
		if !r.OK || r.AbsPath != "/a/proj/sub/prd.md" {
			t.Errorf("want OK abs=/a/proj/sub/prd.md, got OK=%v abs=%q", r.OK, r.AbsPath)
		}
	})

	t.Run("absolute path inside cwd is not mangled", func(t *testing.T) {
		r := ValidateLockRequest("/a/proj/sub/prd.md", "/a/proj", always)
		if !r.OK || r.AbsPath != "/a/proj/sub/prd.md" {
			t.Errorf("want OK abs=/a/proj/sub/prd.md (no mangle), got OK=%v abs=%q", r.OK, r.AbsPath)
		}
	})

	t.Run("missing file → 422 (never 500)", func(t *testing.T) {
		r := ValidateLockRequest("sub/prd.md", "/a/proj", never)
		if r.OK || r.Status != 422 {
			t.Errorf("want 422 !OK, got OK=%v status=%d", r.OK, r.Status)
		}
	})
}
