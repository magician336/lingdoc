package rulesetstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/template"
)

func TestRuleSetUseAuditPageUsesStableCursorAndFilters(t *testing.T) {
	db := openStoreTestDB(t, true)
	store := NewStore(db, testAuthorizer{allow: true})
	ctx := context.Background()
	actor := Actor{TenantID: 1, UserID: "page-owner", Role: "tenant_admin"}
	draft := testDraft("audit-page-v1")
	draft.ID = "audit-page-rules"
	if err := store.CreateDraft(ctx, actor, draft); err != nil {
		t.Fatal(err)
	}
	published, err := store.Publish(ctx, actor, draft.ID, draft.Version)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO lingdoc_projects (id) VALUES (?)", "audit-page-project").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO lingdoc_members (project_id, user_id, role) VALUES (?, ?, ?)", "audit-page-project", actor.UserID, "owner").Error; err != nil {
		t.Fatal(err)
	}
	verifiedTemplate, verifiedBinding := verifiedAuditEvidence(t, published)
	base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for i, action := range []string{"check", "preflight", "release_snapshot", "export"} {
		event := template.RuleSetUseAuditEvent{
			ID: "audit-page-" + action, TenantID: actor.TenantID, ActorID: actor.UserID, ActorRole: actor.Role,
			ProjectID: "audit-page-project", Action: action, RuleSetID: published.ID, RuleSetVersion: published.Version,
			RuleSetHash: published.RulesetHash, EffectiveRulesetHash: verifiedBinding.EffectiveHash, TemplateID: verifiedTemplate.ID,
			TemplateVersion: "1", TemplateCopyID: "copy-1", TemplateCopyVersion: 1, ProjectVersion: 1,
			Outcome: "passed", CreatedAt: base.Add(time.Duration(i) * time.Minute), VerifiedTemplate: &verifiedTemplate, VerifiedBinding: &verifiedBinding,
		}
		if err := store.RecordRuleSetUse(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	first, err := store.ListRuleSetUseAuditsPage(ctx, actor, "audit-page-project", RuleSetUseAuditQuery{Limit: 2})
	if err != nil || len(first.Items) != 2 || first.NextCursor == "" {
		t.Fatalf("first page = %+v err=%v, want two items and cursor", first, err)
	}
	second, err := store.ListRuleSetUseAuditsPage(ctx, actor, "audit-page-project", RuleSetUseAuditQuery{Cursor: first.NextCursor, Limit: 2})
	if err != nil || len(second.Items) != 2 || second.NextCursor != "" || second.Items[0].ID == first.Items[0].ID {
		t.Fatalf("second page = %+v err=%v, want remaining non-overlapping items", second, err)
	}
	filtered, err := store.ListRuleSetUseAuditsPage(ctx, actor, "audit-page-project", RuleSetUseAuditQuery{Action: "export", Limit: 10})
	if err != nil || len(filtered.Items) != 1 || filtered.Items[0].Action != "export" {
		t.Fatalf("filtered page = %+v err=%v", filtered, err)
	}
	if _, err := store.ListRuleSetUseAuditsPage(ctx, actor, "audit-page-project", RuleSetUseAuditQuery{Cursor: "not-a-cursor"}); !errors.Is(err, ErrInvalidRuleSetUseAudit) {
		t.Fatalf("invalid cursor error = %v, want ErrInvalidRuleSetUseAudit", err)
	}
}

func TestRuleSetUseAuditPageReadsWithdrawnHistoryAndRejectsInvalidRange(t *testing.T) {
	db := openStoreTestDB(t, true)
	store := NewStore(db, testAuthorizer{allow: true})
	ctx := context.Background()
	actor := Actor{TenantID: 1, UserID: "history-owner", Role: "tenant_admin"}
	draft := testDraft("audit-history-v1")
	draft.ID = "audit-history-rules"
	if err := store.CreateDraft(ctx, actor, draft); err != nil {
		t.Fatal(err)
	}
	published, err := store.Publish(ctx, actor, draft.ID, draft.Version)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO lingdoc_projects (id) VALUES (?)", "audit-history-project").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO lingdoc_members (project_id, user_id, role) VALUES (?, ?, ?)", "audit-history-project", actor.UserID, "owner").Error; err != nil {
		t.Fatal(err)
	}
	verifiedTemplate, verifiedBinding := verifiedAuditEvidence(t, published)
	event := template.RuleSetUseAuditEvent{
		ID: "audit-history-event", TenantID: actor.TenantID, ActorID: actor.UserID, ActorRole: actor.Role,
		ProjectID: "audit-history-project", Action: "preflight", RuleSetID: published.ID, RuleSetVersion: published.Version,
		RuleSetHash: published.RulesetHash, EffectiveRulesetHash: verifiedBinding.EffectiveHash, TemplateID: verifiedTemplate.ID,
		TemplateVersion: "1", Outcome: "passed", CreatedAt: time.Now().UTC(), VerifiedTemplate: &verifiedTemplate, VerifiedBinding: &verifiedBinding,
	}
	if err := store.RecordRuleSetUse(ctx, event); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Withdraw(ctx, actor, published.ID, published.Version); err != nil {
		t.Fatal(err)
	}
	page, err := store.ListRuleSetUseAuditsPage(ctx, actor, "audit-history-project", RuleSetUseAuditQuery{Limit: 1})
	if err != nil || len(page.Items) != 1 || page.Items[0].RuleSetHash != published.RulesetHash {
		t.Fatalf("withdrawn history = %+v err=%v", page, err)
	}
	from := time.Now().UTC()
	to := from.Add(-time.Minute)
	if _, err := store.ListRuleSetUseAuditsPage(ctx, actor, "audit-history-project", RuleSetUseAuditQuery{From: &from, To: &to}); !errors.Is(err, ErrInvalidRuleSetUseAudit) {
		t.Fatalf("invalid time range error = %v, want ErrInvalidRuleSetUseAudit", err)
	}
}
