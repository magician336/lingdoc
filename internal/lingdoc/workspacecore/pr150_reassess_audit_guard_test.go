package workspacecore

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestPR150ReassessMissingAuditRollsBackAndRecoversSameKey(t *testing.T) {
	s, a, p, source, _ := r25InverseFieldFixture(t)
	ctx := context.Background()
	raw, _, _, err := s.CreateChangeSet(ctx, a, p.ID, "pr150-audit-ordinary-proposal", CreateChangeSetInput{
		ExpectedContextRevision: p.CurrentContextRevision, Reason: "ordinary reviewed goal",
		Fields:             map[string]ChangeFieldInput{"research_goal": {OldValue: p.Spec["research_goal"], NewValue: "ordinary proposed goal"}},
		AffectedChapterIDs: []string{source.Impacts[0].ChapterID},
	})
	proposal := decodeAgentJSON[ChangeSet](t, raw, err)
	raw, _, _, err = s.CreateChangeSet(ctx, a, p.ID, "pr150-audit-intervening", CreateChangeSetInput{
		ExpectedContextRevision: p.CurrentContextRevision, Reason: "independent subject revision",
		Fields:             map[string]ChangeFieldInput{"research_subject": {OldValue: p.Spec["research_subject"], NewValue: "independent subject"}},
		AffectedChapterIDs: []string{source.Impacts[0].ChapterID},
	})
	other := decodeAgentJSON[ChangeSet](t, raw, err)
	if _, status, _, err := s.ApplyChangeSet(ctx, a, p.ID, other.ID, "pr150-audit-intervening-apply"); err != nil || status != 200 {
		t.Fatalf("intervening apply: status=%d err=%v", status, err)
	}
	if _, status, _, err := s.ApplyChangeSet(ctx, a, p.ID, proposal.ID, "pr150-audit-mark-stale"); err != nil || status != 409 {
		t.Fatalf("mark ordinary proposal stale: status=%d err=%v", status, err)
	}
	stale, err := s.GetChangeSet(ctx, a, p.ID, proposal.ID)
	if err != nil || stale.Status != "stale" || stale.ReversesChangeSetID != "" {
		t.Fatalf("requires a real ordinary stale proposal: %+v err=%v", stale, err)
	}
	state := func() string {
		t.Helper()
		return pr150RecoveryState(t, s, a, p.ID, proposal.ID)
	}
	counts := func() [4]int64 {
		t.Helper()
		var result [4]int64
		for i, model := range []any{&changeSetRow{}, &operationRow{}, &projectRecordRow{}} {
			if err := s.testDB().Model(model).Count(&result[i]).Error; err != nil {
				t.Fatal(err)
			}
		}
		if err := s.testDB().Table("lingdoc_project_audits").Count(&result[3]).Error; err != nil {
			t.Fatal(err)
		}
		return result
	}
	beforeState, beforeCounts := state(), counts()
	key := "pr150-ordinary-reassess-no-audit"
	withoutAudit := *s
	withoutAudit.repository = auditlessRepository{inner: s.repository}
	raw, status, replayed, err := withoutAudit.ReassessChangeSet(ctx, a, p.ID, proposal.ID, key)
	if !errors.Is(err, ErrInvalidState) || raw != nil || status != 0 || replayed {
		t.Fatalf("missing audit capability was accepted: status=%d replay=%v body=%s err=%v", status, replayed, raw, err)
	}
	if afterState, afterCounts := state(), counts(); afterState != beforeState || afterCounts != beforeCounts {
		t.Fatalf("missing-audit rejection wrote business/history/operation/audit state: rows %v -> %v", beforeCounts, afterCounts)
	}
	first, status, replayed, err := s.ReassessChangeSet(ctx, a, p.ID, proposal.ID, key)
	if err != nil || status != 201 || replayed {
		t.Fatalf("audit recovery consumed key or source: status=%d replay=%v err=%v", status, replayed, err)
	}
	reassessed := decodeAgentJSON[ChangeSet](t, first, err)
	if reassessed.Status != "assessed" || reassessed.ReassessedFromID != proposal.ID || reassessed.ReversesChangeSetID != "" {
		t.Fatalf("ordinary assessed recovery changed semantics: %+v", reassessed)
	}
	var operations int64
	if err := s.testDB().Model(&operationRow{}).Where("key = ?", key).Count(&operations).Error; err != nil || operations != 1 {
		t.Fatalf("recovered operation count=%d err=%v", operations, err)
	}
	events, err := s.ListAuditEvents(ctx, a, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	businessAudits := 0
	for _, event := range events {
		if event.Action == "project.change_set.reassess" && event.Target == reassessed.ID {
			businessAudits++
		}
	}
	if businessAudits != 1 {
		t.Fatalf("recovered reassess business audits=%d, want 1", businessAudits)
	}
	afterSuccessState, afterSuccessCounts := state(), counts()
	again, status, replayed, err := s.ReassessChangeSet(ctx, a, p.ID, proposal.ID, key)
	if err != nil || status != 201 || !replayed || !bytes.Equal(first, again) {
		t.Fatalf("recovered reassess lost-response replay: status=%d replay=%v err=%v", status, replayed, err)
	}
	if state() != afterSuccessState || counts() != afterSuccessCounts {
		t.Fatal("reassessment replay added business/history/operation/audit writes")
	}
}
