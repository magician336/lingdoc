package workspace

import (
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

func newTestHandler(db *gorm.DB, shares interfaces.KBShareService, knowledge interfaces.KnowledgeBaseService) *Handler {
	runtime := NewGORMSourceRuntime(db, nil, shares, knowledge)
	service := core.NewServiceWithSources(core.NewGORMRepository(db), core.ContractDemoTemplate{}, runtime.WorkspaceSourcePolicy())
	runtime.ConnectProjects(service)
	return NewHandler(HandlerDependencies{Service: service, Sources: runtime, Integration: runtime})
}

func testRuntime(handler *Handler) *SourceRuntime {
	return handler.integration.(*SourceRuntime)
}
