package templategovernance

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/rulesetstore"
	"github.com/Tencent/WeKnora/internal/lingdoc/template"
)

type contextBuilder struct {
	disallow bool
	extra    bool
}

func (c contextBuilder) Build(_ context.Context, _ Actor, scope SuggestionScope) (AuthorizedContext, error) {
	result := AuthorizedContext{ProjectID: scope.ProjectID, Basis: scope.Basis, AuthorizationState: "allowed", Sources: []AuthorizedSource{}}
	for _, ref := range scope.Sources {
		quote := "authorized quotation"
		result.Sources = append(result.Sources, AuthorizedSource{SourceRef: ref, QuotedText: quote, QuotedTextHash: digestText(quote), ModelUseAllowed: !c.disallow})
	}
	if c.extra {
		result.Sources = append(result.Sources, AuthorizedSource{SourceRef: SourceRef{ID: "outside-project", Version: "1"}, QuotedTextHash: digestText(""), ModelUseAllowed: true})
	}
	return result, nil
}

type suggestionModel struct {
	calls        int
	raw          json.RawMessage
	err          error
	before       func()
	checkedScope bool
}

func (m *suggestionModel) Suggest(_ context.Context, _ Actor, request RuleSuggestionRequest) (RuleSuggestionOutput, error) {
	m.calls++
	m.checkedScope = request.Scope.ProjectID == request.Context.ProjectID && request.CorrelationID != "" && request.PromptVersion == RuleSuggestionPromptVersion
	if m.before != nil {
		m.before()
	}
	raw := m.raw
	if len(raw) == 0 {
		raw = json.RawMessage(`{"proposals":[{"definition_id":"pattern","definition_version":"1","target_kind":"field","target_id":"research_goal","parameters":{"pattern":"^Study"},"message":"Consider using a concrete goal","rationale":"The field should declare an observable action","evidence_source_ids":[]}],"explanation":"Human review is required; this is a suggestion only"}`)
	}
	return RuleSuggestionOutput{StructuredOutput: raw, ModelID: "fixture-model", RunMode: "mock", InputTokens: 10, OutputTokens: 8}, m.err
}
func suggestionSetup(t *testing.T) (*Service, *testAuth, Actor, TemplateVersion, SuggestionScope, *suggestionModel) {
	t.Helper()
	s, _, auth, actor, draft, _ := seedService(t)
	published := publishTestVersion(t, s, actor, draft)
	scope := SuggestionScope{ProjectID: "project-1", Basis: auth.basis, TemplateID: published.TemplateID, TemplateVersion: published.Version, CatalogRevision: published.Revision, RuleSet: published.RuleSet, TargetKind: "field", TargetID: "research_goal", Sources: []SourceRef{}}
	model := &suggestionModel{}
	s.ports.Model = model
	s.ports.Context = contextBuilder{}
	return s, auth, actor, published, scope, model
}
func acceptedBindings(run SuggestionRun) []template.RuleBinding {
	p := run.Output.Proposals[0]
	return []template.RuleBinding{{RuleID: p.RuleID, DefinitionID: p.DefinitionID, DefinitionVersion: p.DefinitionVersion, Origin: "project", TargetKind: p.TargetKind, TargetID: p.TargetID, Parameters: p.Parameters, Severity: template.SeverityWarning, Message: p.Message, TriggerScenes: []string{"write_check", "preflight"}, Enabled: true, Owner: "human"}}
}
func TestSuggestionAcceptOnlyCreatesDraftAndReplaysWithoutModelOrNewWrites(t *testing.T) {
	s, _, actor, _, scope, model := suggestionSetup(t)
	run, err := s.RequestRuleSuggestion(context.Background(), actor, "suggestion-request-1", scope)
	if err != nil || run.Status != "suggestion" || run.Output == nil {
		t.Fatalf("run = %v %v", run, err)
	}
	if !model.checkedScope || model.calls != 1 || run.RunMode != "mock" || run.ModelID != "fixture-model" {
		t.Fatal("model/runtime scope metadata missing")
	}
	retry, err := s.RequestRuleSuggestion(context.Background(), actor, "suggestion-request-1", scope)
	if err != nil || retry.ID != run.ID || model.calls != 1 {
		t.Fatal("request retry invoked the model again")
	}
	input := AcceptSuggestionInput{ExpectedRevision: run.Revision, RuleSetID: scope.RuleSet.ID, Version: "suggestion-draft-1", Bindings: acceptedBindings(run), Reason: "human reviewed and edited the proposed check"}
	accepted, err := s.AcceptRuleSuggestion(context.Background(), actor, scope.ProjectID, run.ID, "accept-suggestion-1", input)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := rulesetstore.NewStore(s.db, s.ports.RuleSets).GetVersion(context.Background(), actor, scope.RuleSet.ID, input.Version)
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Status != "accepted" || draft.State != template.RuleSetDraft || draft.RulesetHash != "" || len(draft.Bindings) != 2 {
		t.Fatal("suggestion acceptance published or lost existing rules")
	}
	original, err := s.GetVersion(context.Background(), actor, scope.TemplateID, scope.TemplateVersion)
	if err != nil || original.Revision != scope.CatalogRevision || original.RuleSet != scope.RuleSet {
		t.Fatal("accept changed the active template")
	}
	before := countRows(t, s.db, "audit_logs", "")
	again, err := s.AcceptRuleSuggestion(context.Background(), actor, scope.ProjectID, run.ID, "accept-suggestion-1", input)
	if err != nil || again.Revision != accepted.Revision || countRows(t, s.db, "audit_logs", "") != before {
		t.Fatal("accept retry duplicated writes")
	}
	input.Reason = "different decision"
	if _, err := s.AcceptRuleSuggestion(context.Background(), actor, scope.ProjectID, run.ID, "accept-suggestion-1", input); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("different body = %v", err)
	}
}
func TestSuggestionStaleAndRevokedActorCannotAcceptOrReadCachedSuccess(t *testing.T) {
	s, auth, actor, _, scope, _ := suggestionSetup(t)
	run, err := s.RequestRuleSuggestion(context.Background(), actor, "request-stale-run", scope)
	if err != nil {
		t.Fatal(err)
	}
	auth.basis.ContextRevision++
	stale, err := s.GetRuleSuggestion(context.Background(), actor, scope.ProjectID, run.ID)
	if err != nil || stale.Status != "stale" {
		t.Fatalf("stale = %v %v", stale, err)
	}
	if _, err := s.AcceptRuleSuggestion(context.Background(), actor, scope.ProjectID, run.ID, "accept-stale-run", AcceptSuggestionInput{ExpectedRevision: stale.Revision, RuleSetID: scope.RuleSet.ID, Version: "draft-stale", Bindings: acceptedBindings(run), Reason: "try stale suggestion"}); !errors.Is(err, ErrStale) {
		t.Fatalf("stale accepted = %v", err)
	}
	rejected, err := s.RejectRuleSuggestion(context.Background(), actor, scope.ProjectID, run.ID, "reject-stale-run", RejectSuggestionInput{ExpectedRevision: stale.Revision, Reason: "the input context changed"})
	if err != nil || rejected.Status != "rejected" {
		t.Fatal("stale suggestion wasn't recoverable by rejection")
	}
	auth.allow = false
	if _, err := s.GetRuleSuggestion(context.Background(), actor, scope.ProjectID, run.ID); err == nil {
		t.Fatal("revoked actor read suggestion")
	}
	if _, err := s.RequestRuleSuggestion(context.Background(), actor, "request-stale-run", scope); err == nil {
		t.Fatal("revoked actor replayed request")
	}
}
func TestSuggestionModelCompletionRechecksAuthorizationAndBasis(t *testing.T) {
	for _, change := range []string{"revoke", "context"} {
		t.Run(change, func(t *testing.T) {
			s, auth, actor, _, scope, model := suggestionSetup(t)
			model.before = func() {
				if change == "revoke" {
					auth.allow = false
				} else {
					auth.basis.ProjectVersion++
				}
			}
			run, err := s.RequestRuleSuggestion(context.Background(), actor, "delayed-model-run", scope)
			if change == "revoke" {
				if err == nil {
					t.Fatal("revoked completion returned output")
				}
				var rows []suggestionRow
				if e := s.db.Find(&rows).Error; e != nil {
					t.Fatal(e)
				}
				if len(rows) != 1 || rows[0].Status != "failed" || rows[0].OutputJSON != "null" {
					t.Fatal("revoked output persisted")
				}
			} else if err != nil || run.Status != "stale" || run.Output != nil {
				t.Fatalf("stale completion = %v %v", run, err)
			}
		})
	}
}
func TestSuggestionSourceScopeAndUnavailableModelFailClosed(t *testing.T) {
	for _, kind := range []string{"disallowed", "extra", "no-model"} {
		t.Run(kind, func(t *testing.T) {
			s, _, actor, _, scope, model := suggestionSetup(t)
			scope.Sources = []SourceRef{{ID: "source-1", Version: "1"}}
			switch kind {
			case "disallowed":
				s.ports.Context = contextBuilder{disallow: true}
			case "extra":
				s.ports.Context = contextBuilder{extra: true}
			case "no-model":
				s.ports.Model = nil
			}
			run, err := s.RequestRuleSuggestion(context.Background(), actor, "source-suggestion-run", scope)
			if err != nil || run.Status != "failed" || run.Output != nil {
				t.Fatalf("failure ledger = %v %v", run, err)
			}
			if kind == "no-model" {
				if run.ErrorCode != "model_unavailable" {
					t.Fatal(run.ErrorCode)
				}
			} else if model.calls != 0 || run.ErrorCode != "source_access_denied" {
				t.Fatal("unauthorized context reached model")
			}
		})
	}
}
func TestSuggestionRejectsUnsafeModelOutputAndHumanBlocking(t *testing.T) {
	for _, raw := range []string{
		`{"proposals":[],"explanation":"empty"}`,
		`{"proposals":[{"definition_id":"presence","definition_version":"1","target_kind":"field","target_id":"research_goal","parameters":{},"message":"candidate","rationale":"review required","evidence_source_ids":[]}],"explanation":"candidate only","basis":{"project_version":1}}`,
		`{"proposals":[{"definition_id":"pattern","definition_version":"1","target_kind":"field","target_id":"research_goal","severity":"blocking","parameters":{"pattern":".*"},"rationale":"unsafe"}],"explanation":"unsafe"}`,
		`{"proposals":[{"rule_id":"goal-present","definition_id":"presence","definition_version":"1","target_kind":"field","target_id":"research_goal","rationale":"replace identity"}],"explanation":"unsafe"}`,
		`{"proposals":[{"definition_id":"script","definition_version":"1","target_kind":"field","target_id":"research_goal","rationale":"execute code"}],"explanation":"unsafe"}`,
		`{"proposals":[{"definition_id":"presence","definition_version":"1","target_kind":"field","target_id":"research_goal","rationale":"use foreign source","evidence_source_ids":["outside-project"]}],"explanation":"unsafe"}`,
	} {
		t.Run(raw, func(t *testing.T) {
			s, _, actor, _, scope, model := suggestionSetup(t)
			model.raw = json.RawMessage(raw)
			run, err := s.RequestRuleSuggestion(context.Background(), actor, "invalid-output-run", scope)
			if err != nil || run.Status != "failed" || run.ErrorCode != "invalid_model_output" {
				t.Fatalf("unsafe model = %v %v", run, err)
			}
		})
	}
	s, _, actor, _, scope, _ := suggestionSetup(t)
	run, err := s.RequestRuleSuggestion(context.Background(), actor, "valid-output-run", scope)
	if err != nil {
		t.Fatal(err)
	}
	bindings := acceptedBindings(run)
	bindings[0].Severity = template.SeverityBlocking
	if _, err := s.AcceptRuleSuggestion(context.Background(), actor, scope.ProjectID, run.ID, "blocking-human-accept", AcceptSuggestionInput{ExpectedRevision: run.Revision, RuleSetID: scope.RuleSet.ID, Version: "blocking-draft", Bindings: bindings, Reason: "attempt blocking suggestion"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("blocking accepted = %v", err)
	}
	if countRows(t, s.db, "lingdoc_rule_sets", "version=?", "blocking-draft") != 0 {
		t.Fatal("blocking draft persisted")
	}
}
func TestSuggestionAcceptAuditFailureRollsBackExistingRuleSetStoreSavepoint(t *testing.T) {
	s, _, actor, _, scope, _ := suggestionSetup(t)
	run, err := s.RequestRuleSuggestion(context.Background(), actor, "audit-test-suggestion", scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.Exec(`CREATE TRIGGER deny_accept_audit BEFORE INSERT ON audit_logs WHEN NEW.action='lingdoc.template.accept_suggestion' BEGIN SELECT RAISE(ABORT,'audit unavailable'); END`).Error; err != nil {
		t.Fatal(err)
	}
	input := AcceptSuggestionInput{ExpectedRevision: run.Revision, RuleSetID: scope.RuleSet.ID, Version: "audit-draft", Bindings: acceptedBindings(run), Reason: "reviewed suggestion"}
	if _, err := s.AcceptRuleSuggestion(context.Background(), actor, scope.ProjectID, run.ID, "audit-failed-accept", input); err == nil {
		t.Fatal("audit failure succeeded")
	}
	if countRows(t, s.db, "lingdoc_rule_sets", "version=?", "audit-draft") != 0 {
		t.Fatal("nested ruleset draft survived parent rollback")
	}
	current, err := s.GetRuleSuggestion(context.Background(), actor, scope.ProjectID, run.ID)
	if err != nil || current.Status != "suggestion" || current.Revision != run.Revision {
		t.Fatal("failed acceptance changed run")
	}
	if countRows(t, s.db, "lingdoc_template_governance_operations", "idempotency_key=?", "audit-failed-accept") != 0 {
		t.Fatal("failed acceptance operation persisted")
	}
}

type modeSuggestionModel struct {
	inner suggestionModel
	mode  string
}

func (m *modeSuggestionModel) Suggest(ctx context.Context, actor Actor, request RuleSuggestionRequest) (RuleSuggestionOutput, error) {
	output, err := m.inner.Suggest(ctx, actor, request)
	output.RunMode = m.mode
	return output, err
}
func TestSuggestionPreservesSharedFakeModelModeWithoutClaimingRealModel(t *testing.T) {
	s, _, actor, _, scope, _ := suggestionSetup(t)
	s.ports.Model = &modeSuggestionModel{mode: "real_api_fake_model"}
	run, err := s.RequestRuleSuggestion(context.Background(), actor, "shared-fake-mode", scope)
	if err != nil || run.Status != "suggestion" || run.RunMode != "real_api_fake_model" {
		t.Fatalf("shared fake mode = %v %v", run, err)
	}
	if run.RunMode == "real" {
		t.Fatal("fake provider recorded as a real model")
	}
}

func TestSuggestionCannotBindAnotherCatalogWithOtherwiseCurrentProjectBasis(t *testing.T) {
	for _, dimension := range []string{"template_id", "template_version"} {
		t.Run(dimension, func(t *testing.T) {
			s, auth, actor, _, scope, model := suggestionSetup(t)
			if dimension == "template_id" {
				auth.basis.TemplateID = "another-project-template"
			} else {
				auth.basis.TemplateVersion = "another-project-version"
			}
			// The request knows and pins the current project basis. It still cannot
			// mix that project with a separately published old catalog definition.
			scope.Basis = auth.basis
			before := countRows(t, s.db, "audit_logs", "")
			if _, err := s.RequestRuleSuggestion(context.Background(), actor, "mismatched-current-template", scope); !errors.Is(err, ErrStale) {
				t.Fatalf("mismatched catalog accepted: %v", err)
			}
			if model.calls != 0 || countRows(t, s.db, "lingdoc_rule_suggestion_runs", "") != 0 || countRows(t, s.db, "audit_logs", "") != before {
				t.Fatal("mismatched scope crossed model/operation boundary")
			}
		})
	}
}
