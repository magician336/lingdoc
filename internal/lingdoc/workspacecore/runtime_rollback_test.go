package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// G6 运行控制的回退端点（#383）。
//
// 回退经既有领域入口执行（reverseChangeSet），不改 revision 指针、
// 模板指针或发布文件：这几段用例把「回退只写运行控制记录与审计」钉住。

// acceptingRuntimeRecovery accepts every recovery recheck, so that the
// resume path can be exercised without a snapshot and an export.
type acceptingRuntimeRecovery struct{ calls int }

func (c *acceptingRuntimeRecovery) Recheck(context.Context, Actor, string, string, string) error {
	c.calls++
	return nil
}

func appliedChangeSet(t *testing.T, svc *Service, actor Actor, p Project, chapter Chapter) ChangeSet {
	t.Helper()
	change := extendedChange(t, svc, actor, p, chapter)
	if _, _, _, err := svc.ApplyChangeSet(context.Background(), actor, p.ID, change.ID, "rollback-apply"); err != nil {
		t.Fatalf("apply the change set: %v", err)
	}
	return change
}

func TestG6RollbackRequiresATarget(t *testing.T) {
	svc, actor, p := extendedImpactStore(t)
	if _, _, _, err := svc.ControlRuntime(context.Background(), actor, p.ID, "rollback-without-target", RuntimeControlInput{
		Scope: RuntimeScope{Kind: "project", Value: p.ID}, Action: "rollback", Reason: "没有目标",
	}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("a rollback without a target was accepted: %v", err)
	}
	if _, _, _, err := svc.ControlRuntime(context.Background(), actor, p.ID, "rollback-empty-target", RuntimeControlInput{
		Scope: RuntimeScope{Kind: "project", Value: p.ID}, Action: "rollback", Reason: "空目标",
		Rollback: &RuntimeRollbackInput{},
	}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("a rollback with an empty target was accepted: %v", err)
	}
}

func TestG6RollbackRefusesACrossScopeAndAnOutsider(t *testing.T) {
	svc, actor, p := extendedImpactStore(t)
	ctx := context.Background()
	// 跨范围：别的课题。
	if _, _, _, err := svc.ControlRuntime(ctx, actor, p.ID, "rollback-cross-scope", RuntimeControlInput{
		Scope: RuntimeScope{Kind: "project", Value: "another-project"}, Action: "rollback", Reason: "跨课题回退",
		Rollback: &RuntimeRollbackInput{TemplateVersion: p.TemplateVersion},
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a cross-project rollback was accepted: %v", err)
	}
	// 未授权：不是课题负责人。
	outsider := Actor{TenantID: actor.TenantID, UserID: "outsider"}
	if _, _, _, err := svc.ControlRuntime(ctx, outsider, p.ID, "rollback-outsider", RuntimeControlInput{
		Scope: RuntimeScope{Kind: "project", Value: p.ID}, Action: "rollback", Reason: "越权回退",
		Rollback: &RuntimeRollbackInput{TemplateVersion: p.TemplateVersion},
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an unauthorized rollback was accepted: %v", err)
	}
	// 被拒的回退没有留下运行控制记录。
	controls, err := svc.ListRuntimeControls(ctx, actor, p.ID)
	if err != nil {
		t.Fatalf("list controls: %v", err)
	}
	if len(controls) != 0 {
		t.Fatalf("a refused rollback left a control record: %+v", controls)
	}
}

func TestG6RollbackGoesThroughTheDomainEntryAndKeepsTheHistory(t *testing.T) {
	svc, actor, p := extendedImpactStore(t)
	ctx := context.Background()
	chapter := extendedChapter(t, svc, actor, p)
	change := appliedChangeSet(t, svc, actor, p, chapter)

	// 回退之前的样子：模板副本版本、章节版本、审计行。
	before, err := svc.GetProject(ctx, actor, p.ID)
	if err != nil {
		t.Fatalf("read the project: %v", err)
	}
	var versionsBefore, auditsBefore int64
	if err := svc.testDB().Model(&chapterVersionRow{}).Where("project_id = ?", p.ID).Count(&versionsBefore).Error; err != nil {
		t.Fatalf("count the chapter versions: %v", err)
	}
	if err := svc.testDB().Table("lingdoc_project_audits").Where("project_id = ?", p.ID).Count(&auditsBefore).Error; err != nil {
		t.Fatalf("count the audit rows: %v", err)
	}

	raw, _, _, err := svc.ControlRuntime(ctx, actor, p.ID, "rollback-control", RuntimeControlInput{
		Scope: RuntimeScope{Kind: "project", Value: p.ID}, Action: "rollback", Reason: "回退到已验证配置",
		Rollback: &RuntimeRollbackInput{ChangeSetID: change.ID, TemplateVersion: p.TemplateVersion, RulesetHash: p.TemplateCopy.RulesetHash},
	})
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	var control RuntimeControl
	if err := json.Unmarshal(raw, &control); err != nil {
		t.Fatalf("decode the control: %v", err)
	}
	if control.Status != "PAUSED" {
		t.Fatalf("the rollback did not stop new writes: %+v", control)
	}
	if control.RollbackTo == nil || control.RollbackTo.TemplateVersion != p.TemplateVersion ||
		control.RollbackTo.RulesetHash != p.TemplateCopy.RulesetHash {
		t.Fatalf("the rollback did not record its target: %+v", control)
	}

	// 逆向变更集是既有领域入口的产物：它是一���新的、还没有应用的变更集。
	changes, err := svc.ListChangeSets(ctx, actor, p.ID)
	if err != nil {
		t.Fatalf("list the change sets: %v", err)
	}
	inverse := 0
	for _, candidate := range changes {
		if candidate.ReversesChangeSetID == change.ID {
			inverse++
		}
	}
	if inverse != 1 {
		t.Fatalf("the rollback did not go through the inverse change set: %+v", changes)
	}

	// 已发布模板、章节历史与既有审计没有被改写。
	after, err := svc.GetProject(ctx, actor, p.ID)
	if err != nil {
		t.Fatalf("read the project again: %v", err)
	}
	if after.TemplateCopyVersion != before.TemplateCopyVersion || after.TemplateVersion != before.TemplateVersion {
		t.Fatalf("the rollback rewrote the published template: %+v -> %+v", before, after)
	}
	var versionsAfter, auditsAfter int64
	if err := svc.testDB().Model(&chapterVersionRow{}).Where("project_id = ?", p.ID).Count(&versionsAfter).Error; err != nil {
		t.Fatalf("count the chapter versions again: %v", err)
	}
	if versionsAfter != versionsBefore {
		t.Fatalf("the rollback rewrote the chapter history: %d -> %d", versionsBefore, versionsAfter)
	}
	if err := svc.testDB().Table("lingdoc_project_audits").Where("project_id = ?", p.ID).Count(&auditsAfter).Error; err != nil {
		t.Fatalf("count the audit rows again: %v", err)
	}
	if auditsAfter < auditsBefore {
		t.Fatalf("the rollback dropped confirmed audits: %d -> %d", auditsBefore, auditsAfter)
	}
	// 回退自己留下了一条审计。
	var rollbackAudits int64
	if err := svc.testDB().Table("lingdoc_project_audits").Where("project_id = ? AND action = ?", p.ID, "runtime.rollback").Count(&rollbackAudits).Error; err != nil {
		t.Fatalf("count the rollback audits: %v", err)
	}
	if rollbackAudits != 1 {
		t.Fatalf("the rollback did not write its audit: %d", rollbackAudits)
	}
}

func TestG6ResumeRefusesARollbackTargetTheProjectNoLongerCarries(t *testing.T) {
	svc, actor, p := extendedImpactStore(t)
	ctx := context.Background()
	checker := &acceptingRuntimeRecovery{}
	svc.SetRuntimeRecoveryChecker(checker)

	// 回退到一个课题已经不再带着的模板版本。
	if _, _, _, err := svc.ControlRuntime(ctx, actor, p.ID, "rollback-old-template", RuntimeControlInput{
		Scope: RuntimeScope{Kind: "project", Value: p.ID}, Action: "rollback", Reason: "回退到旧模板",
		Rollback: &RuntimeRollbackInput{TemplateVersion: "a-template-version-the-project-left"},
	}); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	// 恢复：既有复验要过，目标配置也要仍可读。
	_, _, _, err := svc.ControlRuntime(ctx, actor, p.ID, "resume-old-template", RuntimeControlInput{
		ExpectedVersion: 1, Scope: RuntimeScope{Kind: "project", Value: p.ID}, Action: "resume",
		Reason: "恢复运行", SnapshotID: "snapshot-1", ExportID: "export-1",
	})
	if !errors.Is(err, ErrRuntimeRecoveryRequired) {
		t.Fatalf("a resume into an unreadable target was accepted: %v", err)
	}
	if checker.calls != 1 {
		t.Fatalf("the resume skipped the recovery recheck: %d", checker.calls)
	}
	// 运行仍然是暂停的。
	controls, err := svc.ListRuntimeControls(ctx, actor, p.ID)
	if err != nil {
		t.Fatalf("list controls: %v", err)
	}
	if len(controls) != 1 || controls[0].Status != "PAUSED" {
		t.Fatalf("the refused resume changed the runtime: %+v", controls)
	}
}

func TestG6ResumeAfterARollbackNeedsTheRechecks(t *testing.T) {
	svc, actor, p := extendedImpactStore(t)
	ctx := context.Background()
	if _, _, _, err := svc.ControlRuntime(ctx, actor, p.ID, "rollback-plain", RuntimeControlInput{
		Scope: RuntimeScope{Kind: "project", Value: p.ID}, Action: "rollback", Reason: "回退",
		Rollback: &RuntimeRollbackInput{TemplateVersion: p.TemplateVersion},
	}); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	// 没有快照与导出：既有复验要求仍然生效。
	if _, _, _, err := svc.ControlRuntime(ctx, actor, p.ID, "resume-without-rechecks", RuntimeControlInput{
		ExpectedVersion: 1, Scope: RuntimeScope{Kind: "project", Value: p.ID}, Action: "resume", Reason: "恢复运行",
	}); !errors.Is(err, ErrRuntimeRecoveryRequired) {
		t.Fatalf("a resume without the rechecks was accepted: %v", err)
	}
	// 复验通过之后才恢复。
	svc.SetRuntimeRecoveryChecker(&acceptingRuntimeRecovery{})
	raw, _, _, err := svc.ControlRuntime(ctx, actor, p.ID, "resume-with-rechecks", RuntimeControlInput{
		ExpectedVersion: 1, Scope: RuntimeScope{Kind: "project", Value: p.ID}, Action: "resume",
		Reason: "恢复运行", SnapshotID: "snapshot-1", ExportID: "export-1",
	})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	var control RuntimeControl
	if err := json.Unmarshal(raw, &control); err != nil {
		t.Fatalf("decode the control: %v", err)
	}
	if control.Status != "ACTIVE" || !control.RecoveryVerified {
		t.Fatalf("the resume did not activate the runtime: %+v", control)
	}
}
