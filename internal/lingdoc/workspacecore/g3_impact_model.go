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
	ID         string           `json:"id"`
	ProjectID  string           `json:"project_id"`
	Source     ObjectVersionRef `json:"source"`
	Target     ObjectVersionRef `json:"target"`
	Relation   string           `json:"relation"`
	Action     string           `json:"action"`
	Origin     string           `json:"origin"`
	Reason     string           `json:"reason"`
	Status     string           `json:"status"`
	Confidence float64          `json:"confidence"`
	ActorID    string           `json:"actor_id"`
	Version    int64            `json:"version"`
	CreatedAt  time.Time        `json:"created_at"`
	UpdatedAt  time.Time        `json:"updated_at"`
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
	ID                   string             `json:"id"`
	ProjectID            string             `json:"project_id"`
	ChangeSetID          string             `json:"change_set_id"`
	Target               ObjectVersionRef   `json:"target"`
	TargetRevision       int64              `json:"target_revision"`
	Action               string             `json:"action"`
	Severity             string             `json:"severity"`
	ResponsibilityDomain string             `json:"responsibility_domain"`
	AssigneeID           string             `json:"assignee_id,omitempty"`
	Status               string             `json:"status"`
	Causes               []ImpactCause      `json:"causes"`
	Version              int64              `json:"version"`
	Disposition          string             `json:"disposition,omitempty"`
	HumanDecision        string             `json:"human_decision,omitempty"`
	ClosureEvidence      []ObjectVersionRef `json:"closure_evidence"`
	CreatedAt            time.Time          `json:"created_at"`
	UpdatedAt            time.Time          `json:"updated_at"`
}
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
	CheckedAt       time.Time       `json:"checked_at"`
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
