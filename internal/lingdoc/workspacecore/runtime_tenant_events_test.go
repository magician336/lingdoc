package workspacecore

import (
	"context"
	"errors"
	"testing"
)

// #469 残留②:租户级授权拒绝事件落在 project_id="" 上,按课题读不到。
// 租户级运行事件读面把这部分事件按租户读出来,授权给**租户管理者**——与
// 租户范围控制记录(ListRuntimeControls 把 project_id="" 的行并入课题读)的
// 处置相比,这里是专用读面,避免把租户范围的事实混进每个课题的事件流。

func TestG6TenantRuntimeEventsAreReadableByATenantManager(t *testing.T) {
	svc, actor, p := extendedImpactStore(t)
	ctx := context.Background()
	// 触发一次租户级授权拒绝:**同租户但不在租户成员表里的用户**列项目——
	// legacyAuthorizeTenant 拒绝它,事件落在**本租户**的 project_id="" 上
	// (这正是值班要能在租户读面看到的形状;跨租户探测的拒绝归探测者租户,
	// 本租户读不到,存在性隐藏语义不破)。
	stranger := Actor{TenantID: actor.TenantID, UserID: "not-a-tenant-member"}
	if _, _, err := svc.ListProjects(ctx, stranger); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a stranger listed the projects: %v", err)
	}

	// 课题级读面:租户级事件按设计不可见(既有语义零变化,契约①)。
	projectEvents, err := svc.ListRuntimeEvents(ctx, actor, p.ID, "")
	if err != nil {
		t.Fatalf("read the project runtime events: %v", err)
	}
	for _, event := range projectEvents {
		if event.Category == "permission" && event.Status == "deny" && event.Capability == "read" {
			t.Fatalf("a project-scoped read returned the tenant-level denial event: %+v", event)
		}
	}

	// 租户级读面:管理者读出 permission/deny 事件。
	events, err := svc.ListTenantRuntimeEvents(ctx, actor, "")
	if err != nil {
		t.Fatalf("read the tenant runtime events: %v", err)
	}
	denied := 0
	for _, event := range events {
		if event.Category != "permission" || event.Status != "deny" {
			continue
		}
		denied++
		if event.Capability != "read" {
			t.Fatalf("the tenant-level denial event does not name the refused capability: %+v", event)
		}
	}
	if denied != 1 {
		t.Fatalf("the tenant-level read did not return exactly one denial event: %+v", events)
	}

	// correlation_id 过滤对齐既有读面语义。
	filtered, err := svc.ListTenantRuntimeEvents(ctx, actor, "no-such-correlation")
	if err != nil {
		t.Fatalf("read the tenant runtime events with a correlation id: %v", err)
	}
	if len(filtered) != 0 {
		t.Fatalf("the correlation filter did not apply: %+v", filtered)
	}
}

func TestG6TenantRuntimeEventsRefuseNonManagers(t *testing.T) {
	svc, actor, p := extendedImpactStore(t)
	ctx := context.Background()
	// 非管理者的租户成员:课题读者不等于租户读者。
	member := Actor{TenantID: actor.TenantID, UserID: "plain-member"}
	if _, _, _, err := svc.ControlRuntime(ctx, member, p.ID, "probe-runtime", RuntimeControlInput{
		Scope: RuntimeScope{Kind: "project", Value: p.ID}, Action: "rollback", Reason: "探针",
		Rollback: &RuntimeRollbackInput{TemplateVersion: p.TemplateVersion},
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the member probe did not confirm the outsider role: %v", err)
	}
	if _, err := svc.ListTenantRuntimeEvents(ctx, member, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a plain member read the tenant runtime events: %v", err)
	}
	// 外租户:同样拒绝(存在性隐藏)。
	outsider := Actor{TenantID: actor.TenantID + 1, UserID: "outsider"}
	if _, err := svc.ListTenantRuntimeEvents(ctx, outsider, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an outsider read the tenant runtime events: %v", err)
	}
}
