package templategovernance

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

const (
	defaultAuditHistoryLimit = 50
	maxAuditHistoryLimit     = 100
)

// ListAuditHistory returns append-only catalog lifecycle events for the
// current tenant. Only current catalog managers may read the full audit
// details; authorization and the query share the same transaction.
func (s *Service) ListAuditHistory(ctx context.Context, actor Actor, templateID string, filter AuditHistoryFilter) (AuditHistoryPage, error) {
	if err := s.ready(actor); err != nil {
		return AuditHistoryPage{}, err
	}
	if !identifier.MatchString(templateID) || filter.Limit < 0 || filter.Limit > maxAuditHistoryLimit {
		return AuditHistoryPage{}, ErrInvalidRequest
	}
	limit := filter.Limit
	if limit == 0 {
		limit = defaultAuditHistoryLimit
	}

	page := AuditHistoryPage{Events: make([]AuditEvent, 0, limit)}
	err := s.withTx(ctx, func(tx *gorm.DB) error {
		if err := s.ports.Authorizer.AuthorizeTemplate(ctx, tx, actor, ActionRead, templateID); err != nil {
			return err
		}
		canManage, err := s.ports.Authorizer.CanManageTemplateCatalog(ctx, tx, actor)
		if err != nil {
			return err
		}
		if !canManage {
			return ErrAuditHistoryForbidden
		}
		if _, err := loadProfile(tx, actor, templateID); err != nil {
			return err
		}

		actions := []string{
			"lingdoc.template.create_profile", "lingdoc.template.save_draft",
			"lingdoc.template.publish", "lingdoc.template.withdraw",
			"lingdoc.template.recall", "lingdoc.template.copy",
			// Replay fixture registration is a delivered governance action: its
			// success and failure receipts must be readable here, not only in
			// storage tests (Spec #768 D10).
			"lingdoc.template.register_fixture",
		}
		detailsTarget := "json_extract(details, '$.target_ref')"
		// Ruleset-path registration pins the replay base's version, so its
		// receipts belong to the base template's history; the details carry
		// the replay base reference (D10's dual reference) and this filter
		// matches on it.
		replayBaseTarget := "json_extract(details, '$.details.replay_base.template_id')"
		if tx.Name() == "postgres" {
			// The application table uses JSONB, while legacy/test audit tables
			// may expose the same serialized details as text. Cast explicitly so
			// both PostgreSQL shapes read consistently.
			detailsTarget = "CAST(details AS jsonb) ->> 'target_ref'"
			replayBaseTarget = "CAST(details AS jsonb) -> 'details' -> 'replay_base' ->> 'template_id'"
		}
		prefix := templateID + "/"
		q := tx.Table("audit_logs").
			Where("tenant_id=? AND scope_type=? AND action IN ?", actor.TenantID, "lingdoc_template", actions).
			Where("(scope_id=? OR substr(scope_id,1,?)=? OR target_id=? OR substr(target_id,1,?)=? OR "+detailsTarget+"=? OR substr("+detailsTarget+",1,?)=? OR "+replayBaseTarget+"=?)",
				templateID, len(prefix), prefix,
				templateID, len(prefix), prefix,
				templateID, len(prefix), prefix,
				templateID)
		if filter.AfterID > 0 {
			q = q.Where("id < ?", filter.AfterID)
		}
		var rows []auditHistoryRow
		if err := q.Order("id DESC").Limit(limit + 1).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) > limit {
			rows = rows[:limit]
			page.NextAfterID = rows[len(rows)-1].ID
		}
		for _, row := range rows {
			page.Events = append(page.Events, AuditEvent{
				ID: row.ID, ActorUserID: row.ActorUserID, ActorRole: row.ActorRole,
				Action: row.Action, TargetType: row.TargetType, TargetID: row.TargetID,
				Outcome: row.Outcome, Details: json.RawMessage(row.Details), CreatedAt: row.CreatedAt,
			})
		}
		return nil
	})
	return page, err
}

type auditHistoryRow struct {
	ID          uint64
	ActorUserID string
	ActorRole   string
	Action      string
	TargetType  string
	TargetID    string
	Outcome     string
	Details     types.JSON
	CreatedAt   time.Time
}
