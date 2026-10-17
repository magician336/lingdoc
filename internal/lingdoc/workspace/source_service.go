package workspace

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// SourceService implements the asset/source use cases independently from
// HTTP. Its ports are injectable so authorization, revocation, and source
// changes can be exercised without a Gin server or concrete database.
type SourceService struct {
	projects  ProjectAuthorizer
	bindings  BindingStore
	gateway   evidence.AssetGateway
	catalog   SourceCatalog
	origins   evidence.OriginReader
	kbShares  interfaces.KBShareService
	knowledge KnowledgeSearchService
	audit     AuditSink
}

func NewSourceService(
	projects ProjectAuthorizer,
	bindings BindingStore,
	gateway evidence.AssetGateway,
	catalog SourceCatalog,
	origins evidence.OriginReader,
	kbShares interfaces.KBShareService,
	knowledge KnowledgeSearchService,
	audits ...AuditSink,
) *SourceService {
	var audit AuditSink
	if len(audits) > 0 {
		audit = audits[0]
	}
	return &SourceService{
		projects: projects, bindings: bindings, gateway: gateway, catalog: catalog,
		origins: origins, kbShares: kbShares, knowledge: knowledge, audit: audit,
	}
}

func (s *SourceService) recordSourceAudit(ctx context.Context, actor Actor, projectID, capability string, err error) {
	if s.audit == nil {
		return
	}
	decision, reason := "allow", ""
	if err != nil {
		decision, reason = "deny", err.Error()
	}
	_ = s.audit.Record(ctx, AuditEvent{TenantID: actor.TenantID, UserID: actor.UserID, Role: actor.Role, ProjectID: projectID, Capability: capability, Decision: decision, Reason: reason})
}

func (s *SourceService) ListAssets(ctx context.Context, actor Actor, projectID string) ([]evidence.Asset, error) {
	if err := s.projects.Authorize(ctx, actor, projectID, "read"); err != nil {
		return nil, err
	}
	assets, err := s.bindings.BoundAssets(ctx, projectID)
	if err != nil {
		return nil, err
	}
	requested := make([]string, 0, len(assets))
	for _, asset := range assets {
		requested = append(requested, asset.ID)
	}
	resolved, err := s.gateway.ResolveAllowed(ctx, projectID, evidenceActor(actor), requested)
	if err != nil {
		s.recordSourceAudit(ctx, actor, projectID, "source.list", err)
		return nil, err
	}
	if len(resolved.Denied) > 0 {
		s.recordSourceAudit(ctx, actor, projectID, "source.list", ErrSourceUnavailable)
	}
	return resolved.Allowed, nil
}

func (s *SourceService) BindAsset(
	ctx context.Context, actor Actor, projectID, key, knowledgeID string,
) (evidence.Asset, bool, error) {
	if err := s.projects.Authorize(ctx, actor, projectID, "write"); err != nil {
		return evidence.Asset{}, false, err
	}
	knowledgeID = strings.TrimSpace(knowledgeID)
	if knowledgeID == "" {
		return evidence.Asset{}, false, ErrInvalidRequest
	}
	knowledge, err := s.catalog.Knowledge(ctx, knowledgeID)
	if err != nil {
		return evidence.Asset{}, false, err
	}
	if knowledge == nil || s.knowledge == nil {
		return evidence.Asset{}, false, evidence.ErrAssetNotFound
	}
	kb, err := s.knowledge.GetKnowledgeBaseByIDOnly(ctx, knowledge.KnowledgeBaseID)
	if err != nil || kb == nil {
		return evidence.Asset{}, false, evidence.ErrAssetNotFound
	}
	allowed, err := (kbReadChecker{shares: s.kbShares}).canReadKnowledgeBase(ctx, actor, kb)
	if err != nil {
		return evidence.Asset{}, false, err
	}
	if !allowed {
		return evidence.Asset{}, false, evidence.ErrAssetNotFound
	}
	asset, replay, err := s.bindings.BindIdempotent(ctx, actor.TenantID, actor.UserID, projectID, key, evidence.BindInput{
		TenantID: kb.TenantID, ProjectID: projectID, KnowledgeID: knowledge.ID,
		KnowledgeBaseID: knowledge.KnowledgeBaseID, Title: knowledge.Title, CreatedBy: actor.UserID,
		Signal: evidence.KnowledgeSignal{
			KnowledgeID: knowledge.ID, ParseStatus: knowledge.ParseStatus, FileHash: knowledge.FileHash,
			FileSize: knowledge.FileSize, ProcessedAt: valueTime(knowledge.ProcessedAt),
		},
	})
	return asset, replay, err
}

func (s *SourceService) GetSource(ctx context.Context, actor Actor, projectID, sourceID string) (evidence.Source, error) {
	if err := s.projects.Authorize(ctx, actor, projectID, "read"); err != nil {
		return evidence.Source{}, err
	}
	chunk, err := s.catalog.Chunk(ctx, sourceID)
	if err != nil {
		return evidence.Source{}, err
	}
	if chunk == nil {
		return evidence.Source{}, ErrNotFound
	}
	asset, err := s.bindings.AssetForKnowledge(ctx, projectID, chunk.KnowledgeID)
	if err != nil {
		return evidence.Source{}, ErrNotFound
	}
	actorRef := evidenceActor(actor)
	resolved, err := s.gateway.ResolveAllowed(ctx, projectID, actorRef, []string{asset.ID})
	if err != nil {
		s.recordSourceAudit(ctx, actor, projectID, "source.read", err)
		return evidence.Source{}, err
	}
	if len(resolved.Allowed) != 1 {
		s.recordSourceAudit(ctx, actor, projectID, "source.read", ErrSourceUnavailable)
		return evidence.Source{}, ErrSourceUnavailable
	}
	asset = resolved.Allowed[0]
	hit := &types.SearchResult{
		ID: chunk.ID, KnowledgeID: chunk.KnowledgeID, ChunkIndex: chunk.ChunkIndex,
		StartAt: chunk.StartAt, EndAt: chunk.EndAt, Content: chunk.Content,
		ContentRevision: chunk.ContentRevision, KnowledgeTitle: asset.Title,
	}
	sources, err := evidence.NewSourceResolver(s.origins).Resolve(ctx, asset, []*types.SearchResult{hit})
	if err != nil {
		return evidence.Source{}, err
	}
	if len(sources) != 1 {
		return evidence.Source{}, ErrNotFound
	}
	source := sources[0]
	checked, err := evidence.NewSourcePolicy(s.gateway, s.origins, s.bindings).Validate(ctx, projectID, actorRef, []evidence.Source{source})
	if err != nil {
		s.recordSourceAudit(ctx, actor, projectID, "source.read", err)
		return evidence.Source{}, err
	}
	if len(checked.Unusable) > 0 {
		unusable := checked.Unusable[0]
		if unusable.AssetDeny != "" {
			s.recordSourceAudit(ctx, actor, projectID, "source.read", ErrSourceUnavailable)
			return evidence.Source{}, ErrSourceUnavailable
		}
		source.Status = unusable.Status
	}
	return source, nil
}

func (s *SourceService) RetrieveSources(
	ctx context.Context, actor Actor, projectID string, input RetrieveSourcesInput,
) ([]evidence.Source, error) {
	if err := s.projects.Authorize(ctx, actor, projectID, "read"); err != nil {
		return nil, err
	}
	input.Query = strings.TrimSpace(input.Query)
	hasAssetID := false
	for _, id := range input.AssetIDs {
		if strings.TrimSpace(id) != "" {
			hasAssetID = true
			break
		}
	}
	if input.Query == "" || !hasAssetID {
		return nil, ErrInvalidRequest
	}
	resolved, err := s.gateway.ResolveAllowed(ctx, projectID, evidenceActor(actor), input.AssetIDs)
	if err != nil {
		s.recordSourceAudit(ctx, actor, projectID, "source.retrieve", err)
		return nil, err
	}
	if len(resolved.Denied) > 0 {
		err := &DeniedAssetsError{Denied: resolved.Denied}
		s.recordSourceAudit(ctx, actor, projectID, "source.retrieve", err)
		return nil, err
	}
	if s.knowledge == nil {
		return nil, ErrSourceUnavailable
	}
	assetsByKnowledge := make(map[string]evidence.Asset, len(resolved.Allowed))
	assetsByKB := make(map[string][]string)
	for _, asset := range resolved.Allowed {
		assetsByKnowledge[asset.KnowledgeID] = asset
		scope, err := s.bindings.AssetScope(ctx, projectID, asset.ID)
		if err != nil {
			return nil, err
		}
		assetsByKB[scope.KnowledgeBaseID] = append(assetsByKB[scope.KnowledgeBaseID], asset.KnowledgeID)
	}
	resolver := evidence.NewSourceResolver(s.origins)
	result := make([]evidence.Source, 0)
	kbIDs := make([]string, 0, len(assetsByKB))
	for kbID := range assetsByKB {
		kbIDs = append(kbIDs, kbID)
	}
	sort.Strings(kbIDs)
	for _, kbID := range kbIDs {
		hits, err := s.knowledge.HybridSearch(ctx, kbID, types.SearchParams{
			QueryText: input.Query, MatchCount: 20, KnowledgeIDs: assetsByKB[kbID],
		})
		if err != nil {
			return nil, err
		}
		for _, hit := range hits {
			asset, ok := assetsByKnowledge[hit.KnowledgeID]
			if !ok {
				continue
			}
			sources, err := resolver.Resolve(ctx, asset, []*types.SearchResult{hit})
			if err != nil {
				return nil, err
			}
			result = append(result, sources...)
		}
	}
	return result, nil
}

func (s *SourceService) AccessStatus(ctx context.Context, actor Actor, projectID string) (AccessStatus, error) {
	if err := s.projects.Authorize(ctx, actor, projectID, "read"); err != nil {
		return AccessStatus{}, err
	}
	state := "available"
	assets, err := s.bindings.BoundAssets(ctx, projectID)
	if err != nil {
		state = "unknown"
	} else {
		ids := make([]string, 0, len(assets))
		for _, a := range assets {
			ids = append(ids, a.ID)
		}
		resolved, err := s.gateway.ResolveAllowed(ctx, projectID, evidenceActor(actor), ids)
		if err != nil {
			state = "unknown"
		} else if len(resolved.Denied) > 0 {
			state = "restricted"
		}
	}
	actions := []string{}
	if state == "restricted" {
		actions = []string{"restore_source_authorization", "create_clean_project"}
	}
	return AccessStatus{ProjectID: projectID, ContentAccess: state, RecoveryActions: actions, CanCreateProject: true}, nil
}

func evidenceActor(actor Actor) evidence.Actor {
	return evidence.Actor{UserID: actor.UserID, TenantID: strconv.FormatUint(actor.TenantID, 10)}
}

type kbReadChecker struct{ shares interfaces.KBShareService }

func (a kbReadChecker) CanReadKB(ctx context.Context, actor evidence.Actor, knowledgeBaseID string, ownerTenantID uint64) (bool, error) {
	caller := types.CallerFromContext(ctx)
	if caller.UserID != actor.UserID || strconv.FormatUint(caller.TenantID, 10) != actor.TenantID || a.shares == nil {
		return false, nil
	}
	return access.NewKBPermissions(ctx, a.shares).Check(knowledgeBaseID, ownerTenantID, types.OrgRoleViewer)
}

func (a kbReadChecker) canReadKnowledgeBase(ctx context.Context, actor Actor, kb *types.KnowledgeBase) (bool, error) {
	return a.CanReadKB(ctx, evidenceActor(actor), kb.ID, kb.TenantID)
}

// Context expansion is supplied by the source runtime; fake consumers may override it.
func (s *SourceService) GetSourceContext(context.Context, Actor, string, string) (map[string]any, error) {
	return nil, ErrSourceUnavailable
}

var _ SourceApplicationService = (*SourceService)(nil)
