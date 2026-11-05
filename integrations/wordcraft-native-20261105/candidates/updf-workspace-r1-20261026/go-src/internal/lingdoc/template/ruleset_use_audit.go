package template

import (
	"context"
	"time"
)

// RuleSetUseAuditEvent is an append-only record of a governed RuleSet being
// consumed by a project operation. The source hash identifies the immutable
// published RuleSet; EffectiveRulesetHash also includes template and project
// overrides used by the evaluator.
type RuleSetUseAuditEvent struct {
	ID                   string    `json:"id"`
	TenantID             uint64    `json:"tenant_id"`
	ActorID              string    `json:"actor_id"`
	ActorRole            string    `json:"actor_role"`
	ProjectID            string    `json:"project_id"`
	Action               string    `json:"action"`
	RuleSetID            string    `json:"ruleset_id"`
	RuleSetVersion       string    `json:"ruleset_version"`
	RuleSetHash          string    `json:"ruleset_hash"`
	EffectiveRulesetHash string    `json:"effective_ruleset_hash"`
	TemplateID           string    `json:"template_id"`
	TemplateVersion      string    `json:"template_version"`
	TemplateCopyID       string    `json:"template_copy_id,omitempty"`
	TemplateCopyVersion  int64     `json:"template_copy_version,omitempty"`
	ProjectVersion       int64     `json:"project_version,omitempty"`
	ResourceID           string    `json:"resource_id,omitempty"`
	Outcome              string    `json:"outcome"`
	CreatedAt            time.Time `json:"created_at"`
	// VerifiedTemplate and VerifiedBinding are application-internal evidence.
	// They never cross the HTTP/JSON boundary and are required when an audit
	// event is recorded without a durable delivery snapshot.
	VerifiedTemplate *Template               `json:"-"`
	VerifiedBinding  *VerifiedRuleSetBinding `json:"-"`
}

// RuleSetUseAuditRecorder is fail-closed. Callers must not return a governed
// check result when its use record cannot be durably written.
type RuleSetUseAuditRecorder interface {
	RecordRuleSetUse(context.Context, RuleSetUseAuditEvent) error
}
