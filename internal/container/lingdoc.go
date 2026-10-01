package container

import (
	"github.com/Tencent/WeKnora/internal/lingdoc/workspace"
	"github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// NewLingDocWorkspaceHandler is the composition root for LingDoc workspace
// application services and their production adapters. The HTTP handler itself
// receives only application ports and has no knowledge of GORM construction.
func NewLingDocWorkspaceHandler(
	db *gorm.DB,
	kbShares interfaces.KBShareService,
	knowledge interfaces.KnowledgeBaseService,
) *workspace.Handler {
	projectService := workspace.NewService(workspacecore.NewGORMRepository(db, workspacecore.ContractDemoTemplate{}))
	sourcePorts := workspace.NewGORMSourcePorts(db, kbShares)
	sourceService := workspace.NewSourceService(
		projectService,
		sourcePorts.Bindings,
		sourcePorts.Gateway,
		sourcePorts.Catalog,
		sourcePorts.Origins,
		kbShares,
		knowledge,
	)
	return workspace.NewHandler(workspace.HandlerDependencies{
		Service: projectService,
		Sources: sourceService,
	})
}
