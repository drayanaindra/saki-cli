package usecase

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/drayanaindra/saki-cli/backend/domain"
)

var (
	ErrWorkflowNotFound       = errors.New("workflow not found")
	ErrWorkflowTarget         = errors.New("workflow target could not be resolved")
	ErrWorkflowTargetNotFound = errors.New("workflow target was not found")
	ErrWorkflowTerminal       = errors.New("workflow is terminal and cannot be continued")
	ErrWorkflowOption         = errors.New("invalid workflow decision option")
)

type WorkflowJournal interface {
	EnsureWritable() error
	Write(domain.Workflow) error
	Load() ([]domain.Workflow, error)
}

type WorkflowChildRequest struct {
	Prompt         string
	Cwd            *string
	ConfigDir      *string
	Meta           *domain.Meta
	Engine         domain.RunEngine
	Build          bool
	IdempotencyKey *string
}

type WorkflowChildSpawner interface {
	SpawnWorkflowChild(WorkflowChildRequest) (domain.Run, error)
}

type WorkflowChildStopper interface {
	StopWorkflowChild(id string) bool
}

// WorkflowChildAdapter keeps the coordinator independent of the concrete spawn engines while
// routing build phases through BuildEngineService (and all other phases through RunService).
type WorkflowChildAdapter struct {
	Runs   *RunService
	Builds *BuildEngineService
	Stops  *StopService
}

func (a WorkflowChildAdapter) SpawnWorkflowChild(req WorkflowChildRequest) (domain.Run, error) {
	if req.Build {
		return a.Builds.SpawnBuild(BuildSpawnReq{Prompt: req.Prompt, Cwd: req.Cwd, ConfigDir: req.ConfigDir, Meta: req.Meta, IdempotencyKey: req.IdempotencyKey, Engine: req.Engine})
	}
	return a.Runs.Spawn(SpawnReq{Prompt: req.Prompt, Cwd: req.Cwd, ConfigDir: req.ConfigDir, Meta: req.Meta, Engine: req.Engine})
}

func (a WorkflowChildAdapter) StopWorkflowChild(id string) bool {
	if run, ok := a.Runs.Owned(id); ok && run.Meta != nil && run.Meta.Kind.IsBuild() {
		return a.Builds.Stop(id)
	}
	if a.Stops == nil {
		return false
	}
	return a.Stops.Stop(id)
}

type WorkflowStartRequest struct {
	Cwd            string
	Target         string
	ConfigDir      *string
	Engine         domain.RunEngine
	IdempotencyKey string
}

type WorkflowStartResult struct {
	Workflow domain.Workflow `json:"workflow"`
	Deduped  bool            `json:"deduped"`
}

type WorkflowService struct {
	journal WorkflowJournal
	store   RunStore
	spawn   WorkflowChildSpawner
	stop    WorkflowChildStopper
	fs      WorkItemsFS
	git     CommitVerifier
	lock    WorkflowLocker
	output  FullOutput
	clock   Clock
	idgen   IDGen

	mu        sync.Mutex
	workflows map[string]*domain.Workflow
	byLane    map[string]string
	byIdem    map[string]string
}

type WorkflowLocker interface {
	Lock(cwd, path, date string) (int, any)
}

func NewWorkflowService(journal WorkflowJournal, store RunStore, spawn WorkflowChildSpawner, stop WorkflowChildStopper, fs WorkItemsFS, git CommitVerifier, lock WorkflowLocker, output FullOutput, clock Clock, idgen IDGen) *WorkflowService {
	return &WorkflowService{
		journal: journal, store: store, spawn: spawn, stop: stop, fs: fs, git: git, lock: lock,
		output: output, clock: clock, idgen: idgen, workflows: map[string]*domain.Workflow{},
		byLane: map[string]string{}, byIdem: map[string]string{},
	}
}

// Rehydrate loads only valid workflow records. A parked/awaiting workflow is deliberately not
// advanced on boot; a running workflow is reconciled against the child-run store before spawning.
func (s *WorkflowService) Rehydrate() error {
	recs, err := s.journal.Load()
	if err != nil {
		return err
	}
	s.mu.Lock()
	// Rehydrate is a boot operation, but keeping it idempotent makes recovery safe for embedders
	// and tests that invoke it again after replacing/reloading the journal. Without clearing these
	// indexes, the second pass would mistake each record for a duplicate lane of its own prior copy
	// and fail closed a healthy workflow.
	s.workflows = map[string]*domain.Workflow{}
	s.byLane = map[string]string{}
	s.byIdem = map[string]string{}
	for i := range recs {
		w := recs[i]
		if !validWorkflow(w) {
			continue // fail closed: malformed state can never authorize a child spawn
		}
		// A failed/stopped record is historical and may be followed by a fresh attempt. Keep it
		// journal-visible, but let the non-historical attempt own the lane. A completed workflow is
		// also authoritative: a later failed/stopped record must not invalidate durable success just
		// because journal directory order is nondeterministic. Any collision involving two live,
		// resumable, or two successful identities is corruption: fail closed rather than selecting
		// whichever record happened to be read last.
		if previousID := s.byLane[w.LaneKey]; previousID != "" {
			if previous := s.workflows[previousID]; previous != nil {
				if historicalWorkflow(*previous) && !historicalWorkflow(w) {
					copy := w
					s.workflows[w.ID] = &copy
					s.byLane[w.LaneKey] = w.ID
					if w.IdempotencyKey != "" {
						s.byIdem[w.IdempotencyKey] = w.ID
					}
					continue
				}
				if !historicalWorkflow(*previous) && historicalWorkflow(w) {
					copy := w
					s.workflows[w.ID] = &copy
					if w.IdempotencyKey != "" {
						s.byIdem[w.IdempotencyKey] = w.ID
					}
					continue
				}
				if previous.Status == domain.WorkflowDone && historicalWorkflow(w) {
					// Preserve verified success as the lane owner; the failed/stopped record is
					// historical and remains visible through its own workflow id.
					copy := w
					s.workflows[w.ID] = &copy
					if w.IdempotencyKey != "" {
						s.byIdem[w.IdempotencyKey] = w.ID
					}
					continue
				}
				if previous.Status == domain.WorkflowDone && w.Status != domain.WorkflowDone {
					previous.Status = domain.WorkflowFailed
					previous.Reason = "duplicate workflow lane in journal; refusing recovery"
					previous.ChildRunID = ""
					previous.UpdatedAt = s.clock.Now()
					_ = s.persist(previous)
				}
				previous.Status = domain.WorkflowFailed
				previous.Reason = "duplicate workflow lane in journal; refusing recovery"
				previous.ChildRunID = ""
				previous.UpdatedAt = s.clock.Now()
				_ = s.persist(previous)
			}
			w.Status = domain.WorkflowFailed
			w.Reason = "duplicate workflow lane in journal; refusing recovery"
			w.ChildRunID = ""
			w.UpdatedAt = s.clock.Now()
			copy := w
			s.workflows[w.ID] = &copy
			_ = s.persist(&copy)
			continue
		}
		copy := w
		s.workflows[w.ID] = &copy
		s.byLane[w.LaneKey] = w.ID
		if w.IdempotencyKey != "" {
			s.byIdem[w.IdempotencyKey] = w.ID
		}
	}
	// A terminal record is not trusted merely because it says done. Re-check its repository facts on
	// boot; stale/mutated artifacts become a durable failed workflow instead of a false green.
	for _, w := range s.workflows {
		if w.Status != domain.WorkflowDone {
			continue
		}
		if evidence, reason := s.verify(w); evidence == nil {
			w.Status, w.Reason = domain.WorkflowFailed, reason
			_ = s.persist(w)
		} else {
			w.CompletionEvidence = evidence
			_ = s.persist(w)
		}
	}
	s.mu.Unlock()
	// Rehydrate is the backend boot boundary: once it returns, a running workflow has been
	// reconciled with the child store and a due child can be adopted/spawned. Keep this here rather
	// than relying on the production timer so embedders and restart tests get the same recovery
	// semantics. Sweep is lock-safe and deliberately leaves parked/awaiting workflows untouched.
	s.Sweep()
	return nil
}

func validWorkflow(w domain.Workflow) bool {
	if w.ID == "" || strings.ContainsAny(w.ID, `/\\`) || w.LaneKey == "" || w.Cwd == "" || w.Target == "" || w.Status == "" || w.Track == "" || w.Phase == "" {
		return false
	}
	if w.Track != "PRD" && w.Track != "Plan" {
		return false
	}
	if !validWorkflowPhase(w.Phase) {
		return false
	}
	// The phase vocabulary is shared by both tracks, but the ordered tracks are not. A
	// mismatched phase is malformed state: accepting it after restart could invoke a PRD skill for
	// a Plan workflow (or vice versa), so fail closed before any child can be spawned.
	if w.Track == "PRD" && !containsWorkflowPhase(domain.PRDPhases(), w.Phase) {
		return false
	}
	if w.Track == "Plan" && !containsWorkflowPhase(domain.PlanPhases(), w.Phase) {
		return false
	}
	switch w.Status {
	case domain.WorkflowRunning, domain.WorkflowParked, domain.WorkflowAwaitingDecision, domain.WorkflowDone, domain.WorkflowFailed, domain.WorkflowStopped:
	default:
		return false
	}
	if w.Status == domain.WorkflowAwaitingDecision && w.AwaitingDecision == nil {
		return false
	}
	if w.Status != domain.WorkflowAwaitingDecision && w.AwaitingDecision != nil {
		return false
	}
	root, err := filepath.Abs(w.Cwd)
	if err != nil || !strings.HasPrefix(w.LaneKey, filepath.Clean(root)+"::") {
		return false
	}
	if w.Status == domain.WorkflowDone && w.CompletionEvidence == nil {
		return false
	}
	if w.Status == domain.WorkflowDone && (w.CompletionEvidence.VerifiedAt <= 0 || len(w.CompletionEvidence.CheckedPaths) == 0) {
		return false
	}
	return true
}

func reopenableWorkflow(w domain.Workflow) bool {
	return w.Status == domain.WorkflowFailed || w.Status == domain.WorkflowStopped
}

func historicalWorkflow(w domain.Workflow) bool {
	return w.Status == domain.WorkflowFailed || w.Status == domain.WorkflowStopped
}

func containsWorkflowPhase(phases []domain.WorkflowPhase, wanted domain.WorkflowPhase) bool {
	for _, phase := range phases {
		if phase == wanted {
			return true
		}
	}
	return false
}

func validWorkflowPhase(phase domain.WorkflowPhase) bool {
	for _, candidate := range append(domain.PRDPhases(), domain.PlanPhases()...) {
		if candidate == phase {
			return true
		}
	}
	return false
}

func (s *WorkflowService) Start(req WorkflowStartRequest) (WorkflowStartResult, error) {
	if strings.TrimSpace(req.Cwd) == "" || strings.TrimSpace(req.Target) == "" {
		return WorkflowStartResult{}, fmt.Errorf("%w: cwd and target are required", ErrWorkflowTarget)
	}
	info, err := s.resolve(req.Cwd, req.Target)
	if err != nil {
		return WorkflowStartResult{}, err
	}
	lane := filepath.Clean(info.cwd) + "::" + info.identity

	s.mu.Lock()
	defer s.mu.Unlock()
	if req.IdempotencyKey != "" {
		if id := s.byIdem[req.IdempotencyKey]; id != "" {
			if w := s.workflows[id]; w != nil {
				// Idempotency keys are request hints, not global workflow identities. A caller
				// may legitimately reuse a key in another repository; only the canonical lane
				// makes the request eligible for re-adoption.
				if w.LaneKey == lane && !reopenableWorkflow(*w) {
					s.refreshTerminalLocked(w)
					return WorkflowStartResult{Workflow: *w, Deduped: true}, nil
				}
			}
		}
	}
	if id := s.byLane[lane]; id != "" {
		if w := s.workflows[id]; w != nil {
			s.refreshTerminalLocked(w)
			if !reopenableWorkflow(*w) {
				return WorkflowStartResult{Workflow: *w, Deduped: true}, nil
			}
		}
	}

	now := s.clock.Now()
	id := s.idgen.New()
	w := &domain.Workflow{ID: id, LaneKey: lane, Cwd: info.cwd, Target: strings.TrimSpace(req.Target), Track: info.track, Phase: domain.PhaseResolve, Status: domain.WorkflowRunning, PhaseHistory: []domain.PhaseRecord{{Phase: domain.PhaseResolve, Status: "started", At: now}}, IdempotencyKey: req.IdempotencyKey, CreatedAt: now, UpdatedAt: now}
	w.Engine = domain.ResolveEngine(req.Engine)
	if req.ConfigDir != nil {
		w.ConfigDir = *req.ConfigDir
	}
	s.workflows[id], s.byLane[lane] = w, id
	if req.IdempotencyKey != "" {
		s.byIdem[req.IdempotencyKey] = id
	}
	if err := s.persist(w); err != nil {
		delete(s.workflows, id)
		delete(s.byLane, lane)
		return WorkflowStartResult{}, err
	}
	if err := s.advanceLocked(w, req.ConfigDir, req.Engine, ""); err != nil {
		return WorkflowStartResult{}, err
	}
	return WorkflowStartResult{Workflow: *w}, nil
}

func (s *WorkflowService) refreshTerminalLocked(w *domain.Workflow) {
	if w.Status != domain.WorkflowDone {
		return
	}
	evidence, reason := s.verify(w)
	if evidence == nil {
		w.Status, w.Reason = domain.WorkflowFailed, reason
		_ = s.persist(w)
		return
	}
	w.CompletionEvidence = evidence
	_ = s.persist(w)
}

type workflowTarget struct {
	cwd, identity, track, path string
}

var workflowIDRe = regexp.MustCompile(`(?i)^[EFIB][0-9]+$`)

func (s *WorkflowService) resolve(cwd, target string) (workflowTarget, error) {
	root, err := filepath.Abs(cwd)
	if err != nil {
		return workflowTarget{}, fmt.Errorf("%w: invalid cwd", ErrWorkflowTarget)
	}
	root = filepath.Clean(root)
	if workflowIDRe.MatchString(strings.TrimSpace(target)) {
		roadmap := filepath.Join(root, "tasks", "roadmap.md")
		content, ok := s.fs.Read(roadmap)
		if !ok {
			return workflowTarget{}, fmt.Errorf("%w: no tasks/roadmap.md found", ErrWorkflowTargetNotFound)
		}
		_, items := domain.ParseRoadmap(content)
		for _, item := range items {
			if !strings.EqualFold(item.ID, strings.TrimSpace(target)) {
				continue
			}
			path := ""
			if item.Track == "PRD" && item.ChildPrd != nil {
				path = containedChild(root, *item.ChildPrd, s.fs)
			}
			if item.Track == "Plan" && item.ChildPlan != nil {
				path = containedChild(root, *item.ChildPlan, s.fs)
			}
			return workflowTarget{cwd: root, identity: item.ID, track: item.Track, path: path}, nil
		}
		return workflowTarget{}, fmt.Errorf("%w: roadmap item %s not found", ErrWorkflowTargetNotFound, target)
	}
	if !strings.HasSuffix(strings.ToLower(strings.TrimSpace(target)), ".md") {
		return workflowTarget{}, fmt.Errorf("%w: target must be a roadmap id or .md path", ErrWorkflowTarget)
	}
	path := strings.TrimSpace(target)
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path = filepath.Clean(path)
	if !contained(root, path) {
		return workflowTarget{}, fmt.Errorf("%w: target path escapes cwd", ErrWorkflowTarget)
	}
	if !s.fs.Exists(path) {
		return workflowTarget{}, fmt.Errorf("%w: target path is missing", ErrWorkflowTargetNotFound)
	}
	kind := domain.Classify(root, path)
	track := "PRD"
	if kind == domain.KindPlan {
		track = "Plan"
	} else if kind != domain.KindPRD {
		return workflowTarget{}, fmt.Errorf("%w: target is not a PRD or plan", ErrWorkflowTarget)
	}
	// A path and its roadmap id must share one lane. Prefer the roadmap identity when the path is
	// attached to a work item; this makes `build F7` and `build tasks/prd-f7.md` re-adopt one workflow.
	roadmap := filepath.Join(root, "tasks", "roadmap.md")
	if content, ok := s.fs.Read(roadmap); ok {
		_, items := domain.ParseRoadmap(content)
		for _, item := range items {
			var child *string
			if item.Track == "PRD" {
				child = item.ChildPrd
			} else {
				child = item.ChildPlan
			}
			if child != nil && containedChild(root, *child, s.fs) == path {
				return workflowTarget{cwd: root, identity: item.ID, track: item.Track, path: path}, nil
			}
		}
	}
	return workflowTarget{cwd: root, identity: path, track: track, path: path}, nil
}

func containedChild(root, child string, fs WorkItemsFS) string {
	path := child
	if !filepath.IsAbs(path) {
		// Roadmap writers normally store a bare filename, while imported roadmaps sometimes store
		// the repo-relative `tasks/...` form. Accept both without producing `tasks/tasks/...`.
		if filepath.Base(filepath.Dir(path)) == "tasks" || strings.HasPrefix(filepath.ToSlash(path), "tasks/") {
			path = filepath.Join(root, path)
		} else {
			path = filepath.Join(root, "tasks", path)
		}
	}
	path = filepath.Clean(path)
	if contained(root, path) && fs.Exists(path) {
		return path
	}
	return ""
}

func contained(root, path string) bool {
	return path == root || strings.HasPrefix(path, root+string(filepath.Separator))
}

func (s *WorkflowService) advanceLocked(w *domain.Workflow, configDir *string, engine domain.RunEngine, continuation string) error {
	if configDir == nil && w.ConfigDir != "" {
		configDir = &w.ConfigDir
	}
	if engine == "" {
		engine = domain.ResolveEngine(w.Engine)
	}
	for w.Status == domain.WorkflowRunning {
		info, err := s.resolve(w.Cwd, w.Target)
		if err != nil {
			return s.failLocked(w, err.Error())
		}
		w.ResolvedPath = info.path
		if w.Phase == domain.PhaseResolve {
			next := s.firstPhase(info)
			if err := s.transitionLocked(w, next); err != nil {
				return err
			}
			continue
		}
		// A phase completion and the next-phase transition are persisted together. This recovery
		// guard also makes old records safe: if a process stopped after recording the completed phase
		// but before recording its successor, advance from the durable completion instead of spawning
		// the same child twice.
		if w.Phase != domain.PhaseVerify && phaseDone(w, w.Phase) {
			next := nextPhase(w.Track, w.Phase)
			if next == "" {
				return s.failLocked(w, "workflow phase has no successor")
			}
			w.Phase = next
			w.PhaseHistory = append(w.PhaseHistory, domain.PhaseRecord{Phase: next, Status: "started", At: s.clock.Now()})
			w.UpdatedAt = s.clock.Now()
			if err := s.persist(w); err != nil {
				return err
			}
			continue
		}
		if w.Phase == domain.PhaseLock {
			if s.lock == nil || info.path == "" {
				return s.failLocked(w, "cannot lock: PRD path is not present")
			}
			status, body := s.lock.Lock(w.Cwd, info.path, time.Now().UTC().Format(time.DateOnly))
			if status >= 300 || !bodyOK(body) {
				return s.failLocked(w, fmt.Sprintf("PRD lock failed (HTTP %d)", status))
			}
			if err := s.completePhaseLocked(w, "lock complete"); err != nil {
				return err
			}
			continue
		}
		if w.Phase == domain.PhaseVerify {
			evidence, reason := s.verify(w)
			if evidence == nil {
				return s.failLocked(w, reason)
			}
			w.CompletionEvidence = evidence
			if err := s.completePhaseLocked(w, "verified completion"); err != nil {
				return err
			}
			w.Status, w.Reason = domain.WorkflowDone, "verified completion"
			return s.persist(w)
		}
		if w.ChildRunID != "" {
			return nil
		}
		// Recover the small crash window between a child spawn and the workflow journal update.
		if adopted := s.findLiveChild(w); adopted != "" {
			w.ChildRunID = adopted
			if child, ok := s.store.Get(adopted); ok && w.Phase == domain.PhaseBuild {
				w.ResumeCount = child.ResumeCount
			}
			return s.persist(w)
		}
		kind := string(w.Phase)
		promptTarget := w.ResolvedPath
		if promptTarget == "" {
			promptTarget = w.Target
		}
		if w.Phase == domain.PhasePickup || w.Phase == domain.PhaseRPlan {
			promptTarget = w.Target
		}
		prompt := "/saki-builder:" + kind
		if promptTarget != "" {
			prompt += " " + promptTarget
		}
		if w.Phase == domain.PhaseBuild {
			// The build engine uses the sentinel to distinguish an incomplete turn from a
			// completed build. Make that contract explicit to every workflow child; a normal
			// summary alone is intentionally not enough to consume the remaining build turns.
			prompt += "\nWhen all required implementation work is complete and the durable build evidence is written, end your final response with PRD_BUILD_COMPLETE on its own line."
		}
		if continuation != "" {
			prompt += "\nOperator selected option: " + continuation
		}
		cwd := w.Cwd
		// The workflow lane is the idempotency boundary. Passing the request key to every build
		// child would make an explicit Continue hit BuildEngineService's child-level idempotency map
		// and re-adopt the finalized predecessor instead of spawning the requested next turn.
		run, err := s.spawn.SpawnWorkflowChild(WorkflowChildRequest{Prompt: prompt, Cwd: &cwd, ConfigDir: configDir, Meta: &domain.Meta{Kind: domain.RunKind(kind), LaneKey: w.LaneKey}, Engine: engine, Build: w.Phase == domain.PhaseBuild})
		if err != nil {
			return s.failLocked(w, err.Error())
		}
		w.ChildRunID = run.ID
		if w.Phase == domain.PhaseBuild {
			w.ResumeCount = run.ResumeCount
		} else {
			w.ResumeCount++
		}
		return s.persist(w)
	}
	return nil
}

func (s *WorkflowService) firstPhase(info workflowTarget) domain.WorkflowPhase {
	if info.track == "Plan" {
		// An attached plan is the input to rplan, not proof that the rplan phase was
		// completed. The workflow owns the ordered plan-track journey and only a
		// completed child transition may advance to review. This also keeps a
		// restarted workflow from silently skipping the first required verdict.
		return domain.PhaseRPlan
	}
	if info.path == "" {
		return domain.PhasePickup
	}
	content, ok := s.fs.Read(info.path)
	if ok && domain.IsLocked(content) && s.protoPresent(info.cwd, info.path) {
		return domain.PhaseBuild
	}
	return domain.PhaseProto
}

func (s *WorkflowService) protoPresent(cwd, path string) bool {
	return findProtoPreview(s.fs, cwd, path) != ""
}

func bodyOK(body any) bool {
	m, ok := body.(map[string]any)
	return ok && m["ok"] == true
}

func (s *WorkflowService) transitionLocked(w *domain.Workflow, next domain.WorkflowPhase) error {
	if w.Phase == next {
		return nil
	}
	now := s.clock.Now()
	w.Phase = next
	w.ChildRunID = ""
	w.PhaseHistory = append(w.PhaseHistory, domain.PhaseRecord{Phase: next, Status: "started", At: now})
	w.UpdatedAt = now
	return s.persist(w)
}

func (s *WorkflowService) completePhaseLocked(w *domain.Workflow, reason string) error {
	for i := len(w.PhaseHistory) - 1; i >= 0; i-- {
		if w.PhaseHistory[i].Phase == w.Phase && (w.PhaseHistory[i].Status == "started" || w.PhaseHistory[i].Status == "continued") {
			w.PhaseHistory[i].Status = "done"
			w.PhaseHistory[i].ChildRunID = w.ChildRunID
			w.PhaseHistory[i].Reason = reason
			break
		}
	}
	w.ChildRunID = ""
	next := nextPhase(w.Track, w.Phase)
	if next != "" {
		w.Phase = next
		w.PhaseHistory = append(w.PhaseHistory, domain.PhaseRecord{Phase: next, Status: "started", At: s.clock.Now()})
	}
	w.UpdatedAt = s.clock.Now()
	// One journal write covers both the completed phase and its successor. A restart can therefore
	// observe either the old started phase or the new phase, but never a completed phase that would
	// be spawned again.
	return s.persist(w)
}

func phaseDone(w *domain.Workflow, phase domain.WorkflowPhase) bool {
	for i := len(w.PhaseHistory) - 1; i >= 0; i-- {
		if w.PhaseHistory[i].Phase != phase {
			continue
		}
		return w.PhaseHistory[i].Status == "done"
	}
	return false
}

func nextPhase(track string, phase domain.WorkflowPhase) domain.WorkflowPhase {
	var phases []domain.WorkflowPhase
	if track == "Plan" {
		phases = domain.PlanPhases()
	} else {
		phases = domain.PRDPhases()
	}
	for i, p := range phases {
		if p == phase && i+1 < len(phases) {
			return phases[i+1]
		}
	}
	return ""
}

func (s *WorkflowService) failLocked(w *domain.Workflow, reason string) error {
	w.Status, w.Reason, w.ChildRunID = domain.WorkflowFailed, reason, ""
	w.UpdatedAt = s.clock.Now()
	if n := len(w.PhaseHistory); n > 0 && w.PhaseHistory[n-1].Status == "started" {
		w.PhaseHistory[n-1].Status, w.PhaseHistory[n-1].Reason = "failed", reason
	}
	if err := s.persist(w); err != nil {
		return err
	}
	return nil
}

func (s *WorkflowService) persist(w *domain.Workflow) error {
	if err := s.journal.EnsureWritable(); err != nil {
		return err
	}
	return s.journal.Write(*w)
}

func (s *WorkflowService) findLiveChild(w *domain.Workflow) string {
	for _, r := range s.store.List() {
		if r.Status == domain.StatusRunning && r.Meta != nil && r.Meta.LaneKey == w.LaneKey && r.Prompt != "" {
			return r.ID
		}
	}
	return ""
}

// findBuildSuccessor closes the observation race between the build engine spawning a resumed
// turn and the workflow sweep adopting it. A successor can already be finalized by the time the
// sweep runs, so looking only for live children would make the workflow classify its predecessor
// (which has no completion sentinel) as a failed build.
func (s *WorkflowService) findBuildSuccessor(w *domain.Workflow, predecessor domain.Run) string {
	if w.Phase != domain.PhaseBuild || predecessor.Meta == nil {
		return ""
	}
	bestID := ""
	bestResume := predecessor.ResumeCount
	for _, candidate := range s.store.List() {
		if candidate.ID == predecessor.ID || candidate.Meta == nil || candidate.Meta.LaneKey != w.LaneKey || !candidate.Meta.Kind.IsBuild() || candidate.Prompt != predecessor.Prompt {
			continue
		}
		if candidate.ResumeCount <= bestResume {
			continue
		}
		if bestID == "" || candidate.ResumeCount > bestResume {
			bestID, bestResume = candidate.ID, candidate.ResumeCount
		}
	}
	return bestID
}

// Sweep is the coordinator's restart-safe pump. BuildEngineService.Sweep remains responsible for
// child retry/backoff; this pump only observes the child and advances the workflow identity.
func (s *WorkflowService) Sweep() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, w := range s.workflows {
		if w.Status != domain.WorkflowRunning {
			continue
		}
		_ = s.observeLocked(w, nil, "")
	}
}

func (s *WorkflowService) observeLocked(w *domain.Workflow, configDir *string, engine domain.RunEngine) error {
	if w.ChildRunID == "" {
		return s.advanceLocked(w, configDir, engine, "")
	}
	run, ok := s.store.Get(w.ChildRunID)
	if !ok {
		if adopted := s.findLiveChild(w); adopted != "" {
			w.ChildRunID = adopted
			if child, ok := s.store.Get(adopted); ok && w.Phase == domain.PhaseBuild {
				w.ResumeCount = child.ResumeCount
			}
			return s.persist(w)
		}
		return s.failLocked(w, "child run disappeared; refusing to claim workflow success")
	}
	if successor := s.findBuildSuccessor(w, run); successor != "" {
		w.ChildRunID = successor
		if child, ok := s.store.Get(successor); ok {
			w.ResumeCount = child.ResumeCount
		}
		w.PendingUntil = 0
		if err := s.persist(w); err != nil {
			return err
		}
		return s.observeLocked(w, configDir, engine)
	}
	if run.Status == domain.StatusRunning {
		return nil
	}
	if run.Finalizing {
		return nil
	}
	if w.Phase == domain.PhaseBuild {
		for _, candidate := range s.store.List() {
			if candidate.Status == domain.StatusRunning && candidate.Meta != nil && candidate.Meta.Kind.IsBuild() && candidate.Meta.LaneKey == w.LaneKey {
				w.ChildRunID = candidate.ID
				w.ResumeCount = candidate.ResumeCount
				w.PendingUntil = 0
				return s.persist(w)
			}
		}
		if run.PendingResume != nil {
			w.ResumeCount = run.PendingResume.ResumeCount
			w.PendingUntil = run.PendingResume.ResumeAt
			_ = s.persist(w)
			return nil
		}
		w.PendingUntil = 0
	}
	lines := []ParsedLine{}
	if s.output != nil {
		lines, _ = s.output.ReadAll(run.ID)
	}
	if w.Phase == domain.PhaseBuild && run.PushedAt != nil && !pushResultRecorded(lines) {
		// BuildEngine records PushedAt before its bounded push goroutine runs. Keep the workflow
		// non-terminal until that durable result is visible, so a failed push cannot be hidden by the
		// child exit-0 race.
		return nil
	}
	if d := ParseDecision(FinalSpokenText(lines)); d != nil {
		w.Status = domain.WorkflowAwaitingDecision
		w.AwaitingDecision = &domain.WorkflowDecision{Kind: d.Kind, Question: d.Question, Options: domain.CopyStrings(d.Options), Slice: d.Slice}
		w.Reason = d.Question
		w.ChildRunID = ""
		return s.persist(w)
	}
	if reason := workflowParkReason(lines); reason != "" {
		w.Status, w.ParkedReason, w.Reason, w.ChildRunID = domain.WorkflowParked, reason, reason, ""
		return s.persist(w)
	}
	if run.Status != domain.StatusDone {
		return s.failLocked(w, fmt.Sprintf("child phase %s failed (exit %v)", w.Phase, run.ExitCode))
	}
	// A child completion is only a phase transition.  The build sentinel is useful context for
	// legacy callers, but it is not required here: some engines/turns finish with a normal summary
	// instead.  The verify phase below is the sole success boundary and will fail closed when the
	// durable manifest, commits, or phase evidence are absent.
	if err := s.completePhaseLocked(w, "child completed"); err != nil {
		return err
	}
	return s.advanceLocked(w, configDir, engine, "")
}

func workflowParkReason(lines []ParsedLine) string {
	for i := len(lines) - 1; i >= 0; i-- {
		if lines[i].Kind != "raw" {
			continue
		}
		text := lines[i].Text
		if parkedRe.MatchString(text) {
			return text
		}
	}
	if final := FinalSpokenText(lines); final != nil {
		trimmed := strings.TrimSpace(*final)
		if strings.HasPrefix(trimmed, "BLOCKED:") || strings.HasPrefix(trimmed, "HARD STOP") {
			return trimmed
		}
	}
	return ""
}

func pushResultRecorded(lines []ParsedLine) bool {
	for _, line := range lines {
		if line.Kind != "raw" {
			continue
		}
		text := strings.ToLower(strings.TrimSpace(line.Text))
		if strings.HasPrefix(text, "pushed ") || strings.HasPrefix(text, "push skipped:") || strings.HasPrefix(text, "push failed:") {
			return true
		}
	}
	return false
}

func (s *WorkflowService) verify(w *domain.Workflow) (*domain.WorkflowEvidence, string) {
	info, err := s.resolve(w.Cwd, w.Target)
	if err != nil || info.path == "" {
		return nil, "verification failed: target artifact is missing or escaped cwd"
	}
	evidence := &domain.WorkflowEvidence{CheckedPaths: []string{info.path}, CommitIDs: []string{}, PhaseVerdicts: map[string]string{}, VerifiedAt: s.clock.Now()}
	for _, record := range w.PhaseHistory {
		if record.Status == "done" {
			evidence.PhaseVerdicts[string(record.Phase)] = "success"
		}
	}
	if w.Track == "PRD" {
		content, ok := s.fs.Read(info.path)
		if !ok || !domain.IsLocked(content) {
			return nil, "verification failed: PRD is not locked"
		}
		if !s.protoPresent(w.Cwd, info.path) {
			return nil, "verification failed: proto preview artifact is missing"
		}
		statePath, manifest := s.readManifest(w.Cwd, info.path)
		if manifest == nil {
			return nil, "verification failed: build manifest is missing or malformed"
		}
		evidence.CheckedPaths = append(evidence.CheckedPaths, statePath)
		if manifest.PRD == "" || !sameContainedPath(w.Cwd, manifest.PRD, info.path) {
			return nil, "verification failed: build manifest targets a different PRD"
		}
		if len(manifest.Slices) == 0 {
			return nil, "verification failed: build manifest has no slices"
		}
		for _, slice := range manifest.Slices {
			if strings.ToLower(slice.Status) != "done" {
				return nil, fmt.Sprintf("verification failed: slice %d is not done", slice.N)
			}
			if len(slice.Steps) == 0 {
				return nil, fmt.Sprintf("verification failed: slice %d has no recorded steps", slice.N)
			}
			for _, step := range slice.Steps {
				if strings.ToLower(step.Status) != "done" {
					return nil, fmt.Sprintf("verification failed: slice %d has an incomplete step", slice.N)
				}
				if step.Artifact != "" && !artifactExists(s.fs, w.Cwd, step.Artifact) {
					return nil, "verification failed: a recorded artifact is missing"
				}
				if step.Commit == "" {
					continue
				}
				if s.git == nil || !s.git.CommitExists(w.Cwd, step.Commit) {
					return nil, "verification failed: implementation commit is missing"
				}
				evidence.CommitIDs = append(evidence.CommitIDs, step.Commit)
			}
		}
		if !phaseVerdict(evidence, domain.PhaseBuild) {
			return nil, "verification failed: build phase has no successful verdict"
		}
	} else {
		content, ok := s.fs.Read(info.path)
		if !ok {
			return nil, "verification failed: plan is missing"
		}
		progress := domain.ClassifyCheckboxes(content)
		if progress.Total == 0 || progress.Done != progress.Total {
			return nil, "verification failed: plan gates are incomplete"
		}
		for _, phase := range []domain.WorkflowPhase{domain.PhaseQA, domain.PhaseReviewer, domain.PhaseWrap} {
			if !phaseVerdict(evidence, phase) {
				return nil, fmt.Sprintf("verification failed: %s phase has no successful verdict", phase)
			}
		}
	}
	lastChild := ""
	for i := len(w.PhaseHistory) - 1; i >= 0; i-- {
		if w.PhaseHistory[i].Phase == domain.PhaseBuild && w.PhaseHistory[i].ChildRunID != "" {
			lastChild = w.PhaseHistory[i].ChildRunID
			break
		}
	}
	if s.output != nil && lastChild != "" {
		lines, _ := s.output.ReadAll(lastChild)
		for _, line := range lines {
			if line.Kind == "raw" && strings.HasPrefix(strings.ToLower(strings.TrimSpace(line.Text)), "push failed:") {
				return nil, "verification failed: push failed — " + line.Text
			}
		}
	}
	return evidence, ""
}

func artifactExists(fs WorkItemsFS, cwd, artifact string) bool {
	path := artifact
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	path = filepath.Clean(path)
	return contained(filepath.Clean(cwd), path) && fs.Exists(path)
}

func sameContainedPath(cwd, left, right string) bool {
	root := filepath.Clean(cwd)
	resolve := func(path string) string {
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		return filepath.Clean(path)
	}
	leftPath, rightPath := resolve(left), resolve(right)
	return contained(root, leftPath) && contained(root, rightPath) && leftPath == rightPath
}

func phaseVerdict(e *domain.WorkflowEvidence, phase domain.WorkflowPhase) bool {
	return e.PhaseVerdicts[string(phase)] == "success"
}

func (s *WorkflowService) readManifest(cwd, path string) (string, *domain.BuildManifest) {
	for _, candidate := range domain.BuildArtifactCandidates(cwd, path, "state") {
		if raw, ok := s.fs.Read(candidate); ok {
			if manifest := domain.ParseManifest(raw); manifest != nil {
				return candidate, manifest
			}
		}
	}
	return "", nil
}

func (s *WorkflowService) Continue(id, option string) (WorkflowStartResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w := s.workflows[id]
	if w == nil {
		return WorkflowStartResult{}, ErrWorkflowNotFound
	}
	if w.Status == domain.WorkflowDone {
		return WorkflowStartResult{Workflow: *w, Deduped: true}, nil
	}
	if w.Status == domain.WorkflowRunning {
		return WorkflowStartResult{Workflow: *w, Deduped: true}, nil
	}
	if w.Status == domain.WorkflowFailed || w.Status == domain.WorkflowStopped {
		return WorkflowStartResult{}, ErrWorkflowTerminal
	}
	if w.Status == domain.WorkflowAwaitingDecision {
		if w.AwaitingDecision == nil {
			return WorkflowStartResult{}, ErrWorkflowTerminal
		}
		valid := false
		for _, candidate := range w.AwaitingDecision.Options {
			if candidate == option {
				valid = true
				break
			}
		}
		if !valid {
			return WorkflowStartResult{}, fmt.Errorf("%w: %q is not one of the recorded options", ErrWorkflowOption, option)
		}
		w.AwaitingDecision = nil
	}
	w.Status, w.ParkedReason, w.Reason = domain.WorkflowRunning, "", ""
	w.PhaseHistory = append(w.PhaseHistory, domain.PhaseRecord{Phase: w.Phase, Status: "continued", Reason: option, At: s.clock.Now()})
	if err := s.persist(w); err != nil {
		return WorkflowStartResult{}, err
	}
	if err := s.advanceLocked(w, nil, "", option); err != nil {
		return WorkflowStartResult{}, err
	}
	return WorkflowStartResult{Workflow: *w}, nil
}

func (s *WorkflowService) Stop(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	w := s.workflows[id]
	if w == nil || w.Status.Terminal() {
		return false
	}
	child := w.ChildRunID
	w.Status, w.Reason, w.ChildRunID, w.PendingUntil = domain.WorkflowStopped, "stopped by operator", "", 0
	_ = s.persist(w)
	if child != "" && s.stop != nil {
		s.stop.StopWorkflowChild(child)
	}
	return true
}

func (s *WorkflowService) Get(id string) (domain.Workflow, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.workflows[id]
	if !ok {
		return domain.Workflow{}, false
	}
	return *w, true
}

type WorkflowEvent struct {
	Kind       string                `json:"kind"`
	WorkflowID string                `json:"workflowId"`
	Phase      domain.WorkflowPhase  `json:"phase"`
	Status     domain.WorkflowStatus `json:"status"`
	ChildRunID string                `json:"childRunId,omitempty"`
	Line       *ParsedLine           `json:"line,omitempty"`
}

func (s *WorkflowService) Stream(ctx context.Context, id string, emit func(WorkflowEvent), end func(domain.Workflow)) {
	seen := map[string]int{}
	t := time.NewTicker(150 * time.Millisecond)
	defer t.Stop()
	for {
		s.mu.Lock()
		w, ok := s.workflows[id]
		if !ok {
			s.mu.Unlock()
			return
		}
		copy := *w
		emit(WorkflowEvent{Kind: "workflow", WorkflowID: copy.ID, Phase: copy.Phase, Status: copy.Status, ChildRunID: copy.ChildRunID})
		if s.output != nil {
			// A phase transition clears ChildRunID after recording it in phase history. Include
			// those historical ids as well as the active child so a follower receives every
			// child's output exactly once, even when a fast child completes between polls.
			childIDs := make([]string, 0, len(copy.PhaseHistory)+1)
			for _, record := range copy.PhaseHistory {
				if record.ChildRunID != "" {
					childIDs = append(childIDs, record.ChildRunID)
				}
			}
			if copy.ChildRunID != "" {
				childIDs = append(childIDs, copy.ChildRunID)
			}
			for _, childID := range childIDs {
				lines, _ := s.output.ReadAll(childID)
				start := seen[childID]
				for i := start; i < len(lines); i++ {
					line := lines[i]
					emit(WorkflowEvent{Kind: "child", WorkflowID: copy.ID, Phase: copy.Phase, Status: copy.Status, ChildRunID: childID, Line: &line})
				}
				seen[childID] = len(lines)
			}
		}
		s.mu.Unlock()
		if copy.Status.Terminal() {
			end(copy)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Sweep()
		}
	}
}

func (s *WorkflowService) WorkflowIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.workflows))
	for id := range s.workflows {
		ids = append(ids, id)
	}
	return ids
}
