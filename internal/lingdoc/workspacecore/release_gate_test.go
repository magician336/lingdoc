package workspacecore

import (
	"context"
	"errors"
	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	"github.com/Tencent/WeKnora/internal/types"
	"strconv"
	"testing"
	"time"
)

func TestReceiptPinsBasisAndMetadata(t *testing.T) {
	s, a, p := extendedImpactStore(t)
	basis, err := s.ReadPreflightBasis(context.Background(), a, p.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	evaluations, err := template.Evaluate(p.TemplateCopy.Definition, template.EvaluationInput{ProjectID: p.ID, TargetVersion: strconv.FormatInt(p.CurrentContextRevision, 10), Fields: p.Spec})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := s.StoreImpactComputationReceipt(context.Background(), a, basis, "actual-synthetic-template-input", "blocked", evaluations, basis.SourceIDs)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.InputHash == "" || receipt.SourceBasisHash == "" || receipt.ComputationBasisHash == "" || len(receipt.Evaluations) == 0 {
		t.Fatal("receipt fabricated or omitted actual evaluation basis")
	}
	if err := s.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		return tx.(impactTransaction).InsertImpactTask(ImpactTask{ID: "metadata-only-task", ProjectID: p.ID, ChangeSetID: "synthetic-only", Target: ObjectVersionRef{Kind: "ProjectSpec", ID: p.ID, Version: "1"}, TargetRevision: p.CurrentContextRevision, Action: "REVIEW_REQUIRED", Status: "OPEN", Version: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadImpactComputationReceipt(context.Background(), a, p.ID, receipt.ID); err != nil {
		t.Fatalf("task metadata made receipt stale: %v", err)
	}
	if _, err := s.StoreImpactComputationReceipt(context.Background(), a, basis, "actual-input", "passed", evaluations, []string{"forged-source"}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("source mismatch was ignored: %v", err)
	}
	// An actual formal edit invalidates the before-check basis.
	chapters, err := s.ListChapters(context.Background(), a, p.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("actual affected chapters: %v", err)
	}
	raw, _, _, err := s.CreateChangeSet(context.Background(), a, p.ID, "receipt-stale-change", CreateChangeSetInput{ExpectedContextRevision: p.CurrentContextRevision, Reason: "合成正式编辑", AffectedChapterIDs: []string{chapters[0].ID}, Fields: map[string]ChangeFieldInput{"research_goal": {OldValue: p.Spec["research_goal"], NewValue: "修改后的条件"}}})
	change := decodeAgentJSON[ChangeSet](t, raw, err)
	if _, _, _, err := s.ApplyChangeSet(context.Background(), a, p.ID, change.ID, "receipt-stale-apply"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StoreImpactComputationReceipt(context.Background(), a, basis, "actual-input", "blocked", evaluations, basis.SourceIDs); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale after-computation persisted: %v", err)
	}
	if _, err := s.ReadImpactComputationReceipt(context.Background(), a, p.ID, receipt.ID); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("old receipt falsely current: %v", err)
	}
}
func TestSemanticReassessmentRejectsMeaningConflict(t *testing.T) {
	f := newAgentFixture(t, false)
	claim := f.claim(t)
	original := semanticDraft(t, f.svc, f.owner, f.project, "reassess-original", SemanticDelta{Kind: "ResearchClaim", TargetID: claim.ID, ExpectedVersion: claim.Version, ProposedClaim: &SaveResearchClaimInput{Text: "拟议语义", Scope: claim.Scope, Status: "active", Confirmed: true}})
	if err := f.svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		// Append-only metadata updates may change the version without changing intent.
		now, row, e := loadStoredObject[ResearchClaim](tx, f.project.ID, ResearchClaimKind, claim.ID)
		if e != nil {
			return e
		}
		now.Version++
		now.Reason = "metadata reviewed"
		if e := storeStoredObject(tx, row, f.project.ID, ResearchClaimKind, claim.ID, now.Version, now); e != nil {
			return e
		}
		plan, e := f.svc.ReassessSemanticPlan(context.Background(), tx, f.owner, f.project, "new-reassessment", original)
		if e != nil {
			return e
		}
		if len(plan) != 1 || plan[0].ExpectedVersion != 2 || plan[0].ProposedClaim.Text != "拟议语义" {
			t.Fatal("reassessment dropped or altered semantic intent")
		}
		now, row, e = loadStoredObject[ResearchClaim](tx, f.project.ID, ResearchClaimKind, claim.ID)
		if e != nil {
			return e
		}
		now.Version++
		now.Scope = "另一次人工实际修改"
		if e := storeStoredObject(tx, row, f.project.ID, ResearchClaimKind, claim.ID, now.Version, now); e != nil {
			return e
		}
		if _, e := f.svc.ReassessSemanticPlan(context.Background(), tx, f.owner, f.project, "conflicted-reassessment", original); !errors.Is(e, ErrVersionConflict) {
			t.Fatal("real meaning conflict silently rebased")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSemanticGraphInvalidatesOnlyReferencedChapter(t *testing.T) {
	f := newAgentFixture(t, false)
	claim := f.claim(t)
	chapter := extendedChapter(t, f.svc, f.owner, f.project)
	if err := f.svc.testDB().Exec("UPDATE lingdoc_chapter_versions SET confirmation_valid = ? WHERE project_id = ? AND id = ?", true, f.project.ID, *chapter.CurrentVersionID).Error; err != nil {
		t.Fatal(err)
	}
	p, err := f.svc.GetProject(context.Background(), f.owner, f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := f.svc.CreateDependencyEdge(context.Background(), f.owner, p.ID, "claim-chapter-dependency", DependencyEdgeInput{ExpectedContextRevision: p.CurrentContextRevision, Source: ObjectVersionRef{Kind: "ResearchClaim", ID: claim.ID, Version: strconv.FormatInt(claim.Version, 10)}, Target: ObjectVersionRef{Kind: "ChapterVersion", ID: chapter.ID, Version: *chapter.CurrentVersionID}, Action: "REVIEW_REQUIRED", Relation: "actual_claim_reference", Origin: "user", Reason: "用户登记的合成真实引用关系"}); err != nil {
		t.Fatal(err)
	}
	c := semanticDraft(t, f.svc, f.owner, p, "graph-semantic-draft", SemanticDelta{Kind: "ResearchClaim", TargetID: claim.ID, ExpectedVersion: claim.Version, ProposedClaim: &SaveResearchClaimInput{Text: "修改实际被章节使用的主张", Scope: claim.Scope, Status: "active", Confirmed: true}})
	if len(c.Impacts) != 0 {
		t.Fatal("test must not rely on manually declared affected chapters")
	}
	semanticApply(t, f.svc, f.owner, p, c, "graph-semantic-apply")
	chapters, err := f.svc.ListChapters(context.Background(), f.owner, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, now := range chapters {
		if now.ID == chapter.ID {
			found = true
		}
		if now.ID == chapter.ID && now.ConfirmationValid {
			t.Fatal("graph task was created but actual current chapter confirmation remained valid")
		}
	}
	if !found {
		t.Fatal("referenced current chapter was hidden instead of invalidated")
	}
	after, err := f.svc.GetProject(context.Background(), f.owner, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.SpecRevision != p.SpecRevision {
		t.Fatal("test invalidated by unrelated global SpecRevision instead of exact graph target")
	}
}
func TestHumanDecisionsCannotBeBypassedOutsideHTTP(t *testing.T) {
	s, a, p := extendedImpactStore(t)
	ctx := types.WithPrincipal(context.Background(), types.Principal{Type: types.PrincipalAPITenant, ID: "synthetic-api-principal"})
	if _, _, _, err := s.StabilizeRevision(ctx, a, p.ID, "any-change", "human-stabilize"); !errors.Is(err, ErrHumanDecisionRequired) {
		t.Fatalf("machine stabilized through non HTTP entry: %v", err)
	}
	if _, _, _, err := s.SignTrialDecision(ctx, a, p.ID, "any-decision", "human-sign", TrialDecisionSignInput{ExpectedVersion: 1, Responsibility: "qa", Reason: "synthetic signing"}); !errors.Is(err, ErrHumanDecisionRequired) {
		t.Fatalf("machine signed through non HTTP entry: %v", err)
	}
	if _, _, _, err := s.ControlRuntime(ctx, a, p.ID, "human-pause", RuntimeControlInput{Scope: RuntimeScope{Kind: "project", Value: p.ID}, Action: "pause", Reason: "synthetic pause"}); !errors.Is(err, ErrHumanDecisionRequired) {
		t.Fatalf("machine pause bypassed domain guard: %v", err)
	}
}
