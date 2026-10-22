package workspace

import (
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"github.com/Tencent/WeKnora/internal/lingdoc/membership"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// newMembershipService assembles the member and owner service over the
// workspace's ports, the way the composition root does.
//
// The member and owner context left the aggregate (Spec #47, ticket #268), so
// a handler now needs that service, not the aggregate. This is a small,
// obvious repeat of the composition root's wiring: the workspace's test
// package cannot reach the aggregate's own assembly, and the member and owner
// service must never hold the aggregate itself.
func newMembershipService(service *core.Service, repository core.Repository) *membership.Service {
	storage := core.NewMemberStorage(service)
	return membership.NewService(membership.Ports{
		Runner:     core.NewOperationRunner(service),
		Authorizer: core.NewWorkspaceAuthorizer(service),
		Policy:     core.NewWorkspacePolicy(service),
		Repository: repository,
		Records:    storage,
		Directory:  storage,
		Transfers:  storage,
		APIKeys:    storage,
		Resources:  storage,
	})
}

func newTestHandler(db *gorm.DB, shares interfaces.KBShareService, knowledge interfaces.KnowledgeBaseService) *Handler {
	runtime := NewGORMSourceRuntime(db, ProjectPorts{}, shares, knowledge, delivery.NewFixedTemplateReader())
	service := core.NewServiceWithSources(core.NewGORMRepository(db), core.ContractDemoTemplate{}, runtime.WorkspaceSourcePolicy())
	runtime.ConnectProjects(ProjectPorts{Authorizer: service, Projects: service, Context: service})
	connectAggregateCapabilities(runtime, service)
	deps := HandlerDependencies{
		Projects:    service,
		Members:     newMembershipService(service, core.NewGORMRepository(db)),
		Chapters:    service,
		TemplateApp: service,
		ChangeSets:  service,
		Sources:     runtime,
		Integration: runtime,
	}
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
