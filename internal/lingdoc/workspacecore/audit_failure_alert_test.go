package workspacecore

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// A failed audit write on an ordinary read has to become visible in the
// runtime observation stream, not only in a log line (#327).
//
// The alert is published on its own transaction, so it survives the failure
// it reports, and it never changes the authorization decision.

// installAuditWriteFailureTrigger makes every audit insert fail.
//
// ABORT undoes the failing statement and leaves the transaction usable,
// which is the fail-open shape an ordinary read has on SQLite: the read
// continues while the audit row is lost.
func installAuditWriteFailureTrigger(t *testing.T, svc *Service) {
	t.Helper()
	statement := `CREATE TRIGGER audit_write_failure BEFORE INSERT ON lingdoc_project_audits
BEGIN SELECT RAISE(ABORT, 'synthetic audit write failure'); END`
	if err := svc.testDB().Exec(statement).Error; err != nil {
		t.Fatal(err)
	}
}

func TestAuditFailurePublishesRuntimeEventAndAlert(t *testing.T) {
	svc, actor, p := extendedImpactStore(t)
	// The audit sink has to be present: without it the service skips the
	// audit write entirely and there is no failure to report.
	svc.audit = &auditSinkStub{}
	ctx := context.Background()
	if _, _, _, err := svc.SaveRuntimeAlertRule(ctx, actor, p.ID, "audit-failure-rule", RuntimeAlertRule{
		Signal: "audit/failed", Severity: "P2", Action: "notify", ResponsibleID: actor.UserID,
	}); err != nil {
		t.Fatalf("save the audit failure rule: %v", err)
	}
	installAuditWriteFailureTrigger(t, svc)

	if err := svc.Authorize(ctx, actor, p.ID, "read"); err != nil {
		t.Fatalf("the audit failure stopped the read: %v", err)
	}

	events, err := svc.ListRuntimeEvents(ctx, actor, p.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Category != "audit" {
			continue
		}
		found = true
		if event.Status != "failed" {
			t.Fatalf("the audit failure event carries another status: %+v", event)
		}
		if event.ErrorCode != auditFailureErrorCode {
			t.Fatalf("the audit failure event carries another error code: %+v", event)
		}
		// The event reports the failure of an observability sink. It must not
		// carry what the audit was about: no capability, no details, no
		// research content.
		encoded, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"authorization", "details", "spec", "body"} {
			if strings.Contains(string(encoded), forbidden) {
				t.Fatalf("the audit failure event carries %q: %s", forbidden, encoded)
			}
		}
	}
	if !found {
		t.Fatalf("the audit failure produced no runtime event: %+v", events)
	}

	alerts, err := svc.ListRuntimeAlerts(ctx, actor, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 1 {
		t.Fatalf("the audit failure produced %d alerts, want 1: %+v", len(alerts), alerts)
	}
	if alerts[0].Severity != "P2" || alerts[0].Action != "notify" {
		t.Fatalf("the alert does not carry the rule's disposition: %+v", alerts[0])
	}
}

func TestAuditFailureDoesNotChangeTheAuthorizationDecision(t *testing.T) {
	svc, actor, p := extendedImpactStore(t)
	svc.audit = &auditSinkStub{}
	ctx := context.Background()
	installAuditWriteFailureTrigger(t, svc)

	// A caller without access is still refused while the audit sink is down.
	if err := svc.Authorize(ctx, Actor{TenantID: actor.TenantID, UserID: "stranger"}, p.ID, "read"); err == nil {
		t.Fatal("the audit failure turned a denial into an allow")
	}
}

func TestAlertWriteFailureDoesNotBlockTheRead(t *testing.T) {
	svc, actor, p := extendedImpactStore(t)
	svc.audit = &auditSinkStub{}
	ctx := context.Background()
	installAuditWriteFailureTrigger(t, svc)
	// The runtime record write fails too, so the alert cannot be published
	// either. Losing the alert may not cost the read.
	statement := `CREATE TRIGGER runtime_record_failure BEFORE INSERT ON lingdoc_g6_records
BEGIN SELECT RAISE(ABORT, 'synthetic runtime write failure'); END`
	if err := svc.testDB().Exec(statement).Error; err != nil {
		t.Fatal(err)
	}

	if err := svc.Authorize(ctx, actor, p.ID, "read"); err != nil {
		t.Fatalf("a failed alert stopped the read: %v", err)
	}
}
