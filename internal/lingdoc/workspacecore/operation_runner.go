package workspacecore

import (
	"context"
	"encoding/json"
)

// The workspace's operation pipeline, as a port.
//
// The pipeline itself is workspacecore's: it owns the order of the steps, the
// idempotency key rule, the request digest, the replay rule, the human
// decision gate, and what a failure reads outward. A context that has left
// the aggregate still runs its writes through that same pipeline, so it needs
// a way in.
//
// This file is that way in. It is deliberately small: one method, and the
// declarations that method needs. A caller that assembled the pipeline's
// steps itself would hold a second copy of that wiring, and every change to
// the pipeline would then have to be made twice.

// OperationName names one workspace operation.
//
// It is the pipeline's own name type, so an operation named here is the same
// operation the pipeline runs.
type OperationName = operationName

// The operations the member and owner context runs.
const (
	OperationSaveMembers            = operationSaveMembers
	OperationTransferOwner          = operationTransferOwner
	OperationRequestOwnerTransfer   = operationRequestOwnerTransfer
	OperationAcceptOwnerTransfer    = operationAcceptOwnerTransfer
	OperationInviteMember           = operationInviteMember
	OperationAcceptMemberInvitation = operationAcceptMemberInvitation
	OperationMemberAssignment       = operationMemberAssignment
	OperationMemberLifecycle        = operationMemberLifecycle
	OperationRejectOwnerTransfer    = operationRejectOwnerTransfer
	OperationApiKeyProjectGrant     = operationApiKeyProjectGrant
)

// OperationStatus is the status a write answers with.
type OperationStatus = operationStatus

// The statuses a write can answer with.
const (
	OperationStatusOK      = operationStatusOK
	OperationStatusCreated = operationStatusCreated
)

// OperationOutputKind is the kind of output an operation wrote.
type OperationOutputKind = operationOutputKind

// OperationOutputFor returns the kind of output an operation writes.
//
// The mapping belongs to the workspace, so a caller states which operation it
// runs and asks. A caller never has to know which kind of output that
// operation produces.
func OperationOutputFor(name OperationName) OperationOutputKind {
	return operationOutputFor(name)
}

// OperationSpec describes one operation to run.
//
// Body is the request body the pipeline hashes to bind an idempotency key to
// one request. SourceIDs is nil for an operation whose sources need no
// recheck; the pipeline reads that nil to decide whether the operation is
// source sensitive.
type OperationSpec struct {
	Name         OperationName
	Target       string
	Key          string
	Body         any
	Request      AuthorizationRequest
	SourceIDs    func(ChapterWorkspaceTransaction, OperationResult, bool) ([]string, error)
	Output       OperationOutputKind
	CheckProject func(Project) error
}

// OperationOutcome is what a write produced.
//
// The pipeline marshals Value exactly as it marshaled the aggregate's own
// value, so the type inside is never inspected.
type OperationOutcome struct {
	Value  any
	Status OperationStatus
	Output OperationOutputKind
}

// OperationWrite performs the mutation.
//
// It runs inside the pipeline's transaction, so it must not open one. It
// receives the wide transaction and raises the typed port it needs with
// MutationPort.
type OperationWrite func(tx Transaction, project Project) (OperationOutcome, error)

// OperationRunner runs one workspace operation under the shared pipeline.
//
// The runner is the single copy of the pipeline's wiring. A caller supplies
// what only it knows: which operation this is, and what to write.
type OperationRunner interface {
	Run(ctx context.Context, actor Actor, spec OperationSpec, write OperationWrite) (json.RawMessage, int, bool, error)
}

// operationRunner runs operations for a context that left the aggregate.
//
// It holds the aggregate because the pipeline's seams are the aggregate's
// facts: who the actor is, where the records live, and what a failure reads
// outward. The caller never sees that; it sees the port.
type operationRunner struct {
	service *Service
}

// Run runs one operation under the shared pipeline.
func (r *operationRunner) Run(ctx context.Context, actor Actor, spec OperationSpec, write OperationWrite) (json.RawMessage, int, bool, error) {
	return runOperationCore(r.service, ctx, actor, spec, write)
}

// NewOperationRunner returns the runner a context uses to run operations.
//
// The service is the workspace's, and only the composition root holds it: the
// runner hands the caller a port, never the aggregate.
func NewOperationRunner(service *Service) OperationRunner {
	return &operationRunner{service: service}
}
