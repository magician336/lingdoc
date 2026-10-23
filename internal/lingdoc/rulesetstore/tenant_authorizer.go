package rulesetstore

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/clienterror"
	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

// ErrRuleSetAccessDenied is a member of the tenant that may not perform this
// rule set operation. It states its own outward form.
var ErrRuleSetAccessDenied = clienterror.New(clienterror.Form{Status: 403, Code: "forbidden", Message: "当前租户成员无权执行此规则集操作。", Retryable: false}, "ruleset access denied")

// TenantMembershipAuthorizer rechecks the active tenant membership inside the
// RuleSet transaction. Reads are available to active members; governance writes
// are limited to tenant owners and admins.
//
// The membership role it reads is a tenant role, so it is judged with the
// tenant role vocabulary owned by internal/types rather than with a second
// list of role words spelled out here.
type TenantMembershipAuthorizer struct{}

func (TenantMembershipAuthorizer) AuthorizeRuleSet(_ context.Context, tx *gorm.DB, actor Actor, action Action, _ *template.RuleSet) error {
	var membership struct {
		Role types.TenantRole `gorm:"column:role"`
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
	if !membership.Role.IsValid() ||
		(membership.Role != types.TenantRoleOwner && membership.Role != types.TenantRoleAdmin) {
		return ErrRuleSetAccessDenied
	}
	return nil
}
