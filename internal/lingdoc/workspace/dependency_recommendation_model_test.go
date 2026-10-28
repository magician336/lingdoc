package workspace

import (
	"context"
	"errors"
	"strings"
	"testing"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
)

// The recommender's host adapter (#751): one Chat call, strict decode, one
// ledger row per call. These tests use the shared stub trio (model service,
// chat, ledger) — no provider is contacted.

func recommendationRequest() core.DependencyRecommendationRequest {
	return core.DependencyRecommendationRequest{
		ProjectID: "p-1",
		Signals: core.DependencyRecommendationSignals{
			Objects: []core.DependencySignalObject{{
				Ref:    core.ObjectVersionRef{Kind: "ProjectSpec", ID: "p-1", Version: "1"},
				Title:  "项目研究条件",
				Detail: "模板字段：method（研究方法）",
			}},
			Edges: []core.DependencyEdgeSummary{},
		},
	}
}

func recommendationChatStub(content string, err error) *acceptanceChatStub {
	return &acceptanceChatStub{
		response: &types.ChatResponse{
			Content:      content,
			FinishReason: "stop",
			Usage:        types.TokenUsage{PromptTokens: 120, CompletionTokens: 48},
		},
		err: err,
	}
}

const recommendationAnswer = `{"proposals":[{"source_kind":"ProjectSpec","source_id":"p-1","target_kind":"ProjectSpec","target_id":"p-1","relation":"uses","action":"REGENERATE","evidence":"第 1 章引用了研究方法字段","reason":"条件变化需重生成","confidence":0.7}]}`

func TestDependencyRecommendationModelParsesTheAnswerAndReportsTheModel(t *testing.T) {
	chatModel := recommendationChatStub(recommendationAnswer, nil)
	model := NewHostDependencyRecommendationModel(&acceptanceModelServiceStub{
		models: []*types.Model{acceptanceDeepSeekModel()}, chatModel: chatModel,
	})
	result, err := model.Recommend(context.Background(), core.Actor{TenantID: 9, UserID: "owner"}, recommendationRequest())
	if err != nil {
		t.Fatalf("recommend: %v", err)
	}
	if len(result.Proposals) != 1 || result.Proposals[0].SourceKind != "ProjectSpec" || result.Proposals[0].Action != "REGENERATE" {
		t.Fatalf("proposals = %+v", result.Proposals)
	}
	if result.ModelID != "model-1" || result.RunMode != "real" || !strings.HasPrefix(result.ModelRunID, "rec-") {
		t.Fatalf("model info = %+v", result)
	}
	if len(chatModel.messages) != 2 || !strings.Contains(chatModel.messages[0].Content, "依赖图推荐器") {
		t.Fatalf("messages = %+v", chatModel.messages)
	}
	if chatModel.options == nil || chatModel.options.MaxCompletionTokens != 4096 {
		t.Fatalf("options = %+v", chatModel.options)
	}
}

func TestDependencyRecommendationModelFailsClosedWithoutADefaultModel(t *testing.T) {
	model := NewHostDependencyRecommendationModel(&acceptanceModelServiceStub{
		models: []*types.Model{}, chatModel: recommendationChatStub(recommendationAnswer, nil),
	})
	_, err := model.Recommend(context.Background(), core.Actor{TenantID: 9, UserID: "owner"}, recommendationRequest())
	if !errors.Is(err, core.ErrDependencyRecommendationUnavailable) {
		t.Fatalf("no default model must fail closed, got %v", err)
	}
}

func TestDependencyRecommendationModelSurfacesProviderFailureWithAFailedRow(t *testing.T) {
	ledger := &recordingModelCallLedger{}
	model := NewHostDependencyRecommendationModel(&acceptanceModelServiceStub{
		models: []*types.Model{acceptanceDeepSeekModel()}, chatModel: recommendationChatStub("", errors.New("provider down")),
	}, WithRecommendationLedger(ledger))
	_, err := model.Recommend(context.Background(), core.Actor{TenantID: 9, UserID: "owner"}, recommendationRequest())
	if !errors.Is(err, core.ErrDependencyRecommendationUnavailable) {
		t.Fatalf("provider failure must surface as unavailable, got %v", err)
	}
	if len(ledger.entries) != 1 || ledger.entries[0].Status != core.ModelCallLedgerStatusFailed ||
		ledger.entries[0].SourceKind != core.ModelCallLedgerSourceDependencyRecommendationRun {
		t.Fatalf("failure ledger = %+v", ledger.entries)
	}
}

func TestDependencyRecommendationModelRefusesUnshapelyAnswers(t *testing.T) {
	cases := map[string]string{
		"not json":         "这不是 JSON",
		"unknown field":    `{"proposals":[],"notes":"extra"}`,
		"trailing value":   recommendationAnswer + ` {"again":1}`,
		"truncated answer": `{"proposals":[{"source_kind":"ProjectSpec"`,
	}
	for name, content := range cases {
		model := NewHostDependencyRecommendationModel(&acceptanceModelServiceStub{
			models: []*types.Model{acceptanceDeepSeekModel()}, chatModel: recommendationChatStub(content, nil),
		})
		_, err := model.Recommend(context.Background(), core.Actor{TenantID: 9, UserID: "owner"}, recommendationRequest())
		if !errors.Is(err, core.ErrDependencyRecommendationInvalidOutput) {
			t.Fatalf("%s must be refused as invalid output, got %v", name, err)
		}
	}
}

func TestDependencyRecommendationModelLeavesOneSucceededRowPerCall(t *testing.T) {
	ledger := &recordingModelCallLedger{}
	model := NewHostDependencyRecommendationModel(&acceptanceModelServiceStub{
		models: []*types.Model{acceptanceDeepSeekModel()}, chatModel: recommendationChatStub(recommendationAnswer, nil),
	}, WithRecommendationLedger(ledger))
	if _, err := model.Recommend(context.Background(), core.Actor{TenantID: 9, UserID: "owner"}, recommendationRequest()); err != nil {
		t.Fatal(err)
	}
	if len(ledger.entries) != 1 {
		t.Fatalf("one call must leave one row, got %d", len(ledger.entries))
	}
	entry := ledger.entries[0]
	if entry.Status != core.ModelCallLedgerStatusSucceeded || entry.ModelID != "model-1" || entry.Provider != "generic" ||
		entry.InputTokens != 120 || entry.OutputTokens != 48 || entry.ProjectID != "p-1" || entry.SourceID == "" {
		t.Fatalf("ledger entry = %+v", entry)
	}
}
