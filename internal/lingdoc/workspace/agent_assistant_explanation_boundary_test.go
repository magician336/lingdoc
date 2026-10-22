package workspace

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// AC-G3-14 模型边界复核（issue #315）。
//
// #251 把模型边界收成「给了 change_draft 就必须同时给 affected_explanation，
// 否则整条回包被拒」。本票复核并放宽这条边界，三条路径各自钉住：
//
//  1. 模型只给 change_draft、不给 affected_explanation → 不再整条被拒：草稿留下，
//     自然语言回复也留下，服务端如实记「助手未给两组说明」；
//  2. 模型给了两组说明且每条依据可追溯 → 照旧接受，两组说明随草稿存在；
//  3. 模型给了两组说明但某条依据不可追溯 → 照旧既有语义：不可追溯的条目不得进入
//     任一组（整条回包被拒，这一轮失败）。
//
// 这些测试走真实应用缝：真实 host 模型解析器、真实轮次落库、真实提案确认、
// 真实 workspace service（SQLite）。不直接调用领域内部函数。

const assistantBoundarySyntheticValue = "仅使用合成访谈数据比较软件流程的可追踪性"

// assistantBoundaryTurn 让模型回包就是 payload 本身（不多不少一个键），跑一轮助手。
func assistantBoundaryTurn(t *testing.T, f deliveryGuardFixture, payload map[string]any, key string) (core.AgentTurn, core.AgentChangeDraftInput) {
	t.Helper()
	p, err := f.projects.GetProject(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	encoded, err := json.Marshal(payload)
	require.NoError(t, err)
	chat := &acceptanceChatStub{response: &types.ChatResponse{Content: string(encoded), FinishReason: "stop", Usage: types.TokenUsage{PromptTokens: 100, CompletionTokens: 30}}}
	registered := acceptanceDeepSeekModel()
	registered.Parameters.Provider = "fake"
	host := &HostAgentModel{models: &acceptanceModelServiceStub{models: []*types.Model{registered}, chatModel: chat}}
	f.projects.ConfigureAgentRuntime(core.AgentRuntime{
		Sources: NewResearchAdapter(f.db, f.projects, f.runtime, adapterSharesStub{}, nil, nil),
		Model:   host, Actions: NewAgentActionExecutor(f.projects, f.runtime, nil),
	})
	raw, _, _, err := f.projects.CreateAgentSession(f.ctx, f.actor, p.ID, "task_assistant", "agent-boundary-session-"+key, agentDraftExpectation(p, 0))
	require.NoError(t, err)
	var session core.AgentSession
	require.NoError(t, json.Unmarshal(raw, &session))
	raw, _, _, err = f.projects.SendAgentTurn(f.ctx, f.actor, p.ID, session.ID, "agent-boundary-turn-"+key, core.AgentTurnInput{
		AgentContextInput: core.AgentContextInput{VersionExpectation: agentDraftExpectation(p, session.Version), SourceIDs: []string{}, CardRefs: []core.VersionRef{}, SensitiveContextConfirmed: false},
		UserInput:         "请将 research_goal 改为：" + assistantBoundarySyntheticValue + "。只提出草稿。",
	})
	require.NoError(t, err)
	var turn core.AgentTurn
	require.NoError(t, json.Unmarshal(raw, &turn))
	input := core.AgentChangeDraftInput{
		ExtendedChangeSetInput: core.ExtendedChangeSetInput{ExpectedContextRevision: p.CurrentContextRevision, Reason: "明确请求调整 research_goal", Fields: map[string]core.ChangeFieldInput{}, AffectedChapterIDs: []string{f.chapters[0].ID}},
		FieldMetadata:          map[string]core.SpecFieldInput{},
	}
	old, exists := p.Spec["research_goal"]
	require.True(t, exists)
	input.Fields["research_goal"] = core.ChangeFieldInput{OldValue: old, NewValue: assistantBoundarySyntheticValue}
	input.FieldMetadata["research_goal"] = core.SpecFieldInput{Origin: "ai_assisted_human", Status: "pending_confirmation", Provenance: &core.ProvenanceRecord{
		SourceType: "agent_turn", SourceID: turn.ID, SourceVersion: strconv.FormatInt(turn.Version, 10), Purpose: "project_spec", Locator: "reply",
	}}
	return turn, input
}

// assistantBoundaryDraft 登记并人工确认「只建草稿」的提案，返回确认后的提案与草稿。
func assistantBoundaryDraft(t *testing.T, f deliveryGuardFixture, input core.AgentChangeDraftInput, key string) (core.ActionProposal, core.ChangeSet) {
	t.Helper()
	p, err := f.projects.GetProject(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	payload, err := json.Marshal(input)
	require.NoError(t, err)
	raw, _, _, err := f.projects.CreateActionProposal(f.ctx, f.actor, p.ID, "agent-boundary-proposal-"+key, core.CreateActionProposalInput{
		VersionExpectation: agentDraftExpectation(p, 0), Action: "create_change_set", TargetID: p.ID, Input: payload, SourceIDs: []string{}, ExpectedEffect: "仅创建待评估变更草稿，正式版本不变",
	})
	require.NoError(t, err)
	var proposal core.ActionProposal
	require.NoError(t, json.Unmarshal(raw, &proposal))
	decision := core.ConfirmationInput{VersionExpectation: agentDraftExpectation(f.project, proposal.Version), Confirmed: true, Reason: "本人复核后仅确认建草稿"}
	raw, _, _, err = f.projects.ConfirmActionProposal(f.ctx, f.actor, f.project.ID, proposal.ID, "agent-boundary-confirmed-"+key, decision)
	require.NoError(t, err)
	var completed core.ActionProposal
	require.NoError(t, json.Unmarshal(raw, &completed))
	var draft core.ChangeSet
	if completed.Status == "completed" {
		require.NoError(t, json.Unmarshal(completed.Result, &draft))
	}
	return completed, draft
}

// assistantBoundaryDraftPayload 是一份合法的助手草稿候选。
func assistantBoundaryDraftPayload(revision int64) core.AgentChangeDraftSuggestion {
	return core.AgentChangeDraftSuggestion{
		ExpectedContextRevision: revision, Reason: "明确请求调整 research_goal",
		Fields: map[string]string{"research_goal": assistantBoundarySyntheticValue}, AffectedChapterIDs: []string{},
	}
}

// 路径 1：模型只给 change_draft、不给 affected_explanation。
//
// 改动前：整条回包被拒 → 这一轮失败，自然语言回复与草稿一起丢掉，助手草稿功能不可用。
// 改动后：这一轮完成，草稿写得出来，服务端如实记「助手未给两组说明」，
// 草稿上没有 affected_explanation（验收台读回时就是「助手未给两组说明」这一条事实）。
func TestAssistantBoundaryKeepsDraftWhenModelOmitsAffectedExplanation(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	before := f.project
	turn, input := assistantBoundaryTurn(t, f, map[string]any{
		"reply":        "这是研究条件候选；本轮没有给出两组说明。",
		"change_draft": assistantBoundaryDraftPayload(f.project.CurrentContextRevision),
	}, "missing")

	// 整条回包不再被拒：这一轮完成，自然语言回复与结构化输出都留着。
	require.Equal(t, "completed", turn.Status, "a draft without the two groups must not lose the whole reply")
	require.NotEmpty(t, turn.Reply, "the natural language reply must survive")
	require.Contains(t, string(turn.Suggestions), "change_draft")

	// 服务端如实记下「助手未给两组说明」，而不是把这一轮当成失败。
	state, err := core.AssistantAffectedStateForTurn(turn)
	require.NoError(t, err)
	require.Equal(t, core.AssistantAffectedStateMissing, state)

	// 草稿写得出来，且不带上助手没给过的两组。
	completed, draft := assistantBoundaryDraft(t, f, input, "missing")
	require.Equal(t, "completed", completed.Status, "a draft without the two groups must still be writable")
	require.Equal(t, "draft", draft.Status)
	require.Nil(t, draft.AffectedExplanation, "the draft must not carry groups the assistant never gave")
	encoded, err := json.Marshal(draft)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "affected_explanation", "the draft reads back as 'the assistant gave no two groups'")
	require.NotContains(t, string(encoded), "certainty")

	// 正式状态未动：没有修订推进，没有影响任务，没有交付物。
	after, err := f.projects.GetProject(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	require.Equal(t, before.ProjectVersion, after.ProjectVersion)
	require.Equal(t, before.SpecRevision, after.SpecRevision)
	require.Equal(t, before.CurrentContextRevision, after.CurrentContextRevision)
	require.Equal(t, before.Spec, after.Spec)
	tasks, err := f.projects.ListImpactTasks(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	require.Empty(t, tasks, "the assistant draft must not raise an impact task")
}

// 路径 2：模型给了两组说明且每条依据可追溯 → 照旧接受，两组说明随草稿存在。
func TestAssistantBoundaryKeepsBothGroupsWhenModelGivesTraceableExplanation(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	turn, input := assistantBoundaryTurn(t, f, map[string]any{
		"reply":        "这是研究条件候选；确定影响与待确认的可能影响分别列出。",
		"change_draft": assistantBoundaryDraftPayload(f.project.CurrentContextRevision),
		"affected_explanation": map[string]any{
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
		},
	}, "traceable")

	require.Equal(t, "completed", turn.Status)
	state, err := core.AssistantAffectedStateForTurn(turn)
	require.NoError(t, err)
	require.Equal(t, core.AssistantAffectedStatePresent, state)

	completed, draft := assistantBoundaryDraft(t, f, input, "traceable")
	require.Equal(t, "completed", completed.Status)
	require.Equal(t, "draft", draft.Status)
	require.NotNil(t, draft.AffectedExplanation, "the draft must carry the assistant's two groups")
	require.Len(t, draft.AffectedExplanation.DefiniteAffected, 1)
	require.Len(t, draft.AffectedExplanation.PossibleAffected, 1)
	definite := draft.AffectedExplanation.DefiniteAffected[0]
	require.Equal(t, "ChapterVersion", definite.Object.Kind)
	require.Equal(t, f.chapters[0].ID, definite.Object.ID)
	require.NotEmpty(t, definite.Reason)
	require.Equal(t, "dependency_edge", definite.Basis.SourceType)
	require.NotEmpty(t, definite.Basis.SourceID)
}

// 路径 3：模型给了两组说明但某条依据不可追溯 → 既有语义不变：
// 不可追溯的条目不得进入任一组，整条回包被拒，这一轮失败。
func TestAssistantBoundaryRefusesUntraceableExplanation(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	turn, _ := assistantBoundaryTurn(t, f, map[string]any{
		"reply":        "候选",
		"change_draft": assistantBoundaryDraftPayload(f.project.CurrentContextRevision),
		"affected_explanation": map[string]any{
			"definite_affected": []any{map[string]any{
				"object": map[string]any{"kind": "ChapterVersion", "id": f.chapters[0].ID, "version": "v1"},
				"reason": "该章正文引用了被修改的研究条件",
			}},
			"possible_affected": []any{},
		},
	}, "unbased")

	require.Equal(t, "failed", turn.Status, "an entry without a basis must not be accepted")
	require.Empty(t, turn.Suggestions, "the refused reply leaves no structured output behind")
	require.NotEmpty(t, turn.FailureCode)
}

// 模型边界本身（不经轮次）：缺两组说明的回包被接受，依据不可追溯的回包被拒。
func TestAssistantBoundaryModelReplyKeepsDraftWithoutAffectedExplanation(t *testing.T) {
	model := &acceptanceChatStub{response: &types.ChatResponse{Content: `{"reply":"候选","change_draft":{"expected_context_revision":1,"reason":"明确请求调整 research_goal","fields":{"research_goal":"` + assistantBoundarySyntheticValue + `"},"affected_chapter_ids":[]}}`, FinishReason: "stop", Usage: types.TokenUsage{PromptTokens: 100, CompletionTokens: 30}}}
	host := &HostAgentModel{models: &acceptanceModelServiceStub{models: []*types.Model{acceptanceDeepSeekModel()}, chatModel: model}}
	reply, err := host.Reply(context.Background(), core.Actor{TenantID: 7, UserID: "owner"}, core.ModelRequest{Agent: "task_assistant", Purpose: "conversation", Context: testModelContext(), UserInput: "提出候选"})
	require.NoError(t, err, "a reply without the two groups must not be refused as a whole")
	require.Equal(t, "候选", reply.Content)
	require.Contains(t, string(reply.StructuredOutput), "change_draft")

	// 依据不可追溯的回包仍然被拒（既有语义不变）。
	model.response = &types.ChatResponse{Content: `{"reply":"候选","change_draft":{"expected_context_revision":1,"reason":"明确请求调整 research_goal","fields":{"research_goal":"` + assistantBoundarySyntheticValue + `"},"affected_chapter_ids":[]},"affected_explanation":{"definite_affected":[{"object":{"kind":"ChapterVersion","id":"chapter-1","version":"v1"},"reason":"没有依据"}],"possible_affected":[]}}`, FinishReason: "stop", Usage: types.TokenUsage{PromptTokens: 100, CompletionTokens: 30}}
	_, err = host.Reply(context.Background(), core.Actor{TenantID: 7, UserID: "owner"}, core.ModelRequest{Agent: "task_assistant", Purpose: "conversation", Context: testModelContext(), UserInput: "提出候选"})
	require.Error(t, err, "an entry without a basis must still be refused")
}
