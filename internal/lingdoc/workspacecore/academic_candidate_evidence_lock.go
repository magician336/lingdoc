package workspacecore

import (
	"context"
	"slices"
	"sort"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm/clause"
)

// lockCandidateAcademicEvidence keeps the native decisions and immutable
// snapshot rows stable until the parent candidate-adoption transaction ends.
// It never reads an external object store or opens another transaction.
func (s *Service) lockCandidateAcademicEvidence(ctx context.Context, tx Transaction, actor Actor, projectID, chapterID string, selected EvidenceSelection) (Project, error) {
	if err := lockAcademicActorRows(tx, actor, projectID); err != nil {
		return Project{}, err
	}
	if err := lockAcademicEvidenceProject(tx, actor, projectID); err != nil {
		return Project{}, err
	}
	if err := lockAcademicEvidenceChapters(tx, projectID, []string{chapterID}); err != nil {
		return Project{}, err
	}
	p, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationWrite, chapterID))
	if err != nil {
		return Project{}, err
	}
	db, err := TransactionDatabase(tx)
	if err != nil {
		return Project{}, ErrDependencyUnavailable
	}
	type objectKey struct{ kind, id string }
	objects := []objectKey{}
	refs := []string{}
	for _, card := range selected.Cards {
		if card.Confirmation == nil {
			continue
		}
		objects = append(objects, objectKey{EvidenceCardKind, card.ID})
		if card.ClaimRef != nil {
			objects = append(objects, objectKey{ResearchClaimKind, card.ClaimRef.ID})
		}
		for _, anchor := range card.AnchorRefs {
			objects = append(objects, objectKey{SourceAnchorKind, anchor.ID})
		}
		for _, snapshot := range card.Confirmation.AcademicSnapshots {
			refs = append(refs, snapshot.RawRef.SHA256, snapshot.TextRef.SHA256)
			for _, binding := range snapshot.Associations {
				objects = append(objects, objectKey{AcademicSourceAssociationKind, binding.AssociationRef.ID}, objectKey{ImportCandidateKind, binding.CandidateRef.ID})
			}
			if err = validateAssociationSourceInTransaction(ctx, tx, actor, p, ResearchSource{SourceID: snapshot.SourceID, AssetID: snapshot.AssetID, AssetRevision: snapshot.AssetRevision, KnowledgeID: snapshot.KnowledgeID, QuotedTextHash: snapshot.TextRef.SHA256}); err != nil {
				return Project{}, err
			}
			// These independent rows are also part of the native proof; locking the
			// chunk alone does not prevent their concurrent update or deletion.
			var revision evidence.ProjectAssetRevision
			if err = db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("asset_id = ? AND revision_no = ?", snapshot.AssetID, snapshot.AssetRevision).Take(&revision).Error; err != nil {
				return Project{}, ErrEvidenceIncomplete
			}
			var origin types.ChunkOriginSHA256
			if err = db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("chunk_id = ? AND tenant_id = ? AND knowledge_id = ?", snapshot.SourceID, actor.TenantID, snapshot.KnowledgeID).Take(&origin).Error; err != nil {
				return Project{}, ErrEvidenceIncomplete
			}
			if snapshot.Page > 0 {
				var page types.ChunkPDFPage
				if err = db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("chunk_id = ? AND tenant_id = ? AND knowledge_id = ?", snapshot.SourceID, actor.TenantID, snapshot.KnowledgeID).Take(&page).Error; err != nil {
					return Project{}, ErrEvidenceIncomplete
				}
			}
			if err = s.authorizeAcademicEvidenceSourceInTransaction(ctx, tx, actor, projectID, chapterID, snapshot.AssetID); err != nil {
				return Project{}, err
			}
		}
	}
	sort.Slice(objects, func(i, j int) bool {
		if objects[i].kind == objects[j].kind {
			return objects[i].id < objects[j].id
		}
		return objects[i].kind < objects[j].kind
	})
	objects = slices.Compact(objects)
	for _, object := range objects {
		var row objectRow
		if err = db.Clauses(clause.Locking{Strength: "UPDATE"}).Select("project_id", "kind", "id").Where("project_id = ? AND kind = ? AND id = ?", projectID, object.kind, object.id).Take(&row).Error; err != nil {
			return Project{}, ErrEvidenceIncomplete
		}
	}
	refs = sortedUniqueIDs(refs)
	for _, sha := range refs {
		var row struct{ SHA256 string }
		if err = db.Clauses(clause.Locking{Strength: "UPDATE"}).Table("lingdoc_academic_original_snapshots").Select("sha256").Where("tenant_id = ? AND project_id = ? AND sha256 = ?", actor.TenantID, projectID, sha).Take(&row).Error; err != nil {
			return Project{}, ErrEvidenceIncomplete
		}
	}
	return s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationWrite, chapterID))
}
