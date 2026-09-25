package generation

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestGenerationRoutesMatchContractAndRejectMissingActor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterRoutes(router.Group("/api/v1/lingdoc"), NewHandler(nil, nil))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/lingdoc/projects/p-demo/generations", strings.NewReader(`{}`))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("POST status = %d, want %d; body=%s", response.Code, http.StatusUnauthorized, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"code":"unauthenticated"`) {
		t.Fatalf("response missing contract error envelope: %s", response.Body.String())
	}

	get := httptest.NewRequest(http.MethodGet, "/api/v1/lingdoc/projects/p-demo/generations/run-1", nil)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, get)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("GET status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestGenerationStartStrictlyValidatesRequestBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	validResolver := func(*gin.Context) (Actor, bool) {
		return Actor{TenantID: 1, UserID: "user-1"}, true
	}
	oversized := fmt.Sprintf(`{"instruction":"%s"}`, strings.Repeat("x", maxGenerationRequestBytes))
	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{name: "unknown field", body: `{"chapter_id":"chapter-1","unexpected":true}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_request"},
		{name: "non-json unicode whitespace", body: "\u00a0{\"chapter_id\":\"chapter-1\"}", wantStatus: http.StatusBadRequest, wantCode: "invalid_request"},
		{name: "second json value", body: `{"chapter_id":"chapter-1"} {}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_request"},
		{name: "trailing data", body: `{"chapter_id":"chapter-1"} garbage`, wantStatus: http.StatusBadRequest, wantCode: "invalid_request"},
		{name: "null is not an object", body: `null`, wantStatus: http.StatusBadRequest, wantCode: "invalid_request"},
		{name: "body too large", body: oversized, wantStatus: http.StatusBadRequest, wantCode: "invalid_request"},
		{name: "valid request", body: `{"chapter_id":"chapter-1"}`, wantStatus: http.StatusServiceUnavailable, wantCode: "dependency_unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := gin.New()
			RegisterRoutes(router.Group("/api/v1/lingdoc"), NewHandler(nil, validResolver))
			request := httptest.NewRequest(http.MethodPost, "/api/v1/lingdoc/projects/p-demo/generations", strings.NewReader(tt.body))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != tt.wantStatus {
				t.Fatalf("POST status = %d, want %d; body=%s", response.Code, tt.wantStatus, response.Body.String())
			}
			if !strings.Contains(response.Body.String(), `"code":"`+tt.wantCode+`"`) {
				t.Fatalf("response missing %s error: %s", tt.wantCode, response.Body.String())
			}
		})
	}
}
