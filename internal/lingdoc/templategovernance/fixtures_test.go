package templategovernance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

// seedRegistrationService reuses the seeded catalog (profile + published rule
// set + draft version) and hands back a service whose replay reader and fixture
// store are the same merged store, exactly like the production runtime wires
// them.
func seedRegistrationService(t *testing.T) (*Service, *gorm.DB, *testAuth, Actor, TemplateVersion, ReplayFixtureStore) {
	t.Helper()
	s, db, auth, actor, draft, _ := seedService(t)
	store := NewReplayFixtureStore(db, []ReplayFixtureRecord{{
		Fixture:             ReplayFixture{ID: "taken-builtin", RuleSet: RuleSetRef{ID: "ruleset-demo", Version: "1", Hash: "demo-hash"}, RuleID: "goal-present", Scene: "write_check", Polarity: "positive", TargetRef: "field/research_goal", ExpectedStatus: template.EvaluationPass},
		TemplateID:          "template-demo",
		TemplateVersion:     "1",
		TemplateContentHash: "builtin-content-hash",
		PolicyHash:          "builtin-policy-hash",
		ReviewedBy:          "lingdoc-builtin-fixture-review",
		ReviewReference:     "repository:templategovernance-demo-v1",
		ReviewedAt:          time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC),
	}})
	service := NewService(db, Ports{Authorizer: auth, RuleSets: auth, Replay: ExampleReplayVerifier{Reader: store}, Usage: testUsage{}, Sources: sourceVerifier{allow: true}, Fixtures: store})
	_ = s
	return service, db, auth, actor, draft, store
}

// registrationFixtureInputs covers every enabled binding × trigger scene ×
// polarity of the seeded draft with actually evaluated inputs.
func registrationFixtureInputs() []ReplayFixtureInput {
	inputs := []ReplayFixtureInput{}
	for _, scene := range []string{"write_check", "preflight"} {
		for _, polarity := range []string{"positive", "negative"} {
			fields := map[string]string{}
			status := template.EvaluationIssue
			if polarity == "positive" {
				fields["research_goal"] = "observable objective"
				status = template.EvaluationPass
			}
			inputs = append(inputs, ReplayFixtureInput{
				FixtureID:       "reg-" + scene + "-" + polarity,
				RuleID:          "goal-present",
				Scene:           scene,
				Polarity:        polarity,
				TargetRef:       "field/research_goal",
				Input:           template.EvaluationInput{ProjectID: "registration", TargetVersion: "1", Fields: fields},
				ExpectedStatus:  status,
				ReviewReference: "review:fixture-registration-1",
			})
		}
	}
	return inputs
}

func registerInput(draft TemplateVersion, fixtures []ReplayFixtureInput) RegisterFixturesInput {
	return RegisterFixturesInput{TemplateID: draft.TemplateID, Version: draft.Version, Fixtures: fixtures, Reason: "registration acceptance"}
}

func countFixtureRows(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var count int64
	if err := db.Table("lingdoc_template_replay_fixtures").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}

func auditReceiptDetails(t *testing.T, db *gorm.DB, action, outcome string) []map[string]any {
	t.Helper()
	var rows []struct{ Details string }
	if err := db.Table("audit_logs").Select("details").Where("action=? AND outcome=?", action, outcome).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	parsed := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		var envelope struct {
			Details map[string]any `json:"details"`
		}
		if err := json.Unmarshal([]byte(row.Details), &envelope); err != nil {
			t.Fatal(err)
		}
		parsed = append(parsed, envelope.Details)
	}
	return parsed
}

func TestReplayFixtureStoreMergedReadFailsClosed(t *testing.T) {
	s, db, _, actor, draft, store := seedRegistrationService(t)
	ctx := context.Background()

	record, err := store.GetFixture(ctx, actor.TenantID, "taken-builtin")
	if err != nil || record.TemplateContentHash != "builtin-content-hash" {
		t.Fatalf("builtin record = %+v %v", record, err)
	}
	if _, err := store.GetFixture(ctx, actor.TenantID, "reg-write_check-positive"); !errors.Is(err, ErrFixtureNotStored) {
		t.Fatalf("unknown fixture = %v, want ErrFixtureNotStored", err)
	}
	if _, err := s.RegisterFixtures(ctx, actor, "store-merge-key", registerInput(draft, registrationFixtureInputs())); err != nil {
		t.Fatal(err)
	}
	record, err = store.GetFixture(ctx, actor.TenantID, "reg-write_check-positive")
	if err != nil || record.TemplateContentHash != draft.ContentHash || record.Fixture.RuleSet.Hash != draft.RulesetHash {
		t.Fatalf("registered record pin = %+v %v", record, err)
	}
	if _, err := store.GetFixture(ctx, actor.TenantID+1, "reg-write_check-positive"); !errors.Is(err, ErrFixtureNotStored) {
		t.Fatalf("cross-tenant row visible: %v", err)
	}
	if !store.ConflictsBuiltin("taken-builtin") || store.ConflictsBuiltin("reg-write_check-positive") {
		t.Fatal("builtin conflict answer wrong")
	}

	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetFixture(ctx, actor.TenantID, "unknown-fixture"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("storage down unknown fixture = %v, want fail-closed ErrUnavailable", err)
	}
	if _, err := store.ListFixtures(ctx, actor.TenantID); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("storage down listing = %v, want fail-closed ErrUnavailable (never builtin-only)", err)
	}
	if record, err := store.GetFixture(ctx, actor.TenantID, "taken-builtin"); err != nil || record.Fixture.ID != "taken-builtin" {
		t.Fatalf("code-defined builtin must keep answering while storage is down: %+v %v", record, err)
	}
	if _, err := store.GetReplayFixture(ctx, actor, "unknown-fixture"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("reader seam must fail closed too: %v", err)
	}
}

func TestRegisterFixturesTrialsAtomicallyAuditsAndPublishes(t *testing.T) {
	s, db, _, actor, draft, _ := seedRegistrationService(t)
	ctx := context.Background()

	receipt, err := s.RegisterFixtures(ctx, actor, "register-accept-1", registerInput(draft, registrationFixtureInputs()))
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ContentHash != draft.ContentHash || receipt.PolicyHash != draft.PolicyHash || receipt.TemplateID != draft.TemplateID || receipt.Version != draft.Version {
		t.Fatalf("server-fixed pin missing from receipt: %+v", receipt)
	}
	if len(receipt.Registered) != 4 {
		t.Fatalf("registered = %d", len(receipt.Registered))
	}
	for _, summary := range receipt.Registered {
		if summary.RuleSet != draft.RuleSet {
			t.Fatalf("client-influenced ruleset pin %v, want server version pin %v", summary.RuleSet, draft.RuleSet)
		}
	}
	if !receipt.Coverage.Complete || len(receipt.Coverage.Entries) != 2 {
		t.Fatalf("coverage = complete:%v entries:%d", receipt.Coverage.Complete, len(receipt.Coverage.Entries))
	}
	for _, entry := range receipt.Coverage.Entries {
		if !entry.PositiveCovered || !entry.NegativeCovered {
			t.Fatalf("cell not covered after full registration: %+v", entry)
		}
	}
	if countFixtureRows(t, db) != 4 {
		t.Fatal("registration rows missing")
	}
	record, err := seedRegistrationRead(t, s, actor, "reg-write_check-negative")
	if err != nil {
		t.Fatal(err)
	}
	if record.Fixture.ExpectedStatus != template.EvaluationIssue || record.ReviewReference != "review:fixture-registration-1" {
		t.Fatalf("stored fixture wrong: %+v", record)
	}

	page, err := s.ListAuditHistory(ctx, actor, draft.TemplateID, AuditHistoryFilter{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range page.Events {
		if event.Action == "lingdoc.template.register_fixture" && event.Outcome == "success" {
			found = true
		}
	}
	if !found {
		t.Fatalf("register_fixture success event unreadable in admin audit history: %+v", page.Events)
	}

	ids := make([]string, 0, 4)
	for _, summary := range receipt.Registered {
		ids = append(ids, summary.FixtureID)
	}
	published, err := s.Publish(ctx, actor, "publish-registered-1", PublishInput{TemplateID: draft.TemplateID, Version: draft.Version, ExpectedRevision: draft.Revision, ReplayFixtureIDs: ids, Reason: "registered fixtures carry the publication"})
	if err != nil {
		t.Fatal(err)
	}
	if published.State != template.RuleSetPublished {
		t.Fatalf("publish with registered fixtures only: %s", published.State)
	}
}

func seedRegistrationRead(t *testing.T, s *Service, actor Actor, id string) (ReplayFixtureRecord, error) {
	t.Helper()
	return s.ports.Fixtures.GetFixture(context.Background(), actor.TenantID, id)
}

func TestRegisterFixturesRejectsShapeSemanticsBuiltinConflictsAndBounds(t *testing.T) {
	s, db, _, actor, draft, _ := seedRegistrationService(t)
	ctx := context.Background()
	base := registrationFixtureInputs()

	cases := []struct {
		name    string
		mutate  func([]ReplayFixtureInput) []ReplayFixtureInput
		wantErr error
	}{
		{"empty batch", func(f []ReplayFixtureInput) []ReplayFixtureInput { return nil }, ErrInvalidRequest},
		{"batch over limit", func(f []ReplayFixtureInput) []ReplayFixtureInput {
			oversized := make([]ReplayFixtureInput, 0, MaxReplayFixturesPerBatch+1)
			for i := 0; i <= MaxReplayFixturesPerBatch; i++ {
				one := f[0]
				one.FixtureID = fmt.Sprintf("reg-over-%d", i)
				oversized = append(oversized, one)
			}
			return oversized
		}, ErrInvalidRequest},
		{"scene outside trigger scenes", func(f []ReplayFixtureInput) []ReplayFixtureInput {
			f[0].Scene = "delivery"
			return f
		}, ErrInvalidRequest},
		{"polarity outside vocabulary", func(f []ReplayFixtureInput) []ReplayFixtureInput {
			f[0].Polarity = "either"
			return f
		}, ErrInvalidRequest},
		{"rule not an enabled binding", func(f []ReplayFixtureInput) []ReplayFixtureInput {
			f[0].RuleID = "absent-rule"
			return f
		}, ErrInvalidRequest},
		{"expected status contradicts polarity class", func(f []ReplayFixtureInput) []ReplayFixtureInput {
			f[0].ExpectedStatus = template.EvaluationIssue
			return f
		}, ErrInvalidRequest},
		{"fixture id outside stable lexicon", func(f []ReplayFixtureInput) []ReplayFixtureInput {
			f[0].FixtureID = "Bad Fixture"
			return f
		}, ErrInvalidRequest},
		{"builtin fixture id is reserved", func(f []ReplayFixtureInput) []ReplayFixtureInput {
			f[0].FixtureID = "taken-builtin"
			return f
		}, ErrInvalidRequest},
		{"review reference required", func(f []ReplayFixtureInput) []ReplayFixtureInput {
			f[0].ReviewReference = ""
			return f
		}, ErrInvalidRequest},
		{"duplicate ids in one batch", func(f []ReplayFixtureInput) []ReplayFixtureInput {
			f[1].FixtureID = f[0].FixtureID
			return f
		}, ErrInvalidRequest},
		{"target ref unbounded", func(f []ReplayFixtureInput) []ReplayFixtureInput {
			f[0].TargetRef = strings.Repeat("field/", 40)
			return f
		}, ErrInvalidRequest},
	}
	for _, testCase := range cases {
		receipt, err := s.RegisterFixtures(ctx, actor, "reject-"+testCase.name, registerInput(draft, testCase.mutate(append([]ReplayFixtureInput{}, base...))))
		if !errors.Is(err, testCase.wantErr) {
			t.Fatalf("%s: err = %v, want %v", testCase.name, err, testCase.wantErr)
		}
		if receipt.Coverage.Entries != nil {
			t.Fatalf("%s: rejected request returned a receipt %+v", testCase.name, receipt)
		}
	}
	if rows := countFixtureRows(t, db); rows != 0 {
		t.Fatalf("rejected registrations wrote %d rows", rows)
	}
	details := auditReceiptDetails(t, db, "lingdoc.template.register_fixture", "failure")
	if len(details) != len(cases) {
		t.Fatalf("failure receipts = %d, want %d", len(details), len(cases))
	}
	for _, receipt := range details {
		if receipt["error_code"] != "invalid_request" {
			t.Fatalf("failure receipt code = %v", receipt["error_code"])
		}
	}
}

func TestRegisterFixturesReplayEvidenceFamilyFailuresLeaveZeroRows(t *testing.T) {
	s, db, _, actor, draft, _ := seedRegistrationService(t)
	ctx := context.Background()
	base := registrationFixtureInputs()

	cases := []struct {
		name   string
		mutate func([]ReplayFixtureInput) []ReplayFixtureInput
	}{
		{"declared outcome contradicts actual evaluation", func(f []ReplayFixtureInput) []ReplayFixtureInput {
			f[0].Polarity = "positive"
			f[0].ExpectedStatus = template.EvaluationPass
			f[0].Input = template.EvaluationInput{ProjectID: "registration", TargetVersion: "1", Fields: map[string]string{}}
			return f
		}},
		{"declared target is not exercised", func(f []ReplayFixtureInput) []ReplayFixtureInput {
			f[0].TargetRef = "field/research_subject"
			return f
		}},
	}
	for _, testCase := range cases {
		_, err := s.RegisterFixtures(ctx, actor, "replay-reject-"+testCase.name, registerInput(draft, testCase.mutate(append([]ReplayFixtureInput{}, base...))))
		if !errors.Is(err, ErrReplayRequired) {
			t.Fatalf("%s: err = %v, want the replay evidence family", testCase.name, err)
		}
		if rows := countFixtureRows(t, db); rows != 0 {
			t.Fatalf("%s: failed trial wrote %d rows", testCase.name, rows)
		}
	}
	details := auditReceiptDetails(t, db, "lingdoc.template.register_fixture", "failure")
	if len(details) != len(cases) {
		t.Fatalf("failure receipts = %d, want %d", len(details), len(cases))
	}
	for _, receipt := range details {
		if receipt["error_code"] != "replay_required" {
			t.Fatalf("failure receipt code = %v", receipt["error_code"])
		}
	}
}

func TestRegisterFixturesIdempotentReplayConflictAndAppendOnly(t *testing.T) {
	s, db, _, actor, draft, _ := seedRegistrationService(t)
	ctx := context.Background()
	input := registerInput(draft, registrationFixtureInputs())

	replayed := false
	replayCtx := ObserveOperationReplay(ctx, func() { replayed = true })
	first, err := s.RegisterFixtures(replayCtx, actor, "register-idem-1", input)
	if err != nil || replayed {
		t.Fatalf("first registration: %v replayed=%v", err, replayed)
	}
	second, err := s.RegisterFixtures(replayCtx, actor, "register-idem-1", input)
	if err != nil || !replayed {
		t.Fatalf("same-key replay must return the same result: %v replayed=%v", err, replayed)
	}
	if len(second.Registered) != len(first.Registered) || second.ContentHash != first.ContentHash {
		t.Fatal("replayed receipt differs from the first result")
	}
	changed := append([]ReplayFixtureInput{}, input.Fixtures...)
	changed[0].TargetRef = "field/research_subject"
	if _, err := s.RegisterFixtures(replayCtx, actor, "register-idem-1", registerInput(draft, changed)); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("same key different request = %v", err)
	}

	// Identical content under a new key is the append-only no-op.
	if _, err := s.RegisterFixtures(ctx, actor, "register-idem-2", input); err != nil {
		t.Fatal(err)
	}
	if rows := countFixtureRows(t, db); rows != 4 {
		t.Fatalf("identical re-registration duplicated rows: %d", rows)
	}
	// Same fixture id, different immutable input is refused, never rewritten.
	mutated := append([]ReplayFixtureInput{}, input.Fixtures...)
	mutated[0].Input = template.EvaluationInput{ProjectID: "registration-2", TargetVersion: "1", Fields: map[string]string{"research_goal": "observable objective"}}
	if _, err := s.RegisterFixtures(ctx, actor, "register-idem-3", registerInput(draft, mutated)); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("append-only rewrite = %v", err)
	}
	if rows := countFixtureRows(t, db); rows != 4 {
		t.Fatalf("conflicting re-registration changed rows: %d", rows)
	}

	// The draft is re-saved with new content; the old pin stops answering and
	// the replayed operation key conflicts, while the old rows survive.
	def := template.Clone(draft.Definition)
	def.Rules = nil
	def.ContentHash = ""
	def.RulesetHash = ""
	def.Fields[0].Label = "renamed goal"
	if _, err := s.SaveDraft(ctx, actor, "resave-draft-1", SaveDraftInput{TemplateID: draft.TemplateID, Version: draft.Version, ExpectedRevision: draft.Revision, Definition: def, RuleSet: draft.RuleSet, SourceType: "lingdoc_builtin", Scope: "internal-demo", Curator: actor.UserID, Reason: "content changed"}); err != nil {
		t.Fatal(err)
	}
	resaved, err := s.GetVersion(ctx, actor, draft.TemplateID, draft.Version)
	if err != nil || resaved.ContentHash == draft.ContentHash {
		t.Fatalf("re-saved draft content = %v %v", resaved.ContentHash, err)
	}
	if _, err := s.RegisterFixtures(replayCtx, actor, "register-idem-1", input); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("replayed registration over a moved draft = %v", err)
	}
	if _, err := s.RegisterFixtures(ctx, actor, "register-after-resave", registerInput(resaved, registrationFixtureInputs())); err != nil {
		t.Fatalf("re-registration against the current pin = %v", err)
	}
	if rows := countFixtureRows(t, db); rows != 8 {
		t.Fatalf("old rows must survive append-only: %d", rows)
	}
}

func TestReplayCoverageSharedJudgementAgreesWithVerify(t *testing.T) {
	s, _, _, actor, draft, store := seedRegistrationService(t)
	ctx := context.Background()

	partial := registrationFixtureInputs()[:2]
	receipt, err := s.RegisterFixtures(ctx, actor, "register-partial-1", registerInput(draft, partial))
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Coverage.Complete {
		t.Fatal("half-covered matrix reported complete")
	}
	missingPolarityCells := 0
	for _, entry := range receipt.Coverage.Entries {
		if entry.Scene == "preflight" {
			if !entry.PositiveCovered {
				missingPolarityCells++
			}
			if !entry.NegativeCovered {
				missingPolarityCells++
			}
		}
		if entry.Scene == "write_check" && (!entry.PositiveCovered || !entry.NegativeCovered) {
			t.Fatalf("registered write_check cells reported missing: %+v", entry)
		}
	}
	if missingPolarityCells != 2 {
		t.Fatalf("preflight polarity cells missing = %d, want 2: %+v", missingPolarityCells, receipt.Coverage.Entries)
	}

	verifier := ExampleReplayVerifier{Reader: store}
	err = verifier.Verify(ctx, actor, draft, []string{"reg-write_check-positive", "reg-write_check-negative"})
	if err == nil || !strings.Contains(err.Error(), "goal-present/preflight") {
		t.Fatalf("verify with half coverage = %v", err)
	}
	remaining := registrationFixtureInputs()[2:]
	if _, err := s.RegisterFixtures(ctx, actor, "register-partial-2", registerInput(draft, remaining)); err != nil {
		t.Fatal(err)
	}
	if err := verifier.Verify(ctx, actor, draft, []string{"reg-write_check-positive", "reg-write_check-negative", "reg-preflight-positive", "reg-preflight-negative"}); err != nil {
		t.Fatalf("verify with full registered coverage = %v", err)
	}
	if err := verifier.Verify(ctx, actor, draft, []string{"reg-write_check-positive", "reg-write_check-positive"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("duplicate ids = %v", err)
	}
}

func TestTemplateReplayCoverageReadbackIsManagerOnly(t *testing.T) {
	s, _, _, actor, draft, _ := seedRegistrationService(t)
	ctx := context.Background()
	if _, err := s.RegisterFixtures(ctx, actor, "register-coverage-1", registerInput(draft, registrationFixtureInputs())); err != nil {
		t.Fatal(err)
	}
	view, err := s.ReplayCoverage(ctx, actor, draft.TemplateID, draft.Version, nil)
	if err != nil || !view.Complete || view.ContentHash != draft.ContentHash {
		t.Fatalf("manager coverage view = %+v %v", view, err)
	}
	reader := Actor{TenantID: actor.TenantID, UserID: "reader", Role: string(types.TenantRoleViewer)}
	if _, err := s.ReplayCoverage(ctx, reader, draft.TemplateID, draft.Version, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-manager coverage read = %v, want anti-enumerating 404", err)
	}
}
