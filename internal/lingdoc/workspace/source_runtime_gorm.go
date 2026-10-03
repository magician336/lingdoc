package workspace

import (
	"context"
	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// SourceRuntime is the production read-side/source adapter. HTTP holds only
// its application/integration ports; live adapters are shared by all consumers.
type SourceRuntime struct {
	service   ApplicationService
	bindings  *evidence.Bindings
	gateway   evidence.AssetGateway
	kbShares  interfaces.KBShareService
	knowledge interfaces.KnowledgeBaseService
	db        *gorm.DB
	audit     AuditSink
}

func NewGORMSourceRuntime(db *gorm.DB, service ApplicationService, shares interfaces.KBShareService, knowledge interfaces.KnowledgeBaseService, audits ...AuditSink) *SourceRuntime {
	ports := NewGORMSourcePorts(db, shares)
	var audit AuditSink
	if len(audits) > 0 {
		audit = audits[0]
	}
	return &SourceRuntime{db: db, service: service, bindings: ports.Bindings.(*evidence.Bindings), gateway: ports.Gateway, kbShares: shares, knowledge: knowledge, audit: audit}
}

// ConnectProjects is called only at composition time to connect the mutually
// dependent project authorization and current source-recheck ports.
func (r *SourceRuntime) ConnectProjects(service ApplicationService) { r.service = service }
func (r *SourceRuntime) application() *SourceService {
	return NewSourceService(r.service, r.bindings, r.gateway, dbSourceCatalog{db: r.db}, r.origins(), r.kbShares, r.knowledge, r.audit)
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
