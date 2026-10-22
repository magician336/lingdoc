package membership

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

// These are service regressions over testStore's production-migration SQLite
// rows. No AutoMigrate, fabricated inverse row, or helper-only mirror is used.
//
// The fixture is a copy, not a move: the aggregate's own inverse tests still
// use theirs.
func r25InverseFieldFixture(t *testing.T) (*testWorkspace, core.Actor, core.Project, core.ChangeSet, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "inverse.db")
	w := newTestWorkspace(t, path)
	a := core.Actor{TenantID: 925, UserID: "owner"}
	p := seedActiveImpactProject(t, w.svc, w.db, a)
	chapters, err := w.svc.ListChapters(context.Background(), a, p.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("chapters: %v", err)
	}
	raw, _, _, err := w.svc.CreateChangeSet(
		context.Background(), a, p.ID, "r25-original-create",
		core.CreateChangeSetInput{
			ExpectedContextRevision: p.CurrentContextRevision, Reason: "new research goal",
			Fields: map[string]core.ChangeFieldInput{
				"research_goal": {OldValue: p.Spec["research_goal"], NewValue: "changed goal"},
			},
			AffectedChapterIDs: []string{chapters[0].ID},
		},
	)
	source := decodeAgentJSON[core.ChangeSet](t, raw, err)
	raw, status, replayed, err := w.svc.ApplyChangeSet(context.Background(), a, p.ID, source.ID, "r25-original-apply")
	if err != nil || status != 200 || replayed {
		t.Fatalf("apply original: status=%d replay=%v err=%v", status, replayed, err)
	}
	source = decodeAgentJSON[core.ChangeSet](t, raw, err)
	p, err = w.svc.GetProject(context.Background(), a, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	return w, a, p, source, path
}

func r25InverseOperationRecord(
	t *testing.T,
	w *testWorkspace,
	a core.Actor,
	pid, sourceID, key string,
) core.OperationResult {
	t.Helper()
	var result core.OperationResult
	repository := core.NewGORMRepository(w.db)
	err := repository.Transaction(
		context.Background(),
		core.TransactionOptions{ReadOnly: true},
		func(tx core.Transaction) error {
			var found bool
			var err error
			result, found, err = tx.Operation(core.OperationIdentity{
				Actor: a, Operation: "reverseChangeSet", Target: pid + "/" + sourceID, Key: key,
			})
			if err == nil && !found {
				t.Fatal("missing durable reverse operation")
			}
			return err
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestR25ReverseReplayRejectsFormerOwnerWithoutAlteringOperation(t *testing.T) {
	w, a, p, source, _ := r25InverseFieldFixture(t)
	ctx := context.Background()
	key := "r25-owner-replay"
	if _, _, _, err := w.svc.ReverseChangeSet(ctx, a, p.ID, source.ID, key); err != nil {
		t.Fatal(err)
	}
	original := r25InverseOperationRecord(t, w, a, p.ID, source.ID, key)
	target := core.Actor{TenantID: a.TenantID, UserID: "new-owner"}
	seedTenantMember(t, w.db, target)
	raw, _, _, err := w.member.RequestOwnerTransfer(
		ctx,
		a,
		p.ID,
		"r25-owner-transfer",
		core.OwnerTransferInput{
			ToUserID:               target.UserID,
			ExpectedProjectVersion: p.ProjectVersion,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	transfer := rawOwnerTransfer(t, raw)
	if _, _, _, err := w.member.AcceptOwnerTransfer(ctx, target, p.ID, transfer.ID, "r25-owner-accept"); err != nil {
		t.Fatal(err)
	}
	raw, status, replayed, err := w.svc.ReverseChangeSet(ctx, a, p.ID, source.ID, key)
	if !errors.Is(err, core.ErrNotFound) || raw != nil || status != 0 || replayed {
		t.Fatalf("former Owner replayed inverse: status=%d replay=%v body=%s err=%v", status, replayed, raw, err)
	}
	if got := r25InverseOperationRecord(t, w, a, p.ID, source.ID, key); !reflect.DeepEqual(original, got) {
		t.Fatal("Owner revocation altered durable operation")
	}
}
