package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// #599: "parsed but not indexed" must be visible on the knowledge row.
//
// The chunk-level index_status=failed and the failed stage record only answer
// the question for readers that know to look at the chunks table (or the span
// tree). Every reader that only sees the knowledge row — list views, detail
// views, ops panels, anything filtering on parse_status — is told "completed"
// for a document that never reached the index: "upload succeeded but search
// finds nothing", with no signal on the row itself.
//
// The guards below pin BOTH sides of the contract, and neither may be relaxed:
//
//   - the knowledge row carries a queryable, non-fatal marker
//     (IndexStatus=failed) that mirrors the chunk-level verdict of the same
//     parse run — the same fact, one layer up;
//   - the parse itself still reads as a success: a missing index is not a
//     parse failure, and the re-parse recovery path depends on that.
// ---------------------------------------------------------------------------

// guardPayloadIsVisibleOnTheRow asserts the marker survives the exact
// serialization the knowledge detail/list endpoints ship (the handler returns
// the *types.Knowledge struct as-is), so the signal cannot be silently
// dropped from the API surface while the internal field survives.
func guardPayloadIsVisibleOnTheRow(t *testing.T, knowledge *types.Knowledge) {
	t.Helper()
	payload, err := json.Marshal(knowledge)
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(payload, &fields))
	require.Contains(t, fields, "index_status",
		"the knowledge row must expose index_status to API readers")
	require.JSONEq(t, `"failed"`, string(fields["index_status"]),
		"the knowledge row must carry the failed marker")
}

// A parse whose index write fails must leave the failure visible on the
// knowledge row itself — not only on the chunks and the stage records.
func TestKnowledgeRowCarriesTheMarkerWhenTheIndexWriteFails(t *testing.T) {
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

	// The marker: visible on the row, queryable without joining chunks.
	require.Equal(t, types.KnowledgeIndexStatusFailed, knowledge.IndexStatus,
		"a knowledge row whose index write failed must say so")
	guardPayloadIsVisibleOnTheRow(t, knowledge)
	// The parse itself still reads as a success: the row stays on the
	// success trajectory (processing → finalizing → completed via
	// post-process) and must not be re-graded as failed.
	require.Equal(t, types.ParseStatusProcessing, knowledge.ParseStatus,
		"an index write failure must not turn the successful parse into a failed one")
}

// A run that never reaches the index because no usable embedding model (or
// engine) exists must mark the row the same way — same fact, two layers:
// the chunks are created with index_status=failed, and the row mirrors it.
func TestKnowledgeRowCarriesTheMarkerWhenNoIndexCouldBeBuilt(t *testing.T) {
	knowledge := &types.Knowledge{
		ID:              "knowledge-1",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		ParseStatus:     types.ParseStatusProcessing,
	}
	chunkService := &embeddingDecoupledChunkService{}
	ctx := context.WithValue(context.Background(), types.TenantInfoContextKey, embeddingDecoupledTestTenant())
	svc := &knowledgeService{
		repo:           &parentChildKnowledgeRepo{knowledge: knowledge},
		chunkRepo:      chunkService,
		modelService:   &unavailableEmbeddingModelService{},
		retrieveEngine: parentChildRetrieveRegistry{engine: &parentChildRetrieveEngine{}},
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

	for _, chunk := range chunkService.created {
		require.Equal(t, "failed", chunk.IndexStatus,
			"a chunk that was never indexed must say so (chunk layer)")
	}
	require.Equal(t, types.KnowledgeIndexStatusFailed, knowledge.IndexStatus,
		"the knowledge row must mirror the chunk-level failure (row layer)")
	guardPayloadIsVisibleOnTheRow(t, knowledge)
	require.NotEqual(t, types.ParseStatusFailed, knowledge.ParseStatus,
		"a missing embedding model must not fail the parse")
}

// The marker must not be a stuck-on "failed": a run whose index write
// succeeds stamps the row ready, so a re-parse after a failure recovers the
// row-level verdict exactly like it recovers the chunk-level one.
func TestKnowledgeRowRecoversToReadyWhenAReparseIndexesSuccessfully(t *testing.T) {
	knowledge := &types.Knowledge{
		ID:              "knowledge-1",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		ParseStatus:     types.ParseStatusProcessing,
		// A previous run failed to build the index (the marker under test).
		IndexStatus: types.KnowledgeIndexStatusFailed,
	}
	chunkService := &embeddingDecoupledChunkService{}
	retrieveEngine := &keywordOnlyRetrieveEngine{}
	ctx := context.WithValue(context.Background(), types.TenantInfoContextKey, keywordOnlyTestTenant())
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
		IndexingStrategy: types.IndexingStrategy{KeywordEnabled: true},
	}

	svc.processChunks(ctx, kb, knowledge, embeddingDecoupledChunks())

	for _, chunk := range chunkService.created {
		require.Equal(t, "ready", chunk.IndexStatus, "the chunks were indexed")
	}
	require.Equal(t, types.KnowledgeIndexStatusReady, knowledge.IndexStatus,
		"a successful re-index must clear the row-level marker")
	require.NotEqual(t, types.ParseStatusFailed, knowledge.ParseStatus,
		"the parse must still read as a success")
}
