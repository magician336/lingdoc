package workspacecore

import (
	"context"
	"time"
)

type ObjectVersionRef struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Version string `json:"version"`
}
type ImpactObjectState struct {
	Ref             ObjectVersionRef `json:"ref"`
	State           string           `json:"state"`
	SourceIDs       []string         `json:"source_ids"`
	Confirmed       bool             `json:"confirmed"`
	ContextRevision int64            `json:"context_revision"`
}
type DependencyEdge struct {
	ID        string           `json:"id"`
	ProjectID string           `json:"project_id"`
	Source    ObjectVersionRef `json:"source"`
	Target    ObjectVersionRef `json:"target"`
	Relation  string           `json:"relation"`
	Action    string           `json:"action"`
	Origin    string           `json:"origin"`
	Reason    string           `json:"reason"`
	Status    string           `json:"status"`
	// SupersededRelation/SupersededAction remember the tuple a human replaced
	// with decision="modify", so a later automated run that re-proposes it is
	// rejected instead of silently resurrecting the corrected candidate.
	SupersededRelation string    `json:"superseded_relation,omitempty"`
	SupersededAction   string    `json:"superseded_action,omitempty"`
	Confidence         float64   `json:"confidence"`
	ActorID            string    `json:"actor_id"`
	Version            int64     `json:"version"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}
type DependencyEdgeInput struct {
	ExpectedContextRevision int64            `json:"expected_context_revision"`
	Source                  ObjectVersionRef `json:"source"`
	Target                  ObjectVersionRef `json:"target"`
	Relation                string           `json:"relation"`
	Action                  string           `json:"action"`
	Origin                  string           `json:"origin"`
	Reason                  string           `json:"reason"`
	Confidence              float64          `json:"confidence"`
}
type DependencyDecisionInput struct {
	ExpectedVersion int64  `json:"expected_version"`
	Decision        string `json:"decision"`
	Reason          string `json:"reason"`
	// Optional rebinding for decision="modify". Pointer and omitempty keep the
	// existing confirm/reject payloads byte-for-byte backward compatible, and
	// let "modify" distinguish "leave this field alone" from "set it to empty".
	Relation   *string  `json:"relation,omitempty"`
	Action     *string  `json:"action,omitempty"`
	Confidence *float64 `json:"confidence,omitempty"`
}
type ImpactCause struct {
	EdgeID string           `json:"edge_id"`
	Source ObjectVersionRef `json:"source"`
	Reason string           `json:"reason"`
}
type ResolvedImpact struct {
	Target    ObjectVersionRef `json:"target"`
	Action    string           `json:"action"`
	Certainty string           `json:"certainty"`
	Causes    []ImpactCause    `json:"causes"`
}
type ImpactAssessment struct {
	ChangeSetID         string              `json:"change_set_id"`
	ProjectID           string              `json:"project_id"`
	ContextRevision     int64               `json:"context_revision"`
	TemplateCopyVersion int64               `json:"template_copy_version"`
	RulesetHash         string              `json:"ruleset_hash"`
	InputHash           string              `json:"input_hash"`
	Objects             []ImpactObjectState `json:"objects"`
	Edges               []DependencyEdge    `json:"edges"`
	Impacts             []ResolvedImpact    `json:"impacts"`
	SemanticHash        string              `json:"semantic_hash,omitempty"`
	AssessedAt          time.Time           `json:"assessed_at"`
}
type ImpactTask struct {
	ID                   string           `json:"id"`
	ProjectID            string           `json:"project_id"`
	ChangeSetID          string           `json:"change_set_id"`
	Target               ObjectVersionRef `json:"target"`
	TargetRevision       int64            `json:"target_revision"`
	Action               string           `json:"action"`
	Severity             string           `json:"severity"`
	ResponsibilityDomain string           `json:"responsibility_domain"`
	AssigneeID           string           `json:"assignee_id,omitempty"`
	Status               string           `json:"status"`
	Causes               []ImpactCause    `json:"causes"`
	// LocationHints (#774 tier A) points at where the watched values literally
	// occur in the target chapter body. It is an attachment for the human
	// pass, never an input to any verdict the task carries.
	LocationHints   []ImpactLocationHint `json:"location_hints,omitempty"`
	Version         int64                `json:"version"`
	Disposition     string               `json:"disposition,omitempty"`
	HumanDecision   string               `json:"human_decision,omitempty"`
	ClosureEvidence []ObjectVersionRef   `json:"closure_evidence"`
	// StaleReason explains why a task became STALE. STALE is terminal but is
	// not closure: the basis the task was raised against no longer exists, so
	// the task cannot be completed and must not satisfy the release gate.
	StaleReason string    `json:"stale_reason,omitempty"`
	StaleAt     time.Time `json:"stale_at,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Stale reason codes. They are part of the task contract: a consumer must be
// able to tell a replaced target version from a revoked responsible actor
// without re-deriving it from timing.
//
// A revoked source is deliberately absent. It is not an unreachable code left
// behind by an unfinished implementation - it is a decision (#214). The
// operation-level source recheck runs before the write transaction and its
// reference set is a strict superset of the task target's, so a revoked source
// always fails the operation outright and never reaches the stale transition.
// The alternative - reading the task anyway to mark it stale - would mean
// widening a fail-closed check to permit a read, which trades a refusal that
// says nothing for a state change that asserts the obligation is gone when it
// is not. A task whose sources were withdrawn stays unhandled and keeps
// failing; it does not get to quietly become obsolete.
const (
	// ImpactTaskStaleTargetReplaced means the exact target version the task was
	// raised against is no longer the current version of that object.
	ImpactTaskStaleTargetReplaced = "target_version_replaced"
	// ImpactTaskStaleAssigneeRevoked means the assignee lost the capability or
	// project scope required to act on the task.
	ImpactTaskStaleAssigneeRevoked = "assignee_revoked"
)

// ImpactTaskStatuses lists every reachable task state.
//
// DISMISSED is a responsible manager's reasoned acceptance that this impact
// needs no work this time - it is the impact-side counterpart of a waiver, and
// it is only reachable for a non-blocking impact. CANCELLED is deliberately
// absent: it used to sit next to DISMISSED with the same practical meaning
// ("not doing it"), which left two words for one decision in the audit trail.
//
// STALE is terminal and explicitly excluded from the closed set: it records
// that the basis is gone, not that the impact was handled.
var ImpactTaskStatuses = []string{"OPEN", "IN_PROGRESS", "WAITING_USER", "DONE", "DISMISSED", "STALE"}

// ImpactTaskClosedStatuses are the states that count as a closed loop for the
// impact. STALE is intentionally absent: an invalidated task is never closure.
var ImpactTaskClosedStatuses = []string{"DONE", "DISMISSED"}

type ImpactTaskActionInput struct {
	HumanDecision   string             `json:"human_decision,omitempty"`
	ExpectedVersion int64              `json:"expected_version"`
	TargetRevision  int64              `json:"target_revision"`
	Action          string             `json:"action"`
	AssigneeID      string             `json:"assignee_id,omitempty"`
	Reason          string             `json:"reason"`
	ClosureEvidence []ObjectVersionRef `json:"closure_evidence"`
}
type ImpactBlocker struct {
	Code     string `json:"code"`
	TargetID string `json:"target_id,omitempty"`
	Reason   string `json:"reason"`
}
type StabilizationResult struct {
	ChangeSetID     string          `json:"change_set_id"`
	ContextRevision int64           `json:"context_revision"`
	Status          string          `json:"status"`
	Blockers        []ImpactBlocker `json:"blockers"`
	// StaleTaskIDs lists tasks that were invalidated before stabilization.
	// They are neither blockers (their basis is gone, so blocking would be an
	// unresolvable deadlock) nor closure (nothing was discharged). Reporting
	// them keeps the transition from being silently dropped.
	StaleTaskIDs []string  `json:"stale_task_ids"`
	CheckedAt    time.Time `json:"checked_at"`
}
type ImpactRuleChecker interface {
	Check(Transaction, Project) ([]ImpactBlocker, error)
}
type currentEvidenceObjectTransaction interface {
	CurrentEvidenceObject(string, string, string) (ImpactObjectState, error)
}
type impactTransaction interface {
	PutDependencyEdge(DependencyEdge) error
	DependencyEdges(string) ([]DependencyEdge, error)
	DependencyEdge(string, string) (DependencyEdge, error)
	UpdateDependencyEdge(DependencyEdge, DependencyEdge) error
	SaveImpactAssessment(ImpactAssessment) error
	ImpactAssessment(string, string) (ImpactAssessment, error)
	InsertImpactTask(ImpactTask) error
	ImpactTasks(string) ([]ImpactTask, error)
	ImpactTask(string, string) (ImpactTask, error)
	ImpactTaskByIdentity(string, string) (ImpactTask, error)
	UpdateImpactTask(ImpactTask, ImpactTask) error
	InsertImpactHandoff(ImpactTask) error
	RecordContextRevision(Project, ChangeSet) error
}
type ExtendedChangeSetInput struct {
	SemanticDeltas          []SemanticDelta             `json:"semantic_deltas,omitempty"`
	ExpectedContextRevision int64                       `json:"expected_context_revision"`
	Reason                  string                      `json:"reason"`
	Fields                  map[string]ChangeFieldInput `json:"fields"`
	AffectedChapterIDs      []string                    `json:"affected_chapter_ids"`
}
type RuntimeRecoveryChecker interface {
	Recheck(context.Context, Actor, string, string, string) error
}
