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

// Ports are the workspace's capabilities the member and owner context needs.
//
// Each of them is a narrow port with one owner. The service holds them and
// never the aggregate: that is what lets the aggregate stop implementing the
// member and owner operations.
type Ports struct {
	// Runner runs one workspace operation under the shared pipeline.
	Runner core.OperationRunner
	// Authorizer judges a request inside a transaction.
	Authorizer core.WorkspaceAuthorizer
	// Policy is the workspace's own operating policy.
	Policy core.WorkspacePolicy
	// Repository opens a transaction.
	Repository core.Repository
	// Records reads and writes the workspace's general records.
	Records core.MemberRecords
	// Directory lists the members of a tenant.
	Directory core.MemberDirectoryReader
	// Transfers reads and cancels the owner transfers.
	Transfers core.OwnerTransferRecords
	// APIKeys reads the tenant's API keys.
	APIKeys core.TenantAPIKeyReader
	// Resources reports whether a project holds a resource.
	Resources core.ProjectResourceReader
}

// Service owns the member and owner context.
//
// The ports it holds are the workspace's, narrowed to what the member and
// owner operations actually need. A caller that supplies them supplies the
// capabilities, not the aggregate.
type Service struct {
	runner     core.OperationRunner
	authorizer core.WorkspaceAuthorizer
	policy     core.WorkspacePolicy
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
func NewService(ports Ports) *Service {
	return &Service{
		runner:     ports.Runner,
		authorizer: ports.Authorizer,
		policy:     ports.Policy,
		repository: ports.Repository,
		records:    ports.Records,
		directory:  ports.Directory,
		transfers:  ports.Transfers,
		apiKeys:    ports.APIKeys,
		resources:  ports.Resources,
	}
}
