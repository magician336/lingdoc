package rulesetstore

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	"github.com/Tencent/WeKnora/internal/types"
)

// #193 残留②切片的持久层补强:Store.Recall 此前只有 HTTP stub 与 template 域
// 函数覆盖,store 级的生命周期转换、审计写入与状态机非法转换没有持久层测试。
// recall 是比 withdraw 更强的安全停(RecordRuleSetUseInTransaction 会拒
// recalled 规则集的使用审计),它写进 audit_logs 的行必须存在且状态机必须拒绝
// 非法转换。
func TestStoreRecallLifecyclePersistsAuditAndRefusesIllegalTransitions(t *testing.T) {
	db := openStoreTestDB(t, true)
	store := NewStore(db, testAuthorizer{allow: true})
	ctx := context.Background()
	actor := Actor{TenantID: 1, UserID: "admin-1", Role: string(types.TenantRoleAdmin)}
	draft := testDraft("v-recall")

	if err := store.CreateDraft(ctx, actor, draft); err != nil {
		t.Fatal(err)
	}
	published, err := store.Publish(ctx, actor, draft.ID, draft.Version)
	if err != nil {
		t.Fatal(err)
	}

	// draft 态不能直接 recall:recall 只作用于已发布的版本。用第二个 draft
	// 版本验证(主版本已 publish,recall 在它上面成功是后续断言的前提)。
	draft2 := testDraft("v-recall-draft2")
	if err := store.CreateDraft(ctx, actor, draft2); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Recall(ctx, actor, draft2.ID, draft2.Version); !errors.Is(err, template.ErrRuleSetNotPublished) {
		t.Fatalf("recall on a draft = %v, want ErrRuleSetNotPublished", err)
	}

	recalled, err := store.Recall(ctx, actor, draft.ID, draft.Version)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if recalled.State != template.RuleSetRecalled {
		t.Fatalf("recalled state = %q, want %q", recalled.State, template.RuleSetRecalled)
	}
	// recall 是更强的安全停,但不改历史绑定哈希——旧快照的重放依据保持原样。
	if recalled.RulesetHash != published.RulesetHash {
		t.Fatalf("recall changed the historical binding hash: %+v", recalled)
	}

	// 持久化读回:recalled 状态与历史哈希都已落库。
	stored, err := store.GetVersion(ctx, actor, draft.ID, draft.Version)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != template.RuleSetRecalled || stored.RulesetHash != published.RulesetHash {
		t.Fatalf("historical read-back after recall = %+v", stored)
	}

	// recalled 之后不能再 publish/withdraw/recall:状态机只出不入。
	if _, err := store.Publish(ctx, actor, draft.ID, draft.Version); !errors.Is(err, template.ErrRuleSetNotDraft) {
		t.Fatalf("publishing a recalled version = %v, want ErrRuleSetNotDraft", err)
	}
	if _, err := store.Withdraw(ctx, actor, draft.ID, draft.Version); !errors.Is(err, template.ErrRuleSetNotPublished) {
		t.Fatalf("withdrawing a recalled version = %v, want ErrRuleSetNotPublished", err)
	}
	if _, err := store.Recall(ctx, actor, draft.ID, draft.Version); !errors.Is(err, template.ErrRuleSetNotPublished) {
		t.Fatalf("recalling a recalled version = %v, want ErrRuleSetNotPublished", err)
	}

	// 审计链:两个 draft 各一条 create、主版本 publish 与 recall 各一条——
	// recall 是强安全停,必须留痕;被拒的 draft 态 recall 不写审计。
	var audits int64
	if err := db.Table("audit_logs").Where("tenant_id = ? AND scope_type = ? AND scope_id = ?", actor.TenantID, "lingdoc_ruleset", draft.ID).Count(&audits).Error; err != nil {
		t.Fatal(err)
	}
	if audits != 4 {
		t.Fatalf("audit records = %d, want create(v1)+publish+create(v2)+recall", audits)
	}
	var recallAudits int64
	if err := db.Table("audit_logs").Where("tenant_id = ? AND scope_type = ? AND scope_id = ? AND action = ?", actor.TenantID, "lingdoc_ruleset", draft.ID, "lingdoc.ruleset.recall").Count(&recallAudits).Error; err != nil {
		t.Fatal(err)
	}
	if recallAudits != 1 {
		t.Fatalf("recall audit records = %d, want exactly one", recallAudits)
	}
}

// 词表外的角色不能触达 recall:与读侧/withdraw 同一口径(Actor.Role 会写进
// audit_logs.actor_role,词表外的值没有读者能解释)。
func TestStoreRefusesRecallWithAnActorRoleOutsideTheVocabulary(t *testing.T) {
	db := openStoreTestDB(t, true)
	store := NewStore(db, testAuthorizer{allow: true})
	ctx := context.Background()
	actor := Actor{TenantID: 1, UserID: "admin-1", Role: string(types.TenantRoleAdmin)}
	draft := testDraft("v-recall-guard")
	if err := store.CreateDraft(ctx, actor, draft); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Publish(ctx, actor, draft.ID, draft.Version); err != nil {
		t.Fatal(err)
	}
	drifter := Actor{TenantID: 1, UserID: "admin-1", Role: "tenant_admin"}
	if _, err := store.Recall(ctx, drifter, draft.ID, draft.Version); !errors.Is(err, ErrActorRoleInvalid) {
		t.Fatalf("recall with a drifted role = %v, want ErrActorRoleInvalid", err)
	}
	// 被拒的 recall 没有留下审计行:拒绝本身不是一次治理操作。
	var audits int64
	if err := db.Table("audit_logs").Where("tenant_id = ? AND scope_type = ? AND scope_id = ? AND action = ?", actor.TenantID, "lingdoc_ruleset", draft.ID, "lingdoc.ruleset.recall").Count(&audits).Error; err != nil {
		t.Fatal(err)
	}
	if audits != 0 {
		t.Fatalf("a refused recall wrote an audit row: %d", audits)
	}
}
