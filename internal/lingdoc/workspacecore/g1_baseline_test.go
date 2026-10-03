package workspacecore

import (
	"context"
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
	activated, _, _, err := svc.ActivateProject(context.Background(), actor, projectID, "g1-activate-01", ActivateProjectInput{ExpectedSpecRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	active := asProject(t, activated)
	if active.Status != "active" || active.CurrentContextRevision != 1 {
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
	candidates, err := svc.ListDraftCandidates(context.Background(), actor, projectID)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("candidates = %+v, err=%v", candidates, err)
	}
	if candidates[0].BasedOnContextRevision != 0 || candidates[0].Level != "background" || candidates[0].Provenance == nil {
		t.Fatalf("candidate = %+v", candidates[0])
	}
	audits, err := svc.ListAuditEvents(context.Background(), actor, projectID)
	if err != nil || len(audits) != 2 {
		t.Fatalf("audits = %+v, err=%v", audits, err)
	}
	if audits[0].Action != "project_spec.save" || audits[1].Action != "draft_candidate.create" {
		t.Fatalf("audit actions = %+v", audits)
	}
}
