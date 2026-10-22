package workspace

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type r26OperationEnvelope struct {
	Data json.RawMessage `json:"data"`
	Meta struct {
		Replayed bool `json:"replayed"`
	} `json:"meta"`
	Error struct {
		Code string `json:"code"`
	} `json:"error"`
}

// Exercise the real registered transport against the production core service.
// Authentication is the existing fixture's current Caller/Principal context.
func r26OperationHTTP(t *testing.T, f deliveryGuardFixture, method, path, key string, input any) (*httptest.ResponseRecorder, r26OperationEnvelope) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	RegisterAgentRoutes(engine.Group("/api"), agentRoutePortsFor(f.projects))
	RegisterImpactRoutes(engine.Group("/api"), impactPortsFor(f.projects))
	body, err := json.Marshal(input)
	require.NoError(t, err)
	request := httptest.NewRequest(method, path, bytes.NewReader(body)).WithContext(f.ctx)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", key)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	var envelope r26OperationEnvelope
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope), response.Body.String())
	return response, envelope
}

// Close the actual disk-backed fixture pool, open the same database file, and
// reconstruct the production repository/source runtime/application service.
// No schema, migration, operation cache or source policy is replaced by a fake.
func r26ReopenOperationFixture(t *testing.T, f deliveryGuardFixture) deliveryGuardFixture {
	t.Helper()
	var databases []struct {
		Name string
		File string
	}
	require.NoError(t, f.db.Raw("PRAGMA database_list").Scan(&databases).Error)
	filename := ""
	for _, database := range databases {
		if database.Name == "main" {
			filename = database.File
		}
	}
	require.NotEmpty(t, filename)
	require.True(t, filepath.IsAbs(filename), "recovery must read a real database file")
	oldPool, err := f.db.DB()
	require.NoError(t, err)
	require.NoError(t, oldPool.Close())
	db, err := gorm.Open(sqlite.Dialector{DriverName: "sqlite", DSN: filename + "?_pragma=busy_timeout(5000)"}, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(1)
	require.NotSame(t, oldPool, pool)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	f.db = db
	f.projects = agentReopenDraftService(t, f)
	return f
}

type r26OperationMutationSnapshot struct {
	Project struct {
		ProjectVersion         int64
		SpecRevision           int64
		CurrentContextRevision int64
		SpecJSON               string
	}
	Operations, Audits, Changes, Objects, Pins, ChapterVersions int64
}

func r26SnapshotOperationMutations(t *testing.T, f deliveryGuardFixture) r26OperationMutationSnapshot {
	t.Helper()
	var snapshot r26OperationMutationSnapshot
	require.NoError(t, f.db.Table("lingdoc_projects").Select("project_version,spec_revision,current_context_revision,spec_json").Where("id=?", f.project.ID).Take(&snapshot.Project).Error)
	for _, count := range []struct {
		Table string
		Value *int64
	}{
		{"lingdoc_operations", &snapshot.Operations},
		{"lingdoc_project_audits", &snapshot.Audits},
		{"lingdoc_change_sets", &snapshot.Changes},
		{"lingdoc_g2_objects", &snapshot.Objects},
		{"lingdoc_chapter_versions", &snapshot.ChapterVersions},
	} {
		require.NoError(t, f.db.Table(count.Table).Count(count.Value).Error)
	}
	require.NoError(t, f.db.Table("lingdoc_g5_records").Where("kind='g1_provenance'").Count(&snapshot.Pins).Error)
	return snapshot
}

// The selected source is used ONLY by the AgentTurn. Neither the formal Spec
// nor either current chapter cites it; a later denial cannot be masked by a
// second, unrelated current-baseline source check.
func r26AgentDraftWithIndependentSource(t *testing.T, f deliveryGuardFixture) (core.AgentChangeDraftInput, string) {
	t.Helper()
	_, source := adapterSourceFixture(t, f.governance, f.db, f.project, f.actor)
	turn, input, _ := agentDraftModelTurn(t, f, source.ID)
	found := false
	for _, ref := range turn.Context.SourceRefs {
		found = found || ref.SourceID == source.ID
	}
	require.True(t, found, "the real AgentTurn must retain the explicitly selected source")
	p, err := f.projects.GetProject(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	for _, field := range p.SpecFields {
		require.Nil(t, field.Provenance, "this regression needs a human-only formal baseline")
	}
	chapters, err := f.projects.ListChapters(f.ctx, f.actor, p.ID)
	require.NoError(t, err)
	for _, chapter := range chapters {
		require.Empty(t, chapter.SourceIDs)
	}
	return input, source.ID
}

func r26DraftEdit(input core.AgentChangeDraftInput, suffix string) core.ExtendedChangeSetInput {
	value := input.Fields["research_goal"]
	value.NewValue += suffix
	return core.ExtendedChangeSetInput{
		ExpectedContextRevision: input.ExpectedContextRevision,
		Fields:                  map[string]core.ChangeFieldInput{"research_goal": value},
		AffectedChapterIDs:      append([]string{}, input.AffectedChapterIDs...),
		Reason:                  "合成操作回执：" + suffix,
	}
}

func TestR26AgentDraftOldEditReplayAfterRemovalRechecksOriginalSource(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for _, seam := range []string{"service", "http"} {
			name := seam + "/new_operation"
			if legacy {
				name = seam + "/legacy_operation_without_immutable_proof"
			}
			t.Run(name, func(t *testing.T) {
				f := newDeliveryGuardFixture(t, false)
				input, _ := r26AgentDraftWithIndependentSource(t, f)
				draft := agentConfirmPreparedDraft(t, f, agentDraftPropose(t, f, input))
				first := r26DraftEdit(input, "；旧回执的 AI 派生文本")
				key := "r26-original-ai-edit"
				original, _, replayed, err := f.projects.EditChangeDraft(f.ctx, f.actor, f.project.ID, draft.ID, key, first)
				require.NoError(t, err)
				require.False(t, replayed)
				require.Contains(t, string(original), first.Fields["research_goal"].NewValue)
				// A legitimate later edit removes the AI field entirely, leaving a
				// separate manual change. It must not erase the old op's proof.
				manual := core.ExtendedChangeSetInput{ExpectedContextRevision: input.ExpectedContextRevision, Fields: map[string]core.ChangeFieldInput{
					"research_subject": {OldValue: f.project.Spec["research_subject"], NewValue: "后续独立人工研究对象"},
				}, AffectedChapterIDs: append([]string{}, input.AffectedChapterIDs...), Reason: "放弃旧 AI 新值，只保留人工字段"}
				_, _, _, err = f.projects.EditChangeDraft(f.ctx, f.actor, f.project.ID, draft.ID, "r26-remove-all-ai-fields", manual)
				require.NoError(t, err)
				history, pins, _ := agentReadDraftHistory(t, f, draft.ID)
				require.Empty(t, pins)
				if legacy {
					// Candidate storage contract: existing G5 sidecar, no new
					// migration. c4 has no such records, so this is a no-op there.
					// Removing it after a fixed build models a c4-era saved op.
					require.NoError(t, f.db.Exec("DELETE FROM lingdoc_g5_records WHERE project_id=? AND kind='g1_operation_provenance'", f.project.ID).Error)
				}
				require.NoError(t, f.db.Exec("UPDATE knowledge_bases SET deleted_at=CURRENT_TIMESTAMP WHERE id='kb-runtime'").Error)
				f = r26ReopenOperationFixture(t, f)
				current, err := f.projects.GetProject(f.ctx, f.actor, f.project.ID)
				require.NoError(t, err, "current formal human fields remain accessible")
				require.Equal(t, f.project.Spec, current.Spec)
				before := r26SnapshotOperationMutations(t, f)
				if seam == "service" {
					body, _, replayed, err := f.projects.EditChangeDraft(f.ctx, f.actor, f.project.ID, draft.ID, key, first)
					require.ErrorIs(t, err, core.ErrSourceUnavailable, "old AI receipt must retain its own revoked source proof")
					require.Empty(t, body)
					require.False(t, replayed)
				} else {
					response, envelope := r26OperationHTTP(t, f, http.MethodPut, "/api/projects/"+f.project.ID+"/change-sets/"+draft.ID+"/draft", key, first)
					require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
					require.Equal(t, "source_access_denied", envelope.Error.Code)
					require.Empty(t, envelope.Data)
					require.NotContains(t, response.Body.String(), first.Fields["research_goal"].NewValue)
				}
				require.Equal(t, before, r26SnapshotOperationMutations(t, f))
				afterHistory, _, _ := agentReadDraftHistory(t, f, draft.ID)
				require.Equal(t, history, afterHistory)
			})
		}
	}
}

func TestR26AgentDraftOldEditReplayAfterLaterValueEditSurvivesReopen(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	input, _ := r26AgentDraftWithIndependentSource(t, f)
	draft := agentConfirmPreparedDraft(t, f, agentDraftPropose(t, f, input))
	first := r26DraftEdit(input, "；第一次人工补充")
	original, originalStatus, _, err := f.projects.EditChangeDraft(f.ctx, f.actor, f.project.ID, draft.ID, "r26-first-value-edit", first)
	require.NoError(t, err)
	second := r26DraftEdit(input, "；第二次不同补充")
	_, _, _, err = f.projects.EditChangeDraft(f.ctx, f.actor, f.project.ID, draft.ID, "r26-second-value-edit", second)
	require.NoError(t, err)
	f = r26ReopenOperationFixture(t, f)
	before := r26SnapshotOperationMutations(t, f)
	response, envelope := r26OperationHTTP(t, f, http.MethodPut, "/api/projects/"+f.project.ID+"/change-sets/"+draft.ID+"/draft", "r26-first-value-edit", first)
	require.Equal(t, originalStatus, response.Code, response.Body.String())
	require.True(t, envelope.Meta.Replayed)
	require.JSONEq(t, string(original), string(envelope.Data), "a later value/pin cannot replace the original receipt's provenance")
	require.Equal(t, before, r26SnapshotOperationMutations(t, f))
	changes, err := f.projects.ListChangeSets(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	require.Len(t, changes, 1)
	require.Equal(t, second.Fields["research_goal"].NewValue, changes[0].Fields[0].NewValue, "replay must not restore the old draft value")
}

func r26InstallOtherChapterWriter(t *testing.T, f deliveryGuardFixture) core.Actor {
	t.Helper()
	writer := core.Actor{TenantID: f.actor.TenantID, UserID: "r26-other-author", Role: types.TenantRoleContributor}
	require.NoError(t, f.db.Exec("INSERT INTO users(id,username,is_active) VALUES(?,?,1)", writer.UserID, writer.UserID).Error)
	require.NoError(t, f.db.Exec("INSERT INTO tenant_members(tenant_id,user_id,role,status) VALUES(?,?,'contributor','active')", writer.TenantID, writer.UserID).Error)
	require.NoError(t, f.db.Exec("INSERT INTO lingdoc_members(project_id,user_id,role) VALUES(?,?,'collaborator')", f.project.ID, writer.UserID).Error)
	scopes, err := json.Marshal(map[string][]string{"author": {f.chapters[0].ID}})
	require.NoError(t, err)
	require.NoError(t, f.db.Exec(`INSERT INTO lingdoc_member_permissions(project_id,user_id,governance_role,function_roles_json,function_scopes_json,status) VALUES(?,?,'member','["author"]',?,'active')`, f.project.ID, writer.UserID, string(scopes)).Error)
	return writer
}

func TestR26AgentProposalCreationRetryAfterOtherAuthorSaveUsesStoredReceipt(t *testing.T) {
	for _, scenario := range []string{"same_request", "new_key_stale", "same_key_different_payload", "source_revoked_then_restored", "actor_revoked"} {
		t.Run(scenario, func(t *testing.T) {
			f := newDeliveryGuardFixture(t, false)
			writer := r26InstallOtherChapterWriter(t, f)
			input, sourceID := r26AgentDraftWithIndependentSource(t, f)
			require.Equal(t, "ai_assisted_human", input.FieldMetadata["research_goal"].Origin)
			p, err := f.projects.GetProject(f.ctx, f.actor, f.project.ID)
			require.NoError(t, err)
			payload, err := json.Marshal(input)
			require.NoError(t, err)
			request := core.CreateActionProposalInput{VersionExpectation: agentDraftExpectation(p, 0), Action: "create_change_set", TargetID: p.ID, Input: payload, SourceIDs: []string{}, ExpectedEffect: "只准备候选变更草稿"}
			path, key := "/api/projects/"+p.ID+"/action-proposals", "r26-create-proposal-retry"
			first, original := r26OperationHTTP(t, f, http.MethodPost, path, key, request)
			require.Equal(t, http.StatusCreated, first.Code, first.Body.String())
			require.False(t, original.Meta.Replayed)
			var registered core.ActionProposal
			require.NoError(t, json.Unmarshal(original.Data, &registered))
			var canonical core.AgentChangeDraftInput
			require.NoError(t, json.Unmarshal(registered.Input, &canonical))
			require.Equal(t, "ai_generated", canonical.FieldMetadata["research_goal"].Origin, "first write corrected the original client origin")
			require.Contains(t, registered.SourceIDs, sourceID, "server canonicalized the source set before hashing")
			chapter := f.chapters[0]
			writerContext := f.requestContext(writer.UserID, writer.Role, "r26-other-writer-save")
			_, _, _, err = f.projects.SaveChapter(writerContext, writer, p.ID, chapter.ID, "r26-other-writer-chapter", core.SaveChapterInput{ExpectedChapterVersionID: chapter.CurrentVersionID, ExpectedSpecRevision: p.SpecRevision, BodyMarkdown: "另一位已授权作者只改普通措辞", SourceIDs: []string{}})
			require.NoError(t, err, "the unrelated edit must pass real current author/scope authorization")
			advanced, err := f.projects.GetProject(f.ctx, f.actor, p.ID)
			require.NoError(t, err)
			require.Greater(t, advanced.ProjectVersion, p.ProjectVersion)
			require.Equal(t, p.SpecRevision, advanced.SpecRevision)
			require.Equal(t, p.CurrentContextRevision, advanced.CurrentContextRevision)
			f = r26ReopenOperationFixture(t, f)
			before := r26SnapshotOperationMutations(t, f)
			switch scenario {
			case "new_key_stale":
				key = "r26-new-key-stale-request"
			case "same_key_different_payload":
				request.ExpectedEffect += "（不同请求）"
			case "source_revoked_then_restored":
				require.NoError(t, f.db.Exec("UPDATE knowledge_bases SET deleted_at=CURRENT_TIMESTAMP WHERE id='kb-runtime'").Error)
			case "actor_revoked":
				require.NoError(t, f.db.Exec("UPDATE tenant_members SET status='suspended' WHERE tenant_id=? AND user_id=?", f.actor.TenantID, f.actor.UserID).Error)
			}
			response, replay := r26OperationHTTP(t, f, http.MethodPost, path, key, request)
			switch scenario {
			case "same_request":
				require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
				require.True(t, replay.Meta.Replayed)
				require.JSONEq(t, string(original.Data), string(replay.Data))
			case "new_key_stale":
				require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
				require.Equal(t, "version_conflict", replay.Error.Code)
			case "same_key_different_payload":
				require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
				require.Equal(t, "idempotency_conflict", replay.Error.Code)
			case "actor_revoked":
				require.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
				require.Empty(t, replay.Data)
			case "source_revoked_then_restored":
				require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
				require.Equal(t, "source_access_denied", replay.Error.Code)
				require.Empty(t, replay.Data)
				require.NoError(t, f.db.Exec("UPDATE knowledge_bases SET deleted_at=NULL WHERE id='kb-runtime'").Error)
				restored, receipt := r26OperationHTTP(t, f, http.MethodPost, path, key, request)
				require.Equal(t, http.StatusCreated, restored.Code, restored.Body.String())
				require.True(t, receipt.Meta.Replayed)
				require.JSONEq(t, string(original.Data), string(receipt.Data), "denial must preserve the original operation for later authorized recovery")
			}
			require.Equal(t, before, r26SnapshotOperationMutations(t, f), "retry/denial must not add a proposal, draft, pin, formal version, operation, or domain audit")
			require.Zero(t, agentDraftCount(t, f), "proposal creation remains preparation; it does not create or apply a ChangeSet")
		})
	}
}
