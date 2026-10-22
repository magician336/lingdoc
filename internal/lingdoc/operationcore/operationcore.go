// Package operationcore owns the shared operation pipeline: the order and the
// rules that every mutating workspace operation runs under.
//
// Why this lives in its own package
// ----------------------------------
// The workspace aggregate used to carry the pipeline inside itself. The
// pipeline's body sat in `workspacecore/service_facade.go` next to the
// aggregate's own code, and the same handful of rules were re-derived at
// every place that needed them:
//
//   - the idempotency key length rule, written out at four call sites;
//   - the request body digest, computed once for the main pipeline and once
//     again for the research pipeline with a second helper;
//   - the "same key, different body" conflict test, written out four times;
//   - the human decision gate, whose operation list lived apart from the
//     pipeline that applied it.
//
// Each copy was a place where the copies could drift. Nothing forced the
// research pipeline's replay rule to agree with the main pipeline's replay
// rule, and nothing recorded that the key length rule was the same rule in
// both.
//
// This package makes the pipeline an independent deep module. Its public
// surface is deliberately small:
//
//	Identity, Result, Request, Errors, Ledger, Options
//	Run
//	RequestHash, ValueHash, ValidKey
//	RequireHumanDecision, RequiresHumanDecision
//	MarkReplayPayloadDecodeError, IsReplayPayloadDecodeError
//	AuditWriteFailed
//
// The aggregate fills in the seams (authorize, audit, sources, ledger,
// prepare, validate, store, project) and calls Run. What Run owns is the part
// that is easy to get subtly wrong and impossible to see from one call site:
//
//   - the order of the steps (prepare, source recheck, authorize, audit,
//     idempotency, write, replay);
//   - that authorization precedes replay, so a revoked permission refuses a
//     cached response instead of replaying it;
//   - that the request body digest and the idempotency key have exactly one
//     computation;
//   - that the source recheck happens in a read-only transaction, outside any
//     connection held across policy IO;
//   - that the write transaction carries the authorization, the audit row,
//     the idempotency record and the business write together.
//
// The aggregate keeps the domain rules the pipeline does not own: which
// projects a caller may touch, which sources are readable, what a mutation
// actually writes. The pipeline never re-decides those, and the aggregate
// never re-decides the pipeline's rules.
//
// What this package deliberately does not own
// --------------------------------------------
// The aggregate's own machinery - provenance preparation, provenance
// validation, source pins, reverse lineage, runtime tracing, and the
// critical-audit rows - stays in the aggregate. Those are domain rules about
// *what* an operation records, not about how operations are sequenced. The
// pipeline calls them as seams, in the order it decides.
//
// The error *presentation* also stays with the aggregate. The pipeline decides
// that a conflict is a conflict; the aggregate's sentinel decides what a
// caller reads. The pipeline raises the sentinels it is given, so the outward
// form of every failure is unchanged by this package's existence.
package operationcore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"time"
)

// Idempotency key bounds. An operation key is the caller's handle for one
// operation; a key that is too short collides by accident and a key that is
// too long is not a key at all.
const (
	MinKeyLength = 8
	MaxKeyLength = 128
)

// ValidKey reports whether an idempotency key is usable.
//
// The rule lives here so that every path that accepts an operation key
// accepts the same one.
func ValidKey(key string) bool {
	return len(key) >= MinKeyLength && len(key) <= MaxKeyLength
}

// ValueHash returns the digest of the JSON encoding of value.
//
// A value that cannot be encoded still has a stable digest: the digest of its
// empty encoding. Workspace value hashes (pins, deltas, receipts) rely on
// that, because they hash values they do not control.
func ValueHash(value any) string {
	digest, _ := digestJSON(value)
	return digest
}

// RequestHash returns the digest of an operation's request body.
//
// Unlike ValueHash it reports the encoding failure, because an operation
// whose body cannot be hashed cannot be idempotent: the digest is what binds
// a key to one request.
func RequestHash(body any) (string, error) {
	return digestJSON(body)
}

// digestJSON is the one place a JSON value is digested.
//
// The workspace used to compute this digest in more than one place, with two
// different helpers, so the pipeline's request digest and the research
// pipeline's request digest could disagree about what "the same request"
// means. They are the same function now.
func digestJSON(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		// json.Marshal reports the failure and returns no bytes. The digest of
		// the empty encoding keeps the value hashes stable for values the
		// caller could not encode, exactly as before.
		sum := sha256.Sum256(nil)
		return hex.EncodeToString(sum[:]), err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

// Identity is the identity of one operation attempt: the actor, the
// operation, the target it acts on, and the idempotency key.
//
// The identity is what the ledger stores the result under, so it is also what
// a replay is looked up by.
type Identity[A any] struct {
	Actor             A
	Operation, Target string
	Key               string
}

// Authorization is the structured authorization request the pipeline makes
// for one operation.
type Authorization[A any] struct {
	Actor     A
	ProjectID string
	Action    string
	Resource  string
}

// Capability returns the audit capability string for the request.
//
// The capability is the category a caller was asking for, so it is what the
// audit row records.
func (a Authorization[A]) Capability() string {
	if a.Resource == "" {
		return a.Action
	}
	return a.Action + ":" + a.Resource
}

// Result is a stored operation outcome: the request digest that produced it,
// the response body, the status, and the kind of output the operation wrote.
type Result struct {
	BodyHash string
	Body     json.RawMessage
	Status   int
	Output   string
}

// Options are the transaction options the pipeline chooses.
type Options struct {
	// ReadOnly marks a transaction that only reads.
	ReadOnly bool
	// RetryLocks lets a write transaction retry on lock contention.
	RetryLocks bool
}

// Errors are the workspace's sentinels the pipeline raises.
//
// The pipeline owns *when* a failure happens. The workspace owns how the
// failure reads outward, so the sentinels are handed in rather than declared
// here: a conflict raised by this package must be the very same error value
// the workspace raised before, or a caller's error check would change.
type Errors struct {
	InvalidRequest      error
	IdempotencyConflict error
	VersionConflict     error
	InvalidState        error
	// HumanDecisionRequired is raised when the caller may not act for itself.
	HumanDecisionRequired error
}

// Ledger is the store of completed operations.
//
// A ledger is bound to one transaction, so the pipeline cannot read one
// operation's record outside the transaction that is about to write it.
type Ledger[A any] interface {
	// Lookup returns the stored result of a previous attempt.
	Lookup(id Identity[A]) (Result, bool, error)
	// Save stores the result of a completed attempt.
	Save(id Identity[A], result Result) error
}

// Request is one operation to run: what it is, and the seams it runs on.
//
// The declaration half of the request is the operation's identity, its body,
// the capability it needs, and the kind of output it writes. The seam half is
// the aggregate's side of the work: the steps the pipeline sequences but the
// aggregate implements.
type Request[A any, Tx any, P any] struct {
	// Identity is the operation's identity, including its idempotency key.
	Identity Identity[A]
	// Authorization is the authorization the operation needs.
	Authorization Authorization[A]
	// Body is the request body. It is the pipeline's input for the request
	// digest.
	Body any
	// Output is the kind of output the operation writes.
	Output string
	// SourceSensitive marks an operation whose sources must be rechecked
	// before the operation is allowed to run.
	SourceSensitive bool
	// HumanGate declares whether this operation needs the human decision
	// gate. The caller knows: the gate is the domain's policy.
	HumanGate HumanGate
	// Errors are the workspace sentinels the pipeline raises.
	Errors Errors

	// Transaction runs fn inside a storage transaction with the given
	// options.
	Transaction func(ctx context.Context, options Options, fn func(tx Tx) error) error
	// ValidActor reports whether the actor may act at all. A request without a
	// usable actor is refused before anything else happens.
	ValidActor func(actor A) bool
	// AuditWriteFailed translates a failed audit write into the error the
	// caller reads. The pipeline decides that a failed audit write fails the
	// operation; the aggregate decides what that failure reads.
	AuditWriteFailed func(cause error) error
	// Ledger opens the operation ledger for a transaction.
	Ledger func(tx Tx) (Ledger[A], error)
	// AuthorizeTenant decides a tenant-scoped operation.
	AuthorizeTenant func(ctx context.Context, tx Tx, request Authorization[A]) error
	// AuthorizeProject decides a project-scoped operation and returns the
	// project it decided on.
	AuthorizeProject func(ctx context.Context, tx Tx, request Authorization[A]) (P, error)
	// RecordAudit writes the authorization decision onto the operation's
	// transaction.
	//
	// This is the **fail-closed** hook: the pipeline calls it when the
	// operation is allowed to proceed, and a failed audit write fails the
	// operation.
	RecordAudit func(ctx context.Context, tx Tx, projectID, capability string, authErr error) error
	// RecordDeniedAudit writes the authorization decision of a **refused**
	// operation onto the operation's transaction.
	//
	// The refusal path is fail-open: the operation is refused anyway, so a
	// failed audit write changes nothing about the outcome. It is a separate
	// hook because the two paths have different consequences, and the
	// difference must not be inferred from how the caller discards the
	// error: a fail-closed caller must not raise an "audit unavailable"
	// alert on top of the failure it already reports.
	//
	// A request without this hook falls back to RecordAudit.
	RecordDeniedAudit func(ctx context.Context, tx Tx, projectID, capability string, authErr error) error
	// CheckSources revalidates the sources an operation depends on. It runs
	// outside every transaction, because source policy IO must not hold a
	// connection.
	CheckSources func(ctx context.Context, projectID string, sourceIDs []string) error
	// SourceIDs resolves the sources an operation depends on, from the
	// operation's input or from a stored replay.
	SourceIDs func(tx Tx, previous Result, found bool) ([]string, error)
	// PrepareCached prepares the operation and reports whether a cached
	// result was found.
	PrepareCached func(ctx context.Context, id Identity[A], body any) (context.Context, bool, error)
	// Prepare prepares the operation when no cached result was found.
	Prepare func(ctx context.Context, id Identity[A], body any) (context.Context, error)
	// RequireInverseCaller refuses an operation that did not come from the
	// path that is allowed to call it.
	RequireInverseCaller func(ctx context.Context, body any) error
	// Validate applies the operation's own validation inside the write
	// transaction.
	Validate func(ctx context.Context, tx Tx, project P) error
	// CheckProject applies the operation's precondition to the project.
	CheckProject func(project P) error
	// StorePins records the operation's source pins.
	StorePins func(ctx context.Context, tx Tx, projectID string) error
	// Write performs the operation's mutation and returns the written value,
	// the kind of output it produced, and the status it answers with.
	Write func(ctx context.Context, tx Tx, project P) (value any, output string, status int, err error)
	// StoreReverseSources records the reverse lineage of the written value.
	StoreReverseSources func(ctx context.Context, tx Tx, value any) error
	// StoreProvenance records the operation's provenance proof.
	StoreProvenance func(ctx context.Context, tx Tx, id Identity[A], result Result, value any) error
	// Project renders the stored result for the caller.
	Project func(ctx context.Context, operation, projectID string, body json.RawMessage) (json.RawMessage, error)
	// RuntimeGate applies the runtime mode gate inside the write
	// transaction.
	RuntimeGate func(ctx context.Context, tx Tx, project P, operation string) error
	// AfterWrite performs the post-write bookkeeping inside the write
	// transaction.
	//
	// The project is the one the write was performed on, so a domain can record
	// what only that project knows.
	AfterWrite func(ctx context.Context, tx Tx, project P, projectID, operation, target string, status int, value any, elapsed time.Duration) error
	// ObserveFailure records a failed operation.
	ObserveFailure func(ctx context.Context, projectID, operation string, cause error, elapsed time.Duration)
	// Basis returns the comparable basis of an authorized project. The
	// pipeline refuses an operation whose basis moved between the source
	// recheck and the write.
	Basis func(project P) string
}

// Outcome is the result of a completed operation.
type Outcome struct {
	// Body is the response the caller reads.
	Body json.RawMessage
	// Status is the response status.
	Status int
	// Replayed reports that the response was replayed from a stored record
	// rather than produced by this call.
	Replayed bool
}

// Run executes one operation under the shared pipeline.
//
// The order of the steps is the pipeline's knowledge and the reason this
// package exists:
//
//  1. the idempotency key is checked;
//  2. the human decision gate is applied to the operations that need one;
//  3. the request body is digested - once, here;
//  4. a source-sensitive operation rechecks its sources in a read-only
//     transaction, and then rechecks them again outside any transaction;
//  5. the write transaction authorizes, audits, checks the idempotency
//     record, and either replays the stored result or performs the write;
//  6. the stored result is projected for the caller.
//
// recordDeniedAudit writes the audit row of a refused operation.
//
// The refusal path is fail-open, so it uses the request's dedicated
// fail-open hook when there is one. A request that only knows the
// fail-closed hook falls back to it: the refusal already decides the
// outcome, and the row is written on the same transaction either way.
func recordDeniedAudit[A any, Tx any, P any](ctx context.Context, request Request[A, Tx, P], tx Tx, projectID, capability string, authErr error) error {
	if request.RecordDeniedAudit != nil {
		return request.RecordDeniedAudit(ctx, tx, projectID, capability, authErr)
	}
	return request.RecordAudit(ctx, tx, projectID, capability, authErr)
}

// Authorization precedes replay on purpose. A caller whose permission was
// revoked must be refused, not handed the cached response of an operation
// they may no longer perform.
func Run[A any, Tx any, P any](ctx context.Context, request Request[A, Tx, P]) (Outcome, error) {
	identity := request.Identity
	operation, target, key := identity.Operation, identity.Target, identity.Key
	body, projectID := request.Body, request.Authorization.ProjectID
	capability := request.Authorization.Capability()

	if request.ValidActor != nil && !request.ValidActor(identity.Actor) {
		return Outcome{}, request.Errors.InvalidRequest
	}
	if !ValidKey(key) {
		return Outcome{}, request.Errors.InvalidRequest
	}
	if request.RequireInverseCaller != nil {
		if err := request.RequireInverseCaller(ctx, body); err != nil {
			return Outcome{}, err
		}
	}
	switch request.HumanGate {
	case HumanGateRequired:
		if err := RequireHumanDecision(ctx, request.Errors.HumanDecisionRequired); err != nil {
			return Outcome{}, err
		}
	case HumanGateNotRequired:
		// The caller may act for themselves.
	default:
		// The caller did not say. Refusing is the safe direction: a check
		// that was never declared must not be assumed absent.
		return Outcome{}, request.Errors.InvalidRequest
	}
	started := time.Now()
	hash, err := RequestHash(body)
	if err != nil {
		return Outcome{}, request.Errors.InvalidRequest
	}
	// Not every domain has a preparation step. A seam that is not filled in is
	// a step the domain does not have, not a step that must be faked.
	if request.PrepareCached != nil {
		prepared, cached, err := request.PrepareCached(ctx, identity, body)
		if err != nil {
			return Outcome{Replayed: IsReplayPayloadDecodeError(err)}, err
		}
		ctx = prepared
		if !cached && request.Prepare != nil {
			ctx, err = request.Prepare(ctx, identity, body)
			if err != nil {
				return Outcome{}, err
			}
		}
	} else if request.Prepare != nil {
		ctx, err = request.Prepare(ctx, identity, body)
		if err != nil {
			return Outcome{}, err
		}
	}

	var result Result
	var replayed bool
	var checkedProject P
	var checkedSources []string
	var checkedFound bool
	if request.SourceSensitive {
		// Source policies may query the same single-connection SQLite pool.
		// Release this snapshot before calling them, then revalidate its basis
		// in the final write transaction. Never hold a transaction across
		// policy IO.
		err = request.Transaction(ctx, Options{ReadOnly: true}, func(tx Tx) error {
			ledger, err := request.Ledger(tx)
			if err != nil {
				return err
			}
			checkedProject, err = request.AuthorizeProject(ctx, tx, request.Authorization)
			if err != nil {
				return err
			}
			if request.CheckProject != nil {
				if err := request.CheckProject(checkedProject); err != nil {
					return err
				}
			}
			previous, found, err := ledger.Lookup(identity)
			if err != nil {
				return err
			}
			if found && previous.BodyHash != hash {
				return request.Errors.IdempotencyConflict
			}
			checkedFound = found
			if request.SourceIDs != nil {
				checkedSources, err = request.SourceIDs(tx, previous, found)
			}
			if IsReplayPayloadDecodeError(err) {
				replayed = true
			}
			return err
		})
		if err != nil {
			return Outcome{Replayed: replayed}, err
		}
		// Replays reuse the saved result, not the old authorization decision:
		// revocation must reject a replay before any cached body is returned.
		// Denial preserves the record, so reauthorization can replay it
		// unchanged.
		if request.CheckSources != nil {
			if err := request.CheckSources(ctx, projectID, checkedSources); err != nil {
				return Outcome{}, err
			}
		}
	}

	err = request.Transaction(ctx, Options{RetryLocks: RetryLocks(operation)}, func(tx Tx) error {
		ledger, err := request.Ledger(tx)
		if err != nil {
			return err
		}
		var project P
		if projectID == "" {
			if authErr := request.AuthorizeTenant(ctx, tx, request.Authorization); authErr != nil {
				_ = recordDeniedAudit(ctx, request, tx, projectID, capability, authErr)
				return authErr
			}
		} else {
			var authErr error
			project, authErr = request.AuthorizeProject(ctx, tx, request.Authorization)
			if authErr != nil {
				_ = recordDeniedAudit(ctx, request, tx, projectID, capability, authErr)
				return authErr
			}
		}
		if request.CheckProject != nil {
			if err := request.CheckProject(project); err != nil {
				return err
			}
		}
		if err := request.RecordAudit(ctx, tx, projectID, capability, nil); err != nil {
			// The audit row is part of the operation's atomic boundary, so a
			// failed audit write fails the operation (fail-closed) - but it
			// must not escape as a raw storage error, because the shared
			// transport mapper would answer a valid request with a bare 500.
			return request.AuditWriteFailed(err)
		}
		if request.RuntimeGate != nil {
			if err := request.RuntimeGate(ctx, tx, project, operation); err != nil {
				return err
			}
		}
		previous, found, err := ledger.Lookup(identity)
		if err != nil {
			return err
		}
		if found && previous.BodyHash != hash {
			return request.Errors.IdempotencyConflict
		}
		if request.Validate != nil {
			if err := request.Validate(ctx, tx, project); err != nil {
				return err
			}
		}
		if request.SourceSensitive {
			var sourceIDs []string
			if request.SourceIDs != nil {
				sourceIDs, err = request.SourceIDs(tx, previous, found)
			}
			if IsReplayPayloadDecodeError(err) {
				replayed = true
			}
			if err != nil {
				return err
			}
			if request.Basis(project) != request.Basis(checkedProject) || found != checkedFound || !slices.Equal(sourceIDs, checkedSources) {
				return request.Errors.VersionConflict
			}
		}
		if found {
			result, replayed = previous, true
			return nil
		}
		if request.StorePins != nil {
			if err := request.StorePins(ctx, tx, projectID); err != nil {
				return err
			}
		}
		value, output, status, err := request.Write(ctx, tx, project)
		if err != nil {
			return err
		}
		if output == "" {
			output = request.Output
		}
		if output != request.Output {
			return request.Errors.InvalidState
		}
		if request.StoreReverseSources != nil {
			if err := request.StoreReverseSources(ctx, tx, value); err != nil {
				return err
			}
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		result = Result{BodyHash: hash, Body: raw, Status: status, Output: output}
		if request.StoreProvenance != nil {
			if err := request.StoreProvenance(ctx, tx, identity, result, value); err != nil {
				return err
			}
		}
		if request.AfterWrite != nil {
			if err := request.AfterWrite(ctx, tx, project, projectID, operation, target, status, value, time.Since(started)); err != nil {
				return err
			}
		}
		return ledger.Save(identity, result)
	})
	if err != nil {
		if request.ObserveFailure != nil {
			request.ObserveFailure(ctx, projectID, operation, err, time.Since(started))
		}
		return Outcome{Replayed: replayed}, err
	}
	visible, err := request.Project(ctx, operation, projectID, result.Body)
	if err != nil {
		return Outcome{}, err
	}
	return Outcome{Body: visible, Status: result.Status, Replayed: replayed}, nil
}

// retryLockedOperations are the operations whose write transaction may retry
// on lock contention.
//
// The rule used to be written out inline at the transaction call, where the
// operation names were spelled again. It lives here so that the set of
// retrying operations has one owner.
var retryLockedOperations = map[string]bool{
	"saveSpec":                true,
	"createProject":           true,
	"saveTemplateCopy":        true,
	"bindTemplateCopyRuleSet": true,
	"reverseChangeSet":        true,
}

// RetryLocks reports whether an operation's write transaction may retry on
// lock contention.
func RetryLocks(operation string) bool {
	return retryLockedOperations[operation]
}
