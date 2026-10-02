package container

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"github.com/Tencent/WeKnora/internal/lingdoc/workspace"
	"github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"go.uber.org/dig"
	"gorm.io/gorm"
)

// NewLingDocWorkspaceHandler is the composition root for LingDoc workspace
// application services and their production adapters. The HTTP handler itself
// receives only application ports and has no knowledge of GORM construction.
type LingDocWorkspace struct {
	dig.Out
	Handler     *workspace.Handler
	Projects    workspace.ApplicationService
	Integration workspace.WorkspaceIntegration
	Runtime     *workspace.SourceRuntime
	Audit       workspacecore.AuditSink
}

func NewLingDocWorkspaceHandler(
	db *gorm.DB,
	kbShares interfaces.KBShareService,
	knowledge interfaces.KnowledgeBaseService,
	audit interfaces.AuditLogService,
) LingDocWorkspace {
	auditSink := workspaceAuditSink{service: audit}
	runtime := workspace.NewGORMSourceRuntime(db, nil, kbShares, knowledge, auditSink)
	projectService := workspacecore.NewServiceWithAudit(workspacecore.NewGORMRepository(db), workspacecore.ContractDemoTemplate{}, runtime.WorkspaceSourcePolicy(), nil, auditSink)
	runtime.ConnectProjects(projectService)
	handler := workspace.NewHandler(workspace.HandlerDependencies{
		Service:     projectService,
		Sources:     runtime,
		Integration: runtime,
	})
	return LingDocWorkspace{Handler: handler, Projects: projectService, Integration: runtime, Runtime: runtime, Audit: auditSink}
}

type workspaceAuditSink struct{ service interfaces.AuditLogService }

func (s workspaceAuditSink) Record(ctx context.Context, event workspacecore.AuditEvent) error {
	if s.service == nil {
		return nil
	}
	eventDetails := map[string]any{"capability": event.Capability, "decision": event.Decision, "reason": event.Reason}
	for key, value := range event.Details {
		eventDetails[key] = value
	}
	details, err := json.Marshal(eventDetails)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"audit_event": "lingdoc_authorization"})
		return nil
	}
	action := types.AuditAction("rbac.lingdoc_authorized")
	outcome := types.AuditOutcomeSuccess
	if event.Decision == "deny" {
		action = types.AuditActionAccessDenied
		outcome = types.AuditOutcomeDenied
	}
	if err := s.service.Log(ctx, &types.AuditLog{TenantID: event.TenantID, ActorUserID: event.UserID, ActorRole: string(event.Role), Action: action, ScopeType: "project", ScopeID: event.ProjectID, TargetType: "lingdoc_project", TargetID: event.ProjectID, Outcome: outcome, Details: types.JSON(details)}); err != nil {
		// Authorization remains fail-open when audit storage is unavailable:
		// the policy decision is already made and the logger preserves the
		// operational signal without turning observability into a dependency.
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"audit_event": "lingdoc_authorization", "tenant_id": event.TenantID, "project_id": event.ProjectID})
	}
	return nil
}

// DeliveryDependencies keeps persistence, version checks and authorization
// explicit. No memory store or always-current/access-allowed fallback is
// silently installed in production. Existing delivery consumers supply these
// ports; this refactor does not add an unimplemented HTTP endpoint.
type DeliveryDependencies struct {
	dig.In
	Snapshots   delivery.SnapshotStore       `optional:"true"`
	Exports     delivery.ExportStore         `optional:"true"`
	Currentness delivery.CurrentnessChecker  `optional:"true"`
	Access      delivery.ExportAccessChecker `optional:"true"`
	Renderer    delivery.FrozenRenderer      `optional:"true"`
	Validator   delivery.FrozenValidator     `optional:"true"`
	Audit       workspacecore.AuditSink      `optional:"true"`
}
type LingDocDelivery struct {
	dig.Out
	Releases delivery.ReleaseApplication
	Exports  delivery.ExportApplication
}

func NewLingDocDelivery(deps DeliveryDependencies) (LingDocDelivery, error) {
	if deps.Snapshots == nil || deps.Exports == nil || deps.Currentness == nil || deps.Access == nil || deps.Validator == nil {
		return LingDocDelivery{}, fmt.Errorf("delivery persistence, currentness and access ports are required")
	}
	if deps.Renderer == nil {
		deps.Renderer = delivery.DOCXRenderer{}
	}
	return LingDocDelivery{Releases: delivery.NewReleaseService(deps.Snapshots, deps.Currentness), Exports: delivery.NewExportService(deps.Snapshots, deps.Exports, deps.Renderer, deps.Validator, deps.Currentness, deps.Access, deps.Audit)}, nil
}
