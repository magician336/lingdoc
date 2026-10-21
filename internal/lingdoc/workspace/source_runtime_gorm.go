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
type SourceRuntime struct {
	authorizer   core.CapabilityAuthorizer
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
}

func NewGORMSourceRuntime(db *gorm.DB, projectPorts ProjectPorts, shares interfaces.KBShareService, knowledge interfaces.KnowledgeBaseService, templates delivery.TemplateReader, audits ...AuditSink) *SourceRuntime {
	ports := NewGORMSourcePorts(db, shares)
	var audit AuditSink
	if len(audits) > 0 {
		audit = audits[0]
	}
	return &SourceRuntime{db: db, authorizer: projectPorts.Authorizer, projects: projectPorts.Projects, context: projectPorts.Context, bindings: ports.Bindings.(*evidence.Bindings), gateway: ports.Gateway, kbShares: shares, knowledge: knowledge, templates: templates, audit: audit}
}

// ConnectProjects is called only at composition time to connect the mutually
// dependent project authorization and current source-recheck ports.
func (r *SourceRuntime) ConnectProjects(projectPorts ProjectPorts) {
	r.authorizer = projectPorts.Authorizer
	r.projects = projectPorts.Projects
	r.context = projectPorts.Context
}
func (r *SourceRuntime) EnableFullWorkflowPolicy() {
	r.gateway = NewG2ProjectAssetPolicyGateway(r.db, liveGORMSourceGateway{db: r.db, base: r.gateway})
	r.modelGateway = NewG2ModelAssetPolicyGateway(r.db, r.gateway)
}
func (r *SourceRuntime) generationGateway() evidence.AssetGateway {
	if r.modelGateway != nil {
		return r.modelGateway
	}
	return r.gateway
}
func (r *SourceRuntime) application() *SourceService {
	return NewSourceServiceWithReviewer(r.authorizer, r.bindings, r.gateway, dbSourceCatalog{db: r.db}, r.origins(), r.kbShares, r.knowledge, r.sourceRevalidator(), r.audit)
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
