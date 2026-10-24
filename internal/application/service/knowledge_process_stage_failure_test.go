package service

import (
	"context"
	"testing"

	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// The embedding stage must record the REAL reason it could not index.
//
// The stage row carries one error code. Two different failures used to write
// to it, and the later one won: a knowledge base whose keyword pipeline had
// no engine bound was reported as "the embedding model is unavailable" —
// which sends the operator to fix a model that was never the problem.
//
// The tracker is the seam where the record is written, so these cases read it
// there: what was written, and how many times.
// ---------------------------------------------------------------------------

// recordingSpanTracker records what the pipeline wrote to the stage.
//
// It embeds the no-op tracker so the cases only have to say what they care
// about: the stage's begin, the stage's failure and the stage's end.
type recordingSpanTracker struct {
	noopSpanTracker
	span      *Span
	failures  []recordedFailure
	ended     int
	skipped   int
	beginCall int
}

type recordedFailure struct {
	code    string
	message string
	detail  error
}

func (t *recordingSpanTracker) BeginStage(_ context.Context, knowledgeID string, attempt int, stage string, _ types.JSONMap) *Span {
	t.beginCall++
	if t.span == nil {
		t.span = &Span{KnowledgeID: knowledgeID, Attempt: attempt, SpanID: stage, Name: stage}
	}
	return t.span
}

func (t *recordingSpanTracker) LookupStage(_ context.Context, _ string, _ int, _ string) *Span {
	return t.span
}

func (t *recordingSpanTracker) FailSpan(_ context.Context, _ *Span, errorCode, errorMessage string, errorDetail error) {
	t.failures = append(t.failures, recordedFailure{code: errorCode, message: errorMessage, detail: errorDetail})
}

func (t *recordingSpanTracker) EndSpan(_ context.Context, _ *Span, _ types.JSONMap) { t.ended++ }

func (t *recordingSpanTracker) SkipSpan(_ context.Context, _ *Span, _ string) { t.skipped++ }

// keywordOnlyTenantNoEngine is a tenant whose engine supports only the
// vector pipeline: a knowledge base that wants the keyword pipeline has no
// engine to serve it.
func keywordOnlyTenantNoEngine() *types.Tenant {
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

// The routing failure is recorded AS the routing failure: the code says the
// store could not serve the pipeline, and the detail carries the routing
// error. It must not be reported as a missing embedding model.
func TestTheEmbeddingStageRecordsTheRoutingFailureAsTheRoutingFailure(t *testing.T) {
	tracker := &recordingSpanTracker{}
	knowledge := &types.Knowledge{ID: "k-1", TenantID: 1, KnowledgeBaseID: "kb-1", ParseStatus: types.ParseStatusProcessing}
	ctx := withAttempt(context.WithValue(context.Background(), types.TenantInfoContextKey, keywordOnlyTenantNoEngine()), 1)
	svc := &knowledgeService{
		repo:         &parentChildKnowledgeRepo{knowledge: knowledge},
		chunkRepo:    &embeddingDecoupledChunkService{},
		modelService: parentChildModelService{embedder: parentChildEmbedder{}},
		retrieveEngine: parentChildRetrieveRegistry{
			engine: &parentChildRetrieveEngine{}, // supports the vector pipeline only
		},
		graphEngine: parentChildGraphRepo{},
		tenantRepo:  parentChildTenantRepo{},
		task:        parentChildTaskEnqueuer{},
		spanTracker: tracker,
	}
	kb := &types.KnowledgeBase{
		ID:       "kb-1",
		TenantID: 1,
		// The keyword pipeline is wanted, but no engine serves it.
		IndexingStrategy: types.IndexingStrategy{KeywordEnabled: true},
	}

	svc.processChunks(ctx, kb, knowledge, embeddingDecoupledChunks())

	require.Len(t, tracker.failures, 1, "the stage must record exactly one failure")
	failure := tracker.failures[0]
	require.Equal(t, werrors.ErrCodeVectorStoreWriteFailed, failure.code,
		"a routing failure must not be reported as a missing embedding model")
	require.Contains(t, failure.message, "no retriever engine")
	require.Error(t, failure.detail,
		"the routing error must be carried as the detail, not dropped")
}

// The missing embedding model is recorded as the missing embedding model.
//
// It is a different failure with a different fix, and the two must not
// overwrite each other.
func TestTheEmbeddingStageRecordsTheMissingEmbeddingModelAsSuch(t *testing.T) {
	tracker := &recordingSpanTracker{}
	knowledge := &types.Knowledge{ID: "k-1", TenantID: 1, KnowledgeBaseID: "kb-1", ParseStatus: types.ParseStatusProcessing}
	ctx := withAttempt(context.WithValue(context.Background(), types.TenantInfoContextKey, keywordOnlyTenantNoEngine()), 1)
	svc := &knowledgeService{
		repo:         &parentChildKnowledgeRepo{knowledge: knowledge},
		chunkRepo:    &embeddingDecoupledChunkService{},
		modelService: &unavailableEmbeddingModelService{},
		retrieveEngine: parentChildRetrieveRegistry{
			engine: &parentChildRetrieveEngine{},
		},
		graphEngine: parentChildGraphRepo{},
		tenantRepo:  parentChildTenantRepo{},
		task:        parentChildTaskEnqueuer{},
		spanTracker: tracker,
	}
	kb := &types.KnowledgeBase{
		ID:       "kb-1",
		TenantID: 1,
		// Only the vector pipeline is wanted, and there is no embedder for it.
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true},
	}

	svc.processChunks(ctx, kb, knowledge, embeddingDecoupledChunks())

	require.Len(t, tracker.failures, 1, "the stage must record exactly one failure")
	failure := tracker.failures[0]
	require.Equal(t, werrors.ErrCodeEmbeddingModelUnavailable, failure.code)
	require.Contains(t, failure.message, "embedding model unavailable")
	require.Error(t, failure.detail,
		"the embedding error must be carried as the detail, not dropped")
	require.Contains(t, failure.detail.Error(), "model ID",
		"the detail must be the embedding error itself: %v", failure.detail)
}

// The routing failure and the missing embedding model can happen AT THE SAME
// TIME. The record must still carry the real cause — the routing — and the
// detail must not be empty: "the embedding model is missing" would send the
// operator to fix a model that was never the problem.
func TestTheRoutingCauseWinsWhenBothCausesHappen(t *testing.T) {
	tracker := &recordingSpanTracker{}
	knowledge := &types.Knowledge{ID: "k-1", TenantID: 1, KnowledgeBaseID: "kb-1", ParseStatus: types.ParseStatusProcessing}
	ctx := withAttempt(context.WithValue(context.Background(), types.TenantInfoContextKey, keywordOnlyTenantNoEngine()), 1)
	svc := &knowledgeService{
		repo:         &parentChildKnowledgeRepo{knowledge: knowledge},
		chunkRepo:    &embeddingDecoupledChunkService{},
		modelService: &unavailableEmbeddingModelService{}, // the embedder is missing TOO
		retrieveEngine: parentChildRetrieveRegistry{
			engine: &parentChildRetrieveEngine{}, // and no engine serves the keyword pipeline
		},
		graphEngine: parentChildGraphRepo{},
		tenantRepo:  parentChildTenantRepo{},
		task:        parentChildTaskEnqueuer{},
		spanTracker: tracker,
	}
	kb := &types.KnowledgeBase{
		ID:       "kb-1",
		TenantID: 1,
		// Both pipelines are wanted; there is no embedder for the vector one
		// and no engine for the keyword one.
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true, KeywordEnabled: true},
	}

	svc.processChunks(ctx, kb, knowledge, embeddingDecoupledChunks())

	require.Len(t, tracker.failures, 1, "the stage must record exactly one failure")
	failure := tracker.failures[0]
	require.Equal(t, werrors.ErrCodeVectorStoreWriteFailed, failure.code,
		"the routing cause must not be overwritten by the embedding cause")
	require.Contains(t, failure.message, "no retriever engine")
	require.Error(t, failure.detail, "the detail must not be empty")
	require.Contains(t, failure.detail.Error(), "retriever",
		"the detail must be the routing error itself: %v", failure.detail)
}

// The embedding model service must never be asked for a model when the
// knowledge base wants no embedding pipeline at all: that lookup is a call
// into the model layer, and the model layer is where a provider would be
// reached.
func TestNoEmbeddingModelIsLookedUpWhenNoPipelineNeedsOne(t *testing.T) {
	tracker := &recordingSpanTracker{}
	knowledge := &types.Knowledge{ID: "k-1", TenantID: 1, KnowledgeBaseID: "kb-1", ParseStatus: types.ParseStatusProcessing}
	ctx := withAttempt(context.WithValue(context.Background(), types.TenantInfoContextKey, keywordOnlyTenantNoEngine()), 1)
	modelService := &unavailableEmbeddingModelService{}
	svc := &knowledgeService{
		repo:         &parentChildKnowledgeRepo{knowledge: knowledge},
		chunkRepo:    &embeddingDecoupledChunkService{},
		modelService: modelService,
		retrieveEngine: parentChildRetrieveRegistry{
			engine: &keywordOnlyRetrieveEngine{},
		},
		graphEngine: parentChildGraphRepo{},
		tenantRepo:  parentChildTenantRepo{},
		task:        parentChildTaskEnqueuer{},
		spanTracker: tracker,
	}
	kb := &types.KnowledgeBase{
		ID:       "kb-1",
		TenantID: 1,
		// No pipeline at all: the embedding stage is skipped entirely.
		IndexingStrategy: types.IndexingStrategy{},
	}

	svc.processChunks(ctx, kb, knowledge, embeddingDecoupledChunks())

	require.Zero(t, modelService.calls,
		"a knowledge base that wants no embedding pipeline must not ask the model layer for one")
	require.Empty(t, tracker.failures, "a skipped stage records no failure")
}

var _ embedding.Embedder = parentChildEmbedder{}
