package service

import (
	"testing"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// embedding_model_id must point at an embedding-capable model.
//
// This is the pair of the summary_model_id guard
// (knowledgebase_summary_model_guard_test.go): that one guards the
// text-generation slot, this one guards the embedding slot, and both take the
// verdict from the model's TYPE, never from its name.
//
// Binding a KnowledgeQA / Rerank / ASR model as embedding_model_id is not a
// no-op: both the ingestion side (chunk embedding) and the retrieval side
// (query embedding) then call the embeddings endpoint of a model that does
// not serve it. The provider rejects those calls (HTTP 400, no vector), so
// the knowledge base looks healthy while every document and every query
// fails to vectorise.
//
// These are pure logic tests that drive CreateKnowledgeBase through stubbed
// dependencies — no DB I/O and no model API calls.
// ---------------------------------------------------------------------------

func TestCreateKnowledgeBaseRejectsChatModelAsEmbeddingModel(t *testing.T) {
	svc, repo := newSummaryGuardKBService(map[string]*types.Model{
		"chat-1": {
			ID:   "chat-1",
			Name: "qwen-plus",
			Type: types.ModelTypeKnowledgeQA,
		},
	})

	_, err := svc.CreateKnowledgeBase(ctxWithTenant(1), &types.KnowledgeBase{
		Name:             "kb",
		EmbeddingModelID: "chat-1",
	})

	require.Error(t, err)
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok)
	t.Logf("rejection message: %s", appErr.Message)
	assert.Equal(t, apperrors.ErrBadRequest, appErr.Code)
	assert.Contains(t, appErr.Message, "KnowledgeQA")
	assert.Contains(t, appErr.Message, "embedding")
	assert.Empty(t, repo.rows, "knowledge base must not be persisted when the embedding model is invalid")
}

func TestCreateKnowledgeBaseRejectsNonEmbeddingModelTypes(t *testing.T) {
	cases := []struct {
		name      string
		modelType types.ModelType
	}{
		{name: "rerank", modelType: types.ModelTypeRerank},
		{name: "asr", modelType: types.ModelTypeASR},
		{name: "vlm", modelType: types.ModelTypeVLLM},
		{name: "empty type", modelType: types.ModelType("")},
		{name: "unknown type", modelType: types.ModelType("Chat")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc, repo := newSummaryGuardKBService(map[string]*types.Model{
				"m-1": {ID: "m-1", Type: c.modelType},
			})

			_, err := svc.CreateKnowledgeBase(ctxWithTenant(1), &types.KnowledgeBase{
				Name:             "kb",
				EmbeddingModelID: "m-1",
			})

			require.Error(t, err)
			appErr, ok := apperrors.IsAppError(err)
			require.True(t, ok)
			assert.Equal(t, apperrors.ErrBadRequest, appErr.Code)
			assert.Empty(t, repo.rows, "knowledge base must not be persisted when the embedding model is invalid")
		})
	}
}

func TestCreateKnowledgeBaseAllowsEmbeddingModelAsEmbeddingModel(t *testing.T) {
	svc, repo := newSummaryGuardKBService(map[string]*types.Model{
		"embed-1": {ID: "embed-1", Type: types.ModelTypeEmbedding},
	})

	kb, err := svc.CreateKnowledgeBase(ctxWithTenant(1), &types.KnowledgeBase{
		Name:             "kb",
		EmbeddingModelID: "embed-1",
	})

	require.NoError(t, err)
	assert.Equal(t, "embed-1", kb.EmbeddingModelID)
	assert.Len(t, repo.rows, 1, "knowledge base must be persisted for a valid embedding model")
}

func TestCreateKnowledgeBaseAllowsEmptyEmbeddingModel(t *testing.T) {
	// An empty embedding_model_id means "this knowledge base has no embedding
	// stage" — it must stay allowed, otherwise the guard would block every
	// knowledge base that has not picked an embedding model yet.
	svc, repo := newSummaryGuardKBService(map[string]*types.Model{})

	kb, err := svc.CreateKnowledgeBase(ctxWithTenant(1), &types.KnowledgeBase{Name: "kb"})

	require.NoError(t, err)
	assert.Empty(t, kb.EmbeddingModelID)
	assert.Len(t, repo.rows, 1)
}

func TestCreateKnowledgeBaseKeepsExistingBehaviourForUnknownEmbeddingModel(t *testing.T) {
	// A model id that does not resolve is NOT rejected here: the existing
	// behaviour already surfaces that mistake loudly at ingestion time
	// (ErrModelNotFound), and turning it into a configuration-time rejection
	// would be a second, unrelated contract change. This guard only speaks
	// about the TYPE of a row that does resolve.
	svc, repo := newSummaryGuardKBService(map[string]*types.Model{})

	kb, err := svc.CreateKnowledgeBase(ctxWithTenant(1), &types.KnowledgeBase{
		Name:             "kb",
		EmbeddingModelID: "does-not-exist",
	})

	require.NoError(t, err)
	assert.Equal(t, "does-not-exist", kb.EmbeddingModelID)
	assert.Len(t, repo.rows, 1)
}

func TestCreateKnowledgeBaseEmbeddingModelGuardUsesTypeNotName(t *testing.T) {
	// A chat-named model of KnowledgeQA type is still a chat model: the guard
	// must follow the type, otherwise a rename would slip through.
	svc, _ := newSummaryGuardKBService(map[string]*types.Model{
		"looks-like-embed": {ID: "looks-like-embed", Name: "text-embedding-3-small",
			Type: types.ModelTypeKnowledgeQA},
	})

	_, err := svc.CreateKnowledgeBase(ctxWithTenant(1), &types.KnowledgeBase{
		Name:             "kb",
		EmbeddingModelID: "looks-like-embed",
	})
	require.Error(t, err)

	// An embedding-named model of Embedding type is an embedding model: it
	// must be accepted.
	svc, repo := newSummaryGuardKBService(map[string]*types.Model{
		"looks-like-chat": {ID: "looks-like-chat", Name: "gpt-4o", Type: types.ModelTypeEmbedding},
	})

	_, err = svc.CreateKnowledgeBase(ctxWithTenant(1), &types.KnowledgeBase{
		Name:             "kb",
		EmbeddingModelID: "looks-like-chat",
	})
	require.NoError(t, err)
	assert.Len(t, repo.rows, 1)
}
