package candidateadoption_test

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/workspace"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRegisterRoutesLeavesChapterListingToWorkspaceOwner(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	candidateadoption.RegisterRoutes(router, &candidateadoption.CandidateAdoptionHandler{})

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
	candidateadoption.RegisterRoutes(v1.Group("/lingdoc"), &candidateadoption.CandidateAdoptionHandler{})

	count := 0
	for _, route := range r.Routes() {
		if route.Method == "GET" && route.Path == "/api/v1/lingdoc/projects/:projectId/chapters" {
			count++
		}
	}
	require.Equal(t, 1, count)
}
