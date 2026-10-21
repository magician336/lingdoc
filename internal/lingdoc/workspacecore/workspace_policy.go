package workspacecore

import "context"

// WorkspacePolicy is the workspace's own operating policy.
//
// Four facts about the workspace are not any one context's own: the
// authorization mode the workspace runs in, whether it still requires the
// project to be activated before members may be written, how it records an
// audit event, and how it authorizes outside a transaction.
//
// They are workspace-wide facts, so they stay in the workspace and reach a
// context through this port. A context that guessed them would guess the
// behaviour of the workspace, and the guess would be wrong the moment the
// workspace's mode changed.
//
// The port is required, not optional. A composition that leaves it out has
// not said what the context may do, and the context fails closed at
// assembly time rather than guessing at request time.
type WorkspacePolicy interface {
	// AuthorizationMode is the mode the workspace authorizes in.
	AuthorizationMode() AuthorizationMode
	// RequiresActivationRead reports whether the workspace still requires the
	// project to be activated before members may be written.
	RequiresActivationRead() bool
	// RecordAuditEvent writes one audit event outside the caller's
	// transaction.
	RecordAuditEvent(ctx context.Context, event AuditEvent) error
	// Authorize answers whether the actor may act on the project, and records
	// the decision in the project's audit log.
	Authorize(ctx context.Context, actor Actor, projectID, capability string) error
}

// workspacePolicy is the workspace's implementation of its own policy.
//
// It holds the aggregate because the policy is the aggregate's: the mode, the
// activation requirement, the audit sink and the authorizer are the
// workspace's facts. The caller never sees that; it sees the port.
type workspacePolicy struct {
	service *Service
}

// AuthorizationMode is the mode the workspace authorizes in.
func (p *workspacePolicy) AuthorizationMode() AuthorizationMode {
	return p.service.mode
}

// RequiresActivationRead reports whether the workspace still requires the
// project to be activated before members may be written.
func (p *workspacePolicy) RequiresActivationRead() bool {
	return p.service.requireActivationRead
}

// RecordAuditEvent writes one audit event outside the caller's transaction.
func (p *workspacePolicy) RecordAuditEvent(ctx context.Context, event AuditEvent) error {
	return p.service.recordAuditEvent(ctx, event)
}

// Authorize answers whether the actor may act on the project, and records the
// decision in the project's audit log.
func (p *workspacePolicy) Authorize(ctx context.Context, actor Actor, projectID, capability string) error {
	return p.service.Authorize(ctx, actor, projectID, capability)
}

// NewWorkspacePolicy returns the workspace's own operating policy.
//
// The service is the workspace's, and only the composition root holds it: the
// policy hands the caller a port, never the aggregate.
func NewWorkspacePolicy(service *Service) WorkspacePolicy {
	return &workspacePolicy{service: service}
}
