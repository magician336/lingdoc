package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ---------------------------------------------------------------------------
// The knowledge base update endpoint must carry embedding_model_id to the
// service instead of dropping it on the way in.
//
// The field used to be accepted by the HTTP layer and never handed to the
// service: the caller got 200 and an empty value back on the next read.
// These tests pin the wire shape (the body field reaches the service) and the
// refusal shape (a typed refusal is a 400, not a generic 500).
// ---------------------------------------------------------------------------

type stubKBUpdateService struct {
	interfaces.KnowledgeBaseService
	kb                  *types.KnowledgeBase
	gotEmbeddingModelID *string
	gotConfig           *types.KnowledgeBaseConfig
	updateErr           error
}

func (s *stubKBUpdateService) GetKnowledgeBaseByID(_ context.Context, _ string) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

func (s *stubKBUpdateService) UpdateKnowledgeBase(
	_ context.Context, _ string, _ string, _ string,
	config *types.KnowledgeBaseConfig, embeddingModelID *string,
) (*types.KnowledgeBase, error) {
	s.gotConfig = config
	s.gotEmbeddingModelID = embeddingModelID
	if s.updateErr != nil {
		return nil, s.updateErr
	}
	kb := *s.kb
	if embeddingModelID != nil {
		kb.EmbeddingModelID = *embeddingModelID
	}
	return &kb, nil
}

func newKBUpdateRouter(svc interfaces.KnowledgeBaseService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler(), func(c *gin.Context) {
		ctx := types.WithCaller(c.Request.Context(),
			types.Caller{TenantID: 7, UserID: "user-1", Role: types.TenantRoleAdmin})
		ctx = types.WithExecutionTenant(ctx, 7)
		c.Set(types.TenantIDContextKey.String(), uint64(7))
		c.Set(types.UserIDContextKey.String(), "user-1")
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	h := &KnowledgeBaseHandler{service: svc}
	r.PUT("/knowledge-bases/:id", h.UpdateKnowledgeBase)
	return r
}

func putKBUpdate(t *testing.T, svc interfaces.KnowledgeBaseService, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/knowledge-bases/kb-1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	newKBUpdateRouter(svc).ServeHTTP(w, req)
	return w
}

func TestUpdateKBRequestCarriesEmbeddingModelIDToTheService(t *testing.T) {
	svc := &stubKBUpdateService{kb: &types.KnowledgeBase{ID: "kb-1", TenantID: 7, Name: "kb"}}

	w := putKBUpdate(t, svc, `{"name":"kb","embedding_model_id":"embed-1"}`)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NotNil(t, svc.gotEmbeddingModelID, "embedding_model_id must reach the service")
	require.Equal(t, "embed-1", *svc.gotEmbeddingModelID)
	require.Contains(t, w.Body.String(), "embed-1", "the response must read back the persisted value")
}

func TestUpdateKBRequestWithoutEmbeddingModelIDKeepsItOutOfTheServiceCall(t *testing.T) {
	svc := &stubKBUpdateService{kb: &types.KnowledgeBase{ID: "kb-1", TenantID: 7, Name: "kb"}}

	w := putKBUpdate(t, svc, `{"name":"kb","description":"d"}`)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Nil(t, svc.gotEmbeddingModelID,
		"an absent embedding_model_id must not be turned into a write")
}

func TestUpdateKBRequestReportsATypedRefusalAsBadRequest(t *testing.T) {
	// The service refuses with its own verdict (e.g. "that model is not an
	// embedding model"). Reporting that as a 500 would hide the cause.
	svc := &stubKBUpdateService{
		kb:        &types.KnowledgeBase{ID: "kb-1", TenantID: 7, Name: "kb"},
		updateErr: apperrors.NewBadRequestError("embedding model \"chat-1\" has type \"KnowledgeQA\""),
	}

	w := putKBUpdate(t, svc, `{"name":"kb","embedding_model_id":"chat-1"}`)

	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "embedding")
}
