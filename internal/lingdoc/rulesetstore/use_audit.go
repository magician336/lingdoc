package rulesetstore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

var ErrInvalidRuleSetUseAudit = errors.New("invalid ruleset use audit event")

type ruleSetUseAuditRow struct {
	ID                   string    `gorm:"primaryKey;size:36"`
	TenantID             uint64    `gorm:"not null;index:idx_lingdoc_ruleset_use_audit_scope,priority:1"`
	ActorID              string    `gorm:"not null;size:64"`
	ActorRole            string    `gorm:"not null;size:32"`
	ProjectID            string    `gorm:"not null;size:64;index:idx_lingdoc_ruleset_use_audit_scope,priority:2"`
	Action               string    `gorm:"not null;size:32"`
	RuleSetID            string    `gorm:"column:ruleset_id;not null;size:64;index:idx_lingdoc_ruleset_use_audit_version,priority:2"`
	RuleSetVersion       string    `gorm:"column:ruleset_version;not null;size:64;index:idx_lingdoc_ruleset_use_audit_version,priority:3"`
	RuleSetHash          string    `gorm:"column:ruleset_hash;not null;size:64"`
	EffectiveRulesetHash string    `gorm:"not null;size:64"`
	TemplateID           string    `gorm:"not null;size:80"`
	TemplateVersion      string    `gorm:"not null;size:40"`
	TemplateCopyID       string    `gorm:"not null;size:64"`
	TemplateCopyVersion  int64     `gorm:"not null"`
	ProjectVersion       int64     `gorm:"not null"`
	ResourceID           string    `gorm:"not null;size:80"`
	Outcome              string    `gorm:"not null;size:24"`
	CreatedAt            time.Time `gorm:"not null;index:idx_lingdoc_ruleset_use_audit_scope,priority:3"`
}

func (ruleSetUseAuditRow) TableName() string { return "lingdoc_ruleset_use_audits" }

// RecordRuleSetUse validates the exact persisted source version and appends
// its use event in a transaction. A use of an unknown or hash-mismatched
// RuleSet is never recorded as if it were valid.
func (s *Store) RecordRuleSetUse(ctx context.Context, event template.RuleSetUseAuditEvent) error {
	if err := s.validateActor(Actor{TenantID: event.TenantID, UserID: event.ActorID, Role: event.ActorRole}); err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		set, err := loadVersion(tx, event.TenantID, event.RuleSetID, event.RuleSetVersion)
		if err != nil {
			return err
		}
		ruleSet, err := decodeRuleSet(set)
		if err != nil {
			return err
		}
		if ruleSet.RulesetHash == "" || ruleSet.RulesetHash != event.RuleSetHash {
			return fmt.Errorf("%w: source RuleSet hash mismatch", ErrInvalidRuleSetUseAudit)
		}
		if err := s.authorize(ctx, tx, Actor{TenantID: event.TenantID, UserID: event.ActorID, Role: event.ActorRole}, ActionRead, &ruleSet); err != nil {
			return err
		}
		return RecordRuleSetUseInTransaction(tx, event)
	})
}

// RecordRuleSetUseInTransaction lets snapshot/export adapters commit the use
// audit and governed artifact atomically in the same database transaction.
func RecordRuleSetUseInTransaction(tx *gorm.DB, event template.RuleSetUseAuditEvent) error {
	if tx == nil {
		return errors.New("ruleset use audit transaction is unavailable")
	}
	event, err := normalizeRuleSetUseAudit(event)
	if err != nil {
		return err
	}
	row := ruleSetUseAuditRow{
		ID: event.ID, TenantID: event.TenantID, ActorID: event.ActorID, ActorRole: event.ActorRole,
		ProjectID: event.ProjectID, Action: event.Action, RuleSetID: event.RuleSetID,
		RuleSetVersion: event.RuleSetVersion, RuleSetHash: event.RuleSetHash,
		EffectiveRulesetHash: event.EffectiveRulesetHash, TemplateID: event.TemplateID,
		TemplateVersion: event.TemplateVersion, TemplateCopyID: event.TemplateCopyID,
		TemplateCopyVersion: event.TemplateCopyVersion, ProjectVersion: event.ProjectVersion,
		ResourceID: event.ResourceID, Outcome: event.Outcome, CreatedAt: event.CreatedAt.UTC(),
	}
	if err := tx.Create(&row).Error; err != nil {
		return fmt.Errorf("persist ruleset use audit: %w", err)
	}
	return nil
}

func (s *Store) ListRuleSetUseAudits(ctx context.Context, actor Actor, projectID string) ([]template.RuleSetUseAuditEvent, error) {
	if err := s.validateActor(actor); err != nil {
		return nil, err
	}
	if strings.TrimSpace(projectID) == "" {
		return nil, ErrInvalidRuleSetUseAudit
	}
	var rows []ruleSetUseAuditRow
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var versions []ruleSetUseAuditRow
		if err := tx.Where("tenant_id = ? AND project_id = ?", actor.TenantID, projectID).
			Order("created_at DESC, id DESC").Find(&versions).Error; err != nil {
			return err
		}
		for _, row := range versions {
			version, err := loadVersion(tx, row.TenantID, row.RuleSetID, row.RuleSetVersion)
			if err != nil {
				return err
			}
			set, err := decodeRuleSet(version)
			if err != nil {
				return err
			}
			if err := s.authorize(ctx, tx, actor, ActionRead, &set); err != nil {
				return err
			}
			rows = append(rows, row)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	events := make([]template.RuleSetUseAuditEvent, 0, len(rows))
	for _, row := range rows {
		events = append(events, template.RuleSetUseAuditEvent{
			ID: row.ID, TenantID: row.TenantID, ActorID: row.ActorID, ActorRole: row.ActorRole,
			ProjectID: row.ProjectID, Action: row.Action, RuleSetID: row.RuleSetID,
			RuleSetVersion: row.RuleSetVersion, RuleSetHash: row.RuleSetHash,
			EffectiveRulesetHash: row.EffectiveRulesetHash, TemplateID: row.TemplateID,
			TemplateVersion: row.TemplateVersion, TemplateCopyID: row.TemplateCopyID,
			TemplateCopyVersion: row.TemplateCopyVersion, ProjectVersion: row.ProjectVersion,
			ResourceID: row.ResourceID, Outcome: row.Outcome, CreatedAt: row.CreatedAt.UTC(),
		})
	}
	return events, nil
}

func normalizeRuleSetUseAudit(event template.RuleSetUseAuditEvent) (template.RuleSetUseAuditEvent, error) {
	if event.ID == "" {
		event.ID = uuid.NewString()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	if event.TenantID == 0 || strings.TrimSpace(event.ActorID) == "" || strings.TrimSpace(event.ActorRole) == "" ||
		strings.TrimSpace(event.ProjectID) == "" || !validUseAction(event.Action) ||
		strings.TrimSpace(event.RuleSetID) == "" || strings.TrimSpace(event.RuleSetVersion) == "" || len(event.RuleSetHash) != 64 ||
		len(event.EffectiveRulesetHash) != 64 || strings.TrimSpace(event.TemplateID) == "" || strings.TrimSpace(event.TemplateVersion) == "" ||
		strings.TrimSpace(event.Outcome) == "" {
		return template.RuleSetUseAuditEvent{}, ErrInvalidRuleSetUseAudit
	}
	return event, nil
}

func validUseAction(action string) bool {
	switch action {
	case "check", "preflight", "release_snapshot", "export":
		return true
	default:
		return false
	}
}
