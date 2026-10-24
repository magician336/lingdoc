package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// The embedding model of a knowledge base must survive a write.
//
// The update endpoint used to accept embedding_model_id and drop it: the
// caller got 200 and an empty value back on the next read, so "the model was
// set" and "the field was ignored" looked the same from the outside (#485).
//
// The service-level test proves the service ASKS the repository to save the
// value; this one proves the repository actually writes the COLUMN and reads
// it back — the last link between the endpoint and the stored value.
// ---------------------------------------------------------------------------

func TestKnowledgeBaseEmbeddingModelIDRoundTripsThroughTheDatabase(t *testing.T) {
	db := setupKBTestDB(t)
	repo := NewKnowledgeBaseRepository(db)
	ctx := context.Background()

	kb := &types.KnowledgeBase{
		ID:       "kb-1",
		Name:     "kb",
		TenantID: 1,
		Type:     types.KnowledgeBaseTypeDocument,
	}
	require.NoError(t, repo.CreateKnowledgeBase(ctx, kb))

	// The service writes the model id and asks the repository to save it.
	kb.EmbeddingModelID = "embed-1"
	require.NoError(t, repo.UpdateKnowledgeBase(ctx, kb))

	stored, err := repo.GetKnowledgeBaseByID(ctx, "kb-1")
	require.NoError(t, err)
	require.Equal(t, "embed-1", stored.EmbeddingModelID,
		"the embedding model must be readable back from the database")

	// The empty string is a value too: it is how a knowledge base says "this
	// one has no embedding stage". It must be readable back as the empty
	// string, not as the old value.
	kb.EmbeddingModelID = ""
	require.NoError(t, repo.UpdateKnowledgeBase(ctx, kb))

	stored, err = repo.GetKnowledgeBaseByID(ctx, "kb-1")
	require.NoError(t, err)
	require.Empty(t, stored.EmbeddingModelID,
		"clearing the embedding model must be readable back as cleared")
}
