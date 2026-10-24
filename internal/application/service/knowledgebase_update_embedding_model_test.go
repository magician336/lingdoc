package service

import (
	"testing"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// The knowledge base update endpoint must not swallow embedding_model_id.
//
// The endpoint used to accept the field and drop it: the caller got 200 and
// an empty value back on the next read, so "the model was set" and "the field
// was ignored" looked the same from the outside (#485 ran into exactly this:
// it configured the model, got 200, and the next read still said empty).
//
// The rules pinned here:
//   - a value in the payload is persisted;
//   - an absent value keeps the current one (the update is not a reset);
//   - the empty string clears it (that is what "no embedding stage" means);
//   - the same guard as the create path applies — a chat model is refused
//     instead of being written, because every ingest and every query would
//     then call an endpoint that does not serve embeddings.
//
// These are pure logic tests driven through stubbed dependencies — no DB I/O
// and no model API calls.
// ---------------------------------------------------------------------------

func newUpdateGuardKBService(
	models map[string]*types.Model,
) (*knowledgeBaseService, *fakeKBRepo) {
	svc, repo := newSummaryGuardKBService(models)
	repo.rows["kb-1"] = &types.KnowledgeBase{ID: "kb-1", TenantID: 1, Name: "kb"}
	return svc, repo
}

func TestUpdateKnowledgeBasePersistsEmbeddingModelID(t *testing.T) {
	svc, repo := newUpdateGuardKBService(map[string]*types.Model{
		"embed-1": {ID: "embed-1", Type: types.ModelTypeEmbedding},
	})
	modelID := "embed-1"

	kb, err := svc.UpdateKnowledgeBase(ctxWithTenant(1), "kb-1", "kb", "d", nil, &modelID)

	require.NoError(t, err)
	assert.Equal(t, "embed-1", kb.EmbeddingModelID)
	assert.Equal(t, "embed-1", repo.rows["kb-1"].EmbeddingModelID,
		"the embedding model must be persisted, not dropped")
}

func TestUpdateKnowledgeBaseKeepsEmbeddingModelWhenAbsent(t *testing.T) {
	svc, repo := newUpdateGuardKBService(map[string]*types.Model{
		"embed-1": {ID: "embed-1", Type: types.ModelTypeEmbedding},
	})
	repo.rows["kb-1"].EmbeddingModelID = "embed-1"

	kb, err := svc.UpdateKnowledgeBase(ctxWithTenant(1), "kb-1", "kb", "d", nil, nil)

	require.NoError(t, err)
	assert.Equal(t, "embed-1", kb.EmbeddingModelID,
		"an update that does not mention the embedding model must keep the current one")
}

func TestUpdateKnowledgeBasePersistsEmbeddingModelFromConfig(t *testing.T) {
	// Clients that put the whole configuration under "config" get the same
	// treatment: the field is read from there too.
	svc, repo := newUpdateGuardKBService(map[string]*types.Model{
		"embed-1": {ID: "embed-1", Type: types.ModelTypeEmbedding},
	})
	modelID := "embed-1"

	kb, err := svc.UpdateKnowledgeBase(ctxWithTenant(1), "kb-1", "kb", "d",
		&types.KnowledgeBaseConfig{EmbeddingModelID: &modelID}, nil)

	require.NoError(t, err)
	assert.Equal(t, "embed-1", kb.EmbeddingModelID)
	assert.Equal(t, "embed-1", repo.rows["kb-1"].EmbeddingModelID)
}

func TestUpdateKnowledgeBaseExplicitEmbeddingModelWinsOverConfig(t *testing.T) {
	svc, repo := newUpdateGuardKBService(map[string]*types.Model{
		"embed-1": {ID: "embed-1", Type: types.ModelTypeEmbedding},
		"embed-2": {ID: "embed-2", Type: types.ModelTypeEmbedding},
	})
	fromConfig := "embed-1"
	explicit := "embed-2"

	kb, err := svc.UpdateKnowledgeBase(ctxWithTenant(1), "kb-1", "kb", "d",
		&types.KnowledgeBaseConfig{EmbeddingModelID: &fromConfig}, &explicit)

	require.NoError(t, err)
	assert.Equal(t, "embed-2", kb.EmbeddingModelID)
	assert.Equal(t, "embed-2", repo.rows["kb-1"].EmbeddingModelID)
}

func TestUpdateKnowledgeBaseClearsEmbeddingModelWithEmptyString(t *testing.T) {
	// The empty string means "this knowledge base has no embedding stage".
	// It is a value, so it is written — it is not the same as "not in the
	// payload" (which keeps the current value).
	svc, repo := newUpdateGuardKBService(map[string]*types.Model{})
	repo.rows["kb-1"].EmbeddingModelID = "embed-1"
	empty := ""

	kb, err := svc.UpdateKnowledgeBase(ctxWithTenant(1), "kb-1", "kb", "d", nil, &empty)

	require.NoError(t, err)
	assert.Empty(t, kb.EmbeddingModelID)
	assert.Empty(t, repo.rows["kb-1"].EmbeddingModelID)
}

func TestUpdateKnowledgeBaseRejectsChatModelAsEmbeddingModel(t *testing.T) {
	// The create path already refuses this; the update path must not become
	// a way around it now that the field is actually persisted.
	svc, repo := newUpdateGuardKBService(map[string]*types.Model{
		"chat-1": {ID: "chat-1", Name: "qwen-plus", Type: types.ModelTypeKnowledgeQA},
	})
	modelID := "chat-1"

	_, err := svc.UpdateKnowledgeBase(ctxWithTenant(1), "kb-1", "kb", "d", nil, &modelID)

	require.Error(t, err)
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok)
	assert.Equal(t, apperrors.ErrBadRequest, appErr.Code)
	assert.Contains(t, appErr.Message, "embedding")
	assert.Empty(t, repo.rows["kb-1"].EmbeddingModelID,
		"a refused embedding model must not be written")
}
