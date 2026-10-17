package workspacecore

import "context"

// Discarded drafts are not silently returned in the ordinary project list.
// The owner can find their own recoverable drafts without exposing conditions.
func (s *Service) ListDiscardedProjects(ctx context.Context, a Actor) ([]Project, error) {
	result := []Project{}
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if err := s.authorizeTenant(ctx, tx, a, "read"); err != nil {
			return err
		}
		db, err := TransactionDatabase(tx)
		if err != nil {
			return err
		}
		var ids []string
		if err := db.Table("lingdoc_projects AS p").Select("p.id").Joins("JOIN lingdoc_members m ON m.project_id=p.id").Where("p.tenant_id=? AND m.user_id=? AND m.role='owner' AND p.discarded_at IS NOT NULL", a.TenantID, a.UserID).Order("p.created_at DESC,p.id").Limit(50).Scan(&ids).Error; err != nil {
			return err
		}
		for _, id := range ids {
			p, err := s.authorizeProject(ctx, tx, a, id, "read")
			if err != nil {
				continue
			}
			p.Spec = map[string]string{}
			p.SpecFields = map[string]SpecField{}
			result = append(result, p)
		}
		return nil
	})
	return result, err
}
