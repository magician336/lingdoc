package workspacecore

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"path/filepath"
	"strconv"
	"testing"

	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
)

func r26TemplateDeltaFixture(t *testing.T, migrateGoal bool) (*Service, Actor, Project, Project, ChangeSet, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "template-inverse-current-baseline.db")
	s := testStore(t, path)
	a := Actor{TenantID: 926, UserID: "r26-owner"}
	baseline := seedActiveG3Project(t, s, a)
	if baseline.TemplateCopy == nil || baseline.TemplateCopy.Status != TemplateCopyBound {
		t.Fatal("fixture requires a real bound template")
	}
	definition := lingdoctemplate.Clone(baseline.TemplateCopy.Definition)
	definition.Fields[0].Label += " (reviewed R26 upgrade)"
	values := map[string]string{}
	if migrateGoal {
		values["research_goal"] = "goal explicitly changed by template migration"
	}
	raw, status, replayed, err := s.CreateChangeSet(context.Background(), a, baseline.ID, "r26-template-source-create", CreateChangeSetInput{
		ExpectedContextRevision: baseline.CurrentContextRevision, Reason: "Only the reviewed template migration",
		TemplateUpgrade: &TemplateUpgradeInput{
			ExpectedProjectVersion: baseline.ProjectVersion, ExpectedTemplateCopyVersion: baseline.TemplateCopyVersion,
			Fields: definition.Fields, Sections: definition.Sections, Terms: append([]lingdoctemplate.Term{}, definition.Terms...),
			RequiredFields: definition.RequiredFields, Rules: definition.Rules, FieldValues: values,
		},
	})
	if err != nil || status != 201 || replayed {
		t.Fatalf("create source: status=%d replay=%v err=%v", status, replayed, err)
	}
	change := pr150DecodeChange(t, raw)
	raw, status, replayed, err = s.ApplyChangeSet(context.Background(), a, baseline.ID, change.ID, "r26-template-source-apply")
	if err != nil || status != 200 || replayed {
		t.Fatalf("apply source: status=%d replay=%v err=%v", status, replayed, err)
	}
	change = pr150DecodeChange(t, raw)
	after, err := s.GetProject(context.Background(), a, baseline.ID)
	if err != nil || after.CurrentContextRevision != baseline.CurrentContextRevision+1 || after.SpecRevision != baseline.SpecRevision+1 || after.TemplateCopyVersion != baseline.TemplateCopyVersion+1 {
		t.Fatalf("source target: %+v err=%v", after, err)
	}
	return s, a, baseline, after, change, path
}

func r26ApplyIndependentField(t *testing.T, s *Service, a Actor, p Project, key, field, value string) (Project, ChangeSet) {
	t.Helper()
	chapters, err := s.ListChapters(context.Background(), a, p.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("real project chapters: %v", err)
	}
	raw, status, replayed, err := s.CreateChangeSet(context.Background(), a, p.ID, key+"-create", CreateChangeSetInput{
		ExpectedContextRevision: p.CurrentContextRevision, Reason: "Later independently reviewed research edit",
		Fields:             map[string]ChangeFieldInput{field: {OldValue: p.Spec[field], NewValue: value}},
		AffectedChapterIDs: []string{chapters[0].ID},
	})
	if err != nil || status != 201 || replayed {
		t.Fatalf("create independent field: status=%d replay=%v err=%v", status, replayed, err)
	}
	change := pr150DecodeChange(t, raw)
	raw, status, replayed, err = s.ApplyChangeSet(context.Background(), a, p.ID, change.ID, key+"-apply")
	if err != nil || status != 200 || replayed {
		t.Fatalf("apply independent field: status=%d replay=%v err=%v", status, replayed, err)
	}
	change = pr150DecodeChange(t, raw)
	after, err := s.GetProject(context.Background(), a, p.ID)
	if err != nil || after.Spec[field] != value || after.CurrentContextRevision != p.CurrentContextRevision+1 || after.SpecRevision != p.SpecRevision+1 || after.TemplateCopyVersion != p.TemplateCopyVersion {
		t.Fatalf("independent edit did not actually commit: %+v err=%v", after, err)
	}
	return after, change
}

// This intentionally reaches real Service Apply and then reopens the DB before
// asserting the independent value. A proposal-only or SQL sentinel would miss
// a durable baseline.Spec overwrite hidden inside template application.
func TestR26TemplateInversePreservesLaterIndependentFieldAfterReopen(t *testing.T) {
	for _, migrateGoal := range []bool{false, true} {
		name := "template_label_only"
		if migrateGoal {
			name = "template_with_explicit_goal_delta"
		}
		t.Run(name, func(t *testing.T) {
			s, a, baseline, sourceTarget, source, path := r26TemplateDeltaFixture(t, migrateGoal)
			before, independent := r26ApplyIndependentField(t, s, a, sourceTarget, "r26-independent-subject", "research_subject", "later independent subject must survive")
			s = reopenSourcePolicyStore(t, s, path)
			ctx := context.Background()
			first, status, replayed, err := s.ReverseChangeSet(ctx, a, before.ID, source.ID, "r26-template-inverse-retry")
			if err != nil || status != 201 || replayed {
				t.Fatalf("reverse: status=%d replay=%v err=%v", status, replayed, err)
			}
			inverse := pr150DecodeChange(t, first)
			if inverse.TemplateUpgrade == nil || inverse.BaseContextRevision != before.CurrentContextRevision || inverse.ReversesChangeSetID != source.ID {
				t.Fatalf("inverse current basis: %+v", inverse)
			}
			raw, status, replayed, err := s.ApplyChangeSet(ctx, a, before.ID, inverse.ID, "r26-template-inverse-apply")
			if err != nil || status != 200 || replayed {
				t.Fatalf("inverse apply: status=%d replay=%v err=%v", status, replayed, err)
			}
			applied := pr150DecodeChange(t, raw)
			s = reopenSourcePolicyStore(t, s, path)
			after, err := s.GetProject(ctx, a, before.ID)
			want := maps.Clone(before.Spec)
			want["research_goal"] = baseline.Spec["research_goal"]
			if err != nil || !maps.Equal(after.Spec, want) {
				t.Fatalf("template inverse overwrote later independent edit after reopen: got=%+v want=%+v err=%v", after.Spec, want, err)
			}
			if after.CurrentContextRevision != before.CurrentContextRevision+1 || after.SpecRevision != before.SpecRevision+1 || after.TemplateCopyVersion != before.TemplateCopyVersion+1 || applied.TargetContextRevision == nil || *applied.TargetContextRevision != after.CurrentContextRevision {
				t.Fatalf("inverse did not append exactly one current revision: %+v change=%+v", after, applied)
			}
			if after.TemplateCopy == nil || after.TemplateCopy.Definition.ContentHash != baseline.TemplateCopy.Definition.ContentHash || after.TemplateCopy.Definition.RulesetHash != baseline.TemplateCopy.Definition.RulesetHash {
				t.Fatal("inverse lost exact historical template/RuleSet definition")
			}
			history, err := s.ListContextRevisions(ctx, a, before.ID)
			if err != nil || len(history) != 4 {
				t.Fatalf("four actual history snapshots missing: %+v %v", history, err)
			}
			wantSpecs := []map[string]string{baseline.Spec, sourceTarget.Spec, before.Spec, want}
			wantIDs := []string{source.ID, source.ID, independent.ID, inverse.ID}
			for i, snapshot := range history {
				if snapshot.Revision != baseline.CurrentContextRevision+int64(i) || snapshot.SpecRevision != baseline.SpecRevision+int64(i) || snapshot.ChangeSetID != wantIDs[i] || !maps.Equal(snapshot.Spec, wantSpecs[i]) || snapshot.SpecProvenance.ContextRevision != snapshot.Revision || snapshot.SpecProvenance.SpecRevision != snapshot.SpecRevision {
					t.Fatalf("history %d changed: %+v want_change=%s want_spec=%+v", i, snapshot, wantIDs[i], wantSpecs[i])
				}
			}
			r25AssertInverseHistory(t, s, a, before.ID, source, inverse)
			again, status, replayed, err := s.ReverseChangeSet(ctx, a, before.ID, source.ID, "r26-template-inverse-retry")
			if err != nil || status != 201 || !replayed || !bytes.Equal(first, again) {
				t.Fatalf("lost-response inverse replay after reopen: status=%d replay=%v same=%v err=%v", status, replayed, bytes.Equal(first, again), err)
			}
		})
	}
}

func TestR26TemplateInverseRejectsLaterEditToMigratedField(t *testing.T) {
	s, a, _, sourceTarget, source, path := r26TemplateDeltaFixture(t, true)
	before, _ := r26ApplyIndependentField(t, s, a, sourceTarget, "r26-later-goal-edit", "research_goal", "later goal must not be silently undone")
	s = reopenSourcePolicyStore(t, s, path)
	state := pr150RecoveryState(t, s, a, before.ID, source.ID)
	counts := func() map[string]int64 {
		t.Helper()
		out := map[string]int64{}
		for _, table := range []string{"lingdoc_change_sets", "lingdoc_operations", "lingdoc_g3_records", "lingdoc_g5_records"} {
			var n int64
			if err := s.testDB().Table(table).Count(&n).Error; err != nil {
				t.Fatal(err)
			}
			out[table] = n
		}
		return out
	}
	countBefore := counts()
	ctx := context.Background()
	raw, status, replayed, err := s.ReverseChangeSet(ctx, a, before.ID, source.ID, "r26-conflicting-template-inverse")
	if !errors.Is(err, ErrVersionConflict) || raw != nil || status != 0 || replayed {
		t.Fatalf("later migrated-field edit was not rejected: status=%d replay=%v body=%s err=%v", status, replayed, raw, err)
	}
	if state != pr150RecoveryState(t, s, a, before.ID, source.ID) || !maps.Equal(countBefore, counts()) {
		t.Fatal("conflicting inverse left project/history/audit/lineage/operation/provenance writes")
	}
	// Restore the source target through another explicit current-baseline proposal.
	// The failed reverse must not consume its idempotency key or lineage slot.
	current, currentErr := s.GetProject(ctx, a, before.ID)
	if currentErr != nil {
		t.Fatal(currentErr)
	}
	current, _ = r26ApplyIndependentField(t, s, a, current, "r26-human-resolve-goal", "research_goal", sourceTarget.Spec["research_goal"])
	raw, status, replayed, err = s.ReverseChangeSet(ctx, a, current.ID, source.ID, "r26-conflicting-template-inverse")
	if err != nil || status != 201 || replayed {
		t.Fatalf("conflict consumed key/source slot: status=%d replay=%v err=%v", status, replayed, err)
	}
	inverse := pr150DecodeChange(t, raw)
	if inverse.BaseContextRevision != current.CurrentContextRevision || inverse.ReversesChangeSetID != source.ID {
		t.Fatalf("resolved inverse lost current basis/lineage: %+v", inverse)
	}
}

// Corruption is confined to the fixture's stored history. The assertion still
// enters the real Service and checks every protected stored row, not a helper.
func TestR26TemplateInverseRejectsUntrustedTargetHistory(t *testing.T) {
	for _, name := range []string{"wrong_source", "wrong_snapshot_revision", "missing_value", "self_consistent_forged_value"} {
		t.Run(name, func(t *testing.T) {
			s, a, _, target, source, path := r26TemplateDeltaFixture(t, true)
			ctx := context.Background()
			err := s.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error {
				store := tx.(gormTransaction)
				var revision ContextRevisionRecord
				key := strconv.FormatInt(*source.TargetContextRevision, 10)
				version, err := store.g3Read(target.ID, "revision", key, &revision)
				if err != nil {
					return err
				}
				switch name {
				case "wrong_source":
					revision.ChangeSetID = "not-the-source-change"
				case "wrong_snapshot_revision":
					revision.SpecProvenance.SpecRevision++
				case "missing_value":
					delete(revision.Spec, "research_goal")
				case "self_consistent_forged_value":
					revision.Spec["research_goal"] = "not the source's applied migration"
					for i := range revision.SpecProvenance.Fields {
						ref := &revision.SpecProvenance.Fields[i]
						if ref.FieldKey == "research_goal" {
							ref.ValueHash = hashImpactValue(revision.Spec[ref.FieldKey])
						}
					}
					revision.SpecProvenance.Digest = g1SnapshotDigest(revision.SpecProvenance)
					if err := ValidateFrozenProvenanceInput(revision.Spec, revision.SpecProvenance); err != nil {
						return err
					}
					if err := store.G5Put(target.ID, "g1_snapshot", revision.SpecProvenance.Digest, 0, revision.SpecProvenance); err != nil {
						return err
					}
				}
				return store.g3CAS(target.ID, "revision", key, version, revision)
			})
			if err != nil {
				t.Fatal(err)
			}
			s = reopenSourcePolicyStore(t, s, path)
			before := r26InverseStoredState(t, s)
			raw, status, replayed, err := s.ReverseChangeSet(ctx, a, target.ID, source.ID, "r26-corrupt-target")
			if !errors.Is(err, ErrInvalidState) || raw != nil || status != 0 || replayed {
				t.Fatalf("untrusted target accepted: status=%d replay=%v body=%s err=%v", status, replayed, raw, err)
			}
			if before != r26InverseStoredState(t, s) {
				t.Fatal("rejected target left protected project/template/change/operation/history/provenance/audit writes")
			}
		})
	}
}

func r26InverseStoredState(t *testing.T, s *Service) string {
	t.Helper()
	db, err := s.testDB().DB()
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]string{}
	for _, table := range []string{"lingdoc_projects", "lingdoc_project_template_copies", "lingdoc_change_sets", "lingdoc_operations", "lingdoc_g3_records", "lingdoc_g5_records", "lingdoc_chapters", "lingdoc_chapter_versions", "lingdoc_working_copies", "lingdoc_chapter_confirmations"} {
		rows[table] = pr150MigrationRows(t, db, "SELECT * FROM "+table)
	}
	rows["business_audits"] = pr150MigrationRows(t, db, "SELECT * FROM lingdoc_project_audits WHERE action NOT LIKE 'authorization.%'")
	return pr150JSON(t, rows)
}

func TestR26TemplateInverseRejectsCurrentValueIncompatibleWithOldTemplate(t *testing.T) {
	f := pr150NewTemplateFixture(t)
	ctx := context.Background()
	baseline := f.project
	definition := lingdoctemplate.Clone(baseline.TemplateCopy.Definition)
	found := false
	for i := range definition.Fields {
		if definition.Fields[i].ID == "study_design" {
			definition.Fields[i].Options = append(definition.Fields[i].Options, "r26-new-option")
			found = true
		}
	}
	if !found {
		t.Fatal("fixture requires the real enum field")
	}
	raw, status, replayed, err := f.s.CreateChangeSet(ctx, f.actor, baseline.ID, "r26-widen-enum", CreateChangeSetInput{
		ExpectedContextRevision: baseline.CurrentContextRevision, Reason: "Allow a reviewed additional enum value",
		TemplateUpgrade: &TemplateUpgradeInput{
			ExpectedProjectVersion: baseline.ProjectVersion, ExpectedTemplateCopyVersion: baseline.TemplateCopyVersion,
			Fields: definition.Fields, Sections: definition.Sections, Terms: append([]lingdoctemplate.Term{}, definition.Terms...),
			RequiredFields: definition.RequiredFields, Rules: definition.Rules, FieldValues: map[string]string{},
		},
	})
	if err != nil || status != 201 || replayed {
		t.Fatalf("create enum source: status=%d replay=%v err=%v", status, replayed, err)
	}
	source := pr150DecodeChange(t, raw)
	raw, status, replayed, err = f.s.ApplyChangeSet(ctx, f.actor, baseline.ID, source.ID, "r26-widen-enum-apply")
	if err != nil || status != 200 || replayed {
		t.Fatalf("apply enum source: status=%d replay=%v err=%v", status, replayed, err)
	}
	source = pr150DecodeChange(t, raw)
	current, err := f.s.GetProject(ctx, f.actor, baseline.ID)
	if err != nil {
		t.Fatal(err)
	}
	current, _ = r26ApplyIndependentField(t, f.s, f.actor, current, "r26-new-enum-selection", "study_design", "r26-new-option")
	f.reopen(t)
	before := r26InverseStoredState(t, f.s)
	raw, status, replayed, err = f.s.ReverseChangeSet(ctx, f.actor, current.ID, source.ID, "r26-incompatible-old-enum")
	if !errors.Is(err, ErrVersionConflict) || raw != nil || status != 0 || replayed {
		t.Fatalf("old template silently discarded current enum value: status=%d replay=%v body=%s err=%v", status, replayed, raw, err)
	}
	if before != r26InverseStoredState(t, f.s) {
		t.Fatal("incompatible old template changed protected stored state")
	}
	f.reopen(t)
	after, err := f.s.GetProject(ctx, f.actor, current.ID)
	if err != nil || !maps.Equal(after.Spec, current.Spec) || after.TemplateCopyVersion != current.TemplateCopyVersion || after.Spec["study_design"] != "r26-new-option" {
		t.Fatalf("rejected inverse lost current project after reopen: %+v err=%v", after, err)
	}
}
