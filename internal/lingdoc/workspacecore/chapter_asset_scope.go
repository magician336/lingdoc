package workspacecore

import (
	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/types"
)

// chapterCitesAsset derives the association from the current persisted formal
// chapter and tenant/project-bound asset, within the authorization transaction.
// It grants no new research scope and does not replace the source gateway's
// subsequent checks of current asset availability, permissions and versions.
func chapterCitesAsset(tx Transaction, tenantID uint64, projectID, chapterID, assetID string) (bool, error) {
	chapter, err := tx.Chapter(projectID, chapterID)
	if err != nil {
		return false, err
	}
	if chapter.CurrentVersionID == nil || len(chapter.SourceIDs) == 0 {
		return false, nil
	}
	db, err := TransactionDatabase(tx)
	if err != nil {
		return false, err
	}
	var count int64
	err = db.Model(&types.Chunk{}).
		Where("chunks.tenant_id = ? AND chunks.id IN ?", tenantID, chapter.SourceIDs).
		Where("EXISTS (?)", db.Model(&evidence.ProjectAsset{}).Select("1").
			Where("tenant_id = ? AND project_id = ? AND id = ?", tenantID, projectID, assetID).
			Where("knowledge_id = chunks.knowledge_id")).
		Count(&count).Error
	return count > 0, err
}
