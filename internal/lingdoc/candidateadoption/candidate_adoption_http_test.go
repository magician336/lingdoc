package candidateadoption_test

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/workspace"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// candidateAdoptionSourcesStub stands in for the sources port when a test
// does not exercise the source routes. The port is required, so a handler
// assembled without it would fail at startup; this keeps the other routes
// usable.
type candidateAdoptionSourcesStub struct {
	workspace.SourceApplicationService
}

// candidateAdoptionServiceStub stands in for the application ports. The test
// only checks which routes the workspace registers, so none of the methods
// are called.
type candidateAdoptionServiceStub struct {
	workspace.ProjectApplication
	workspace.MembershipApplication
	workspace.ChapterApplication
	workspace.TemplateApplication
	workspace.ChangeSetApplication
}

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
	service := &candidateAdoptionServiceStub{}
	workspaceHandler, err := workspace.NewHandler(workspace.HandlerDependencies{
		Projects:    service,
		Members:     service,
		Chapters:    service,
		TemplateApp: service,
		ChangeSets:  service,
		Sources:     candidateAdoptionSourcesStub{},
	})
	if err != nil {
		t.Fatalf("assemble the workspace handler: %v", err)
	}
	workspaceHandler.Register(workspace.RouteGroups{Read: v1.Group("/lingdoc"), Write: v1.Group("/lingdoc")})
	candidateadoption.RegisterRoutes(v1.Group("/lingdoc"), &candidateadoption.CandidateAdoptionHandler{})

	count := 0
	for _, route := range r.Routes() {
		if route.Method == "GET" && route.Path == "/api/v1/lingdoc/projects/:projectId/chapters" {
			count++
		}
	}
	require.Equal(t, 1, count)
}
