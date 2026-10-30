package workspacecore

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm/clause"
)

// lockAcademicEvidenceChapters stabilizes chapter citations used by the
// research resource fallback. Caller holds the project lock; sorted IDs give
// all confirmation transactions the same lock order.
func lockAcademicEvidenceChapters(tx Transaction, projectID string, chapterIDs []string) error {
	db, err := TransactionDatabase(tx)
	if err != nil {
		return ErrDependencyUnavailable
	}
	for _, chapterID := range sortedUniqueIDs(chapterIDs) {
		var row chapterRow
		if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("id = ? AND project_id = ?", chapterID, projectID).Take(&row).Error; err != nil {
			return ErrNotFound
		}
	}
	return nil
}

// authorizeAcademicEvidenceSourceInTransaction matches the resource-batch
// decision, without opening another transaction while locks are held.
func (s *Service) authorizeAcademicEvidenceSourceInTransaction(ctx context.Context, tx Transaction, actor Actor, projectID, chapterID, assetID string) error {
	ref, err := NewAssetResourceRefForChapter(assetID, chapterID)
	if err != nil {
		return err
	}
	chapter, err := tx.Chapter(projectID, chapterID)
	if err != nil {
		return err
	}
	_, err = s.authorizeProjectWithoutShadow(ctx, tx, authorizationRequest(actor, projectID, AuthorizationResearch, ref.AuthorizationResource()))
	if err == nil || !errors.Is(err, ErrNotFound) {
		return err
	}
	if _, chapterErr := s.authorizeProjectWithoutShadow(ctx, tx, authorizationRequest(actor, projectID, AuthorizationResearch, chapterID)); chapterErr != nil {
		return chapterErr
	}
	// The fallback derives access from the chapter's cited chunks. Hold their
	// current bindings until confirmation commits, just as the chapter row is
	// held; otherwise a concurrent chunk move could revoke the fallback.
	if len(chapter.SourceIDs) > 0 {
		db, e := TransactionDatabase(tx)
		if e != nil {
			return ErrDependencyUnavailable
		}
		var rows []types.Chunk
		if e = db.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("tenant_id = ? AND id IN ?", actor.TenantID, sortedUniqueIDs(chapter.SourceIDs)).Order("id").Find(&rows).Error; e != nil {
			return e
		}
	}
	cited, err := chapterCitesAsset(tx, actor.TenantID, projectID, chapterID, assetID)
	if err != nil {
		return err
	}
	if !cited {
		return ErrSourceUnavailable
	}
	return nil
}
