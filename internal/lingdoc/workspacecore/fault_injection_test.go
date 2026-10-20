package workspacecore

import (
	"context"
	"errors"
	"testing"
)

// 受控故障注入的定向用例（#256 / AC-G3-03）。
//
// 这些用例在领域服务层证明三件事：注入点是窄端口上的替换而不是产品端点；开关
// 默认关闭；注入的失败带可读原因，并且整体回滚（项目 revision、模板引用与
// ImpactTask 集合都不变）。HTTP seam 的实跑证据在
// internal/lingdoc/workspace/fault_injection_http_test.go。

func TestFaultInjectionStaysInertWithoutATenantScope(t *testing.T) {
	svc, actor, p := extendedImpactStore(t)
	chapter := extendedChapter(t, svc, actor, p)
	change := extendedChange(t, svc, actor, p, chapter)

	// 没有点名租户就没有作用域：这种配置不得装上任何失败实现。
	svc.InstallFaultInjection(FaultInjection{Point: FaultPointImpactTaskWrite})
	if svc.impactTasks != nil {
		t.Fatal("没有租户作用域的注入装上了 ImpactTask 写入端口")
	}

	if _, status, replay, err := svc.ApplyChangeSet(context.Background(), actor, p.ID, change.ID, "fault-inert-apply"); err != nil || status != 200 || replay {
		t.Fatalf("未开启注入时 apply 行为变了: status=%d replay=%v err=%v", status, replay, err)
	}
}

func TestImpactTaskWriteFaultRollsBackTheApply(t *testing.T) {
	svc, actor, p := extendedImpactStore(t)
	chapter := extendedChapter(t, svc, actor, p)
	change := extendedChange(t, svc, actor, p, chapter)
	svc.InstallFaultInjection(FaultInjection{Point: FaultPointImpactTaskWrite, TenantID: actor.TenantID, Reason: "合成注入：ImpactTask 写入不可用"})

	ctx := context.Background()
	before, err := svc.GetProject(ctx, actor, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	tasksBefore := countImpactTasks(t, svc, actor, p.ID)

	_, status, replay, err := svc.ApplyChangeSet(ctx, actor, p.ID, change.ID, "fault-impact-task-apply")
	if err == nil {
		t.Fatal("注入的任务写入失败没有让 apply 失败")
	}
	if !IsInjectedFault(err) {
		t.Fatalf("apply 的错误不是注入失败: %v", err)
	}
	if !errors.Is(err, ErrInjectedFault) {
		t.Fatalf("注入失败没有保留可判别的 sentinel: %v", err)
	}
	var facing ClientFacingError
	if !errors.As(err, &facing) || facing.ClientCode() != "injected_fault" {
		t.Fatalf("注入失败没有自己的客户端表述: %v", err)
	}
	if status != 0 || replay {
		t.Fatalf("失败的 apply 不该有成功状态: status=%d replay=%v", status, replay)
	}

	after, err := svc.GetProject(ctx, actor, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.CurrentContextRevision != before.CurrentContextRevision || after.ProjectVersion != before.ProjectVersion || after.SpecRevision != before.SpecRevision {
		t.Fatalf("失败的应用留下了半完成状态\nbefore=%+v\nafter=%+v", before, after)
	}
	if after.TemplateCopyVersion != before.TemplateCopyVersion || after.TemplateID != before.TemplateID || after.TemplateVersion != before.TemplateVersion {
		t.Fatalf("失败的应用改动了模板引用: before=%+v after=%+v", before.TemplateCopyVersion, after.TemplateCopyVersion)
	}
	if got := countImpactTasks(t, svc, actor, p.ID); got != tasksBefore {
		t.Fatalf("失败的应用留下了 ImpactTask: %d -> %d", tasksBefore, got)
	}
}

func TestBusinessAuditFaultRollsBackTheApply(t *testing.T) {
	svc, actor, p := extendedImpactStore(t)
	chapter := extendedChapter(t, svc, actor, p)
	change := extendedChange(t, svc, actor, p, chapter)
	// 业务审计是 apply 事务的最后一次写入：它失败时，revision 推进与 ImpactTask
	// 写入都必须一起回滚。
	svc.InstallFaultInjection(FaultInjection{Point: FaultPointAuditWrite, TenantID: actor.TenantID, Action: "project.change_set.apply", Reason: "合成注入：审计写入不可用"})

	ctx := context.Background()
	before, err := svc.GetProject(ctx, actor, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	tasksBefore := countImpactTasks(t, svc, actor, p.ID)

	if _, _, _, err := svc.ApplyChangeSet(ctx, actor, p.ID, change.ID, "fault-audit-apply"); err == nil || !IsInjectedFault(err) {
		t.Fatalf("注入的审计写入失败没有让 apply 失败: %v", err)
	}

	after, err := svc.GetProject(ctx, actor, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.CurrentContextRevision != before.CurrentContextRevision || after.ProjectVersion != before.ProjectVersion {
		t.Fatalf("审计写失败留下了半完成状态: before=%+v after=%+v", before, after)
	}
	if got := countImpactTasks(t, svc, actor, p.ID); got != tasksBefore {
		t.Fatalf("审计写失败留下了 ImpactTask: %d -> %d", tasksBefore, got)
	}
}

func TestFaultInjectionDoesNotReachAnotherTenant(t *testing.T) {
	svc, actor, p := extendedImpactStore(t)
	chapter := extendedChapter(t, svc, actor, p)
	change := extendedChange(t, svc, actor, p, chapter)
	// 注入只对被点名的租户生效。
	svc.InstallFaultInjection(FaultInjection{Point: FaultPointImpactTaskWrite, TenantID: actor.TenantID + 1, Reason: "合成注入：另一个租户"})

	if _, status, replay, err := svc.ApplyChangeSet(context.Background(), actor, p.ID, change.ID, "fault-other-tenant-apply"); err != nil || status != 200 || replay {
		t.Fatalf("非演示租户被注入了: status=%d replay=%v err=%v", status, replay, err)
	}
}

func TestInjectedFaultErrorStatesItsOwnClientPresentation(t *testing.T) {
	err := &InjectedFaultError{Point: FaultPointImpactTaskWrite, Reason: "合成注入"}
	if err.ClientStatus() != 503 || err.ClientCode() != "injected_fault" {
		t.Fatalf("注入失败的客户端表述不对: status=%d code=%s", err.ClientStatus(), err.ClientCode())
	}
	if err.Error() == "" || !IsInjectedFault(err) {
		t.Fatalf("注入失败没有可读原因: %v", err)
	}
	if !(FaultInjection{Point: FaultPointImpactTaskWrite, TenantID: 1}).KnownPoint() {
		t.Fatal("受支持的注入点没有被认出来")
	}
	if (FaultInjection{Point: "typo", TenantID: 1}).KnownPoint() {
		t.Fatal("未知注入点被当成了受支持的注入点")
	}
}
