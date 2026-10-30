package workspacecore

import "gorm.io/gorm/clause"

// Lock before authorization/ledger reads so concurrent windows and first
// reservations serialize on an existing project, not a possibly absent row.
func (t gormTransaction) LockResearchQuotaProject(tenantID uint64, projectID string) error {
	var row projectRow
	return storageError(t.db.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("id = ? AND tenant_id = ?", projectID, tenantID).Take(&row).Error)
}
