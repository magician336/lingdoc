package workspacecore

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
)

func projectionGuardEdit(project Project) TemplateCopyDefinitionInput {
	definition := lingdoctemplate.Clone(project.TemplateCopy.Definition)
	return TemplateCopyDefinitionInput{
		ExpectedProjectVersion: project.ProjectVersion, ExpectedTemplateCopyVersion: project.TemplateCopyVersion,
		Fields: definition.Fields, Sections: definition.Sections, Terms: append([]lingdoctemplate.Term{}, definition.Terms...),
		RequiredFields: definition.RequiredFields, Rules: definition.Rules,
	}
}

func TestLegacySidecarRejectsDirectProjectionEditsInDraftAndActive(t *testing.T) {
	ctx := context.Background()
	svc, owner, project, _ := sourcePolicyProject(t, filepath.Join(t.TempDir(), "legacy-projection-guard.db"))
	raw, _, _, err := svc.SaveSpec(ctx, owner, project.ID, "legacy-projection-spec", SaveSpecInput{
		ExpectedSpecRevision: project.SpecRevision,
		Fields:               map[string]string{"research_subject": "subject", "research_goal": "goal"},
	})
	if err != nil {
		t.Fatal(err)
	}
	project = asProject(t, raw)
	edit := projectionGuardEdit(project)
	edit.Rules[0].Message = "unreviewed legacy projection change"
	if _, err := svc.PreviewTemplateCopyEdit(ctx, owner, project.ID, edit); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("legacy projection preview = %v, want invalid request", err)
	}
	if _, _, _, err := svc.SaveTemplateCopy(ctx, owner, project.ID, "legacy-projection-save", edit); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("legacy projection save = %v, want invalid request", err)
	}
	unchanged, err := svc.GetProject(ctx, owner, project.ID)
	if err != nil || unchanged.ProjectVersion != project.ProjectVersion || unchanged.TemplateCopyVersion != project.TemplateCopyVersion ||
		!reflect.DeepEqual(unchanged.TemplateCopy, project.TemplateCopy) {
		t.Fatalf("rejected draft edit changed the copy: %+v, err=%v", unchanged, err)
	}
	var copies, operations, audits int64
	if err := svc.testDB().Table("lingdoc_project_template_copies").Where("project_id = ?", project.ID).Count(&copies).Error; err != nil || copies != 1 {
		t.Fatalf("rejected draft edit left copies=%d, err=%v", copies, err)
	}
	if err := svc.testDB().Table("lingdoc_operations").Where("key = ?", "legacy-projection-save").Count(&operations).Error; err != nil || operations != 0 {
		t.Fatalf("rejected draft edit left operation count=%d, err=%v", operations, err)
	}
	if err := svc.testDB().Table("lingdoc_project_audits").Where("project_id = ? AND action = ?", project.ID, "project_template.edit").Count(&audits).Error; err != nil || audits != 0 {
		t.Fatalf("rejected draft edit left success audit count=%d, err=%v", audits, err)
	}

	raw, _, _, err = svc.ActivateProject(ctx, owner, project.ID, "legacy-projection-activate", ActivateProjectInput{
		ExpectedSpecRevision: project.SpecRevision, ExpectedProjectVersion: project.ProjectVersion, ReviewedProjectVersion: project.ProjectVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	active := asProject(t, raw)
	edit = projectionGuardEdit(active)
	edit.Rules[0].Message = "unreviewed active legacy projection change"
	input := CreateChangeSetInput{ExpectedContextRevision: active.CurrentContextRevision, Reason: "Attempt direct legacy rule edit",
		TemplateUpgrade: &TemplateUpgradeInput{ExpectedProjectVersion: active.ProjectVersion, ExpectedTemplateCopyVersion: active.TemplateCopyVersion,
			Fields: edit.Fields, Sections: edit.Sections, Terms: edit.Terms, RequiredFields: edit.RequiredFields, Rules: edit.Rules}}
	if _, _, _, err := svc.CreateChangeSet(ctx, owner, project.ID, "legacy-projection-upgrade", input); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("active legacy projection upgrade = %v, want invalid request", err)
	}
	unchanged, err = svc.GetProject(ctx, owner, project.ID)
	if err != nil || unchanged.ProjectVersion != active.ProjectVersion || unchanged.TemplateCopyVersion != active.TemplateCopyVersion ||
		!reflect.DeepEqual(unchanged.TemplateCopy, active.TemplateCopy) {
		t.Fatalf("rejected active edit changed the copy: %+v, err=%v", unchanged, err)
	}
	var changes int64
	if err := svc.testDB().Table("lingdoc_change_sets").Where("project_id = ?", project.ID).Count(&changes).Error; err != nil || changes != 0 {
		t.Fatalf("rejected active edit left changesets=%d, err=%v", changes, err)
	}
	if err := svc.testDB().Table("lingdoc_operations").Where("key = ?", "legacy-projection-upgrade").Count(&operations).Error; err != nil || operations != 0 {
		t.Fatalf("rejected active edit left operation count=%d, err=%v", operations, err)
	}
}

func TestUnboundTemplateRulesRemainEditable(t *testing.T) {
	ctx := context.Background()
	svc := testStore(t, filepath.Join(t.TempDir(), "unbound-template-rules.db"))
	owner := Actor{TenantID: 42120, UserID: "unbound-owner"}
	seedTenantMember(t, svc, owner)
	raw, _, _, err := svc.CreateProject(ctx, owner, "unbound-rule-create", CreateProjectInput{Name: "Unbound rule edit", TemplateID: lingdoctemplate.DemoTemplateID})
	if err != nil {
		t.Fatal(err)
	}
	project := asProject(t, raw)
	raw, _, _, err = svc.SaveSpec(ctx, owner, project.ID, "unbound-rule-spec", SaveSpecInput{
		ExpectedSpecRevision: project.SpecRevision, Fields: map[string]string{"research_subject": "subject", "research_goal": "goal"},
	})
	if err != nil {
		t.Fatal(err)
	}
	project = asProject(t, raw)
	if project.TemplateCopy.RuleSetSnapshot != nil || project.TemplateCopy.Definition.RuleSetSnapshot != nil {
		t.Fatal("test requires an unbound template")
	}
	edit := projectionGuardEdit(project)
	edit.Rules[0].Message = "A reviewed independent rule message"
	if _, err := svc.PreviewTemplateCopyEdit(ctx, owner, project.ID, edit); err != nil {
		t.Fatalf("preview unbound rule edit: %v", err)
	}
	raw, _, _, err = svc.SaveTemplateCopy(ctx, owner, project.ID, "unbound-rule-edit", edit)
	if err != nil {
		t.Fatalf("save unbound rule edit: %v", err)
	}
	updated := asProject(t, raw)
	if updated.TemplateCopy.Definition.Rules[0].Message != edit.Rules[0].Message || updated.TemplateCopy.RulesetHash == project.TemplateCopy.RulesetHash {
		t.Fatal("unbound rule change or its effective hash was not persisted")
	}
}
