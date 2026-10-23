package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ---------------------------------------------------------------------------
// UpdateKBConfig must not accept a non-text-generation model as the
// knowledge base's summary model.
//
// This is the only write path that can change summary_model_id after the
// knowledge base exists: the handler looks the requested model up (existence
// only) and then stores its id in kb.SummaryModelID. Without a type check,
// an Embedding model can be bound after creation — which is exactly the
// configuration that makes the document pipeline derive a summary sub-task
// against a chat endpoint that the embedding model does not serve.
// ---------------------------------------------------------------------------

type summaryGuardKBService struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (s *summaryGuardKBService) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

type summaryGuardModelService struct {
	interfaces.ModelService
	models map[string]*types.Model
}

func (s *summaryGuardModelService) GetModelByID(_ context.Context, id string) (*types.Model, error) {
	return s.models[id], nil
}

type summaryGuardKnowledgeService struct {
	interfaces.KnowledgeService
}

func (s *summaryGuardKnowledgeService) ListPagedKnowledgeByKnowledgeBaseID(
	context.Context, string, *types.Pagination, types.KnowledgeListFilter,
) (*types.PageResult, error) {
	return nil, nil
}

type summaryGuardKBRepository struct {
	interfaces.KnowledgeBaseRepository
	updated *types.KnowledgeBase
}

func (r *summaryGuardKBRepository) UpdateKnowledgeBase(_ context.Context, kb *types.KnowledgeBase) error {
	r.updated = kb
	return nil
}

func newSummaryGuardRouter(
	models map[string]*types.Model, repo *summaryGuardKBRepository,
) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.Use(func(c *gin.Context) {
		c.Set(types.TenantIDContextKey.String(), uint64(1))
		c.Set(types.UserIDContextKey.String(), "u-test")
		c.Next()
	})
	h := &InitializationHandler{
		kbService: &summaryGuardKBService{kb: &types.KnowledgeBase{
			ID:   "kb-1",
			Name: "kb",
		}},
		modelService:     &summaryGuardModelService{models: models},
		knowledgeService: &summaryGuardKnowledgeService{},
		kbRepository:     repo,
	}
	r.PUT("/initialization/config/:kbId", h.UpdateKBConfig)
	return r
}

func TestUpdateKBConfigRejectsEmbeddingModelAsSummaryModel(t *testing.T) {
	repo := &summaryGuardKBRepository{}
	router := newSummaryGuardRouter(map[string]*types.Model{
		"embed-1": {ID: "embed-1", Type: types.ModelTypeEmbedding},
	}, repo)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/initialization/config/kb-1",
		strings.NewReader(`{"llmModelId":"embed-1"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	t.Logf("rejection body: %s", body)
	if !strings.Contains(body, "Embedding") {
		t.Fatalf("expected the error to name the offending model type, got %s", body)
	}
	if repo.updated != nil {
		t.Fatalf("knowledge base must not be persisted with an invalid summary model: %+v", repo.updated)
	}
}

func TestUpdateKBConfigAllowsChatModelAsSummaryModel(t *testing.T) {
	repo := &summaryGuardKBRepository{}
	router := newSummaryGuardRouter(map[string]*types.Model{
		"chat-1": {ID: "chat-1", Type: types.ModelTypeKnowledgeQA},
	}, repo)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/initialization/config/kb-1",
		strings.NewReader(`{"llmModelId":"chat-1"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if repo.updated == nil {
		t.Fatal("expected the knowledge base to be persisted")
	}
	if repo.updated.SummaryModelID != "chat-1" {
		t.Fatalf("summary_model_id = %q, want chat-1", repo.updated.SummaryModelID)
	}
}
