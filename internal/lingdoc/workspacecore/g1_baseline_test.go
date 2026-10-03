package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

func TestG1ActiveProjectRejectsOrdinarySpecSave(t *testing.T) {
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

func TestG1DraftSpecCarriesProvenanceAndCandidatePool(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "g1-provenance.db"))
	actor := Actor{TenantID: 82, UserID: "owner"}
	seedTenantMember(t, svc, actor)
	created, _, _, err := svc.CreateProject(context.Background(), actor, "g1-create-02", CreateProjectInput{Name: "G1 provenance", TemplateID: "template-demo"})
	if err != nil {
		t.Fatal(err)
	}
	projectID := asProject(t, created).ID
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
	for _, candidate := range candidates {
		if candidate.Title == "受限线索" && (candidate.Content != "" || candidate.Provenance == nil || !candidate.Provenance.NeedsReview) {
			t.Fatalf("revoked candidate = %+v", candidate)
		}
	}
	audits, err := svc.ListAuditEvents(context.Background(), actor, projectID)
	if err != nil || len(audits) != 3 {
		t.Fatalf("audits = %+v, err=%v", audits, err)
	}
	if audits[0].Action != "project_spec.save" || audits[1].Action != "draft_candidate.create" {
		t.Fatalf("audit actions = %+v", audits)
	}
}

func TestG1ActivationDiffBindsConfirmationToProjectVersion(t *testing.T) {
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

func TestG1OwnerTransferAcceptsExactlyOnce(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "g1-owner-transfer.db"))
	owner := Actor{TenantID: 84, UserID: "owner"}
	target := Actor{TenantID: 84, UserID: "target"}
	seedTenantMember(t, svc, owner)
	seedTenantMember(t, svc, target)
	created, _, _, err := svc.CreateProject(context.Background(), owner, "g1-create-04", CreateProjectInput{Name: "G1 owner", TemplateID: "template-demo"})
	if err != nil {
		t.Fatal(err)
	}
	project := asProject(t, created)
	raw, _, _, err := svc.RequestOwnerTransfer(context.Background(), owner, project.ID, "g1-transfer-04", OwnerTransferInput{ToUserID: target.UserID, ExpectedProjectVersion: project.ProjectVersion})
	if err != nil {
		t.Fatal(err)
	}
	transfer := rawOwnerTransfer(t, raw)
	acceptedRaw, _, _, err := svc.AcceptOwnerTransfer(context.Background(), target, project.ID, transfer.ID, "g1-accept-04")
	if err != nil {
		t.Fatal(err)
	}
	accepted := rawOwnerTransfer(t, acceptedRaw)
	if accepted.Status != "accepted" || accepted.AcceptedAt == nil {
		t.Fatalf("accepted transfer = %+v", accepted)
	}
	if _, _, _, err := svc.AcceptOwnerTransfer(context.Background(), target, project.ID, transfer.ID, "g1-accept-05"); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("second accept error = %v, want %v", err, ErrInvalidState)
	}
	current, err := svc.GetProject(context.Background(), target, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	owners := 0
	for _, member := range current.Members {
		if member.Role == "owner" {
			owners++
		}
	}
	if owners != 1 {
		t.Fatalf("owners = %d, members=%+v", owners, current.Members)
	}
}

func TestG1AdminCanRecoverTransferWhenOwnerIsInactive(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "g1-owner-recovery.db"))
	owner := Actor{TenantID: 88, UserID: "owner"}
	target := Actor{TenantID: 88, UserID: "target"}
	admin := Actor{TenantID: 88, UserID: "admin", TenantRole: "admin"}
	seedTenantMember(t, svc, owner)
	seedTenantMember(t, svc, target)
	seedTenantMember(t, svc, admin)
	created, _, _, err := svc.CreateProject(context.Background(), owner, "g1-create-recovery-08", CreateProjectInput{Name: "G1 recovery", TemplateID: "template-demo"})
	if err != nil {
		t.Fatal(err)
	}
	project := asProject(t, created)
	if err := svc.repository.(*GORMRepository).db.Exec("UPDATE tenant_members SET status = ? WHERE tenant_id = ? AND user_id = ?", "suspended", owner.TenantID, owner.UserID).Error; err != nil {
		t.Fatal(err)
	}
	raw, _, _, err := svc.RequestOwnerTransfer(context.Background(), admin, project.ID, "g1-recovery-transfer-08", OwnerTransferInput{ToUserID: target.UserID, ExpectedProjectVersion: project.ProjectVersion})
	if err != nil {
		t.Fatal(err)
	}
	if rawOwnerTransfer(t, raw).FromUserID != owner.UserID {
		t.Fatalf("recovery transfer = %s", raw)
	}
}

type g1TemplateReader struct{}

func (g1TemplateReader) Get(id, version string) (Template, error) {
	if id == "template-alt" && (version == "" || version == "2") {
		return Template{ID: "template-alt", Version: "2", RequiredFields: []string{"research_subject", "research_method"}, Sections: []Section{{ID: "method", Title: "方法", Required: true}}}, nil
	}
	return ContractDemoTemplate{}.Get(id, version)
}

func TestG1TemplateMigrationPreviewsAndCommitsWithoutDroppingOrphans(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "g1-template-migration.db"))
	svc.templates = g1TemplateReader{}
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
	changed, _, _, err := svc.ChangeTemplate(context.Background(), actor, project.ID, "g1-template-05", TemplateMigrationInput{ExpectedProjectVersion: 2, TemplateID: "template-alt", TemplateVersion: "2"})
	if err != nil {
		t.Fatal(err)
	}
	result := asProject(t, changed)
	if result.TemplateID != "template-alt" || result.ProjectVersion != 3 || result.Spec["legacy"] != "保留" {
		t.Fatalf("changed project = %+v", result)
	}
	if _, _, _, err := svc.ChangeTemplate(context.Background(), actor, project.ID, "g1-template-stale", TemplateMigrationInput{ExpectedProjectVersion: 2, TemplateID: "template-demo", TemplateVersion: "1"}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale template change = %v, want %v", err, ErrVersionConflict)
	}
}

func TestG1ActivationRequiresOwnerAndRecordsAtomicAudit(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "g1-activation-gate.db"))
	owner := Actor{TenantID: 86, UserID: "owner"}
	collaborator := Actor{TenantID: 86, UserID: "collaborator"}
	seedTenantMember(t, svc, owner)
	seedTenantMember(t, svc, collaborator)
	created, _, _, err := svc.CreateProject(context.Background(), owner, "g1-create-06", CreateProjectInput{Name: "G1 activation", TemplateID: "template-demo"})
	if err != nil {
		t.Fatal(err)
	}
	project := asProject(t, created)
	if _, _, _, err := svc.SaveMembers(context.Background(), owner, project.ID, "g1-members-06", SaveMembersInput{ExpectedProjectVersion: 1, CollaboratorUserIDs: []string{collaborator.UserID}}); err != nil {
		t.Fatal(err)
	}
	current, err := svc.GetProject(context.Background(), owner, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.SaveSpec(context.Background(), collaborator, project.ID, "g1-spec-06", SaveSpecInput{ExpectedSpecRevision: 0, Fields: map[string]string{"research_subject": "主题", "research_goal": "目标"}}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.ActivateProject(context.Background(), collaborator, project.ID, "g1-activate-collaborator-06", ActivateProjectInput{ExpectedSpecRevision: 1, ExpectedProjectVersion: current.ProjectVersion + 1}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("collaborator activation error = %v, want %v", err, ErrNotFound)
	}
	latest, err := svc.GetProject(context.Background(), owner, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if latest.Status != "draft" {
		t.Fatalf("failed activation changed status = %s", latest.Status)
	}
	if _, _, _, err := svc.ActivateProject(context.Background(), owner, project.ID, "g1-activate-owner-06", ActivateProjectInput{ExpectedSpecRevision: 1, ExpectedProjectVersion: latest.ProjectVersion, ReviewedProjectVersion: latest.ProjectVersion}); err != nil {
		t.Fatal(err)
	}
	audits, err := svc.ListAuditEvents(context.Background(), owner, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) == 0 || audits[len(audits)-1].Action != "project.activate" {
		t.Fatalf("activation audit = %+v", audits)
	}
}

func TestG1DiscardRestoreBlocksWritesAndMarksRevokedProvenance(t *testing.T) {
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

func rawOwnerTransfer(t *testing.T, raw []byte) OwnerTransfer {
	t.Helper()
	var transfer OwnerTransfer
	if err := json.Unmarshal(raw, &transfer); err != nil {
		t.Fatal(err)
	}
	return transfer
}
