package membership

import (
	"context"
	"path/filepath"
	"testing"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

// TestStandardMutationResultFamiliesPreserveTypedValueAndReplayOwnerTransfer
// is the owner-transfer family of the aggregate's typed mutation-result table.
//
// The family moved with the member and owner context: RequestOwnerTransferResult
// now belongs to it, and the aggregate's internal test package cannot import
// that package (it would close a cycle). The assertions are the ones the
// aggregate's table has always carried.
func TestStandardMutationResultFamiliesPreserveTypedValueAndReplayOwnerTransfer(t *testing.T) {
	ctx := context.Background()
	w := newTestWorkspace(t, filepath.Join(t.TempDir(), "owner-transfer.db"))
	actor := core.Actor{TenantID: 402, UserID: "owner"}
	project := seedActiveImpactProject(t, w.svc, w.db, actor)
	recipient := core.Actor{TenantID: actor.TenantID, UserID: "recipient"}
	seedTenantMember(t, w.db, recipient)
	input := core.OwnerTransferInput{ExpectedProjectVersion: project.ProjectVersion, ToUserID: recipient.UserID}
	first, err := w.member.RequestOwnerTransferResult(ctx, actor, project.ID, "typed-owner-transfer", input)
	if err != nil {
		t.Fatalf("typed owner transfer: %v", err)
	}
	if first.Replayed || first.Transfer.ProjectID != project.ID || first.Transfer.ToUserID != recipient.UserID ||
		first.Transfer.Status != "pending" {
		t.Fatalf("typed owner transfer result = %+v, want pending transfer", first)
	}
	replayed, err := w.member.RequestOwnerTransferResult(ctx, actor, project.ID, "typed-owner-transfer", input)
	if err != nil {
		t.Fatalf("typed owner transfer replay: %v", err)
	}
	if !replayed.Replayed || replayed.Transfer.ID != first.Transfer.ID ||
		replayed.Transfer.Status != first.Transfer.Status {
		t.Fatalf("typed owner transfer replay = %+v, want same value with replay=true", replayed)
	}
}
