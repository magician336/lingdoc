package workspacecore

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/template"
)

func activeArchiveFixture(t *testing.T) (*Service, Actor, Project, TemplateUpgradeInput) {
	t.Helper()
	s := testStore(t, filepath.Join(t.TempDir(), "active-archive.db"))
	actor := Actor{TenantID: 9873, UserID: "active-archive-owner"}
	project := seedActiveImpactProject(t, s, actor)
	definition := template.Clone(project.TemplateCopy.Definition)
	definition.Fields = slices.DeleteFunc(definition.Fields, func(field template.Field) bool { return field.ID == "research_goal" })
	definition.RequiredFields = slices.DeleteFunc(definition.RequiredFields, func(id string) bool { return id == "research_goal" })
	upgrade := TemplateUpgradeInput{ExpectedProjectVersion: project.ProjectVersion, ExpectedTemplateCopyVersion: project.TemplateCopyVersion,
		Fields: definition.Fields, Sections: definition.Sections, Terms: append([]template.Term{}, definition.Terms...),
		RequiredFields: definition.RequiredFields, Rules: definition.Rules, FieldValues: map[string]string{}}
	return s, actor, project, upgrade
}

func activeArchiveInput(project Project, fieldID string, upgrade TemplateUpgradeInput) CreateFieldArchiveInput {
	return CreateFieldArchiveInput{FieldID: fieldID, ExpectedSpecRevision: project.SpecRevision,
		ExpectedContextRevision: project.CurrentContextRevision, TemplateUpgrade: &upgrade}
}

func TestActiveArchiveOnlyChangesProjectAtAssessedApply(t *testing.T) {
	s, actor, project, upgrade := activeArchiveFixture(t)
	created, err := s.CreateFieldArchiveResult(context.Background(), actor, project.ID, "active-archive-create", activeArchiveInput(project, "research_goal", upgrade))
	if err != nil || created.Archive.Status != "recorded" {
		t.Fatalf("archive: %+v err=%v", created, err)
	}
	view, err := s.GetFieldArchive(context.Background(), actor, project.ID, created.Archive.ID)
	if err != nil || view.Value != project.Spec["research_goal"] || view.ContextRevision != project.CurrentContextRevision {
		t.Fatalf("archive view: %+v err=%v", view, err)
	}
	if _, err := s.RemoveArchivedFieldResult(context.Background(), actor, project.ID, created.Archive.ID, "active-direct-remove", RemoveArchivedFieldInput{
		Confirmed: true, ExpectedArchiveVersion: 1, ExpectedSpecRevision: project.SpecRevision, TemplateCopy: archiveCandidate(project, "research_goal"),
	}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("active direct removal accepted: %v", err)
	}
	upgrade.ArchivedRemovals = []ArchivedFieldReference{{FieldID: "research_goal", ArchiveID: created.Archive.ID, ExpectedArchiveVersion: 1}}
	input := CreateChangeSetInput{ExpectedContextRevision: project.CurrentContextRevision, Reason: "archive and remove goal", TemplateUpgrade: &upgrade}
	raw, _, _, err := s.CreateChangeSet(context.Background(), actor, project.ID, "active-archive-change", input)
	if err != nil {
		t.Fatalf("assess archive removal: %v", err)
	}
	change := decodeAgentJSON[ChangeSet](t, raw, nil)
	if change.TemplateUpgrade == nil || len(change.TemplateUpgrade.ArchivedRemovals) != 1 || change.Status != ChangeSetStatusAssessed {
		t.Fatalf("assessed change lost archive reference: %+v", change)
	}
	before, err := s.GetProject(context.Background(), actor, project.ID)
	if err != nil || before.Spec["research_goal"] != project.Spec["research_goal"] || before.ProjectVersion != project.ProjectVersion {
		t.Fatalf("assessment removed or changed the field: %+v err=%v", before, err)
	}
	if _, _, _, err := s.ApplyChangeSet(context.Background(), actor, project.ID, change.ID, "active-archive-apply"); err != nil {
		t.Fatalf("apply archive removal: %v", err)
	}
	after, err := s.GetProject(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := after.Spec["research_goal"]; exists {
		t.Fatal("applied field was retained")
	}
	if _, exists := after.SpecFields["research_goal"]; exists {
		t.Fatal("applied metadata was retained")
	}
	if after.Spec["research_subject"] != project.Spec["research_subject"] || after.TemplateCopyVersion != project.TemplateCopyVersion+1 ||
		after.SpecRevision != project.SpecRevision+1 || after.CurrentContextRevision != project.CurrentContextRevision+1 {
		t.Fatalf("wrong post-apply versions or unrelated field: %+v", after)
	}
	view, err = s.GetFieldArchive(context.Background(), actor, project.ID, created.Archive.ID)
	if err != nil || view.Status != "removed" || view.ArchiveVersion != 2 || view.Value != project.Spec["research_goal"] {
		t.Fatalf("archive not retained after apply: %+v err=%v", view, err)
	}
	raw, _, _, err = s.ReverseChangeSet(context.Background(), actor, project.ID, change.ID, "active-archive-reverse")
	if err != nil {
		t.Fatalf("prepare reverse: %v", err)
	}
	inverse := decodeAgentJSON[ChangeSet](t, raw, nil)
	if inverse.TemplateUpgrade == nil || len(inverse.TemplateUpgrade.ArchiveRestores) != 1 {
		t.Fatalf("inverse lost server-owned restore reference: %+v", inverse)
	}
	if slices.Contains(inverse.TemplateUpgrade.Preview.MissingRequired, "research_goal") {
		t.Fatal("inverse preview incorrectly reports restored required field missing")
	}
	if _, present := inverse.TemplateUpgrade.FieldValues["research_goal"]; present {
		t.Fatal("inverse disclosed archived value in ordinary field values")
	}
	if _, _, _, err := s.ApplyChangeSet(context.Background(), actor, project.ID, inverse.ID, "active-archive-reverse-apply"); err != nil {
		t.Fatalf("apply reverse: %v", err)
	}
	restored, err := s.GetProject(context.Background(), actor, project.ID)
	if err != nil || restored.Spec["research_goal"] != project.Spec["research_goal"] ||
		!reflect.DeepEqual(restored.SpecFields["research_goal"], project.SpecFields["research_goal"]) ||
		restored.Spec["research_subject"] != after.Spec["research_subject"] {
		t.Fatalf("inverse failed to restore exact old metadata or preserved fields: %+v err=%v", restored, err)
	}
}

func TestActiveArchiveRejectsChangedPlanAndDuplicateReferences(t *testing.T) {
	s, actor, project, upgrade := activeArchiveFixture(t)
	created, err := s.CreateFieldArchiveResult(context.Background(), actor, project.ID, "active-archive-negative-create", activeArchiveInput(project, "research_goal", upgrade))
	if err != nil {
		t.Fatal(err)
	}
	ref := ArchivedFieldReference{FieldID: "research_goal", ArchiveID: created.Archive.ID, ExpectedArchiveVersion: 1}
	upgrade.ArchivedRemovals = []ArchivedFieldReference{ref, ref}
	input := CreateChangeSetInput{ExpectedContextRevision: project.CurrentContextRevision, Reason: "negative archival", TemplateUpgrade: &upgrade}
	if _, _, _, err := s.CreateChangeSet(context.Background(), actor, project.ID, "active-archive-duplicate", input); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("duplicate references: %v", err)
	}
	upgrade.ArchivedRemovals = []ArchivedFieldReference{ref}
	upgrade.Fields = append([]template.Field{}, upgrade.Fields...)
	upgrade.Fields[0].Label += " changed"
	if _, _, _, err := s.CreateChangeSet(context.Background(), actor, project.ID, "active-archive-changed-plan", input); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("changed plan: %v", err)
	}
	after, err := s.GetProject(context.Background(), actor, project.ID)
	if err != nil || !reflect.DeepEqual(after.Spec, project.Spec) || after.ProjectVersion != project.ProjectVersion {
		t.Fatalf("rejection changed project: %+v %v", after, err)
	}
}
