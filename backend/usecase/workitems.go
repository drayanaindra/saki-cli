package usecase

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

// WorkItemsService serves the board's GET /api/workitems read path: walk cwd, classify each .md as a
// PRD/plan, and build its work-queue entry (checkbox progress + /build resume targets). Faithful port
// of apps/server/src/workitems.ts findWorkItems + buildItem. R1: read-only (fs port exposes no writer).
type WorkItemsService struct {
	fs  WorkItemsFS
	git CommitVerifier
}

// NewWorkItemsService wires the read-only fs + commit verifier into the service.
func NewWorkItemsService(fs WorkItemsFS, git CommitVerifier) WorkItemsService {
	return WorkItemsService{fs: fs, git: git}
}

// FindWorkItems walks cwd and returns every PRD/plan, split into the two lanes, each sorted by
// locale-aware name order (parity with findWorkItems' localeCompare). Absolute item paths.
// The done-filter is applied by the adapter (parity: index.ts filters at the route), not here.
func (s WorkItemsService) FindWorkItems(cwd string) (prds, plans []domain.WorkItem) {
	prds, plans = []domain.WorkItem{}, []domain.WorkItem{}
	for _, full := range s.fs.WalkMarkdown(cwd) {
		switch domain.Classify(cwd, full) {
		case domain.KindPRD:
			prds = append(prds, s.buildItem(cwd, full, domain.KindPRD))
		case domain.KindPlan:
			plans = append(plans, s.buildItem(cwd, full, domain.KindPlan))
		}
	}
	sortByName(prds)
	sortByName(plans)
	return prds, plans
}

func sortByName(items []domain.WorkItem) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && domain.LocaleLess(items[j].Name, items[j-1].Name); j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}

// buildItem mirrors workitems.ts buildItem: a PRD prefers its /build scratchpad (progress → status +
// steps), then a manifest-only resume target; a plan (and a scratchpad-less PRD) classifies by its own
// checkboxes.
func (s WorkItemsService) buildItem(cwd, full string, kind domain.Kind) domain.WorkItem {
	name := domain.LabelFor(full)
	if kind == domain.KindPRD {
		resumeTarget := s.manifestResumeTarget(cwd, full)
		progress, hasProgress := s.readArtifact(cwd, full, "progress")
		if hasProgress {
			c := domain.ClassifyCheckboxes(progress)
			status := domain.PrdBuildStatus(&progress)
			completed := domain.CompletedSliceNums(progress)
			var activeStep *domain.ResumeTarget
			if status == domain.WorkInProgress {
				activeStep = s.activeStepFromDisk(cwd, full, completed)
			}
			return domain.WorkItem{
				Name: name, Path: full, Status: status, Done: c.Done, Total: c.Total,
				CompletedSliceNums: completed,
				BlockedSliceNums:   domain.BlockedSliceNums(progress),
				DeferredSliceNums:  domain.DeferredSliceNums(progress),
				Resumable:          true, ResumeTarget: resumeTarget, ActiveStep: activeStep,
			}
		}
		if resumeTarget != nil {
			md, _ := s.fs.Read(full)
			c := domain.ClassifyCheckboxes(md)
			completed := domain.CompletedSliceNums(md)
			var activeStep *domain.ResumeTarget
			if c.Status == domain.WorkInProgress {
				activeStep = s.activeStepFromDisk(cwd, full, completed)
			}
			return domain.WorkItem{
				Name: name, Path: full, Status: c.Status, Done: c.Done, Total: c.Total,
				CompletedSliceNums: completed,
				BlockedSliceNums:   domain.BlockedSliceNums(md),
				DeferredSliceNums:  domain.DeferredSliceNums(md),
				Resumable:          true, ResumeTarget: resumeTarget, ActiveStep: activeStep,
			}
		}
	}
	md, _ := s.fs.Read(full)
	c := domain.ClassifyCheckboxes(md)
	return domain.WorkItem{
		Name: name, Path: full, Status: c.Status, Done: c.Done, Total: c.Total,
		CompletedSliceNums: domain.CompletedSliceNums(md),
		BlockedSliceNums:   domain.BlockedSliceNums(md),
		DeferredSliceNums:  domain.DeferredSliceNums(md),
	}
}

// ProgressFingerprint is the coarse "how far has this build gotten" fingerprint for the auto-resume
// no-progress bail (F6 Slice 2): the count of verified-complete slices (from the progress scratchpad)
// plus the next verified resume target (slice.step). Two equal fingerprints across consecutive resumes
// ⇒ no verifiable progress. Reuses the SAME F4 helpers the board's resume path uses (readArtifact +
// manifestResumeTarget). Faithful port of workitems.ts buildProgressFingerprint (:332-342), with one
// deliberate mapping: an absent/unreadable progress scratchpad returns NIL (neutral) rather than the
// TS "0:none" — so the engine treats an unreadable build (AC 2.3: absent/locked/malformed .build-
// progress.md) as UNKNOWN and falls back to the turn/wall caps, never falsely incrementing the streak.
// Never panics — every helper returns zero-values on error, so a deleted cwd / bad git yields a stable
// result, not a crash.
func (s WorkItemsService) ProgressFingerprint(cwd, prdPath string) *string {
	progress, ok := s.readArtifact(cwd, prdPath, "progress")
	if !ok {
		return nil // no readable scratchpad → unknown → neutral (AC 2.3)
	}
	completed := len(domain.CompletedSliceNums(progress))
	rtStr := "none"
	if rt := s.manifestResumeTarget(cwd, prdPath); rt != nil {
		rtStr = itoa(rt.Slice) + "." + rt.Step
	}
	fp := itoa(completed) + ":" + rtStr
	return &fp
}

// readArtifact returns the first existing /build artifact (kind "progress"|"state") content.
func (s WorkItemsService) readArtifact(cwd, full, kind string) (string, bool) {
	for _, p := range domain.BuildArtifactCandidates(cwd, full, kind) {
		if s.fs.Exists(p) {
			if content, ok := s.fs.Read(p); ok {
				return content, true
			}
		}
	}
	return "", false
}

// manifestResumeTarget is the step-level resume point from a PRD's state manifest, or nil.
func (s WorkItemsService) manifestResumeTarget(cwd, full string) *domain.ResumeTarget {
	raw, ok := s.readArtifact(cwd, full, "state")
	if !ok {
		return nil
	}
	m := domain.ParseManifest(raw)
	if m == nil {
		return nil
	}
	return domain.ComputeResumeTarget(m, s.realVerifiers(cwd))
}

// activeStepFromDisk wires ActiveStepFor to the state manifest, or nil when not applicable.
func (s WorkItemsService) activeStepFromDisk(cwd, full string, completed []int) *domain.ResumeTarget {
	raw, ok := s.readArtifact(cwd, full, "state")
	var m *domain.BuildManifest
	if ok {
		m = domain.ParseManifest(raw)
	}
	return domain.ActiveStepFor(m, completed, s.realVerifiers(cwd), func(n int) bool {
		return s.planArtifactExists(cwd, full, n)
	})
}

var shaRe = regexp.MustCompile(`(?i)^[0-9a-f]{4,40}$`)

// realVerifiers wires the pure resume logic to the fs + git ports.
func (s WorkItemsService) realVerifiers(cwd string) domain.ResumeVerifiers {
	return domain.ResumeVerifiers{
		FileExists: func(p string) bool {
			if !filepath.IsAbs(p) {
				p = filepath.Join(cwd, p)
			}
			return s.fs.Exists(p)
		},
		CommitExists: func(sha string) bool {
			if !shaRe.MatchString(sha) {
				return false
			}
			return s.git.CommitExists(cwd, sha)
		},
	}
}

// planArtifactExists is durable proof slice n's /rplan ran: a plan file exists in the PRD's dir or
// legacy tasks/. Mirrors workitems.ts planArtifactExists.
func (s WorkItemsService) planArtifactExists(cwd, prdPath string, n int) bool {
	dir := filepath.Dir(prdPath)
	if s.fs.Exists(filepath.Join(dir, "plan-slice"+itoa(n)+".md")) {
		return true
	}
	re := regexp.MustCompile(`(?i)(slice0*` + itoa(n) + `\b.*plan|plan.*slice0*` + itoa(n) + `\b)`)
	for _, d := range []string{dir, filepath.Join(cwd, "tasks")} {
		for _, f := range s.fs.ListDir(d) {
			if strings.HasSuffix(strings.ToLower(f), ".md") && re.MatchString(f) {
				return true
			}
		}
	}
	return false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
