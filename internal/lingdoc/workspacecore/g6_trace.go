package workspacecore

import (
	"context"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"strings"
	"time"
)

type RuntimeTrace struct {
	CorrelationID, CausationID, RequestID, TaskID, ChangeSetID, ImpactTaskID, SnapshotID, ExportID, Category, ErrorCode, RuntimeMode string
	TargetRevision                                                                                                                   int64
	QueueDepth                                                                                                                       int64
}
type runtimeTraceKey struct{}

func WithRuntimeTrace(ctx context.Context, trace RuntimeTrace) context.Context {
	return context.WithValue(ctx, runtimeTraceKey{}, trace)
}
func RuntimeTraceFromContext(ctx context.Context) RuntimeTrace {
	trace, _ := ctx.Value(runtimeTraceKey{}).(RuntimeTrace)
	if trace.RequestID == "" {
		trace.RequestID, _ = types.RequestIDFromContext(ctx)
	}
	if trace.CorrelationID == "" {
		trace.CorrelationID = trace.RequestID
	}
	return trace
}
func RecordRuntimeOperationContext(ctx context.Context, tx Transaction, actor Actor, p Project, operation, status string, durationMS int64) error {
	store, ok := tx.(runtimeTransaction)
	if !ok {
		return ErrInvalidState
	}
	trace := RuntimeTraceFromContext(ctx)
	if trace.CorrelationID == "" {
		trace.CorrelationID = uuid.NewString()
	}
	category := trace.Category
	if category == "" {
		category = "request"
		lower := strings.ToLower(operation)
		switch {
		case strings.Contains(lower, "changeset"):
			category = "changeset"
		case strings.Contains(lower, "impacttask"):
			category = "impact_task"
		case strings.Contains(lower, "stabilize"):
			category = "stabilization"
		case strings.Contains(lower, "preflight"):
			category = "preflight"
		case strings.Contains(lower, "snapshot") || strings.Contains(lower, "prepare"):
			category = "snapshot"
		case strings.Contains(lower, "download"):
			category = "download"
		case strings.Contains(lower, "export"):
			category = "export"
		}
	}
	event := RuntimeEvent{ID: uuid.NewString(), Origin: "runtime", TenantAlias: "tenant." + hashImpactValue(actor.TenantID)[:16], ProjectAlias: "project." + hashImpactValue(p.ID)[:16], CorrelationID: trace.CorrelationID, CausationID: trace.CausationID, RequestID: trace.RequestID, TaskID: trace.TaskID, ChangeSetID: trace.ChangeSetID, ImpactTaskID: trace.ImpactTaskID, SnapshotID: trace.SnapshotID, ExportID: trace.ExportID, Category: category, Status: status, ErrorCode: trace.ErrorCode, RuntimeMode: effectiveRuntimeMode(trace.RuntimeMode), ContextRevision: p.CurrentContextRevision, TargetRevision: trace.TargetRevision, TemplateVersion: p.TemplateVersion, DurationMS: durationMS, QueueDepth: trace.QueueDepth, CreatedAt: time.Now().UTC()}
	if p.TemplateCopy != nil {
		event.RulesetHash = p.TemplateCopy.RulesetHash
	}
	if !validRuntimeEvent(event) {
		return ErrInvalidRequest
	}
	if err := store.G6Insert(actor.TenantID, p.ID, "event", event.ID, 1, event); err != nil {
		return err
	}
	return applyRuntimeAlerts(tx, actor, p, event)
}

// ObserveRuntime records domain/queue outcomes after their transaction has
// committed, preserving the originating trace for durable asynchronous work.
func (s *Service) ObserveRuntime(ctx context.Context, actor Actor, projectID, operation, status string, durationMS int64) error {
	return s.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error {
		p, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationRead, ""))
		if err != nil {
			return err
		}
		trace := RuntimeTraceFromContext(ctx)
		trace.RuntimeMode = effectiveRuntimeMode(s.runtimeMode)
		return RecordRuntimeOperationContext(WithRuntimeTrace(ctx, trace), tx, actor, p, operation, status, durationMS)
	})
}
