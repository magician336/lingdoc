package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// The chunk stage and the embedding stage are decoupled.
//
// A knowledge base whose embedding model is missing (or cannot be resolved)
// must still be able to finish parsing with its chunks on disk. The chunks
// carry the source anchors — coordinates plus quoted text — so refusing to
// write them turns "this document is not searchable" into "this document does
// not exist", and every operation that re-checks the sources of that material
// is refused for a reason that has nothing to do with the material (#485 /
// D5: the material could not be ingested at all inside the controlled
// window).
//
// The failure is not hidden: the chunks report index_status=failed (they are
// not reflected in the retrieval stores) and the stage records the cause.
// ---------------------------------------------------------------------------

// embeddingDecoupledChunkService records the chunks the pipeline writes and
// the index status it later marks them with.
type embeddingDecoupledChunkService struct {
	interfaces.ChunkRepository
	created []*types.Chunk
	updated []*types.Chunk
	// deleted counts the wipes of the whole knowledge. The pipeline wipes
	// the previous attempt's chunks before it writes the new ones, so the
	// counter alone says nothing; deletedAfterCreate is the one that
	// matters — a wipe after the chunks were written would take the
	// material's anchors with it.
	deleted            int
	deletedAfterCreate int
}

func (s *embeddingDecoupledChunkService) DeleteChunksByKnowledgeID(context.Context, uint64, string) error {
	s.deleted++
	if len(s.created) > 0 {
		s.deletedAfterCreate++
	}
	return nil
}

func (s *embeddingDecoupledChunkService) CreateChunks(_ context.Context, chunks []*types.Chunk) error {
	s.created = append([]*types.Chunk(nil), chunks...)
	return nil
}

func (s *embeddingDecoupledChunkService) UpdateChunk(_ context.Context, chunk *types.Chunk) error {
	s.updated = append(s.updated, chunk)
	return nil
}

// unavailableEmbeddingModelService answers every embedding lookup with an
// error — the shape of "this knowledge base has no usable embedding model".
type unavailableEmbeddingModelService struct {
	interfaces.ModelService
	calls int
}

func (s *unavailableEmbeddingModelService) GetEmbeddingModel(
	context.Context, string,
) (embedding.Embedder, error) {
	s.calls++
	return nil, errors.New("model ID cannot be empty")
}

// failingIndexRetrieveEngine accepts the index write and then fails it — the
// shape of "the provider refused the call".
type failingIndexRetrieveEngine struct {
	interfaces.RetrieveEngineService
	indexed []*types.IndexInfo
}

func (e *failingIndexRetrieveEngine) EngineType() types.RetrieverEngineType {
	return types.PostgresRetrieverEngineType
}

func (e *failingIndexRetrieveEngine) Support() []types.RetrieverType {
	return []types.RetrieverType{types.VectorRetrieverType}
}

func (e *failingIndexRetrieveEngine) DeleteByKnowledgeIDList(
	context.Context, []string, int, string,
) error {
	return nil
}

func (e *failingIndexRetrieveEngine) EstimateStorageSize(
	context.Context, embedding.Embedder, []*types.IndexInfo, []types.RetrieverType,
) int64 {
	return 0
}

func (e *failingIndexRetrieveEngine) BatchIndex(
	_ context.Context,
	_ embedding.Embedder,
	infos []*types.IndexInfo,
	_ []types.RetrieverType,
) error {
	e.indexed = append([]*types.IndexInfo(nil), infos...)
	return errors.New("model is outside the acceptance budget profile")
}

func embeddingDecoupledTestTenant() *types.Tenant {
	return &types.Tenant{
		ID: 1,
		RetrieverEngines: types.RetrieverEngines{Engines: []types.RetrieverEngineParams{
			{
				RetrieverType:       types.VectorRetrieverType,
				RetrieverEngineType: types.PostgresRetrieverEngineType,
			},
		}},
	}
}

// keywordOnlyTestTenant is a tenant whose only retrieval pipeline is the
// keyword one — the shape a deployment has when no embedding store is bound.
func keywordOnlyTestTenant() *types.Tenant {
	return &types.Tenant{
		ID: 1,
		RetrieverEngines: types.RetrieverEngines{Engines: []types.RetrieverEngineParams{
			{
				RetrieverType:       types.KeywordsRetrieverType,
				RetrieverEngineType: types.PostgresRetrieverEngineType,
			},
		}},
	}
}

// keywordOnlyRetrieveEngine is the keyword pipeline. It records the embedder
// it was handed: the keyword path must not need one.
type keywordOnlyRetrieveEngine struct {
	interfaces.RetrieveEngineService
	indexed         []*types.IndexInfo
	embedder        embedding.Embedder
	retrieverTypes  []types.RetrieverType
	storageEstimate int64
}

func (e *keywordOnlyRetrieveEngine) EngineType() types.RetrieverEngineType {
	return types.PostgresRetrieverEngineType
}

func (e *keywordOnlyRetrieveEngine) Support() []types.RetrieverType {
	return []types.RetrieverType{types.KeywordsRetrieverType}
}

func (e *keywordOnlyRetrieveEngine) DeleteByKnowledgeIDList(
	context.Context, []string, int, string,
) error {
	return nil
}

func (e *keywordOnlyRetrieveEngine) EstimateStorageSize(
	_ context.Context, _ embedding.Embedder, _ []*types.IndexInfo, _ []types.RetrieverType,
) int64 {
	return e.storageEstimate
}

func (e *keywordOnlyRetrieveEngine) BatchIndex(
	_ context.Context,
	embedder embedding.Embedder,
	infos []*types.IndexInfo,
	retrieverTypes []types.RetrieverType,
) error {
	e.indexed = append([]*types.IndexInfo(nil), infos...)
	e.embedder = embedder
	e.retrieverTypes = append([]types.RetrieverType(nil), retrieverTypes...)
	return nil
}

// A knowledge base that only wants the keyword pipeline must get that
// pipeline's index even when it has no embedding model.
//
// The keyword pipeline stores no vectors: it needs the chunk text and
// nothing else. Refusing to index at all would turn "this document is not
// searchable by meaning" into "this document is not searchable at all", and
// every downstream step that needs a source (the generation run reads the
// sources of the bound assets through the keyword channel) would then have
// nothing to work with.
//
// This is the case that decides whether the generation path can run with
// zero embedding calls at all.
func TestProcessChunksBuildsTheKeywordIndexWithoutAnEmbeddingModel(t *testing.T) {
	knowledge := &types.Knowledge{
		ID:              "knowledge-1",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		ParseStatus:     types.ParseStatusProcessing,
	}
	chunkService := &embeddingDecoupledChunkService{}
	retrieveEngine := &keywordOnlyRetrieveEngine{storageEstimate: 42}
	modelService := &unavailableEmbeddingModelService{}
	ctx := context.WithValue(context.Background(), types.TenantInfoContextKey, keywordOnlyTestTenant())
	svc := &knowledgeService{
		repo:           &parentChildKnowledgeRepo{knowledge: knowledge},
		chunkRepo:      chunkService,
		modelService:   modelService,
		retrieveEngine: parentChildRetrieveRegistry{engine: retrieveEngine},
		graphEngine:    parentChildGraphRepo{},
		tenantRepo:     parentChildTenantRepo{},
		task:           parentChildTaskEnqueuer{},
	}
	kb := &types.KnowledgeBase{
		ID:       "kb-1",
		TenantID: 1,
		// The deployment state under test: the keyword pipeline only, and no
		// embedding model at all.
		IndexingStrategy: types.IndexingStrategy{KeywordEnabled: true},
	}

	svc.processChunks(ctx, kb, knowledge, embeddingDecoupledChunks())

	require.Len(t, chunkService.created, 2, "the chunks must be stored")
	require.Len(t, retrieveEngine.indexed, 2,
		"the keyword index must be written without an embedding model")
	require.Nil(t, retrieveEngine.embedder,
		"the keyword pipeline must not be handed an embedder")
	require.Equal(t, []types.RetrieverType{types.KeywordsRetrieverType}, retrieveEngine.retrieverTypes,
		"only the keyword pipeline may be routed")
	require.NotEqual(t, types.ParseStatusFailed, knowledge.ParseStatus,
		"a missing embedding model must not fail the parse")
	for _, chunk := range chunkService.created {
		require.Equal(t, "ready", chunk.IndexStatus,
			"a chunk that reached the only pipeline it needs is indexed")
	}
	require.Zero(t, chunkService.deletedAfterCreate,
		"the chunks must not be wiped after they were written")
}

// A knowledge base that wants BOTH pipelines and has no embedding model gets
// the keyword index and no vector index — and its chunks say that the
// vector pipeline is missing.
func TestProcessChunksKeepsTheKeywordPipelineWhenTheVectorOneIsUnavailable(t *testing.T) {
	knowledge := &types.Knowledge{
		ID:              "knowledge-1",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		ParseStatus:     types.ParseStatusProcessing,
	}
	chunkService := &embeddingDecoupledChunkService{}
	retrieveEngine := &keywordOnlyRetrieveEngine{}
	ctx := context.WithValue(context.Background(), types.TenantInfoContextKey, keywordOnlyTestTenant())
	svc := &knowledgeService{
		repo:           &parentChildKnowledgeRepo{knowledge: knowledge},
		chunkRepo:      chunkService,
		modelService:   &unavailableEmbeddingModelService{},
		retrieveEngine: parentChildRetrieveRegistry{engine: retrieveEngine},
		graphEngine:    parentChildGraphRepo{},
		tenantRepo:     parentChildTenantRepo{},
		task:           parentChildTaskEnqueuer{},
	}
	kb := &types.KnowledgeBase{
		ID:               "kb-1",
		TenantID:         1,
		EmbeddingModelID: "embedding-1",
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true, KeywordEnabled: true},
	}

	svc.processChunks(ctx, kb, knowledge, embeddingDecoupledChunks())

	require.Len(t, retrieveEngine.indexed, 2,
		"the keyword pipeline must still be indexed")
	require.Equal(t, []types.RetrieverType{types.KeywordsRetrieverType}, retrieveEngine.retrieverTypes,
		"the vector pipeline must not be routed without an embedder")
	for _, chunk := range chunkService.created {
		require.Equal(t, "failed", chunk.IndexStatus,
			"a chunk that did not reach the wanted vector pipeline must say so")
	}
}

func embeddingDecoupledChunks() []types.ParsedChunk {
	return []types.ParsedChunk{
		{Content: "第一段正文", Seq: 0, Start: 0, End: 5},
		{Content: "第二段正文", Seq: 1, Start: 5, End: 10},
	}
}

func TestProcessChunksPersistsChunksWhenEmbeddingModelIsUnavailable(t *testing.T) {
	knowledge := &types.Knowledge{
		ID:              "knowledge-1",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		ParseStatus:     types.ParseStatusProcessing,
	}
	chunkService := &embeddingDecoupledChunkService{}
	retrieveEngine := &parentChildRetrieveEngine{}
	modelService := &unavailableEmbeddingModelService{}
	ctx := context.WithValue(context.Background(), types.TenantInfoContextKey, embeddingDecoupledTestTenant())
	svc := &knowledgeService{
		repo:           &parentChildKnowledgeRepo{knowledge: knowledge},
		chunkRepo:      chunkService,
		modelService:   modelService,
		retrieveEngine: parentChildRetrieveRegistry{engine: retrieveEngine},
		graphEngine:    parentChildGraphRepo{},
		tenantRepo:     parentChildTenantRepo{},
		task:           parentChildTaskEnqueuer{},
	}
	kb := &types.KnowledgeBase{
		ID:       "kb-1",
		TenantID: 1,
		// No embedding model configured, but the knowledge base still asks
		// for an index: that is the deployment state, not the fix.
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true, KeywordEnabled: true},
	}

	svc.processChunks(ctx, kb, knowledge, embeddingDecoupledChunks())

	require.Len(t, chunkService.created, 2, "the chunks must be stored even without an embedding model")
	require.Empty(t, retrieveEngine.indexed, "no index may be written without an embedding model")
	require.NotEqual(t, types.ParseStatusFailed, knowledge.ParseStatus,
		"a missing embedding model must not fail the parse")
	for _, chunk := range chunkService.created {
		require.Equal(t, "failed", chunk.IndexStatus,
			"a chunk that was never indexed must not claim to be ready")
	}
}

func TestProcessChunksKeepsChunksWhenTheIndexWriteFails(t *testing.T) {
	knowledge := &types.Knowledge{
		ID:              "knowledge-1",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		ParseStatus:     types.ParseStatusProcessing,
	}
	chunkService := &embeddingDecoupledChunkService{}
	retrieveEngine := &failingIndexRetrieveEngine{}
	ctx := context.WithValue(context.Background(), types.TenantInfoContextKey, embeddingDecoupledTestTenant())
	svc := &knowledgeService{
		repo:           &parentChildKnowledgeRepo{knowledge: knowledge},
		chunkRepo:      chunkService,
		modelService:   parentChildModelService{embedder: parentChildEmbedder{}},
		retrieveEngine: parentChildRetrieveRegistry{engine: retrieveEngine},
		graphEngine:    parentChildGraphRepo{},
		tenantRepo:     parentChildTenantRepo{},
		task:           parentChildTaskEnqueuer{},
	}
	kb := &types.KnowledgeBase{
		ID:               "kb-1",
		TenantID:         1,
		EmbeddingModelID: "embedding-1",
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true},
	}

	svc.processChunks(ctx, kb, knowledge, embeddingDecoupledChunks())

	require.Len(t, chunkService.created, 2, "the chunks must survive a failed index write")
	require.NotEmpty(t, retrieveEngine.indexed, "the index write was attempted")
	require.NotEqual(t, types.ParseStatusFailed, knowledge.ParseStatus,
		"a failed index write must not fail the parse")
	require.Zero(t, chunkService.deletedAfterCreate,
		"the chunks must not be wiped after they were written")
	for _, chunk := range chunkService.created {
		require.Equal(t, "failed", chunk.IndexStatus,
			"a chunk that is not in the index must not claim to be ready")
	}
	require.Len(t, chunkService.updated, 2, "every chunk must be marked as not indexed")
}
