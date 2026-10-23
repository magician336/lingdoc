package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// ---------------------------------------------------------------------------
// The connection self-check must respect the type of the model row it points
// at.
//
// The self-check builds a temporary model with a hard-coded type (KnowledgeQA
// for the chat check, Embedding for the embedding check) and never looks at
// the stored row. So "test connection" on an Embedding row used to send a
// real chat request to a model that does not serve chat — and reported
// "connection normal, model usable" for a row the production chat path
// rejects with ErrModelTypeNotChatCapable.
//
// The guard reads the stored row's TYPE and refuses before any provider
// request is built. The tests below are offline: the base URL is a loopback
// test server that counts requests, and the SSRF whitelist is relaxed for it
// the same way the service-side guard tests do it. No provider is called and
// no request leaves the process.
// ---------------------------------------------------------------------------

// modelTypeGuardRepo answers model lookups from a fixed map. The other
// ModelRepository methods stay unimplemented through the embedded interface.
type modelTypeGuardRepo struct {
	interfaces.ModelRepository
	models map[string]*types.Model
}

func (r *modelTypeGuardRepo) GetByID(_ context.Context, _ uint64, id string) (*types.Model, error) {
	return r.models[id], nil
}

// newModelTypeGuardService builds the REAL model service over the fixed rows,
// so the self-check and the service exit are compared on the same rules.
func newModelTypeGuardService(models map[string]*types.Model) interfaces.ModelService {
	return service.NewModelService(&modelTypeGuardRepo{models: models}, nil, nil, nil, nil, nil)
}

// newModelTypeGuardRouter wires the two self-check endpoints over the given
// stored model rows.
func newModelTypeGuardRouter(models map[string]*types.Model) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.Use(func(c *gin.Context) {
		// The tenant scope has to be visible on BOTH context surfaces: the
		// handlers read it from the request context, the repository from the
		// gin keys.
		ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(1))
		ctx = context.WithValue(ctx, types.TenantInfoContextKey, &types.Tenant{ID: 1})
		c.Request = c.Request.WithContext(ctx)
		c.Set(types.TenantIDContextKey.String(), uint64(1))
		c.Next()
	})
	h := &InitializationHandler{modelService: newModelTypeGuardService(models)}
	r.POST("/initialization/remote/check", h.CheckRemoteModel)
	r.POST("/initialization/embedding/test", h.TestEmbeddingModel)
	return r
}

// allowLoopbackForSelfCheck lets the SSRF check accept the loopback test
// server. Production keeps the default strict SSRF policy.
func allowLoopbackForSelfCheck(t *testing.T) {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", "127.0.0.1,localhost")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)
}

// chatCompletionServer answers like an OpenAI-compatible chat endpoint and
// counts the requests it received.
func chatCompletionServer(t *testing.T, requests *int32) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(requests, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-test",
			"object":  "chat.completion",
			"created": 0,
			"model":   "test-model",
			"choices": []any{map[string]any{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": "ok"},
				"finish_reason": "stop",
			}},
		})
	}))
	t.Cleanup(server.Close)
	return server
}

// embeddingServer answers like an OpenAI-compatible embeddings endpoint and
// counts the requests it received.
func embeddingServer(t *testing.T, requests *int32) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(requests, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"model":  "test-model",
			"data": []any{map[string]any{
				"object":    "embedding",
				"index":     0,
				"embedding": []float32{0.1, 0.2, 0.3},
			}},
			"usage": map[string]any{"prompt_tokens": 1, "total_tokens": 1},
		})
	}))
	t.Cleanup(server.Close)
	return server
}

func TestCheckRemoteModelRejectsNonChatStoredModelTypeWithoutProviderRequest(t *testing.T) {
	allowLoopbackForSelfCheck(t)
	var requests int32
	server := chatCompletionServer(t, &requests)

	router := newModelTypeGuardRouter(map[string]*types.Model{
		"embed-1": {
			ID:         "embed-1",
			Name:       "text-embedding-v4",
			Type:       types.ModelTypeEmbedding,
			Source:     types.ModelSourceRemote,
			Status:     types.ModelStatusActive,
			Parameters: types.ModelParameters{BaseURL: server.URL, Provider: "generic"},
		},
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/initialization/remote/check",
		strings.NewReader(fmt.Sprintf(
			`{"modelId":"embed-1","modelName":"text-embedding-v4","baseUrl":%q,"provider":"generic"}`,
			server.URL)))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, string(types.ModelTypeEmbedding)) {
		t.Fatalf("expected the error to name the offending model type, got %s", body)
	}
	if !strings.Contains(body, "不能服务文本生成") {
		t.Fatalf("expected the error to say the model cannot serve text generation, got %s", body)
	}
	if got := atomic.LoadInt32(&requests); got != 0 {
		t.Fatalf("no provider request may be sent for a non-chat model, got %d", got)
	}
}

func TestCheckRemoteModelRejectsEmptyAndUnknownStoredModelTypes(t *testing.T) {
	allowLoopbackForSelfCheck(t)
	for _, modelType := range []types.ModelType{types.ModelType(""), types.ModelType("Chat")} {
		var requests int32
		server := chatCompletionServer(t, &requests)

		router := newModelTypeGuardRouter(map[string]*types.Model{
			"m-1": {
				ID:         "m-1",
				Name:       "mystery",
				Type:       modelType,
				Source:     types.ModelSourceRemote,
				Status:     types.ModelStatusActive,
				Parameters: types.ModelParameters{BaseURL: server.URL, Provider: "generic"},
			},
		})

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/initialization/remote/check",
			strings.NewReader(fmt.Sprintf(
				`{"modelId":"m-1","modelName":"mystery","baseUrl":%q,"provider":"generic"}`,
				server.URL)))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("type %q: expected 400, got %d body=%s", modelType, w.Code, w.Body.String())
		}
		if got := atomic.LoadInt32(&requests); got != 0 {
			t.Fatalf("type %q: no provider request may be sent, got %d", modelType, got)
		}
	}
}

func TestCheckRemoteModelAllowsChatStoredModelType(t *testing.T) {
	allowLoopbackForSelfCheck(t)
	var requests int32
	server := chatCompletionServer(t, &requests)

	router := newModelTypeGuardRouter(map[string]*types.Model{
		"chat-1": {
			ID:         "chat-1",
			Name:       "qwen-plus",
			Type:       types.ModelTypeKnowledgeQA,
			Source:     types.ModelSourceRemote,
			Status:     types.ModelStatusActive,
			Parameters: types.ModelParameters{BaseURL: server.URL, Provider: "generic"},
		},
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/initialization/remote/check",
		strings.NewReader(fmt.Sprintf(
			`{"modelId":"chat-1","modelName":"qwen-plus","baseUrl":%q,"provider":"generic"}`,
			server.URL)))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("a chat-capable row must still be checked against the provider, got %d requests", got)
	}
	if !strings.Contains(w.Body.String(), `"available":true`) {
		t.Fatalf("expected the check to report the model as available, got %s", w.Body.String())
	}
}

func TestCheckRemoteModelWithoutModelIDKeepsPreviousBehaviour(t *testing.T) {
	allowLoopbackForSelfCheck(t)
	var requests int32
	server := chatCompletionServer(t, &requests)

	// A pure form self-test carries no stored row: there is no type to read,
	// so the request's own fields decide — exactly as before.
	router := newModelTypeGuardRouter(map[string]*types.Model{})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/initialization/remote/check",
		strings.NewReader(fmt.Sprintf(
			`{"modelName":"qwen-plus","baseUrl":%q,"provider":"generic"}`, server.URL)))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("a form self-test must still call the provider, got %d requests", got)
	}
}

func TestTestEmbeddingModelRejectsNonEmbeddingStoredModelTypeWithoutProviderRequest(t *testing.T) {
	allowLoopbackForSelfCheck(t)
	var requests int32
	server := embeddingServer(t, &requests)

	router := newModelTypeGuardRouter(map[string]*types.Model{
		"chat-1": {
			ID:         "chat-1",
			Name:       "qwen-plus",
			Type:       types.ModelTypeKnowledgeQA,
			Source:     types.ModelSourceRemote,
			Status:     types.ModelStatusActive,
			Parameters: types.ModelParameters{BaseURL: server.URL, Provider: "generic"},
		},
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/initialization/embedding/test",
		strings.NewReader(fmt.Sprintf(
			`{"modelId":"chat-1","modelName":"qwen-plus","baseUrl":%q,"provider":"generic"}`,
			server.URL)))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, string(types.ModelTypeKnowledgeQA)) {
		t.Fatalf("expected the error to name the offending model type, got %s", body)
	}
	if got := atomic.LoadInt32(&requests); got != 0 {
		t.Fatalf("no provider request may be sent for a non-embedding model, got %d", got)
	}
}

func TestTestEmbeddingModelAllowsEmbeddingStoredModelType(t *testing.T) {
	allowLoopbackForSelfCheck(t)
	var requests int32
	server := embeddingServer(t, &requests)

	router := newModelTypeGuardRouter(map[string]*types.Model{
		"embed-1": {
			ID:         "embed-1",
			Name:       "text-embedding-v4",
			Type:       types.ModelTypeEmbedding,
			Source:     types.ModelSourceRemote,
			Status:     types.ModelStatusActive,
			Parameters: types.ModelParameters{BaseURL: server.URL, Provider: "generic"},
		},
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/initialization/embedding/test",
		strings.NewReader(fmt.Sprintf(
			`{"modelId":"embed-1","modelName":"text-embedding-v4","baseUrl":%q,"provider":"generic"}`,
			server.URL)))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("an embedding-capable row must still be checked against the provider, got %d requests", got)
	}
	if !strings.Contains(w.Body.String(), `"available":true`) {
		t.Fatalf("expected the check to report the model as available, got %s", w.Body.String())
	}
}

// TestSelfCheckAndServiceExitAgreeOnTheSameRow is the cross-exit symmetry
// proof for the self-check: after a self-check succeeds for a stored row, the
// production chat exit hands out a client for that very same row — and a row
// the self-check refuses is a row the chat exit refuses too.
func TestSelfCheckAndServiceExitAgreeOnTheSameRow(t *testing.T) {
	allowLoopbackForSelfCheck(t)
	var requests int32
	server := chatCompletionServer(t, &requests)

	models := map[string]*types.Model{
		"chat-1": {
			ID:         "chat-1",
			Name:       "qwen-plus",
			Type:       types.ModelTypeKnowledgeQA,
			Source:     types.ModelSourceRemote,
			Status:     types.ModelStatusActive,
			Parameters: types.ModelParameters{BaseURL: server.URL, Provider: "generic"},
		},
		"embed-1": {
			ID:         "embed-1",
			Name:       "text-embedding-v4",
			Type:       types.ModelTypeEmbedding,
			Source:     types.ModelSourceRemote,
			Status:     types.ModelStatusActive,
			Parameters: types.ModelParameters{BaseURL: server.URL, Provider: "generic"},
		},
	}
	svc := newModelTypeGuardService(models)
	router := newModelTypeGuardRouter(models)

	// The chat row: the self-check passes and so does the chat exit.
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/initialization/remote/check",
		strings.NewReader(fmt.Sprintf(
			`{"modelId":"chat-1","modelName":"qwen-plus","baseUrl":%q,"provider":"generic"}`,
			server.URL)))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for the chat row, got %d body=%s", w.Code, w.Body.String())
	}

	tenantCtx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	client, err := svc.GetChatModel(tenantCtx, "chat-1")
	if err != nil {
		t.Fatalf("the chat exit must hand out a client for the row the self-check accepted: %v", err)
	}
	if client == nil {
		t.Fatal("expected a chat client for the row the self-check accepted")
	}

	// The embedding row: the self-check refuses it, and so does the chat exit.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/initialization/remote/check",
		strings.NewReader(fmt.Sprintf(
			`{"modelId":"embed-1","modelName":"text-embedding-v4","baseUrl":%q,"provider":"generic"}`,
			server.URL)))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for the embedding row, got %d body=%s", w.Code, w.Body.String())
	}

	client, err = svc.GetChatModel(tenantCtx, "embed-1")
	if err == nil {
		t.Fatal("the chat exit must refuse the row the self-check refused")
	}
	if client != nil {
		t.Fatal("no chat client may be built for the row the self-check refused")
	}
}
