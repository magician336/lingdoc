package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// ---------------------------------------------------------------------------
// UpdateKBConfig must not accept a non-embedding model as the knowledge
// base's embedding model.
//
// This is the pair of the summary-model guard in the same handler: that one
// refuses a chat model as summary_model_id, this one refuses a chat model as
// embedding_model_id. Both are configuration-time guards for the same class
// of mistake — a model row bound to a purpose its upstream does not serve.
//
// The embedding slot is the more expensive half: a wrong row here breaks the
// ingestion side and the retrieval side of the knowledge base at once, and
// the provider rejects the calls (HTTP 400, no vector) far from the cause.
// ---------------------------------------------------------------------------

func TestUpdateKBConfigRejectsChatModelAsEmbeddingModel(t *testing.T) {
	repo := &summaryGuardKBRepository{}
	router := newSummaryGuardRouter(map[string]*types.Model{
		"chat-1": {ID: "chat-1", Type: types.ModelTypeKnowledgeQA},
	}, repo)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/initialization/config/kb-1",
		strings.NewReader(`{"llmModelId":"chat-1","embeddingModelId":"chat-1"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	t.Logf("rejection body: %s", body)
	if !strings.Contains(body, string(types.ModelTypeKnowledgeQA)) {
		t.Fatalf("expected the error to name the offending model type, got %s", body)
	}
	if repo.updated != nil {
		t.Fatalf("knowledge base must not be persisted with an invalid embedding model: %+v", repo.updated)
	}
}

func TestUpdateKBConfigAllowsEmbeddingModelAsEmbeddingModel(t *testing.T) {
	repo := &summaryGuardKBRepository{}
	router := newSummaryGuardRouter(map[string]*types.Model{
		"chat-1":  {ID: "chat-1", Type: types.ModelTypeKnowledgeQA},
		"embed-1": {ID: "embed-1", Type: types.ModelTypeEmbedding},
	}, repo)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/initialization/config/kb-1",
		strings.NewReader(`{"llmModelId":"chat-1","embeddingModelId":"embed-1"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if repo.updated == nil {
		t.Fatal("expected the knowledge base to be persisted")
	}
	if repo.updated.EmbeddingModelID != "embed-1" {
		t.Fatalf("embedding_model_id = %q, want embed-1", repo.updated.EmbeddingModelID)
	}
}
