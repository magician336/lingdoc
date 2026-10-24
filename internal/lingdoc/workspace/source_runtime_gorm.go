package workspace

import (
	"context"
	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// SourceRuntime is the production read-side/source adapter. HTTP holds only
// its application/integration ports; live adapters are shared by all consumers.
//
// The three project-side fields are three narrow ports, one per context. The
// legacy ApplicationService (37 methods) used to play all three roles at once;
// after it is retired each port answers exactly one question.
//
// The four optional fields below are the capabilities the transport used to
// discover with a run-time type assertion on the project service. They are
// plain nullable ports now: the composition root hands them over, and a
// workspace that does not have one answers "the dependency is unavailable"
// rather than quietly dropping a check.
// ModelCallLedger returns the ledger this runtime owns.
//
// The composition root hands the same instance to every adapter, so one real
// model call leaves one row whichever path made the call.
func (r *SourceRuntime) ModelCallLedger() core.ModelCallLedger {
	if r == nil {
		return nil
	}
	return r.modelCallLedger
}

type SourceRuntime struct {
	authorizer   core.CapabilityAuthorizer
	resources    core.ResourceBatchAuthorizer
	projects     core.ProjectApplication
	context      core.GenerationContextApplication
	bindings     *evidence.Bindings
	modelGateway evidence.AssetGateway
	gateway      evidence.AssetGateway
	kbShares     interfaces.KBShareService
	knowledge    interfaces.KnowledgeBaseService
	templates    delivery.TemplateReader
	db           *gorm.DB
	audit        AuditSink
	// originFiles reads a material's source bytes from storage. It is
	// optional; see ConnectOriginFiles.
	originFiles OriginFileReader
	// modelCallLedger is the ledger every adapter that calls a provider
	// writes to.  The runtime owns the one instance on purpose: the agent
	// turn and the generation run write to the same ledger, so "one real
	// model call leaves one row" holds no matter which path made the call.
	modelCallLedger core.ModelCallLedger

	provenance            core.ProjectProvenanceApplication
	evidenceSelector      core.EvidenceSelectionApplication
	evidenceSelectorInTx  core.EvidenceSelectionTransactionApplication
	evidenceProofPreparer core.EvidenceProofPreparationApplication
}

// OptionalCapabilities carries the four optional capabilities the transport
// resolves at run time.
//
// Every one of them is a capability of the aggregate, so the aggregate's own
// package owns the language and the port (Spec #47 决策 33). The composition
// root is the only place that knows the production value: it hands them over
// explicitly, so no transport ever has to ask a value what it can do.
type OptionalCapabilities struct {
	// Provenance answers "is the provenance in this frozen input real".
	Provenance core.ProjectProvenanceApplication
	// EvidenceSelection answers "which evidence may go into the model".
	EvidenceSelection core.EvidenceSelectionApplication
	// EvidenceSelectionInTransaction answers the same question inside a transaction.
	EvidenceSelectionInTransaction core.EvidenceSelectionTransactionApplication
	// EvidenceProofPreparation answers "prepare the candidate's evidence proof".
	EvidenceProofPreparation core.EvidenceProofPreparationApplication
}

func NewGORMSourceRuntime(db *gorm.DB, projectPorts ProjectPorts, shares interfaces.KBShareService, knowledge interfaces.KnowledgeBaseService, templates delivery.TemplateReader, audits ...AuditSink) *SourceRuntime {
	ports := NewGORMSourcePorts(db, shares)
	var audit AuditSink
	if len(audits) > 0 {
		audit = audits[0]
	}
	return &SourceRuntime{db: db, authorizer: projectPorts.Authorizer, resources: projectPorts.Resources, projects: projectPorts.Projects, context: projectPorts.Context, bindings: ports.Bindings.(*evidence.Bindings), gateway: ports.Gateway, kbShares: shares, knowledge: knowledge, templates: templates, audit: audit, modelCallLedger: core.NewGORMModelCallLedger(db)}
}

// ConnectProjects is called only at composition time to connect the mutually
// dependent project authorization and current source-recheck ports.
func (r *SourceRuntime) ConnectProjects(projectPorts ProjectPorts) {
	r.authorizer = projectPorts.Authorizer
	r.resources = projectPorts.Resources
	r.projects = projectPorts.Projects
	r.context = projectPorts.Context
}

// ConnectOptionalCapabilities is called at composition time, next to
// ConnectProjects, to hand over the four optional capabilities.
//
// They used to be found with a run-time type assertion on the project
// service. That assertion was a back door: the value it was asked about was
// the aggregate, so the assertion could hand back any of the aggregate's
// operations, not only the ones the narrow port names. Handing them over
// explicitly keeps the transport's reach at exactly what the port declares.
func (r *SourceRuntime) ConnectOptionalCapabilities(capabilities OptionalCapabilities) {
	if r == nil {
		return
	}
	r.provenance = capabilities.Provenance
	r.evidenceSelector = capabilities.EvidenceSelection
	r.evidenceSelectorInTx = capabilities.EvidenceSelectionInTransaction
	r.evidenceProofPreparer = capabilities.EvidenceProofPreparation
}
func (r *SourceRuntime) EnableFullWorkflowPolicy() {
	r.gateway = NewProjectAssetPolicyGateway(r.db, liveGORMSourceGateway{db: r.db, base: r.gateway})
	r.modelGateway = NewModelAssetPolicyGateway(r.db, r.gateway)
}
func (r *SourceRuntime) generationGateway() evidence.AssetGateway {
	if r.modelGateway != nil {
		return r.modelGateway
	}
	return r.gateway
}
func (r *SourceRuntime) application() *SourceService {
	return NewSourceServiceWithReviewer(sourceAssetAuthorizer{capabilities: r.authorizer, resources: r.resources}, r.bindings, r.gateway, dbSourceCatalog{db: r.db}, r.origins(), r.kbShares, r.knowledge, r.sourceRevalidator(), r.audit)
}

type sourceAssetAuthorizer struct {
	capabilities core.CapabilityAuthorizer
	resources    core.ResourceBatchAuthorizer
}

func (a sourceAssetAuthorizer) Authorize(ctx context.Context, actor Actor, projectID, capability string) error {
	if a.capabilities == nil {
		return core.ErrPermissionSchemaUnavailable
	}
	return a.capabilities.Authorize(ctx, actor, projectID, capability)
}

func (a sourceAssetAuthorizer) AuthorizeResourceBatch(ctx context.Context, actor Actor, projectID string, action AuthorizationAction, resources []ResourceRef) ([]ResourceAuthorizationDecision, error) {
	if a.resources == nil {
		return nil, core.ErrPermissionSchemaUnavailable
	}
	return a.resources.AuthorizeResourceBatch(ctx, actor, projectID, action, resources)
}
func (r *SourceRuntime) ListAssets(ctx context.Context, a Actor, p string) ([]evidence.Asset, error) {
	return r.application().ListAssets(ctx, a, p)
}
func (r *SourceRuntime) BindAsset(ctx context.Context, a Actor, p, key, id string) (evidence.Asset, bool, error) {
	return r.application().BindAsset(ctx, a, p, key, id)
}
func (r *SourceRuntime) RetrieveSources(ctx context.Context, a Actor, p string, in RetrieveSourcesInput) ([]evidence.Source, error) {
	return r.application().RetrieveSources(ctx, a, p, in)
}
func (r *SourceRuntime) AccessStatus(ctx context.Context, a Actor, p string) (AccessStatus, error) {
	return r.application().AccessStatus(ctx, a, p)
}
func (r *SourceRuntime) GetSource(ctx context.Context, a Actor, p, id string) (evidence.Source, error) {
	read, err := r.readSource(ctx, a, p, id)
	return read.Source, err
}
func (r *SourceRuntime) GetSourceContext(ctx context.Context, a Actor, p, id string) (map[string]any, error) {
	read, err := r.readSource(ctx, a, p, id)
	if err != nil {
		return nil, err
	}
	return r.expandSourceContext(ctx, read)
}

var _ SourceApplicationService = (*SourceRuntime)(nil)
var _ WorkspaceIntegration = (*SourceRuntime)(nil)
