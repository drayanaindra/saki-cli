package domain

// Workflow is the durable, user-facing aggregate around one or more child Runs.  A Run remains
// the process/journal record; Workflow is the identity that survives phase changes, retries and
// backend restarts.
type Workflow struct {
	ID                 string            `json:"workflowId"`
	LaneKey            string            `json:"laneKey"`
	Cwd                string            `json:"cwd"`
	Target             string            `json:"target"`
	ResolvedPath       string            `json:"resolvedPath,omitempty"`
	Engine             RunEngine         `json:"engine,omitempty"`
	ConfigDir          string            `json:"configDir,omitempty"`
	Track              string            `json:"track"`
	Phase              WorkflowPhase     `json:"phase"`
	ChildRunID         string            `json:"childRunId,omitempty"`
	PhaseHistory       []PhaseRecord     `json:"phaseHistory"`
	Status             WorkflowStatus    `json:"status"`
	PendingUntil       int64             `json:"pendingUntil,omitempty"`
	ResumeCount        int               `json:"resumeCount,omitempty"`
	AwaitingDecision   *WorkflowDecision `json:"awaitingDecision,omitempty"`
	ParkedReason       string            `json:"parkedReason,omitempty"`
	Reason             string            `json:"reason,omitempty"`
	CompletionEvidence *WorkflowEvidence `json:"completionEvidence,omitempty"`
	IdempotencyKey     string            `json:"idempotencyKey,omitempty"`
	CreatedAt          int64             `json:"createdAt"`
	UpdatedAt          int64             `json:"updatedAt"`
}

type WorkflowPhase string

const (
	PhaseResolve     WorkflowPhase = "resolve"
	PhasePickup      WorkflowPhase = "pickup"
	PhaseProto       WorkflowPhase = "proto"
	PhaseLock        WorkflowPhase = "lock"
	PhaseBuild       WorkflowPhase = "build"
	PhaseRPlan       WorkflowPhase = "rplan"
	PhaseRPlanReview WorkflowPhase = "rplan-review"
	PhaseApproved    WorkflowPhase = "approved"
	PhaseQA          WorkflowPhase = "qa"
	PhaseReviewer    WorkflowPhase = "reviewer"
	PhaseWrap        WorkflowPhase = "wrap"
	PhaseVerify      WorkflowPhase = "verify"
)

type WorkflowStatus string

const (
	WorkflowRunning          WorkflowStatus = "running"
	WorkflowParked           WorkflowStatus = "parked"
	WorkflowAwaitingDecision WorkflowStatus = "awaiting-decision"
	WorkflowDone             WorkflowStatus = "done"
	WorkflowFailed           WorkflowStatus = "failed"
	WorkflowStopped          WorkflowStatus = "stopped"
)

func (s WorkflowStatus) Terminal() bool {
	// Parked and awaiting-decision are terminal for follow/stream purposes: they are durable
	// non-success outcomes and must return control to the operator. Continue explicitly reopens them.
	return s == WorkflowDone || s == WorkflowFailed || s == WorkflowStopped || s == WorkflowParked || s == WorkflowAwaitingDecision
}

type PhaseRecord struct {
	Phase      WorkflowPhase `json:"phase"`
	Status     string        `json:"status"`
	ChildRunID string        `json:"childRunId,omitempty"`
	Reason     string        `json:"reason,omitempty"`
	At         int64         `json:"at"`
}

// WorkflowDecision is intentionally data-only.  Continue validates the selected value against
// this persisted option list; it never becomes an arbitrary prompt/shell endpoint.
type WorkflowDecision struct {
	Kind     string   `json:"kind"`
	Question string   `json:"question"`
	Options  []string `json:"options"`
	Slice    *int     `json:"slice,omitempty"`
}

type WorkflowEvidence struct {
	CheckedPaths  []string          `json:"checkedPaths"`
	CommitIDs     []string          `json:"commitIds"`
	PhaseVerdicts map[string]string `json:"phaseVerdicts"`
	VerifiedAt    int64             `json:"verifiedAt"`
}

func PRDPhases() []WorkflowPhase {
	return []WorkflowPhase{PhaseResolve, PhasePickup, PhaseProto, PhaseLock, PhaseBuild, PhaseVerify}
}

func PlanPhases() []WorkflowPhase {
	return []WorkflowPhase{PhaseResolve, PhaseRPlan, PhaseRPlanReview, PhaseApproved, PhaseQA, PhaseReviewer, PhaseWrap, PhaseVerify}
}

func CopyStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}
