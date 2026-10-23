package service

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// GetChatModel must fail closed on the model type.
//
// A chat client built for an Embedding / Rerank / ASR model is not a working
// client: the upstream speaks a different protocol and rejects the call
// (HTTP 400, no output). The failure then surfaces far from its cause — a
// document without a summary, a generation run that failed for no visible
// reason — so the client must never be built.
//
// The verdict is taken from the model TYPE, never from the model name: a name
// check would be bypassed by renaming the model.
//
// These tests are offline: the base URL is a loopback test server that
// counts requests, and the SSRF whitelist is relaxed for it the same way the
// other connector tests do it. No provider is called and no request leaves
// the process.
// ---------------------------------------------------------------------------

// newChatTypeGuardService builds a model service whose model lookup is
// answered from a fixed row.
func newChatTypeGuardService(model *types.Model) interfaces.ModelService {
	return NewModelService(&stubModelRepoForDelete{model: model}, nil, nil, nil, nil, nil)
}

// allowLoopbackForTest lets the SSRF check accept the loopback test server.
// Production keeps the default strict SSRF policy.
func allowLoopbackForTest(t *testing.T) {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", "127.0.0.1,localhost")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)
}

func TestGetChatModelRejectsEmbeddingModelType(t *testing.T) {
	allowLoopbackForTest(t)
	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	svc := newChatTypeGuardService(&types.Model{
		ID:         "embed-1",
		Name:       "qwen3.7-text-embedding-flash",
		Type:       types.ModelTypeEmbedding,
		Source:     types.ModelSourceRemote,
		Parameters: types.ModelParameters{BaseURL: server.URL, Provider: "generic"},
	})

	client, err := svc.GetChatModel(ctxWithTenant(1), "embed-1")

	require.Error(t, err, "an embedding model must not produce a chat client")
	require.ErrorIs(t, err, ErrModelTypeNotChatCapable)
	assert.Nil(t, client, "no chat client may be built for an embedding model")
	assert.Zero(t, atomic.LoadInt32(&requests), "no request may leave the process")
}

func TestGetChatModelRejectsNonTextModelTypes(t *testing.T) {
	allowLoopbackForTest(t)
	cases := []struct {
		name      string
		modelType types.ModelType
	}{
		{name: "rerank", modelType: types.ModelTypeRerank},
		{name: "asr", modelType: types.ModelTypeASR},
		{name: "empty type", modelType: types.ModelType("")},
		{name: "unknown type", modelType: types.ModelType("Chat")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc := newChatTypeGuardService(&types.Model{
				ID:         "model-1",
				Name:       "some-model",
				Type:       c.modelType,
				Source:     types.ModelSourceRemote,
				Parameters: types.ModelParameters{BaseURL: "http://127.0.0.1:1/v1", Provider: "generic"},
			})

			client, err := svc.GetChatModel(ctxWithTenant(1), "model-1")

			require.Error(t, err)
			require.ErrorIs(t, err, ErrModelTypeNotChatCapable)
			assert.Nil(t, client)
		})
	}
}

func TestGetChatModelAllowsChatCapableModelTypes(t *testing.T) {
	allowLoopbackForTest(t)
	cases := []struct {
		name      string
		modelType types.ModelType
	}{
		{name: "knowledge qa", modelType: types.ModelTypeKnowledgeQA},
		// A VLLM row is a multimodal model served over the same
		// OpenAI-compatible chat/completions protocol; the query-understand
		// fallback loads it through GetChatModel
		// (chat_pipeline/query_understand.go), so it must stay allowed.
		{name: "vlm", modelType: types.ModelTypeVLLM},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var requests int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				atomic.AddInt32(&requests, 1)
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			svc := newChatTypeGuardService(&types.Model{
				ID:         "model-1",
				Name:       "deepseek-flash",
				Type:       c.modelType,
				Source:     types.ModelSourceRemote,
				Parameters: types.ModelParameters{BaseURL: server.URL, Provider: "generic"},
			})

			client, err := svc.GetChatModel(ctxWithTenant(1), "model-1")

			require.NoError(t, err, "a chat-capable model must still produce a chat client")
			assert.NotNil(t, client)
			assert.Zero(t, atomic.LoadInt32(&requests), "building a client must not call the provider")
		})
	}
}

func TestGetChatModelGuardErrorNamesTheModelAndItsType(t *testing.T) {
	svc := newChatTypeGuardService(&types.Model{
		ID:         "embed-42",
		Name:       "text-embedding-v4",
		Type:       types.ModelTypeEmbedding,
		Source:     types.ModelSourceRemote,
		Parameters: types.ModelParameters{BaseURL: "http://127.0.0.1:1/v1", Provider: "generic"},
	})

	_, err := svc.GetChatModel(ctxWithTenant(1), "embed-42")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "embed-42", "the error must name the model that was rejected")
	assert.Contains(t, err.Error(), string(types.ModelTypeEmbedding), "the error must name the rejected type")
}

func TestGetChatModelGuardFollowsTypeNotName(t *testing.T) {
	allowLoopbackForTest(t)
	// A chat-named model of Embedding type is still an embedding model.
	svc := newChatTypeGuardService(&types.Model{
		ID:         "looks-like-chat",
		Name:       "gpt-4o",
		Type:       types.ModelTypeEmbedding,
		Source:     types.ModelSourceRemote,
		Parameters: types.ModelParameters{BaseURL: "http://127.0.0.1:1/v1", Provider: "generic"},
	})
	client, err := svc.GetChatModel(ctxWithTenant(1), "looks-like-chat")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrModelTypeNotChatCapable)
	assert.Nil(t, client)

	// An embedding-named model of KnowledgeQA type is a chat model.
	svc = newChatTypeGuardService(&types.Model{
		ID:         "looks-like-embed",
		Name:       "text-embedding-3-small",
		Type:       types.ModelTypeKnowledgeQA,
		Source:     types.ModelSourceRemote,
		Parameters: types.ModelParameters{BaseURL: "http://127.0.0.1:1/v1", Provider: "generic"},
	})
	client, err = svc.GetChatModel(ctxWithTenant(1), "looks-like-embed")
	require.NoError(t, err)
	assert.NotNil(t, client)
}
