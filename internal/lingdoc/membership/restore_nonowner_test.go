package membership

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

// 恢复丢弃的 draft 是 Owner 专属：项目成员恢复被拒（反枚举）且丢弃状态不动，
// Owner 随后恢复成功（#143 TD-03.11 恢复越权半边）。
func TestRestoreProjectRefusesNonOwner(t *testing.T) {
	w := newTestWorkspace(t, filepath.Join(t.TempDir(), "g1-restore-nonowner.db"))
	owner := core.Actor{TenantID: 94, UserID: "owner"}
	collaborator := core.Actor{TenantID: 94, UserID: "collaborator"}
	seedTenantMember(t, w.db, owner)
	seedTenantMember(t, w.db, collaborator)
	created, _, _, err := w.svc.CreateProject(context.Background(), owner, "g1-create-12", core.CreateProjectInput{
		Name:       "G1 restore nonowner",
		TemplateID: "template-demo",
	})
	if err != nil {
		t.Fatal(err)
	}
	projectID := asProject(t, created).ID
	if _, _, _, err := w.member.SaveMembers(
		context.Background(),
		owner,
		projectID,
		"g1-members-12",
		core.SaveMembersInput{
			ExpectedProjectVersion: 1,
			CollaboratorUserIDs:    []string{collaborator.UserID},
		},
	); err != nil {
		t.Fatal(err)
	}
	current, err := w.svc.GetProject(context.Background(), owner, projectID)
	if err != nil {
		t.Fatal(err)
	}
	// 丢弃前：恢复是 Owner 专属动作，成员侧得到统一不可见拒绝。
	if _, _, _, err := w.svc.RestoreProject(context.Background(), collaborator, projectID, "g1-restore-collab-pre-12", current.ProjectVersion); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("non-owner restore before discard = %v, want %v", err, core.ErrNotFound)
	}
	discarded, _, _, err := w.svc.DiscardProject(context.Background(), owner, projectID, "g1-discard-12", current.ProjectVersion)
	if err != nil {
		t.Fatal(err)
	}
	// 丢弃后：丢弃项目对非 Owner 不可达，恢复在授权层即被拒。
	if _, _, _, err := w.svc.RestoreProject(context.Background(), collaborator, projectID, "g1-restore-collab-12", asProject(t, discarded).ProjectVersion); err == nil {
		t.Fatal("non-owner restore after discard must be refused")
	}
	still, err := w.svc.GetProject(context.Background(), owner, projectID)
	if err != nil || still.DiscardedAt == nil {
		t.Fatalf("discarded state moved = %+v, err=%v", still, err)
	}
	if _, _, _, err := w.svc.RestoreProject(context.Background(), owner, projectID, "g1-restore-owner-12", still.ProjectVersion); err != nil {
		t.Fatal(err)
	}
	restored, err := w.svc.GetProject(context.Background(), owner, projectID)
	if err != nil || restored.DiscardedAt != nil || restored.Status != "draft" {
		t.Fatalf("owner restore = %+v, err=%v", restored, err)
	}
}
