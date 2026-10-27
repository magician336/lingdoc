package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

type captureKBActivityAudit struct {
	entry *types.AuditLog
}

func (c *captureKBActivityAudit) Log(_ context.Context, entry *types.AuditLog) error {
	c.entry = entry
	return nil
}

func (*captureKBActivityAudit) LogDenied(context.Context, *gin.Context, uint64, string, string, types.TenantRole) error {
	return nil
}

func (*captureKBActivityAudit) List(context.Context, uint64, *interfaces.AuditLogQuery) ([]*types.AuditLog, error) {
	return nil, nil
}

func (*captureKBActivityAudit) Purge(context.Context, int) (int64, error) { return 0, nil }

func TestRecordKBActivityCarriesInitiatorAndTaskMetadata(t *testing.T) {
	ctx := types.TaskInitiator{UserID: "user-1", Role: types.TenantRoleAdmin}.Apply(context.Background())
	ctx = withKBActivityTask(ctx, "task-1", "user")
	audit := &captureKBActivityAudit{}

	recordKBActivity(ctx, audit, 7, "kb-1", types.AuditActionKnowledgeMoveCompleted,
		"knowledge_move", "task-1", types.AuditOutcomeSuccess, map[string]any{"count": 2})

	if audit.entry == nil {
		t.Fatal("expected an activity entry")
	}
	if audit.entry.ActorUserID != "user-1" || audit.entry.ActorRole != "admin" {
		t.Fatalf("actor = %q/%q", audit.entry.ActorUserID, audit.entry.ActorRole)
	}
	var details map[string]any
	if err := json.Unmarshal(audit.entry.Details, &details); err != nil {
		t.Fatalf("unmarshal details: %v", err)
	}
	if details["task_id"] != "task-1" || details["trigger"] != "user" || details["count"] != float64(2) {
		t.Fatalf("details = %#v", details)
	}
}

func TestFAQImportCompletedOutcome(t *testing.T) {
	cases := []struct {
		success, failed, skipped int
		outcome                  types.AuditOutcome
	}{
		{10, 0, 0, types.AuditOutcomeSuccess},
		{8, 2, 0, types.AuditOutcomePartial},
		{5, 1, 2, types.AuditOutcomePartial},
		{0, 1, 1, types.AuditOutcomePartial},
		{0, 2, 0, types.AuditOutcomeFailed},
		{0, 0, 2, types.AuditOutcomeFailed},
	}
	for _, tc := range cases {
		outcome := faqImportCompletedOutcome(tc.success, tc.failed, tc.skipped)
		if outcome != tc.outcome {
			t.Fatalf("faqImportCompletedOutcome(%d,%d,%d) = %s, want %s",
				tc.success, tc.failed, tc.skipped, outcome, tc.outcome)
		}
	}
}

func TestFAQImportActivityDetails(t *testing.T) {
	payload := &types.FAQImportPayload{Mode: types.FAQBatchModeAppend}
	progress := &types.FAQImportProgress{SuccessCount: 0, FailedCount: 1, Total: 2}
	details := faqImportActivityDetails(payload, progress, 2)
	if details["count"] != 0 {
		t.Fatalf("count = %#v, want 0", details["count"])
	}
	if details["total"] != 2 || details["failed"] != 1 || details["skipped"] != 1 {
		t.Fatalf("details = %#v", details)
	}
}

func TestKBActivityAppendSampleTitles(t *testing.T) {
	details := map[string]any{"count": 3}
	kbActivityAppendSampleTitles(details, "  Alpha  ", "Beta", "Alpha", "Gamma")
	if details["title"] != "Alpha" {
		t.Fatalf("title = %#v", details["title"])
	}
	titles, ok := details["titles"].([]string)
	if !ok || len(titles) != 3 {
		t.Fatalf("titles = %#v", details["titles"])
	}

	single := map[string]any{"count": 1}
	kbActivityAppendSampleTitles(single, "Only one")
	if single["title"] != "Only one" {
		t.Fatalf("single title = %#v", single["title"])
	}
	if _, exists := single["titles"]; exists {
		t.Fatalf("single titles should be omitted: %#v", single)
	}
}

func TestRecordKBActivityCanSuppressCompositeTaskChildren(t *testing.T) {
	audit := &captureKBActivityAudit{}
	ctx := withKBActivitySuppressed(context.Background())
	recordKBActivity(ctx, audit, 7, "kb-1", types.AuditActionKnowledgeCreated,
		"knowledge", "knowledge-1", types.AuditOutcomeAccepted, nil)
	if audit.entry != nil {
		t.Fatalf("suppressed child activity was recorded: %#v", audit.entry)
	}
}

func TestRecordWikiContentActivityUsesUnifiedActivityFeed(t *testing.T) {
	audit := &captureKBActivityAudit{}
	RecordWikiContentActivity(context.Background(), audit, 7, "kb-1", map[string]int{
		"ingest":  3,
		"retract": 1,
	})

	if audit.entry == nil {
		t.Fatal("expected a wiki activity entry")
	}
	if audit.entry.Action != types.AuditActionWikiContentChanged ||
		audit.entry.ScopeType != auditScopeKnowledgeBase || audit.entry.ScopeID != "kb-1" {
		t.Fatalf("unexpected activity entry: %#v", audit.entry)
	}
	var details map[string]any
	if err := json.Unmarshal(audit.entry.Details, &details); err != nil {
		t.Fatalf("unmarshal details: %v", err)
	}
	if details["count"] != float64(4) {
		t.Fatalf("count = %#v, want 4", details["count"])
	}
	actions, ok := details["actions"].(map[string]any)
	if !ok || actions["ingest"] != float64(3) || actions["retract"] != float64(1) {
		t.Fatalf("actions = %#v", details["actions"])
	}
}

// TestKBActivityTriggerReflectsInitiatorIdentity locks in the worker
// attribution used by ProcessKBClone / ProcessKnowledgeMove /
// ProcessKnowledgeListDelete / ProcessKnowledgeListReparse: the task
// trigger must be derived from the restored initiator, not hard-coded to
// "user". A synthetic (API-key) or empty initiator is service-owned work
// and must therefore report "system", matching the empty actor attribution.
func TestKBActivityTriggerReflectsInitiatorIdentity(t *testing.T) {
	// Real human initiator → "user".
	userCtx := types.TaskInitiator{UserID: "user-1", Role: types.TenantRoleAdmin}.Apply(context.Background())
	if got := kbActivityTrigger(userCtx); got != "user" {
		t.Fatalf("real initiator trigger = %q, want user", got)
	}

	// Empty / legacy initiator (e.g. scheduler or API-key origin) → "system".
	systemCtx := types.TaskInitiator{}.Apply(context.Background())
	if got := kbActivityTrigger(systemCtx); got != "system" {
		t.Fatalf("empty initiator trigger = %q, want system", got)
	}

	// Synthetic API-key user is service identity → "system".
	synthCtx := context.WithValue(context.Background(), types.UserIDContextKey, "system-7")
	if got := kbActivityTrigger(synthCtx); got != "system" {
		t.Fatalf("synthetic user trigger = %q, want system", got)
	}
}

// TestRecordKBActivityActorRoleSemantics pins #559's conclusion for the
// kb_activity actor-role branch.
//
// With a human actor the role never comes out empty: auditActorRole reads
// TenantRoleFromContext, whose viewer fallback keeps an unpopulated context at
// least "viewer". The empty string only appears together with an empty
// ActorUserID — service-internal flows with no human actor (system tasks,
// register-time EnsureOwner). That pair is a documented, determined meaning
// (see the comment at the branch in kb_activity.go): the closed actor_role
// vocabulary (#535 ruling) has no word for "no human actor" yet, and adding
// one is a glossary decision. Until it is made, this test keeps the two
// observable halves honest: a human actor always carries a vocabulary role,
// and a system actor always carries the empty actor together with an empty
// user id, never a fabricated tenant role.
func TestRecordKBActivityActorRoleSemantics(t *testing.T) {
	// A human actor without a populated role context still gets the viewer
	// fallback, never the empty string.
	humanCtx := context.WithValue(context.Background(), types.UserIDContextKey, "user-1")
	audit := &captureKBActivityAudit{}
	recordKBActivity(humanCtx, audit, 7, "kb-1", types.AuditActionKnowledgeCreated,
		"knowledge", "knowledge-1", types.AuditOutcomeSuccess, nil)
	if audit.entry == nil {
		t.Fatal("expected an activity entry")
	}
	if audit.entry.ActorUserID != "user-1" || audit.entry.ActorRole == "" {
		t.Fatalf("human actor = %q/%q, want a non-empty role", audit.entry.ActorUserID, audit.entry.ActorRole)
	}

	// No human actor → the documented empty pair: both the user id and the
	// role are empty, never a fabricated tenant role.
	systemAudit := &captureKBActivityAudit{}
	recordKBActivity(context.Background(), systemAudit, 7, "kb-1", types.AuditActionKnowledgeCreated,
		"knowledge", "knowledge-1", types.AuditOutcomeSuccess, nil)
	if systemAudit.entry == nil {
		t.Fatal("expected a system activity entry")
	}
	if systemAudit.entry.ActorUserID != "" || systemAudit.entry.ActorRole != "" {
		t.Fatalf("system actor = %q/%q, want the documented empty pair",
			systemAudit.entry.ActorUserID, systemAudit.entry.ActorRole)
	}
}
