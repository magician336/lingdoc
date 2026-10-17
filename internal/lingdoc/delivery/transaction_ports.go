package delivery

import (
	"errors"
	"fmt"
)

// ErrTransactionalPortUnavailable indicates a composition error. A governed
// delivery service must be wired to a store that can commit its artifact,
// idempotency record, and RuleSet-use audit as one operation.
var ErrTransactionalPortUnavailable = errors.New("transactional delivery port unavailable")

// TransactionalSnapshotPort is the required application port for release
// snapshots. RuleSetUseFreezeRecorder is deliberately part of the contract so
// an audit write cannot be deferred until after the snapshot commit.
type TransactionalSnapshotPort interface {
	SnapshotStore
	FreezeRecorder
	RuleSetUseFreezeRecorder
}

// TransactionalExportPort is the required application port for export
// artifacts. It combines artifact persistence, action idempotency, and the
// governed RuleSet-use audit write in one adapter boundary.
type TransactionalExportPort interface {
	ExportStore
	ExportRecorder
	RuleSetUseExportRecorder
}

// TransactionalDeliveryPort is the composition-root value for a governed
// release/export pair. The two stores may use different adapters, but both
// must expose their atomic artifact+idempotency+audit write before the pair is
// admitted to the application graph.
type TransactionalDeliveryPort struct {
	Snapshots TransactionalSnapshotPort
	Exports   TransactionalExportPort
}

// NewTransactionalDeliveryPort validates both sides of the governed delivery
// graph in one composition call.
func NewTransactionalDeliveryPort(snapshots SnapshotStore, exports ExportStore) (TransactionalDeliveryPort, error) {
	snapshotPort, err := NewTransactionalSnapshotPort(snapshots)
	if err != nil {
		return TransactionalDeliveryPort{}, err
	}
	exportPort, err := NewTransactionalExportPort(exports)
	if err != nil {
		return TransactionalDeliveryPort{}, err
	}
	return TransactionalDeliveryPort{Snapshots: snapshotPort, Exports: exportPort}, nil
}

// NewTransactionalSnapshotPort validates the release persistence capability
// during composition, before any HTTP request can discover a missing audit
// transaction.
func NewTransactionalSnapshotPort(store SnapshotStore) (TransactionalSnapshotPort, error) {
	port, ok := store.(TransactionalSnapshotPort)
	if !ok {
		return nil, fmt.Errorf("%w: snapshot store must implement atomic RuleSet-use recording", ErrTransactionalPortUnavailable)
	}
	return port, nil
}

// NewTransactionalExportPort validates the export persistence capability
// during composition, before rendering or idempotency state can be touched.
func NewTransactionalExportPort(store ExportStore) (TransactionalExportPort, error) {
	port, ok := store.(TransactionalExportPort)
	if !ok {
		return nil, fmt.Errorf("%w: export store must implement atomic RuleSet-use recording", ErrTransactionalPortUnavailable)
	}
	return port, nil
}
