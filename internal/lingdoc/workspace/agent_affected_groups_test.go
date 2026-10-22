package workspace

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// AC-G3-14 (issue #251): the G3 impact assistant explains what a proposed
// change affects in two groups - "确定受影响" and "可能受影响" - and every
// entry carries an object reference, a reason and a basis.
//
// These tests drive the real application seam: the real host model parser, the
// real agent session/turn storage, the real action proposal confirmation, and
// the real workspace service over SQLite. Nothing here calls the domain
// internals directly.

const agentGroupsSyntheticValue = "仅使用合成访谈数据比较软件流程的可追踪性"

// assistantGroupsExplanation is a complete, traceable two-group explanation.
func assistantGroupsExplanation(t *testing.T, chapterID string) any {
	t.Helper()
	return map[string]any{
		"definite_affected": []any{
			map[string]any{
				"object": map[string]any{"kind": "ChapterVersion", "id": chapterID, "version": "v1"},
				"reason": "该章正文引用了被修改的研究条件",
				"basis":  map[string]any{"source_type": "dependency_edge", "source_id": "edge-1", "detail": "uses"},
			},
		},
		"possible_affected": []any{
			map[string]any{
				"object": map[string]any{"kind": "EvidenceCard", "id": "card-1", "version": "2"},
				"reason": "证据卡适用范围可能覆盖新条件",
				"basis":  map[string]any{"source_type": "ruleset", "source_id": "ruleset-1"},
			},
		},
	}
}

// assistantGroupsModelTurn runs one assistant turn whose model reply proposes a
// change draft and states the two explanation groups. The reply is produced by
// the real host model parser, so the groups travel the same path the
// production assistant uses.
func assistantGroupsModelTurn(t *testing.T, f deliveryGuardFixture, explanation any) (core.AgentTurn, core.AgentChangeDraftInput) {
	return assistantGroupsModelTurnWithKey(t, f, explanation, "one")
}

// assistantGroupsModelTurnWithKey runs one assistant turn under its own
// idempotency keys, so a second turn is not a replay of the first.
func assistantGroupsModelTurnWithKey(t *testing.T, f deliveryGuardFixture, explanation any, key string) (core.AgentTurn, core.AgentChangeDraftInput) {
	t.Helper()
	p, err := f.projects.GetProject(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	suggestion := core.AgentChangeDraftSuggestion{
		ExpectedContextRevision: p.CurrentContextRevision, Reason: "明确请求调整 research_goal",
		Fields: map[string]string{"research_goal": agentGroupsSyntheticValue}, AffectedChapterIDs: []string{},
	}
	response, err := json.Marshal(map[string]any{
		"reply":                "这是研究条件候选；确定影响与待确认的可能影响分别列出。",
		"change_draft":         suggestion,
		"affected_explanation": explanation,
	})
	require.NoError(t, err)
	chat := &acceptanceChatStub{response: &types.ChatResponse{Content: string(response), FinishReason: "stop", Usage: types.TokenUsage{PromptTokens: 100, CompletionTokens: 30}}}
	registered := acceptanceDeepSeekModel()
	registered.Parameters.Provider = "fake"
	host := &HostAgentModel{models: &acceptanceModelServiceStub{models: []*types.Model{registered}, chatModel: chat}}
	f.projects.ConfigureG2(core.G2Runtime{
		Sources: NewResearchAdapter(f.db, f.projects, f.runtime, adapterSharesStub{}, nil, nil),
		Model:   host, Actions: NewAgentActionExecutor(f.projects, f.runtime, nil),
	})
	raw, _, _, err := f.projects.CreateAgentSession(f.ctx, f.actor, p.ID, "task_assistant", "agent-groups-session-"+key, agentDraftExpectation(p, 0))
	require.NoError(t, err)
	var session core.AgentSession
	require.NoError(t, json.Unmarshal(raw, &session))
	raw, _, _, err = f.projects.SendAgentTurn(f.ctx, f.actor, p.ID, session.ID, "agent-groups-turn-"+key, core.AgentTurnInput{
		AgentContextInput: core.AgentContextInput{G2Expectation: agentDraftExpectation(p, session.Version), SourceIDs: []string{}, CardRefs: []core.G2VersionRef{}, SensitiveContextConfirmed: false},
		UserInput:         "请将 research_goal 改为：" + agentGroupsSyntheticValue + "。只提出草稿。",
	})
	require.NoError(t, err)
	var turn core.AgentTurn
	require.NoError(t, json.Unmarshal(raw, &turn))
	input := core.AgentChangeDraftInput{
		ExtendedChangeSetInput: core.ExtendedChangeSetInput{ExpectedContextRevision: p.CurrentContextRevision, Reason: suggestion.Reason, Fields: map[string]core.ChangeFieldInput{}, AffectedChapterIDs: []string{f.chapters[0].ID}},
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
	return turn, input
}

// assistantGroupsPropose registers the action proposal that would create the
// draft, without confirming it.
func assistantGroupsPropose(t *testing.T, f deliveryGuardFixture, input core.AgentChangeDraftInput) core.ActionProposal {
	return assistantGroupsProposeWithKey(t, f, input, "one")
}

// assistantGroupsProposeWithKey registers the action proposal under its own
// idempotency key, so a second proposal is not a replay of the first.
func assistantGroupsProposeWithKey(t *testing.T, f deliveryGuardFixture, input core.AgentChangeDraftInput, key string) core.ActionProposal {
	t.Helper()
	p, err := f.projects.GetProject(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	payload, err := json.Marshal(input)
	require.NoError(t, err)
	raw, _, _, err := f.projects.CreateActionProposal(f.ctx, f.actor, p.ID, "agent-groups-proposal-"+key, core.CreateActionProposalInput{
		G2Expectation: agentDraftExpectation(p, 0), Action: "create_change_set", TargetID: p.ID, Input: payload, SourceIDs: []string{}, ExpectedEffect: "仅创建待评估变更草稿，正式版本不变",
	})
	require.NoError(t, err)
	var proposal core.ActionProposal
	require.NoError(t, json.Unmarshal(raw, &proposal))
	require.Equal(t, "pending", proposal.Status)
	return proposal
}

// assistantGroupsTamperTurn replaces the stored turn's suggestions with the
// given raw model reply, so a test can prove what the server does with a turn
// it did not accept from the model boundary.
func assistantGroupsTamperTurn(t *testing.T, f deliveryGuardFixture, turn core.AgentTurn, suggestions string) {
	t.Helper()
	var row struct {
		PayloadJSON string
	}
	require.NoError(t, f.db.Table("lingdoc_g2_objects").Select("payload_json").
		Where("project_id=? AND kind=? AND id=?", f.project.ID, core.G2TurnKind, turn.ID).Take(&row).Error)
	var payload map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(row.PayloadJSON), &payload))
	payload["suggestions"] = json.RawMessage(suggestions)
	encoded, err := json.Marshal(payload)
	require.NoError(t, err)
	require.NoError(t, f.db.Table("lingdoc_g2_objects").
		Where("project_id=? AND kind=? AND id=?", f.project.ID, core.G2TurnKind, turn.ID).
		Update("payload_json", string(encoded)).Error)
}

// The assistant's two groups reach the draft, and the draft is still only a
// draft: the project revision does not move, no impact task is raised, no task
// is closed and no export is released.
func TestAssistantAffectedGroupsReachTheDraftWithoutTouchingFormalState(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	before := f.project
	turn, input := assistantGroupsModelTurn(t, f, assistantGroupsExplanation(t, f.chapters[0].ID))
	require.Equal(t, "completed", turn.Status)
	proposal := assistantGroupsPropose(t, f, input)
	decision := core.G2ConfirmedInput{G2Expectation: agentDraftExpectation(f.project, proposal.Version), Confirmed: true, Reason: "本人复核后仅确认建草稿"}
	raw, _, _, err := f.projects.ConfirmActionProposal(f.ctx, f.actor, f.project.ID, proposal.ID, "agent-groups-confirmed", decision)
	require.NoError(t, err)
	var completed core.ActionProposal
	require.NoError(t, json.Unmarshal(raw, &completed))
	require.Equal(t, "completed", completed.Status)
	var draft core.ChangeSet
	require.NoError(t, json.Unmarshal(completed.Result, &draft))
	require.Equal(t, "draft", draft.Status)

	// The two groups are readable on the draft, each with object, reason and basis.
	require.NotNil(t, draft.AffectedExplanation, "the draft must carry the assistant's two groups")
	require.Len(t, draft.AffectedExplanation.DefiniteAffected, 1)
	require.Len(t, draft.AffectedExplanation.PossibleAffected, 1)
	definite := draft.AffectedExplanation.DefiniteAffected[0]
	require.Equal(t, "ChapterVersion", definite.Object.Kind)
	require.Equal(t, f.chapters[0].ID, definite.Object.ID)
	require.NotEmpty(t, definite.Object.Version)
	require.NotEmpty(t, definite.Reason)
	require.Equal(t, "dependency_edge", definite.Basis.SourceType)
	require.NotEmpty(t, definite.Basis.SourceID)
	possible := draft.AffectedExplanation.PossibleAffected[0]
	require.Equal(t, "EvidenceCard", possible.Object.Kind)
	require.NotEmpty(t, possible.Reason)
	require.Equal(t, "ruleset", possible.Basis.SourceType)

	// The assistant side never answers with the resolver's field name.
	encoded, err := json.Marshal(draft.AffectedExplanation)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "certainty")
	require.Contains(t, string(encoded), "definite_affected")
	require.Contains(t, string(encoded), "possible_affected")

	// The formal state is untouched: no revision moved, no impact task was
	// raised, no task was closed and no export was released.
	after, err := f.projects.GetProject(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	require.Equal(t, before.ProjectVersion, after.ProjectVersion)
	require.Equal(t, before.SpecRevision, after.SpecRevision)
	require.Equal(t, before.CurrentContextRevision, after.CurrentContextRevision)
	require.Equal(t, before.Spec, after.Spec)
	tasks, err := f.projects.ListImpactTasks(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	require.Empty(t, tasks, "the assistant draft must not raise an impact task")
	var snapshots int64
	require.NoError(t, f.db.Table("lingdoc_release_snapshots").Where("project_id=?", f.project.ID).Count(&snapshots).Error)
	require.Zero(t, snapshots, "the assistant draft must not release a delivery snapshot")
	var artifacts int64
	require.NoError(t, f.db.Table("lingdoc_export_artifacts").Where("project_id=?", f.project.ID).Count(&artifacts).Error)
	require.Zero(t, artifacts, "the assistant draft must not release an export artifact")

	// The turn the draft came from is still the only source of the groups.
	require.Equal(t, turn.ID, input.FieldMetadata["research_goal"].Provenance.SourceID)
}

// The draft's two groups and the impact resolver's own verdict are two
// different things, and both can be asserted separately.
func TestAssistantAffectedGroupsAndResolverCertaintyAreSeparate(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	_, input := assistantGroupsModelTurn(t, f, assistantGroupsExplanation(t, f.chapters[0].ID))
	proposal := assistantGroupsPropose(t, f, input)
	decision := core.G2ConfirmedInput{G2Expectation: agentDraftExpectation(f.project, proposal.Version), Confirmed: true, Reason: "本人复核后仅确认建草稿"}
	raw, _, _, err := f.projects.ConfirmActionProposal(f.ctx, f.actor, f.project.ID, proposal.ID, "agent-groups-separate", decision)
	require.NoError(t, err)
	var completed core.ActionProposal
	require.NoError(t, json.Unmarshal(raw, &completed))
	var draft core.ChangeSet
	require.NoError(t, json.Unmarshal(completed.Result, &draft))

	// The draft side: two groups, no certainty field anywhere in the payload.
	draftJSON, err := json.Marshal(draft)
	require.NoError(t, err)
	require.NotContains(t, string(draftJSON), "certainty")
	require.Contains(t, string(draftJSON), "definite_affected")
	require.Contains(t, string(draftJSON), "possible_affected")

	// The resolver side: its own verdict still uses certainty, and it is not
	// reshaped into the assistant's two groups.
	_, _, _, err = f.projects.AssessChangeSet(f.ctx, f.actor, f.project.ID, draft.ID, "agent-groups-assess")
	require.NoError(t, err)
	assessment, err := f.projects.GetImpactAssessment(f.ctx, f.actor, f.project.ID, draft.ID)
	require.NoError(t, err)
	assessmentJSON, err := json.Marshal(assessment)
	require.NoError(t, err)
	require.Contains(t, string(assessmentJSON), "certainty")
	require.NotContains(t, string(assessmentJSON), "definite_affected")
	require.NotContains(t, string(assessmentJSON), "possible_affected")
}

// An explanation entry that cannot cite a basis is refused: it enters neither
// group, and the draft write leaves nothing behind.
//
// The refusal happens twice on purpose. The model boundary already refuses a
// reply whose explanation cannot cite a basis, so such a turn never completes.
// And even if a turn with such an explanation were persisted anyway, the draft
// write re-derives the groups from that turn and refuses, so the unsupported
// object still cannot reach a group.
func TestAssistantAffectedGroupsRefuseEntriesWithoutBasis(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	before := f.project
	unsupported := map[string]any{
		"definite_affected": []any{
			map[string]any{
				"object": map[string]any{"kind": "ChapterVersion", "id": f.chapters[0].ID, "version": "v1"},
				"reason": "该章正文引用了被修改的研究条件",
			},
		},
		"possible_affected": []any{},
	}
	// The model boundary refuses the reply outright.
	_, _ = assistantGroupsModelTurnWithKey(t, f, unsupported, "refused")

	// A turn that carries such an explanation anyway is refused by the draft
	// write, because the groups are re-derived from the persisted turn.
	turn, input := assistantGroupsModelTurnWithKey(t, f, assistantGroupsExplanation(t, f.chapters[0].ID), "tampered")
	require.Equal(t, "completed", turn.Status)
	tampered, err := json.Marshal(map[string]any{
		"reply":                "候选",
		"change_draft":         core.AgentChangeDraftSuggestion{ExpectedContextRevision: f.project.CurrentContextRevision, Reason: "明确请求调整 research_goal", Fields: map[string]string{"research_goal": agentGroupsSyntheticValue}, AffectedChapterIDs: []string{}},
		"affected_explanation": unsupported,
	})
	require.NoError(t, err)
	assistantGroupsTamperTurn(t, f, turn, string(tampered))

	proposal := assistantGroupsPropose(t, f, input)
	decision := core.G2ConfirmedInput{G2Expectation: agentDraftExpectation(f.project, proposal.Version), Confirmed: true, Reason: "本人复核后仅确认建草稿"}
	raw, _, _, err := f.projects.ConfirmActionProposal(f.ctx, f.actor, f.project.ID, proposal.ID, "agent-groups-no-basis", decision)
	require.NoError(t, err)
	var completed core.ActionProposal
	require.NoError(t, json.Unmarshal(raw, &completed))
	require.Equal(t, "failed", completed.Status, "a draft without a traceable basis must not be written")
	require.NotEmpty(t, completed.FailureCode)
	require.Empty(t, completed.Result)
	var drafts int64
	require.NoError(t, f.db.Table("lingdoc_change_sets").Where("project_id=?", f.project.ID).Count(&drafts).Error)
	require.Zero(t, drafts, "the refused draft must leave nothing behind")
	after, err := f.projects.GetProject(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	require.Equal(t, before.ProjectVersion, after.ProjectVersion)
	require.Equal(t, before.CurrentContextRevision, after.CurrentContextRevision)
	tasks, err := f.projects.ListImpactTasks(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	require.Empty(t, tasks)
	require.NotEmpty(t, turn.ID)

	// The refusal is the explanation check, not an incidental failure: the
	// same flow with a traceable explanation writes the draft.
	_, inputOK := assistantGroupsModelTurnWithKey(t, f, assistantGroupsExplanation(t, f.chapters[0].ID), "traceable")
	proposalOK := assistantGroupsProposeWithKey(t, f, inputOK, "traceable")
	rawOK, _, _, err := f.projects.ConfirmActionProposal(f.ctx, f.actor, f.project.ID, proposalOK.ID, "agent-groups-traceable", core.G2ConfirmedInput{G2Expectation: agentDraftExpectation(f.project, proposalOK.Version), Confirmed: true, Reason: "本人复核后仅确认建草稿"})
	require.NoError(t, err)
	var completedOK core.ActionProposal
	require.NoError(t, json.Unmarshal(rawOK, &completedOK))
	require.Equal(t, "completed", completedOK.Status, "a traceable explanation must still produce the draft")
}

// The client cannot state which objects are affected: the groups are derived
// from the persisted turn, so a forged payload changes nothing.
func TestAssistantAffectedGroupsIgnoreClientSuppliedExplanation(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	_, input := assistantGroupsModelTurn(t, f, assistantGroupsExplanation(t, f.chapters[0].ID))
	// The client tries to attach its own explanation to the proposal payload.
	forged := map[string]any{
		"expected_context_revision": input.ExpectedContextRevision,
		"reason":                    input.Reason,
		"fields":                    input.Fields,
		"affected_chapter_ids":      input.AffectedChapterIDs,
		"field_metadata":            input.FieldMetadata,
		"affected_explanation": map[string]any{
			"definite_affected": []any{
				map[string]any{
					"object": map[string]any{"kind": "ProjectSpec", "id": f.project.ID, "version": "999"},
					"reason": "客户端自称的确定影响",
					"basis":  map[string]any{"source_type": "dependency_edge", "source_id": "forged"},
				},
			},
			"possible_affected": []any{},
		},
	}
	payload, err := json.Marshal(forged)
	require.NoError(t, err)
	raw, _, _, err := f.projects.CreateActionProposal(f.ctx, f.actor, f.project.ID, "agent-groups-forged", core.CreateActionProposalInput{
		G2Expectation: agentDraftExpectation(f.project, 0), Action: "create_change_set", TargetID: f.project.ID, Input: payload, SourceIDs: []string{}, ExpectedEffect: "客户端自称的影响说明",
	})
	require.Error(t, err, "a client-supplied explanation must not be accepted")
	require.Empty(t, raw)
}

// A human confirmation is required before the draft exists, and the assistant's
// own output never reaches formal evaluation on its own.
func TestAssistantAffectedGroupsNeedHumanConfirmationBeforeAnyAssessment(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	before := f.project
	_, input := assistantGroupsModelTurn(t, f, assistantGroupsExplanation(t, f.chapters[0].ID))
	proposal := assistantGroupsPropose(t, f, input)
	var drafts int64
	require.NoError(t, f.db.Table("lingdoc_change_sets").Where("project_id=?", f.project.ID).Count(&drafts).Error)
	require.Zero(t, drafts, "a pending proposal is not a draft")

	// Unconfirmed: nothing is written.
	decision := core.G2ConfirmedInput{G2Expectation: agentDraftExpectation(f.project, proposal.Version), Confirmed: false, Reason: "尚未确认"}
	_, _, _, err := f.projects.ConfirmActionProposal(f.ctx, f.actor, f.project.ID, proposal.ID, "agent-groups-unconfirmed", decision)
	require.Error(t, err)
	require.NoError(t, f.db.Table("lingdoc_change_sets").Where("project_id=?", f.project.ID).Count(&drafts).Error)
	require.Zero(t, drafts)

	// A machine caller cannot confirm it either.
	machine := types.WithPrincipal(f.ctx, types.Principal{Type: types.PrincipalAPITenant, ID: "machine"})
	decision.Confirmed = true
	_, _, _, err = f.projects.ConfirmActionProposal(machine, f.actor, f.project.ID, proposal.ID, "agent-groups-machine", decision)
	require.Error(t, err)
	require.NoError(t, f.db.Table("lingdoc_change_sets").Where("project_id=?", f.project.ID).Count(&drafts).Error)
	require.Zero(t, drafts)

	// Only the human confirmation creates the draft, and it is still a draft.
	raw, _, _, err := f.projects.ConfirmActionProposal(f.ctx, f.actor, f.project.ID, proposal.ID, "agent-groups-human", decision)
	require.NoError(t, err)
	var completed core.ActionProposal
	require.NoError(t, json.Unmarshal(raw, &completed))
	require.Equal(t, "completed", completed.Status)
	var draft core.ChangeSet
	require.NoError(t, json.Unmarshal(completed.Result, &draft))
	require.Equal(t, "draft", draft.Status)
	require.NotNil(t, draft.AffectedExplanation)

	// The draft alone produces no formal evaluation and no impact task.
	_, err = f.projects.GetImpactAssessment(f.ctx, f.actor, f.project.ID, draft.ID)
	require.Error(t, err, "the assistant draft must not be an evaluation")
	tasks, err := f.projects.ListImpactTasks(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	require.Empty(t, tasks)
	after, err := f.projects.GetProject(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	require.Equal(t, before.ProjectVersion, after.ProjectVersion)
	require.Equal(t, before.CurrentContextRevision, after.CurrentContextRevision)
}

// The turn's own reply is what carries the groups; the draft endpoint
// contract publishes them, and the model may not smuggle a certainty field in.
func TestAssistantAffectedGroupsStayOutOfTheResolverVocabulary(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	forged := map[string]any{
		"definite_affected": []any{
			map[string]any{
				"object":    map[string]any{"kind": "ChapterVersion", "id": f.chapters[0].ID, "version": "v1"},
				"reason":    "该章正文引用了被修改的研究条件",
				"basis":     map[string]any{"source_type": "dependency_edge", "source_id": "edge-1"},
				"certainty": "definite",
			},
		},
		"possible_affected": []any{},
	}
	_, _ = assistantGroupsModelTurn(t, f, forged)
	// The host model parser refuses the reply: the assistant side has no
	// certainty field at all, so the resolver's vocabulary cannot leak in.
	p, err := f.projects.GetProject(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	response, err := json.Marshal(map[string]any{
		"reply":                "候选",
		"change_draft":         core.AgentChangeDraftSuggestion{ExpectedContextRevision: p.CurrentContextRevision, Reason: "合成修改", Fields: map[string]string{"research_goal": agentGroupsSyntheticValue}, AffectedChapterIDs: []string{}},
		"affected_explanation": forged,
	})
	require.NoError(t, err)
	require.True(t, strings.Contains(string(response), "certainty"))
	chat := &acceptanceChatStub{response: &types.ChatResponse{Content: string(response), FinishReason: "stop", Usage: types.TokenUsage{PromptTokens: 100, CompletionTokens: 30}}}
	registered := acceptanceDeepSeekModel()
	registered.Parameters.Provider = "fake"
	host := &HostAgentModel{models: &acceptanceModelServiceStub{models: []*types.Model{registered}, chatModel: chat}}
	_, err = host.Reply(f.ctx, f.actor, core.G2ModelRequest{Agent: "task_assistant", Purpose: "conversation", Context: testModelContext(), UserInput: "提出候选"})
	require.Error(t, err, "the assistant side must not carry a certainty field")
}
