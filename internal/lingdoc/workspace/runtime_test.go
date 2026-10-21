package workspace

import (
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

func newTestHandler(db *gorm.DB, shares interfaces.KBShareService, knowledge interfaces.KnowledgeBaseService) *Handler {
	runtime := NewGORMSourceRuntime(db, ProjectPorts{}, shares, knowledge, delivery.NewFixedTemplateReader())
	service := core.NewServiceWithSources(core.NewGORMRepository(db), core.ContractDemoTemplate{}, runtime.WorkspaceSourcePolicy())
	runtime.ConnectProjects(ProjectPorts{Authorizer: service, Projects: service, Context: service})
	deps := HandlerDependencies{Projects: service, Members: service, Chapters: service, TemplateApp: service, ChangeSets: service, Sources: runtime, Integration: runtime}
	// The capabilities the project service provides are named by the caller.
	deps.WithMachineReads(service)
	deps.WithReviewAnchors(service, service)
	handler, err := NewHandler(deps)
	if err != nil {
		panic("assemble the workspace handler: " + err.Error())
	}
	return handler
}

func testRuntime(handler *Handler) *SourceRuntime {
	return handler.integration.(*SourceRuntime)
}
