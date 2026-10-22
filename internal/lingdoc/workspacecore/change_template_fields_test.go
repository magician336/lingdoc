package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"path/filepath"
	"sort"
	"testing"

	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
)

// Reuse the production-migration SQLite harness and the actual copy-edit,
// Spec-save and activation services. Custom fields are never added by direct
// SQL or trusted client field metadata.
type pr150TemplateFixture struct {
	s       *Service
	actor   Actor
	project Project
	chapter string
	path    string
}

func pr150NewTemplateFixture(t *testing.T) *pr150TemplateFixture {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "template-fields.sqlite")
	s := testStore(t, path)
	actor := Actor{TenantID: 25131, UserID: "template-field-owner"}
	seedTenantMember(t, s, actor)
	raw, _, _, err := s.CreateProject(ctx, actor, "template-field-create", CreateProjectInput{Name: "Template fields", TemplateID: "template-demo"})
	if err != nil {
		t.Fatal(err)
	}
	p := asProject(t, raw)
	definition := lingdoctemplate.Clone(p.TemplateCopy.Definition)
	definition.Fields = append(definition.Fields,
		lingdoctemplate.Field{ID: "methods", Label: "Methods", Type: "text"},
		lingdoctemplate.Field{ID: "variables", Label: "Variable count", Type: "number"},
		lingdoctemplate.Field{ID: "inclusion", Label: "Inclusion", Type: "boolean"},
		lingdoctemplate.Field{ID: "study_design", Label: "Study design", Type: "enum", Options: []string{"observational", "experimental"}},
		lingdoctemplate.Field{ID: "bound_unset", Label: "Not in current Spec", Type: "string"})
	raw, _, _, err = s.SaveTemplateCopy(ctx, actor, p.ID, "template-field-copy", TemplateCopyDefinitionInput{
		ExpectedProjectVersion: p.ProjectVersion, ExpectedTemplateCopyVersion: p.TemplateCopyVersion,
		Fields: definition.Fields, Sections: definition.Sections,
		Terms: append([]lingdoctemplate.Term{}, definition.Terms...), RequiredFields: definition.RequiredFields,
		Rules: append([]lingdoctemplate.Rule{}, definition.Rules...),
	})
	if err != nil {
		t.Fatalf("bind real custom template fields: %v", err)
	}
	p = asProject(t, raw)
	raw, _, _, err = s.SaveSpec(ctx, actor, p.ID, "template-field-spec", SaveSpecInput{
		ExpectedSpecRevision: p.SpecRevision,
		Fields: map[string]string{"research_subject": "Synthetic subject", "research_goal": "Synthetic goal",
			"methods": "Method A", "variables": "3", "inclusion": "true", "study_design": "observational",
			"legacy_unbound": "Historic value remains readable"},
	})
	if err != nil {
		t.Fatal(err)
	}
	p = asProject(t, raw)
	raw, _, _, err = s.ActivateProject(ctx, actor, p.ID, "template-field-activate", ActivateProjectInput{
		ExpectedSpecRevision: p.SpecRevision, ExpectedProjectVersion: p.ProjectVersion, ReviewedProjectVersion: p.ProjectVersion,
	})
	if err != nil {
		t.Fatalf("activate custom template: %v", err)
	}
	p = asProject(t, raw)
	if p.TemplateCopy == nil || p.TemplateCopy.Status != TemplateCopyBound {
		t.Fatal("custom fields were not bound through activation")
	}
	chapters, err := s.ListChapters(ctx, actor, p.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("fixture chapters: %v", err)
	}
	return &pr150TemplateFixture{s, actor, p, chapters[0].ID, path}
}

func (f *pr150TemplateFixture) input() ExtendedChangeSetInput {
	return ExtendedChangeSetInput{ExpectedContextRevision: f.project.CurrentContextRevision,
		Fields: map[string]ChangeFieldInput{
			"methods":      {OldValue: "Method A", NewValue: "Method B"},
			"variables":    {OldValue: "3", NewValue: "8"},
			"inclusion":    {OldValue: "true", NewValue: "false"},
			"study_design": {OldValue: "observational", NewValue: "experimental"},
		}, AffectedChapterIDs: []string{f.chapter}, Reason: "Review four bound template fields"}
}

func (f *pr150TemplateFixture) create(t *testing.T, draft bool, key string, input ExtendedChangeSetInput) ChangeSet {
	t.Helper()
	var raw json.RawMessage
	var status int
	var replayed bool
	var err error
	if draft {
		raw, status, replayed, err = f.s.CreateChangeDraft(context.Background(), f.actor, f.project.ID, key, input)
	} else {
		raw, status, replayed, err = f.s.CreateChangeSet(context.Background(), f.actor, f.project.ID, key, CreateChangeSetInput{
			ExpectedContextRevision: input.ExpectedContextRevision, Fields: input.Fields,
			AffectedChapterIDs: input.AffectedChapterIDs, Reason: input.Reason,
		})
	}
	if err != nil || status != 201 || replayed {
		t.Fatalf("create bound-field proposal: status=%d replay=%v err=%v", status, replayed, err)
	}
	return pr150DecodeChange(t, raw)
}

func (f *pr150TemplateFixture) reopen(t *testing.T) {
	t.Helper()
	db, err := f.s.testDB().DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	f.s = testStore(t, f.path)
}

func TestPR150BoundTemplateFieldsLifecycleAndReverse(t *testing.T) {
	for _, draft := range []bool{false, true} {
		name := "assessed-create"
		if draft {
			name = "draft-create"
		}
		t.Run(name, func(t *testing.T) {
			f := pr150NewTemplateFixture(t)
			ctx := context.Background()
			baseline := f.project
			change := f.create(t, draft, "custom-fields-start", f.input())
			if len(change.Fields) != 4 {
				t.Fatalf("custom fields lost at create: %+v", change.Fields)
			}
			edit := f.input()
			edit.Fields["methods"] = ChangeFieldInput{OldValue: "Method A", NewValue: "Method C"}
			edit.Reason = "Human revised the custom methods proposal"
			raw, status, replayed, err := f.s.EditChangeDraft(ctx, f.actor, baseline.ID, change.ID, "custom-fields-edit", edit)
			if err != nil || status != 200 || replayed {
				t.Fatalf("edit: status=%d replay=%v err=%v", status, replayed, err)
			}
			change = pr150DecodeChange(t, raw)
			if change.Status != "draft" || change.Reason != edit.Reason || len(change.Fields) != 4 {
				t.Fatalf("edited proposal: %+v", change)
			}
			raw, status, replayed, err = f.s.AssessChangeSet(ctx, f.actor, baseline.ID, change.ID, "custom-fields-assess")
			if err != nil || status != 200 || replayed || pr150DecodeChange(t, raw).Status != "assessed" {
				t.Fatalf("assess: status=%d replay=%v err=%v", status, replayed, err)
			}
			beforeApply, err := f.s.GetProject(ctx, f.actor, baseline.ID)
			if err != nil || !maps.Equal(beforeApply.Spec, baseline.Spec) || beforeApply.CurrentContextRevision != baseline.CurrentContextRevision || beforeApply.SpecRevision != baseline.SpecRevision || beforeApply.ProjectVersion != baseline.ProjectVersion {
				t.Fatalf("draft/edit/assess changed formal values: %+v err=%v", beforeApply, err)
			}
			raw, status, replayed, err = f.s.ApplyChangeSet(ctx, f.actor, baseline.ID, change.ID, "custom-fields-apply")
			if err != nil || status != 200 || replayed {
				t.Fatalf("apply: status=%d replay=%v err=%v", status, replayed, err)
			}
			applied := pr150DecodeChange(t, raw)
			f.reopen(t)
			current, err := f.s.GetProject(ctx, f.actor, baseline.ID)
			if err != nil || current.CurrentContextRevision != baseline.CurrentContextRevision+1 || current.SpecRevision != baseline.SpecRevision+1 || current.ProjectVersion != baseline.ProjectVersion+1 {
				t.Fatalf("durable formal revision: %+v err=%v", current, err)
			}
			for key, delta := range edit.Fields {
				if current.Spec[key] != delta.NewValue || current.SpecFields[key].Value != delta.NewValue || current.SpecFields[key].Origin != "human" || current.SpecFields[key].Status != "pending_confirmation" {
					t.Fatalf("durable applied custom field %s: value=%q metadata=%+v", key, current.Spec[key], current.SpecFields[key])
				}
			}
			raw, status, replayed, err = f.s.ReverseChangeSet(ctx, f.actor, baseline.ID, change.ID, "custom-fields-reverse")
			if err != nil || status != 201 || replayed {
				t.Fatalf("reverse: status=%d replay=%v err=%v", status, replayed, err)
			}
			inverse := pr150DecodeChange(t, raw)
			if inverse.Status != "assessed" || inverse.ReversesChangeSetID != applied.ID || len(inverse.Fields) != 4 || inverse.BaseContextRevision != current.CurrentContextRevision {
				t.Fatalf("inverse lost current baseline/lineage/custom fields: %+v", inverse)
			}
			if _, status, replayed, err := f.s.ApplyChangeSet(ctx, f.actor, baseline.ID, inverse.ID, "custom-fields-inverse-apply"); err != nil || status != 200 || replayed {
				t.Fatalf("apply inverse: status=%d replay=%v err=%v", status, replayed, err)
			}
			f.reopen(t)
			restored, err := f.s.GetProject(ctx, f.actor, baseline.ID)
			if err != nil || !maps.Equal(restored.Spec, baseline.Spec) || restored.CurrentContextRevision != baseline.CurrentContextRevision+2 || restored.SpecRevision != baseline.SpecRevision+2 {
				t.Fatalf("inverse did not durably restore custom field values: %+v err=%v", restored, err)
			}
			source, err := f.s.GetChangeSet(ctx, f.actor, baseline.ID, applied.ID)
			if err != nil || source.Status != "applied" || source.Reason != edit.Reason || len(source.Fields) != 4 {
				t.Fatalf("reverse rewrote source history: %+v err=%v", source, err)
			}
		})
	}
}

// Public formal projections and durable impact sidecars catch partial assessment,
// revision, confirmation or impact writes. Authorization observations are
// excluded by the shared recovery helper; business audits are retained.
func pr150TemplateState(t *testing.T, f *pr150TemplateFixture, changeID string) string {
	t.Helper()
	formal := pr150RecoveryState(t, f.s, f.actor, f.project.ID, changeID)
	p, err := f.s.GetProject(context.Background(), f.actor, f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := f.s.ListChangeSets(context.Background(), f.actor, f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].ID < changes[j].ID })
	var records []versionedRecordRow
	if err := f.s.testDB().Where("project_id = ?", f.project.ID).Order("id").Find(&records).Error; err != nil {
		t.Fatal(err)
	}
	var provenance []projectRecordRow
	if err := f.s.testDB().Where("project_id = ?", f.project.ID).Order("kind, id").Find(&provenance).Error; err != nil {
		t.Fatal(err)
	}
	return pr150JSON(t, struct {
		Formal     string
		Project    Project
		Changes    []ChangeSet
		Records    []versionedRecordRow
		Provenance []projectRecordRow
	}{formal, p, changes, records, provenance})
}

func pr150AssertNoTemplateOperation(t *testing.T, f *pr150TemplateFixture, operation, target, key string) {
	t.Helper()
	err := f.s.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		_, found, err := tx.Operation(OperationIdentity{Actor: f.actor, Operation: operation, Target: target, Key: key})
		if err == nil && found {
			t.Error("rejected field mutation left a durable successful operation")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPR150InvalidTemplateFieldsLeaveNoBusinessWrites(t *testing.T) {
	cases := []struct {
		name  string
		field string
		old   string
		next  string
	}{
		{"unknown-empty-old", "injected_key", "", "injected"},
		{"historic-unbound", "legacy_unbound", "Historic value remains readable", "rewritten"},
		{"bound-but-not-in-spec", "bound_unset", "", "injected"},
		{"wrong-number", "variables", "3", "many"},
		{"wrong-boolean", "inclusion", "true", "sometimes"},
		{"wrong-enum", "study_design", "observational", "unlisted"},
	}
	for _, tc := range cases {
		for _, stage := range []string{"create", "draft", "edit"} {
			t.Run(tc.name+"/"+stage, func(t *testing.T) {
				f := pr150NewTemplateFixture(t)
				anchor := f.create(t, true, "template-field-anchor", f.input())
				before := pr150TemplateState(t, f, anchor.ID)
				input := f.input()
				input.Fields = map[string]ChangeFieldInput{tc.field: {OldValue: tc.old, NewValue: tc.next}}
				key := "reject-template-" + stage
				target, operation := f.project.ID, "createChangeSet"
				var raw json.RawMessage
				var replayed bool
				var err error
				switch stage {
				case "create":
					raw, _, replayed, err = f.s.CreateChangeSet(context.Background(), f.actor, f.project.ID, key, CreateChangeSetInput{ExpectedContextRevision: input.ExpectedContextRevision, Fields: input.Fields, AffectedChapterIDs: input.AffectedChapterIDs, Reason: input.Reason})
				case "draft":
					operation = "createChangeDraft"
					raw, _, replayed, err = f.s.CreateChangeDraft(context.Background(), f.actor, f.project.ID, key, input)
				case "edit":
					operation, target = "editChangeDraft", f.project.ID+"/"+anchor.ID
					raw, _, replayed, err = f.s.EditChangeDraft(context.Background(), f.actor, f.project.ID, anchor.ID, key, input)
				}
				if !errors.Is(err, ErrInvalidRequest) || replayed || len(raw) != 0 {
					t.Fatalf("invalid field accepted or rejected by an unrelated guard: replay=%v raw=%s err=%v", replayed, raw, err)
				}
				if after := pr150TemplateState(t, f, anchor.ID); after != before {
					t.Fatalf("rejected field mutation changed business state\nbefore=%s\nafter=%s", before, after)
				}
				pr150AssertNoTemplateOperation(t, f, operation, target, key)
			})
		}
	}
}

func TestPR150ExistingFieldProposalsRevalidateAtAssessApplyAndReassess(t *testing.T) {
	for _, stage := range []string{"assess", "apply", "reassess"} {
		for _, field := range []ChangeFieldDelta{
			{Key: "variables", OldValue: "3", NewValue: "not-a-number"},
			{Key: "injected_key", OldValue: "", NewValue: "unbound"},
		} {
			t.Run(stage+"/"+field.Key, func(t *testing.T) {
				f := pr150NewTemplateFixture(t)
				change := f.create(t, false, "persisted-field-proposal", f.input())
				// Seed a historical persisted proposal through the real native
				// store. This deliberately bypasses new-entry validation so the
				// downstream application boundary must check current TemplateCopy.
				next := change
				next.Fields = []ChangeFieldDelta{field}
				if stage == "reassess" {
					next.Status, next.StaleReason = "stale", "context_revision_changed"
				}
				if err := f.s.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
					return tx.(changeSetTransaction).UpdateChangeSet(change, next)
				}); err != nil {
					t.Fatalf("seed legacy persisted proposal: %v", err)
				}
				before := pr150TemplateState(t, f, change.ID)
				key := "legacy-field-" + stage
				var raw json.RawMessage
				var replayed bool
				var err error
				want, operation := ErrInvalidRequest, "assessChangeSet"
				switch stage {
				case "assess":
					raw, _, replayed, err = f.s.AssessChangeSet(context.Background(), f.actor, f.project.ID, change.ID, key)
				case "apply":
					operation = "applyChangeSet"
					raw, _, replayed, err = f.s.ApplyChangeSet(context.Background(), f.actor, f.project.ID, change.ID, key)
				case "reassess":
					operation, want = "reassessChangeSet", ErrInvalidState
					raw, _, replayed, err = f.s.ReassessChangeSet(context.Background(), f.actor, f.project.ID, change.ID, key)
				}
				if !errors.Is(err, want) || replayed || len(raw) != 0 {
					t.Fatalf("persisted invalid proposal crossed %s: replay=%v raw=%s err=%v want=%v", stage, replayed, raw, err, want)
				}
				if after := pr150TemplateState(t, f, change.ID); after != before {
					t.Fatalf("rejected %s changed business state\nbefore=%s\nafter=%s", stage, before, after)
				}
				pr150AssertNoTemplateOperation(t, f, operation, f.project.ID+"/"+change.ID, key)
			})
		}
	}
}

func TestPR150CustomFieldsReassessAfterUnrelatedContextChange(t *testing.T) {
	f := pr150NewTemplateFixture(t)
	ctx := context.Background()
	stale := f.create(t, false, "custom-reassess-original", f.input())
	other := f.input()
	other.Fields = map[string]ChangeFieldInput{"research_goal": {OldValue: f.project.Spec["research_goal"], NewValue: "Unrelated updated goal"}}
	competitor := f.create(t, false, "custom-reassess-competitor", other)
	if _, status, _, err := f.s.ApplyChangeSet(ctx, f.actor, f.project.ID, competitor.ID, "custom-reassess-advance"); err != nil || status != 200 {
		t.Fatalf("advance unrelated context: status=%d err=%v", status, err)
	}
	if _, status, _, err := f.s.ApplyChangeSet(ctx, f.actor, f.project.ID, stale.ID, "custom-reassess-mark-stale"); err != nil || status != 409 {
		t.Fatalf("preserve stale contract: status=%d err=%v", status, err)
	}
	raw, status, replayed, err := f.s.ReassessChangeSet(ctx, f.actor, f.project.ID, stale.ID, "custom-reassess-current")
	if err != nil || status != 201 || replayed {
		t.Fatalf("reassess bound custom fields: status=%d replay=%v err=%v", status, replayed, err)
	}
	change := pr150DecodeChange(t, raw)
	if change.ReassessedFromID != stale.ID || change.Status != "assessed" || len(change.Fields) != 4 {
		t.Fatalf("reassessment lost custom fields or lineage: %+v", change)
	}
	if _, status, _, err := f.s.ApplyChangeSet(ctx, f.actor, f.project.ID, change.ID, "custom-reassess-apply"); err != nil || status != 200 {
		t.Fatalf("apply reassessed custom fields: status=%d err=%v", status, err)
	}
	current, err := f.s.GetProject(ctx, f.actor, f.project.ID)
	if err != nil || current.Spec["research_goal"] != "Unrelated updated goal" {
		t.Fatalf("reassessment overwrote unrelated current data: %+v err=%v", current, err)
	}
	for key, delta := range f.input().Fields {
		if current.Spec[key] != delta.NewValue {
			t.Fatalf("reassessed custom field %s = %q, want %q", key, current.Spec[key], delta.NewValue)
		}
	}
}

func TestPR150ReverseChecksCurrentTemplateOptionsWithoutPartialWrites(t *testing.T) {
	f := pr150NewTemplateFixture(t)
	ctx := context.Background()
	input := f.input()
	input.Fields = map[string]ChangeFieldInput{"study_design": {OldValue: "observational", NewValue: "experimental"}}
	source := f.create(t, false, "enum-original", input)
	if _, status, _, err := f.s.ApplyChangeSet(ctx, f.actor, f.project.ID, source.ID, "enum-original-apply"); err != nil || status != 200 {
		t.Fatalf("apply enum proposal: status=%d err=%v", status, err)
	}
	current, err := f.s.GetProject(ctx, f.actor, f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	definition := lingdoctemplate.Clone(current.TemplateCopy.Definition)
	for i := range definition.Fields {
		if definition.Fields[i].ID == "study_design" {
			definition.Fields[i].Options = []string{"experimental"}
		}
	}
	// A legitimate Owner-applied template migration changes the permitted enum
	// values. The old source proposal stays immutable; it cannot authorize an
	// inverse value which is no longer in the currently bound template.
	raw, status, _, err := f.s.CreateChangeSet(ctx, f.actor, f.project.ID, "enum-template-upgrade", CreateChangeSetInput{
		ExpectedContextRevision: current.CurrentContextRevision, Reason: "Restrict the current study-design options",
		TemplateUpgrade: &TemplateUpgradeInput{
			ExpectedProjectVersion: current.ProjectVersion, ExpectedTemplateCopyVersion: current.TemplateCopyVersion,
			Fields: definition.Fields, Sections: definition.Sections, Terms: append([]lingdoctemplate.Term{}, definition.Terms...),
			RequiredFields: definition.RequiredFields, Rules: append([]lingdoctemplate.Rule{}, definition.Rules...),
		},
	})
	if err != nil || status != 201 {
		t.Fatalf("create enum template migration: status=%d err=%v", status, err)
	}
	upgrade := pr150DecodeChange(t, raw)
	if _, status, _, err := f.s.ApplyChangeSet(ctx, f.actor, f.project.ID, upgrade.ID, "enum-template-upgrade-apply"); err != nil || status != 200 {
		t.Fatalf("apply enum template migration: status=%d err=%v", status, err)
	}
	before := pr150TemplateState(t, f, source.ID)
	raw, _, replayed, err := f.s.ReverseChangeSet(ctx, f.actor, f.project.ID, source.ID, "enum-reverse-denied")
	if !errors.Is(err, ErrInvalidRequest) || replayed || len(raw) != 0 {
		t.Fatalf("reverse ignored current template options: replay=%v raw=%s err=%v", replayed, raw, err)
	}
	if after := pr150TemplateState(t, f, source.ID); after != before {
		t.Fatalf("rejected reverse changed formal state, lineage, impacts or audit\nbefore=%s\nafter=%s", before, after)
	}
	pr150AssertNoTemplateOperation(t, f, "reverseChangeSet", f.project.ID+"/"+source.ID, "enum-reverse-denied")
}
