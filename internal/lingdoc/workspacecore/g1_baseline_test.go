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
