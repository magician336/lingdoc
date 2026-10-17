package container

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"github.com/Tencent/WeKnora/internal/lingdoc/rulesetstore"
	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
	"github.com/Tencent/WeKnora/internal/lingdoc/workspace"
	"github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"go.uber.org/dig"
	"gorm.io/gorm"
)

// NewLingDocWorkspaceHandler is the composition root for LingDoc workspace
// application services and their production adapters. The HTTP handler itself
// receives only application ports and has no knowledge of GORM construction.
type LingDocWorkspace struct {
	dig.Out
	Handler     *workspace.Handler
	RuleSets    *rulesetstore.HTTPHandler
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
	models interfaces.ModelService,
	templates lingdoctemplate.Reader,
) (LingDocWorkspace, error) {
	if templates == nil {
		return LingDocWorkspace{}, fmt.Errorf("lingdoc template catalog is required")
	}
	auditSink := workspaceAuditSink{service: audit}
	runtime := workspace.NewGORMSourceRuntime(db, nil, kbShares, knowledge, templates, auditSink)
	ruleSetStore := rulesetstore.NewStore(db, rulesetstore.TenantMembershipAuthorizer{})
	projectService := workspacecore.NewServiceWithAudit(workspacecore.NewGORMRepository(db), templates, runtime.WorkspaceSourcePolicy(), nil, auditSink).
		WithRuleSetSnapshotReader(workspaceRuleSetSnapshotReader{store: ruleSetStore})
	runtime.ConnectProjects(projectService)
	handler := workspace.NewHandler(workspace.HandlerDependencies{
		Read:             projectService,
		Write:            projectService,
		Sources:          runtime,
		Integration:      runtime,
		Templates:        templates,
		SelectedRewrites: workspace.NewSelectedRewriteService(db, projectService, runtime, workspace.NewSelectedRewriteHostModel(models), auditSink),
	})
	ruleSets := rulesetstore.NewHTTPHandler(ruleSetStore, lingDocRuleSetRequestInfo)
	return LingDocWorkspace{Handler: handler, RuleSets: ruleSets, Projects: projectService, Integration: runtime, Runtime: runtime, Audit: auditSink}, nil
}

type workspaceRuleSetSnapshotReader struct{ store *rulesetstore.Store }

func (r workspaceRuleSetSnapshotReader) GetVersion(ctx context.Context, actor workspacecore.Actor, id, version string) (lingdoctemplate.RuleSet, error) {
	if r.store == nil {
		return lingdoctemplate.RuleSet{}, workspacecore.ErrInvalidState
	}
	role := string(actor.Role)
	if role == "" {
		role = actor.TenantRole
	}
	set, err := r.store.GetVersion(ctx, rulesetstore.Actor{TenantID: actor.TenantID, UserID: actor.UserID, Role: role}, id, version)
	if errors.Is(err, rulesetstore.ErrRuleSetNotFound) {
		return lingdoctemplate.RuleSet{}, workspacecore.ErrNotFound
	}
	if errors.Is(err, rulesetstore.ErrRuleSetAccessDenied) {
		return lingdoctemplate.RuleSet{}, workspacecore.ErrNotFound
	}
	return set, err
}

func (r workspaceRuleSetSnapshotReader) GetVersionInTransaction(ctx context.Context, tx workspacecore.Transaction, actor workspacecore.Actor, id, version string) (lingdoctemplate.RuleSet, error) {
	if r.store == nil {
		return lingdoctemplate.RuleSet{}, workspacecore.ErrInvalidState
	}
	dbTx, ok := tx.(interface{ GORMDB() *gorm.DB })
	if !ok {
		return lingdoctemplate.RuleSet{}, workspacecore.ErrInvalidState
	}
	role := string(actor.Role)
	if role == "" {
		role = actor.TenantRole
	}
	set, err := r.store.GetVersionForBinding(ctx, dbTx.GORMDB(), rulesetstore.Actor{TenantID: actor.TenantID, UserID: actor.UserID, Role: role}, id, version)
	if errors.Is(err, rulesetstore.ErrRuleSetNotFound) || errors.Is(err, rulesetstore.ErrRuleSetAccessDenied) {
		return lingdoctemplate.RuleSet{}, workspacecore.ErrNotFound
	}
	return set, err
}

func lingDocRuleSetRequestInfo(c *gin.Context) (rulesetstore.Actor, string, bool) {
	ctx := c.Request.Context()
	userID, userOK := types.UserIDFromContext(ctx)
	tenantID, tenantOK := types.TenantIDFromContext(ctx)
	requestID, _ := types.RequestIDFromContext(ctx)
	if !userOK || !tenantOK || tenantID == 0 {
		return rulesetstore.Actor{}, requestID, false
	}
	return rulesetstore.Actor{
		TenantID: tenantID,
		UserID:   userID,
		Role:     string(types.TenantRoleFromContext(ctx)),
	}, requestID, true
}

// NewLingDocTemplateCatalog is the explicit composition point for the
// versioned catalog. The current catalog is the immutable internal demo
// version; callers must inject it rather than receiving a silent constructor
// fallback. Replacing this provider with the formal catalog keeps every
// workspace, generation, and delivery consumer on one reader instance.
func NewLingDocTemplateCatalog() lingdoctemplate.Reader {
	return lingdoctemplate.FixedDemoReader{}
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
	ports, err := delivery.NewTransactionalDeliveryPort(deps.Snapshots, deps.Exports)
	if err != nil {
		return LingDocDelivery{}, err
	}
	return LingDocDelivery{Releases: delivery.NewReleaseService(ports.Snapshots, deps.Currentness), Exports: delivery.NewExportService(ports.Snapshots, ports.Exports, deps.Renderer, deps.Validator, deps.Currentness, deps.Access, deps.Audit)}, nil
}
