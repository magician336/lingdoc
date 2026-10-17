package rulesetstore

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	"gorm.io/gorm"
)

var ErrRuleSetAccessDenied = errors.New("ruleset access denied")

// TenantMembershipAuthorizer rechecks the active tenant membership inside the
// RuleSet transaction. Reads are available to active members; governance writes
// are limited to tenant owners and admins.
type TenantMembershipAuthorizer struct{}

func (TenantMembershipAuthorizer) AuthorizeRuleSet(_ context.Context, tx *gorm.DB, actor Actor, action Action, _ *template.RuleSet) error {
	var membership struct {
		Role string `gorm:"column:role"`
	}
	err := tx.Table("tenant_members").
		Select("role").
		Where("tenant_id = ? AND user_id = ? AND status = ? AND deleted_at IS NULL", actor.TenantID, actor.UserID, "active").
		Take(&membership).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrRuleSetAccessDenied
	}
	if err != nil {
		return err
	}
	if action == ActionRead {
		return nil
	}
	if membership.Role != "owner" && membership.Role != "admin" {
		return ErrRuleSetAccessDenied
	}
	return nil
}
