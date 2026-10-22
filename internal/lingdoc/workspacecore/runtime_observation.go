package workspacecore

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/clienterror"
	"github.com/google/uuid"
)

// G6 的运行观测（#327 / #382-C）。
//
// 两种事实必须活过产生它的那次事务：
//
//   - 授权拒绝。拒绝会让操作事务回滚，写在同一个事务里的事件会一起消失；
//   - 审计写失败。审计写失败在 PostgreSQL 上会让整个事务进入 aborted 状态，
//     写在同一个事务里的事件同样写不进去。
//
// 所以判定结果不写进事务，而是**带在上下文里**，等事务结束后由一个独立
// 事务写出去。上下文本身不可变，但承载它的 holder 是指针，可以变。

// RuntimeObservationKind names the kind of fact one observation carries.
type RuntimeObservationKind string

const (
	// RuntimeObservationAuthorizationDenial is one refused capability
	// decision.
	//
	// Only denials are recorded. An allow is the normal case and it happens
	// on every read; recording it would bury the denials in the event
	// stream. The existing request/success events already cover the allow
	// side.
	RuntimeObservationAuthorizationDenial RuntimeObservationKind = "authorization_denial"
	// RuntimeObservationAuditWriteFailure is one audit write that did not
	// land while the surrounding operation stayed fail-open.
	RuntimeObservationAuditWriteFailure RuntimeObservationKind = "audit_write_failure"
)

// RuntimeObservation is one fact that must outlive the transaction that
// produced it.
type RuntimeObservation struct {
	Kind       RuntimeObservationKind
	Actor      Actor
	ProjectID  string
	Capability string
	ErrorCode  string
	Reason     string
}

// RuntimeObservationHolder collects the observations one request produced.
//
// The context is immutable, but the holder it carries is not: the holder is a
// pointer, so the code that makes a decision can still add to it after the
// context was built.
type RuntimeObservationHolder struct {
	mu           sync.Mutex
	observations []RuntimeObservation
	// denialRecorded remembers that this request already refused one
	// capability decision. A request stops at its first refusal, so a second
	// refusal in the same request is the observation path asking whether it
	// may read the very project it just failed on. Recording that again
	// would double the count the duty officer reads.
	denialRecorded bool
	// projectVersion and contextRevision are the revision the authorization
	// decision was made on. They stay 0 when no project was read: a refusal
	// must not read the project row first, because that would leak whether
	// the project exists.
	projectVersion  int64
	contextRevision int64
}

// RecordDenial claims the right to record this request's refusal and reports
// whether the caller is the first to do so.
func (h *RuntimeObservationHolder) RecordDenial() bool {
	if h == nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.denialRecorded {
		return false
	}
	h.denialRecorded = true
	return true
}

// Add records one observation.
func (h *RuntimeObservationHolder) Add(observation RuntimeObservation) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.observations = append(h.observations, observation)
}

// Take returns the observations recorded so far and clears the holder, so
// that the same fact is never written twice.
func (h *RuntimeObservationHolder) Take() []RuntimeObservation {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	taken := h.observations
	h.observations = nil
	return taken
}

// SetRevisions remembers the revision the authorization decision was made on.
// The first decision wins: a later decision on another project must not
// overwrite the one the audit row is about.
func (h *RuntimeObservationHolder) SetRevisions(projectVersion, contextRevision int64) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.projectVersion == 0 {
		h.projectVersion = projectVersion
	}
	if h.contextRevision == 0 {
		h.contextRevision = contextRevision
	}
}

// Revisions returns the revision the authorization decision was made on.
func (h *RuntimeObservationHolder) Revisions() (int64, int64) {
	if h == nil {
		return 0, 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.projectVersion, h.contextRevision
}

type runtimeObservationKey struct{}

// WithRuntimeObservationHolder returns a context that carries an observation
// holder, and the holder itself.
//
// It is idempotent: a context that already carries a holder keeps it, so the
// outermost caller and the inner caller observe the same holder and the same
// fact is never written twice.
func WithRuntimeObservationHolder(ctx context.Context) (context.Context, *RuntimeObservationHolder) {
	if holder, ok := ctx.Value(runtimeObservationKey{}).(*RuntimeObservationHolder); ok && holder != nil {
		return ctx, holder
	}
	holder := &RuntimeObservationHolder{}
	return context.WithValue(ctx, runtimeObservationKey{}, holder), holder
}

// RuntimeObservationHolderFromContext returns the holder a context carries.
func RuntimeObservationHolderFromContext(ctx context.Context) (*RuntimeObservationHolder, bool) {
	holder, ok := ctx.Value(runtimeObservationKey{}).(*RuntimeObservationHolder)
	return holder, ok && holder != nil
}

// withRuntimeObservations installs the observation holder around one unit of
// work and returns the function that writes the observations it produced.
//
// The returned function must run **after** the transaction that produced the
// observations has ended. That is the whole point of the holder: a refusal
// rolls its transaction back, and a failed audit write may have poisoned it
// on PostgreSQL, so an observation written inside that transaction would be
// lost exactly when it matters most.
func (s *Service) withRuntimeObservations(ctx context.Context) (context.Context, func()) {
	ctx, holder := WithRuntimeObservationHolder(ctx)
	return ctx, func() { s.emitRuntimeObservations(ctx, holder) }
}

// noteAuthorizationDenial records one refused capability decision.
//
// The capability comes from the refusal itself when the policy wrapped it
// (authorizationDecisionError); otherwise it is the capability the request
// asked for.
func noteAuthorizationDenial(ctx context.Context, request AuthorizationRequest, err error) {
	if err == nil {
		return
	}
	holder, ok := RuntimeObservationHolderFromContext(ctx)
	if !ok {
		return
	}
	capability := request.capability()
	var decision authorizationDecisionError
	if errors.As(err, &decision) && decision.capability != "" {
		capability = decision.capability
	}
	if !holder.RecordDenial() {
		// This request already refused a capability decision. The refusal
		// that reaches here second is the observation path asking whether it
		// may read the very project it just failed on; recording it again
		// would double the count the duty officer reads.
		return
	}
	holder.Add(RuntimeObservation{Kind: RuntimeObservationAuthorizationDenial, Actor: request.Actor,
		ProjectID: request.ProjectID, Capability: capability,
		ErrorCode: runtimeErrorCode(err), Reason: err.Error()})
}

// noteAuditWriteFailure records one audit write that did not land.
//
// Only a fail-open caller records it. A fail-closed caller already fails the
// operation, and an "audit unavailable" alert on top of that failure would be
// noise.
func noteAuditWriteFailure(ctx context.Context, actor Actor, projectID, capability string, err error) {
	if err == nil {
		return
	}
	holder, ok := RuntimeObservationHolderFromContext(ctx)
	if !ok {
		return
	}
	holder.Add(RuntimeObservation{Kind: RuntimeObservationAuditWriteFailure, Actor: actor,
		ProjectID: projectID, Capability: capability,
		ErrorCode: "audit_write_failed", Reason: err.Error()})
}

// runtimeErrorCode answers the stable code of a failure, for the event stream.
func runtimeErrorCode(err error) string {
	if err == nil {
		return ""
	}
	if form, ok := clienterror.FormOf(err); ok && form.Code != "" {
		return form.Code
	}
	return "authorization_denied"
}

// emitRuntimeObservations writes the observations the request produced.
//
// It never changes the caller's outcome: a failure to write an observation is
// dropped, exactly like a fail-open audit.
func (s *Service) emitRuntimeObservations(ctx context.Context, holder *RuntimeObservationHolder) {
	if s == nil || holder == nil {
		return
	}
	observations := holder.Take()
	if len(observations) == 0 {
		return
	}
	_ = s.WriteRuntimeObservations(ctx, observations)
}

// WriteRuntimeObservations stores the observations as runtime events and
// lets the matching alert rules react to them.
//
// The events are written in a transaction of their own, after the caller's
// transaction has ended.
func (s *Service) WriteRuntimeObservations(ctx context.Context, observations []RuntimeObservation) error {
	if s == nil || len(observations) == 0 {
		return nil
	}
	return s.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error {
		store, ok := tx.(runtimeTransaction)
		if !ok {
			return nil
		}
		for _, observation := range observations {
			event, err := s.runtimeObservationEvent(observation)
			if err != nil {
				continue
			}
			if err := store.InsertRuntimeRecord(observation.Actor.TenantID, observation.ProjectID, "event", event.ID, 1, event); err != nil {
				return err
			}
			if observation.Kind != RuntimeObservationAuditWriteFailure || observation.ProjectID == "" {
				continue
			}
			// Only an audit write failure triggers alert matching. A
			// tenant-level audit failure has no project to match a rule
			// against: the event is still recorded, the alert is not.
			//
			// An authorization denial is deliberately **not** matched here.
			// A rule whose action is `pause` would suspend the whole
			// project over a single refused call, and a denial is already
			// visible in the event stream.
			p, err := tx.Project(observation.Actor.TenantID, observation.ProjectID)
			if err != nil {
				continue
			}
			if err := applyRuntimeAlerts(tx, observation.Actor, p, event); err != nil {
				return err
			}
		}
		return nil
	})
}

// runtimeObservationEvent shapes one observation as a runtime event.
func (s *Service) runtimeObservationEvent(observation RuntimeObservation) (RuntimeEvent, error) {
	event := RuntimeEvent{ID: uuid.NewString(), Origin: "runtime",
		TenantAlias:   "tenant." + hashImpactValue(observation.Actor.TenantID)[:16],
		ProjectAlias:  "project." + hashImpactValue(observation.ProjectID)[:16],
		CorrelationID: uuid.NewString(), RuntimeMode: effectiveRuntimeMode(s.runtimeMode),
		CreatedAt: time.Now().UTC()}
	switch observation.Kind {
	case RuntimeObservationAuthorizationDenial:
		event.Category = "permission"
		event.Status = "deny"
		event.ErrorCode = observation.ErrorCode
		event.Capability = safeRuntimeCapability(observation.Capability)
	case RuntimeObservationAuditWriteFailure:
		event.Category = "audit"
		event.Status = "failed"
		event.ErrorCode = "audit_write_failed"
		event.Capability = safeRuntimeCapability(observation.Capability)
	default:
		return RuntimeEvent{}, ErrInvalidRequest
	}
	if !validRuntimeEvent(event) {
		return RuntimeEvent{}, ErrInvalidRequest
	}
	return event, nil
}

// safeRuntimeCapability keeps a capability that the runtime event stream can
// carry safely, and falls back to its action when the resource part is not a
// safe code.
//
// The resource of a capability is caller-supplied and unbounded, so it may
// not be safe to put into an event label. The action alone still says which
// capability was refused.
func safeRuntimeCapability(capability string) string {
	if capability == "" {
		return ""
	}
	if runtimeSafeCode.MatchString(capability) {
		return capability
	}
	action, _, _ := strings.Cut(capability, ":")
	if runtimeSafeCode.MatchString(action) {
		return action
	}
	return ""
}
