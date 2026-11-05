package workspace

import (
	"encoding/json"
	"testing"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/stretchr/testify/require"
)

func TestR26AgentDraftOperationProofFailureRollsBackAndSameKeyRecovers(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	input, _ := r26AgentDraftWithIndependentSource(t, f)
	draft := agentConfirmPreparedDraft(t, f, agentDraftPropose(t, f, input))
	history, _, _ := agentReadDraftHistory(t, f, draft.ID)
	before := r26SnapshotOperationMutations(t, f)
	var proofs int64
	require.NoError(t, f.db.Table("lingdoc_g5_records").Where("kind='g1_operation_provenance'").Count(&proofs).Error)
	// Inject failure in the production native sidecar write, after the edit
	// callback has written its field, pin/history and domain audit changes.
	require.NoError(t, f.db.Exec(`CREATE TRIGGER r26_operation_proof_fail BEFORE INSERT ON lingdoc_g5_records WHEN NEW.kind='g1_operation_provenance' BEGIN SELECT RAISE(ABORT,'synthetic immutable receipt proof failure'); END`).Error)
	edit := r26DraftEdit(input, "；仅成功事务才能保存的新值")
	key := "r26-proof-write-failure"
	body, _, replayed, err := f.projects.EditChangeDraft(f.ctx, f.actor, f.project.ID, draft.ID, key, edit)
	require.Error(t, err)
	require.Empty(t, body)
	require.False(t, replayed)
	require.Equal(t, before, r26SnapshotOperationMutations(t, f))
	afterHistory, _, _ := agentReadDraftHistory(t, f, draft.ID)
	require.Equal(t, history, afterHistory)
	var afterProofs int64
	require.NoError(t, f.db.Table("lingdoc_g5_records").Where("kind='g1_operation_provenance'").Count(&afterProofs).Error)
	require.Equal(t, proofs, afterProofs)
	changes, err := f.projects.ListChangeSets(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	require.Len(t, changes, 1)
	require.Equal(t, draft.Fields, changes[0].Fields)
	require.Equal(t, draft.Reason, changes[0].Reason)
	// Recovery does not force an operation ledger or reconstruct a schema.
	// The same native DB is closed/reopened before retrying the original key.
	require.NoError(t, f.db.Exec("DROP TRIGGER r26_operation_proof_fail").Error)
	f = r26ReopenOperationFixture(t, f)
	body, status, replayed, err := f.projects.EditChangeDraft(f.ctx, f.actor, f.project.ID, draft.ID, key, edit)
	require.NoError(t, err)
	require.False(t, replayed)
	var changed core.ChangeSet
	require.NoError(t, json.Unmarshal(body, &changed))
	require.Equal(t, edit.Fields["research_goal"].NewValue, changed.Fields[0].NewValue)
	afterSuccess := r26SnapshotOperationMutations(t, f)
	again, replayStatus, replayed, err := f.projects.EditChangeDraft(f.ctx, f.actor, f.project.ID, draft.ID, key, edit)
	require.NoError(t, err)
	require.True(t, replayed)
	require.Equal(t, status, replayStatus)
	require.JSONEq(t, string(body), string(again))
	require.Equal(t, afterSuccess, r26SnapshotOperationMutations(t, f))
}
