package membership

// The owner transfer cannot be used to walk around the freeze.
//
// The transfer is an Owner-only write like the five decisions in the
// workspace core, so it carries the same freeze: while the project's Owner
// may not act, the Owner cannot hand the project over either. That is what
// the Spec asks for -- only the current *available* Owner may initiate a
// normal transfer -- and the recovery of a stranded project belongs to the
// tenant Owner, not to the Owner whose identity lapsed.
//
// Before the freeze gate was added here, the ordinary authorization answered
// a lapsed Owner with a plain "not found": the same answer it gives for a
// project that is not there. The caller could not act on it, because "not
// found" says nothing about whether the Owner may come back.
//
// The assertions are on the service, not on the HTTP seam, and the difference
// matters. The transport refuses a request whose tenant membership is gone
// before it reaches this package, so a caller in that shape never sees the
// refusal this file asserts -- it sees the transport's own answer. The shape
// this file covers is the one the transport lets through: the Owner whose
// project membership has lapsed while the tenant identity is untouched.

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

// suspendOwnerProjectMembership takes the Owner's project membership away
// while the tenant identity stays untouched.
//
// The Owner can still reach the project; the Owner-only actions are frozen.
func suspendOwnerProjectMembership(t *testing.T, w *testWorkspace, projectID, userID string) {
	t.Helper()
	if err := w.db.Exec(
		"UPDATE lingdoc_member_permissions SET status = 'suspended' WHERE project_id = ? AND user_id = ?",
		projectID, userID,
	).Error; err != nil {
		t.Fatal(err)
	}
}

// TestOwnerTransferRefusesTheUnavailableOwnerAndNamesTheFreeze is the
// attribution itself: the Owner whose project membership lapsed cannot
// transfer the project away, and the refusal says that the Owner is gone
// rather than that the project is not there.
//
// Both shapes of the transfer are covered, because they are two doors to the
// same decision.
func TestOwnerTransferRefusesTheUnavailableOwnerAndNamesTheFreeze(t *testing.T) {
	w := newTestWorkspace(t, filepath.Join(t.TempDir(), "pf9-owner-transfer-freeze.db"))
	owner := core.Actor{TenantID: 91, UserID: "owner"}
	target := core.Actor{TenantID: 91, UserID: "target"}
	seedTenantMember(t, w.db, owner)
	seedTenantMember(t, w.db, target)
	created, _, _, err := w.svc.CreateProject(context.Background(), owner, "pf9-create-01", core.CreateProjectInput{
		Name:       "PF9 owner transfer freeze",
		TemplateID: "template-demo",
	})
	if err != nil {
		t.Fatal(err)
	}
	project := asProject(t, created)

	suspendOwnerProjectMembership(t, w, project.ID, owner.UserID)

	// The direct transfer.
	_, _, _, err = w.member.TransferOwner(context.Background(), owner, project.ID, "pf9-transfer-01",
		core.TransferOwnerInput{ExpectedProjectVersion: project.ProjectVersion, NewOwnerUserID: target.UserID})
	if !errors.Is(err, core.ErrOwnerUnavailable) {
		t.Fatalf("transfer error = %v, want %v", err, core.ErrOwnerUnavailable)
	}

	// The requested transfer.
	_, _, _, err = w.member.RequestOwnerTransfer(context.Background(), owner, project.ID, "pf9-request-01",
		core.OwnerTransferInput{ToUserID: target.UserID, ExpectedProjectVersion: project.ProjectVersion})
	if !errors.Is(err, core.ErrOwnerUnavailable) {
		t.Fatalf("transfer request error = %v, want %v", err, core.ErrOwnerUnavailable)
	}
}

// TestOwnerTransferKeepsTheTenantOwnerRecovery is the other side of the same
// gate: the recovery of a stranded project must not be blocked by it.
//
// The tenant Owner is not the project's Owner, so the freeze is not their
// reason for being refused, and the request goes through.
func TestOwnerTransferKeepsTheTenantOwnerRecovery(t *testing.T) {
	w := newTestWorkspace(t, filepath.Join(t.TempDir(), "pf9-owner-transfer-recovery.db"))
	owner := core.Actor{TenantID: 92, UserID: "owner"}
	target := core.Actor{TenantID: 92, UserID: "target"}
	tenantOwner := core.Actor{TenantID: 92, UserID: "tenant-owner", TenantRole: "owner"}
	seedTenantMember(t, w.db, owner)
	seedTenantMember(t, w.db, target)
	seedTenantMember(t, w.db, tenantOwner)
	created, _, _, err := w.svc.CreateProject(context.Background(), owner, "pf9-create-02", core.CreateProjectInput{
		Name:       "PF9 owner transfer recovery",
		TemplateID: "template-demo",
	})
	if err != nil {
		t.Fatal(err)
	}
	project := asProject(t, created)

	suspendOwnerProjectMembership(t, w, project.ID, owner.UserID)

	raw, _, _, err := w.member.RequestOwnerTransfer(context.Background(), tenantOwner, project.ID, "pf9-recovery-02",
		core.OwnerTransferInput{ToUserID: target.UserID, ExpectedProjectVersion: project.ProjectVersion})
	if err != nil {
		t.Fatalf("the tenant Owner must still be able to recover the project: %v", err)
	}
	if transfer := rawOwnerTransfer(t, raw); transfer.FromUserID != owner.UserID {
		t.Fatalf("recovery transfer = %s, want it to name the unavailable Owner", raw)
	}
}

// TestOwnerTransferRefusalDoesNotNameTheFreezeForANonOwner keeps the two
// refusals apart.
//
// A member who is not the Owner is refused because the member is not the
// Owner. That refusal names no freeze: the freeze is not their reason for
// being refused, and saying it is would swallow the meaning they need.
func TestOwnerTransferRefusalDoesNotNameTheFreezeForANonOwner(t *testing.T) {
	w := newTestWorkspace(t, filepath.Join(t.TempDir(), "pf9-owner-transfer-non-owner.db"))
	owner := core.Actor{TenantID: 93, UserID: "owner"}
	target := core.Actor{TenantID: 93, UserID: "target"}
	seedTenantMember(t, w.db, owner)
	seedTenantMember(t, w.db, target)
	created, _, _, err := w.svc.CreateProject(context.Background(), owner, "pf9-create-03", core.CreateProjectInput{
		Name:       "PF9 owner transfer non owner",
		TemplateID: "template-demo",
	})
	if err != nil {
		t.Fatal(err)
	}
	project := asProject(t, created)

	suspendOwnerProjectMembership(t, w, project.ID, owner.UserID)

	_, _, _, err = w.member.RequestOwnerTransfer(context.Background(), target, project.ID, "pf9-non-owner-03",
		core.OwnerTransferInput{ToUserID: owner.UserID, ExpectedProjectVersion: project.ProjectVersion})
	if err == nil {
		t.Fatal("a member who is not the Owner must not be able to request the transfer")
	}
	if errors.Is(err, core.ErrOwnerUnavailable) {
		t.Fatalf("the non-Owner refusal names the freeze (%v), want the ordinary refusal", err)
	}
}
