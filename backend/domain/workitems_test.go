package domain

import (
	"encoding/json"
	"testing"
)

// ---- MarshalJSON -----------------------------------------------------------------

func TestWorkItemMarshalJSON_nonResumable(t *testing.T) {
	w := WorkItem{Name: "foo", Path: "/p", Status: WorkInProgress, Done: 1, Total: 2}
	b, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["resumeTarget"]; ok {
		t.Error("non-resumable item should not have resumeTarget key")
	}
	if v, _ := m["completedSliceNums"].([]any); v == nil {
		t.Error("completedSliceNums should be [] not null")
	}
}

func TestWorkItemMarshalJSON_resumable(t *testing.T) {
	rt := &ResumeTarget{Slice: 2, Step: "qa"}
	w := WorkItem{
		Name: "my-prd", Path: "/x", Status: WorkDone, Done: 3, Total: 3,
		Resumable: true, ResumeTarget: rt, ActiveStep: nil,
	}
	b, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if _, ok := m["resumeTarget"]; !ok {
		t.Error("resumable item must have resumeTarget key")
	}
	if _, ok := m["activeStep"]; !ok {
		t.Error("resumable item must have activeStep key (even if null)")
	}
}

func TestNonNil(t *testing.T) {
	if got := nonNil(nil); got == nil {
		t.Error("nonNil(nil) must return empty slice, not nil")
	}
	in := []int{1, 2}
	if got := nonNil(in); len(got) != 2 {
		t.Errorf("nonNil preserves slice: got %v", got)
	}
}

// ---- ParseManifest ---------------------------------------------------------------

func TestParseManifest_valid(t *testing.T) {
	js := `{"prd":"foo.md","branch":"b","commitPolicy":"per-step","slices":[{"n":1,"status":"done","steps":{}}]}`
	m := ParseManifest(js)
	if m == nil {
		t.Fatal("expected non-nil manifest")
	}
	if len(m.Slices) != 1 {
		t.Errorf("expected 1 slice, got %d", len(m.Slices))
	}
	if m.PRD != "foo.md" {
		t.Errorf("PRD: got %q", m.PRD)
	}
}

func TestParseManifest_badJSON(t *testing.T) {
	if ParseManifest("not-json") != nil {
		t.Error("bad JSON should return nil")
	}
}

func TestParseManifest_missingSlices(t *testing.T) {
	if ParseManifest(`{"prd":"x"}`) != nil {
		t.Error("missing slices key should return nil")
	}
}

func TestParseManifest_nonArraySlices(t *testing.T) {
	if ParseManifest(`{"slices":"bad"}`) != nil {
		t.Error("non-array slices should return nil")
	}
}

// ---- stepVerified ----------------------------------------------------------------

func TestStepVerified_rplan(t *testing.T) {
	v := ResumeVerifiers{
		FileExists:   func(p string) bool { return p == "/plan.md" },
		CommitExists: func(s string) bool { return false },
	}
	sl := ManifestSlice{N: 1, Steps: map[string]ManifestStep{
		"rplan": {Status: "done", Artifact: "/plan.md"},
	}}
	if !stepVerified("rplan", sl, v) {
		t.Error("rplan step with existing artifact should be verified")
	}
}

func TestStepVerified_rplanMissingArtifact(t *testing.T) {
	v := ResumeVerifiers{FileExists: func(string) bool { return false }, CommitExists: func(string) bool { return false }}
	sl := ManifestSlice{N: 1, Steps: map[string]ManifestStep{"rplan": {Status: "done", Artifact: ""}}}
	if stepVerified("rplan", sl, v) {
		t.Error("rplan with empty artifact should not be verified")
	}
}

func TestStepVerified_approved(t *testing.T) {
	v := ResumeVerifiers{
		FileExists:   func(string) bool { return false },
		CommitExists: func(s string) bool { return s == "abc123" },
	}
	sl := ManifestSlice{N: 1, Steps: map[string]ManifestStep{
		"approved": {Status: "done", Commit: "abc123"},
	}}
	if !stepVerified("approved", sl, v) {
		t.Error("approved step with existing commit should be verified")
	}
}

func TestStepVerified_notDone(t *testing.T) {
	v := ResumeVerifiers{FileExists: func(string) bool { return true }, CommitExists: func(string) bool { return true }}
	sl := ManifestSlice{N: 1, Steps: map[string]ManifestStep{"rplan": {Status: "in-progress", Artifact: "/f"}}}
	if stepVerified("rplan", sl, v) {
		t.Error("non-done step should not be verified")
	}
}

func TestStepVerified_missingStep(t *testing.T) {
	v := ResumeVerifiers{FileExists: func(string) bool { return true }, CommitExists: func(string) bool { return true }}
	sl := ManifestSlice{N: 1, Steps: map[string]ManifestStep{}}
	if stepVerified("rplan", sl, v) {
		t.Error("missing step should not be verified")
	}
}

func TestStepVerified_qaAlwaysFalse(t *testing.T) {
	v := ResumeVerifiers{FileExists: func(string) bool { return true }, CommitExists: func(string) bool { return true }}
	sl := ManifestSlice{N: 1, Steps: map[string]ManifestStep{"qa": {Status: "done"}}}
	if stepVerified("qa", sl, v) {
		t.Error("qa step should never be verified (no durable artifact)")
	}
}

// ---- sortedSlices ----------------------------------------------------------------

func TestSortedSlices(t *testing.T) {
	in := []ManifestSlice{{N: 3}, {N: 1}, {N: 2}}
	out := sortedSlices(in)
	if out[0].N != 1 || out[1].N != 2 || out[2].N != 3 {
		t.Errorf("not sorted: %v", out)
	}
	// original must be unmodified
	if in[0].N != 3 {
		t.Error("sortedSlices should not mutate the input")
	}
}

// ---- ComputeResumeTarget ---------------------------------------------------------

func TestComputeResumeTarget_nil(t *testing.T) {
	v := ResumeVerifiers{FileExists: func(string) bool { return true }, CommitExists: func(string) bool { return true }}
	if ComputeResumeTarget(nil, v) != nil {
		t.Error("nil manifest should return nil")
	}
}

func TestComputeResumeTarget_notPerStep(t *testing.T) {
	m := &BuildManifest{CommitPolicy: "per-slice", Slices: []ManifestSlice{{N: 1}}}
	v := ResumeVerifiers{FileExists: func(string) bool { return true }, CommitExists: func(string) bool { return true }}
	if ComputeResumeTarget(m, v) != nil {
		t.Error("non-per-step policy should return nil")
	}
}

func TestComputeResumeTarget_resumesAtRplan(t *testing.T) {
	m := &BuildManifest{
		CommitPolicy: "per-step",
		Slices:       []ManifestSlice{{N: 1, Status: "in-progress", Steps: map[string]ManifestStep{}}},
	}
	v := ResumeVerifiers{FileExists: func(string) bool { return false }, CommitExists: func(string) bool { return false }}
	rt := ComputeResumeTarget(m, v)
	if rt == nil || rt.Step != "rplan" || rt.Slice != 1 {
		t.Errorf("expected rplan resume at slice 1, got %v", rt)
	}
}

func TestComputeResumeTarget_allDone(t *testing.T) {
	m := &BuildManifest{
		CommitPolicy: "per-step",
		Slices: []ManifestSlice{{
			N: 1, Status: "done",
			Steps: map[string]ManifestStep{"approved": {Status: "done", Commit: "sha1"}},
		}},
	}
	v := ResumeVerifiers{FileExists: func(string) bool { return true }, CommitExists: func(s string) bool { return s == "sha1" }}
	if rt := ComputeResumeTarget(m, v); rt != nil {
		t.Errorf("all done slices should return nil, got %v", rt)
	}
}

// ---- ActiveStepFor ---------------------------------------------------------------

func TestActiveStepFor_nilManifest(t *testing.T) {
	v := ResumeVerifiers{FileExists: func(string) bool { return true }, CommitExists: func(string) bool { return true }}
	if ActiveStepFor(nil, nil, v, func(int) bool { return false }) != nil {
		t.Error("nil manifest → nil")
	}
}

func TestActiveStepFor_rplanNotDone(t *testing.T) {
	m := &BuildManifest{Slices: []ManifestSlice{{N: 1, Steps: map[string]ManifestStep{}}}}
	v := ResumeVerifiers{FileExists: func(string) bool { return false }, CommitExists: func(string) bool { return false }}
	got := ActiveStepFor(m, nil, v, func(int) bool { return false })
	if got == nil || got.Step != "rplan" {
		t.Errorf("expected rplan, got %v", got)
	}
}

func TestActiveStepFor_approvedNotDone(t *testing.T) {
	m := &BuildManifest{Slices: []ManifestSlice{{
		N:     1,
		Steps: map[string]ManifestStep{"rplan": {Artifact: "/plan.md"}},
	}}}
	v := ResumeVerifiers{FileExists: func(p string) bool { return p == "/plan.md" }, CommitExists: func(string) bool { return false }}
	got := ActiveStepFor(m, nil, v, func(int) bool { return false })
	if got == nil || got.Step != "approved" {
		t.Errorf("expected approved, got %v", got)
	}
}

func TestActiveStepFor_atQA(t *testing.T) {
	m := &BuildManifest{Slices: []ManifestSlice{{
		N: 1,
		Steps: map[string]ManifestStep{
			"rplan":    {Artifact: "/plan.md"},
			"approved": {Commit: "sha1"},
		},
	}}}
	v := ResumeVerifiers{FileExists: func(p string) bool { return p == "/plan.md" }, CommitExists: func(s string) bool { return s == "sha1" }}
	got := ActiveStepFor(m, nil, v, func(int) bool { return false })
	if got == nil || got.Step != "qa" {
		t.Errorf("expected qa, got %v", got)
	}
}

func TestActiveStepFor_allCompleted(t *testing.T) {
	m := &BuildManifest{Slices: []ManifestSlice{{N: 1}}}
	v := ResumeVerifiers{FileExists: func(string) bool { return true }, CommitExists: func(string) bool { return true }}
	got := ActiveStepFor(m, []int{1}, v, func(int) bool { return false })
	if got != nil {
		t.Errorf("all completed → nil, got %v", got)
	}
}

func TestActiveStepFor_planExistsFallback(t *testing.T) {
	m := &BuildManifest{Slices: []ManifestSlice{{N: 2, Steps: map[string]ManifestStep{}}}}
	v := ResumeVerifiers{FileExists: func(string) bool { return false }, CommitExists: func(string) bool { return false }}
	got := ActiveStepFor(m, nil, v, func(n int) bool { return n == 2 })
	if got == nil || got.Step != "approved" {
		t.Errorf("planExists fallback: expected approved, got %v", got)
	}
}

// ---- atoi -----------------------------------------------------------------------

func TestAtoi(t *testing.T) {
	cases := map[string]int{"0": 0, "1": 1, "42": 42, "100": 100}
	for s, want := range cases {
		if got := atoi(s); got != want {
			t.Errorf("atoi(%q) = %d, want %d", s, got, want)
		}
	}
}

// ---- CompletedSliceNums ----------------------------------------------------------

func TestCompletedSliceNums(t *testing.T) {
	md := "- [x] 1. Slice one\n- [x] Slice 2 — title\n- [ ] 3. not done\n"
	got := CompletedSliceNums(md)
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Errorf("got %v, want [1 2]", got)
	}
}

func TestCompletedSliceNums_empty(t *testing.T) {
	if got := CompletedSliceNums("no checkboxes"); len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

// ---- BlockedSliceNums / DeferredSliceNums ----------------------------------------

func TestBlockedSliceNums(t *testing.T) {
	md := "- [ ] 1. BLOCKED something\n- [ ] 2. BLOCKED later\n- [ ] 3. normal\n"
	got := BlockedSliceNums(md)
	if len(got) != 1 || got[0] != 1 {
		t.Errorf("got %v, want [1]", got)
	}
}

func TestDeferredSliceNums(t *testing.T) {
	md := "- [ ] 1. BLOCKED later\n- [ ] 2. BLOCKED now\n"
	got := DeferredSliceNums(md)
	if len(got) != 1 || got[0] != 1 {
		t.Errorf("got %v, want [1]", got)
	}
}

func TestUncheckedSliceNum_formatB(t *testing.T) {
	line := "- [ ] Slice 3 — something"
	if got := uncheckedSliceNum(line); got != 3 {
		t.Errorf("got %d, want 3", got)
	}
}

func TestUncheckedSliceNum_notUnchecked(t *testing.T) {
	if got := uncheckedSliceNum("- [x] 1. done"); got != -1 {
		t.Errorf("checked line should return -1, got %d", got)
	}
}

func TestUncheckedSliceNum_strikethrough(t *testing.T) {
	// ~~ is stripped before matching; dash must follow the number per uncheckedNumB regex
	line := "- [ ] ~~Slice 4 — title~~"
	if got := uncheckedSliceNum(line); got != 4 {
		t.Errorf("got %d, want 4", got)
	}
}

// ---- ClassifyCheckboxes ----------------------------------------------------------

func TestClassifyCheckboxes_notStarted(t *testing.T) {
	p := ClassifyCheckboxes("- [ ] one\n- [ ] two\n")
	if p.Status != WorkNotStarted || p.Done != 0 || p.Total != 2 {
		t.Errorf("unexpected %+v", p)
	}
}

func TestClassifyCheckboxes_inProgress(t *testing.T) {
	p := ClassifyCheckboxes("- [x] one\n- [ ] two\n")
	if p.Status != WorkInProgress || p.Done != 1 {
		t.Errorf("unexpected %+v", p)
	}
}

func TestClassifyCheckboxes_done(t *testing.T) {
	p := ClassifyCheckboxes("- [x] one\n- [X] two\n")
	if p.Status != WorkDone || p.Done != 2 || p.Total != 2 {
		t.Errorf("unexpected %+v", p)
	}
}

func TestClassifyCheckboxes_noBoxes(t *testing.T) {
	p := ClassifyCheckboxes("no checkboxes here")
	if p.Status != WorkNotStarted || p.Total != 0 {
		t.Errorf("unexpected %+v", p)
	}
}

// ---- PrdBuildStatus -------------------------------------------------------------

func TestPrdBuildStatus_nil(t *testing.T) {
	if PrdBuildStatus(nil) != WorkNotStarted {
		t.Error("nil → not-started")
	}
}

func TestPrdBuildStatus_complete(t *testing.T) {
	s := "PRD_BUILD_COMPLETE\n"
	if PrdBuildStatus(&s) != WorkDone {
		t.Error("PRD_BUILD_COMPLETE → done")
	}
}

func TestPrdBuildStatus_inProgress(t *testing.T) {
	s := "slice 1 done"
	if PrdBuildStatus(&s) != WorkInProgress {
		t.Error("non-complete progress → in-progress")
	}
}

// ---- Classify -------------------------------------------------------------------

func TestClassify_prdByName(t *testing.T) {
	if got := Classify("/cwd", "/cwd/tasks/prd-foo.md"); got != KindPRD {
		t.Errorf("prd-foo.md should be KindPRD, got %q", got)
	}
}

func TestClassify_planByName(t *testing.T) {
	if got := Classify("/cwd", "/cwd/tasks/foo-plan.md"); got != KindPlan {
		t.Errorf("foo-plan.md should be KindPlan, got %q", got)
	}
}

func TestClassify_planUnderPrdDir(t *testing.T) {
	if got := Classify("/cwd", "/cwd/prds/foo-plan.md"); got != KindPRD {
		t.Errorf("plan file under prds/ should be KindPRD, got %q", got)
	}
}

func TestClassify_excludeReview(t *testing.T) {
	if got := Classify("/cwd", "/cwd/prd-foo-review.md"); got != KindNone {
		t.Errorf("review file should be KindNone, got %q", got)
	}
}

func TestClassify_excludeProgress(t *testing.T) {
	if got := Classify("/cwd", "/cwd/prd-foo-progress.md"); got != KindNone {
		t.Errorf("progress file should be KindNone, got %q", got)
	}
}

func TestClassify_excludeBuildPrefix(t *testing.T) {
	if got := Classify("/cwd", "/cwd/tasks/.build-foo.md"); got != KindNone {
		t.Errorf(".build- file should be KindNone, got %q", got)
	}
}

func TestClassify_excludePlanProto(t *testing.T) {
	if got := Classify("/cwd", "/cwd/prd-planproto-foo.md"); got != KindNone {
		t.Errorf("prd-planproto- should be KindNone, got %q", got)
	}
}

func TestClassify_unclassified(t *testing.T) {
	if got := Classify("/cwd", "/cwd/readme.md"); got != KindNone {
		t.Errorf("readme.md should be KindNone, got %q", got)
	}
}

// ---- dirTokens ------------------------------------------------------------------

func TestDirTokens(t *testing.T) {
	tokens := dirTokens("/cwd", "/cwd/tasks/sub/file.md")
	if len(tokens) != 2 || tokens[0] != "tasks" || tokens[1] != "sub" {
		t.Errorf("got %v", tokens)
	}
}

func TestDirTokens_rootFile(t *testing.T) {
	if got := dirTokens("/cwd", "/cwd/file.md"); len(got) != 0 {
		t.Errorf("file at root: want [], got %v", got)
	}
}

// ---- LabelFor -------------------------------------------------------------------

func TestLabelFor_genericName(t *testing.T) {
	if got := LabelFor("/cwd/myfeature/plan.md"); got != "myfeature" {
		t.Errorf("got %q, want myfeature", got)
	}
}

func TestLabelFor_specificName(t *testing.T) {
	if got := LabelFor("/cwd/foo-plan.md"); got != "foo-plan.md" {
		t.Errorf("got %q, want foo-plan.md", got)
	}
}

// ---- dirStyleStem ---------------------------------------------------------------

func TestDirStyleStem(t *testing.T) {
	if !dirStyleStem("/a/feature/plan.md") {
		t.Error("plan.md should be dir style")
	}
	if !dirStyleStem("/a/feature/prd.md") {
		t.Error("prd.md should be dir style")
	}
	if dirStyleStem("/a/foo-plan.md") {
		t.Error("foo-plan.md should not be dir style")
	}
}

// ---- PrdSlug --------------------------------------------------------------------

func TestPrdSlug_flatFile(t *testing.T) {
	if got := PrdSlug("/tasks/prd-my-feature.md"); got != "my-feature" {
		t.Errorf("got %q, want my-feature", got)
	}
}

func TestPrdSlug_dirStyle(t *testing.T) {
	if got := PrdSlug("/cwd/my-feature/prd.md"); got != "my-feature" {
		t.Errorf("got %q, want my-feature", got)
	}
}

func TestPrdSlug_noSlug(t *testing.T) {
	if got := PrdSlug("/cwd/readme.md"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// ---- BuildArtifactCandidates ----------------------------------------------------

func TestBuildArtifactCandidates_dirStyle(t *testing.T) {
	got := BuildArtifactCandidates("/cwd", "/cwd/myfeat/prd.md", "progress")
	if len(got) == 0 {
		t.Fatal("expected candidates")
	}
	found := false
	for _, c := range got {
		if c == "/cwd/myfeat/.build-progress.md" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected dir-style candidate, got %v", got)
	}
}

func TestBuildArtifactCandidates_flatFile(t *testing.T) {
	got := BuildArtifactCandidates("/cwd", "/cwd/tasks/prd-my-feature.md", "state")
	if len(got) == 0 {
		t.Fatal("expected candidates")
	}
}

// ---- LocaleLess -----------------------------------------------------------------

func TestLocaleLess(t *testing.T) {
	if !LocaleLess("alpha", "beta") {
		t.Error("alpha < beta")
	}
	if LocaleLess("beta", "alpha") {
		t.Error("beta is not < alpha")
	}
	if !LocaleLess("Alpha", "beta") {
		t.Error("case-insensitive: Alpha < beta")
	}
	if LocaleLess("same", "same") {
		t.Error("same is not < same")
	}
}
