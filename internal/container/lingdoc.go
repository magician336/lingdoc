package container

import (
	"fmt"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
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
	projectService := workspacecore.NewService(workspacecore.NewGORMRepository(db), workspacecore.ContractDemoTemplate{})
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

// DeliveryDependencies keeps persistence, version checks and authorization
// explicit. No memory store or always-current/access-allowed fallback is
// silently installed in production. Existing delivery consumers supply these
// ports; this refactor does not add an unimplemented HTTP endpoint.
type DeliveryDependencies struct {
	Snapshots   delivery.SnapshotStore
	Exports     delivery.ExportStore
	Currentness delivery.CurrentnessChecker
	Access      delivery.ExportAccessChecker
	Renderer    delivery.FrozenRenderer
}
type LingDocDelivery struct {
	Releases delivery.ReleaseApplication
	Exports  delivery.ExportApplication
}

func NewLingDocDelivery(deps DeliveryDependencies) (LingDocDelivery, error) {
	if deps.Snapshots == nil || deps.Exports == nil || deps.Currentness == nil || deps.Access == nil {
		return LingDocDelivery{}, fmt.Errorf("delivery persistence, currentness and access ports are required")
	}
	if deps.Renderer == nil {
		deps.Renderer = delivery.DOCXRenderer{}
	}
	return LingDocDelivery{Releases: delivery.NewReleaseService(deps.Snapshots, deps.Currentness), Exports: delivery.NewExportService(deps.Snapshots, deps.Exports, deps.Renderer, deps.Currentness, deps.Access)}, nil
}
