package workspacecore

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
)

// These are service regressions over testStore's production-migration SQLite
// rows. No AutoMigrate, fabricated inverse row, or helper-only mirror is used.
func r25InverseFieldFixture(t *testing.T) (*Service, Actor, Project, ChangeSet, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "inverse.db")
	s := testStore(t, path)
	a := Actor{TenantID: 925, UserID: "owner"}
	p := seedActiveImpactProject(t, s, a)
	chapters, err := s.ListChapters(context.Background(), a, p.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("chapters: %v", err)
	}
	raw, _, _, err := s.CreateChangeSet(context.Background(), a, p.ID, "r25-original-create", CreateChangeSetInput{
		ExpectedContextRevision: p.CurrentContextRevision, Reason: "new research goal",
		Fields:             map[string]ChangeFieldInput{"research_goal": {OldValue: p.Spec["research_goal"], NewValue: "changed goal"}},
		AffectedChapterIDs: []string{chapters[0].ID},
	})
	source := decodeAgentJSON[ChangeSet](t, raw, err)
	raw, status, replayed, err := s.ApplyChangeSet(context.Background(), a, p.ID, source.ID, "r25-original-apply")
	if err != nil || status != 200 || replayed {
		t.Fatalf("apply original: status=%d replay=%v err=%v", status, replayed, err)
	}
	source = decodeAgentJSON[ChangeSet](t, raw, err)
	p, err = s.GetProject(context.Background(), a, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	return s, a, p, source, path
}

func r25InverseOperationRecord(t *testing.T, s *Service, a Actor, pid, sourceID, key string) OperationResult {
	t.Helper()
	var result OperationResult
	err := s.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		var found bool
		var err error
		result, found, err = tx.Operation(OperationIdentity{Actor: a, Operation: "reverseChangeSet", Target: pid + "/" + sourceID, Key: key})
		if err == nil && !found {
			t.Fatal("missing durable reverse operation")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// Compare the complete persisted/public ChangeSet meaning. Hydration may turn
// an omitted empty semantic plan into [] and DB time scans need not retain a
// time.Location pointer or monotonic clock. No business field is dropped.
func r25ChangeSetPersistenceEqual(want, got ChangeSet) bool {
	if !want.CreatedAt.Equal(got.CreatedAt) || !r25PersistenceTimeEqual(want.AppliedAt, got.AppliedAt) || !r25PersistenceTimeEqual(want.DecidedAt, got.DecidedAt) {
		return false
	}
	canonical := func(change ChangeSet) ChangeSet {
		change.CreatedAt = change.CreatedAt.UTC()
		if change.AppliedAt != nil {
			value := change.AppliedAt.UTC()
			change.AppliedAt = &value
		}
		if change.DecidedAt != nil {
			value := change.DecidedAt.UTC()
			change.DecidedAt = &value
		}
		return change
	}
	return hashImpactValue(canonical(want)) == hashImpactValue(canonical(got))
}

func r25PersistenceTimeEqual(want, got *time.Time) bool {
	if want == nil || got == nil {
		return want == nil && got == nil
	}
	return want.Equal(*got)
}

func r25AssertInverseHistory(t *testing.T, s *Service, a Actor, pid string, source, inverse ChangeSet) {
	t.Helper()
	persisted, err := s.GetChangeSet(context.Background(), a, pid, inverse.ID)
	if err != nil || persisted.ReversesChangeSetID != source.ID {
		t.Fatalf("durable lineage: change=%+v err=%v", persisted, err)
	}
	unchanged, err := s.GetChangeSet(context.Background(), a, pid, source.ID)
	if err != nil || !r25ChangeSetPersistenceEqual(source, unchanged) {
		t.Fatalf("original history changed: before=%+v after=%+v err=%v", source, unchanged, err)
	}
	err = s.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		var history changeProvenance
		_, err := tx.(projectRecordTransaction).GetProjectRecord(pid, "g1_change_provenance", inverse.ID, &history)
		if err == nil && !reflect.DeepEqual(history.InheritedChangeIDs, []string{source.ID}) {
			t.Fatalf("inverse lost historical G1 ancestry: %+v", history)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestR25ReverseLostResponseAfterApplyAndReopen(t *testing.T) {
	s, a, p, source, path := r25InverseFieldFixture(t)
	ctx := context.Background()
	key := "r25-inverse-lost-response"
	first, status, replayed, err := s.ReverseChangeSet(ctx, a, p.ID, source.ID, key)
	if err != nil || status != 201 || replayed {
		t.Fatalf("reverse: status=%d replay=%v err=%v", status, replayed, err)
	}
	inverse := decodeAgentJSON[ChangeSet](t, first, err)
	if inverse.Status != "assessed" || inverse.BaseContextRevision != p.CurrentContextRevision || inverse.ReversesChangeSetID != source.ID {
		t.Fatalf("inverse proposal: %+v", inverse)
	}
	operation := r25InverseOperationRecord(t, s, a, p.ID, source.ID, key)
	if _, status, _, err := s.ApplyChangeSet(ctx, a, p.ID, inverse.ID, "r25-inverse-apply"); err != nil || status != 200 {
		t.Fatalf("apply inverse: status=%d err=%v", status, err)
	}
	s = reopenSourcePolicyStore(t, s, path)
	again, againStatus, replayed, err := s.ReverseChangeSet(ctx, a, p.ID, source.ID, key)
	if err != nil || !replayed || againStatus != status || !bytes.Equal(first, again) {
		t.Fatalf("lost-response replay after inverse applied/reopen: status=%d replay=%v same=%v err=%v", againStatus, replayed, bytes.Equal(first, again), err)
	}
	if got := r25InverseOperationRecord(t, s, a, p.ID, source.ID, key); !reflect.DeepEqual(operation, got) {
		t.Fatal("replay replaced the committed operation")
	}
	r25AssertInverseHistory(t, s, a, p.ID, source, inverse)
	var linked int
	if err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		changes, err := tx.(changeSetTransaction).ChangeSets(p.ID)
		for _, c := range changes {
			if c.ReversesChangeSetID == source.ID {
				linked++
			}
		}
		return err
	}); err != nil || linked != 1 {
		t.Fatalf("replay duplicated inverse: count=%d err=%v", linked, err)
	}
	current, err := s.GetProject(ctx, a, p.ID)
	if err != nil || current.Spec["research_goal"] != source.Fields[0].OldValue || current.CurrentContextRevision != p.CurrentContextRevision+1 {
		t.Fatalf("inverse rewound or duplicated formal state: %+v err=%v", current, err)
	}
	appliedInverse, err := s.GetChangeSet(ctx, a, p.ID, inverse.ID)
	if err != nil {
		t.Fatal(err)
	}
	if source.TargetContextRevision == nil || source.TargetSpecRevision == nil ||
		appliedInverse.TargetContextRevision == nil || appliedInverse.TargetSpecRevision == nil {
		t.Fatalf("applied changes lost target revisions: source=%+v inverse=%+v", source, appliedInverse)
	}
	if *source.TargetContextRevision != source.BaseContextRevision+1 ||
		*source.TargetSpecRevision != source.BaseSpecRevision+1 ||
		p.CurrentContextRevision != *source.TargetContextRevision || p.SpecRevision != *source.TargetSpecRevision ||
		appliedInverse.Status != "applied" || appliedInverse.ReversesChangeSetID != source.ID ||
		appliedInverse.BaseContextRevision != *source.TargetContextRevision ||
		appliedInverse.BaseSpecRevision != *source.TargetSpecRevision ||
		*appliedInverse.TargetContextRevision != appliedInverse.BaseContextRevision+1 ||
		*appliedInverse.TargetSpecRevision != appliedInverse.BaseSpecRevision+1 ||
		current.CurrentContextRevision != *appliedInverse.TargetContextRevision || current.SpecRevision != *appliedInverse.TargetSpecRevision {
		t.Fatalf("inverse must append exactly one context/spec revision: source=%+v inverse=%+v current=%+v", source, appliedInverse, current)
	}
	history, err := s.ListContextRevisions(ctx, a, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 3 {
		t.Fatalf("want baseline, source target and inverse target snapshots; got %+v", history)
	}
	wantChanges := []string{source.ID, source.ID, inverse.ID}
	wantGoals := []string{source.Fields[0].OldValue, source.Fields[0].NewValue, source.Fields[0].OldValue}
	for i, snapshot := range history {
		wantContext := source.BaseContextRevision + int64(i)
		wantSpecRevision := source.BaseSpecRevision + int64(i)
		wantSpec := maps.Clone(p.Spec)
		wantSpec["research_goal"] = wantGoals[i]
		if snapshot.ProjectID != p.ID || snapshot.Revision != wantContext || snapshot.SpecRevision != wantSpecRevision ||
			snapshot.ChangeSetID != wantChanges[i] || snapshot.TemplateCopyVersion != p.TemplateCopyVersion ||
			hashImpactValue(snapshot.Spec) != hashImpactValue(wantSpec) {
			t.Fatalf("snapshot %d lost exact historical state/lineage: got=%+v want_context=%d want_spec_revision=%d want_change=%s want_spec=%+v", i, snapshot, wantContext, wantSpecRevision, wantChanges[i], wantSpec)
		}
		provenance := snapshot.SpecProvenance
		if provenance.ProjectID != snapshot.ProjectID || provenance.ContextRevision != snapshot.Revision ||
			provenance.SpecRevision != snapshot.SpecRevision || provenance.TemplateCopyVersion != snapshot.TemplateCopyVersion || provenance.Digest == "" {
			t.Fatalf("snapshot %d lost frozen provenance identity: %+v", i, snapshot)
		}
	}
	if hashImpactValue(history[2].Spec) != hashImpactValue(current.Spec) ||
		history[2].Revision != *appliedInverse.TargetContextRevision || history[2].SpecRevision != *appliedInverse.TargetSpecRevision {
		t.Fatalf("reopened formal state differs from appended inverse snapshot: current=%+v snapshot=%+v", current, history[2])
	}
}

func TestR25InverseRejectedRetryAndStaleDescendants(t *testing.T) {
	s, a, p, source, _ := r25InverseFieldFixture(t)
	ctx := context.Background()
	raw, _, _, err := s.ReverseChangeSet(ctx, a, p.ID, source.ID, "r25-rejected-first")
	rejected := decodeAgentJSON[ChangeSet](t, raw, err)
	if _, _, _, err := s.RejectChangeSet(ctx, a, p.ID, rejected.ID, "r25-reject-inverse"); err != nil {
		t.Fatal(err)
	}
	raw, _, _, err = s.ReverseChangeSet(ctx, a, p.ID, source.ID, "r25-rejected-retry")
	inverse := decodeAgentJSON[ChangeSet](t, raw, err)
	if inverse.ID == rejected.ID {
		t.Fatal("rejected retry reused a rejected proposal")
	}
	intervene := func(key, subject string) {
		t.Helper()
		p, err = s.GetProject(ctx, a, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		raw, _, _, e := s.CreateChangeSet(ctx, a, p.ID, key+"-create", CreateChangeSetInput{
			ExpectedContextRevision: p.CurrentContextRevision, Reason: "independent subject change",
			Fields:             map[string]ChangeFieldInput{"research_subject": {OldValue: p.Spec["research_subject"], NewValue: subject}},
			AffectedChapterIDs: []string{source.Impacts[0].ChapterID},
		})
		change := decodeAgentJSON[ChangeSet](t, raw, e)
		if _, _, _, e := s.ApplyChangeSet(ctx, a, p.ID, change.ID, key+"-apply"); e != nil {
			t.Fatal(e)
		}
	}
	intervene("r25-intervene-one", "subject one")
	if _, status, _, err := s.ApplyChangeSet(ctx, a, p.ID, inverse.ID, "r25-mark-inverse-stale"); err != nil || status != 409 {
		t.Fatalf("mark stale: status=%d err=%v", status, err)
	}
	raw, _, _, err = s.ReassessChangeSet(ctx, a, p.ID, inverse.ID, "r25-reassess-one")
	child := decodeAgentJSON[ChangeSet](t, raw, err)
	if child.ReversesChangeSetID != source.ID || child.ReassessedFromID != inverse.ID {
		t.Fatalf("first reassessment lineage: %+v", child)
	}
	if _, _, _, err := s.ReassessChangeSet(ctx, a, p.ID, inverse.ID, "r25-reassess-sibling"); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("live inverse sibling accepted: %v", err)
	}
	intervene("r25-intervene-two", "subject two")
	if _, status, _, err := s.ApplyChangeSet(ctx, a, p.ID, child.ID, "r25-mark-child-stale"); err != nil || status != 409 {
		t.Fatalf("mark child stale: status=%d err=%v", status, err)
	}
	raw, _, _, err = s.ReassessChangeSet(ctx, a, p.ID, child.ID, "r25-reassess-two")
	descendant := decodeAgentJSON[ChangeSet](t, raw, err)
	if descendant.ReversesChangeSetID != source.ID || descendant.ReassessedFromID != child.ID {
		t.Fatalf("ancestor chain blocked/lost: %+v", descendant)
	}
	if _, status, _, err := s.ApplyChangeSet(ctx, a, p.ID, descendant.ID, "r25-apply-descendant"); err != nil || status != 200 {
		t.Fatalf("apply descendant: status=%d err=%v", status, err)
	}
	r25AssertInverseHistory(t, s, a, p.ID, source, descendant)
	if _, _, _, err := s.ReverseChangeSet(ctx, a, p.ID, source.ID, "r25-duplicate-after-apply"); !errors.Is(err, ErrInvalidState) {
		// The original's NewValue no longer matches current spec. Either the
		// unique lineage check or current-value planning must reject it.
		if !errors.Is(err, ErrVersionConflict) {
			t.Fatalf("second canonical inverse accepted: %v", err)
		}
	}
}

func TestR25OrdinaryInverseValidatesFieldsImpactsAndHistoricalG1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inverse-g1.db")
	s := testStore(t, path)
	ctx := context.Background()
	a := Actor{TenantID: 926, UserID: "owner"}
	seedTenantMember(t, s, a)
	raw, _, _, err := s.CreateProject(ctx, a, "r25-g1-project", CreateProjectInput{Name: "inverse G1", TemplateID: "template-demo"})
	p := asProject(t, raw)
	if err != nil {
		t.Fatal(err)
	}
	seedActualTurn(t, s, a, p.ID, "r25-old-session", "r25-old-turn", "pinned old subject")
	raw, _, _, err = s.SaveSpec(ctx, a, p.ID, "r25-g1-spec", SaveSpecInput{
		ExpectedSpecRevision: p.SpecRevision, Fields: map[string]string{"research_subject": "pinned old subject", "research_goal": "human goal"},
		FieldMetadata: map[string]SpecFieldInput{"research_subject": {Origin: "ai_generated", Status: "pending_confirmation",
			Provenance: &ProvenanceRecord{SourceType: "agent_turn", SourceID: "r25-old-turn", SourceVersion: "1"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	p = asProject(t, raw)
	raw, _, _, err = s.ActivateProject(ctx, a, p.ID, "r25-g1-activate", ActivateProjectInput{
		ExpectedSpecRevision: p.SpecRevision, ExpectedProjectVersion: p.ProjectVersion, ReviewedProjectVersion: p.ProjectVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	p = asProject(t, raw)
	chapters, err := s.ListChapters(ctx, a, p.ID)
	if err != nil || len(chapters) < 2 {
		t.Fatalf("chapters: %v", err)
	}
	raw, _, _, err = s.CreateChangeSet(ctx, a, p.ID, "r25-g1-source-create", CreateChangeSetInput{
		ExpectedContextRevision: p.CurrentContextRevision, Reason: "manual replacement",
		Fields:             map[string]ChangeFieldInput{"research_subject": {OldValue: "pinned old subject", NewValue: "manual new subject"}},
		AffectedChapterIDs: []string{chapters[0].ID},
	})
	source := decodeAgentJSON[ChangeSet](t, raw, err)
	raw, _, _, err = s.ApplyChangeSet(ctx, a, p.ID, source.ID, "r25-g1-source-apply")
	source = decodeAgentJSON[ChangeSet](t, raw, err)
	p, err = s.GetProject(ctx, a, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	input := CreateChangeSetInput{ExpectedContextRevision: p.CurrentContextRevision, Reason: "restore historical subject",
		ReversesChangeSetID: source.ID, AffectedChapterIDs: []string{chapters[0].ID},
		Fields: map[string]ChangeFieldInput{"research_subject": {OldValue: "manual new subject", NewValue: "pinned old subject"}},
	}
	wrong := input
	wrong.Fields = map[string]ChangeFieldInput{"research_subject": {OldValue: "manual new subject", NewValue: "forged old subject"}}
	if _, _, _, err := s.CreateChangeSet(ctx, a, p.ID, "r25-g1-forged-inverse", wrong); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("provenance key granted arbitrary inverse: %v", err)
	}
	wrong = input
	wrong.AffectedChapterIDs = []string{chapters[1].ID}
	if _, _, _, err := s.CreateChangeSet(ctx, a, p.ID, "r25-g1-omitted-impact", wrong); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("inverse omitted original affected chapter: %v", err)
	}
	raw, _, _, err = s.CreateChangeSet(ctx, a, p.ID, "r25-g1-real-inverse", input)
	inverse := decodeAgentJSON[ChangeSet](t, raw, err)
	r25AssertInverseHistory(t, s, a, p.ID, source, inverse)
	if _, _, _, err := s.ApplyChangeSet(ctx, a, p.ID, inverse.ID, "r25-g1-inverse-apply"); err != nil {
		t.Fatal(err)
	}
	s = reopenSourcePolicyStore(t, s, path)
	p, err = s.GetProject(ctx, a, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	field := p.SpecFields["research_subject"]
	if field.Value != "pinned old subject" || field.Provenance == nil || field.Provenance.SourceID != "r25-old-turn" || !field.Provenance.Accessible {
		t.Fatalf("ordinary inverse lost the actual historical source pin: %+v", field)
	}
	r25AssertInverseHistory(t, s, a, p.ID, source, inverse)
}

func TestR25SemanticReverseReplayChecksCurrentSourcesAfterApplyAndReopen(t *testing.T) {
	f := newAgentFixture(t, false)
	ctx := context.Background()
	claim := f.claim(t)
	anchor := f.anchor(t, "source-1", "r25-semantic-source-anchor")
	card := f.card(t, []SourceAnchor{anchor}, &claim, "r25-semantic-source-card", "supports")
	if !slices.Contains(card.SourceIDs, "source-1") {
		t.Fatal("source revocation fixture must attach an actual source to the claim")
	}
	source := semanticDraft(t, f.svc, f.owner, f.project, "r25-semantic-source", SemanticDelta{
		Kind: "ResearchClaim", TargetID: claim.ID, ExpectedVersion: claim.Version,
		ProposedClaim: &SaveResearchClaimInput{Text: "changed actual claim", Scope: "bounded scope", Status: "active", Confirmed: true},
	})
	source = semanticApply(t, f.svc, f.owner, f.project, source, "r25-semantic-source")
	if len(source.SemanticDeltas) != 1 || !slices.Contains(source.SemanticDeltas[0].SourceIDs, "source-1") {
		t.Fatal("source semantic plan did not pin the linked evidence source")
	}
	p, err := f.svc.GetProject(ctx, f.owner, f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	key := "r25-semantic-inverse"
	first, status, _, err := f.svc.ReverseChangeSet(ctx, f.owner, p.ID, source.ID, key)
	inverse := decodeAgentJSON[ChangeSet](t, first, err)
	if status != 201 || inverse.Status != "draft" || inverse.ReversesChangeSetID != source.ID ||
		len(inverse.SemanticDeltas) != 1 || inverse.SemanticDeltas[0].ExpectedVersion != claim.Version+1 {
		t.Fatalf("canonical semantic inverse: %+v status=%d", inverse, status)
	}
	if !slices.Contains(inverse.SemanticDeltas[0].SourceIDs, "source-1") {
		t.Fatal("inverse semantic plan did not preserve the linked source")
	}
	semanticApply(t, f.svc, f.owner, p, inverse, "r25-semantic-inverse")
	original := r25InverseOperationRecord(t, f.svc, f.owner, p.ID, source.ID, key)
	f.svc = reopenSourcePolicyStore(t, f.svc, f.path)
	f.svc.sources = f.policy
	f.policy.verdict = ErrSourceUnavailable
	raw, deniedStatus, replayed, err := f.svc.ReverseChangeSet(ctx, f.owner, p.ID, source.ID, key)
	if !errors.Is(err, ErrSourceUnavailable) || raw != nil || deniedStatus != 0 || replayed {
		t.Fatalf("cached semantic inverse bypassed current source access: status=%d replay=%v body=%s err=%v", deniedStatus, replayed, raw, err)
	}
	if got := r25InverseOperationRecord(t, f.svc, f.owner, p.ID, source.ID, key); !reflect.DeepEqual(original, got) {
		t.Fatal("source denial changed the saved operation")
	}
	f.policy.verdict = nil
	again, againStatus, replayed, err := f.svc.ReverseChangeSet(ctx, f.owner, p.ID, source.ID, key)
	if err != nil || !replayed || againStatus != status || !bytes.Equal(first, again) {
		t.Fatalf("semantic retry recomputed against post-inverse version: status=%d replay=%v err=%v", againStatus, replayed, err)
	}
	r25AssertInverseHistory(t, f.svc, f.owner, p.ID, source, inverse)
	if err := f.svc.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		current, _, err := loadStoredObject[ResearchClaim](tx, p.ID, ResearchClaimKind, claim.ID)
		if err == nil && (current.Version != claim.Version+2 || current.Text != claim.Text) {
			t.Fatalf("retry changed formal claim version/text: %+v", current)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestR25ReverseReplayRejectsFormerOwnerWithoutAlteringOperation(t *testing.T) {
	s, a, p, source, _ := r25InverseFieldFixture(t)
	ctx := context.Background()
	key := "r25-owner-replay"
	if _, _, _, err := s.ReverseChangeSet(ctx, a, p.ID, source.ID, key); err != nil {
		t.Fatal(err)
	}
	original := r25InverseOperationRecord(t, s, a, p.ID, source.ID, key)
	target := Actor{TenantID: a.TenantID, UserID: "new-owner"}
	seedTenantMember(t, s, target)
	raw, _, _, err := s.RequestOwnerTransfer(ctx, a, p.ID, "r25-owner-transfer", OwnerTransferInput{ToUserID: target.UserID, ExpectedProjectVersion: p.ProjectVersion})
	if err != nil {
		t.Fatal(err)
	}
	transfer := rawOwnerTransfer(t, raw)
	if _, _, _, err := s.AcceptOwnerTransfer(ctx, target, p.ID, transfer.ID, "r25-owner-accept"); err != nil {
		t.Fatal(err)
	}
	raw, status, replayed, err := s.ReverseChangeSet(ctx, a, p.ID, source.ID, key)
	if !errors.Is(err, ErrNotFound) || raw != nil || status != 0 || replayed {
		t.Fatalf("former Owner replayed inverse: status=%d replay=%v body=%s err=%v", status, replayed, raw, err)
	}
	if got := r25InverseOperationRecord(t, s, a, p.ID, source.ID, key); !reflect.DeepEqual(original, got) {
		t.Fatal("Owner revocation altered durable operation")
	}
}

func TestR25TemplateInverseRestoresExactRuleSetPolicyAndHistory(t *testing.T) {
	for _, sameRef := range []bool{false, true} {
		name := "historical_exact_version"
		if sameRef {
			name = "same_ref_clears_override"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "inverse-template.db")
			s, a, p, v1 := sourcePolicyProject(t, path)
			ctx := context.Background()
			reader := &ruleSetSnapshotReaderStub{set: v1}
			s.WithRuleSetSnapshotReader(reader)
			raw, _, _, err := s.BindTemplateCopyRuleSet(ctx, a, p.ID, "r25-template-bind", BindTemplateCopyRuleSetInput{
				ExpectedProjectVersion: p.ProjectVersion, ExpectedTemplateCopyVersion: p.TemplateCopyVersion,
				RuleSetRef: &lingdoctemplate.RuleSetReference{ID: v1.ID, Version: v1.Version, Hash: v1.RulesetHash},
			})
			if err != nil {
				t.Fatal(err)
			}
			p = asProject(t, raw)
			raw, _, _, err = s.SaveSpec(ctx, a, p.ID, "r25-template-spec", SaveSpecInput{ExpectedSpecRevision: p.SpecRevision,
				Fields: map[string]string{"research_subject": "template subject", "research_goal": "template goal"}})
			if err != nil {
				t.Fatal(err)
			}
			p = asProject(t, raw)
			raw, _, _, err = s.ActivateProject(ctx, a, p.ID, "r25-template-active", ActivateProjectInput{
				ExpectedSpecRevision: p.SpecRevision, ExpectedProjectVersion: p.ProjectVersion, ReviewedProjectVersion: p.ProjectVersion,
			})
			if err != nil {
				t.Fatal(err)
			}
			p = asProject(t, raw)
			historical := p.TemplateCopy
			historicalSidecar := sourcePolicySidecarBytes(t, s, historical)
			v2 := v1
			if !sameRef {
				draft := lingdoctemplate.Clone(lingdoctemplate.Template{RuleSetSnapshot: &v1}).RuleSetSnapshot
				draft.Version, draft.State, draft.RulesetHash = "r25-next", lingdoctemplate.RuleSetDraft, ""
				draft.Bindings[0].Message = "changed rule message"
				v2, err = lingdoctemplate.PublishRuleSet(*draft)
				if err != nil {
					t.Fatal(err)
				}
			}
			reader.set = v2
			disabled := false
			definition := lingdoctemplate.Clone(historical.Definition)
			raw, _, _, err = s.CreateChangeSet(ctx, a, p.ID, "r25-template-upgrade", CreateChangeSetInput{
				ExpectedContextRevision: p.CurrentContextRevision, Reason: "reviewed RuleSet upgrade",
				TemplateUpgrade: &TemplateUpgradeInput{
					ExpectedProjectVersion: p.ProjectVersion, ExpectedTemplateCopyVersion: p.TemplateCopyVersion,
					Fields: definition.Fields, Sections: definition.Sections, Terms: append([]lingdoctemplate.Term{}, definition.Terms...),
					Rules: definition.Rules, RequiredFields: definition.RequiredFields,
					RuleSetRef:           &lingdoctemplate.RuleSetReference{ID: v2.ID, Version: v2.Version, Hash: v2.RulesetHash},
					RuleBindingOverrides: []lingdoctemplate.RuleBindingOverride{{RuleID: "goal-required", Enabled: &disabled}},
				},
			})
			source := decodeAgentJSON[ChangeSet](t, raw, err)
			raw, _, _, err = s.ApplyChangeSet(ctx, a, p.ID, source.ID, "r25-template-upgrade-apply")
			source = decodeAgentJSON[ChangeSet](t, raw, err)
			reader.set = v1
			first, _, _, err := s.ReverseChangeSet(ctx, a, p.ID, source.ID, "r25-template-inverse")
			inverse := decodeAgentJSON[ChangeSet](t, first, err)
			if inverse.TemplateUpgrade == nil || inverse.ReversesChangeSetID != source.ID ||
				inverse.TemplateUpgrade.Definition.ContentHash != historical.Definition.ContentHash ||
				inverse.TemplateUpgrade.Definition.RulesetHash != historical.Definition.RulesetHash ||
				len(inverse.TemplateUpgrade.Definition.RuleBindingOverrides) != len(historical.Definition.RuleBindingOverrides) {
				t.Fatalf("historical exact RuleSet/effective identity lost: %+v", inverse)
			}
			before, err := s.GetProject(ctx, a, p.ID)
			if err != nil {
				t.Fatal(err)
			}
			reader.set, err = lingdoctemplate.WithdrawRuleSet(v1)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := s.ApplyChangeSet(ctx, a, p.ID, inverse.ID, "r25-template-withdrawn"); !errors.Is(err, ErrInvalidState) {
				t.Fatalf("historical pin bypassed current catalog lifecycle: %v", err)
			}
			after, err := s.GetProject(ctx, a, p.ID)
			if err != nil || after.ProjectVersion != before.ProjectVersion || after.TemplateCopyVersion != before.TemplateCopyVersion {
				t.Fatalf("withdrawn inverse left partial writes: %+v err=%v", after, err)
			}
			reader.set = v1
			if _, _, _, err := s.ApplyChangeSet(ctx, a, p.ID, inverse.ID, "r25-template-inverse-apply"); err != nil {
				t.Fatal(err)
			}
			s = reopenSourcePolicyStore(t, s, path)
			restored, err := s.GetProject(ctx, a, p.ID)
			if err != nil {
				t.Fatal(err)
			}
			assertInheritedCanonicalPolicy(t, historical, restored.TemplateCopy, v1)
			assertSourcePolicySidecar(t, s, restored.TemplateCopy)
			assertHistoricalSourcePolicy(t, s, a, historical, historicalSidecar)
			r25AssertInverseHistory(t, s, a, p.ID, source, inverse)
			if restored.TemplateCopy.Version != historical.Version+2 ||
				restored.TemplateCopy.Definition.ContentHash != historical.Definition.ContentHash ||
				restored.TemplateCopy.Definition.RulesetHash != historical.Definition.RulesetHash {
				t.Fatalf("template inverse rewound a copy or kept a new effective identity: %+v", restored.TemplateCopy)
			}
			again, status, replayed, err := s.ReverseChangeSet(ctx, a, p.ID, source.ID, "r25-template-inverse")
			if err != nil || status != 201 || !replayed || !bytes.Equal(first, again) {
				t.Fatalf("template inverse lost-response replay after reopen: status=%d replay=%v err=%v", status, replayed, err)
			}
		})
	}
}

func TestR25ReverseAuditFailureRollsBackLineageProvenanceAndOperation(t *testing.T) {
	s, a, p, source, _ := r25InverseFieldFixture(t)
	ctx := context.Background()
	count := func(model any) int64 {
		t.Helper()
		var n int64
		if err := s.testDB().Model(model).Count(&n).Error; err != nil {
			t.Fatal(err)
		}
		return n
	}
	changesBefore, operationsBefore, recordsBefore := count(&changeSetRow{}), count(&operationRow{}), count(&projectRecordRow{})
	key := "r25-audit-failed-inverse"
	if err := s.testDB().Exec("CREATE TRIGGER r25_inverse_audit_failure BEFORE INSERT ON lingdoc_project_audits WHEN NEW.action = 'project.change_set.create' BEGIN SELECT RAISE(ABORT, 'inverse audit unavailable'); END").Error; err != nil {
		t.Fatal(err)
	}
	raw, status, replayed, err := s.ReverseChangeSet(ctx, a, p.ID, source.ID, key)
	if err == nil || raw != nil || status != 0 || replayed {
		t.Fatalf("audit failure accepted inverse: status=%d replay=%v body=%s err=%v", status, replayed, raw, err)
	}
	if err := s.testDB().Exec("DROP TRIGGER r25_inverse_audit_failure").Error; err != nil {
		t.Fatal(err)
	}
	if count(&changeSetRow{}) != changesBefore || count(&operationRow{}) != operationsBefore || count(&projectRecordRow{}) != recordsBefore {
		t.Fatal("failed inverse left lineage, operation, or provenance rows")
	}
	current, err := s.GetProject(ctx, a, p.ID)
	unchanged, sourceErr := s.GetChangeSet(ctx, a, p.ID, source.ID)
	if err != nil || sourceErr != nil || hashImpactValue(current) != hashImpactValue(p) || !r25ChangeSetPersistenceEqual(source, unchanged) {
		t.Fatalf("failed inverse changed formal state/source: projectErr=%v sourceErr=%v", err, sourceErr)
	}
	if _, status, replayed, err := s.ReverseChangeSet(ctx, a, p.ID, source.ID, key); err != nil || status != 201 || replayed {
		t.Fatalf("failed request consumed key/source slot: status=%d replay=%v err=%v", status, replayed, err)
	}
}

// Preserve every inverse persistence/source/lock capability except audit.
// Missing-audit rejection is therefore independent of a missing lineage lock.
type r25InverseAuditlessRepository struct{ inner Repository }
type r25InverseAuditlessTransaction struct {
	Transaction
	auditlessSourcePorts
	inverseLineageTransaction
	changes changeSetTransaction
}

func (r r25InverseAuditlessRepository) Transaction(ctx context.Context, options TransactionOptions, fn func(Transaction) error) error {
	return r.inner.Transaction(ctx, options, func(tx Transaction) error {
		ports, ok := tx.(auditlessSourcePorts)
		if !ok {
			return ErrInvalidState
		}
		lineage, ok := tx.(inverseLineageTransaction)
		if !ok {
			return ErrInvalidState
		}
		changes, ok := tx.(changeSetTransaction)
		if !ok {
			return ErrInvalidState
		}
		return fn(r25InverseAuditlessTransaction{Transaction: tx, auditlessSourcePorts: ports, inverseLineageTransaction: lineage, changes: changes})
	})
}
func (tx r25InverseAuditlessTransaction) ChangeSet(p, id string) (ChangeSet, error) {
	return tx.changes.ChangeSet(p, id)
}
func (tx r25InverseAuditlessTransaction) ChangeSets(p string) ([]ChangeSet, error) {
	return tx.changes.ChangeSets(p)
}
func (tx r25InverseAuditlessTransaction) InsertChangeSet(change ChangeSet) error {
	return tx.changes.InsertChangeSet(change)
}
func (tx r25InverseAuditlessTransaction) UpdateChangeSet(before, after ChangeSet) error {
	return tx.changes.UpdateChangeSet(before, after)
}
func (tx r25InverseAuditlessTransaction) InvalidateChapterConfirmations(p string, ids []string) error {
	return tx.changes.InvalidateChapterConfirmations(p, ids)
}

func TestR25ReverseMissingAuditPortHasZeroWrites(t *testing.T) {
	s, a, p, source, _ := r25InverseFieldFixture(t)
	counts := func() [3]int64 {
		t.Helper()
		var result [3]int64
		for i, model := range []any{&changeSetRow{}, &operationRow{}, &projectRecordRow{}} {
			if err := s.testDB().Model(model).Count(&result[i]).Error; err != nil {
				t.Fatal(err)
			}
		}
		return result
	}
	before := counts()
	withoutAudit := *s
	withoutAudit.repository = r25InverseAuditlessRepository{inner: s.repository}
	raw, status, replayed, err := withoutAudit.ReverseChangeSet(context.Background(), a, p.ID, source.ID, "r25-no-audit-port")
	if !errors.Is(err, ErrInvalidState) || raw != nil || status != 0 || replayed {
		t.Fatalf("inverse without audit capability: status=%d replay=%v body=%s err=%v", status, replayed, raw, err)
	}
	if after := counts(); after != before {
		t.Fatalf("missing-audit rejection wrote rows: %v -> %v", before, after)
	}
	unchanged, err := s.GetChangeSet(context.Background(), a, p.ID, source.ID)
	if err != nil || !r25ChangeSetPersistenceEqual(source, unchanged) {
		t.Fatalf("missing-audit rejection changed source: %+v err=%v", unchanged, err)
	}
}

func TestR25LegacyG1InverseBlocksDuplicateAndReassessesWithDurableLineage(t *testing.T) {
	s, a, p, source, _ := r25InverseFieldFixture(t)
	ctx := context.Background()
	// Reproduce f8's server-owned reverse flow through the real service: it
	// carried the original ID in G1 history, before the public lineage field.
	prepared, err := s.prepareChangeProvenance(ctx, a, source, map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	prepared = context.WithValue(prepared, reverseSourceKey{}, source.ID)
	raw, _, _, err := s.CreateChangeSet(prepared, a, p.ID, "r25-legacy-inverse", CreateChangeSetInput{
		ExpectedContextRevision: p.CurrentContextRevision, Reason: "legacy field inverse",
		Fields:             map[string]ChangeFieldInput{"research_goal": {OldValue: source.Fields[0].NewValue, NewValue: source.Fields[0].OldValue}},
		AffectedChapterIDs: []string{source.Impacts[0].ChapterID},
	})
	legacy := decodeAgentJSON[ChangeSet](t, raw, err)
	if legacy.ReversesChangeSetID != "" {
		t.Fatal("fixture is not the pre-lineage-field compatibility case")
	}
	if _, _, _, err := s.ReverseChangeSet(ctx, a, p.ID, source.ID, "r25-legacy-duplicate"); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("legacy inverse was ignored by uniqueness: %v", err)
	}
	raw, _, _, err = s.CreateChangeSet(ctx, a, p.ID, "r25-legacy-intervening", CreateChangeSetInput{
		ExpectedContextRevision: p.CurrentContextRevision, Reason: "independent field",
		Fields:             map[string]ChangeFieldInput{"research_subject": {OldValue: p.Spec["research_subject"], NewValue: "new subject"}},
		AffectedChapterIDs: []string{source.Impacts[0].ChapterID},
	})
	other := decodeAgentJSON[ChangeSet](t, raw, err)
	if _, _, _, err := s.ApplyChangeSet(ctx, a, p.ID, other.ID, "r25-legacy-other-apply"); err != nil {
		t.Fatal(err)
	}
	if _, status, _, err := s.ApplyChangeSet(ctx, a, p.ID, legacy.ID, "r25-legacy-mark-stale"); err != nil || status != 409 {
		t.Fatalf("legacy stale: status=%d err=%v", status, err)
	}
	raw, _, _, err = s.ReassessChangeSet(ctx, a, p.ID, legacy.ID, "r25-legacy-reassess")
	child := decodeAgentJSON[ChangeSet](t, raw, err)
	if child.ReassessedFromID != legacy.ID || child.ReversesChangeSetID != source.ID {
		t.Fatalf("legacy reassessment did not carry durable lineage: %+v", child)
	}
	r25AssertInverseHistory(t, s, a, p.ID, source, child)
	unchanged, err := s.GetChangeSet(ctx, a, p.ID, legacy.ID)
	if err != nil || unchanged.ReversesChangeSetID != "" || unchanged.Status != "stale" {
		t.Fatalf("compatibility read rewrote the old lineage field: %+v err=%v", unchanged, err)
	}
}

func TestR25PausedRuntimePermitsInverseProposalButStillBlocksApply(t *testing.T) {
	s, a, p, source, _ := r25InverseFieldFixture(t)
	ctx := context.Background()
	s.SetRuntimeMode("real_api_fake_model")
	if s.runtimeMode == "" {
		t.Fatal("runtime mode fixture did not enable the production gate")
	}
	if _, _, _, err := s.ControlRuntime(ctx, a, p.ID, "r25-pause-before-inverse", RuntimeControlInput{
		Scope: RuntimeScope{Kind: "project", Value: p.ID}, Action: "pause", Reason: "bounded recovery review",
	}); err != nil {
		t.Fatal(err)
	}
	before, err := s.GetProject(ctx, a, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, status, replayed, err := s.ReverseChangeSet(ctx, a, p.ID, source.ID, "r25-paused-inverse")
	if err != nil || status != 201 || replayed {
		t.Fatalf("existing reverse recovery exception: status=%d replay=%v err=%v", status, replayed, err)
	}
	inverse := decodeAgentJSON[ChangeSet](t, raw, err)
	afterProposal, err := s.GetProject(ctx, a, p.ID)
	if err != nil || !reflect.DeepEqual(before, afterProposal) || inverse.Status != "assessed" {
		t.Fatalf("paused inverse proposal changed formal project: err=%v", err)
	}
	body, code, replayed, err := s.ApplyChangeSet(ctx, a, p.ID, inverse.ID, "r25-paused-inverse-apply")
	if !errors.Is(err, ErrRuntimePaused) || body != nil || code != 0 || replayed {
		t.Fatalf("recovery proposal expanded Apply authority: status=%d replay=%v err=%v", code, replayed, err)
	}
	afterApply, err := s.GetProject(ctx, a, p.ID)
	persisted, inverseErr := s.GetChangeSet(ctx, a, p.ID, inverse.ID)
	if err != nil || inverseErr != nil || !reflect.DeepEqual(before, afterApply) || persisted.Status != "assessed" {
		t.Fatalf("blocked Apply changed project/proposal state: projectErr=%v inverseErr=%v", err, inverseErr)
	}
	var consumed int64
	if err := s.testDB().Model(&operationRow{}).Where("key = ?", "r25-paused-inverse-apply").Count(&consumed).Error; err != nil || consumed != 0 {
		t.Fatalf("blocked Apply consumed operation: count=%d err=%v", consumed, err)
	}
}
