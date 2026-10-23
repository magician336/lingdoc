package service

import (
	"testing"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// summary_model_id must point at a text-generation (chat) model.
//
// Binding an Embedding model to summary_model_id is not a no-op: the document
// pipeline derives a summary sub-task from it and calls the chat endpoint of
// a non-chat model. The provider rejects those calls (HTTP 400, no output,
// no tokens billed), so the knowledge base looks healthy while every
// document silently loses its summary.
//
// The verdict is taken from the model TYPE, never from the model name — a
// name check would be bypassed by renaming the model.
//
// These are pure logic tests that drive CreateKnowledgeBase through stubbed
// dependencies — no DB I/O and no model API calls.
// ---------------------------------------------------------------------------

// newSummaryGuardKBService builds a knowledge base service whose model
// lookups are answered from a fixed map.
func newSummaryGuardKBService(
	models map[string]*types.Model,
) (*knowledgeBaseService, *fakeKBRepo) {
	repo := newFakeKBRepo()
	return &knowledgeBaseService{
		repo:         repo,
		modelService: &stubModelService{modelsByID: models},
	}, repo
}

func TestCreateKnowledgeBaseRejectsEmbeddingModelAsSummaryModel(t *testing.T) {
	svc, repo := newSummaryGuardKBService(map[string]*types.Model{
		"embed-1": {
			ID:   "embed-1",
			Name: "qwen3.7-text-embedding-flash",
			Type: types.ModelTypeEmbedding,
		},
	})

	_, err := svc.CreateKnowledgeBase(ctxWithTenant(1), &types.KnowledgeBase{
		Name:           "kb",
		SummaryModelID: "embed-1",
	})

	require.Error(t, err)
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok)
	t.Logf("rejection message: %s", appErr.Message)
	assert.Equal(t, apperrors.ErrBadRequest, appErr.Code)
	assert.Contains(t, appErr.Message, "Embedding")
	assert.Contains(t, appErr.Message, "text-generation")
	assert.Empty(t, repo.rows, "knowledge base must not be persisted when the summary model is invalid")
}

func TestCreateKnowledgeBaseRejectsRerankModelAsSummaryModel(t *testing.T) {
	svc, repo := newSummaryGuardKBService(map[string]*types.Model{
		"rerank-1": {ID: "rerank-1", Type: types.ModelTypeRerank},
	})

	_, err := svc.CreateKnowledgeBase(ctxWithTenant(1), &types.KnowledgeBase{
		Name:           "kb",
		SummaryModelID: "rerank-1",
	})

	require.Error(t, err)
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok)
	assert.Equal(t, apperrors.ErrBadRequest, appErr.Code)
	assert.Contains(t, appErr.Message, "Rerank")
	assert.Empty(t, repo.rows, "knowledge base must not be persisted when the summary model is invalid")
}

func TestCreateKnowledgeBaseRejectsUnknownSummaryModel(t *testing.T) {
	svc, repo := newSummaryGuardKBService(map[string]*types.Model{})

	_, err := svc.CreateKnowledgeBase(ctxWithTenant(1), &types.KnowledgeBase{
		Name:           "kb",
		SummaryModelID: "does-not-exist",
	})

	require.Error(t, err)
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok)
	assert.Equal(t, apperrors.ErrBadRequest, appErr.Code)
	assert.Contains(t, appErr.Message, "does not exist")
	assert.Empty(t, repo.rows, "knowledge base must not be persisted when the summary model is unknown")
}

func TestCreateKnowledgeBaseAllowsChatModelAsSummaryModel(t *testing.T) {
	svc, repo := newSummaryGuardKBService(map[string]*types.Model{
		"chat-1": {ID: "chat-1", Type: types.ModelTypeKnowledgeQA},
	})

	kb, err := svc.CreateKnowledgeBase(ctxWithTenant(1), &types.KnowledgeBase{
		Name:           "kb",
		SummaryModelID: "chat-1",
	})

	require.NoError(t, err)
	assert.Equal(t, "chat-1", kb.SummaryModelID)
	assert.Len(t, repo.rows, 1, "knowledge base must be persisted for a valid summary model")
}

func TestCreateKnowledgeBaseAllowsEmptySummaryModel(t *testing.T) {
	// An empty summary_model_id means "this knowledge base has no summary
	// stage" — it must stay allowed, otherwise the guard would block every
	// knowledge base that has not picked a summary model yet.
	svc, repo := newSummaryGuardKBService(map[string]*types.Model{})

	kb, err := svc.CreateKnowledgeBase(ctxWithTenant(1), &types.KnowledgeBase{Name: "kb"})

	require.NoError(t, err)
	assert.Empty(t, kb.SummaryModelID)
	assert.Len(t, repo.rows, 1)
}

func TestCreateKnowledgeBaseSummaryModelGuardUsesTypeNotName(t *testing.T) {
	// A chat-named model of Embedding type is still an embedding model: the
	// guard must follow the type, otherwise a rename would slip through.
	svc, _ := newSummaryGuardKBService(map[string]*types.Model{
		"looks-like-chat": {ID: "looks-like-chat", Name: "gpt-4o", Type: types.ModelTypeEmbedding},
	})

	_, err := svc.CreateKnowledgeBase(ctxWithTenant(1), &types.KnowledgeBase{
		Name:           "kb",
		SummaryModelID: "looks-like-chat",
	})
	require.Error(t, err)

	// An embedding-named model of KnowledgeQA type is a chat model: it must
	// be accepted.
	svc, repo := newSummaryGuardKBService(map[string]*types.Model{
		"looks-like-embed": {ID: "looks-like-embed", Name: "text-embedding-3-small", Type: types.ModelTypeKnowledgeQA},
	})

	_, err = svc.CreateKnowledgeBase(ctxWithTenant(1), &types.KnowledgeBase{
		Name:           "kb",
		SummaryModelID: "looks-like-embed",
	})
	require.NoError(t, err)
	assert.Len(t, repo.rows, 1)
}
