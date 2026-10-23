package rulesetstore

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	"github.com/Tencent/WeKnora/internal/types"
)

// 这组断言守的是「规则集这一面的写入方只把租户角色写进审计列」。
//
// Actor.Role 会被写进 audit_logs.actor_role。那一列只有一个意思：操作者在
// 那个租户里当时的角色。角色词表由 internal/types 拥有（types.TenantRole），
// 所以词表外的值写进去就没有读者能解释——要么是写错了，要么是有人另抄了
// 一份词表。
//
// 断言分两面：
//
//   - 读侧：词表外的角色进不来（validateActor / GetVersionForBinding）；
//   - 写侧：直写 SQL 的那条路径也被挡住（recordAudit），免得绕过读侧就把
//     一个不可解释的词写进列里。
//
// 词表外的词取自仓库里真实出现过的漂移值（tenant_admin / tenant_viewer /
// template_publisher），加上成员记录那一套角色词——它们是不同的概念，
// 不该混进租户角色列。

// roleOutsideTheTenantRoleVocabulary 是词表外的角色词。
//
// 每一个都曾经在仓库里被当成角色用过，或者属于别的角色词表（成员的项目角色、
// 治理角色、职能角色）。它们都不是租户角色。
var roleOutsideTheTenantRoleVocabulary = []string{
	"tenant_admin",
	"tenant_viewer",
	"template_publisher",
	"member",
	"collaborator",
	"author",
	"OWNER",
}

// TestStoreRefusesActorRoleOutsideTheTenantRoleVocabulary 断言词表外的角色进
// 不来：读侧拒绝，并且不留下任何审计行。
func TestStoreRefusesActorRoleOutsideTheTenantRoleVocabulary(t *testing.T) {
	db := openStoreTestDB(t, true)
	store := NewStore(db, testAuthorizer{allow: true})
	ctx := context.Background()
	draft := testDraft("v1")

	for _, role := range roleOutsideTheTenantRoleVocabulary {
		actor := Actor{TenantID: 1, UserID: "admin-1", Role: role}
		if err := store.CreateDraft(ctx, actor, draft); !errors.Is(err, ErrActorRoleInvalid) {
			t.Errorf("CreateDraft with role %q = %v, want ErrActorRoleInvalid", role, err)
		}
		if _, err := store.GetVersion(ctx, actor, draft.ID, draft.Version); !errors.Is(err, ErrActorRoleInvalid) {
			t.Errorf("GetVersion with role %q = %v, want ErrActorRoleInvalid", role, err)
		}
		if _, err := store.ListVersions(ctx, actor, draft.ID); !errors.Is(err, ErrActorRoleInvalid) {
			t.Errorf("ListVersions with role %q = %v, want ErrActorRoleInvalid", role, err)
		}
		if _, err := store.Publish(ctx, actor, draft.ID, draft.Version); !errors.Is(err, ErrActorRoleInvalid) {
			t.Errorf("Publish with role %q = %v, want ErrActorRoleInvalid", role, err)
		}
		if _, err := store.GetVersionForBinding(ctx, db, actor, draft.ID, draft.Version); !errors.Is(err, ErrActorRoleInvalid) {
			t.Errorf("GetVersionForBinding with role %q = %v, want ErrActorRoleInvalid", role, err)
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

// TestRecordAuditRefusesActorRoleOutsideTheTenantRoleVocabulary 断言直写 SQL
// 的那条审计路径也被挡住。
//
// recordAudit 是最后一道 INSERT。读侧已经有校验，但审计行也可能从别的路径
// 被要求写入；这一层保证「写不进审计列」这件事不依赖调用方记得先校验。
func TestRecordAuditRefusesActorRoleOutsideTheTenantRoleVocabulary(t *testing.T) {
	db := openStoreTestDB(t, true)
	draft := testDraft("v1")

	for _, role := range roleOutsideTheTenantRoleVocabulary {
		actor := Actor{TenantID: 1, UserID: "admin-1", Role: role}
		err := recordAudit(db, actor, draft, ActionCreateDraft, "", template.RuleSetDraft)
		if !errors.Is(err, ErrActorRoleInvalid) {
			t.Errorf("recordAudit with role %q = %v, want ErrActorRoleInvalid", role, err)
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

// TestRecordAuditKeepsTheTenantRoleItWasGiven 断言词表内的角色照旧写进去，
// 而且写进去的就是那个角色。
//
// 守护不能只会拦：词表内的角色必须照常落库，否则这条约束就变成了「审计列
// 永远是空的」这种另一种坏。
func TestRecordAuditKeepsTheTenantRoleItWasGiven(t *testing.T) {
	db := openStoreTestDB(t, true)
	draft := testDraft("v1")
	actor := Actor{TenantID: 1, UserID: "admin-1", Role: string(types.TenantRoleAdmin)}

	if err := recordAudit(db, actor, draft, ActionCreateDraft, "", template.RuleSetDraft); err != nil {
		t.Fatal(err)
	}

	var role string
	if err := db.Table("audit_logs").Select("actor_role").Take(&role).Error; err != nil {
		t.Fatal(err)
	}
	if role != string(types.TenantRoleAdmin) {
		t.Fatalf("audit actor_role = %q, want %q", role, string(types.TenantRoleAdmin))
	}
}
