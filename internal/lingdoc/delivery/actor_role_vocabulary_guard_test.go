package delivery

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// 这组断言守的是「导出这一面的写入方只把租户角色写进审计列」。
//
// 导出把操作者角色写进规则集使用审计（lingdoc_ruleset_use_audits.actor_role，
// 以及同一份事件落到 audit_logs.actor_role）。那一列只有一个意思：操作者在
// 那个租户里当时的角色。角色词表由 internal/types 拥有（types.TenantRole），
// 所以词表外的值写进去就没有读者能解释。
//
// delivery.Actor.Role 的类型就是 types.TenantRole，所以词表外的值只能由
// 调用方显式转换出来——这正是要挡住的那种情况：类型挡不住「转换出来的
// 另一个词表的值」，只能由运行期的词表判定挡住。

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

// TestExportRefusesActorRoleOutsideTheTenantRoleVocabulary 断言词表外的角色
// 进不到导出：导出被拒绝，且没有写下任何规则集使用审计事件。
func TestExportRefusesActorRoleOutsideTheTenantRoleVocabulary(t *testing.T) {
	input := deliveryInputWithPublishedRuleSet(t, validDeliveryInput())
	snapshots := NewMemorySnapshotStore()
	release := NewReleaseService(snapshots)
	snapshot, err := release.Prepare(input)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.RuleSetRef == nil {
		t.Fatal("the fixture must carry a RuleSet reference: the governed export path is the one that records the actor role")
	}

	recorder := &snapshotRuleSetUseAuditRecorder{}
	exports := NewMemoryExportStore()
	service := NewExportService(
		snapshots,
		exports,
		frozenRenderer(func(DeliveryInput) ([]byte, error) { return []byte("docx"), nil }),
		FrozenValidatorFunc(fileValidationNotUnderTest),
		CurrentnessFunc(currentExportInput),
		ExportAccessFunc(allowExport),
	).WithRuleSetUseAuditor(recorder)
	if service == nil {
		t.Fatal("the export service was not constructed")
	}

	for _, role := range roleOutsideTheTenantRoleVocabulary {
		actor := Actor{TenantID: 1, UserID: "owner", Role: types.TenantRole(role)}
		if _, _, err := service.StartAs(context.Background(), actor, snapshot.ProjectID, snapshot.ID, exportActionKey); !errors.Is(err, ErrExportUnavailable) {
			t.Errorf("StartAs with role %q = %v, want ErrExportUnavailable", role, err)
		}
	}

	// 两个写入点都要看：成功路径由产物库记规则集使用审计，失败路径由
	// useAuditor 记。词表外的角色哪一个都不该留下记录。
	if events := exports.RuleSetUseAudits(); len(events) != 0 {
		t.Fatalf("recorded rule set use audits = %d, want none: a role outside the vocabulary must not reach the audit column", len(events))
	}
	if len(recorder.events) != 0 {
		t.Fatalf("rule set use audit events = %d, want none: a role outside the vocabulary must not reach the audit column", len(recorder.events))
	}
}

// TestExportRecordsTheTenantRoleItWasGiven 断言词表内的角色照旧被记录下来，
// 而且记录的就是那个角色。
//
// 守护不能只会拦：词表内的角色必须照常落库，否则这条约束就变成了「审计列
// 永远是空的」这种另一种坏。
func TestExportRecordsTheTenantRoleItWasGiven(t *testing.T) {
	input := deliveryInputWithPublishedRuleSet(t, validDeliveryInput())
	snapshots := NewMemorySnapshotStore()
	release := NewReleaseService(snapshots)
	snapshot, err := release.Prepare(input)
	if err != nil {
		t.Fatal(err)
	}

	recorder := &snapshotRuleSetUseAuditRecorder{}
	exports := NewMemoryExportStore()
	service := NewExportService(
		snapshots,
		exports,
		frozenRenderer(func(DeliveryInput) ([]byte, error) { return []byte("docx"), nil }),
		FrozenValidatorFunc(fileValidationNotUnderTest),
		CurrentnessFunc(currentExportInput),
		ExportAccessFunc(allowExport),
	).WithRuleSetUseAuditor(recorder)
	if service == nil {
		t.Fatal("the export service was not constructed")
	}

	actor := Actor{TenantID: 1, UserID: "owner", Role: types.TenantRoleAdmin}
	if _, _, err := service.StartAs(context.Background(), actor, snapshot.ProjectID, snapshot.ID, exportActionKey); err != nil {
		t.Fatal(err)
	}
	events := exports.RuleSetUseAudits()
	if len(events) != 1 {
		t.Fatalf("recorded rule set use audits = %d, want exactly one", len(events))
	}
	if events[0].ActorRole != string(types.TenantRoleAdmin) {
		t.Fatalf("recorded actor role = %q, want %q", events[0].ActorRole, string(types.TenantRoleAdmin))
	}
}
