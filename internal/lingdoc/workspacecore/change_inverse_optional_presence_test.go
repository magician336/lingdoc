package workspacecore

import (
	"bytes"
	"context"
	"maps"
	"path/filepath"
	"testing"

	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
)

func r27OptionalUpgrade(t *testing.T, mode string) (*Service, Actor, Project, Project, ChangeSet, string, string, bool, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "r27-optional-upgrade.sqlite")
	s := testStore(t, path)
	a := Actor{TenantID: 927, UserID: "r27-template-owner"}
	base := seedActiveG3Project(t, s, a)
	fieldID := "r27_optional"
	fieldType := "string"
	if mode == "added_text_empty" {
		fieldType = "text"
	}
	definition := lingdoctemplate.Clone(base.TemplateCopy.Definition)
	definition.Fields = append(definition.Fields, lingdoctemplate.Field{ID: fieldID, Label: "Optional reviewed note", Type: fieldType})
	values := map[string]string{}
	wantPresent := mode != "added_omitted"
	wantValue := ""
	if mode == "added_nonempty" {
		wantValue = "Reviewed optional note"
	}
	if wantPresent {
		values[fieldID] = wantValue
	}
	raw, status, replayed, err := s.CreateChangeSet(context.Background(), a, base.ID, "r27-create-upgrade", CreateChangeSetInput{
		ExpectedContextRevision: base.CurrentContextRevision, Reason: "Reviewed optional field migration",
		TemplateUpgrade: &TemplateUpgradeInput{
			ExpectedProjectVersion: base.ProjectVersion, ExpectedTemplateCopyVersion: base.TemplateCopyVersion,
			Fields: definition.Fields, Sections: definition.Sections, Terms: append([]lingdoctemplate.Term{}, definition.Terms...),
			RequiredFields: definition.RequiredFields, Rules: definition.Rules, FieldValues: values,
		},
	})
	if err != nil || status != 201 || replayed {
		t.Fatalf("create optional upgrade: status=%d replay=%v err=%v", status, replayed, err)
	}
	source := pr150DecodeChange(t, raw)
	raw, status, replayed, err = s.ApplyChangeSet(context.Background(), a, base.ID, source.ID, "r27-apply-upgrade")
	if err != nil || status != 200 || replayed {
		t.Fatalf("apply optional upgrade: status=%d replay=%v err=%v", status, replayed, err)
	}
	source = pr150DecodeChange(t, raw)
	target, err := s.GetProject(context.Background(), a, base.ID)
	if err != nil {
		t.Fatal(err)
	}
	return s, a, base, target, source, path, fieldID, wantPresent, wantValue
}

func r27AssertOptionalMaterialization(t *testing.T, s *Service, a Actor, p Project, source ChangeSet, fieldID string, wantPresent bool, wantValue string) {
	t.Helper()
	persisted, readErr := s.GetProject(context.Background(), a, p.ID)
	if readErr != nil {
		t.Fatal(readErr)
	}
	p = persisted
	value, present := p.Spec[fieldID]
	if present != wantPresent || (present && value != wantValue) {
		t.Errorf("migration field presence/value lost: field=%s present=%v value=%q want_present=%v want_value=%q", fieldID, present, value, wantPresent, wantValue)
	}
	metadata, hasMetadata := p.SpecFields[fieldID]
	if hasMetadata != wantPresent || (hasMetadata && (metadata.Value != wantValue || metadata.Origin != "human" || metadata.Status != "pending_confirmation" || metadata.ModifiedBy != a.UserID)) {
		t.Errorf("migration metadata differs from committed field presence: metadata=%+v present=%v want_present=%v", metadata, hasMetadata, wantPresent)
	}
	history, err := s.ListContextRevisions(context.Background(), a, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, revision := range history {
		if source.TargetContextRevision == nil || revision.Revision != *source.TargetContextRevision {
			continue
		}
		found = true
		value, present = revision.Spec[fieldID]
		if present != wantPresent || (present && value != wantValue) {
			t.Errorf("frozen target field presence lost: %+v", revision.Spec)
		}
		proofs := 0
		for _, ref := range revision.SpecProvenance.Fields {
			if ref.FieldKey == fieldID {
				proofs++
				if !wantPresent || ref.ValueHash != hashImpactValue(wantValue) {
					t.Errorf("frozen optional-field proof mismatch: %+v", ref)
				}
			}
		}
		if (wantPresent && proofs != 1) || (!wantPresent && proofs != 0) {
			t.Errorf("frozen proof presence=%d want_present=%v", proofs, wantPresent)
		}
	}
	if !found {
		t.Fatal("applied source target history is missing")
	}
}

func TestR27OptionalTemplateFieldUpgradeReverseAfterReopen(t *testing.T) {
	for _, mode := range []string{"added_string_empty", "added_text_empty", "added_omitted", "added_nonempty"} {
		t.Run(mode, func(t *testing.T) {
			s, a, base, target, source, path, fieldID, wantPresent, wantValue := r27OptionalUpgrade(t, mode)
			// This later independent edit must survive reversal too.
			current, _ := r26ApplyIndependentField(t, s, a, target, "r27-independent", "research_subject", "Later independent subject")
			s = reopenSourcePolicyStore(t, s, path)
			raw, status, replayed, err := s.ReverseChangeSet(context.Background(), a, current.ID, source.ID, "r27-reverse-upgrade")
			r27AssertOptionalMaterialization(t, s, a, target, source, fieldID, wantPresent, wantValue)
			if err != nil || status != 201 || replayed {
				_, present := target.Spec[fieldID]
				t.Fatalf("successful optional upgrade cannot be reversed: mode=%s actual_present=%v explicit_value=%q status=%d replay=%v err=%v", mode, present, wantValue, status, replayed, err)
			}
			first := bytes.Clone(raw)
			inverse := pr150DecodeChange(t, raw)
			raw, status, replayed, err = s.ApplyChangeSet(context.Background(), a, current.ID, inverse.ID, "r27-apply-inverse")
			if err != nil || status != 200 || replayed {
				t.Fatalf("apply optional inverse: status=%d replay=%v err=%v", status, replayed, err)
			}
			s = reopenSourcePolicyStore(t, s, path)
			after, err := s.GetProject(context.Background(), a, current.ID)
			if err != nil || !maps.Equal(after.Spec, current.Spec) || after.TemplateCopy.Definition.ContentHash != base.TemplateCopy.Definition.ContentHash {
				t.Fatalf("inverse lost independent edit/readable orphan or historical template: project=%+v err=%v", after, err)
			}
			if after.CurrentContextRevision != current.CurrentContextRevision+1 || after.SpecRevision != current.SpecRevision+1 || after.TemplateCopyVersion != current.TemplateCopyVersion+1 {
				t.Fatalf("inverse did not append exactly one version: before=%+v after=%+v", current, after)
			}
			r25AssertInverseHistory(t, s, a, current.ID, source, inverse)
			beforeReplay := r26InverseStoredState(t, s)
			raw, status, replayed, err = s.ReverseChangeSet(context.Background(), a, current.ID, source.ID, "r27-reverse-upgrade")
			if err != nil || status != 201 || !replayed || !bytes.Equal(first, raw) {
				t.Fatalf("inverse lost-response replay: status=%d replay=%v equal=%v err=%v", status, replayed, bytes.Equal(first, raw), err)
			}
			if beforeReplay != r26InverseStoredState(t, s) {
				t.Fatal("inverse replay wrote protected rows")
			}
		})
	}
}

func TestR27ExistingOptionalFieldCanClearAndReverse(t *testing.T) {
	f := pr150NewTemplateFixture(t)
	base := f.project
	definition := lingdoctemplate.Clone(base.TemplateCopy.Definition)
	definition.Fields[0].Label += " R27 clear"
	raw, status, replayed, err := f.s.CreateChangeSet(context.Background(), f.actor, base.ID, "r27-clear-existing", CreateChangeSetInput{
		ExpectedContextRevision: base.CurrentContextRevision, Reason: "Explicitly clear optional existing field",
		TemplateUpgrade: &TemplateUpgradeInput{
			ExpectedProjectVersion: base.ProjectVersion, ExpectedTemplateCopyVersion: base.TemplateCopyVersion,
			Fields: definition.Fields, Sections: definition.Sections, Terms: append([]lingdoctemplate.Term{}, definition.Terms...),
			RequiredFields: definition.RequiredFields, Rules: definition.Rules, FieldValues: map[string]string{"methods": ""},
		},
	})
	if err != nil || status != 201 || replayed {
		t.Fatalf("create clear: status=%d replay=%v err=%v", status, replayed, err)
	}
	source := pr150DecodeChange(t, raw)
	raw, status, replayed, err = f.s.ApplyChangeSet(context.Background(), f.actor, base.ID, source.ID, "r27-clear-existing-apply")
	if err != nil || status != 200 || replayed {
		t.Fatalf("apply clear: status=%d replay=%v err=%v", status, replayed, err)
	}
	source = pr150DecodeChange(t, raw)
	current, err := f.s.GetProject(context.Background(), f.actor, base.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.reopen(t)
	r27AssertOptionalMaterialization(t, f.s, f.actor, current, source, "methods", true, "")
	raw, status, replayed, err = f.s.ReverseChangeSet(context.Background(), f.actor, base.ID, source.ID, "r27-clear-existing-reverse")
	if err != nil || status != 201 || replayed {
		t.Fatalf("reverse clear: status=%d replay=%v err=%v", status, replayed, err)
	}
	inverse := pr150DecodeChange(t, raw)
	_, status, replayed, err = f.s.ApplyChangeSet(context.Background(), f.actor, base.ID, inverse.ID, "r27-clear-existing-reverse-apply")
	if err != nil || status != 200 || replayed {
		t.Fatalf("apply clear inverse: status=%d replay=%v err=%v", status, replayed, err)
	}
	f.reopen(t)
	restored, err := f.s.GetProject(context.Background(), f.actor, base.ID)
	if err != nil || !maps.Equal(restored.Spec, base.Spec) {
		t.Fatalf("clear inverse did not restore original value: spec=%+v err=%v", restored.Spec, err)
	}
}

func TestR27OptionalEmptyMigrationAuditFailureRollsBackAndRecovers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r27-empty-atomicity.sqlite")
	s := testStore(t, path)
	a := Actor{TenantID: 9271, UserID: "r27-empty-owner"}
	base := seedActiveG3Project(t, s, a)
	definition := lingdoctemplate.Clone(base.TemplateCopy.Definition)
	definition.Fields = append(definition.Fields, lingdoctemplate.Field{ID: "r27_empty", Label: "Optional note", Type: "string"})
	raw, _, _, err := s.CreateChangeSet(context.Background(), a, base.ID, "r27-empty-atomic-create", CreateChangeSetInput{
		ExpectedContextRevision: base.CurrentContextRevision, Reason: "Explicit empty is a committed field",
		TemplateUpgrade: &TemplateUpgradeInput{
			ExpectedProjectVersion: base.ProjectVersion, ExpectedTemplateCopyVersion: base.TemplateCopyVersion,
			Fields: definition.Fields, Sections: definition.Sections, Terms: append([]lingdoctemplate.Term{}, definition.Terms...),
			RequiredFields: definition.RequiredFields, Rules: definition.Rules, FieldValues: map[string]string{"r27_empty": ""},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	source := pr150DecodeChange(t, raw)
	if err = s.testDB().Exec("CREATE TRIGGER r27_empty_audit_failure BEFORE INSERT ON lingdoc_project_audits WHEN NEW.action = 'project.change_set.apply' BEGIN SELECT RAISE(ABORT, 'r27 empty audit unavailable'); END").Error; err != nil {
		t.Fatal(err)
	}
	before := r26InverseStoredState(t, s)
	raw, status, replayed, err := s.ApplyChangeSet(context.Background(), a, base.ID, source.ID, "r27-empty-atomic-apply")
	if err == nil || raw != nil || status != 0 || replayed {
		t.Fatalf("audit failure accepted migration: status=%d replay=%v body=%s err=%v", status, replayed, raw, err)
	}
	if before != r26InverseStoredState(t, s) {
		t.Fatal("failed explicit-empty migration left project/template/history/provenance/operation/audit writes")
	}
	if err = s.testDB().Exec("DROP TRIGGER r27_empty_audit_failure").Error; err != nil {
		t.Fatal(err)
	}
	s = reopenSourcePolicyStore(t, s, path)
	raw, status, replayed, err = s.ApplyChangeSet(context.Background(), a, base.ID, source.ID, "r27-empty-atomic-apply")
	if err != nil || status != 200 || replayed {
		t.Fatalf("same-key recovery: status=%d replay=%v err=%v", status, replayed, err)
	}
	source = pr150DecodeChange(t, raw)
	current, err := s.GetProject(context.Background(), a, base.ID)
	if err != nil {
		t.Fatal(err)
	}
	r27AssertOptionalMaterialization(t, s, a, current, source, "r27_empty", true, "")
	s = reopenSourcePolicyStore(t, s, path)
	before = r26InverseStoredState(t, s)
	again, status, replayed, err := s.ApplyChangeSet(context.Background(), a, base.ID, source.ID, "r27-empty-atomic-apply")
	if err != nil || status != 200 || !replayed || !bytes.Equal(raw, again) {
		t.Fatalf("recovery receipt changed: status=%d replay=%v err=%v", status, replayed, err)
	}
	if before != r26InverseStoredState(t, s) {
		t.Fatal("recovered replay added protected writes")
	}
}
