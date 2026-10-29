package workspace

import (
	"context"
	"errors"
	"strings"
	"testing"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
)

// The sentence-location suggester's host adapter (#774 tier C): one Chat
// call, strict decode, one ledger row per call. These tests use the shared
// stub trio (model service, chat, ledger) — no provider is contacted.

func locationSuggestionRequest() core.ImpactLocationSuggestionRequest {
	return core.ImpactLocationSuggestionRequest{
		ProjectID: "p-1",
		Change:    core.ChangeSet{Reason: "研究对象变化", Fields: []core.ChangeFieldDelta{{Key: "research_subject", OldValue: "旧研究主题全文", NewValue: "新研究主题定稿"}}},
		Task:      core.ImpactTask{Action: "REVIEW_REQUIRED", Target: core.ObjectVersionRef{Kind: "ChapterVersion", ID: "c-1", Version: "v1"}, Causes: []core.ImpactCause{{EdgeID: "explicit:e-1", Reason: "研究条件变化"}}},
		Chapter:   core.Chapter{Title: "研究问题", BodyMarkdown: "研究对象界定为旧研究主题全文。"},
	}
}

func locationChatStub(content string, err error) *acceptanceChatStub {
	return &acceptanceChatStub{
		response: &types.ChatResponse{
			Content:      content,
			FinishReason: "stop",
			Usage:        types.TokenUsage{PromptTokens: 200, CompletionTokens: 80},
		},
		err: err,
	}
}

const locationAnswer = `{"suggestions":[{"quote":"研究对象界定为旧研究主题全文。","paragraph_hint":1,"impact_reason":"样本表述与变更冲突","confidence":0.8,"suggested_action":"核对样本数表述"}]}`

func TestImpactLocationSuggestionModelParsesTheAnswerAndReportsTheModel(t *testing.T) {
	chatModel := locationChatStub(locationAnswer, nil)
	model := NewHostImpactLocationSuggestionModel(&acceptanceModelServiceStub{
		models: []*types.Model{acceptanceDeepSeekModel()}, chatModel: chatModel,
	})
	result, err := model.Suggest(context.Background(), core.Actor{TenantID: 9, UserID: "owner"}, locationSuggestionRequest())
	if err != nil {
		t.Fatalf("suggest: %v", err)
	}
	if len(result.Proposals) != 1 || result.Proposals[0].Quote != "研究对象界定为旧研究主题全文。" || result.Proposals[0].Confidence != 0.8 {
		t.Fatalf("proposals = %+v", result.Proposals)
	}
	if result.ModelID != "model-1" || result.RunMode != "real" || !strings.HasPrefix(result.ModelRunID, "loc-") {
		t.Fatalf("model info = %+v", result)
	}
	if len(chatModel.messages) != 2 || !strings.Contains(chatModel.messages[0].Content, "定位器") {
		t.Fatalf("messages = %+v", chatModel.messages)
	}
	if !strings.Contains(chatModel.messages[1].Content, "旧研究主题全文") {
		t.Fatalf("user payload lost the chapter body")
	}
	if chatModel.options == nil || chatModel.options.MaxCompletionTokens != 4096 {
		t.Fatalf("options = %+v", chatModel.options)
	}
}

func TestImpactLocationSuggestionModelFailsClosedWithoutADefaultModel(t *testing.T) {
	model := NewHostImpactLocationSuggestionModel(&acceptanceModelServiceStub{
		models: []*types.Model{}, chatModel: locationChatStub(locationAnswer, nil),
	})
	_, err := model.Suggest(context.Background(), core.Actor{TenantID: 9, UserID: "owner"}, locationSuggestionRequest())
	if !errors.Is(err, core.ErrImpactLocationSuggestionUnavailable) {
		t.Fatalf("no default model must fail closed, got %v", err)
	}
}

func TestImpactLocationSuggestionModelSurfacesProviderFailureWithAFailedRow(t *testing.T) {
	ledger := &recordingModelCallLedger{}
	model := NewHostImpactLocationSuggestionModel(&acceptanceModelServiceStub{
		models: []*types.Model{acceptanceDeepSeekModel()}, chatModel: locationChatStub("", errors.New("provider down")),
	}, WithImpactLocationLedger(ledger))
	_, err := model.Suggest(context.Background(), core.Actor{TenantID: 9, UserID: "owner"}, locationSuggestionRequest())
	if !errors.Is(err, core.ErrImpactLocationSuggestionUnavailable) {
		t.Fatalf("provider failure must surface as unavailable, got %v", err)
	}
	if len(ledger.entries) != 1 || ledger.entries[0].Status != core.ModelCallLedgerStatusFailed ||
		ledger.entries[0].SourceKind != core.ModelCallLedgerSourceImpactLocationSuggestionRun {
		t.Fatalf("failure ledger = %+v", ledger.entries)
	}
}

func TestImpactLocationSuggestionModelRecordsOneSucceededRowPerCall(t *testing.T) {
	ledger := &recordingModelCallLedger{}
	model := NewHostImpactLocationSuggestionModel(&acceptanceModelServiceStub{
		models: []*types.Model{acceptanceDeepSeekModel()}, chatModel: locationChatStub(locationAnswer, nil),
	}, WithImpactLocationLedger(ledger))
	if _, err := model.Suggest(context.Background(), core.Actor{TenantID: 9, UserID: "owner"}, locationSuggestionRequest()); err != nil {
		t.Fatalf("suggest: %v", err)
	}
	if len(ledger.entries) != 1 || ledger.entries[0].Status != core.ModelCallLedgerStatusSucceeded ||
		ledger.entries[0].SourceKind != core.ModelCallLedgerSourceImpactLocationSuggestionRun ||
		ledger.entries[0].InputTokens != 200 || ledger.entries[0].OutputTokens != 80 {
		t.Fatalf("ledger = %+v", ledger.entries)
	}
}

func TestImpactLocationSuggestionModelRefusesTruncatedAndInventedAnswers(t *testing.T) {
	model := NewHostImpactLocationSuggestionModel(&acceptanceModelServiceStub{
		models: []*types.Model{acceptanceDeepSeekModel()},
		chatModel: &acceptanceChatStub{response: &types.ChatResponse{
			Content: `{"suggestions":[{"quote":"截断`, FinishReason: "length",
			Usage: types.TokenUsage{PromptTokens: 200, CompletionTokens: 4096},
		}},
	})
	if _, err := model.Suggest(context.Background(), core.Actor{TenantID: 9, UserID: "owner"}, locationSuggestionRequest()); !errors.Is(err, core.ErrImpactLocationSuggestionInvalidOutput) {
		t.Fatalf("truncated answer error = %v, want invalid output", err)
	}
	invented := NewHostImpactLocationSuggestionModel(&acceptanceModelServiceStub{
		models:    []*types.Model{acceptanceDeepSeekModel()},
		chatModel: locationChatStub(`{"suggestions":[{"quote":"x","paragraph_hint":1,"impact_reason":"y","confidence":0.5,"suggested_action":"z","target_chapter":"invented"}]}`, nil),
	})
	if _, err := invented.Suggest(context.Background(), core.Actor{TenantID: 9, UserID: "owner"}, locationSuggestionRequest()); !errors.Is(err, core.ErrImpactLocationSuggestionInvalidOutput) {
		t.Fatalf("invented field error = %v, want invalid output", err)
	}
}
