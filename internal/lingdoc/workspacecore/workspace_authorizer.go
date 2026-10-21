package workspacecore

import "context"

// WorkspaceAuthorizer authorizes one action inside a transaction.
//
// It is the workspace policy, not the capability authorizer: it answers
// "may this actor perform this action on this project". It is also the only
// place that decides whether a denial is recorded as a shadow audit, so a
// caller that answers the question itself would answer it differently.
//
// A context that has left the aggregate still needs this judgement. It gets
// it through this port, not through the aggregate.
type WorkspaceAuthorizer interface {
	AuthorizeTenant(ctx context.Context, tx Transaction, request AuthorizationRequest) error
	AuthorizeProject(ctx context.Context, tx Transaction, request AuthorizationRequest) (Project, error)
}

// workspaceAuthorizer is the workspace's implementation of the authorizer.
//
// It holds the aggregate because the policy is the aggregate's: the
// authorizer, the authorization mode and the audit sink are the workspace's
// facts. The caller never sees that; it sees the port.
type workspaceAuthorizer struct {
	service *Service
}

// AuthorizeTenant authorizes the request against the tenant's role.
func (a *workspaceAuthorizer) AuthorizeTenant(ctx context.Context, tx Transaction, request AuthorizationRequest) error {
	return a.service.authorizeTenant(ctx, tx, request)
}

// AuthorizeProject authorizes the request against the project, and returns
// the project it authorized.
func (a *workspaceAuthorizer) AuthorizeProject(ctx context.Context, tx Transaction, request AuthorizationRequest) (Project, error) {
	return a.service.authorizeProject(ctx, tx, request)
}

// NewWorkspaceAuthorizer returns the authorizer a context uses to judge its
// reads and its writes.
//
// The service is the workspace's, and only the composition root holds it: the
// authorizer hands the caller a port, never the aggregate.
func NewWorkspaceAuthorizer(service *Service) WorkspaceAuthorizer {
	return &workspaceAuthorizer{service: service}
}
