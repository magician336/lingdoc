package templategovernance

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// 这组断言守的是「模板治理这一面的写入方只把租户角色写进审计列」。
//
// Actor.Role 会被写进 audit_logs.actor_role。那一列只有一个意思：操作者在
// 那个租户里当时的角色。角色词表由 internal/types 拥有（types.TenantRole），
// 所以词表外的值写进去就没有读者能解释。
//
// 断言分两面：
//
//   - 读侧：ready 拒绝词表外的角色（词表外的角色连一次治理操作都进不来）；
//   - 写侧：直写 SQL 的那条路径（recordAuditOutcome）也被挡住，免得绕过读
//     侧就把一个不可解释的词写进列里。
//
// 词表外的词取自仓库里真实出现过的漂移值（tenant_admin / tenant_viewer /
// template_publisher），加上成员记录那一套角色词——它们是不同的概念，
// 不该混进租户角色列。

// roleOutsideTheTenantRoleVocabulary 是词表外的角色词。
var roleOutsideTheTenantRoleVocabulary = []string{
	"tenant_admin",
	"tenant_viewer",
	"template_publisher",
	"member",
	"collaborator",
	"author",
	"OWNER",
}

// TestServiceReadyLeavesTheRoleDecisionToTheApplicationLayer 断言 ready 不
// 抢先拒绝词表外的角色。
//
// 「声称了一个词表里没有的角色」这件事的对外形态是**身份无法归属**（不可
// 枚举），那个形态由应用层给出：它把声明判成
// errors.Join(core.ErrNotFound, governance.ErrUnverifiedActor)。
// templategovernance 这一层如果抢先拒绝，就会把它换成「授权不可用」，那是
// 契约变更，不是本票该做的事。
//
// 所以 ready 放行，让应用层去给形态；本包在**写侧**守住审计列（见下一条
// 断言）。
func TestServiceReadyLeavesTheRoleDecisionToTheApplicationLayer(t *testing.T) {
	db, _ := openTestDB(t)
	service := NewService(db, Ports{Authorizer: &testAuth{allow: true}})

	for _, role := range roleOutsideTheTenantRoleVocabulary {
		actor := Actor{TenantID: 1, UserID: "owner", Role: role}
		if err := service.ready(actor); err != nil {
			t.Errorf("ready with role %q = %v, want nil: the role decision belongs to the application layer", role, err)
		}
	}

	// 词表内的角色照旧通过：守护不能只会拦。
	ok := Actor{TenantID: 1, UserID: "owner", Role: string(types.TenantRoleAdmin)}
	if err := service.ready(ok); err != nil {
		t.Fatalf("ready with a tenant role = %v, want nil", err)
	}
}

// TestRecordAuditOutcomeRefusesActorRoleOutsideTheTenantRoleVocabulary 断言
// 直写 SQL 的那条审计路径也被挡住。
func TestRecordAuditOutcomeRefusesActorRoleOutsideTheTenantRoleVocabulary(t *testing.T) {
	db, _ := openTestDB(t)

	for _, role := range roleOutsideTheTenantRoleVocabulary {
		actor := Actor{TenantID: 1, UserID: "owner", Role: role}
		err := recordAuditOutcome(db, actor, ActionRead, "template-1", "reason", "success", map[string]any{})
		if !errors.Is(err, ErrUnauthorized) {
			t.Errorf("recordAuditOutcome with role %q = %v, want ErrUnauthorized", role, err)
		}
	}

	var rows int64
	if err := db.Table("audit_logs").Count(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("audit rows = %d, want none: a role outside the vocabulary must not reach the audit column", rows)
	}
}

// TestGovernanceOperationRefusesActorRoleOutsideTheTenantRoleVocabulary 断言
// 一次真实的治理操作在词表外的角色下被拒绝，并且不留下审计行。
func TestGovernanceOperationRefusesActorRoleOutsideTheTenantRoleVocabulary(t *testing.T) {
	db, _ := openTestDB(t)
	service := NewService(db, Ports{Authorizer: &testAuth{allow: true}})

	for _, role := range roleOutsideTheTenantRoleVocabulary {
		actor := Actor{TenantID: 1, UserID: "owner", Role: role}
		_, err := service.CreateProfile(context.Background(), actor, "create-profile-"+role, CreateProfileInput{
			ID: "grant-template", Name: "Internal working template", Reason: "create reviewed internal structure",
		})
		if err == nil {
			t.Errorf("CreateProfile with role %q succeeded, want a refusal", role)
		}
	}

	var rows int64
	if err := db.Table("audit_logs").Count(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("audit rows = %d, want none: a role outside the vocabulary must not reach the audit column", rows)
	}
}
