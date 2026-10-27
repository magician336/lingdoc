package workspace

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// One live round-trip of the dependency recommender against a real provider
// (#751, matrix row 15). Skipped unless the three env vars point at a real
// OpenAI-compatible endpoint — the same convention the Postgres tests use for
// their DSN. The API key is read from the environment and never logged.
//
// What this proves beyond the stub tests: a real model answers the
// recommender's prompt in the agreed JSON shape, the strict decoder accepts a
// real answer, and the ledger row carries the real provider's token usage.
func TestDependencyRecommendationModelLivesAgainstARealProvider(t *testing.T) {
	baseURL := os.Getenv("LINGDOC_LIVE_RECOMMENDATION_BASE_URL")
	apiKey := os.Getenv("LINGDOC_LIVE_RECOMMENDATION_API_KEY")
	modelName := os.Getenv("LINGDOC_LIVE_RECOMMENDATION_MODEL")
	if baseURL == "" || apiKey == "" || modelName == "" {
		t.Skip("live provider not configured: set LINGDOC_LIVE_RECOMMENDATION_BASE_URL / _API_KEY / _MODEL")
	}
	row := &types.Model{
		ID: "live-recommendation-1", Name: modelName,
		Type: types.ModelTypeKnowledgeQA, Source: types.ModelSourceRemote, Status: types.ModelStatusActive, IsDefault: true,
		Parameters: types.ModelParameters{Provider: "generic", BaseURL: baseURL, APIKey: apiKey},
	}
	realChat, err := chat.NewChat(chat.ConfigFromModel(row, "", ""), nil)
	if err != nil {
		t.Fatalf("build chat: %v", err)
	}
	ledger := &recordingModelCallLedger{}
	model := NewHostDependencyRecommendationModel(&acceptanceModelServiceStub{models: []*types.Model{row}, chatModel: realChat}, WithRecommendationLedger(ledger))
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	result, err := model.Recommend(ctx, core.Actor{TenantID: 1, UserID: "live-owner"}, core.DependencyRecommendationRequest{
		ProjectID: "live-project",
		Signals: core.DependencyRecommendationSignals{
			Objects: []core.DependencySignalObject{
				{Ref: core.ObjectVersionRef{Kind: "ProjectSpec", ID: "live-project", Version: "3"}, Title: "项目研究条件", Detail: "模板字段：method（研究方法）、budget（研究预算）"},
				{Ref: core.ObjectVersionRef{Kind: "ChapterVersion", ID: "chapter-1", Version: "v1"}, Title: "研究方法", SourceIDs: []string{"source-1"}},
				{Ref: core.ObjectVersionRef{Kind: "EvidenceCard", ID: "card-1", Version: "2"}, Title: "证据卡 card-1", SourceIDs: []string{"source-1"}, ScopeChapterIDs: []string{"chapter-1"}, Detail: "support_type=supports；scope=方法学"},
			},
			Edges: []core.DependencyEdgeSummary{},
		},
	})
	if err != nil {
		t.Fatalf("live recommend: %v", err)
	}
	if result.ModelID != row.ID || result.RunMode != "real" || !strings.HasPrefix(result.ModelRunID, "rec-") {
		t.Fatalf("model info = %+v", result)
	}
	if len(ledger.entries) != 1 || ledger.entries[0].Status != core.ModelCallLedgerStatusSucceeded ||
		ledger.entries[0].SourceKind != core.ModelCallLedgerSourceDependencyRecommendationRun {
		t.Fatalf("live ledger = %+v", ledger.entries)
	}
	entry := ledger.entries[0]
	t.Logf("live call: provider=%s model=%s tokens=%d/%d duration=%dms proposals=%d",
		entry.Provider, entry.ModelID, entry.InputTokens, entry.OutputTokens, entry.DurationMS, len(result.Proposals))
	for i, proposal := range result.Proposals {
		t.Logf("proposal %d: %s/%s -> %s/%s %s %s confidence=%.2f", i+1,
			proposal.SourceKind, proposal.SourceID, proposal.TargetKind, proposal.TargetID,
			proposal.Relation, proposal.Action, proposal.Confidence)
	}
}
