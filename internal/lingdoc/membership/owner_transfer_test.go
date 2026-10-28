package membership

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

func TestOwnerTransferAcceptsExactlyOnce(t *testing.T) {
	w := newTestWorkspace(t, filepath.Join(t.TempDir(), "g1-owner-transfer.db"))
	owner := core.Actor{TenantID: 84, UserID: "owner"}
	target := core.Actor{TenantID: 84, UserID: "target"}
	seedTenantMember(t, w.db, owner)
	seedTenantMember(t, w.db, target)
	created, _, _, err := w.svc.CreateProject(context.Background(), owner, "g1-create-04", core.CreateProjectInput{
		Name:       "G1 owner",
		TemplateID: "template-demo",
	})
	if err != nil {
		t.Fatal(err)
	}
	project := asProject(t, created)
	raw, _, _, err := w.member.RequestOwnerTransfer(
		context.Background(),
		owner,
		project.ID,
		"g1-transfer-04",
		core.OwnerTransferInput{
			ToUserID:               target.UserID,
			ExpectedProjectVersion: project.ProjectVersion,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	transfer := rawOwnerTransfer(t, raw)
	acceptedRaw, _, _, err := w.member.AcceptOwnerTransfer(
		context.Background(), target, project.ID, transfer.ID, "g1-accept-04",
	)
	if err != nil {
		t.Fatal(err)
	}
	accepted := rawOwnerTransfer(t, acceptedRaw)
	if accepted.Status != "accepted" || accepted.AcceptedAt == nil {
		t.Fatalf("accepted transfer = %+v", accepted)
	}
	if _, _, _, err := w.member.AcceptOwnerTransfer(
		context.Background(), target, project.ID, transfer.ID, "g1-accept-05",
	); !errors.Is(err, core.ErrInvalidState) {
		t.Fatalf("second accept error = %v, want %v", err, core.ErrInvalidState)
	}
	current, err := w.svc.GetProject(context.Background(), target, project.ID)
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

func TestTenantOwnerCanRecoverTransferWhenOwnerIsInactive(t *testing.T) {
	w := newTestWorkspace(t, filepath.Join(t.TempDir(), "g1-owner-recovery.db"))
	owner := core.Actor{TenantID: 88, UserID: "owner"}
	target := core.Actor{TenantID: 88, UserID: "target"}
	admin := core.Actor{TenantID: 88, UserID: "admin", TenantRole: "owner"}
	seedTenantMember(t, w.db, owner)
	seedTenantMember(t, w.db, target)
	seedTenantMember(t, w.db, admin)
	created, _, _, err := w.svc.CreateProject(
		context.Background(),
		owner,
		"g1-create-recovery-08",
		core.CreateProjectInput{
			Name:       "G1 recovery",
			TemplateID: "template-demo",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	project := asProject(t, created)
	if err := w.db.Exec(
		"UPDATE tenant_members SET status = ? WHERE tenant_id = ? AND user_id = ?",
		"suspended", owner.TenantID, owner.UserID,
	).Error; err != nil {
		t.Fatal(err)
	}
	raw, _, _, err := w.member.RequestOwnerTransfer(
		context.Background(),
		admin,
		project.ID,
		"g1-recovery-transfer-08",
		core.OwnerTransferInput{
			ToUserID:               target.UserID,
			ExpectedProjectVersion: project.ProjectVersion,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if rawOwnerTransfer(t, raw).FromUserID != owner.UserID {
		t.Fatalf("recovery transfer = %s", raw)
	}
}

// #127 US-03/US-09/US-11: pending 期所有权不变;同一基线的第二个转让请求
// 恰好一胜一败(串行化后败者是版本冲突);stale 版本被拒。
func TestOwnerTransferSecondRequestLosesAndPendingKeepsOwner(t *testing.T) {
	w := newTestWorkspace(t, filepath.Join(t.TempDir(), "g5-owner-race.db"))
	owner := core.Actor{TenantID: 91, UserID: "owner"}
	target := core.Actor{TenantID: 91, UserID: "target"}
	seedTenantMember(t, w.db, owner)
	seedTenantMember(t, w.db, target)
	created, _, _, err := w.svc.CreateProject(context.Background(), owner, "g5-create-race", core.CreateProjectInput{
		Name:       "G5 race",
		TemplateID: "template-demo",
	})
	if err != nil {
		t.Fatal(err)
	}
	project := asProject(t, created)
	winnerRaw, _, _, err := w.member.RequestOwnerTransfer(
		context.Background(), owner, project.ID, "g5-race-first",
		core.OwnerTransferInput{ToUserID: target.UserID, ExpectedProjectVersion: project.ProjectVersion},
	)
	if err != nil {
		t.Fatal(err)
	}
	winner := rawOwnerTransfer(t, winnerRaw)
	pending, err := w.svc.GetProject(context.Background(), owner, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range pending.Members {
		if member.UserID == owner.UserID && member.Role != "owner" {
			t.Fatalf("pending transfer changed the owner role: %+v", pending.Members)
		}
		if member.UserID == target.UserID && member.Role == "owner" {
			t.Fatalf("pending transfer promoted the recipient: %+v", pending.Members)
		}
	}
	// The project_id unique constraint on pending transfers is the "one
	// winner" rule: the second concurrent request must lose, and the loser
	// must not disturb the winner's pending ticket.
	if _, _, _, err := w.member.RequestOwnerTransfer(
		context.Background(), owner, project.ID, "g5-race-second",
		core.OwnerTransferInput{ToUserID: target.UserID, ExpectedProjectVersion: project.ProjectVersion},
	); err == nil {
		t.Fatal("second request on the same base unexpectedly succeeded")
	}
	if _, _, _, err := w.member.RequestOwnerTransfer(
		context.Background(), owner, project.ID, "g5-race-stale",
		core.OwnerTransferInput{ToUserID: target.UserID, ExpectedProjectVersion: project.ProjectVersion - 1},
	); err == nil {
		t.Fatal("stale request unexpectedly succeeded")
	}
	acceptedRaw, _, _, err := w.member.AcceptOwnerTransfer(
		context.Background(), target, project.ID, winner.ID, "g5-race-accept",
	)
	if err != nil {
		t.Fatal(err)
	}
	if rawOwnerTransfer(t, acceptedRaw).Status != "accepted" {
		t.Fatalf("winner ticket not acceptable after losers: %s", acceptedRaw)
	}
	final, err := w.svc.GetProject(context.Background(), target, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	owners := 0
	for _, member := range final.Members {
		if member.Role == "owner" {
			owners++
		}
	}
	if owners != 1 {
		t.Fatalf("owners = %d, members=%+v", owners, final.Members)
	}
}
