package usecase

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

type workflowTestJournal struct {
	mu      sync.Mutex
	records map[string]domain.Workflow
}

func newWorkflowTestJournal() *workflowTestJournal {
	return &workflowTestJournal{records: map[string]domain.Workflow{}}
}

func (j *workflowTestJournal) EnsureWritable() error { return nil }
func (j *workflowTestJournal) Write(w domain.Workflow) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.records[w.ID] = w
	return nil
}
func (j *workflowTestJournal) Load() ([]domain.Workflow, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	result := make([]domain.Workflow, 0, len(j.records))
	for _, w := range j.records {
		result = append(result, w)
	}
	return result, nil
}

type workflowTestSpawner struct {
	store *memStore
	calls []WorkflowChildRequest
	next  int
}

func (s *workflowTestSpawner) SpawnWorkflowChild(req WorkflowChildRequest) (domain.Run, error) {
	s.next++
	run := domain.Run{ID: fmt.Sprintf("child-%d", s.next), Status: domain.StatusRunning, Prompt: req.Prompt, Cwd: req.Cwd, Meta: req.Meta, Engine: req.Engine}
	s.calls = append(s.calls, req)
	s.store.Put(run)
	return run, nil
}

type workflowTestClock struct{ now int64 }

func (c workflowTestClock) Now() int64 { return c.now }

type workflowTestIDs struct{ next int }

func (g *workflowTestIDs) New() string {
	g.next++
	return fmt.Sprintf("workflow-%d", g.next)
}

type workflowTestOutput struct {
	lines map[string][]ParsedLine
}

func (o workflowTestOutput) ReadAll(id string) ([]ParsedLine, error) { return o.lines[id], nil }
func (workflowTestOutput) Size(string) int64                         { return 0 }

type workflowTestLocker struct{}

func (workflowTestLocker) Lock(string, string, string) (int, any) {
	return 200, map[string]any{"ok": true}
}

type workflowTestGit struct{}

func (workflowTestGit) CommitExists(string, string) bool { return true }

func workflowTestFS(files map[string]string) wiFS { return wiFS{files: files} }

func newWorkflowTestService(fs wiFS, output FullOutput) (*WorkflowService, *workflowTestSpawner, *memStore) {
	store := newMemStore()
	spawner := &workflowTestSpawner{store: store}
	service := NewWorkflowService(
		newWorkflowTestJournal(), store, spawner, nil, fs, workflowTestGit{}, workflowTestLocker{}, output,
		workflowTestClock{now: 1000}, &workflowTestIDs{},
	)
	return service, spawner, store
}

func workflowRoadmap(id, track, child string) string {
	field := "**Child PRD:**"
	if track == "Plan" {
		field = "**Child plan:**"
	}
	return fmt.Sprintf("# Roadmap: Test\n\n### %s · item\n**Status:** In-progress\n**Goal:** test\n%s %s\n", id, field, child)
}

func workflowResultLine(text string) ParsedLine {
	b, _ := json.Marshal(map[string]any{"type": "result", "result": text})
	return ParsedLine{Kind: "json", Value: b}
}

func TestWorkflow_PRDTrackStartsPickupWhenPRDIsAbsent(t *testing.T) {
	fs := workflowTestFS(map[string]string{
		"/repo/tasks/roadmap.md": workflowRoadmap("F7", "PRD", "—"),
	})
	service, spawner, _ := newWorkflowTestService(fs, workflowTestOutput{lines: map[string][]ParsedLine{}})
	started, err := service.Start(WorkflowStartRequest{Cwd: "/repo", Target: "f7"})
	if err != nil {
		t.Fatal(err)
	}
	if started.Workflow.Track != "PRD" || started.Workflow.Phase != domain.PhasePickup || started.Workflow.ChildRunID != "child-1" {
		t.Fatalf("unexpected pickup workflow: %+v", started.Workflow)
	}
	if len(spawner.calls) != 1 || spawner.calls[0].Prompt != "/saki-builder:pickup f7" {
		t.Fatalf("pickup child was not started correctly: %+v", spawner.calls)
	}
}

func TestWorkflow_DoneRequiresManifestAndLockedPRD(t *testing.T) {
	files := map[string]string{
		"/repo/tasks/roadmap.md":            workflowRoadmap("F7", "PRD", "prd-f7.md"),
		"/repo/tasks/prd-f7.md":             "# PRD\n\n<!-- prd-locked: @test · 2026-09-02 · ui:tasks/proto-f7/ -->\n",
		"/repo/tasks/proto-f7/preview.html": "<html></html>",
		"/repo/tasks/.build-f7-state.json":  `{"prd":"tasks/prd-f7.md","commitPolicy":"per-step","slices":[{"n":1,"status":"done","steps":{"rplan":{"status":"done"},"approved":{"status":"done"},"qa":{"status":"done"},"reviewer":{"status":"done"}}}]}`,
	}
	service, _, store := newWorkflowTestService(workflowTestFS(files), workflowTestOutput{lines: map[string][]ParsedLine{"child-1": {workflowResultLine("PRD_BUILD_COMPLETE")}}})
	started, err := service.Start(WorkflowStartRequest{Cwd: "/repo", Target: "F7"})
	if err != nil {
		t.Fatal(err)
	}
	store.Finalize(started.Workflow.ChildRunID, 0)
	service.Sweep()
	got, ok := service.Get(started.Workflow.ID)
	if !ok || got.Status != domain.WorkflowDone || got.CompletionEvidence == nil {
		t.Fatalf("workflow did not reach verified done: ok=%v workflow=%+v", ok, got)
	}
	if got.CompletionEvidence.VerifiedAt != 1000 || len(got.CompletionEvidence.CheckedPaths) != 2 {
		t.Fatalf("unexpected completion evidence: %+v", got.CompletionEvidence)
	}
}

func TestWorkflow_VerifiesSuccessfulBuildWithoutSentinel(t *testing.T) {
	files := map[string]string{
		"/repo/tasks/roadmap.md":            workflowRoadmap("F7", "PRD", "prd-f7.md"),
		"/repo/tasks/prd-f7.md":             "# PRD\n\n<!-- prd-locked: @test · 2026-09-02 · ui:tasks/proto-f7/ -->\n",
		"/repo/tasks/proto-f7/preview.html": "<html></html>",
		"/repo/tasks/.build-f7-state.json":  `{"prd":"tasks/prd-f7.md","commitPolicy":"per-step","slices":[{"n":1,"status":"done","steps":{"rplan":{"status":"done"},"approved":{"status":"done"},"qa":{"status":"done"},"reviewer":{"status":"done"}}}]}`,
	}
	service, _, store := newWorkflowTestService(workflowTestFS(files), workflowTestOutput{lines: map[string][]ParsedLine{"child-1": {workflowResultLine("Implementation and checks completed.")}}})
	started, err := service.Start(WorkflowStartRequest{Cwd: "/repo", Target: "F7"})
	if err != nil {
		t.Fatal(err)
	}
	store.Finalize(started.Workflow.ChildRunID, 0)
	service.Sweep()
	got, _ := service.Get(started.Workflow.ID)
	if got.Status != domain.WorkflowDone || got.CompletionEvidence == nil {
		t.Fatalf("a successful child summary should proceed to artifact verification: %+v", got)
	}
}

func TestWorkflow_CompletePhasePersistsSuccessorAtomically(t *testing.T) {
	files := map[string]string{
		"/repo/tasks/roadmap.md": workflowRoadmap("F7", "PRD", "prd-f7.md"),
		"/repo/tasks/prd-f7.md":  "# PRD\n",
	}
	service, spawner, store := newWorkflowTestService(workflowTestFS(files), workflowTestOutput{lines: map[string][]ParsedLine{}})
	started, err := service.Start(WorkflowStartRequest{Cwd: "/repo", Target: "F7"})
	if err != nil {
		t.Fatal(err)
	}
	if started.Workflow.Phase != domain.PhaseProto {
		t.Fatalf("existing unlocked PRD should enter proto, got %s", started.Workflow.Phase)
	}
	child := started.Workflow.ChildRunID
	store.Finalize(child, 0)
	service.Sweep()
	got, _ := service.Get(started.Workflow.ID)
	if got.Phase != domain.PhaseBuild || got.ChildRunID != "child-2" {
		t.Fatalf("proto completion should durably advance through lock to build: %+v", got)
	}
	if len(spawner.calls) != 2 {
		t.Fatalf("lock is direct and build should be the only successor child: %d calls", len(spawner.calls))
	}
}

func TestWorkflow_ContinueDoesNotReuseFinalizedBuildChild(t *testing.T) {
	files := map[string]string{
		"/repo/tasks/roadmap.md":            workflowRoadmap("F7", "PRD", "prd-f7.md"),
		"/repo/tasks/prd-f7.md":             "# PRD\n<!-- prd-locked: @test · 2026-09-02 · ui:tasks/proto-f7/ -->\n",
		"/repo/tasks/proto-f7/preview.html": "<html></html>",
	}
	out := workflowTestOutput{lines: map[string][]ParsedLine{"child-1": {workflowResultLine(`NEEDS_DECISION: {"question":"pick one","options":["a","b"]}`)}}}
	service, spawner, store := newWorkflowTestService(workflowTestFS(files), out)
	started, err := service.Start(WorkflowStartRequest{Cwd: "/repo", Target: "F7", IdempotencyKey: "request-1"})
	if err != nil {
		t.Fatal(err)
	}
	store.Finalize(started.Workflow.ChildRunID, 0)
	service.Sweep()
	awaiting, _ := service.Get(started.Workflow.ID)
	if awaiting.Status != domain.WorkflowAwaitingDecision {
		t.Fatalf("expected awaiting decision, got %+v", awaiting)
	}
	continued, err := service.Continue(started.Workflow.ID, "a")
	if err != nil {
		t.Fatal(err)
	}
	if continued.Workflow.ChildRunID != "child-2" || len(spawner.calls) != 2 {
		t.Fatalf("continue must spawn a fresh child, got workflow=%+v calls=%d", continued.Workflow, len(spawner.calls))
	}
}

func TestWorkflow_StartReopensStoppedLane(t *testing.T) {
	files := map[string]string{
		"/repo/tasks/roadmap.md": workflowRoadmap("F7", "PRD", "prd-f7.md"),
		"/repo/tasks/prd-f7.md":  "# PRD\n",
	}
	service, spawner, store := newWorkflowTestService(workflowTestFS(files), workflowTestOutput{lines: map[string][]ParsedLine{}})
	first, err := service.Start(WorkflowStartRequest{Cwd: "/repo", Target: "F7"})
	if err != nil {
		t.Fatal(err)
	}
	if !service.Stop(first.Workflow.ID) {
		t.Fatal("initial workflow should be stoppable")
	}
	// The production stopper signals the child process group; model the resulting terminal child
	// here so the reopened attempt cannot accidentally adopt the stopped process during the race.
	store.Finalize(first.Workflow.ChildRunID, 1)
	second, err := service.Start(WorkflowStartRequest{Cwd: "/repo", Target: "F7"})
	if err != nil {
		t.Fatal(err)
	}
	if second.Workflow.ID == first.Workflow.ID || second.Deduped || second.Workflow.Status != domain.WorkflowRunning {
		t.Fatalf("stopped lane should start a fresh workflow attempt: first=%+v second=%+v", first.Workflow, second.Workflow)
	}
	if len(spawner.calls) != 2 {
		t.Fatalf("reopen should spawn exactly one fresh child, calls=%d", len(spawner.calls))
	}
}

func TestWorkflow_SentinelWithoutEvidenceFailsVerification(t *testing.T) {
	files := map[string]string{
		"/repo/tasks/roadmap.md":            workflowRoadmap("F7", "PRD", "prd-f7.md"),
		"/repo/tasks/prd-f7.md":             "# PRD\n<!-- prd-locked: @test · 2026-09-02 · ui:tasks/proto-f7/ -->\n",
		"/repo/tasks/proto-f7/preview.html": "<html></html>",
	}
	service, _, store := newWorkflowTestService(workflowTestFS(files), workflowTestOutput{lines: map[string][]ParsedLine{"child-1": {workflowResultLine("PRD_BUILD_COMPLETE")}}})
	started, err := service.Start(WorkflowStartRequest{Cwd: "/repo", Target: "F7"})
	if err != nil {
		t.Fatal(err)
	}
	store.Finalize(started.Workflow.ChildRunID, 0)
	service.Sweep()
	got, _ := service.Get(started.Workflow.ID)
	if got.Status != domain.WorkflowFailed || got.Reason == "" {
		t.Fatalf("missing build evidence must fail durably, got %+v", got)
	}
}

func TestWorkflow_AdoptsAlreadyFinalizedBuildSuccessor(t *testing.T) {
	files := map[string]string{
		"/repo/tasks/roadmap.md":            workflowRoadmap("F7", "PRD", "prd-f7.md"),
		"/repo/tasks/prd-f7.md":             "# PRD\n<!-- prd-locked: @test · 2026-09-02 · ui:tasks/proto-f7/ -->\n",
		"/repo/tasks/proto-f7/preview.html": "<html></html>",
		"/repo/tasks/.build-f7-state.json":  `{"prd":"tasks/prd-f7.md","slices":[{"n":1,"status":"done","steps":{"rplan":{"status":"done","artifact":"tasks/slice-1-plan.md"},"approved":{"status":"done","commit":"abc"},"qa":{"status":"done"},"reviewer":{"status":"done"}}}]}`,
		"/repo/tasks/slice-1-plan.md":       "# Plan\n",
	}
	out := workflowTestOutput{lines: map[string][]ParsedLine{
		"child-2": {workflowResultLine("PRD_BUILD_COMPLETE")},
	}}
	service, _, store := newWorkflowTestService(workflowTestFS(files), out)
	started, err := service.Start(WorkflowStartRequest{Cwd: "/repo", Target: "F7"})
	if err != nil {
		t.Fatal(err)
	}
	predecessor, _ := store.Get(started.Workflow.ChildRunID)
	store.Finalize(predecessor.ID, 0)
	successor := predecessor
	successor.ID = "child-2"
	successor.Status = domain.StatusDone
	successor.ResumeCount = predecessor.ResumeCount + 1
	store.Put(successor)
	service.Sweep()
	got, _ := service.Get(started.Workflow.ID)
	if got.Status != domain.WorkflowDone {
		t.Fatalf("workflow should adopt a fast finalized successor, got %+v", got)
	}
}

func TestWorkflow_PlanTrackRunsOnlyPlanPhases(t *testing.T) {
	files := map[string]string{
		"/repo/tasks/roadmap.md": workflowRoadmap("I1", "Plan", "i1-plan.md"),
		"/repo/tasks/i1-plan.md": "# Plan\n- [x] 1. Implement\n",
	}
	service, spawner, store := newWorkflowTestService(workflowTestFS(files), workflowTestOutput{lines: map[string][]ParsedLine{}})
	started, err := service.Start(WorkflowStartRequest{Cwd: "/repo", Target: "I1"})
	if err != nil {
		t.Fatal(err)
	}
	if started.Workflow.Phase != domain.PhaseRPlan || len(spawner.calls) != 1 {
		t.Fatalf("plan workflow should start at rplan: %+v", started.Workflow)
	}
	for _, phase := range []domain.WorkflowPhase{domain.PhaseRPlan, domain.PhaseRPlanReview, domain.PhaseApproved, domain.PhaseQA, domain.PhaseReviewer, domain.PhaseWrap} {
		current, _ := service.Get(started.Workflow.ID)
		if current.Phase != phase {
			t.Fatalf("want phase %s, got %s", phase, current.Phase)
		}
		store.Finalize(current.ChildRunID, 0)
		service.Sweep()
	}
	got, _ := service.Get(started.Workflow.ID)
	if got.Status != domain.WorkflowDone || got.Phase != domain.PhaseVerify {
		t.Fatalf("plan workflow did not verify: %+v", got)
	}
	for _, call := range spawner.calls {
		if call.Meta != nil && (call.Meta.Kind == domain.RunKind("pickup") || call.Meta.Kind == domain.RunKind("proto") || call.Meta.Kind == domain.RunKind("build")) {
			t.Fatalf("plan workflow invoked PRD phase %s", call.Meta.Kind)
		}
	}
}

func TestWorkflow_RehydrateDuplicateLaneFailsClosed(t *testing.T) {
	j := newWorkflowTestJournal()
	for _, id := range []string{"workflow-a", "workflow-b"} {
		j.records[id] = domain.Workflow{
			ID: id, LaneKey: "/repo::F7", Cwd: "/repo", Target: "F7", Track: "PRD",
			Phase: domain.PhasePickup, Status: domain.WorkflowRunning,
			PhaseHistory: []domain.PhaseRecord{{Phase: domain.PhasePickup, Status: "started", At: 1}},
		}
	}
	service, spawner, _ := newWorkflowTestService(workflowTestFS(map[string]string{
		"/repo/tasks/roadmap.md": workflowRoadmap("F7", "PRD", "—"),
	}), workflowTestOutput{lines: map[string][]ParsedLine{}})
	// Replace the empty journal used by the helper with the deliberately colliding one.
	service.journal = j
	if err := service.Rehydrate(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"workflow-a", "workflow-b"} {
		got, ok := service.Get(id)
		if !ok || got.Status != domain.WorkflowFailed || got.ChildRunID != "" {
			t.Fatalf("duplicate lane must fail closed: %s %+v", id, got)
		}
	}
	if len(spawner.calls) != 0 {
		t.Fatalf("duplicate lane recovery must not spawn a child: %d", len(spawner.calls))
	}
}

func TestWorkflow_RehydrateIsIdempotent(t *testing.T) {
	j := newWorkflowTestJournal()
	j.records["workflow-a"] = domain.Workflow{
		ID: "workflow-a", LaneKey: "/repo::F7", Cwd: "/repo", Target: "F7", Track: "PRD",
		Phase: domain.PhasePickup, Status: domain.WorkflowParked, ParkedReason: "needs operator action",
		PhaseHistory: []domain.PhaseRecord{{Phase: domain.PhasePickup, Status: "started", At: 1}},
	}
	service, spawner, _ := newWorkflowTestService(workflowTestFS(map[string]string{
		"/repo/tasks/roadmap.md": workflowRoadmap("F7", "PRD", "—"),
	}), workflowTestOutput{lines: map[string][]ParsedLine{}})
	service.journal = j
	if err := service.Rehydrate(); err != nil {
		t.Fatal(err)
	}
	if err := service.Rehydrate(); err != nil {
		t.Fatal(err)
	}
	got, ok := service.Get("workflow-a")
	if !ok || got.Status != domain.WorkflowParked || len(service.WorkflowIDs()) != 1 {
		t.Fatalf("second rehydrate must preserve the valid workflow: ok=%v workflow=%+v ids=%v", ok, got, service.WorkflowIDs())
	}
	if len(spawner.calls) != 0 {
		t.Fatalf("rehydrating a parked workflow must not spawn: %d", len(spawner.calls))
	}
}

func TestWorkflow_RehydratePreservesVerifiedSuccessOverHistoricalFailure(t *testing.T) {
	j := newWorkflowTestJournal()
	verified := domain.Workflow{
		ID: "workflow-done", LaneKey: "/repo::F7", Cwd: "/repo", Target: "F7", Track: "PRD",
		Phase: domain.PhaseVerify, Status: domain.WorkflowDone,
		PhaseHistory: []domain.PhaseRecord{
			{Phase: domain.PhaseBuild, Status: "done", ChildRunID: "child-1", At: 1},
			{Phase: domain.PhaseVerify, Status: "done", At: 2},
		},
		CompletionEvidence: &domain.WorkflowEvidence{CheckedPaths: []string{"/repo/tasks/prd-f7.md"}, VerifiedAt: 2},
	}
	failed := verified
	failed.ID, failed.Status, failed.CompletionEvidence = "workflow-failed", domain.WorkflowFailed, nil
	failed.PhaseHistory = []domain.PhaseRecord{{Phase: domain.PhaseBuild, Status: "started", At: 1}}
	j.records[verified.ID] = verified
	j.records[failed.ID] = failed
	fs := workflowTestFS(map[string]string{
		"/repo/tasks/roadmap.md":            workflowRoadmap("F7", "PRD", "prd-f7.md"),
		"/repo/tasks/prd-f7.md":             "# PRD\n<!-- prd-locked: @test · 2026-09-02 · ui:tasks/proto-f7/ -->\n",
		"/repo/tasks/proto-f7/preview.html": "<html></html>",
		"/repo/tasks/.build-f7-state.json":  `{"prd":"tasks/prd-f7.md","commitPolicy":"per-step","slices":[{"n":1,"status":"done","steps":{"rplan":{"status":"done"},"approved":{"status":"done"},"qa":{"status":"done"},"reviewer":{"status":"done"}}}]}`,
	})
	service, _, _ := newWorkflowTestService(fs, workflowTestOutput{lines: map[string][]ParsedLine{}})
	service.journal = j
	if err := service.Rehydrate(); err != nil {
		t.Fatal(err)
	}
	got, ok := service.Get(verified.ID)
	if !ok || got.Status != domain.WorkflowDone {
		t.Fatalf("verified workflow must survive historical duplicate: ok=%v workflow=%+v", ok, got)
	}
	started, err := service.Start(WorkflowStartRequest{Cwd: "/repo", Target: "F7"})
	if err != nil || !started.Deduped || started.Workflow.ID != verified.ID {
		t.Fatalf("lane should re-adopt verified workflow, got result=%+v err=%v", started, err)
	}
}

func TestWorkflow_RehydrateRejectsPhaseFromAnotherTrack(t *testing.T) {
	j := newWorkflowTestJournal()
	j.records["workflow-plan"] = domain.Workflow{
		ID: "workflow-plan", LaneKey: "/repo::I1", Cwd: "/repo", Target: "I1", Track: "Plan",
		Phase: domain.PhasePickup, Status: domain.WorkflowRunning,
		PhaseHistory: []domain.PhaseRecord{{Phase: domain.PhasePickup, Status: "started", At: 1}},
	}
	service, spawner, _ := newWorkflowTestService(workflowTestFS(map[string]string{
		"/repo/tasks/roadmap.md": workflowRoadmap("I1", "Plan", "i1-plan.md"),
	}), workflowTestOutput{lines: map[string][]ParsedLine{}})
	service.journal = j
	if err := service.Rehydrate(); err != nil {
		t.Fatal(err)
	}
	if _, ok := service.Get("workflow-plan"); ok {
		t.Fatal("malformed cross-track workflow must not be rehydrated")
	}
	if len(spawner.calls) != 0 {
		t.Fatalf("malformed workflow must not spawn a child: %d", len(spawner.calls))
	}
}
