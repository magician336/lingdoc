package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
)

type keywordOnlyProcessEngine struct {
	parentChildRetrieveEngine
	t *testing.T
}

func (e *keywordOnlyProcessEngine) Support() []types.RetrieverType {
	return []types.RetrieverType{types.VectorRetrieverType, types.KeywordsRetrieverType}
}
func (e *keywordOnlyProcessEngine) EstimateStorageSize(_ context.Context, model embedding.Embedder, _ []*types.IndexInfo, kinds []types.RetrieverType) int64 {
	if model != nil || len(kinds) != 1 || kinds[0] != types.KeywordsRetrieverType {
		e.t.Fatalf("keyword estimate requested vectors: model=%v types=%v", model, kinds)
	}
	return 0
}
func (e *keywordOnlyProcessEngine) BatchIndex(ctx context.Context, model embedding.Embedder, infos []*types.IndexInfo, kinds []types.RetrieverType) error {
	if model != nil || len(kinds) != 1 || kinds[0] != types.KeywordsRetrieverType {
		e.t.Fatalf("keyword indexing requested vectors: model=%v types=%v", model, kinds)
	}
	return e.parentChildRetrieveEngine.BatchIndex(ctx, model, infos, kinds)
}

func TestProcessChunksKeywordIndexWithoutEmbeddingModel(t *testing.T) {
	knowledge := &types.Knowledge{ID: "keyword-material", TenantID: 1, KnowledgeBaseID: "kb", ParseStatus: types.ParseStatusProcessing}
	engine := &keywordOnlyProcessEngine{t: t}
	tenant := &types.Tenant{ID: 1, RetrieverEngines: types.RetrieverEngines{Engines: []types.RetrieverEngineParams{
		{RetrieverType: types.VectorRetrieverType, RetrieverEngineType: types.PostgresRetrieverEngineType},
		{RetrieverType: types.KeywordsRetrieverType, RetrieverEngineType: types.PostgresRetrieverEngineType},
	}}}
	ctx := context.WithValue(context.Background(), types.TenantInfoContextKey, tenant)
	svc := &knowledgeService{
		repo: &parentChildKnowledgeRepo{knowledge: knowledge}, chunkRepo: &parentChildChunkService{},
		modelService: unavailableEmbeddingModel{}, retrieveEngine: parentChildRetrieveRegistry{engine: engine},
		graphEngine: parentChildGraphRepo{}, tenantRepo: parentChildTenantRepo{}, task: parentChildTaskEnqueuer{},
	}
	kb := &types.KnowledgeBase{ID: "kb", TenantID: 1, IndexingStrategy: types.IndexingStrategy{KeywordEnabled: true}}
	svc.processChunks(ctx, kb, knowledge, []types.ParsedChunk{{Content: "真实来源内容", Seq: 0, Start: 0, End: 6, ParentIndex: -1}})
	if len(engine.indexed) != 1 || knowledge.ParseStatus == types.ParseStatusFailed {
		t.Fatalf("keyword material not indexed: indexed=%d status=%s error=%s", len(engine.indexed), knowledge.ParseStatus, knowledge.ErrorMessage)
	}
}
