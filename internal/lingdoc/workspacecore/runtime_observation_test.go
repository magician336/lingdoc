package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// G6 的运行观测（#383 授权判定可观测）。
//
// 两段用例分别钉住：课题级授权拒绝产生 `permission/deny` 运行事件、
// 租户级授权拒绝同样产生 `permission/deny` 运行事件。
//
// 审计写失败的观测**不在**本面：#327 已由 PR #389 在 main 上完成（审计表两列
// 独立落库，读类审计失败接 main 的 `EmitAuditFailureAlert`），本面不重复交付。

func TestG6AuthorizationDenialIsObservableAsARuntimeEvent(t *testing.T) {
	svc, actor, p := extendedImpactStore(t)
	ctx := context.Background()
	outsider := Actor{TenantID: actor.TenantID, UserID: "outsider"}

	// 越权读运行事件：被拒。
	if _, err := svc.ListRuntimeEvents(ctx, outsider, p.ID, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an outsider read the runtime events: %v", err)
	}
	events, err := svc.ListRuntimeEvents(ctx, actor, p.ID, "")
	if err != nil {
		t.Fatalf("read the runtime events: %v", err)
	}
	denied := 0
	for _, event := range events {
		if event.Category != "permission" || event.Status != "deny" {
			continue
		}
		denied++
		if event.ErrorCode == "" {
			t.Fatalf("the denial event has no error code: %+v", event)
		}
		if event.Capability != "read" {
			t.Fatalf("the denial event does not name the refused capability: %+v", event)
		}
	}
	if denied != 1 {
		t.Fatalf("the refusal did not become exactly one runtime event: %+v", events)
	}
}

func TestG6TenantLevelDenialIsObservableAsARuntimeEvent(t *testing.T) {
	svc, actor, _ := extendedImpactStore(t)
	ctx := context.Background()
	// 不属于该租户的 actor：租户级判定（authorizeTenant）被拒。
	outsider := Actor{TenantID: actor.TenantID + 1, UserID: "outsider"}
	if _, _, err := svc.ListProjects(ctx, outsider); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an outsider listed the projects: %v", err)
	}
	// 租户级拒绝没有课题可落，事件记在 project_id="" 上——按课题读看不到它。
	var rows []runtimeRecordRow
	if err := svc.testDB().Where("project_id = ? AND kind = ?", "", "event").Find(&rows).Error; err != nil {
		t.Fatalf("read the tenant-level runtime events: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("the tenant-level refusal did not become exactly one runtime event: %+v", rows)
	}
	var event RuntimeEvent
	if err := json.Unmarshal([]byte(rows[0].BodyJSON), &event); err != nil {
		t.Fatalf("decode the tenant-level runtime event: %v", err)
	}
	if event.Category != "permission" || event.Status != "deny" {
		t.Fatalf("the tenant-level refusal is not a denial event: %+v", event)
	}
	if event.Capability != "read" {
		t.Fatalf("the tenant-level denial event does not name the refused capability: %+v", event)
	}
}
