package membership

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

func TestFixedMembersAndRevokedReplay(t *testing.T) {
	w := newTestWorkspace(t, filepath.Join(t.TempDir(), "members.db"))
	ctx := context.Background()
	owner := core.Actor{TenantID: 12, UserID: "owner"}
	member := core.Actor{TenantID: 12, UserID: "member"}
	seedTenantMember(t, w.db, owner)
	seedTenantMember(t, w.db, member)
	if err := w.db.Exec(
		"INSERT INTO tenant_members (tenant_id, user_id, status) VALUES (12, 'inactive', 'inactive')",
	).Error; err != nil {
		t.Fatal(err)
	}
	raw, _, _, err := w.svc.CreateProject(ctx, owner, "create-members", core.CreateProjectInput{
		Name:       "协作",
		TemplateID: "template-demo",
	})
	if err != nil {
		t.Fatal(err)
	}
	id := asProject(t, raw).ID
	if _, _, _, err := w.member.SaveMembers(
		ctx, owner, id, "members-bad",
		core.SaveMembersInput{ExpectedProjectVersion: 1, CollaboratorUserIDs: []string{"inactive"}},
	); !errors.Is(err, core.ErrInvalidState) {
		t.Fatalf("inactive member admitted: %v", err)
	}
	input := core.SaveMembersInput{ExpectedProjectVersion: 1, CollaboratorUserIDs: []string{"member"}}
	raw, _, _, err = w.member.SaveMembers(ctx, owner, id, "members-add", input)
	if err != nil || len(asProject(t, raw).Members) != 2 {
		t.Fatalf("add collaborator: %v", err)
	}
	if _, err := w.svc.GetProject(ctx, member, id); err != nil {
		t.Fatalf("member cannot read: %v", err)
	}
	if _, err := w.svc.GetTemplateCopy(ctx, member, id, 1); err != nil {
		t.Fatalf("member cannot read the bound template copy: %v", err)
	}
	if _, _, _, err := w.member.SaveMembers(
		ctx, member, id, "members-own",
		core.SaveMembersInput{ExpectedProjectVersion: 2, CollaboratorUserIDs: []string{}},
	); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("collaborator managed members: %v", err)
	}
	// Project-wide conditions require a real management assignment; an
	// unassigned Author cannot acquire it from the legacy write fallback.
	if _, _, _, err := w.member.SetMemberAssignment(
		ctx, owner, id, member.UserID, "member-admin",
		core.MemberAssignmentInput{
			ExpectedProjectVersion: 2,
			Assignment: core.Member{
				UserID:         member.UserID,
				GovernanceRole: "admin",
			},
			Reason: "合成管理授权用于验证撤权后的幂等重放",
		},
	); err != nil {
		t.Fatal(err)
	}
	_, _, _, err = w.svc.SaveSpec(ctx, member, id, "member-spec-key", core.SaveSpecInput{
		ExpectedSpecRevision: 0,
		Fields:               map[string]string{"research_subject": "共同编辑"},
	})
	if err != nil {
		t.Fatal(err)
	}
	project, err := w.svc.GetProject(ctx, owner, id)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = w.member.SaveMembers(
		ctx, owner, id, "members-remove",
		core.SaveMembersInput{
			ExpectedProjectVersion: project.ProjectVersion,
			CollaboratorUserIDs:    []string{},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.GetProject(ctx, member, id); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("revoked member read: %v", err)
	}
	if _, err := w.svc.GetTemplateCopy(ctx, member, id, 1); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("revoked member read template copy: %v", err)
	}
	// The old operation exists; current authorization must still run first.
	if _, _, _, err := w.svc.SaveSpec(ctx, member, id, "member-spec-key", core.SaveSpecInput{
		ExpectedSpecRevision: 0,
		Fields:               map[string]string{"research_subject": "共同编辑"},
	}); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("revoked replay exposed content: %v", err)
	}
	if err := w.db.Exec(
		"UPDATE tenant_members SET status = 'inactive' WHERE tenant_id = 12 AND user_id = 'owner'",
	).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.GetProject(ctx, owner, id); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("inactive tenant member read project: %v", err)
	}
	if _, _, _, err := w.svc.CreateProject(
		ctx, owner, "create-after-revoke",
		core.CreateProjectInput{
			Name:       "不应创建",
			TemplateID: "template-demo",
		},
	); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("inactive tenant member created project: %v", err)
	}
}
