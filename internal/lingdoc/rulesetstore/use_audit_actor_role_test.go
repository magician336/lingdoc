package rulesetstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

// 这组断言守的是「规则集使用审计的两个写入口对操作者角色的判定口径一致」。
//
// 规则集使用审计把操作者当时的租户角色写进 lingdoc_ruleset_use_audits 的
// actor_role 列。那一列只有一个意思：操作者在那个租户里当时的角色。角色词表
// 由 internal/types 拥有，types.TenantRole.IsValid 是唯一判定入口；audit_logs
// 那一侧（validateActor / recordAudit）已经按它判定过。
//
// 写侧有两个入口：
//
//   - Store.RecordRuleSetUse：走 validateActor ⇒ 非空 ＋ 在词表内；
//   - RecordRuleSetUseInTransaction：导出的事务入口。交付面（delivery 的快照
//     与导出存储）和 workspace 的全流程守卫走的就是它。
//
// 段一（PR #553）之后，导出入口只查非空、不查词表 ⇒ 词表外的词可以从这条
// 路径绕过去写进审计列。下面这组断言把两个入口钉在同一个口径上：非空 ＋
// 在词表内；词表外的值两个入口都拒绝，且拒绝形态相同。

// vocabularyTestAuthorizer 放行任何角色。
//
// store_test.go 里的 testAuthorizer 只放行 admin。若沿用它，owner /
// contributor / viewer 会先被授权挡住，于是「角色判定」与「授权判定」混在
// 一起，分不清到底是谁在起作用。这里让授权一律放行，于是两个入口之间的
// 差异只可能来自角色判定本身。
type vocabularyTestAuthorizer struct{}

func (vocabularyTestAuthorizer) AuthorizeRuleSet(_ context.Context, _ *gorm.DB, _ Actor, _ Action, _ *template.RuleSet) error {
	return nil
}

// tenantRolesInsideTheVocabulary 是词表内的角色：types.TenantRole 的全部成员。
//
// 这份清单不是判定依据——判定在 types.TenantRole.IsValid——它只是「哪些值
// 应当被接受」的期望值。
var tenantRolesInsideTheVocabulary = []string{
	string(types.TenantRoleOwner),
	string(types.TenantRoleAdmin),
	string(types.TenantRoleContributor),
	string(types.TenantRoleViewer),
}

// rolesOutsideTheTenantRoleVocabulary 是词表外的角色。
//
// 每一个都曾经在仓库里被当成角色用过，或者属于别的角色词表（成员的项目角色、
// 治理角色、职能角色）。它们都不是租户角色。最后一个带空格的词说明判定是
// 精确匹配：词表认的是整个词，不是一个前缀。
var rolesOutsideTheTenantRoleVocabulary = []string{
	"tenant_admin",
	"tenant_viewer",
	"template_publisher",
	"member",
	"collaborator",
	"author",
	"OWNER",
	" admin",
}

// roleGuardFixture 是一条可用的规则集写侧夹具。
type roleGuardFixture struct {
	db    *gorm.DB
	store *Store
	event template.RuleSetUseAuditEvent
}

// newRoleGuardFixture 造一个夹具：一条已发布的规则集、一个项目、一个成员，
// 以及一条形状完整（证据齐全）的使用审计事件。
//
// sourcePresent 为 false 时，事件仍然指向那条已发布规则集、证据仍然与事件
// 自洽，但那条规则集行被删掉了。这样「规则集不存在」不会盖住角色判定：
// 两个入口给出的错误就只可能来自角色判定。
func newRoleGuardFixture(t *testing.T, sourcePresent bool) roleGuardFixture {
	t.Helper()
	db := openStoreTestDB(t, true)
	store := NewStore(db, vocabularyTestAuthorizer{})
	ctx := context.Background()
	admin := Actor{TenantID: 1, UserID: "admin-1", Role: string(types.TenantRoleAdmin)}

	draft := testDraft("v-role-guard")
	if err := store.CreateDraft(ctx, admin, draft); err != nil {
		t.Fatal(err)
	}
	published, err := store.Publish(ctx, admin, draft.ID, draft.Version)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO lingdoc_projects (id) VALUES (?)", "project-role-guard").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO lingdoc_members (project_id, user_id, role) VALUES (?, ?, ?)",
		"project-role-guard", admin.UserID, string(types.TenantRoleOwner)).Error; err != nil {
		t.Fatal(err)
	}
	if !sourcePresent {
		if err := db.Exec("DELETE FROM lingdoc_rule_sets WHERE tenant_id = ? AND ruleset_id = ? AND version = ?",
			admin.TenantID, published.ID, published.Version).Error; err != nil {
			t.Fatal(err)
		}
	}

	verifiedTemplate, verifiedBinding := verifiedAuditEvidence(t, published)
	event := template.RuleSetUseAuditEvent{
		TenantID: admin.TenantID, ActorID: admin.UserID, ActorRole: string(types.TenantRoleAdmin),
		ProjectID: "project-role-guard", Action: "preflight", RuleSetID: published.ID,
		RuleSetVersion: published.Version, RuleSetHash: published.RulesetHash,
		EffectiveRulesetHash: verifiedBinding.EffectiveHash, TemplateID: verifiedTemplate.ID,
		TemplateVersion: verifiedTemplate.Version, TemplateCopyID: "copy-role-guard",
		TemplateCopyVersion: 2, ProjectVersion: 7, ResourceID: "snapshot-role-guard",
		Outcome: "blocked", CreatedAt: time.Now().UTC(),
		VerifiedTemplate: &verifiedTemplate, VerifiedBinding: &verifiedBinding,
	}
	return roleGuardFixture{db: db, store: store, event: event}
}

// useAuditErrorKind 把一个错误归成可比较的种类。
//
// 两个入口可能给同一个判定不同的包装文案，所以比较的是「是哪一种错」而不是
// 错误字符串。返回 "accepted" 表示没有错误。
func useAuditErrorKind(err error) string {
	switch {
	case err == nil:
		return "accepted"
	case errors.Is(err, ErrActorRoleInvalid):
		return "invalid-actor-role"
	case errors.Is(err, ErrActorRequired):
		return "actor-required"
	case errors.Is(err, ErrInvalidRuleSetUseAudit):
		return "invalid-audit-event"
	case errors.Is(err, ErrRuleSetNotFound):
		return "ruleset-not-found"
	default:
		return "unexpected:" + err.Error()
	}
}

// countRuleSetUseAuditRows 数规则集使用审计表里的行。
func countRuleSetUseAuditRows(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var rows int64
	if err := db.Table("lingdoc_ruleset_use_audits").Count(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}

// TestRecordRuleSetUseInTransactionRefusesActorRoleOutsideTheTenantRoleVocabulary
// 断言导出的写入口挡住词表外的角色，且不留下任何审计行。
//
// 这是段一之后剩下的那个写入侧缺口：交付面与 workspace 走的就是这个入口，
// 它原先只查角色非空。词表外的词写进 actor_role 列之后，没有读者能解释它。
func TestRecordRuleSetUseInTransactionRefusesActorRoleOutsideTheTenantRoleVocabulary(t *testing.T) {
	fixture := newRoleGuardFixture(t, true)
	ctx := context.Background()

	for _, role := range rolesOutsideTheTenantRoleVocabulary {
		event := fixture.event
		event.ActorRole = role

		err := fixture.db.Transaction(func(tx *gorm.DB) error {
			return RecordRuleSetUseInTransaction(tx, event)
		})
		if !errors.Is(err, ErrActorRoleInvalid) {
			t.Errorf("RecordRuleSetUseInTransaction with role %q = %v, want ErrActorRoleInvalid", role, err)
		}

		// 另一个写入口对同一个角色必须给出同一个判定。
		if err := fixture.store.RecordRuleSetUse(ctx, event); !errors.Is(err, ErrActorRoleInvalid) {
			t.Errorf("RecordRuleSetUse with role %q = %v, want ErrActorRoleInvalid", role, err)
		}

		if rows := countRuleSetUseAuditRows(t, fixture.db); rows != 0 {
			t.Fatalf("rule set use audit rows = %d, want none: a role outside the vocabulary must not reach the audit column", rows)
		}
	}
}

// TestRuleSetUseWriteEntriesKeepTheTenantRoleInsideTheVocabulary 断言词表内的
// 角色照常写进去，而且写进去的就是那个角色。
//
// 守护不能只会拦：词表内的角色必须照常落库，否则这条约束就变成了「审计列
// 永远是空的」这种另一种坏。
func TestRuleSetUseWriteEntriesKeepTheTenantRoleInsideTheVocabulary(t *testing.T) {
	ctx := context.Background()

	for _, role := range tenantRolesInsideTheVocabulary {
		role := role
		t.Run(role, func(t *testing.T) {
			exported := newRoleGuardFixture(t, true)
			event := exported.event
			event.ActorRole = role
			if err := exported.db.Transaction(func(tx *gorm.DB) error {
				return RecordRuleSetUseInTransaction(tx, event)
			}); err != nil {
				t.Fatalf("RecordRuleSetUseInTransaction with role %q = %v, want it recorded", role, err)
			}
			if rows := countRuleSetUseAuditRows(t, exported.db); rows != 1 {
				t.Fatalf("rule set use audit rows = %d, want exactly one", rows)
			}
			var recorded string
			if err := exported.db.Table("lingdoc_ruleset_use_audits").Select("actor_role").Take(&recorded).Error; err != nil {
				t.Fatal(err)
			}
			if recorded != role {
				t.Fatalf("recorded actor role = %q, want %q", recorded, role)
			}

			store := newRoleGuardFixture(t, true)
			event = store.event
			event.ActorRole = role
			if err := store.store.RecordRuleSetUse(ctx, event); err != nil {
				t.Fatalf("RecordRuleSetUse with role %q = %v, want it recorded", role, err)
			}
			if rows := countRuleSetUseAuditRows(t, store.db); rows != 1 {
				t.Fatalf("rule set use audit rows = %d, want exactly one", rows)
			}
			recorded = ""
			if err := store.db.Table("lingdoc_ruleset_use_audits").Select("actor_role").Take(&recorded).Error; err != nil {
				t.Fatal(err)
			}
			if recorded != role {
				t.Fatalf("recorded actor role = %q, want %q", recorded, role)
			}
		})
	}
}

// TestRuleSetUseWriteEntriesJudgeTheActorRoleTheSameWay 钉住两个写入口的角色
// 判定口径一致。
//
// 同一张角色表依次喂给两个入口，两个入口必须给出同一个判定（都收 / 都拒）
// 和同一个错误种类。只比较「都拒了」会放过「一个说角色非法、另一个说规则集
// 不存在」这种分叉，所以比较的是错误的种类。
//
// 任何一个入口单独放宽或收紧这张表上的判定——多收一个词表外的词，或少收一
// 个词表内的词——两个入口的判定就会分叉，这里转红。
//
// 两个子场景各有分工：
//
//   - source present：规则集在库里，词表内的角色两个入口都写进去；词表外的
//     角色两个入口都拒绝。导出入口若不查词表，它会把这行写进去，于是这里
//     转红。
//   - source missing：规则集不在库里（但事件与证据仍自洽）。此时「规则集不
//     存在」这个失败对两个入口都是一样的，词表外的角色必须被报成「角色非法」
//     而不是「规则集不存在」。读侧入口若哪天不再查词表，它会先撞上「规则集
//     不存在」，两个入口的判定就此分叉，这里转红。
func TestRuleSetUseWriteEntriesJudgeTheActorRoleTheSameWay(t *testing.T) {
	roles := append(append([]string{}, tenantRolesInsideTheVocabulary...), rolesOutsideTheTenantRoleVocabulary...)
	ctx := context.Background()

	t.Run("source present", func(t *testing.T) {
		fixture := newRoleGuardFixture(t, true)
		for _, role := range roles {
			event := fixture.event
			event.ActorRole = role
			storeErr := fixture.store.RecordRuleSetUse(ctx, event)
			exportedErr := fixture.db.Transaction(func(tx *gorm.DB) error {
				return RecordRuleSetUseInTransaction(tx, event)
			})
			if got, want := useAuditErrorKind(exportedErr), useAuditErrorKind(storeErr); got != want {
				t.Errorf("role %q: the exported entry says %q, the store entry says %q: the two write entries must judge a role the same way", role, got, want)
			}
		}
	})

	t.Run("source missing", func(t *testing.T) {
		fixture := newRoleGuardFixture(t, false)
		for _, role := range roles {
			event := fixture.event
			event.ActorRole = role
			storeErr := fixture.store.RecordRuleSetUse(ctx, event)
			exportedErr := fixture.db.Transaction(func(tx *gorm.DB) error {
				return RecordRuleSetUseInTransaction(tx, event)
			})
			if got, want := useAuditErrorKind(exportedErr), useAuditErrorKind(storeErr); got != want {
				t.Errorf("role %q: the exported entry says %q, the store entry says %q: the two write entries must judge a role the same way", role, got, want)
			}
		}
	})
}

// TestRecordRuleSetUseInTransactionRefusesAnEmptyActorRole 断言导出的写入口
// 仍然拒绝空角色。
//
// 「非空」是角色判定的另一半：词表判定只管词本身，空词由事件形状检查
// （normalizeRuleSetUseAudit）挡住。这条断言把那个既有形状钉住，免得空角色
// 悄悄变成另一个错误。
func TestRecordRuleSetUseInTransactionRefusesAnEmptyActorRole(t *testing.T) {
	fixture := newRoleGuardFixture(t, true)

	for _, role := range []string{"", "   "} {
		event := fixture.event
		event.ActorRole = role
		err := fixture.db.Transaction(func(tx *gorm.DB) error {
			return RecordRuleSetUseInTransaction(tx, event)
		})
		if !errors.Is(err, ErrInvalidRuleSetUseAudit) {
			t.Errorf("RecordRuleSetUseInTransaction with role %q = %v, want ErrInvalidRuleSetUseAudit", role, err)
		}
		if rows := countRuleSetUseAuditRows(t, fixture.db); rows != 0 {
			t.Fatalf("rule set use audit rows = %d, want none: an empty role must not reach the audit column", rows)
		}
	}
}
