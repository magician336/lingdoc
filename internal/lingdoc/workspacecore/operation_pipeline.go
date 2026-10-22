package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/operationcore"
)

// The workspace composes the shared operation pipeline.
//
// The pipeline's order and rules live in `operationcore`. This file is the
// seam between the two: it fills the pipeline's declarations with the
// workspace's facts and hands the pipeline back the same errors, the same
// status and the same body the workspace produced before.
//
// The seam decides nothing. The order of the steps, the idempotency key
// rule, the request digest, the human decision gate and the replay rule all
// belong to the pipeline. What the seam supplies is the domain facts the
// pipeline cannot know on its own: who the actor is, what the project is,
// where the operation records live, and what a failure reads outward.

// operationLedger adapts the workspace's operation store to the shared
// pipeline's ledger.
//
// The pipeline reads and writes the operation record through this interface
// so that it cannot read a record outside the transaction that is about to
// write it. The workspace's port already has that shape; this adapter only
// renames the two calls.
type operationLedger struct {
	operations OperationTransaction
}

func (l operationLedger) Lookup(id operationcore.Identity[Actor]) (operationcore.Result, bool, error) {
	previous, found, err := l.operations.Operation(OperationIdentity{
		Actor:     id.Actor,
		Operation: id.Operation,
		Target:    id.Target,
		Key:       id.Key,
	})
	if err != nil {
		return operationcore.Result{}, false, err
	}
	if !found {
		return operationcore.Result{}, false, nil
	}
	return operationcore.Result{
		BodyHash: previous.BodyHash,
		Body:     previous.Body,
		Status:   previous.Status,
		Output:   string(previous.Output),
	}, true, nil
}

func (l operationLedger) Save(id operationcore.Identity[Actor], result operationcore.Result) error {
	return l.operations.SaveOperation(OperationIdentity{
		Actor:     id.Actor,
		Operation: id.Operation,
		Target:    id.Target,
		Key:       id.Key,
	}, OperationResult{
		BodyHash: result.BodyHash,
		Body:     result.Body,
		Status:   result.Status,
		Output:   operationOutputKind(result.Output),
	})
}

// pipelineAuthorizationRequest reads the pipeline's authorization back into
// the workspace's shape.
//
// The round trip is exact: the action and the resource are the same strings
// the workspace declared, so the capability the audit records is the
// capability the workspace has always recorded.
func pipelineAuthorizationRequest(request operationcore.Authorization[Actor]) AuthorizationRequest {
	return AuthorizationRequest{
		Actor:     request.Actor,
		ProjectID: request.ProjectID,
		Action:    AuthorizationAction(request.Action),
		Resource:  request.Resource,
	}
}

// workspaceHumanGate says whether one of the workspace's operations needs the
// human decision gate.
//
// The list is the workspace's own: what changes a project is the workspace's
// judgement, and the research domain decides for its operations separately.
func workspaceHumanGate(operation string) operationcore.HumanGate {
	if workspaceHumanDecisionOperations()[operation] {
		return operationcore.HumanGateRequired
	}
	return operationcore.HumanGateNotRequired
}

// projectBasis is the comparable basis of an authorized project.
//
// The pipeline refuses an operation whose basis moved between the source
// recheck and the write. The workspace's basis is the project version and the
// research-conditions revision: a change to either means the source recheck
// no longer describes the same project.
func projectBasis(p Project) string {
	return strconv.FormatInt(p.ProjectVersion, 10) + ":" + strconv.FormatInt(p.SpecRevision, 10)
}

// runOperationThroughPipeline runs one operation under the shared pipeline.
//
// The signature and the meaning of every return value are unchanged from the
// pipeline this replaces: the body the caller reads, the HTTP status, whether
// the response was replayed, and the error. What changed is that the steps
// are now the shared pipeline's steps.
//
// This is a thin wrapper. It adapts the typed descriptor, the typed
// transaction port and the typed result to the untyped core, so that the
// core holds the only copy of the pipeline's wiring.
func runOperationThroughPipeline[T any, I any, R any](
	s *Service,
	ctx context.Context,
	actor Actor,
	operation operationDescriptor[I],
	port func(Transaction) (T, error),
	write func(T, Project) (operationResult[R], error),
) (json.RawMessage, int, bool, error) {
	return runOperationCore(
		ctx, s, actor, operationSpecOf(operation),
		func(tx Transaction, project Project) (OperationOutcome, error) {
			mutation, err := port(tx)
			if err != nil {
				return OperationOutcome{}, err
			}
			written, err := write(mutation, project)
			if err != nil {
				return OperationOutcome{}, err
			}
			return OperationOutcome{Value: written.Value, Status: written.Status, Output: written.Output}, nil
		},
	)
}

// operationSpecOf reads the typed descriptor as the untyped specification the
// core runs.
//
// SourceIDs stays nil when the descriptor has no source decoder, because the
// core reads that nil to decide whether the operation is source sensitive.
func operationSpecOf[I any](operation operationDescriptor[I]) OperationSpec {
	spec := OperationSpec{
		Name:         operation.Name,
		Target:       operation.Target,
		Key:          operation.Key,
		Body:         operation.Input,
		Request:      operation.Request,
		Output:       operation.Output,
		CheckProject: operation.CheckProject,
	}
	if operation.SourceIDs != nil {
		spec.SourceIDs = func(tx ChapterWorkspaceTransaction, previous OperationResult, found bool) ([]string, error) {
			return operation.SourceIDs(tx, operation, previous, found)
		}
	}
	return spec
}

// runOperationCore runs one operation under the shared pipeline.
//
// The core holds the only copy of the pipeline's wiring: which seams the
// pipeline runs on, in which order, and what a failure reads outward. A
// caller supplies the operation and the write; the core supplies everything
// else.
//
// The write closure receives the wide transaction rather than a typed port,
// so the core stays free of the caller's port types. A caller that needs a
// typed port raises it with MutationPort inside the write, which is where the
// port assertion happened before.
func runOperationCore(
	ctx context.Context,
	s *Service,
	actor Actor,
	spec OperationSpec,
	write OperationWrite,
) (json.RawMessage, int, bool, error) {
	op, target, key := string(spec.Name), spec.Target, spec.Key
	body, projectID := spec.Body, spec.Request.ProjectID

	outcome, err := operationcore.Run(ctx, operationcore.Request[Actor, Transaction, Project]{
		Identity: operationcore.Identity[Actor]{
			Actor:     actor,
			Operation: op,
			Target:    target,
			Key:       key,
		},
		Authorization: operationcore.Authorization[Actor]{
			Actor:     actor,
			ProjectID: projectID,
			Action:    string(spec.Request.Action),
			Resource:  spec.Request.Resource,
		},
		Body:            body,
		Output:          string(spec.Output),
		SourceSensitive: spec.SourceIDs != nil,
		HumanGate:       workspaceHumanGate(op),
		Errors: operationcore.Errors{
			InvalidRequest:        ErrInvalidRequest,
			IdempotencyConflict:   ErrIdempotencyConflict,
			VersionConflict:       ErrVersionConflict,
			InvalidState:          ErrInvalidState,
			HumanDecisionRequired: ErrHumanDecisionRequired,
		},
		Transaction: func(ctx context.Context, options operationcore.Options, fn func(tx Transaction) error) error {
			return s.repository.Transaction(ctx, TransactionOptions{
				ReadOnly:   options.ReadOnly,
				RetryLocks: options.RetryLocks,
			}, fn)
		},
		ValidActor: func(actor Actor) bool { return validActor(actor) },
		AuditWriteFailed: func(cause error) error {
			return auditWriteFailed(cause)
		},
		Ledger: func(tx Transaction) (operationcore.Ledger[Actor], error) {
			operations, err := mutationPort[OperationTransaction](tx)
			if err != nil {
				return nil, err
			}
			return operationLedger{operations: operations}, nil
		},
		AuthorizeTenant: func(ctx context.Context, tx Transaction, request operationcore.Authorization[Actor]) error {
			return s.authorizeTenant(ctx, tx, pipelineAuthorizationRequest(request))
		},
		AuthorizeProject: func(ctx context.Context, tx Transaction, request operationcore.Authorization[Actor]) (Project, error) {
			return s.authorizeProject(ctx, tx, pipelineAuthorizationRequest(request))
		},
		RecordAudit: func(ctx context.Context, tx Transaction, projectID, capability string, authErr error) error {
			return s.recordAuditInTransaction(ctx, tx, actor, projectID, capability, authErr)
		},
		CheckSources: func(ctx context.Context, projectID string, sourceIDs []string) error {
			return s.checkSources(ctx, actor, projectID, sourceIDs)
		},
		SourceIDs: func(tx Transaction, previous operationcore.Result, found bool) ([]string, error) {
			if spec.SourceIDs == nil {
				return nil, nil
			}
			return spec.SourceIDs(tx, OperationResult{
				BodyHash: previous.BodyHash,
				Body:     previous.Body,
				Status:   previous.Status,
				Output:   operationOutputKind(previous.Output),
			}, found)
		},
		PrepareCached: func(ctx context.Context, id operationcore.Identity[Actor], body any) (context.Context, bool, error) {
			return s.prepareCachedChangeOperation(ctx, actor, spec.Request, OperationIdentity{
				Actor:     id.Actor,
				Operation: id.Operation,
				Target:    id.Target,
				Key:       id.Key,
			}, body)
		},
		Prepare: func(ctx context.Context, id operationcore.Identity[Actor], body any) (context.Context, error) {
			return s.prepareProvenanceOperation(ctx, actor, id.Operation, id.Target, projectID, body)
		},
		RequireInverseCaller: func(ctx context.Context, body any) error {
			return requireInverseCaller(ctx, body)
		},
		Validate: func(ctx context.Context, tx Transaction, p Project) error {
			return s.validateProvenanceOperation(ctx, tx, actor, p, op)
		},
		CheckProject: spec.CheckProject,
		StorePins: func(ctx context.Context, tx Transaction, projectID string) error {
			return s.storeOperationPins(ctx, tx, projectID)
		},
		Write: func(ctx context.Context, tx Transaction, p Project) (any, string, int, error) {
			written, err := write(tx, p)
			if err != nil {
				return nil, "", 0, err
			}
			return written.Value, string(written.Output), written.Status.HTTPStatus(), nil
		},
		StoreReverseSources: func(ctx context.Context, tx Transaction, value any) error {
			return storeReverseSources(ctx, tx, value)
		},
		StoreProvenance: func(ctx context.Context, tx Transaction, id operationcore.Identity[Actor], result operationcore.Result, value any) error {
			return storeOperationProvenance(tx, OperationIdentity{
				Actor:     id.Actor,
				Operation: id.Operation,
				Target:    id.Target,
				Key:       id.Key,
			}, OperationResult{
				BodyHash: result.BodyHash,
				Body:     result.Body,
				Status:   result.Status,
				Output:   operationOutputKind(result.Output),
			}, value)
		},
		Project: func(ctx context.Context, operation, projectID string, body json.RawMessage) (json.RawMessage, error) {
			return s.operationProvenanceProjection(ctx, actor, operation, projectID, body)
		},
		RuntimeGate: func(ctx context.Context, tx Transaction, p Project, operation string) error {
			if s.runtimeMode == "" {
				return nil
			}
			return RuntimeGate(tx, actor, p, operation, s.runtimeMode)
		},
		AfterWrite: func(ctx context.Context, tx Transaction, p Project, projectID, operation, target string, status int, value any, elapsed time.Duration) error {
			return s.afterOperationWrite(ctx, tx, actor, p, projectID, operation, target, status, elapsed)
		},
		ObserveFailure: func(ctx context.Context, projectID, operation string, cause error, elapsed time.Duration) {
			s.observeOperationFailure(ctx, actor, projectID, operation, cause, elapsed)
		},
		Basis: func(p Project) string {
			return projectBasis(p)
		},
	})
	if err != nil {
		return nil, 0, outcome.Replayed, err
	}
	return outcome.Body, outcome.Status, outcome.Replayed, nil
}

// afterOperationWrite performs the bookkeeping that follows a successful
// write, still on the write transaction.
//
// The critical audit and the runtime trace are the workspace's own records,
// so they belong to the workspace. The pipeline decides that they happen
// here; the workspace decides what they say.
func (s *Service) afterOperationWrite(ctx context.Context, tx Transaction, actor Actor, project Project, projectID, operation, target string, status int, elapsed time.Duration) error {
	if _, ok := tx.(gormTransaction); !ok || projectID == "" {
		return nil
	}
	current, err := tx.Project(actor.TenantID, projectID)
	if err != nil {
		return err
	}
	if requiresGenericCriticalAudit(operation) {
		if err := recordProjectAuditEvent(tx, actor, projectID, "workspace."+operation, target, ""); err != nil {
			return err
		}
	}
	if s.runtimeMode == "" {
		return nil
	}
	trace := RuntimeTraceFromContext(ctx)
	trace.RuntimeMode = s.runtimeMode
	outcome := "success"
	if status == 409 {
		outcome = "conflict"
	} else if status >= 400 {
		outcome = "rejected"
	}
	return RecordRuntimeOperationContext(WithRuntimeTrace(ctx, trace), tx, actor, current, operation, outcome, elapsed.Milliseconds())
}

// observeOperationFailure records a failed operation.
//
// The failure is observed after its transaction has rolled back, so the
// observation is the only thing the failure leaves behind.
func (s *Service) observeOperationFailure(ctx context.Context, actor Actor, projectID, operation string, cause error, elapsed time.Duration) {
	if s.runtimeMode == "" || projectID == "" {
		return
	}
	outcome, code := "failed", "operation_failed"
	switch {
	case errors.Is(cause, ErrVersionConflict):
		outcome, code = "conflict", "version_conflict"
	case errors.Is(cause, ErrSourceUnavailable):
		outcome, code = "revoked", "source_access_denied"
	case errors.Is(cause, ErrNotFound):
		outcome, code = "rejected", "access_denied"
	}
	trace := RuntimeTraceFromContext(ctx)
	trace.ErrorCode = code
	_ = s.ObserveRuntime(WithRuntimeTrace(ctx, trace), actor, projectID, operation, outcome, elapsed.Milliseconds())
}
