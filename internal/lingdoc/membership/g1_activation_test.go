package membership

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

func TestG1ActivationRequiresOwnerAndRecordsAtomicAudit(t *testing.T) {
	w := newTestWorkspace(t, filepath.Join(t.TempDir(), "g1-activation-gate.db"))
	owner := core.Actor{TenantID: 86, UserID: "owner"}
	collaborator := core.Actor{TenantID: 86, UserID: "collaborator"}
	seedTenantMember(t, w.db, owner)
	seedTenantMember(t, w.db, collaborator)
	created, _, _, err := w.svc.CreateProject(context.Background(), owner, "g1-create-06", core.CreateProjectInput{
		Name:       "G1 activation",
		TemplateID: "template-demo",
	})
	if err != nil {
		t.Fatal(err)
	}
	project := asProject(t, created)
	if _, _, _, err := w.member.SaveMembers(
		context.Background(),
		owner,
		project.ID,
		"g1-members-06",
		core.SaveMembersInput{
			ExpectedProjectVersion: 1,
			CollaboratorUserIDs:    []string{collaborator.UserID},
		},
	); err != nil {
		t.Fatal(err)
	}
	current, err := w.svc.GetProject(context.Background(), owner, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := w.svc.SaveSpec(
		context.Background(),
		owner,
		project.ID,
		"g1-spec-06",
		core.SaveSpecInput{
			ExpectedSpecRevision: 0,
			Fields: map[string]string{
				"research_subject": "主题",
				"research_goal":    "目标",
			},
		},
	); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := w.svc.ActivateProject(
		context.Background(),
		collaborator,
		project.ID,
		"g1-activate-collaborator-06",
		core.ActivateProjectInput{
			ExpectedSpecRevision:   1,
			ExpectedProjectVersion: current.ProjectVersion + 1,
		},
	); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("collaborator activation error = %v, want %v", err, core.ErrNotFound)
	}
	latest, err := w.svc.GetProject(context.Background(), owner, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if latest.Status != "draft" {
		t.Fatalf("failed activation changed status = %s", latest.Status)
	}
	if _, _, _, err := w.svc.ActivateProject(
		context.Background(),
		owner,
		project.ID,
		"g1-activate-owner-06",
		core.ActivateProjectInput{
			ExpectedSpecRevision:   1,
			ExpectedProjectVersion: latest.ProjectVersion,
			ReviewedProjectVersion: latest.ProjectVersion,
		},
	); err != nil {
		t.Fatal(err)
	}
	audits, err := w.svc.ListAuditEvents(context.Background(), owner, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) == 0 || audits[len(audits)-1].Action != "project.activate" {
		t.Fatalf("activation audit = %+v", audits)
	}
}
