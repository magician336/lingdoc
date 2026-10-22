package workspace

import (
	"context"
	"errors"
	"github.com/Tencent/WeKnora/internal/evidence"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"gorm.io/gorm"
	"slices"
)

// This native composition check only narrows the existing authorization. A
// chunk whose parent KB/knowledge was removed is not a live source even when a
// previously allowed gateway or project binding can still resolve its bytes.
type liveGORMSourceGateway struct {
	db   *gorm.DB
	base evidence.AssetGateway
}

func (g liveGORMSourceGateway) ResolveAllowed(ctx context.Context, pid string, actor evidence.Actor, ids []string) (*evidence.ResolveResult, error) {
	if g.db == nil || g.base == nil {
		return nil, core.ErrDependencyUnavailable
	}
	result, err := g.base.ResolveAllowed(ctx, pid, actor, ids)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, core.ErrDependencyUnavailable
	}
	out := &evidence.ResolveResult{Requested: slices.Clone(result.Requested), Denied: slices.Clone(result.Denied), Allowed: []evidence.Asset{}}
	var projects int64
	if err := g.db.WithContext(ctx).Table("lingdoc_projects").Where("id=? AND tenant_id=?", pid, actor.TenantID).Count(&projects).Error; err != nil {
		return nil, err
	}
	if projects != 1 {
		for _, asset := range result.Allowed {
			out.Denied = append(out.Denied, evidence.DeniedAsset{AssetID: asset.ID, Reason: evidence.DenyNotAuthorized})
		}
		return out, nil
	}
	for _, asset := range result.Allowed {
		var binding evidence.ProjectAsset
		err = g.db.WithContext(ctx).Where("id=? AND project_id=? AND deleted_at IS NULL", asset.ID, pid).Take(&binding).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		// Binding.TenantID is the trusted source owner, not the project tenant.
		// The base gateway has already rechecked current KB sharing for this actor;
		// the project, binding, knowledge and KB identities remain exact here.
		if err != nil || binding.TenantID == 0 || binding.KnowledgeBaseID == "" || binding.AssetRevision != int64(asset.AssetRevision) {
			out.Denied = append(out.Denied, evidence.DeniedAsset{AssetID: asset.ID, Reason: evidence.DenyNotAuthorized})
			continue
		}
		var revision evidence.ProjectAssetRevision
		err = g.db.WithContext(ctx).Where("asset_id=? AND revision_no=? AND weknora_knowledge_id=?", asset.ID, asset.AssetRevision, asset.KnowledgeID).Take(&revision).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		if err != nil {
			out.Denied = append(out.Denied, evidence.DeniedAsset{AssetID: asset.ID, Reason: evidence.DenyNotAuthorized})
			continue
		}
		var knowledge struct{ ID, KnowledgeBaseID string }
		err = g.db.WithContext(ctx).Table("knowledges").Select("id,knowledge_base_id").Where("id=? AND tenant_id=? AND deleted_at IS NULL", asset.KnowledgeID, binding.TenantID).Take(&knowledge).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		alive := err == nil && knowledge.KnowledgeBaseID == binding.KnowledgeBaseID
		if alive {
			var count int64
			err = g.db.WithContext(ctx).Table("knowledge_bases").Where("id=? AND tenant_id=? AND deleted_at IS NULL", knowledge.KnowledgeBaseID, binding.TenantID).Count(&count).Error
			if err != nil {
				return nil, err
			}
			alive = count == 1
		}
		if !alive {
			out.Denied = append(out.Denied, evidence.DeniedAsset{AssetID: asset.ID, Reason: evidence.DenyNotAuthorized})
			continue
		}
		out.Allowed = append(out.Allowed, asset)
	}
	return out, nil
}
