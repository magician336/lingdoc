package workspacecore

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestActiveProjectRejectsOrdinarySpecSave(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "g1-baseline.db"))
	actor := Actor{TenantID: 81, UserID: "owner"}
	seedTenantMember(t, svc, actor)

	created, _, _, err := svc.CreateProject(context.Background(), actor, "g1-create-01", CreateProjectInput{Name: "G1 baseline", TemplateID: "template-demo"})
	if err != nil {
		t.Fatal(err)
	}
	projectID := asProject(t, created).ID
	if _, _, _, err := svc.SaveSpec(context.Background(), actor, projectID, "g1-spec-01", SaveSpecInput{ExpectedSpecRevision: 0, Fields: map[string]string{"research_subject": "subject", "research_goal": "goal"}}); err != nil {
		t.Fatal(err)
	}
	activated, _, _, err := svc.ActivateProject(context.Background(), actor, projectID, "g1-activate-01", ActivateProjectInput{ExpectedSpecRevision: 1, ExpectedProjectVersion: 2, ReviewedProjectVersion: 2})
	if err != nil {
		t.Fatal(err)
	}
	active := asProject(t, activated)
	if active.Status != "active" || active.CurrentContextRevision != 1 || active.DeliveryStatus != "NOT_READY" || active.BaselineConfirmationID == "" {
		t.Fatalf("activation baseline = %+v", active)
	}
	if _, _, _, err := svc.SaveSpec(context.Background(), actor, projectID, "g1-spec-active-01", SaveSpecInput{ExpectedSpecRevision: 1, Fields: map[string]string{"research_subject": "changed", "research_goal": "goal"}}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("active ordinary save error = %v, want %v", err, ErrInvalidState)
	}
	current, err := svc.GetProject(context.Background(), actor, projectID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Spec["research_subject"] != "subject" || current.SpecRevision != 1 || current.ProjectVersion != active.ProjectVersion || current.CurrentContextRevision != 1 {
		t.Fatalf("active save changed baseline = %+v", current)
	}
}

func TestDraftSpecCarriesProvenanceAndCandidatePool(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "g1-provenance.db"))
	actor := Actor{TenantID: 82, UserID: "owner"}
	seedTenantMember(t, svc, actor)
	created, _, _, err := svc.CreateProject(context.Background(), actor, "g1-create-02", CreateProjectInput{Name: "G1 provenance", TemplateID: "template-demo"})
	if err != nil {
		t.Fatal(err)
	}
	projectID := asProject(t, created).ID
	seedActualTurn(t, svc, actor, projectID, "session-1", "turn-3", "样本")
	provenance := &ProvenanceRecord{SourceType: "conversation", SourceID: "session-1", SourceVersion: "turn-3", Locator: "turns/3#text", Purpose: "project_spec", CreatedBy: actor.UserID, Accessible: true}
	if _, _, _, err := svc.SaveSpec(context.Background(), actor, projectID, "g1-spec-02", SaveSpecInput{
		ExpectedSpecRevision: 0,
		Fields:               map[string]string{"research_subject": "样本"},
		FieldMetadata:        map[string]SpecFieldInput{"research_subject": {Origin: "ai_generated", Status: "pending_confirmation", Provenance: provenance}},
	}); err != nil {
		t.Fatal(err)
	}
	current, err := svc.GetProject(context.Background(), actor, projectID)
	if err != nil {
		t.Fatal(err)
	}
	field := current.SpecFields["research_subject"]
	if field.Value != "样本" || field.Origin != "ai_generated" || field.Status != "pending_confirmation" || field.Provenance == nil || field.Provenance.SourceID != "session-1" {
		t.Fatalf("spec provenance = %+v", field)
	}
	if _, _, _, err := svc.CreateDraftCandidate(context.Background(), actor, projectID, "g1-candidate-02", DraftCandidateInput{Kind: "retrieval", Title: "背景线索", Content: "原文线索", Level: "background", Provenance: provenance}); err != nil {
		t.Fatal(err)
	}
	revoked := *provenance
	revoked.Accessible = false
	revoked.SourceVersion = "nonexistent-turn"
	if _, _, _, err := svc.CreateDraftCandidate(context.Background(), actor, projectID, "g1-candidate-03", DraftCandidateInput{Kind: "retrieval", Title: "受限线索", Content: "受限正文", Level: "background", Provenance: &revoked}); err != nil {
		t.Fatal(err)
	}
	candidates, err := svc.ListDraftCandidates(context.Background(), actor, projectID)
	if err != nil || len(candidates) != 2 {
		t.Fatalf("candidates = %+v, err=%v", candidates, err)
	}
	if candidates[0].BasedOnContextRevision != 0 || candidates[0].Level != "background" || candidates[0].Provenance == nil {
		t.Fatalf("candidate = %+v", candidates[0])
	}
	if candidates[1].Title != "来源待复核" || candidates[1].Content != "" || candidates[1].Provenance == nil || !candidates[1].Provenance.NeedsReview {
		t.Fatalf("unidentified candidate = %+v", candidates[1])
	}
	audits, err := svc.ListAuditEvents(context.Background(), actor, projectID)
	if err != nil || len(audits) != 3 {
		t.Fatalf("audits = %+v, err=%v", audits, err)
	}
	if audits[0].Action != "project_spec.save" || audits[1].Action != "draft_candidate.create" {
		t.Fatalf("audit actions = %+v", audits)
	}
}

func TestActivationDiffBindsConfirmationToProjectVersion(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "g1-diff.db"))
	actor := Actor{TenantID: 83, UserID: "owner"}
	seedTenantMember(t, svc, actor)
	created, _, _, err := svc.CreateProject(context.Background(), actor, "g1-create-03", CreateProjectInput{Name: "G1 diff", TemplateID: "template-demo"})
	if err != nil {
		t.Fatal(err)
	}
	project := asProject(t, created)
	if _, _, _, err := svc.SaveSpec(context.Background(), actor, project.ID, "g1-spec-03", SaveSpecInput{ExpectedSpecRevision: 0, Fields: map[string]string{"research_subject": "subject", "research_goal": "goal"}}); err != nil {
		t.Fatal(err)
	}
	diff, err := svc.ActivationDiff(context.Background(), actor, project.ID, 0)
	if err != nil || len(diff.ChangedFields) == 0 || diff.CurrentProjectVersion != 2 {
		t.Fatalf("activation diff = %+v, err=%v", diff, err)
	}
	if _, _, _, err := svc.SaveSpec(context.Background(), actor, project.ID, "g1-spec-04", SaveSpecInput{ExpectedSpecRevision: 1, Fields: map[string]string{"research_subject": "subject", "research_goal": "changed"}}); err != nil {
		t.Fatal(err)
	}
	latestDiff, err := svc.ActivationDiff(context.Background(), actor, project.ID, diff.CurrentProjectVersion)
	if err != nil || len(latestDiff.ChangedFields) != 1 || latestDiff.ChangedFields[0].Key != "research_goal" {
		t.Fatalf("precise activation diff = %+v, err=%v", latestDiff, err)
	}
	if _, _, _, err := svc.ActivateProject(context.Background(), actor, project.ID, "g1-activate-03", ActivateProjectInput{ExpectedSpecRevision: 2, ExpectedProjectVersion: diff.CurrentProjectVersion, ReviewedProjectVersion: diff.CurrentProjectVersion}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale activation error = %v, want %v", err, ErrVersionConflict)
	}
}

type provenanceTemplateReader struct{}

func (provenanceTemplateReader) Get(id, version string) (Template, error) {
	if id == "template-alt" && (version == "" || version == "2") {
		return Template{ID: "template-alt", Name: "替代演示模板", Version: "2", RequiredFields: []string{"research_subject", "research_method"}, Sections: []Section{{ID: "method", Title: "方法", Required: true}}}, nil
	}
	if id == "template-alt" && version == "3" {
		return Template{ID: "template-alt", Name: "兼容旧字段的替代演示模板", Version: "3", RequiredFields: []string{"research_subject", "research_method"}, Sections: []Section{{ID: "method", Title: "方法", Required: true}}, Fields: []TemplateField{
			{ID: "research_subject", Label: "研究对象", Type: "string", Required: true},
			{ID: "research_method", Label: "研究方法", Type: "string", Required: true},
			{ID: "research_goal", Label: "研究目标", Type: "string"},
			{ID: "legacy", Label: "历史字段", Type: "string"},
		}}, nil
	}
	return ContractDemoTemplate{}.Get(id, version)
}

func TestTemplateMigrationRejectsUnresolvedRisksAndCommitsWithoutDroppingValues(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "g1-template-migration.db"))
	svc.templates = provenanceTemplateReader{}
	actor := Actor{TenantID: 85, UserID: "owner"}
	seedTenantMember(t, svc, actor)
	created, _, _, err := svc.CreateProject(context.Background(), actor, "g1-create-05", CreateProjectInput{Name: "G1 template", TemplateID: "template-demo"})
	if err != nil {
		t.Fatal(err)
	}
	project := asProject(t, created)
	if _, _, _, err := svc.SaveSpec(context.Background(), actor, project.ID, "g1-spec-05", SaveSpecInput{ExpectedSpecRevision: 0, Fields: map[string]string{"research_subject": "主题", "research_goal": "目标", "legacy": "保留"}}); err != nil {
		t.Fatal(err)
	}
	preview, err := svc.PreviewTemplateMigration(context.Background(), actor, project.ID, TemplateMigrationInput{ExpectedProjectVersion: 2, TemplateID: "template-alt", TemplateVersion: "2"})
	if err != nil || len(preview.Orphaned) != 2 || len(preview.MissingRequired) != 1 {
		t.Fatalf("preview = %+v, err=%v", preview, err)
	}
	// Historically this switch was accepted with missing and orphaned fields.
	// Unresolved risks now block the write without discarding either old value.
	if _, _, _, err := svc.ChangeTemplate(context.Background(), actor, project.ID, "g1-template-05", TemplateMigrationInput{ExpectedProjectVersion: 2, TemplateID: "template-alt", TemplateVersion: "2"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("unresolved migration error = %v, want %v", err, ErrInvalidRequest)
	}
	unchanged, err := svc.GetProject(context.Background(), actor, project.ID)
	if err != nil || unchanged.ProjectVersion != 2 || unchanged.TemplateID != "template-demo" || unchanged.Spec["research_goal"] != "目标" || unchanged.Spec["legacy"] != "保留" {
		t.Fatalf("rejected migration changed the project or old values: %+v err=%v", unchanged, err)
	}
	// Keep the historic values and choose a target that declares their fields.
	// The newly required field can be filled before the switch and re-previewed.
	filled, _, _, err := svc.SaveSpec(context.Background(), actor, project.ID, "g1-template-resolve", SaveSpecInput{ExpectedSpecRevision: unchanged.SpecRevision, Fields: map[string]string{"research_subject": "主题", "research_goal": "目标", "legacy": "保留", "research_method": "方法"}})
	if err != nil {
		t.Fatal(err)
	}
	resolved := asProject(t, filled)
	resolvedPreview, err := svc.PreviewTemplateMigration(context.Background(), actor, project.ID, TemplateMigrationInput{ExpectedProjectVersion: resolved.ProjectVersion, TemplateID: "template-alt", TemplateVersion: "3"})
	if err != nil || len(resolvedPreview.Orphaned)+len(resolvedPreview.MissingRequired)+len(resolvedPreview.Incompatible) != 0 {
		t.Fatalf("resolved preview = %+v, err=%v", resolvedPreview, err)
	}
	changed, _, _, err := svc.ChangeTemplate(context.Background(), actor, project.ID, "g1-template-05", TemplateMigrationInput{ExpectedProjectVersion: resolved.ProjectVersion, TemplateID: "template-alt", TemplateVersion: "3"})
	if err != nil {
		t.Fatal(err)
	}
	result := asProject(t, changed)
	if result.TemplateID != "template-alt" || result.TemplateVersion != "3" || result.ProjectVersion != 4 || result.Spec["research_goal"] != "目标" || result.Spec["legacy"] != "保留" {
		t.Fatalf("changed project = %+v", result)
	}
	if _, _, _, err := svc.ChangeTemplate(context.Background(), actor, project.ID, "g1-template-stale", TemplateMigrationInput{ExpectedProjectVersion: 2, TemplateID: "template-demo", TemplateVersion: "1"}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale template change = %v, want %v", err, ErrVersionConflict)
	}
}

func TestDiscardRestoreBlocksWritesAndMarksRevokedProvenance(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "g1-discard-restore.db"))
	actor := Actor{TenantID: 87, UserID: "owner"}
	seedTenantMember(t, svc, actor)
	created, _, _, err := svc.CreateProject(context.Background(), actor, "g1-create-07", CreateProjectInput{Name: "G1 discard", TemplateID: "template-demo"})
	if err != nil {
		t.Fatal(err)
	}
	project := asProject(t, created)
	if _, _, _, err := svc.SaveSpec(context.Background(), actor, project.ID, "g1-spec-07", SaveSpecInput{ExpectedSpecRevision: 0, Fields: map[string]string{"research_subject": "主题"}, FieldMetadata: map[string]SpecFieldInput{"research_subject": {Origin: "ai_generated", Status: "pending_confirmation", Provenance: &ProvenanceRecord{SourceType: "upload", SourceID: "asset-1", CreatedBy: actor.UserID, Accessible: false}}}}); err != nil {
		t.Fatal(err)
	}
	current, err := svc.GetProject(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	field := current.SpecFields["research_subject"]
	if field.Status != "pending_confirmation" || field.Value != "" || current.Spec["research_subject"] != "" || field.Provenance == nil || !field.Provenance.NeedsReview {
		t.Fatalf("revoked provenance = %+v", field)
	}
	if _, _, _, err := svc.DiscardProject(context.Background(), actor, project.ID, "g1-discard-07", current.ProjectVersion); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.SaveSpec(context.Background(), actor, project.ID, "g1-spec-after-discard-07", SaveSpecInput{ExpectedSpecRevision: 1, Fields: map[string]string{"research_subject": "禁止"}}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("save after discard = %v, want %v", err, ErrInvalidState)
	}
	items, _, err := svc.ListProjects(context.Background(), actor)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("discarded project still listed = %+v", items)
	}
	discarded, err := svc.GetProject(context.Background(), actor, project.ID)
	if err != nil || discarded.DiscardedAt == nil {
		t.Fatalf("discarded project read = %+v, err=%v", discarded, err)
	}
	if _, _, _, err := svc.RestoreProject(context.Background(), actor, project.ID, "g1-restore-07", discarded.ProjectVersion); err != nil {
		t.Fatal(err)
	}
	restored, err := svc.GetProject(context.Background(), actor, project.ID)
	if err != nil || restored.DiscardedAt != nil || restored.Status != "draft" {
		t.Fatalf("restored project = %+v, err=%v", restored, err)
	}
}
