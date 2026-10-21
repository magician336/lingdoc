package workspacecore

import (
	"encoding/json"

	"github.com/Tencent/WeKnora/internal/lingdoc/operationcore"
)

// The workspace's generic mechanisms, as ports.
//
// Each function here is one line over a mechanism the workspace already owns.
// They exist because a context that has left the aggregate still needs them,
// and it must reach them through a name rather than through the aggregate.
//
// The rules they carry do not mention members or owners. That is the point:
// they are the workspace's own, and they stay in one place.

// NewAuthorizationRequest builds the workspace's authorization input.
//
// The action is what the caller wants to do; the resource is the chapter,
// asset, delivery, or issue identifier whose function scope is being checked.
func NewAuthorizationRequest(actor Actor, projectID string, action AuthorizationAction, resource string) AuthorizationRequest {
	return authorizationRequest(actor, projectID, action, resource)
}

// NewAuthorizationRequestFromCapability reads an authorization request back
// from the capability string the callers used before the structured request
// existed.
//
// It fails with ErrInvalidRequest when the capability does not name a known
// action.
func NewAuthorizationRequestFromCapability(actor Actor, projectID, capability string) (AuthorizationRequest, error) {
	return authorizationRequestFromCapability(actor, projectID, capability)
}

// AdaptMutationResult carries a raw operation result across the typed-result
// seam.
//
// The operation stays responsible for authorization, idempotency,
// transactions, and replay ordering; this only carries the serialized value
// and the replay bit across.
func AdaptMutationResult[T any](invoke func() (json.RawMessage, int, bool, error), label string) (T, bool, error) {
	return adaptMutationResult[T](invoke, label)
}

// ExtendedTransaction raises a narrow transaction port back to the wide one.
//
// A focused-only adapter fails closed with ErrInvalidState instead of
// panicking.
func ExtendedTransaction(port any) (Transaction, error) {
	return extendedTransaction(port)
}

// ValidActor reports whether the actor is well formed.
func ValidActor(actor Actor) bool {
	return validActor(actor)
}

// RecordProjectAudit writes one project audit event.
//
// The audit row is the workspace's own record; the caller states what
// happened and the workspace says how it is written.
func RecordProjectAudit(tx Transaction, actor Actor, projectID, action, target, reason string) error {
	return g5Audit(tx, actor, projectID, action, target, reason)
}

// MutationPort returns the typed transaction port for a transaction.
//
// It fails with ErrInvalidState when the transaction does not carry the port.
// A caller that needs a typed port raises it here, inside the transaction
// that the pipeline opened.
func MutationPort[T any](tx Transaction) (T, error) {
	return mutationPort[T](tx)
}

// ValueHash returns the digest of the JSON encoding of the value.
//
// A value that cannot be encoded still has a stable digest: the digest of its
// empty encoding. The workspace's own value hashes rely on that, because they
// hash values they do not control.
func ValueHash(value any) string {
	return operationcore.ValueHash(value)
}
