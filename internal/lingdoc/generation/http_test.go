package generation

import (
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

	cancel := httptest.NewRequest(http.MethodPost, "/api/v1/lingdoc/projects/p-demo/generations/run-1/cancel", nil)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, cancel)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("POST cancel status = %d, want %d", response.Code, http.StatusUnauthorized)
	}

	list := httptest.NewRequest(http.MethodGet, "/api/v1/lingdoc/projects/p-demo/chapters/ch-1/candidates", nil)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, list)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("GET candidates status = %d, want %d", response.Code, http.StatusUnauthorized)
	}

	getCandidate := httptest.NewRequest(http.MethodGet, "/api/v1/lingdoc/projects/p-demo/generations/run-1/candidate", nil)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, getCandidate)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("GET candidate status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}
