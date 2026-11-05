package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Tencent/WeKnora/internal/types"
	"path/filepath"
	"strings"
	"testing"
)

func firstPassMemberFixture(t *testing.T) (*Service, Actor, Actor, Project) {
	t.Helper()
	svc := testStore(t, filepath.Join(t.TempDir(), "first-pass.db"))
	owner := Actor{TenantID: 8123, UserID: "owner", Role: types.TenantRoleOwner}
	member := Actor{TenantID: 8123, UserID: "member", Role: types.TenantRoleContributor}
	seedTenantMember(t, svc, owner)
	seedTenantMember(t, svc, member)
	raw, _, _, err := svc.CreateProject(context.Background(), owner, "fixture-create", CreateProjectInput{Name: "合成验收", TemplateID: "template-demo"})
	if err != nil {
		t.Fatal(err)
	}
	return svc, owner, member, asProject(t, raw)
}
func firstPassInvite(t *testing.T, svc *Service, owner, member Actor, p Project) (MemberInvitation, Project) {
	t.Helper()
	ctx := context.Background()
	raw, _, _, err := svc.InviteProjectMember(ctx, owner, p.ID, "member-invite", InviteMemberInput{ExpectedProjectVersion: p.ProjectVersion, Reason: "合成邀请", Assignment: Member{UserID: member.UserID, GovernanceRole: "member", FunctionRoles: []string{"author"}, FunctionScopes: map[string][]string{"author": {}}}})
	if err != nil {
		t.Fatal(err)
	}
	var invitation MemberInvitation
	if err := json.Unmarshal(raw, &invitation); err != nil {
		t.Fatal(err)
	}
	current, err := svc.GetProject(ctx, owner, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	return invitation, current
}
func TestFirstPassCurrentTenantRoleRejectsOldTokenAndReplay(t *testing.T) {
	svc, owner, _, p := firstPassMemberFixture(t)
	ctx := context.Background()
	input := SaveSpecInput{ExpectedSpecRevision: 0, Fields: map[string]string{"research_subject": "合成主题"}}
	if _, _, _, err := svc.SaveSpec(ctx, owner, p.ID, "same-spec-key", input); err != nil {
		t.Fatal(err)
	}
	if err := svc.testDB().Exec("UPDATE tenant_members SET role='viewer' WHERE tenant_id=? AND user_id=?", owner.TenantID, owner.UserID).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.SaveSpec(ctx, owner, p.ID, "same-spec-key", input); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale role replay: %v", err)
	}
	current, err := svc.GetProject(ctx, owner, p.ID)
	if err != nil || current.SpecRevision != 1 {
		t.Fatalf("unexpected side effects: %+v %v", current, err)
	}
}
func TestFirstPassInvitationRequiresAcceptanceAndResendVersion(t *testing.T) {
	svc, owner, member, p := firstPassMemberFixture(t)
	ctx := context.Background()
	inv, current := firstPassInvite(t, svc, owner, member, p)
	if _, err := svc.GetProject(ctx, member, p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invited content access: %v", err)
	}
	raw, _, _, err := svc.InviteProjectMember(ctx, owner, p.ID, "resend-member", InviteMemberInput{ExpectedProjectVersion: current.ProjectVersion, InvitationID: inv.ID, ExpectedInvitationVersion: inv.Version, Reason: "重发更新", Assignment: inv.Assignment})
	if err != nil {
		t.Fatal(err)
	}
	var replacement MemberInvitation
	_ = json.Unmarshal(raw, &replacement)
	if _, _, _, err := svc.AcceptProjectInvitation(ctx, member, p.ID, inv.ID, "stale-accept", AcceptMemberInput{ExpectedInvitationVersion: inv.Version}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("old invitation: %v", err)
	}
	if _, _, _, err := svc.AcceptProjectInvitation(ctx, member, p.ID, inv.ID, "valid-accept", AcceptMemberInput{ExpectedInvitationVersion: replacement.Version}); err != nil {
		t.Fatal(err)
	}
	if _, _, replay, err := svc.AcceptProjectInvitation(ctx, member, p.ID, inv.ID, "valid-accept", AcceptMemberInput{ExpectedInvitationVersion: replacement.Version}); err != nil || !replay {
		t.Fatalf("accept replay: %v %v", replay, err)
	}
	if _, err := svc.GetProject(ctx, member, p.ID); err != nil {
		t.Fatal(err)
	}
}
func TestFirstPassRemoveDoesNotRestoreRevokedScopes(t *testing.T) {
	svc, owner, member, p := firstPassMemberFixture(t)
	ctx := context.Background()
	inv, _ := firstPassInvite(t, svc, owner, member, p)
	if _, _, _, err := svc.AcceptProjectInvitation(ctx, member, p.ID, inv.ID, "accept-member", AcceptMemberInput{ExpectedInvitationVersion: inv.Version}); err != nil {
		t.Fatal(err)
	}
	current, _ := svc.GetProject(ctx, owner, p.ID)
	raw, _, _, err := svc.ChangeMemberState(ctx, owner, p.ID, member.UserID, "remove-member", MemberLifecycleInput{ExpectedProjectVersion: current.ProjectVersion, Action: "remove", Reason: "撤销测试成员"})
	if err != nil {
		t.Fatal(err)
	}
	removed := asProject(t, raw)
	if _, err := svc.GetProject(ctx, member, p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("removed can read: %v", err)
	}
	if _, _, _, err := svc.ChangeMemberState(ctx, owner, p.ID, member.UserID, "restore-removed", MemberLifecycleInput{ExpectedProjectVersion: removed.ProjectVersion, Action: "restore", Reason: "不能恢复已移除邀请"}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("removed restored without reinvitation: %v", err)
	}
}
func TestFirstPassTenantRecoveryRemainsPending(t *testing.T) {
	svc, owner, member, p := firstPassMemberFixture(t)
	ctx := context.Background()
	inv, _ := firstPassInvite(t, svc, owner, member, p)
	if _, _, _, err := svc.AcceptProjectInvitation(ctx, member, p.ID, inv.ID, "accept-member", AcceptMemberInput{ExpectedInvitationVersion: inv.Version}); err != nil {
		t.Fatal(err)
	}
	if err := svc.testDB().Exec("UPDATE tenant_members SET status='suspended' WHERE tenant_id=? AND user_id=?", member.TenantID, member.UserID).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.CaptureTenantSuspension(ctx, member.TenantID, member.UserID); err != nil {
		t.Fatal(err)
	}
	if err := svc.testDB().Exec("UPDATE tenant_members SET status='active' WHERE tenant_id=? AND user_id=?", member.TenantID, member.UserID).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetProject(ctx, member, p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("tenant recovery silently restored project: %v", err)
	}
	current, _ := svc.GetProject(ctx, owner, p.ID)
	if _, _, _, err := svc.ChangeMemberState(ctx, owner, p.ID, member.UserID, "restore-tenant", MemberLifecycleInput{ExpectedProjectVersion: current.ProjectVersion, Action: "restore_tenant", Reason: "明确复核恢复"}); err != nil {
		t.Fatal(err)
	}
}
func TestFirstPassAuditVisibilityAndNoResearchText(t *testing.T) {
	svc, owner, member, p := firstPassMemberFixture(t)
	ctx := context.Background()
	inv, _ := firstPassInvite(t, svc, owner, member, p)
	if _, _, _, err := svc.AcceptProjectInvitation(ctx, member, p.ID, inv.ID, "accept-member", AcceptMemberInput{ExpectedInvitationVersion: inv.Version}); err != nil {
		t.Fatal(err)
	}
	sentinel := "PRIVATE_SYNTHETIC_RESEARCH_BODY_73ae"
	if _, _, _, err := svc.SaveSpec(ctx, owner, p.ID, "save-private-test", SaveSpecInput{ExpectedSpecRevision: 0, Fields: map[string]string{"research_subject": sentinel}}); err != nil {
		t.Fatal(err)
	}
	all, err := svc.ListAuditEvents(ctx, owner, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(all)
	if strings.Contains(string(encoded), sentinel) {
		t.Fatalf("research text entered audit: %s", encoded)
	}
	own, err := svc.ListAuditEvents(ctx, member, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range own {
		if event.ActorID != member.UserID && event.UserID != member.UserID {
			t.Fatalf("member saw unrelated audit: %+v", event)
		}
	}
}
func TestFirstPassTenantAdminCannotRecoverUnavailableOwner(t *testing.T) {
	svc, owner, member, p := firstPassMemberFixture(t)
	ctx := context.Background()
	member.Role = types.TenantRoleAdmin
	if err := svc.testDB().Exec("UPDATE tenant_members SET role='admin' WHERE tenant_id=? AND user_id=?", member.TenantID, member.UserID).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.testDB().Exec("UPDATE tenant_members SET status='suspended' WHERE tenant_id=? AND user_id=?", owner.TenantID, owner.UserID).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.RequestOwnerTransfer(ctx, member, p.ID, "illegal-bypass", OwnerTransferInput{ExpectedProjectVersion: p.ProjectVersion, ToUserID: "another"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Admin bypass allowed: %v", err)
	}
}
