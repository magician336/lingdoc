package workspace

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

const agentDraftSyntheticValue = "仅使用合成访谈数据比较软件流程的可追踪性"

func agentDraftExpectation(p core.Project, version int64) core.VersionExpectation {
	return core.VersionExpectation{ExpectedProjectVersion: p.ProjectVersion, ExpectedContextRevision: p.CurrentContextRevision, ExpectedVersion: version}
}

// The provider is an in-process existing chat stub. The real host parser,
// SendAgentTurn, stored suggestions, proposal confirmation and workspace service run.
// This helper models the existing editable UI adapter, not a browser E2E.
func agentDraftModelTurn(t *testing.T, f deliveryGuardFixture, selectedSourceIDs ...string) (core.AgentTurn, core.AgentChangeDraftInput, *acceptanceChatStub) {
	t.Helper()
	p, err := f.projects.GetProject(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	response, err := json.Marshal(map[string]any{"reply": "这是研究条件候选；确定影响需由现有评估核对，可能影响仍待确认。", "change_draft": core.AgentChangeDraftSuggestion{
		ExpectedContextRevision: p.CurrentContextRevision, Reason: "明确请求调整 research_goal", Fields: map[string]string{"research_goal": agentDraftSyntheticValue}, AffectedChapterIDs: []string{},
	}, "affected_explanation": map[string]any{
		"definite_affected": []any{map[string]any{
			"object": map[string]any{"kind": "ChapterVersion", "id": f.chapters[0].ID, "version": "v1"},
			"reason": "该章正文引用了被修改的研究条件",
			"basis":  map[string]any{"source_type": "dependency_edge", "source_id": "edge-1"},
		}},
		"possible_affected": []any{map[string]any{
			"object": map[string]any{"kind": "EvidenceCard", "id": "card-1", "version": "2"},
			"reason": "证据卡适用范围可能覆盖新条件",
			"basis":  map[string]any{"source_type": "ruleset", "source_id": "ruleset-1"},
		}},
	}})
	require.NoError(t, err)
	chat := &acceptanceChatStub{response: &types.ChatResponse{Content: string(response), FinishReason: "stop", Usage: types.TokenUsage{PromptTokens: 100, CompletionTokens: 30}}}
	registered := acceptanceDeepSeekModel()
	registered.Parameters.Provider = "fake"
	host := &G2HostAgentModel{models: &acceptanceModelServiceStub{models: []*types.Model{registered}, chatModel: chat}}
	f.projects.ConfigureAgentRuntime(core.AgentRuntime{
		Sources: NewG2ResearchAdapter(f.db, f.projects, f.runtime, g4AdapterShares{}, nil, nil),
		Model:   host, Actions: NewG2ActionExecutor(f.projects, f.runtime, nil),
	})
	raw, _, _, err := f.projects.CreateAgentSession(f.ctx, f.actor, p.ID, "task_assistant", "agent-draft-session", agentDraftExpectation(p, 0))
	require.NoError(t, err)
	var session core.AgentSession
	require.NoError(t, json.Unmarshal(raw, &session))
	raw, _, _, err = f.projects.SendAgentTurn(f.ctx, f.actor, p.ID, session.ID, "agent-draft-turn", core.AgentTurnInput{
		AgentContextInput: core.AgentContextInput{VersionExpectation: agentDraftExpectation(p, session.Version), SourceIDs: append([]string{}, selectedSourceIDs...), CardRefs: []core.VersionRef{}, SensitiveContextConfirmed: len(selectedSourceIDs) > 0},
		UserInput:         "请将 research_goal 改为：" + agentDraftSyntheticValue + "。只提出草稿。",
	})
	require.NoError(t, err)
	var turn core.AgentTurn
	require.NoError(t, json.Unmarshal(raw, &turn))
	require.Equal(t, "completed", turn.Status)
	require.Equal(t, "real_api_fake_model", turn.RunMode)
	var suggestions map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(turn.Suggestions, &suggestions))
	suggestion, err := core.DecodeAgentChangeDraftSuggestion(suggestions["change_draft"], p.CurrentContextRevision)
	require.NoError(t, err)
	require.Empty(t, suggestion.AffectedChapterIDs, "the model did not select a review chapter")
	require.NotEmpty(t, f.chapters)
	// Simulate the user's explicit checkbox selection in the editable UI.
	// Empty model suggestions stay empty; the human chooses a real chapter.
	selectedChapterID := f.chapters[0].ID
	input := core.AgentChangeDraftInput{
		ExtendedChangeSetInput: core.ExtendedChangeSetInput{ExpectedContextRevision: p.CurrentContextRevision, Reason: suggestion.Reason, Fields: map[string]core.ChangeFieldInput{}, AffectedChapterIDs: []string{selectedChapterID}},
		FieldMetadata:          map[string]core.SpecFieldInput{},
	}
	for key, value := range suggestion.Fields {
		old, exists := p.Spec[key]
		require.True(t, exists)
		input.Fields[key] = core.ChangeFieldInput{OldValue: old, NewValue: value}
		input.FieldMetadata[key] = core.SpecFieldInput{Origin: "ai_assisted_human", Status: "pending_confirmation", Provenance: &core.ProvenanceRecord{
			SourceType: "agent_turn", SourceID: turn.ID, SourceVersion: strconv.FormatInt(turn.Version, 10), Purpose: "project_spec", Locator: "reply",
		}}
	}
	// Existing outbound context is metadata and explicitly selected sources.
	// Supplying a candidate does not add the project's Spec/old-value map.
	var packed map[string]json.RawMessage
	require.GreaterOrEqual(t, len(chat.messages), 3)
	require.NoError(t, json.Unmarshal([]byte(chat.messages[1].Content), &packed))
	require.Len(t, packed, 1)
	require.Contains(t, packed, "context")
	var sentContext map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(packed["context"], &sentContext))
	require.NotContains(t, sentContext, "spec")
	require.NotContains(t, sentContext, "spec_fields")
	return turn, input, chat
}

func agentDraftPropose(t *testing.T, f deliveryGuardFixture, input core.AgentChangeDraftInput) core.ActionProposal {
	t.Helper()
	p, err := f.projects.GetProject(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	payload, err := json.Marshal(input)
	require.NoError(t, err)
	raw, _, _, err := f.projects.CreateActionProposal(f.ctx, f.actor, p.ID, "agent-draft-proposal", core.CreateActionProposalInput{
		VersionExpectation: agentDraftExpectation(p, 0), Action: "create_change_set", TargetID: p.ID, Input: payload, SourceIDs: []string{}, ExpectedEffect: "仅创建待评估变更草稿，正式版本不变",
	})
	require.NoError(t, err)
	var proposal core.ActionProposal
	require.NoError(t, json.Unmarshal(raw, &proposal))
	require.Equal(t, "pending", proposal.Status)
	return proposal
}

func agentDraftCount(t *testing.T, f deliveryGuardFixture) int64 {
	t.Helper()
	var count int64
	require.NoError(t, f.db.Table("lingdoc_change_sets").Where("project_id=?", f.project.ID).Count(&count).Error)
	return count
}

func TestAgentChangeDraftFakeModelRequiresHumanThenPreservesAIThroughOwnerApply(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	// Ordinary wording still uses ordinary SaveChapter and creates no ChangeSet.
	chapter := f.chapters[0]
	before := f.project
	_, _, _, err := f.projects.SaveChapter(f.ctx, f.actor, before.ID, chapter.ID, "agent-draft-ordinary-save", core.SaveChapterInput{
		ExpectedChapterVersionID: chapter.CurrentVersionID, ExpectedSpecRevision: before.SpecRevision, BodyMarkdown: "普通措辞更新，不变更研究条件。", SourceIDs: []string{},
	})
	require.NoError(t, err)
	p, err := f.projects.GetProject(f.ctx, f.actor, before.ID)
	require.NoError(t, err)
	require.Equal(t, before.CurrentContextRevision, p.CurrentContextRevision)
	require.Zero(t, agentDraftCount(t, f))
	turn, input, _ := agentDraftModelTurn(t, f)
	require.Zero(t, agentDraftCount(t, f), "model output alone must never create a draft")
	proposal := agentDraftPropose(t, f, input)
	require.Zero(t, agentDraftCount(t, f), "pending proposal is not business confirmation")
	decision := core.ConfirmationInput{VersionExpectation: agentDraftExpectation(p, proposal.Version), Reason: "本人复核后仅确认建草稿"}
	_, _, _, err = f.projects.ConfirmActionProposal(f.ctx, f.actor, p.ID, proposal.ID, "agent-draft-unconfirmed", decision)
	require.ErrorIs(t, err, core.ErrConsentRequired)
	require.Zero(t, agentDraftCount(t, f))
	decision.Confirmed = true
	raw, _, _, err := f.projects.ConfirmActionProposal(f.ctx, f.actor, p.ID, proposal.ID, "agent-draft-confirmed", decision)
	require.NoError(t, err)
	var completed core.ActionProposal
	require.NoError(t, json.Unmarshal(raw, &completed))
	require.Equal(t, "completed", completed.Status)
	// Replaying confirmation must not treat the AgentTurn ID as a chunk ID.
	replayed, _, replay, err := f.projects.ConfirmActionProposal(f.ctx, f.actor, p.ID, proposal.ID, "agent-draft-confirmed", decision)
	require.NoError(t, err)
	require.True(t, replay)
	var replayedProposal core.ActionProposal
	require.NoError(t, json.Unmarshal(replayed, &replayedProposal))
	require.Equal(t, completed.ID, replayedProposal.ID)
	var draft core.ChangeSet
	require.NoError(t, json.Unmarshal(completed.Result, &draft))
	require.Equal(t, "draft", draft.Status)
	require.EqualValues(t, 1, agentDraftCount(t, f))
	unchanged, err := f.projects.GetProject(f.ctx, f.actor, p.ID)
	require.NoError(t, err)
	require.Equal(t, p.Spec, unchanged.Spec)
	require.Equal(t, p.SpecRevision, unchanged.SpecRevision)
	require.Equal(t, p.CurrentContextRevision, unchanged.CurrentContextRevision)
	require.Equal(t, p.ProjectVersion, unchanged.ProjectVersion)
	// Public readback exercises stored history and new-value authorization.
	readback, err := f.projects.ListChangeSets(f.ctx, f.actor, p.ID)
	require.NoError(t, err)
	require.Len(t, readback, 1)
	require.Equal(t, draft.ID, readback[0].ID)
	require.Equal(t, agentDraftSyntheticValue, readback[0].Fields[0].NewValue)
	var history struct{ PayloadJSON string }
	require.NoError(t, f.db.Table("lingdoc_g5_records").Where("project_id=? AND kind=? AND id=?", p.ID, "g1_change_provenance", draft.ID).Take(&history).Error)
	var pins struct {
		ProposedFields map[string]core.SpecProvenanceRef `json:"proposed_fields"`
	}
	require.NoError(t, json.Unmarshal([]byte(history.PayloadJSON), &pins))
	require.NotEmpty(t, pins.ProposedFields["research_goal"].PinID)
	// This is a new explicit Owner action; executor never calls Assess/Apply.
	_, _, _, err = f.projects.AssessChangeSet(f.ctx, f.actor, p.ID, draft.ID, "agent-draft-owner-assess")
	require.NoError(t, err)
	_, _, _, err = f.projects.ApplyChangeSet(f.ctx, f.actor, p.ID, draft.ID, "agent-draft-owner-apply")
	require.NoError(t, err)
	applied, err := f.projects.GetProject(f.ctx, f.actor, p.ID)
	require.NoError(t, err)
	require.Equal(t, agentDraftSyntheticValue, applied.Spec["research_goal"])
	require.Equal(t, p.SpecRevision+1, applied.SpecRevision)
	require.Equal(t, p.CurrentContextRevision+1, applied.CurrentContextRevision)
	field := applied.SpecFields["research_goal"]
	require.Equal(t, "ai_generated", field.Origin, "human confirmation must not wash model text into human provenance")
	require.Equal(t, "pending_confirmation", field.Status)
	require.NotNil(t, field.Provenance)
	require.Equal(t, "agent_turn", field.Provenance.SourceType)
	require.Equal(t, turn.ID, field.Provenance.SourceID)
	require.Equal(t, strconv.FormatInt(turn.Version, 10), field.Provenance.SourceVersion)
	require.True(t, field.Provenance.Accessible)
	require.False(t, field.Provenance.NeedsReview)
	// Completed proposal history remains readable after formal revision advances.
	proposals, err := f.projects.ListActionProposals(f.ctx, f.actor, p.ID)
	require.NoError(t, err)
	require.Len(t, proposals, 1)
	require.Equal(t, "completed", proposals[0].Status)
	require.NotEmpty(t, proposals[0].Result)
}

func TestAgentChangeDraftStrictPayloadTargetAndSelectedTurn(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	_, input, _ := agentDraftModelTurn(t, f)
	raw, err := json.Marshal(input)
	require.NoError(t, err)
	_, err = core.DecodeAgentChangeDraftInput(f.project.ID, "other-project", f.project.CurrentContextRevision, raw)
	require.Error(t, err)
	for _, extra := range []string{`,"confirmed":true}`, `,"human_confirmed":true}`, `,"unknown":1}`} {
		forged := append(append([]byte{}, raw[:len(raw)-1]...), []byte(extra)...)
		_, err = core.DecodeAgentChangeDraftInput(f.project.ID, f.project.ID, f.project.CurrentContextRevision, forged)
		require.Error(t, err)
	}
	_, err = core.DecodeAgentChangeDraftInput(f.project.ID, f.project.ID, f.project.CurrentContextRevision, append(append([]byte{}, raw...), []byte(" {}")...))
	require.Error(t, err)
	_, err = core.DecodeAgentChangeDraftInput(f.project.ID, f.project.ID, f.project.CurrentContextRevision+1, raw)
	require.ErrorIs(t, err, core.ErrVersionConflict)
	input.FieldMetadata["research_goal"].Provenance.SourceVersion = "99999"
	payload, err := json.Marshal(input)
	require.NoError(t, err)
	_, _, _, err = f.projects.CreateActionProposal(f.ctx, f.actor, f.project.ID, "agent-draft-wrong-turn-version", core.CreateActionProposalInput{
		VersionExpectation: agentDraftExpectation(f.project, 0), Action: "create_change_set", TargetID: f.project.ID, Input: payload, ExpectedEffect: "不应创建",
	})
	require.Error(t, err)
	require.Zero(t, agentDraftCount(t, f))
}

func TestAgentChangeDraftCurrentHumanAndSourceGuards(t *testing.T) {
	for _, testCase := range []string{"stale_project", "other_actor", "machine", "revoked_source"} {
		t.Run(testCase, func(t *testing.T) {
			f := newDeliveryGuardFixture(t, false, testCase == "revoked_source")
			_, input, _ := agentDraftModelTurn(t, f)
			proposal := agentDraftPropose(t, f, input)
			p, err := f.projects.GetProject(f.ctx, f.actor, f.project.ID)
			require.NoError(t, err)
			ctx, actor := f.ctx, f.actor
			switch testCase {
			case "stale_project":
				chapter := f.chapters[0]
				_, _, _, err = f.projects.SaveChapter(ctx, actor, p.ID, chapter.ID, "agent-draft-stale-write", core.SaveChapterInput{ExpectedChapterVersionID: chapter.CurrentVersionID, ExpectedSpecRevision: p.SpecRevision, BodyMarkdown: "新措辞使旧提案版本过期", SourceIDs: []string{}})
				require.NoError(t, err)
			case "other_actor":
				actor.UserID = "unrelated-user"
				ctx = f.requestContext(actor.UserID, types.TenantRoleContributor, "agent-draft-other-actor")
			case "machine":
				ctx = types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalAPITenant, ID: "machine"})
			case "revoked_source":
				require.NoError(t, f.db.Exec("UPDATE knowledge_bases SET deleted_at=CURRENT_TIMESTAMP WHERE id='kb-runtime'").Error)
			}
			_, _, _, err = f.projects.ConfirmActionProposal(ctx, actor, p.ID, proposal.ID, "agent-draft-denied-"+testCase, core.ConfirmationInput{VersionExpectation: agentDraftExpectation(p, proposal.Version), Confirmed: true, Reason: "不能越过当前边界"})
			require.Error(t, err)
			require.Zero(t, agentDraftCount(t, f))
		})
	}
}

func TestAgentChangeDraftNativePinFailureRollsBackDraftAndFreshPins(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	_, input, _ := agentDraftModelTurn(t, f)
	proposal := agentDraftPropose(t, f, input)
	var before int64
	require.NoError(t, f.db.Table("lingdoc_g5_records").Where("project_id=? AND kind='g1_provenance'", f.project.ID).Count(&before).Error)
	// Fail the same production sidecar update after InsertChangeSet and fresh
	// pin storage. No alternate schema or repository implementation is created.
	require.NoError(t, f.db.Exec(`CREATE TRIGGER agent_draft_pin_fail BEFORE UPDATE ON lingdoc_g5_records WHEN NEW.kind='g1_change_provenance' BEGIN SELECT RAISE(ABORT,'synthetic proposed pin write failure'); END`).Error)
	raw, _, _, err := f.projects.ConfirmActionProposal(f.ctx, f.actor, f.project.ID, proposal.ID, "agent-draft-rollback", core.ConfirmationInput{VersionExpectation: agentDraftExpectation(f.project, proposal.Version), Confirmed: true, Reason: "合成回滚验证"})
	// Existing G2 protocol returns the persisted failed proposal, rather than
	// changing its public contract to surface executor errors as HTTP errors.
	require.NoError(t, err)
	var failed core.ActionProposal
	require.NoError(t, json.Unmarshal(raw, &failed))
	require.Equal(t, "failed", failed.Status)
	require.NotEmpty(t, failed.FailureCode)
	require.Empty(t, failed.Result)
	require.Zero(t, agentDraftCount(t, f))
	var after int64
	require.NoError(t, f.db.Table("lingdoc_g5_records").Where("project_id=? AND kind='g1_provenance'", f.project.ID).Count(&after).Error)
	require.Equal(t, before, after)
	p, err := f.projects.GetProject(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	require.Equal(t, f.project.Spec, p.Spec)
	require.Equal(t, f.project.CurrentContextRevision, p.CurrentContextRevision)
}

func TestAgentChangeDraftModelCannotAssertConfirmationOrOldValues(t *testing.T) {
	for _, injected := range []string{`"confirmed":true`, `"human_confirmed":true`, `"field_metadata":{}`, `"old_value":"forged"`} {
		payload := `{"reply":"候选","change_draft":{"expected_context_revision":1,"reason":"合成修改","fields":{"research_goal":"new"},"affected_chapter_ids":[],` + injected + `}}`
		model := &acceptanceChatStub{response: &types.ChatResponse{Content: payload, FinishReason: "stop"}}
		host := &G2HostAgentModel{models: &acceptanceModelServiceStub{models: []*types.Model{acceptanceDeepSeekModel()}, chatModel: model}}
		_, err := host.Reply(context.Background(), core.Actor{TenantID: 7, UserID: "owner"}, core.ModelRequest{Agent: "task_assistant", Purpose: "conversation", Context: g2TestModelContext(), UserInput: "提出候选"})
		require.Error(t, err, strings.TrimSpace(injected))
	}
}

func TestAgentChangeDraftHumanEditKeepsAIOriginAfterApply(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	turn, input, _ := agentDraftModelTurn(t, f)
	field := input.Fields["research_goal"]
	field.NewValue += "；人工补充范围限制"
	input.Fields["research_goal"] = field
	proposal := agentDraftPropose(t, f, input)
	raw, _, _, err := f.projects.ConfirmActionProposal(f.ctx, f.actor, f.project.ID, proposal.ID, "agent-draft-edited-confirm", core.ConfirmationInput{VersionExpectation: agentDraftExpectation(f.project, proposal.Version), Confirmed: true, Reason: "复核编辑后的候选"})
	require.NoError(t, err)
	var completed core.ActionProposal
	require.NoError(t, json.Unmarshal(raw, &completed))
	var draft core.ChangeSet
	require.NoError(t, json.Unmarshal(completed.Result, &draft))
	_, _, _, err = f.projects.AssessChangeSet(f.ctx, f.actor, f.project.ID, draft.ID, "agent-draft-edited-assess")
	require.NoError(t, err)
	_, _, _, err = f.projects.ApplyChangeSet(f.ctx, f.actor, f.project.ID, draft.ID, "agent-draft-edited-apply")
	require.NoError(t, err)
	p, err := f.projects.GetProject(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	require.Equal(t, field.NewValue, p.Spec["research_goal"])
	require.Equal(t, "ai_assisted_human", p.SpecFields["research_goal"].Origin)
	require.Equal(t, "pending_confirmation", p.SpecFields["research_goal"].Status)
	require.NotNil(t, p.SpecFields["research_goal"].Provenance)
	require.Equal(t, turn.ID, p.SpecFields["research_goal"].Provenance.SourceID)
}

func TestAgentChangeDraftReassessmentRetainsExactNewValuePin(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	turn, input, _ := agentDraftModelTurn(t, f)
	proposal := agentDraftPropose(t, f, input)
	raw, _, _, err := f.projects.ConfirmActionProposal(f.ctx, f.actor, f.project.ID, proposal.ID, "agent-draft-reassess-confirm", core.ConfirmationInput{VersionExpectation: agentDraftExpectation(f.project, proposal.Version), Confirmed: true, Reason: "明确仅建草稿"})
	require.NoError(t, err)
	var completed core.ActionProposal
	require.NoError(t, json.Unmarshal(raw, &completed))
	var draft core.ChangeSet
	require.NoError(t, json.Unmarshal(completed.Result, &draft))
	_, _, _, err = f.projects.AssessChangeSet(f.ctx, f.actor, f.project.ID, draft.ID, "agent-draft-before-stale-assess")
	require.NoError(t, err)
	// A separate human condition change explicitly selects the first fixture
	// chapter as its suggested review scope and advances the formal baseline.
	raw, _, _, err = f.projects.CreateChangeDraft(f.ctx, f.actor, f.project.ID, "agent-draft-other-field", core.ExtendedChangeSetInput{ExpectedContextRevision: f.project.CurrentContextRevision, Reason: "独立人工条件调整", Fields: map[string]core.ChangeFieldInput{"research_subject": {OldValue: f.project.Spec["research_subject"], NewValue: "另一合成研究对象"}}, AffectedChapterIDs: []string{f.chapters[0].ID}})
	require.NoError(t, err)
	var other core.ChangeSet
	require.NoError(t, json.Unmarshal(raw, &other))
	_, _, _, err = f.projects.AssessChangeSet(f.ctx, f.actor, f.project.ID, other.ID, "agent-draft-other-assess")
	require.NoError(t, err)
	_, _, _, err = f.projects.ApplyChangeSet(f.ctx, f.actor, f.project.ID, other.ID, "agent-draft-other-apply")
	require.NoError(t, err)
	_, status, _, err := f.projects.ApplyChangeSet(f.ctx, f.actor, f.project.ID, draft.ID, "agent-draft-stale-apply")
	require.NoError(t, err)
	require.Equal(t, 409, status)
	raw, _, _, err = f.projects.ReassessChangeSet(f.ctx, f.actor, f.project.ID, draft.ID, "agent-draft-reassess")
	require.NoError(t, err)
	var reassessed core.ChangeSet
	require.NoError(t, json.Unmarshal(raw, &reassessed))
	require.Equal(t, draft.ID, reassessed.ReassessedFromID)
	require.Equal(t, "assessed", reassessed.Status)
	_, _, _, err = f.projects.ApplyChangeSet(f.ctx, f.actor, f.project.ID, reassessed.ID, "agent-draft-reassessed-apply")
	require.NoError(t, err)
	p, err := f.projects.GetProject(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	require.Equal(t, "ai_generated", p.SpecFields["research_goal"].Origin)
	require.NotNil(t, p.SpecFields["research_goal"].Provenance)
	require.Equal(t, turn.ID, p.SpecFields["research_goal"].Provenance.SourceID)
}

func TestAgentChangeDraftUnselectedReviewChapterDoesNotRegisterProposal(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	_, input, _ := agentDraftModelTurn(t, f)
	// The UI may receive no model chapter suggestion. Without a subsequent
	// human selection, the existing backend shape guard must remain strict.
	input.AffectedChapterIDs = []string{}
	p, err := f.projects.GetProject(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	payload, err := json.Marshal(input)
	require.NoError(t, err)
	_, _, _, err = f.projects.CreateActionProposal(f.ctx, f.actor, f.project.ID, "agent-draft-unselected-chapter", core.CreateActionProposalInput{
		VersionExpectation: agentDraftExpectation(p, 0), Action: "create_change_set", TargetID: p.ID,
		Input: payload, SourceIDs: []string{}, ExpectedEffect: "未选建议复核章节不得登记提案",
	})
	require.ErrorIs(t, err, core.ErrInvalidRequest)
	require.Zero(t, agentDraftCount(t, f))
	proposals, err := f.projects.ListActionProposals(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	require.Empty(t, proposals)
}
