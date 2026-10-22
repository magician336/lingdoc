package membership

import (
	"context"
	"path/filepath"
	"testing"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

func TestMemberPermissionAuditFailureRollsBackBusinessWrite(t *testing.T) {
	w := newTestWorkspace(t, filepath.Join(t.TempDir(), "membership-audit.db"))
	ctx := context.Background()
	owner := core.Actor{TenantID: 1, UserID: "owner"}
	member := core.Actor{TenantID: 1, UserID: "member"}
	seedTenantMember(t, w.db, owner)
	seedTenantMember(t, w.db, member)
	raw, _, _, err := w.svc.CreateProject(ctx, owner, "membership-audit-create", core.CreateProjectInput{
		Name:       "audit",
		TemplateID: "template-demo",
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	project := asProject(t, raw)
	installAuditFailureTrigger(t, w.db, "project.member_permissions.replace")

	_, _, _, err = w.member.SaveMembers(ctx, owner, project.ID, "membership-audit-save", core.SaveMembersInput{
		ExpectedProjectVersion: project.ProjectVersion,
		Members: []core.Member{{
			UserID:         member.UserID,
			GovernanceRole: "member",
			FunctionRoles:  []string{"author"},
		}},
	})
	if err == nil {
		t.Fatal("member assignment succeeded despite audit failure")
	}

	current, err := w.svc.GetProject(ctx, owner, project.ID)
	if err != nil {
		t.Fatalf("read project after rollback: %v", err)
	}
	if current.ProjectVersion != project.ProjectVersion || len(current.Members) != len(project.Members) {
		t.Fatalf("business write leaked after audit failure: before=%+v after=%+v", project, current)
	}
}

func TestOwnerTransferAuditFailureRollsBackBusinessWrite(t *testing.T) {
	w := newTestWorkspace(t, filepath.Join(t.TempDir(), "owner-audit.db"))
	ctx := context.Background()
	owner := core.Actor{TenantID: 1, UserID: "owner"}
	recipient := core.Actor{TenantID: 1, UserID: "recipient"}
	seedTenantMember(t, w.db, owner)
	seedTenantMember(t, w.db, recipient)
	raw, _, _, err := w.svc.CreateProject(ctx, owner, "owner-audit-create", core.CreateProjectInput{
		Name:       "audit",
		TemplateID: "template-demo",
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	project := asProject(t, raw)
	if _, _, _, err := w.member.SaveMembers(ctx, owner, project.ID, "owner-audit-member", core.SaveMembersInput{
		ExpectedProjectVersion: project.ProjectVersion,
		Members: []core.Member{{
			UserID:         recipient.UserID,
			GovernanceRole: "member",
			FunctionRoles:  []string{"author"},
		}},
	}); err != nil {
		t.Fatalf("seed recipient membership: %v", err)
	}
	project, err = w.svc.GetProject(ctx, owner, project.ID)
	if err != nil {
		t.Fatalf("read seeded project: %v", err)
	}
	installAuditFailureTrigger(t, w.db, "project.owner_transfer")

	_, _, _, err = w.member.TransferOwner(ctx, owner, project.ID, "owner-audit-transfer", core.TransferOwnerInput{
		ExpectedProjectVersion: project.ProjectVersion,
		NewOwnerUserID:         recipient.UserID,
	})
	if err == nil {
		t.Fatal("owner transfer succeeded despite audit failure")
	}

	current, err := w.svc.GetProject(ctx, owner, project.ID)
	if err != nil {
		t.Fatalf("read project after owner rollback: %v", err)
	}
	if current.ProjectVersion != project.ProjectVersion {
		t.Fatalf("owner transfer leaked version after audit failure: before=%d after=%d",
			project.ProjectVersion, current.ProjectVersion)
	}
	for _, member := range current.Members {
		if member.UserID == owner.UserID && member.Role != "owner" {
			t.Fatalf("owner role changed after audit failure: %+v", current.Members)
		}
		if member.UserID == recipient.UserID && member.Role == "owner" {
			t.Fatalf("recipient promoted after audit failure: %+v", current.Members)
		}
	}
}
