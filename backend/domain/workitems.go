package domain

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
)

// Pure core for the board's work-queue read path: classify a markdown file as a PRD/plan, read its
// checkbox progress, and compute /build resume targets from a state manifest. Faithful port of the
// PURE helpers in apps/server/src/workitems.ts — every fs/git touch lives in usecase/infra (Step 5/6),
// so this file never reads the disk. R1 (read-only) holds structurally: no writer exists here.

// WorkStatus mirrors the TS union 'not-started' | 'in-progress' | 'done'.
type WorkStatus string

const (
	WorkNotStarted WorkStatus = "not-started"
	WorkInProgress WorkStatus = "in-progress"
	WorkDone       WorkStatus = "done"
)

// ResumeTarget is a (slice, step) the /build engine can resume at. JSON shape mirrors the TS type.
type ResumeTarget struct {
	Slice int    `json:"slice"`
	Step  string `json:"step"`
}

// WorkItem is one PRD/plan work-queue entry. Resumable controls the present-vs-absent JSON distinction
// for resumeTarget/activeStep (see MarshalJSON): a PRD with a progress/manifest branch always emits
// both keys (value or null), a plain plan/PRD omits both (parity with workitems.ts buildItem).
type WorkItem struct {
	Name               string
	Path               string
	Status             WorkStatus
	Done               int
	Total              int
	CompletedSliceNums []int
	BlockedSliceNums   []int
	DeferredSliceNums  []int
	Resumable          bool
	ResumeTarget       *ResumeTarget
	ActiveStep         *ResumeTarget
}

// MarshalJSON reproduces the TS res.json shape: a resumable PRD emits resumeTarget + activeStep
// (value or null); a non-resumable item omits both keys. Slice fields serialise as [] never null.
func (w WorkItem) MarshalJSON() ([]byte, error) {
	type base struct {
		Name               string     `json:"name"`
		Path               string     `json:"path"`
		Status             WorkStatus `json:"status"`
		Done               int        `json:"done"`
		Total              int        `json:"total"`
		CompletedSliceNums []int      `json:"completedSliceNums"`
		BlockedSliceNums   []int      `json:"blockedSliceNums"`
		DeferredSliceNums  []int      `json:"deferredSliceNums"`
	}
	b := base{
		Name: w.Name, Path: w.Path, Status: w.Status, Done: w.Done, Total: w.Total,
		CompletedSliceNums: nonNil(w.CompletedSliceNums),
		BlockedSliceNums:   nonNil(w.BlockedSliceNums),
		DeferredSliceNums:  nonNil(w.DeferredSliceNums),
	}
	if !w.Resumable {
		return json.Marshal(b)
	}
	return json.Marshal(struct {
		base
		ResumeTarget *ResumeTarget `json:"resumeTarget"`
		ActiveStep   *ResumeTarget `json:"activeStep"`
	}{base: b, ResumeTarget: w.ResumeTarget, ActiveStep: w.ActiveStep})
}

func nonNil(s []int) []int {
	if s == nil {
		return []int{}
	}
	return s
}

// ---- /build state manifest (tasks/.build-<slug>-state.json) -------------------------------------

// ManifestStep mirrors { status, artifact?, commit? }.
type ManifestStep struct {
	Status   string `json:"status"`
	Artifact string `json:"artifact"`
	Commit   string `json:"commit"`
}

// ManifestSlice mirrors { n, title?, status, steps }.
type ManifestSlice struct {
	N      int                     `json:"n"`
	Title  string                  `json:"title"`
	Status string                  `json:"status"`
	Steps  map[string]ManifestStep `json:"steps"`
}

// BuildManifest mirrors the /build state.json shape.
type BuildManifest struct {
	PRD          string          `json:"prd"`
	Branch       string          `json:"branch"`
	CommitPolicy string          `json:"commitPolicy"`
	Slices       []ManifestSlice `json:"slices"`
}

// ResumeVerifiers inject the fs/git checks so the resume logic stays pure/testable (parity with the
// TS interface). Real impls live in usecase/infra.
type ResumeVerifiers struct {
	FileExists   func(path string) bool
	CommitExists func(sha string) bool
}

// stepOrder is the /build per-slice step order. qa + reviewer have no durable artifact → always re-run.
var stepOrder = []string{"rplan", "approved", "qa", "reviewer"}

// ParseManifest tolerantly parses a state.json: bad JSON or a missing/non-array slices → nil (unknown
// shape treated as not-done). Mirrors workitems.ts parseManifest.
func ParseManifest(jsonStr string) *BuildManifest {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
		return nil
	}
	if _, ok := raw["slices"]; !ok {
		return nil
	}
	var slices []ManifestSlice
	if err := json.Unmarshal(raw["slices"], &slices); err != nil {
		return nil // slices present but not an array
	}
	var m BuildManifest
	_ = json.Unmarshal([]byte(jsonStr), &m)
	m.Slices = slices
	return &m
}

// stepVerified is true only when a step is BOTH marked done AND its durable artifact verifies. qa/
// reviewer have no artifact so they never count as verified-done (parity with workitems.ts).
func stepVerified(step string, slice ManifestSlice, v ResumeVerifiers) bool {
	s, ok := slice.Steps[step]
	if !ok || s.Status != "done" {
		return false
	}
	switch step {
	case "rplan":
		return s.Artifact != "" && v.FileExists(s.Artifact)
	case "approved":
		return s.Commit != "" && v.CommitExists(s.Commit)
	}
	return false
}

// ComputeResumeTarget returns the (slice, step) an interrupted build should resume at, or nil.
// Renamed from the TS `resumeTarget` (Go forbids a func + type sharing an identifier in one package).
// Mirrors workitems.ts resumeTarget.
func ComputeResumeTarget(m *BuildManifest, v ResumeVerifiers) *ResumeTarget {
	if m == nil || m.CommitPolicy != "per-step" || m.Slices == nil {
		return nil
	}
	for _, slice := range sortedSlices(m.Slices) {
		if slice.Status == "done" && stepVerified("approved", slice, v) {
			continue
		}
		for _, step := range stepOrder {
			if step == "qa" || step == "reviewer" {
				return &ResumeTarget{Slice: slice.N, Step: step}
			}
			if !stepVerified(step, slice, v) {
				return &ResumeTarget{Slice: slice.N, Step: step}
			}
		}
		return &ResumeTarget{Slice: slice.N, Step: "qa"}
	}
	return nil
}

// ActiveStepFor is the step the running slice is CURRENTLY on, for the detail-panel pill. Pure —
// planExists(n) is injected (the fs plan-artifact fallback lives in usecase). Mirrors workitems.ts
// activeStepFor, with planArtifactExists lifted to the injected planExists.
func ActiveStepFor(manifest *BuildManifest, completed []int, v ResumeVerifiers, planExists func(n int) bool) *ResumeTarget {
	if manifest == nil || len(manifest.Slices) == 0 {
		return nil
	}
	done := map[int]bool{}
	for _, n := range completed {
		done[n] = true
	}
	var running *ManifestSlice
	for _, s := range sortedSlices(manifest.Slices) {
		if !done[s.N] {
			sc := s
			running = &sc
			break
		}
	}
	if running == nil {
		return nil
	}
	n := running.N
	planArtifact := running.Steps["rplan"].Artifact
	rplanDone := (planArtifact != "" && v.FileExists(planArtifact)) || planExists(n)
	if !rplanDone {
		return &ResumeTarget{Slice: n, Step: "rplan"}
	}
	commit := running.Steps["approved"].Commit
	if commit == "" || !v.CommitExists(commit) {
		return &ResumeTarget{Slice: n, Step: "approved"}
	}
	return &ResumeTarget{Slice: n, Step: "qa"}
}

func sortedSlices(in []ManifestSlice) []ManifestSlice {
	out := make([]ManifestSlice, len(in))
	copy(out, in)
	// insertion sort by N (stable, tiny n) — avoids importing sort for a handful of slices.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].N < out[j-1].N; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// ---- progress-file checkbox parsing -------------------------------------------------------------

var (
	checkedRe       = regexp.MustCompile(`(?m)^\s*[-*]\s*\[[xX]\]`)
	anyBoxRe        = regexp.MustCompile(`(?m)^\s*[-*]\s*\[[ xX]\]`)
	uncheckedLineRe = regexp.MustCompile(`^\s*[-*]\s*\[ \]`)
	checkedLineRe   = regexp.MustCompile(`^\s*[-*]\s*\[[xX]\]`)
	completedNumA   = regexp.MustCompile(`^\s*[-*]\s*\[[xX]\]\s*(\d+)\.`)
	completedNumB   = regexp.MustCompile(`^\s*[-*]\s*\[[xX]\]\s*\*{0,2}Slice\s+(\d+)\s*[—\-]`)
	uncheckedNumA   = regexp.MustCompile(`^\s*[-*]\s*\[ \]\s*(\d+)\.`)
	uncheckedNumB   = regexp.MustCompile(`^\s*[-*]\s*\[ \]\s*\*{0,2}Slice\s+(\d+)\s*[—\-]`)
	blockedMarkerRe = regexp.MustCompile(`(?i)BLOCKED`)
	laterMarkerRe   = regexp.MustCompile(`(?i)later`)
	buildCompleteRe = regexp.MustCompile(`(?i)PRD_BUILD_COMPLETE|/build COMPLETE`)
)

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

// CompletedSliceNums extracts which numbered slices are marked done (Format A "- [x] 1." or Format B
// "- [x] Slice 1 —"). Mirrors workitems.ts completedSliceNums.
func CompletedSliceNums(md string) []int {
	nums := []int{}
	for _, line := range strings.Split(md, "\n") {
		if !checkedLineRe.MatchString(line) {
			continue
		}
		if m := completedNumA.FindStringSubmatch(line); m != nil {
			nums = append(nums, atoi(m[1]))
			continue
		}
		if m := completedNumB.FindStringSubmatch(line); m != nil {
			nums = append(nums, atoi(m[1]))
		}
	}
	return nums
}

// uncheckedSliceNum reads a slice number from an unchecked-checkbox line (after stripping ~~), or -1.
func uncheckedSliceNum(line string) int {
	if !uncheckedLineRe.MatchString(line) {
		return -1
	}
	s := strings.ReplaceAll(line, "~~", "")
	if m := uncheckedNumA.FindStringSubmatch(s); m != nil {
		return atoi(m[1])
	}
	if m := uncheckedNumB.FindStringSubmatch(s); m != nil {
		return atoi(m[1])
	}
	return -1
}

// BlockedSliceNums extracts unchecked BLOCKED-and-actionable slices (LATER-gated ones excluded).
func BlockedSliceNums(md string) []int {
	nums := []int{}
	for _, line := range strings.Split(md, "\n") {
		n := uncheckedSliceNum(line)
		if n < 0 || !blockedMarkerRe.MatchString(line) || laterMarkerRe.MatchString(line) {
			continue
		}
		nums = append(nums, n)
	}
	return nums
}

// DeferredSliceNums extracts unchecked BLOCKED slices gated to a LATER phase.
func DeferredSliceNums(md string) []int {
	nums := []int{}
	for _, line := range strings.Split(md, "\n") {
		n := uncheckedSliceNum(line)
		if n < 0 || !blockedMarkerRe.MatchString(line) || !laterMarkerRe.MatchString(line) {
			continue
		}
		nums = append(nums, n)
	}
	return nums
}

// CheckboxProgress is the {done,total,status} of a markdown file's checkboxes.
type CheckboxProgress struct {
	Done   int
	Total  int
	Status WorkStatus
}

// ClassifyCheckboxes classifies by checkbox progress: none ticked → not-started, some → in-progress,
// all → done. Mirrors workitems.ts classifyCheckboxes.
func ClassifyCheckboxes(md string) CheckboxProgress {
	total := len(anyBoxRe.FindAllString(md, -1))
	done := len(checkedRe.FindAllString(md, -1))
	status := WorkNotStarted
	if total > 0 && done >= total {
		status = WorkDone
	} else if done > 0 {
		status = WorkInProgress
	}
	return CheckboxProgress{Done: done, Total: total, Status: status}
}

// PrdBuildStatus derives a PRD's build status from its /build progress scratchpad (nil → not started).
func PrdBuildStatus(progress *string) WorkStatus {
	if progress == nil {
		return WorkNotStarted
	}
	if buildCompleteRe.MatchString(*progress) {
		return WorkDone
	}
	return WorkInProgress
}

// ---- file classification (pure string logic; no fs) ---------------------------------------------

var (
	classifyTokenSplit = regexp.MustCompile(`[-_.\s]+`)
	planTokenRe        = regexp.MustCompile(`^r?plans?$`)
	prdSlugRe          = regexp.MustCompile(`(?i)^prd-(.+)\.md$`)
	mdSuffixRe         = regexp.MustCompile(`(?i)\.md$`)
)

// Kind is 'prd' | 'plan' | "" (unclassified).
type Kind string

const buildStatePrefix = ".build-" // build-progress/state filename prefix

const (
	KindNone Kind = ""
	KindPRD  Kind = "prd"
	KindPlan Kind = "plan"
)

// Classify identifies a .md file as a PRD or plan, project-agnostic. Inclusion is by filename token;
// the directory signal only picks the bucket (a plan-named file under a prd/prds dir is a PRD).
// Mirrors workitems.ts classify. Pure — takes cwd + the absolute path, no fs.
func Classify(cwd, full string) Kind {
	name := strings.ToLower(filepath.Base(full))
	if strings.HasSuffix(name, "-review.md") {
		return KindNone
	}
	if strings.HasSuffix(name, "-progress.md") || strings.HasPrefix(name, buildStatePrefix) {
		return KindNone
	}
	if strings.HasPrefix(name, "prd-planproto-") {
		return KindNone
	}
	stem := mdSuffixRe.ReplaceAllString(name, "")
	tokens := classifyTokenSplit.Split(stem, -1)
	isPrd, isPlan := classifyTokens(tokens)
	if !isPrd && !isPlan {
		return KindNone
	}
	if isPrd {
		return KindPRD
	}
	for _, d := range dirTokens(cwd, full) {
		if d == "prd" || d == "prds" {
			return KindPRD
		}
	}
	return KindPlan
}

// classifyTokens scans a filename's stem tokens for PRD/plan signals — a `prd`/`prds` token marks a
// PRD, a token matching planTokenRe (`plan`/`plans`/`rplan`/`rplans`) marks a plan. Extracted from
// Classify verbatim.
func classifyTokens(tokens []string) (isPrd, isPlan bool) {
	for _, t := range tokens {
		if t == "prd" || t == "prds" {
			isPrd = true
		}
		if planTokenRe.MatchString(t) {
			isPlan = true
		}
	}
	return isPrd, isPlan
}

// dirTokens returns the lowercased directory segments of full BELOW cwd (excluding the filename) —
// mirrors the TS `full.slice(cwd.length)...split(sep).slice(0,-1)`.
func dirTokens(cwd, full string) []string {
	rel := strings.TrimPrefix(full, cwd)
	rel = strings.TrimLeft(rel, `/\`)
	parts := strings.FieldsFunc(rel, func(r rune) bool { return r == '/' || r == '\\' })
	if len(parts) == 0 {
		return nil
	}
	parts = parts[:len(parts)-1] // drop the filename
	for i := range parts {
		parts[i] = strings.ToLower(parts[i])
	}
	return parts
}

// LabelFor is the display label: a generic filename (plan.md/prd.md) borrows its parent dir name;
// otherwise the filename itself. Mirrors workitems.ts labelFor.
func LabelFor(full string) string {
	file := filepath.Base(full)
	stem := strings.ToLower(mdSuffixRe.ReplaceAllString(file, ""))
	if stem == "plan" || stem == "prd" {
		if d := filepath.Base(filepath.Dir(full)); d != "" && d != "." && d != string(filepath.Separator) {
			return d
		}
		return file
	}
	return file
}

// dirStyleStem is true for a directory-style work item (<feature>/prd.md or <feature>/plan.md).
func dirStyleStem(full string) bool {
	stem := strings.ToLower(mdSuffixRe.ReplaceAllString(filepath.Base(full), ""))
	return stem == "prd" || stem == "plan"
}

// PrdSlug identifies a PRD's /build artifacts: flat prd-<slug>.md → <slug>; <feature>/prd.md → <feature>;
// else "". Mirrors workitems.ts prdSlug.
func PrdSlug(full string) string {
	if m := prdSlugRe.FindStringSubmatch(filepath.Base(full)); m != nil {
		return m[1]
	}
	if dirStyleStem(full) {
		if d := filepath.Base(filepath.Dir(full)); d != "" && d != "." && d != string(filepath.Separator) {
			return d
		}
	}
	return ""
}

// BuildArtifactCandidates returns the candidate paths for a PRD's /build artifact (kind "progress" or
// "state"), most-specific first. Pure — the existence check lives in usecase. Mirrors the candidate
// list in workitems.ts buildArtifactPath.
func BuildArtifactCandidates(cwd, full, kind string) []string {
	base := "progress.md"
	if kind == "state" {
		base = "state.json"
	}
	dir := filepath.Dir(full)
	slug := PrdSlug(full)
	var candidates []string
	if dirStyleStem(full) {
		candidates = append(candidates, filepath.Join(dir, buildStatePrefix+base))
	}
	if slug != "" {
		candidates = append(candidates, filepath.Join(dir, buildStatePrefix+slug+"-"+base))
		candidates = append(candidates, filepath.Join(cwd, "tasks", buildStatePrefix+slug+"-"+base))
	}
	return candidates
}

// LocaleLess approximates JS String.localeCompare for the lowercase-kebab filename domain: primary
// order is case-insensitive, ties fall back to code-point order (so 'Zeta' sorts after 'teacher',
// matching ICU, unlike a raw byte compare where 'Z' < 'a'). Mirrors the findWorkItems name sort.
func LocaleLess(a, b string) bool {
	la, lb := strings.ToLower(a), strings.ToLower(b)
	if la != lb {
		return la < lb
	}
	return a < b
}
