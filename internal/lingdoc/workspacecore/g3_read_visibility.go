package workspacecore

import "context"

// Impact history is retained in storage. Responses include only tasks whose
// referenced objects and sources are currently readable by this caller.
func (s *Service) visibleImpactTasks(ctx context.Context, actor Actor, p Project, tasks []ImpactTask) ([]ImpactTask, error) {
	result := make([]ImpactTask, 0, len(tasks))
	for _, task := range tasks {
		var ids []string
		err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
			current, err := s.authorizeProject(ctx, tx, actor, p.ID, "read:"+task.Target.ID)
			if err != nil {
				return err
			}
			refs := append([]ObjectVersionRef{task.Target}, task.ClosureEvidence...)
			ids, err = impactSourceIDsForRefs(tx, current, refs)
			return err
		})
		if err != nil {
			continue
		}
		if err := s.checkSources(ctx, actor, p.ID, ids); err != nil {
			continue
		}
		result = append(result, task)
	}
	return result, nil
}
