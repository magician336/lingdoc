package rulesetstore

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/clienterror"
	"github.com/Tencent/WeKnora/internal/lingdoc/template"
)

// The rule set storage surface's own wording.
//
// The rule governance surface answers the same two domain failures with
// "请求字段不符合约定。", the rule set storage surface answers them with
// "规则集不符合安全约束。". The form is decided by the situation, so the
// surface that owns the wording declares it here and wraps the sentinel:
// the sentinel keeps its own form for every other surface, and errors.Is
// still finds it.
var (
	// ErrDraftHashNotEmpty is a draft that carries the server-managed hash,
	// as the rule set storage surface words it.
	ErrDraftHashNotEmpty = clienterror.Wrap(clienterror.Form{Status: 400, Code: "invalid_request", Message: "规则集不符合安全约束。", Retryable: false}, "rule set does not satisfy the safety constraints", ErrDraftHashMustBeEmpty)
	// ErrInvalidRuleSetConstraint is a rule set that does not satisfy the
	// safety constraints, as the rule set storage surface words it.
	ErrInvalidRuleSetConstraint = clienterror.Wrap(clienterror.Form{Status: 400, Code: "invalid_request", Message: "规则集不符合安全约束。", Retryable: false}, "rule set does not satisfy the safety constraints", template.ErrInvalidRuleSet)
)

// The failures this surface raises itself, before any application call.
var (
	// ErrInvalidRequest is a rule set request whose fields do not follow the
	// agreed shape.
	ErrInvalidRequest = clienterror.New(clienterror.InvalidRequest, "invalid rule set request")
	// ErrDraftStateRequired is a request to create a rule set version that is
	// not a draft.
	ErrDraftStateRequired = clienterror.New(clienterror.Form{Status: 400, Code: "invalid_request", Message: "只能创建草稿规则集版本。", Retryable: false}, "only a draft rule set version can be created")
)

// surfaceApplication presents the rule set store's failures the way the rule
// set storage surface words them.
//
// The store itself is shared with the rule governance surface, which words
// these two failures differently. The presentation belongs to the surface, so
// it is applied here, at the surface's own application seam, instead of in a
// transport table.
//
// The optional application interfaces stay optional: an adapter only exposes
// the paged audit and the metrics extension when the store behind it does.
type surfaceApplication struct{ inner Application }

type surfaceAuditApplication struct {
	*surfaceApplication
	audit AuditPageApplication
}

type surfaceMetricsApplication struct {
	*surfaceApplication
	metrics MetricsApplication
}

type surfaceFullApplication struct {
	*surfaceAuditApplication
	metrics MetricsApplication
}

func newSurfaceApplication(inner Application) Application {
	if inner == nil {
		return nil
	}
	audit, hasAudit := inner.(AuditPageApplication)
	metrics, hasMetrics := inner.(MetricsApplication)
	switch {
	case hasAudit && hasMetrics:
		return &surfaceFullApplication{
			surfaceAuditApplication: &surfaceAuditApplication{surfaceApplication: &surfaceApplication{inner: inner}, audit: audit},
			metrics:                 metrics,
		}
	case hasAudit:
		return &surfaceAuditApplication{surfaceApplication: &surfaceApplication{inner: inner}, audit: audit}
	case hasMetrics:
		return &surfaceMetricsApplication{surfaceApplication: &surfaceApplication{inner: inner}, metrics: metrics}
	}
	return &surfaceApplication{inner: inner}
}

func (s *surfaceApplication) present(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrDraftHashMustBeEmpty):
		return ErrDraftHashNotEmpty
	case errors.Is(err, template.ErrInvalidRuleSet):
		return ErrInvalidRuleSetConstraint
	}
	return err
}

func (s *surfaceAuditApplication) ListRuleSetUseAuditsPage(ctx context.Context, actor Actor, projectID string, query RuleSetUseAuditQuery) (RuleSetUseAuditPage, error) {
	page, err := s.audit.ListRuleSetUseAuditsPage(ctx, actor, projectID, query)
	return page, s.present(err)
}

func (s *surfaceMetricsApplication) ReadRuleSetMetrics(ctx context.Context, actor Actor, projectID string, query RuleSetMetricsQuery) (RuleSetMetricsSnapshot, error) {
	snapshot, err := s.metrics.ReadRuleSetMetrics(ctx, actor, projectID, query)
	return snapshot, s.present(err)
}

func (s *surfaceFullApplication) ReadRuleSetMetrics(ctx context.Context, actor Actor, projectID string, query RuleSetMetricsQuery) (RuleSetMetricsSnapshot, error) {
	snapshot, err := s.metrics.ReadRuleSetMetrics(ctx, actor, projectID, query)
	return snapshot, s.present(err)
}

func (s *surfaceApplication) CreateDraft(ctx context.Context, actor Actor, set template.RuleSet) error {
	return s.present(s.inner.CreateDraft(ctx, actor, set))
}

func (s *surfaceApplication) GetVersion(ctx context.Context, actor Actor, ruleSetID, version string) (template.RuleSet, error) {
	set, err := s.inner.GetVersion(ctx, actor, ruleSetID, version)
	return set, s.present(err)
}

func (s *surfaceApplication) ListVersions(ctx context.Context, actor Actor, ruleSetID string) ([]template.RuleSet, error) {
	sets, err := s.inner.ListVersions(ctx, actor, ruleSetID)
	return sets, s.present(err)
}

func (s *surfaceApplication) ListRuleSetUseAudits(ctx context.Context, actor Actor, projectID string) ([]template.RuleSetUseAuditEvent, error) {
	events, err := s.inner.ListRuleSetUseAudits(ctx, actor, projectID)
	return events, s.present(err)
}

func (s *surfaceApplication) Publish(ctx context.Context, actor Actor, ruleSetID, version string) (template.RuleSet, error) {
	set, err := s.inner.Publish(ctx, actor, ruleSetID, version)
	return set, s.present(err)
}

func (s *surfaceApplication) Withdraw(ctx context.Context, actor Actor, ruleSetID, version string) (template.RuleSet, error) {
	set, err := s.inner.Withdraw(ctx, actor, ruleSetID, version)
	return set, s.present(err)
}

func (s *surfaceApplication) Recall(ctx context.Context, actor Actor, ruleSetID, version string) (template.RuleSet, error) {
	set, err := s.inner.Recall(ctx, actor, ruleSetID, version)
	return set, s.present(err)
}
