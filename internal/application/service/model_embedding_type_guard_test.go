package service

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// GetEmbeddingModel must fail closed on the model type.
//
// This is the mirror of the chat-side guard (model_chat_type_guard_test.go):
// an embedder built for a KnowledgeQA / Rerank / ASR row is not a working
// embedder — the upstream speaks another protocol and rejects the call
// (HTTP 400, no vector). Because a knowledge base's embedding_model_id is a
// tenant-fillable field, both the ingestion side and the retrieval side of
// that knowledge base break at once, and the cause (the wrong model row was
// bound) stays far away from the failure.
//
// The verdict is taken from the model TYPE, never from the model name: a name
// check would be bypassed by renaming the model.
//
// These tests are offline: the base URL is a loopback test server that counts
// requests, and the SSRF whitelist is relaxed for it the same way the other
// connector tests do it. No provider is called and no request leaves the
// process.
// ---------------------------------------------------------------------------

// newEmbeddingTypeGuardService builds a model service whose model lookup is
// answered from a fixed row.
func newEmbeddingTypeGuardService(model *types.Model) interfaces.ModelService {
	return NewModelService(&stubModelRepoForDelete{model: model}, nil, nil, nil, nil, nil)
}

// embeddingGuardModel builds a stored model row of the given type.
func embeddingGuardModel(modelID string, modelType types.ModelType, baseURL string) *types.Model {
	return &types.Model{
		ID:     modelID,
		Name:   "some-model",
		Type:   modelType,
		Source: types.ModelSourceRemote,
		Status: types.ModelStatusActive,
		Parameters: types.ModelParameters{
			BaseURL:  baseURL,
			Provider: "generic",
		},
	}
}

func TestGetEmbeddingModelRejectsChatModelType(t *testing.T) {
	allowLoopbackForTest(t)
	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	svc := newEmbeddingTypeGuardService(
		embeddingGuardModel("chat-1", types.ModelTypeKnowledgeQA, server.URL))

	embedder, err := svc.GetEmbeddingModel(ctxWithTenant(1), "chat-1")

	require.Error(t, err, "a chat model must not produce an embedder")
	require.ErrorIs(t, err, ErrModelTypeNotEmbeddingCapable)
	assert.Nil(t, embedder, "no embedder may be built for a chat model")
	assert.Zero(t, atomic.LoadInt32(&requests), "no request may leave the process")
}

func TestGetEmbeddingModelRejectsNonEmbeddingModelTypes(t *testing.T) {
	allowLoopbackForTest(t)
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
			svc := newEmbeddingTypeGuardService(
				embeddingGuardModel("model-1", c.modelType, "http://127.0.0.1:1/v1"))

			embedder, err := svc.GetEmbeddingModel(ctxWithTenant(1), "model-1")

			require.Error(t, err)
			require.ErrorIs(t, err, ErrModelTypeNotEmbeddingCapable)
			assert.Nil(t, embedder)
		})
	}
}

func TestGetEmbeddingModelAllowsEmbeddingModelType(t *testing.T) {
	allowLoopbackForTest(t)
	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	svc := newEmbeddingTypeGuardService(
		embeddingGuardModel("embed-1", types.ModelTypeEmbedding, server.URL))

	embedder, err := svc.GetEmbeddingModel(ctxWithTenant(1), "embed-1")

	require.NoError(t, err, "an embedding-capable model must still produce an embedder")
	assert.NotNil(t, embedder)
	assert.Zero(t, atomic.LoadInt32(&requests), "building an embedder must not call the provider")
}

func TestGetEmbeddingModelGuardErrorNamesTheModelAndItsType(t *testing.T) {
	svc := newEmbeddingTypeGuardService(
		embeddingGuardModel("chat-42", types.ModelTypeKnowledgeQA, "http://127.0.0.1:1/v1"))

	_, err := svc.GetEmbeddingModel(ctxWithTenant(1), "chat-42")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "chat-42", "the error must name the model that was rejected")
	assert.Contains(t, err.Error(), string(types.ModelTypeKnowledgeQA),
		"the error must name the rejected type")
}

func TestGetEmbeddingModelGuardFollowsTypeNotName(t *testing.T) {
	allowLoopbackForTest(t)
	// A chat-named model of KnowledgeQA type is still a chat model.
	svc := newEmbeddingTypeGuardService(&types.Model{
		ID:         "looks-like-embed",
		Name:       "text-embedding-3-small",
		Type:       types.ModelTypeKnowledgeQA,
		Source:     types.ModelSourceRemote,
		Status:     types.ModelStatusActive,
		Parameters: types.ModelParameters{BaseURL: "http://127.0.0.1:1/v1", Provider: "generic"},
	})
	embedder, err := svc.GetEmbeddingModel(ctxWithTenant(1), "looks-like-embed")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrModelTypeNotEmbeddingCapable)
	assert.Nil(t, embedder)

	// An embedding-named model of Embedding type is an embedding model.
	svc = newEmbeddingTypeGuardService(&types.Model{
		ID:         "looks-like-chat",
		Name:       "gpt-4o",
		Type:       types.ModelTypeEmbedding,
		Source:     types.ModelSourceRemote,
		Status:     types.ModelStatusActive,
		Parameters: types.ModelParameters{BaseURL: "http://127.0.0.1:1/v1", Provider: "generic"},
	})
	embedder, err = svc.GetEmbeddingModel(ctxWithTenant(1), "looks-like-chat")
	require.NoError(t, err)
	assert.NotNil(t, embedder)
}

// TestGetEmbeddingModelForTenantRejectsChatModelType pins the same guard on
// the cross-tenant exit: the shared-knowledge-base path must not build an
// embedder for a row that cannot serve embedding either.
func TestGetEmbeddingModelForTenantRejectsChatModelType(t *testing.T) {
	allowLoopbackForTest(t)
	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	svc := newEmbeddingTypeGuardService(
		embeddingGuardModel("chat-1", types.ModelTypeKnowledgeQA, server.URL))

	embedder, err := svc.GetEmbeddingModelForTenant(ctxWithTenant(1), "chat-1", 2)

	require.Error(t, err, "a chat model must not produce an embedder")
	require.ErrorIs(t, err, ErrModelTypeNotEmbeddingCapable)
	assert.Nil(t, embedder)
	assert.Zero(t, atomic.LoadInt32(&requests), "no request may leave the process")
}

func TestGetEmbeddingModelForTenantAllowsEmbeddingModelType(t *testing.T) {
	allowLoopbackForTest(t)
	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	svc := newEmbeddingTypeGuardService(
		embeddingGuardModel("embed-1", types.ModelTypeEmbedding, server.URL))

	embedder, err := svc.GetEmbeddingModelForTenant(ctxWithTenant(1), "embed-1", 2)

	require.NoError(t, err)
	assert.NotNil(t, embedder)
	assert.Zero(t, atomic.LoadInt32(&requests), "building an embedder must not call the provider")
}

// TestChatAndEmbeddingExitsAgreeOnTheSameModelRow is the symmetry proof: for
// one and the same stored row, the chat exit and the embedding exit reach the
// same verdict about which purpose that row may serve.
func TestChatAndEmbeddingExitsAgreeOnTheSameModelRow(t *testing.T) {
	allowLoopbackForTest(t)

	chatRow := embeddingGuardModel("row-chat", types.ModelTypeKnowledgeQA, "http://127.0.0.1:1/v1")
	svc := newEmbeddingTypeGuardService(chatRow)

	chatClient, chatErr := svc.GetChatModel(ctxWithTenant(1), "row-chat")
	require.NoError(t, chatErr, "a chat row must still serve the chat exit")
	assert.NotNil(t, chatClient)

	embedder, embedErr := svc.GetEmbeddingModel(ctxWithTenant(1), "row-chat")
	require.Error(t, embedErr, "the same row must not serve the embedding exit")
	require.ErrorIs(t, embedErr, ErrModelTypeNotEmbeddingCapable)
	assert.Nil(t, embedder)

	embedRow := embeddingGuardModel("row-embed", types.ModelTypeEmbedding, "http://127.0.0.1:1/v1")
	svc = newEmbeddingTypeGuardService(embedRow)

	embedder, embedErr = svc.GetEmbeddingModel(ctxWithTenant(1), "row-embed")
	require.NoError(t, embedErr, "an embedding row must still serve the embedding exit")
	assert.NotNil(t, embedder)

	chatClient, chatErr = svc.GetChatModel(ctxWithTenant(1), "row-embed")
	require.Error(t, chatErr, "the same row must not serve the chat exit")
	require.ErrorIs(t, chatErr, ErrModelTypeNotChatCapable)
	assert.Nil(t, chatClient)
}
