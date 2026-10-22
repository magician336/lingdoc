package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// G6 的运行观测（#382-C 授权判定可观测 / #327 审计失败接告警）。
//
// 三段用例分别钉住：授权拒绝产生 `permission/deny` 运行事件、
// 审计写失败产生 `audit/failed` 运行事件并被匹配规则转成告警、
// 审计事件带上判定时的修订号。

// extendedImpactStoreWithAudit is the same store with an audit sink.
//
// The service skips the audit write entirely when it has no sink, so the
// authorization audits only exist when one is installed.
func extendedImpactStoreWithAudit(t *testing.T) (*Service, Actor, Project) {
	t.Helper()
	svc, actor, p := extendedImpactStore(t)
	audited := NewServiceWithAudit(svc.repository.(*GORMRepository), svc.templates, svc.sources, nil, &auditSinkStub{})
	return audited, actor, p
}

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

func TestG6AuditWriteFailureBecomesAnAlert(t *testing.T) {
	svc, actor, p := extendedImpactStoreWithAudit(t)
	ctx := context.Background()
	// 规则先备好：审计表一旦不可用，写规则自己要写的审计行也会失败。
	rule := createAlertRule(t, svc, actor, p.ID, "audit-failure-rule", "audit/failed")
	if err := svc.testDB().Exec("CREATE TRIGGER g6_audit_unavailable BEFORE INSERT ON lingdoc_project_audits BEGIN SELECT RAISE(ABORT, 'audit unavailable'); END").Error; err != nil {
		t.Fatalf("make the audit store unavailable: %v", err)
	}

	// fail-open 仍然是 fail-open：授权判定本身没有失败。
	if err := svc.Authorize(ctx, actor, p.ID, "read"); err != nil {
		t.Fatalf("the audit failure changed the authorization outcome: %v", err)
	}

	events, err := svc.ListRuntimeEvents(ctx, actor, p.ID, "")
	if err != nil {
		t.Fatalf("read the runtime events: %v", err)
	}
	found := false
	for _, event := range events {
		if event.Category == "audit" && event.Status == "failed" && event.ErrorCode == "audit_write_failed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the audit failure did not become a runtime event: %+v", events)
	}

	// 匹配 audit/failed 的规则把它转成告警。
	alerts, err := svc.ListRuntimeAlerts(ctx, actor, p.ID)
	if err != nil {
		t.Fatalf("list alerts: %v", err)
	}
	matched := false
	for _, alert := range alerts {
		if alert.RuleID == rule.ID {
			matched = true
		}
	}
	if !matched {
		t.Fatalf("the audit failure did not raise the alert: %+v", alerts)
	}
}

func TestG6AuditEventCarriesTheRevisionItWasDecidedOn(t *testing.T) {
	svc, actor, p := extendedImpactStoreWithAudit(t)
	ctx := context.Background()
	if err := svc.Authorize(ctx, actor, p.ID, "read"); err != nil {
		t.Fatalf("authorize: %v", err)
	}
	var events []AuditEvent
	if err := svc.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		var err error
		events, err = tx.(AuditTransaction).AuditEvents(p.ID)
		return err
	}); err != nil {
		t.Fatalf("read the audit events: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("the authorization did not write an audit row")
	}
	found := false
	for _, event := range events {
		if event.Action != "authorization.read" {
			continue
		}
		found = true
		if event.ProjectRevision != p.CurrentContextRevision {
			t.Fatalf("the audit row does not carry the project revision: %+v", event)
		}
		if event.TargetVersion != p.ProjectVersion {
			t.Fatalf("the audit row does not carry the target version: %+v", event)
		}
	}
	if !found {
		t.Fatalf("no authorization audit row: %+v", events)
	}
}

func TestG6AuditEventKeepsTheRevisionInsideItsDetails(t *testing.T) {
	svc, actor, p := extendedImpactStoreWithAudit(t)
	ctx := context.Background()
	if err := svc.Authorize(ctx, actor, p.ID, "read"); err != nil {
		t.Fatalf("authorize: %v", err)
	}
	var rows []auditEventRow
	if err := svc.testDB().Where("project_id = ? AND action = ?", p.ID, "authorization.read").Find(&rows).Error; err != nil {
		t.Fatalf("read the audit rows: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("the authorization did not write an audit row")
	}
	details := map[string]any{}
	if err := json.Unmarshal([]byte(rows[0].DetailsJSON), &details); err != nil {
		t.Fatalf("decode the audit details: %v", err)
	}
	revision, ok := details["project_revision"].(float64)
	if !ok || int64(revision) != p.CurrentContextRevision {
		t.Fatalf("the audit details do not carry the project revision: %s", rows[0].DetailsJSON)
	}
}
