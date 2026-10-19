package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Tencent/WeKnora/internal/types"
	"strconv"
	"testing"
)

func TestCandidateSelectorReadsUncommittedClaimChange(t *testing.T) {
	f := newG2Fixture(t, false)
	g2SeedNativeEvidence(t, f)
	seedCandidateProofFoundation(t, f)
	anchor := f.anchor(t, "source-1", "candidate-tx-anchor")
	claim := f.claim(t)
	card := f.card(t, []SourceAnchor{anchor}, &claim, "candidate-tx-card", "supports")
	raw, _, _, err := f.svc.ConfirmEvidenceCard(context.Background(), f.owner, f.project.ID, card.ID, "candidate-tx-confirm", G2ConfirmedInput{G2Expectation: f.expectation(card.Version), Confirmed: true, Reason: "人工核对合成原文和适用范围"})
	card = g2Decode[EvidenceCard](t, raw, err)
	refs := []G2VersionRef{{ID: card.ID, Version: card.Version, Kind: "EvidenceCard"}}
	ctx := types.WithExecutionTenant(types.WithCaller(context.Background(), types.Caller{TenantID: f.owner.TenantID, UserID: f.owner.UserID, Role: f.owner.Role}), f.owner.TenantID)
	prepared, _, err := f.svc.PrepareCandidateGenerationEvidence(ctx, f.owner, f.project.ID, f.chapter.ID, refs, []string{"source-1"})
	if err != nil {
		t.Fatal(err)
	}
	abort := errors.New("rollback synthetic parent tx")
	err = f.svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		selected, e := f.svc.SelectGenerationEvidenceInTransaction(prepared, tx, f.owner, f.project.ID, f.chapter.ID, refs, true, []string{"source-1"})
		if e != nil {
			return e
		}
		if len(selected.Cards) != 1 {
			t.Fatal("no genuine current card selected")
		}
		current, old, e := g2Load[ResearchClaim](tx, f.project.ID, G2ClaimKind, claim.ID)
		if e != nil {
			return e
		}
		current.Version++
		current.Scope = "已经改变的合成范围"
		if e = g2Store(tx, old, f.project.ID, G2ClaimKind, current.ID, current.Version, current); e != nil {
			return e
		}
		if _, e = f.svc.SelectGenerationEvidenceInTransaction(prepared, tx, f.owner, f.project.ID, f.chapter.ID, refs, true, []string{"source-1"}); !errors.Is(e, ErrG2EvidenceIncomplete) {
			t.Fatalf("selector missed actual uncommitted claim revision: %v", e)
		}
		return abort
	})
	if !errors.Is(err, abort) {
		t.Fatal(err)
	}
	if err = f.svc.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		actual, _, e := g2Load[ResearchClaim](tx, f.project.ID, G2ClaimKind, claim.ID)
		if e != nil {
			return e
		}
		if actual.Version != claim.Version {
			t.Fatal("aborted parent tx leaked claim write")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.source.denied["source-1"] = true
	if _, _, e := f.svc.PrepareCandidateGenerationEvidence(ctx, f.owner, f.project.ID, f.chapter.ID, refs, []string{"source-1"}); e == nil {
		t.Fatal("preparation skipped current external source revocation")
	}

}

func TestClaimReviewRecordsDisputeOrRetirement(t *testing.T) {
	for _, status := range []string{"disputed", "superseded"} {
		t.Run(status, func(t *testing.T) {
			f := newG2Fixture(t, false)
			claim := f.claim(t)
			c := semanticDraft(t, f.svc, f.owner, f.project, "claim-status-draft", SemanticDelta{Kind: "ResearchClaim", TargetID: claim.ID, ExpectedVersion: claim.Version, ProposedClaim: &SaveResearchClaimInput{Text: claim.Text, Scope: claim.Scope, Status: status, Explanation: "用户明确记录争议或退役", Confirmed: true}})
			semanticApply(t, f.svc, f.owner, f.project, c, "claim-status")
			p, err := f.svc.GetProject(context.Background(), f.owner, f.project.ID)
			if err != nil {
				t.Fatal(err)
			}
			tasks, err := f.svc.ListImpactTasks(context.Background(), f.owner, p.ID)
			if err != nil {
				t.Fatal(err)
			}
			var task ImpactTask
			for _, candidate := range tasks {
				if candidate.Target.Kind == "ResearchClaim" && candidate.Target.ID == claim.ID && candidate.Action == "REVIEW_REQUIRED" {
					task = candidate
				}
			}
			if task.ID == "" {
				t.Fatal("no actual claim review task")
			}
			raw, _, _, err := f.svc.ActOnImpactTask(context.Background(), f.owner, p.ID, task.ID, "claim-review-assign", ImpactTaskActionInput{ExpectedVersion: task.Version, TargetRevision: p.CurrentContextRevision, Action: "assign", AssigneeID: f.owner.UserID, Reason: "指定本人复核"})
			task = g2Decode[ImpactTask](t, raw, err)
			ref := ObjectVersionRef{Kind: "ResearchClaim", ID: claim.ID, Version: strconv.FormatInt(claim.Version+1, 10)}
			raw, _, _, err = f.svc.ActOnImpactTask(context.Background(), f.owner, p.ID, task.ID, "claim-review-complete", ImpactTaskActionInput{ExpectedVersion: task.Version, TargetRevision: p.CurrentContextRevision, Action: "complete", HumanDecision: "reviewed", ClosureEvidence: []ObjectVersionRef{ref}, Reason: "已核对当前争议状态，未声称主张获证"})
			task = g2Decode[ImpactTask](t, raw, err)
			if task.Status != "DONE" || task.HumanDecision != "reviewed" {
				t.Fatal("explicit current claim review could not close")
			}
			if err = f.svc.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
				current, e := currentImpactObject(tx, p, ref)
				if e != nil {
					return e
				}
				if current.Confirmed || current.State != status {
					t.Fatal("review forged scientific confirmation or changed user decision")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func seedCandidateProofFoundation(t *testing.T, f g2Fixture) {
	t.Helper()
	db := f.svc.testDB()
	for _, table := range []string{"knowledges", "chunks"} {
		if !db.Migrator().HasColumn(table, "tenant_id") {
			if err := db.Exec("ALTER TABLE " + table + " ADD COLUMN tenant_id BIGINT NOT NULL DEFAULT 0").Error; err != nil {
				t.Fatal(err)
			}
		}
		if !db.Migrator().HasColumn(table, "knowledge_base_id") {
			if err := db.Exec("ALTER TABLE " + table + " ADD COLUMN knowledge_base_id TEXT NOT NULL DEFAULT 'synthetic-kb'").Error; err != nil {
				t.Fatal(err)
			}
		}
		if err := db.Table(table).Where("1=1").Update("tenant_id", f.owner.TenantID).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec("CREATE TABLE IF NOT EXISTS knowledge_bases(id TEXT PRIMARY KEY,tenant_id BIGINT NOT NULL,deleted_at DATETIME)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO knowledge_bases(id,tenant_id) VALUES(?,?)", "synthetic-kb", f.owner.TenantID).Error; err != nil {
		t.Fatal(err)
	}
	policy := ProjectAssetPolicy{ID: "asset-1", ProjectID: f.project.ID, AssetID: "asset-1", AssetRevision: 1, Version: 1, PolicyVersion: 1, Scope: "project", Purpose: "合成测试", ModelUseAllowed: true, CreatedBy: f.owner.UserID, GrantedBy: f.owner.UserID, Reason: "合成显式许可"}
	raw, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		return tx.(G2Transaction).InsertG2Object(G2StoredObject{ProjectID: f.project.ID, Kind: G2AssetPolicyKind, ID: policy.ID, Version: 1, Payload: raw})
	}); err != nil {
		t.Fatal(err)
	}
}
