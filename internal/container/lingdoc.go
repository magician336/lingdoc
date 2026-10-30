package container

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"github.com/Tencent/WeKnora/internal/lingdoc/membership"
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
//
// The project side is handed over as three narrow ports, one per context, and
// all three carry the same project service: the composition root is the only
// injection point for each capability (Spec #47 决策 37).
type LingDocWorkspace struct {
	dig.Out
	Guard              *workspace.FullWorkflowGuard
	Handler            *workspace.Handler
	RuleSets           *rulesetstore.HTTPHandler
	Authorizer         workspacecore.CapabilityAuthorizer
	Projects           workspacecore.ProjectApplication
	GenerationContext  workspacecore.GenerationContextApplication
	Integration        workspace.WorkspaceIntegration
	Runtime            *workspace.SourceRuntime
	Audit              workspacecore.AuditSink
	CapabilityObserver delivery.TransactionalCapabilityObserver
	// The manual-judgement channel of the F01 runner (#649). The project
	// service implements this narrow port too, and the composition root
	// hands it over by name, the way the four optional capabilities above
	// are handed over: a run-time type assertion would be a back door,
	// because it could hand back any operation of the aggregate rather
	// than only the ones the narrow port names.
	ManualJudgement workspace.ManualJudgementApplication
}

func NewLingDocWorkspaceHandler(
	db *gorm.DB,
	kbShares interfaces.KBShareService,
	knowledge interfaces.KnowledgeBaseService,
	audit interfaces.AuditLogService,
	models interfaces.ModelService,
	knowledgeObjects interfaces.KnowledgeService,
	webSearch interfaces.WebSearchService,
	templates lingdoctemplate.Reader,
) (LingDocWorkspace, error) {
	if templates == nil {
		return LingDocWorkspace{}, fmt.Errorf("lingdoc template catalog is required")
	}
	auditSink := workspaceAuditSink{service: audit}
	runtime := workspace.NewGORMSourceRuntime(db, workspace.ProjectPorts{}, kbShares, knowledge, templates, auditSink)
	// The origin text of an uploaded material lives in storage: its file path
	// is a storage reference, not a filesystem path. The runtime needs the
	// storage side to read it — without it every quote can only be checked
	// against the chunk itself, and no source of that material counts as a
	// stable original.
	runtime.ConnectOriginFiles(workspace.KnowledgeFileOriginReader{Service: knowledgeObjects})
	ruleSetStore := rulesetstore.NewStore(db, rulesetstore.TenantMembershipAuthorizer{})
	// 受控故障注入只在环境变量显式点名注入点与演示租户时装配（#256）。未装配
	// 时，下面的策略与写入端口与本票之前完全一致。
	fault := faultInjectionFromEnv(context.Background())
	var authorizationPolicy workspacecore.AuthorizationPolicy
	if fault.Point == workspacecore.FaultPointAuthorization {
		authorizationPolicy = workspacecore.NewFaultInjectingAuthorizationPolicy(nil, fault)
	}
	projectService := workspacecore.NewServiceWithAuthorizationPolicy(workspacecore.NewGORMRepository(db), templates, runtime.WorkspaceSourcePolicy(), authorizationPolicy, auditSink, workspacecore.AuthorizationModeEnforce)
	projectService.InstallFaultInjection(fault)
	projectService.WithRuleSetSnapshotReader(workspaceRuleSetSnapshotReader{store: ruleSetStore})
	runtime.ConnectProjects(workspace.ProjectPorts{Authorizer: projectService, Resources: projectService, Projects: projectService, Context: projectService})
	// The four optional capabilities are the aggregate's own. They used to be
	// found by the transport with a run-time type assertion on the project
	// service, which was a back door: the assertion could hand back any
	// operation of the aggregate, not only the ones the narrow port names.
	// The composition root hands them over explicitly instead.
	runtime.ConnectOptionalCapabilities(workspace.OptionalCapabilities{
		Provenance:                     projectService,
		EvidenceSelection:              projectService,
		EvidenceSelectionInTransaction: projectService,
		EvidenceProofPreparation:       projectService,
	})
	runtime.EnableFullWorkflowPolicy()
	projectService.RequireActivationRead(true)
	projectService.SetRuntimeMode("real")
	// The durable quota ledger shares Workspace's authorized project transaction.
	// Approved limits/window definitions have not been supplied: a nil policy
	// source keeps retrieval/model calls blocked rather than granting demo quota.
	researchBudget := workspacecore.NewResearchQuotaLedger(projectService, nil)
	research := workspace.NewResearchAdapter(db, projectService, runtime, kbShares, knowledgeObjects, webSearch,
		workspace.WithResearchBudgetLedger(researchBudget), workspace.WithAcademicSourceCatalogReader(projectService))
	// The model-call ledger is what tells a real sample from a demo one, so the
	// composition root installs it: without it the calls leave no trace and
	// 「this run was a real model」 is unanswerable (55-Spec 决策 1 / 决策 4).
	// One ledger instance for every adapter: the runtime owns it, so the
	// agent turn and the generation run write to the same rows.
	model := workspace.NewHostAgentModel(models,
		workspace.WithModelCallLedger(runtime.ModelCallLedger()))
	projectService.ConfigureAgentRuntime(workspacecore.AgentRuntime{Sources: research, Research: research, Imports: research, Model: model, Actions: workspace.NewAgentActionExecutor(projectService, runtime, nil)})
	// The dependency-edge recommender (#751) shares the same registry and the
	// same ledger instance: one row per call, the same rows the agent turns
	// and generation runs write to.
	projectService.SetDependencyRecommendationModel(workspace.NewHostDependencyRecommendationModel(models,
		workspace.WithRecommendationLedger(runtime.ModelCallLedger())))
	// The AI sentence-location suggester (#774 tier C) shares the same
	// registry and the same ledger instance: one row per call.
	projectService.SetImpactLocationSuggestionModel(workspace.NewHostImpactLocationSuggestionModel(models,
		workspace.WithImpactLocationLedger(runtime.ModelCallLedger())))
	governance, err := workspace.NewTemplateGovernanceRuntime(db, projectService, runtime, model)
	if err != nil {
		return LingDocWorkspace{}, err
	}
	projectService.SetTemplateCatalogResolver(governance)
	projectService.SetImpactRuleChecker(governance.ImpactRuleChecker())
	// The member and owner context left the aggregate: it runs its writes
	// through the shared pipeline and reads through narrow ports, and it never
	// holds the aggregate itself. The composition root is where those ports
	// are built, and it is the only place that knows the aggregate answers
	// them (Spec #47 决策 33、37).
	memberStorage := workspacecore.NewMemberStorage(projectService)
	memberService := membership.NewService(membership.Ports{
		Runner:     workspacecore.NewOperationRunner(projectService),
		Authorizer: workspacecore.NewWorkspaceAuthorizer(projectService),
		Policy:     workspacecore.NewWorkspacePolicy(projectService),
		Repository: workspacecore.NewGORMRepository(db),
		Records:    memberStorage,
		Directory:  memberStorage,
		Transfers:  memberStorage,
		APIKeys:    memberStorage,
		Resources:  memberStorage,
	})
	guard := workspace.NewFullWorkflowGuard(workspace.FullWorkflowGuardPorts{
		Delivery:   projectService,
		Provenance: projectService,
		Impact:     projectService,
		Runtime:    projectService,
		Actions:    projectService,
		Compose:    projectService,
	}, runtime)
	deps := workspace.HandlerDependencies{
		FullWorkflow: &workspace.FullWorkflowDependencies{
			Governance: governance,
			Impact: workspace.ImpactRoutePorts{
				ChangeSets: projectService,
				Impact:     projectService,
				Dependency: projectService,
				Runtime:    projectService,
				Trials:     projectService,
				Roadmap:    projectService,
			},
			Agent: workspace.AgentRoutePorts{
				Research:      projectService,
				Imports:       projectService,
				EvidenceCards: projectService,
				Claims:        projectService,
				Anchors:       projectService,
				AgentSessions: projectService,
				Proposals:     projectService,
				Context:       projectService,
				ChangeSets:    projectService,
			},
			Routes: workspace.FullWorkflowRoutePorts{
				Members:   memberService,
				Reviews:   projectService,
				Lifecycle: projectService,
			},
		},
		Projects:    projectService,
		Members:     memberService,
		Chapters:    projectService,
		TemplateApp: projectService,
		ChangeSets:  projectService,
		Sources:     runtime,
		Integration: runtime,
		Templates:   templates,
	}
	deps.WithSelectedRewrites(workspace.NewSelectedRewriteService(db, projectService, projectService, runtime, workspace.NewSelectedRewriteHostModel(models), auditSink))
	// The machine read and the review anchor endpoints are capabilities the
	// caller asks for by name. The project service provides them; saying so
	// here keeps their presence a decision rather than an accident of which
	// field was filled in.
	deps.WithMachineReads(projectService)
	deps.WithReviewAnchors(projectService, projectService)
	handler, err := workspace.NewHandler(deps)
	if err != nil {
		return LingDocWorkspace{}, err
	}
	ruleSets := rulesetstore.NewHTTPHandler(ruleSetStore, lingDocRuleSetRequestInfo)
	// The manual-judgement channel of the F01 runner (#649): the same
	// project service, handed over by name as the narrow port the two
	// routes speak to.
	return LingDocWorkspace{Guard: guard, Handler: handler, RuleSets: ruleSets, Authorizer: projectService, Projects: projectService, GenerationContext: projectService, Integration: runtime, Runtime: runtime, Audit: auditSink, CapabilityObserver: ruleSetStore, ManualJudgement: projectService}, nil
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

// NewLingDocTemplateCatalog supplies one explicit reader to every consumer.
func NewLingDocTemplateCatalog() lingdoctemplate.Reader {
	return lingdoctemplate.FixedDemoReader{}
}

type workspaceAuditSink struct{ service interfaces.AuditLogService }

// ErrAuditActorRoleEmpty is the refusal the audit sink returns for an event
// whose actor role is the zero value (#559).
//
// audit_logs.actor_role 的空串含义是「未知」，落库口径（#535 管理会话裁定）
// 已把它登记为 2026-10-07 之前的历史遗留，由 90 天保留期自行清除；写侧不再
// 产生新的空串。AuditEvent.Role 的零值恰恰是空串，而上游的填充靠约定——
// HTTP 面的 TenantRoleFromContext / CallerFromContext 各自回退 viewer——
// 不是类型保证，一个漏填的构造点就能再写出一批空串。所以 sink 是最后一道
// 闸：事件不带角色 ⇒ 拒绝写审计、返回可判定错误、一行不落。调用方的语义
// 沿用既有分工：写路径 fail-closed（状态变更不得不可追溯），读路径
// fail-open（丢弃错误、照常发布审计失败告警），拒绝只是少一行无法归因的
// 审计，不改变任何权限结果。
var ErrAuditActorRoleEmpty = errors.New("lingdoc audit event actor role is empty")

// Record writes one authorization audit record to the platform audit log.
//
// The port contract is `Record(context.Context, AuditEvent) error`, and this
// implementation used to return nil no matter what happened: a serialization
// failure and a storage failure were both written to the log and dropped. The
// caller therefore could not tell a recorded decision from a lost one, so
// nothing downstream could alert, count or degrade on it (#414).
//
// The two real failures are returned now. The caller decides what a failed
// write means for the operation it belongs to: the write paths fail closed
// (a state change must never become untraceable) and the ordinary read paths
// stay fail-open and publish an audit-failure alert instead. That difference
// belongs to the caller, not to the sink.
//
// A sink with no platform audit log service is a different case: there is no
// storage to write to, so there is no write that could fail. It stays a
// no-op, the same convention the OpenSearch audit sink adapter uses.
func (s workspaceAuditSink) Record(ctx context.Context, event workspacecore.AuditEvent) error {
	if s.service == nil {
		return nil
	}
	if event.Role == "" {
		err := fmt.Errorf("%w: refusing to write an unattributable audit row (tenant %d, project %s, capability %s)",
			ErrAuditActorRoleEmpty, event.TenantID, event.ProjectID, event.Capability)
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"audit_event": "lingdoc_authorization"})
		return err
	}
	eventDetails := map[string]any{"capability": event.Capability, "decision": event.Decision, "reason": event.Reason}
	for key, value := range event.Details {
		eventDetails[key] = value
	}
	details, err := json.Marshal(eventDetails)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"audit_event": "lingdoc_authorization"})
		return err
	}
	action := types.AuditAction("rbac.lingdoc_authorized")
	outcome := types.AuditOutcomeSuccess
	if event.Decision == "deny" {
		action = types.AuditActionAccessDenied
		outcome = types.AuditOutcomeDenied
	}
	if err := s.service.Log(ctx, &types.AuditLog{TenantID: event.TenantID, ActorUserID: event.UserID, ActorRole: string(event.Role), Action: action, ScopeType: "project", ScopeID: event.ProjectID, TargetType: "lingdoc_project", TargetID: event.ProjectID, Outcome: outcome, Details: types.JSON(details)}); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"audit_event": "lingdoc_authorization", "tenant_id": event.TenantID, "project_id": event.ProjectID})
		return err
	}
	return nil
}

// DeliveryDependencies keeps persistence, version checks and authorization
// explicit. No memory store or always-current/access-allowed fallback is
// silently installed in production. Existing delivery consumers supply these
// ports; this refactor does not add an unimplemented HTTP endpoint.
type DeliveryDependencies struct {
	dig.In
	Snapshots          delivery.SnapshotStore                   `optional:"true"`
	Exports            delivery.ExportStore                     `optional:"true"`
	Currentness        delivery.CurrentnessChecker              `optional:"true"`
	Access             delivery.ExportAccessChecker             `optional:"true"`
	Renderer           delivery.FrozenRenderer                  `optional:"true"`
	Validator          delivery.FrozenValidator                 `optional:"true"`
	Audit              workspacecore.AuditSink                  `optional:"true"`
	CapabilityObserver delivery.TransactionalCapabilityObserver `optional:"true"`
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
	ports, err := delivery.NewTransactionalDeliveryPortWithObserver(deps.Snapshots, deps.Exports, deps.CapabilityObserver)
	if err != nil {
		return LingDocDelivery{}, err
	}
	return LingDocDelivery{Releases: delivery.NewReleaseService(ports.Snapshots, deps.Currentness), Exports: delivery.NewExportService(ports.Snapshots, ports.Exports, deps.Renderer, deps.Validator, deps.Currentness, deps.Access, deps.Audit)}, nil
}
