package workspacecore

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type r34AlteredPreflightChecker struct {
	base  *r34PreflightChecker
	alter func(*ImpactPreflightEvidence)
}

func (c r34AlteredPreflightChecker) Recheck(ctx context.Context, actor Actor, pid, id string) (ImpactPreflightEvidence, error) {
	proof, err := c.base.Recheck(ctx, actor, pid, id)
	if err == nil {
		c.alter(&proof)
	}
	return proof, err
}

func TestR34RefreshRejectsNonCurrentOrMismatchedProofWithoutMutation(t *testing.T) {
	s, actor, p := extendedG3G6Store(t)
	ctx := context.Background()
	proofID, _ := r34PersistedProof(t, s, actor, p, "policy")
	now := time.Now().UTC()
	task := ImpactTask{ID: "r34-negative-done", ProjectID: p.ID, ChangeSetID: "r34-untouched-change", Target: ObjectVersionRef{Kind: "ProjectSpec", ID: p.ID, Version: "1"}, TargetRevision: p.CurrentContextRevision, Action: "BLOCK_RELEASE", Status: "DONE", Severity: "blocking", Version: 2, ClosureEvidence: []ObjectVersionRef{{Kind: "DeliveryPreflight", ID: "previous-proof", Version: "previous-hash"}}, CreatedAt: now, UpdatedAt: now}
	if err := s.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error { return tx.(impactTransaction).InsertImpactTask(task) }); err != nil {
		t.Fatal(err)
	}
	before := r34Tasks(t, s, actor, p.ID)
	for _, tc := range []struct {
		name  string
		alter func(*ImpactPreflightEvidence)
	}{
		{"ruleset_mismatch", func(p *ImpactPreflightEvidence) { p.RulesetHash = "not-the-current-rules" }},
		{"not_current", func(p *ImpactPreflightEvidence) { p.IsCurrent = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s.SetImpactPreflightChecker(r34AlteredPreflightChecker{base: &r34PreflightChecker{service: s}, alter: tc.alter})
			if _, _, _, err := s.AcknowledgeImpactPreflight(ctx, actor, p.ID, task.ChangeSetID, proofID, "r34-negative-"+tc.name); !errors.Is(err, ErrVersionConflict) {
				t.Fatalf("noncurrent proof reached mutation or wrong failure: %v", err)
			}
			if !reflect.DeepEqual(before, r34Tasks(t, s, actor, p.ID)) {
				t.Fatal("rejected proof modified task state")
			}
		})
	}
}
