package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// 交付门禁: the delivery gate must reject READY/freeze/export for a reason the
// client can act on. StabilizeRevision already returns a structured blocker
// list; AssertRevisionStabilized is the gate that READY and export actually
// call, and it must carry the same machine-readable blocking reason instead of
// an opaque ErrInvalidState.

// applySyntheticChangeForGate applies one real ChangeSet on the current revision
// so the applied ChangeSet is present but not stabilized by Owner.
func applySyntheticChangeForGate(t *testing.T, s *Service, a Actor, p Project, key string) ChangeSet {
	t.Helper()
	chapter := extendedChapter(t, s, a, p)
	raw, _, _, err := s.CreateChangeSet(context.Background(), a, p.ID, key+"-create", CreateChangeSetInput{
		ExpectedContextRevision: p.CurrentContextRevision,
		Fields:                  map[string]ChangeFieldInput{"research_goal": {OldValue: p.Spec["research_goal"], NewValue: "交付门禁 合成修改后的目标"}},
		AffectedChapterIDs:      []string{chapter.ID},
		Reason:                  "交付门禁 合成正式变更",
	})
	if err != nil {
		t.Fatal(err)
	}
	change := g2Decode[ChangeSet](t, raw, err)
	if _, _, _, err := s.ApplyChangeSet(context.Background(), a, p.ID, change.ID, key+"-apply"); err != nil {
		t.Fatal(err)
	}
	changes, err := s.ListChangeSets(context.Background(), a, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range changes {
		if item.ID == change.ID {
			return item
		}
	}
	t.Fatalf("applied ChangeSet %s is not listed", change.ID)
	return change
}

// A current applied-but-unstabilized ChangeSet must block the delivery gate,
// and the returned error must name the stabilizer step rather than surfacing a
// bare invalid-state.
func TestDeliveryGateRejectsUnstabilizedWithNamedReason(t *testing.T) {
	s, a, p := extendedImpactStore(t)
	change := applySyntheticChangeForGate(t, s, a, p, "gate-unstabilized")
	if change.Status != "applied" {
		t.Fatalf("precondition status = %q, want applied", change.Status)
	}
	err := s.AssertRevisionStabilized(context.Background(), a, p.ID)
	if !errors.Is(err, ErrRevisionNotStable) {
		t.Fatalf("unstabilized revision gate error = %v, want ErrRevisionNotStable", err)
	}
	if !strings.Contains(err.Error(), change.ID) {
		t.Fatalf("gate rejection %q does not name the blocking ChangeSet %s", err, change.ID)
	}
}

// StabilizeRevision and the release gate must agree on the same current
// revision. This fixture has no rules checker and always carries a real open
// impact task, so the achievable end state is BLOCKED; the positive
// STABILIZED -> gate opens case is proven on the real delivery guard fixture
// in the workspace package (TestDeliveryGateFreezeAndExportRequireStabilization).
func TestDeliveryGateFollowsStabilizeResult(t *testing.T) {
	s, a, p := extendedImpactStore(t)
	change := applySyntheticChangeForGate(t, s, a, p, "gate-agree")
	if err := s.AssertRevisionStabilized(context.Background(), a, p.ID); !errors.Is(err, ErrRevisionNotStable) {
		t.Fatalf("gate before stabilization = %v, want ErrRevisionNotStable", err)
	}
	raw, _, _, err := s.StabilizeRevision(context.Background(), a, p.ID, change.ID, "gate-agree-stabilize")
	if err != nil {
		t.Fatal(err)
	}
	var result StabilizationResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "BLOCKED" {
		t.Fatalf("this fixture cannot reach STABILIZED; got %+v", result)
	}
	if len(result.Blockers) == 0 {
		t.Fatal("BLOCKED result must carry machine-readable blockers")
	}
	if err := s.AssertRevisionStabilized(context.Background(), a, p.ID); !errors.Is(err, ErrRevisionNotStable) {
		t.Fatalf("gate after a BLOCKED result = %v, want ErrRevisionNotStable", err)
	}
}

// When stabilization is genuinely BLOCKED, the gate must not pass. This is the
// "blocked -> repair -> retry" regression for the independent delivery gate.
func TestBlockedStabilizationNeverOpensGate(t *testing.T) {
	s, a, p := extendedImpactStore(t)
	change := applySyntheticChangeForGate(t, s, a, p, "gate-blocked")
	raw, _, _, err := s.StabilizeRevision(context.Background(), a, p.ID, change.ID, "gate-blocked-stabilize")
	if err != nil {
		t.Fatal(err)
	}
	var result StabilizationResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result.Status == "STABILIZED" {
		if err := s.AssertRevisionStabilized(context.Background(), a, p.ID); err != nil {
			t.Fatalf("STABILIZED result must open the gate: %v", err)
		}
		return
	}
	if len(result.Blockers) == 0 {
		t.Fatal("a BLOCKED stabilization result must carry at least one machine-readable blocker")
	}
	if err := s.AssertRevisionStabilized(context.Background(), a, p.ID); err == nil {
		t.Fatal("a BLOCKED stabilization result opened the delivery gate")
	}
}

// A dismissed task is not closure evidence. Even if a BLOCK_RELEASE row for the
// current revision is somehow marked DISMISSED rather than DONE, the release
// gate must keep rejecting: dismissal never stands in for a current preflight
// proof.
func TestDismissedTaskIsNotClosureForGate(t *testing.T) {
	s, a, p := extendedImpactStore(t)
	task := ImpactTask{
		ID: "gate-dismissed-block-release", ProjectID: p.ID, ChangeSetID: "gate-synthetic",
		Target:         ObjectVersionRef{Kind: "ProjectSpec", ID: p.ID, Version: "1"},
		TargetRevision: p.CurrentContextRevision, Action: "BLOCK_RELEASE",
		Severity: DeriveImpactSeverity("BLOCK_RELEASE"), Status: "DISMISSED", Version: 1,
	}
	if err := s.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		return tx.(impactTransaction).InsertImpactTask(task)
	}); err != nil {
		t.Fatal(err)
	}
	err := s.AssertRevisionStabilized(context.Background(), a, p.ID)
	if err == nil {
		t.Fatal("a dismissed BLOCK_RELEASE task opened the delivery gate")
	}
	if !strings.Contains(err.Error(), task.ID) {
		t.Fatalf("gate rejection %q does not name the dismissed blocking task %s", err, task.ID)
	}
}

// The gate is a real Owner-facing HTTP decision; a machine principal must not
// reach the release path through it.
func TestDeliveryGateStaysHumanScoped(t *testing.T) {
	s, a, p := extendedImpactStore(t)
	ctx := types.WithPrincipal(context.Background(), types.Principal{Type: types.PrincipalAPITenant, ID: "gate-machine"})
	if _, _, _, err := s.StabilizeRevision(ctx, a, p.ID, "gate-any", "gate-machine-stabilize"); !errors.Is(err, ErrHumanDecisionRequired) {
		t.Fatalf("machine principal stabilized a revision: %v", err)
	}
}
