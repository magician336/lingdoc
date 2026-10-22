package workspacecore

import (
	"context"
	"errors"
	"github.com/Tencent/WeKnora/internal/types"
	"path/filepath"
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
