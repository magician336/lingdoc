package workspacecore

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func installAuditFailureTrigger(t *testing.T, svc *Service, action string) {
	t.Helper()
	trigger := "fail_lingdoc_audit_" + strings.ReplaceAll(action, ".", "_")
	statement := "CREATE TRIGGER " + trigger + " BEFORE INSERT ON lingdoc_project_audits WHEN NEW.action = '" + action + "' BEGIN SELECT RAISE(ABORT, 'audit unavailable'); END"
	if err := svc.testDB().Exec(statement).Error; err != nil {
		t.Fatalf("install audit trigger: %v", err)
	}
}

func TestMemberPermissionAuditFailureRollsBackBusinessWrite(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "membership-audit.db"))
	ctx := context.Background()
	owner := Actor{TenantID: 1, UserID: "owner"}
	member := Actor{TenantID: 1, UserID: "member"}
	seedTenantMember(t, svc, owner)
	seedTenantMember(t, svc, member)
	raw, _, _, err := svc.CreateProject(ctx, owner, "membership-audit-create", CreateProjectInput{Name: "audit", TemplateID: "template-demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	project := asProject(t, raw)
	installAuditFailureTrigger(t, svc, "project.member_permissions.replace")

	_, _, _, err = svc.SaveMembers(ctx, owner, project.ID, "membership-audit-save", SaveMembersInput{
		ExpectedProjectVersion: project.ProjectVersion,
		Members:                []Member{{UserID: member.UserID, GovernanceRole: "member", FunctionRoles: []string{"author"}}},
	})
	if err == nil {
		t.Fatal("member assignment succeeded despite audit failure")
	}

	current, err := svc.GetProject(ctx, owner, project.ID)
	if err != nil {
		t.Fatalf("read project after rollback: %v", err)
	}
	if current.ProjectVersion != project.ProjectVersion || len(current.Members) != len(project.Members) {
		t.Fatalf("business write leaked after audit failure: before=%+v after=%+v", project, current)
	}
}

func TestOwnerTransferAuditFailureRollsBackBusinessWrite(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "owner-audit.db"))
	ctx := context.Background()
	owner := Actor{TenantID: 1, UserID: "owner"}
	recipient := Actor{TenantID: 1, UserID: "recipient"}
	seedTenantMember(t, svc, owner)
	seedTenantMember(t, svc, recipient)
	raw, _, _, err := svc.CreateProject(ctx, owner, "owner-audit-create", CreateProjectInput{Name: "audit", TemplateID: "template-demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	project := asProject(t, raw)
	if _, _, _, err := svc.SaveMembers(ctx, owner, project.ID, "owner-audit-member", SaveMembersInput{
		ExpectedProjectVersion: project.ProjectVersion,
		Members:                []Member{{UserID: recipient.UserID, GovernanceRole: "member", FunctionRoles: []string{"author"}}},
	}); err != nil {
		t.Fatalf("seed recipient membership: %v", err)
	}
	project, err = svc.GetProject(ctx, owner, project.ID)
	if err != nil {
		t.Fatalf("read seeded project: %v", err)
	}
	installAuditFailureTrigger(t, svc, "project.owner_transfer")

	_, _, _, err = svc.TransferOwner(ctx, owner, project.ID, "owner-audit-transfer", TransferOwnerInput{
		ExpectedProjectVersion: project.ProjectVersion,
		NewOwnerUserID:         recipient.UserID,
	})
	if err == nil {
		t.Fatal("owner transfer succeeded despite audit failure")
	}

	current, err := svc.GetProject(ctx, owner, project.ID)
	if err != nil {
		t.Fatalf("read project after owner rollback: %v", err)
	}
	if current.ProjectVersion != project.ProjectVersion {
		t.Fatalf("owner transfer leaked version after audit failure: before=%d after=%d", project.ProjectVersion, current.ProjectVersion)
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
