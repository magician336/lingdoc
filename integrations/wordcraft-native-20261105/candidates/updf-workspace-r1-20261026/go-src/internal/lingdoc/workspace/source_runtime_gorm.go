package workspace

import (
	"context"
	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// SourceRuntime is the production read-side/source adapter. HTTP holds only
// its application/integration ports; live adapters are shared by all consumers.
type SourceRuntime struct {
	service      ApplicationService
	bindings     *evidence.Bindings
	modelGateway evidence.AssetGateway
	gateway      evidence.AssetGateway
	kbShares     interfaces.KBShareService
	knowledge    interfaces.KnowledgeBaseService
	templates    delivery.TemplateReader
	db           *gorm.DB
	audit        AuditSink
	originFiles  func(*gorm.DB) OriginFileReader
	fullWorkflow bool
}

func NewGORMSourceRuntime(db *gorm.DB, service ApplicationService, shares interfaces.KBShareService, knowledge interfaces.KnowledgeBaseService, templates delivery.TemplateReader, audits ...AuditSink) *SourceRuntime {
	ports := NewGORMSourcePorts(db, shares)
	var audit AuditSink
	if len(audits) > 0 {
		audit = audits[0]
	}
	return &SourceRuntime{db: db, service: service, bindings: ports.Bindings.(*evidence.Bindings), gateway: ports.Gateway, kbShares: shares, knowledge: knowledge, templates: templates, audit: audit}
}

// ConnectProjects is called only at composition time to connect the mutually
// dependent project authorization and current source-recheck ports.
func (r *SourceRuntime) ConnectProjects(service ApplicationService) { r.service = service }

// ConnectOriginFiles keeps original-file reads on the same database connection
// as the caller. Transaction clones must never query the outer SQLite pool.
func (r *SourceRuntime) ConnectOriginFiles(factory func(*gorm.DB) OriginFileReader) {
	r.originFiles = factory
}

func (r *SourceRuntime) CloneWithDatabase(db *gorm.DB) *SourceRuntime {
	clone := NewGORMSourceRuntime(db, r.service, r.kbShares, r.knowledge, r.templates, r.audit)
	clone.originFiles = r.originFiles
	if r.fullWorkflow {
		clone.EnableFullWorkflowPolicy()
	}
	return clone
}

func (r *SourceRuntime) EnableFullWorkflowPolicy() {
	if r.fullWorkflow {
		return
	}
	r.fullWorkflow = true
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
	return NewSourceServiceWithReviewer(r.service, r.bindings, r.gateway, dbSourceCatalog{db: r.db}, r.origins(), r.kbShares, r.knowledge, r.sourceRevalidator(), r.audit)
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
func (r *SourceRuntime) GetSourceDocument(ctx context.Context, a Actor, p, id string) (map[string]any, error) {
	read, err := r.readSource(ctx, a, p, id)
	if err != nil {
		return nil, err
	}
	if read.Source.Status != evidence.SourceAvailable {
		return nil, ErrSourceUnavailable
	}
	origin, haveOrigin, err := r.origins().OriginText(ctx, read.Chunk.KnowledgeID)
	if err != nil {
		return nil, err
	}
	if !haveOrigin {
		return nil, ErrSourceUnavailable
	}
	rows, err := r.documentSegments(ctx, read.Chunk)
	if err != nil {
		return nil, err
	}
	segments := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		segments = append(segments, map[string]any{
			"source_id":   row.ID,
			"chunk_index": row.ChunkIndex,
			"start_at":    row.StartAt,
			"end_at":      row.EndAt,
		})
	}
	return map[string]any{
		"source":   contextSource(read.Source),
		"text":     origin,
		"segments": segments,
	}, nil
}

var _ SourceApplicationService = (*SourceRuntime)(nil)
var _ WorkspaceIntegration = (*SourceRuntime)(nil)
