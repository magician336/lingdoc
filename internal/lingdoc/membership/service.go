// Package membership owns the member and owner context: the operations that
// decide who may act on a project and how ownership moves.
//
// The port is declared in ports.go, next to the language it describes, rather
// than in the transport that consumes it (Spec #47 decision 33). The
// implementation and the invariants live here.
//
// The service never holds the workspace aggregate. It reaches the workspace
// through narrow ports: the operation pipeline that runs its writes, the
// workspace authorizer that judges its reads, the repository that opens a
// transaction, and the member storage ports that hold the records. Each of
// those ports has one owner and one copy of the knowledge it carries.
package membership

import (
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

// Service owns the member and owner context.
//
// The ports it holds are the workspace's, narrowed to what the member and
// owner operations actually need. A caller that supplies them supplies the
// capabilities, not the aggregate.
type Service struct {
	runner     core.OperationRunner
	authorizer core.WorkspaceAuthorizer
	repository core.Repository
	records    core.MemberRecords
	directory  core.MemberDirectoryReader
	transfers  core.OwnerTransferRecords
	apiKeys    core.TenantAPIKeyReader
	resources  core.ProjectResourceReader
}

// NewService returns the member and owner service.
//
// The ports are the workspace's narrow capabilities. The service holds them
// and never the aggregate: that is what lets the aggregate stop implementing
// the member and owner operations.
func NewService(
	runner core.OperationRunner,
	authorizer core.WorkspaceAuthorizer,
	repository core.Repository,
	records core.MemberRecords,
	directory core.MemberDirectoryReader,
	transfers core.OwnerTransferRecords,
	apiKeys core.TenantAPIKeyReader,
	resources core.ProjectResourceReader,
) *Service {
	return &Service{
		runner:     runner,
		authorizer: authorizer,
		repository: repository,
		records:    records,
		directory:  directory,
		transfers:  transfers,
		apiKeys:    apiKeys,
		resources:  resources,
	}
}
