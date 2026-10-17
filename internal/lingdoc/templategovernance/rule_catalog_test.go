package templategovernance

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/template"
)

func TestRuleCatalogDraftAndLifecycleLostResponseHaveSameBasis(t *testing.T) {
	s, db, auth, actor, v, _ := seedService(t)
	ctx := context.Background()
	draft := v.RuleSetSnapshot
	draft.State = template.RuleSetDraft
	draft.Version = "new-draft"
	draft.RulesetHash = ""
	input := RuleDraftInput{Draft: draft, Reason: "curator reviewed exact rule draft"}
	if _, err := s.CreateRuleSetDraft(ctx, actor, "draft-response-lost", input); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateRuleSetDraft(ctx, actor, "draft-response-lost", input); err != nil {
		t.Fatal(err)
	}
	input.Reason = "different action"
	if _, err := s.CreateRuleSetDraft(ctx, actor, "draft-response-lost", input); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed payload retry: %v", err)
	}
	govern := RuleGovernanceInput{ExpectedState: template.RuleSetPublished, ExpectedRuleSetHash: v.RuleSet.Hash, Reason: "withdraw new bindings while retaining history"}
	next, err := s.WithdrawRuleSet(ctx, actor, v.RuleSet.ID, v.RuleSet.Version, "withdraw-response-lost", govern)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.WithdrawRuleSet(ctx, actor, v.RuleSet.ID, v.RuleSet.Version, "withdraw-response-lost", govern)
	if err != nil || next.RulesetHash != again.RulesetHash || again.State != template.RuleSetWithdrawn {
		t.Fatal("lifecycle retry lost exact result", err)
	}
	if countRows(t, db, "lingdoc_template_governance_operations", "operation='ruleset_withdraw'") != 1 {
		t.Fatal("replayed lifecycle wrote another operation")
	}
	auth.allow = false
	if _, err := s.WithdrawRuleSet(ctx, actor, v.RuleSet.ID, v.RuleSet.Version, "withdraw-response-lost", govern); err == nil {
		t.Fatal("current permission skipped on replay")
	}
}
func TestRuleCatalogMutationAndAuditRollbackTogether(t *testing.T) {
	s, db, _, actor, v, _ := seedService(t)
	if err := db.Exec("DROP TABLE audit_logs").Error; err != nil {
		t.Fatal(err)
	}
	_, err := s.WithdrawRuleSet(context.Background(), actor, v.RuleSet.ID, v.RuleSet.Version, "audit-must-be-atomic", RuleGovernanceInput{ExpectedState: template.RuleSetPublished, ExpectedRuleSetHash: v.RuleSet.Hash, Reason: "fixture unavailable audit must roll back"})
	if err == nil {
		t.Fatal("audit failure passed")
	}
	current, err := s.GetRuleSetVersion(context.Background(), actor, v.RuleSet.ID, v.RuleSet.Version)
	if err != nil || current.State != template.RuleSetPublished {
		t.Fatal("RuleSet escaped atomic parent transaction", err)
	}
	if countRows(t, db, "lingdoc_template_governance_operations", "operation='ruleset_withdraw'") != 0 {
		t.Fatal("failed transaction left a successful operation receipt")
	}
}
