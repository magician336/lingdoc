package candidateadoption

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/workspace"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRegisterRoutesLeavesChapterListingToWorkspaceOwner(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterRoutes(router, &CandidateAdoptionHandler{})

	for _, route := range router.Routes() {
		require.False(t, route.Method == "GET" && route.Path == "/projects/:projectId/chapters",
			"candidate adoption must not register the workspace chapter-list route")
	}
}

func TestRegisterRoutesComposesWithWorkspaceWithoutDuplicateChapterListing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	v1 := r.Group("/api/v1")
	workspace.NewHandler(nil, nil, nil).Register(v1)
	RegisterRoutes(v1.Group("/lingdoc"), &CandidateAdoptionHandler{})

	count := 0
	for _, route := range r.Routes() {
		if route.Method == "GET" && route.Path == "/api/v1/lingdoc/projects/:projectId/chapters" {
			count++
		}
	}
	require.Equal(t, 1, count)
}

func TestDecodeCandidateRequestRejectsUnknownAndTrailingJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{
		`{"candidate_id":"candidate-1","unknown":true}`,
		`{"candidate_id":"candidate-1"}{"candidate_id":"candidate-2"}`,
	} {
		t.Run(body, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/", strings.NewReader(body))
			var req acceptCandidateRequest
			require.Error(t, decodeCandidateRequest(c, &req))
		})
	}
}
