package workspace

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Reuse the production SQLite/full-workflow fixture, real AgentTurn parser,
// explicit human confirmation and native formal-change lifecycle. The only model is the
// existing in-process synthetic stub; no external model or HTTP server runs.
func r27StaleAgentDraft(t *testing.T, f deliveryGuardFixture) (core.AgentChangeDraftInput, core.ChangeSet) {
	t.Helper()
	input, _ := r26AgentDraftWithIndependentSource(t, f)
	draft := agentConfirmPreparedDraft(t, f, agentDraftPropose(t, f, input))
	_, _, _, err := f.projects.AssessChangeSet(f.ctx, f.actor, f.project.ID, draft.ID, "r27-original-assess")
	require.NoError(t, err)
	otherRaw, _, _, err := f.projects.CreateChangeDraft(f.ctx, f.actor, f.project.ID, "r27-other-condition", core.ExtendedChangeSetInput{
		ExpectedContextRevision: f.project.CurrentContextRevision,
		Reason:                  "另一项明确人工条件调整推进正式基线",
		Fields:                  map[string]core.ChangeFieldInput{"research_subject": {OldValue: f.project.Spec["research_subject"], NewValue: "R27另一合成研究对象"}},
		AffectedChapterIDs:      []string{f.chapters[0].ID},
	})
	require.NoError(t, err)
	var other core.ChangeSet
	require.NoError(t, json.Unmarshal(otherRaw, &other))
	_, _, _, err = f.projects.AssessChangeSet(f.ctx, f.actor, f.project.ID, other.ID, "r27-other-assess")
	require.NoError(t, err)
	_, _, _, err = f.projects.ApplyChangeSet(f.ctx, f.actor, f.project.ID, other.ID, "r27-other-apply")
	require.NoError(t, err)
	_, status, _, err := f.projects.ApplyChangeSet(f.ctx, f.actor, f.project.ID, draft.ID, "r27-mark-stale")
	require.NoError(t, err)
	require.Equal(t, http.StatusConflict, status)
	stale, err := f.projects.GetChangeSet(f.ctx, f.actor, f.project.ID, draft.ID)
	require.NoError(t, err)
	require.Equal(t, "stale", stale.Status)
	return input, stale
}

// Use the focused production application port and the real bodyless HTTP route.
// An empty body is intentional: even {} is forbidden by the reassess contract.
func r27ReassessHTTP(t *testing.T, f deliveryGuardFixture, changeID, key, body string) (*httptest.ResponseRecorder, r26OperationEnvelope) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	NewHandler(HandlerDependencies{ChangeSets: f.projects}).Register(RouteGroups{Read: engine.Group("/api"), Write: engine.Group("/api")})
	request := httptest.NewRequest(http.MethodPost, "/api/projects/"+f.project.ID+"/change-sets/"+changeID+"/reassess", strings.NewReader(body)).WithContext(f.ctx)
	request.Header.Set("Idempotency-Key", key)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	var envelope r26OperationEnvelope
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope), response.Body.String())
	return response, envelope
}

func r27SuccessfulReassess(t *testing.T, f deliveryGuardFixture, changeID, key, seam string, wantReplay bool) json.RawMessage {
	t.Helper()
	if seam == "http" {
		response, envelope := r27ReassessHTTP(t, f, changeID, key, "")
		require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
		require.Equal(t, wantReplay, envelope.Meta.Replayed)
		return envelope.Data
	}
	body, status, replayed, err := f.projects.ReassessChangeSet(f.ctx, f.actor, f.project.ID, changeID, key)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, status)
	require.Equal(t, wantReplay, replayed)
	return body
}

type r27ReassessMutationSnapshot struct {
	Domain r26OperationMutationSnapshot
	G3     []struct {
		ID, ProjectID, Kind, RecordKey string
		Version                        int64
		BodyJSON                       string
	}
	G5 []struct {
		ProjectID, Kind, ID string
		Version             int64
		PayloadJSON         string
	}
}

func r27SnapshotReassessMutations(t *testing.T, f deliveryGuardFixture) r27ReassessMutationSnapshot {
	t.Helper()
	snapshot := r27ReassessMutationSnapshot{Domain: r26SnapshotOperationMutations(t, f)}
	require.NoError(t, f.db.Table("lingdoc_g3_records").Select("id,project_id,kind,record_key,version,body_json").Where("project_id=?", f.project.ID).Order("kind,id").Find(&snapshot.G3).Error)
	require.NoError(t, f.db.Table("lingdoc_g5_records").Select("project_id,kind,id,version,payload_json").Where("project_id=?", f.project.ID).Order("kind,id").Find(&snapshot.G5).Error)
	return snapshot
}

func r27EditReassessedDraft(t *testing.T, f deliveryGuardFixture, input core.AgentChangeDraftInput, reassessed core.ChangeSet, removeAIField bool) core.ChangeSet {
	t.Helper()
	p, err := f.projects.GetProject(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	edit := r26DraftEdit(input, "；重评之后人工再次编辑的新值")
	edit.ExpectedContextRevision = p.CurrentContextRevision
	if removeAIField {
		edit.Fields = map[string]core.ChangeFieldInput{"research_subject": {OldValue: p.Spec["research_subject"], NewValue: "R27仅保留新的人工候选对象"}}
	}
	raw, _, _, err := f.projects.EditChangeDraft(f.ctx, f.actor, p.ID, reassessed.ID, "r27-later-draft-edit", edit)
	require.NoError(t, err)
	var changed core.ChangeSet
	require.NoError(t, json.Unmarshal(raw, &changed))
	require.Equal(t, "draft", changed.Status)
	require.Equal(t, reassessed.ID, changed.ID)
	require.NotEqual(t, reassessed.Fields, changed.Fields)
	if removeAIField {
		_, pins, _ := agentReadDraftHistory(t, f, changed.ID)
		require.Empty(t, pins, "the current human-only draft must not serve as proof for the old AI receipt")
	}
	return changed
}

func TestR27AgentReassessReplayAfterDraftEditRetainsOriginalReceipt(t *testing.T) {
	for _, seam := range []string{"service", "http"} {
		t.Run(seam, func(t *testing.T) {
			f := newDeliveryGuardFixture(t, false)
			input, stale := r27StaleAgentDraft(t, f)
			key := "r27-reassess-original-receipt"
			original := r27SuccessfulReassess(t, f, stale.ID, key, seam, false)
			var reassessed core.ChangeSet
			require.NoError(t, json.Unmarshal(original, &reassessed))
			require.Equal(t, "assessed", reassessed.Status)
			require.Equal(t, stale.ID, reassessed.ReassessedFromID)
			require.Len(t, reassessed.Fields, 1)
			require.Equal(t, input.Fields["research_goal"].NewValue, reassessed.Fields[0].NewValue)
			changed := r27EditReassessedDraft(t, f, input, reassessed, false)
			f = r26ReopenOperationFixture(t, f)
			before := r27SnapshotReassessMutations(t, f)
			got := r27SuccessfulReassess(t, f, stale.ID, key, seam, true)
			require.Equal(t, string(original), string(got), "a later mutable draft/pin must not sanitize or replace the original receipt")
			require.Equal(t, before, r27SnapshotReassessMutations(t, f), "replay must not create another assessment, operation, pin, history or domain audit")
			current, err := f.projects.GetChangeSet(f.ctx, f.actor, f.project.ID, reassessed.ID)
			require.NoError(t, err)
			require.Equal(t, changed.Fields, current.Fields, "replay must preserve the user's latest draft")
			require.Equal(t, changed.Status, current.Status)
		})
	}
}

func TestR27AgentReassessReplayEnforcesCurrentAccessAndLegacyProof(t *testing.T) {
	for _, scenario := range []string{"source_revoked_then_restored", "actor_revoked", "legacy_missing_proof"} {
		t.Run(scenario, func(t *testing.T) {
			f := newDeliveryGuardFixture(t, false)
			input, stale := r27StaleAgentDraft(t, f)
			key := "r27-reassess-access"
			original := r27SuccessfulReassess(t, f, stale.ID, key, "service", false)
			var reassessed core.ChangeSet
			require.NoError(t, json.Unmarshal(original, &reassessed))
			r27EditReassessedDraft(t, f, input, reassessed, true)
			switch scenario {
			case "source_revoked_then_restored":
				require.NoError(t, f.db.Exec("UPDATE knowledge_bases SET deleted_at=CURRENT_TIMESTAMP WHERE id='kb-runtime'").Error)
			case "actor_revoked":
				require.NoError(t, f.db.Exec("UPDATE tenant_members SET status='suspended' WHERE tenant_id=? AND user_id=?", f.actor.TenantID, f.actor.UserID).Error)
			case "legacy_missing_proof":
				// Model a saved pre-R27 reassess receipt without immutable proof.
				// Current mutable history must never be promoted into old proof.
				require.NoError(t, f.db.Exec("DELETE FROM lingdoc_g5_records WHERE project_id=? AND kind='g1_operation_provenance'", f.project.ID).Error)
			}
			f = r26ReopenOperationFixture(t, f)
			before := r27SnapshotReassessMutations(t, f)
			body, _, replayed, err := f.projects.ReassessChangeSet(f.ctx, f.actor, f.project.ID, stale.ID, key)
			wantError, wantStatus, wantCode := core.ErrSourceUnavailable, http.StatusForbidden, "source_access_denied"
			if scenario == "actor_revoked" {
				wantError, wantStatus, wantCode = core.ErrNotFound, http.StatusNotFound, "not_found"
			}
			require.ErrorIs(t, err, wantError)
			require.Empty(t, body)
			require.False(t, replayed)
			response, envelope := r27ReassessHTTP(t, f, stale.ID, key, "")
			require.Equal(t, wantStatus, response.Code, response.Body.String())
			require.Equal(t, wantCode, envelope.Error.Code)
			require.Empty(t, envelope.Data)
			require.NotContains(t, response.Body.String(), input.Fields["research_goal"].NewValue)
			require.Equal(t, before, r27SnapshotReassessMutations(t, f))
			if scenario == "source_revoked_then_restored" {
				require.NoError(t, f.db.Exec("UPDATE knowledge_bases SET deleted_at=NULL WHERE id='kb-runtime'").Error)
				got := r27SuccessfulReassess(t, f, stale.ID, key, "http", true)
				require.Equal(t, string(original), string(got))
				require.Equal(t, before, r27SnapshotReassessMutations(t, f))
			}
		})
	}
}

func TestR27AgentReassessBodylessAndDifferentKeyContracts(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	input, stale := r27StaleAgentDraft(t, f)
	key := "r27-reassess-contract"
	original := r27SuccessfulReassess(t, f, stale.ID, key, "http", false)
	var reassessed core.ChangeSet
	require.NoError(t, json.Unmarshal(original, &reassessed))
	r27EditReassessedDraft(t, f, input, reassessed, false)
	f = r26ReopenOperationFixture(t, f)
	before := r27SnapshotReassessMutations(t, f)
	response, envelope := r27ReassessHTTP(t, f, stale.ID, key, `{}`)
	require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
	require.Equal(t, "invalid_request", envelope.Error.Code)
	require.Empty(t, envelope.Data)
	require.Equal(t, before, r27SnapshotReassessMutations(t, f), "nonempty payload must be rejected before cache lookup")
	// Reassess is bodyless and targets a stale source. A genuinely different key
	// remains a fresh assessment; it must not return the edited result as a replay.
	fresh := r27SuccessfulReassess(t, f, stale.ID, "r27-reassess-distinct-key", "service", false)
	var distinct core.ChangeSet
	require.NoError(t, json.Unmarshal(fresh, &distinct))
	require.NotEqual(t, reassessed.ID, distinct.ID)
	require.Equal(t, "assessed", distinct.Status)
	require.Equal(t, reassessed.Fields, distinct.Fields)
	afterFresh := r27SnapshotReassessMutations(t, f)
	got := r27SuccessfulReassess(t, f, stale.ID, key, "http", true)
	require.Equal(t, string(original), string(got))
	require.Equal(t, afterFresh, r27SnapshotReassessMutations(t, f))
}

func TestR27AgentReassessProofFailureRollsBackAndSameKeyRecovers(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	_, stale := r27StaleAgentDraft(t, f)
	before := r27SnapshotReassessMutations(t, f)
	require.NoError(t, f.db.Exec(`CREATE TRIGGER r27_reassess_proof_fail BEFORE INSERT ON lingdoc_g5_records WHEN NEW.kind='g1_operation_provenance' BEGIN SELECT RAISE(ABORT,'synthetic reassessment receipt proof failure'); END`).Error)
	key := "r27-reassess-proof-failure"
	body, _, replayed, err := f.projects.ReassessChangeSet(f.ctx, f.actor, f.project.ID, stale.ID, key)
	require.Error(t, err, "a failed native proof write must abort assessment, history, audit and operation together")
	require.Empty(t, body)
	require.False(t, replayed)
	require.Equal(t, before, r27SnapshotReassessMutations(t, f))
	require.NoError(t, f.db.Exec("DROP TRIGGER r27_reassess_proof_fail").Error)
	f = r26ReopenOperationFixture(t, f)
	original := r27SuccessfulReassess(t, f, stale.ID, key, "service", false)
	afterSuccess := r27SnapshotReassessMutations(t, f)
	got := r27SuccessfulReassess(t, f, stale.ID, key, "http", true)
	require.Equal(t, string(original), string(got))
	require.Equal(t, afterSuccess, r27SnapshotReassessMutations(t, f))
}
