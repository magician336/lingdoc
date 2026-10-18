package delivery

import (
	"context"
	"errors"
	"testing"
)

func TestTransactionalSnapshotPortRequiresAtomicRuleSetUseAudit(t *testing.T) {
	if _, err := NewTransactionalSnapshotPort(&freezeRecorderOnlyStore{inner: NewMemorySnapshotStore()}); !errors.Is(err, ErrTransactionalPortUnavailable) {
		t.Fatalf("snapshot port error = %v, want ErrTransactionalPortUnavailable", err)
	}
	if _, err := NewTransactionalSnapshotPort(NewMemorySnapshotStore()); err != nil {
		t.Fatalf("memory snapshot store should satisfy the transactional port: %v", err)
	}
}

func TestTransactionalExportPortRequiresAtomicRuleSetUseAudit(t *testing.T) {
	if _, err := NewTransactionalExportPort(&exportRecorderOnlyStore{inner: NewMemoryExportStore()}); !errors.Is(err, ErrTransactionalPortUnavailable) {
		t.Fatalf("export port error = %v, want ErrTransactionalPortUnavailable", err)
	}
	if _, err := NewTransactionalExportPort(NewMemoryExportStore()); err != nil {
		t.Fatalf("memory export store should satisfy the transactional port: %v", err)
	}
	if service := NewExportService(
		NewMemorySnapshotStore(),
		&exportRecorderOnlyStore{inner: NewMemoryExportStore()},
		frozenRenderer(func(DeliveryInput) ([]byte, error) { return []byte("docx"), nil }),
		FrozenValidatorFunc(fileValidationNotUnderTest),
		CurrentnessFunc(currentExportInput),
		ExportAccessFunc(allowExport),
	); service != nil {
		t.Fatal("export service accepted a store without an atomic RuleSet-use port")
	}
}

func TestTransactionalDeliveryPortValidatesThePairAtComposition(t *testing.T) {
	if _, err := NewTransactionalDeliveryPort(NewMemorySnapshotStore(), &exportRecorderOnlyStore{inner: NewMemoryExportStore()}); !errors.Is(err, ErrTransactionalPortUnavailable) {
		t.Fatalf("delivery port error = %v, want ErrTransactionalPortUnavailable", err)
	}
	port, err := NewTransactionalDeliveryPort(NewMemorySnapshotStore(), NewMemoryExportStore())
	if err != nil || port.Snapshots == nil || port.Exports == nil {
		t.Fatalf("valid delivery port = %+v err=%v", port, err)
	}
}

func TestTransactionalCapabilityObserverRecordsMissingPorts(t *testing.T) {
	observer := &transactionalCapabilityObserverRecorder{}
	if _, err := NewTransactionalDeliveryPortWithObserver(
		&freezeRecorderOnlyStore{inner: NewMemorySnapshotStore()},
		&exportRecorderOnlyStore{inner: NewMemoryExportStore()},
		observer,
	); !errors.Is(err, ErrTransactionalPortUnavailable) {
		t.Fatalf("delivery port error = %v, want ErrTransactionalPortUnavailable", err)
	}
	if len(observer.errors) != 1 || observer.errors[0].capability != "snapshot" || observer.errors[0].reason != "missing_atomic_audit_port" {
		t.Fatalf("capability observations = %+v, want one snapshot capability error", observer.errors)
	}
}

type transactionalCapabilityObservation struct{ capability, reason string }

type transactionalCapabilityObserverRecorder struct {
	errors []transactionalCapabilityObservation
}

func (r *transactionalCapabilityObserverRecorder) RecordTransactionalCapabilityError(_ context.Context, capability, reason string) {
	r.errors = append(r.errors, transactionalCapabilityObservation{capability: capability, reason: reason})
}

// These adapters intentionally expose idempotency but not the combined audit
// write. They model a composition mistake that must fail before a request.
type freezeRecorderOnlyStore struct{ inner *MemorySnapshotStore }

func (s *freezeRecorderOnlyStore) Save(snapshot ReleaseSnapshot) error {
	return s.inner.Save(snapshot)
}
func (s *freezeRecorderOnlyStore) Get(projectID, snapshotID string) (ReleaseSnapshot, error) {
	return s.inner.Get(projectID, snapshotID)
}
func (s *freezeRecorderOnlyStore) ReplayFreeze(attempt FreezeAttempt, requestHash string) (ReleaseSnapshot, bool, error) {
	return s.inner.ReplayFreeze(attempt, requestHash)
}
func (s *freezeRecorderOnlyStore) RecordFreeze(snapshot ReleaseSnapshot, attempt FreezeAttempt, requestHash string) (ReleaseSnapshot, bool, error) {
	return s.inner.RecordFreeze(snapshot, attempt, requestHash)
}

type exportRecorderOnlyStore struct{ inner *MemoryExportStore }

func (s *exportRecorderOnlyStore) SaveExport(artifact ExportArtifact) error {
	return s.inner.SaveExport(artifact)
}
func (s *exportRecorderOnlyStore) GetExport(projectID, exportID string) (ExportArtifact, error) {
	return s.inner.GetExport(projectID, exportID)
}
func (s *exportRecorderOnlyStore) ReplayExport(attempt ExportAttempt, requestHash string) (ExportArtifact, bool, error) {
	return s.inner.ReplayExport(attempt, requestHash)
}
func (s *exportRecorderOnlyStore) RecordExport(artifact ExportArtifact, attempt ExportAttempt, requestHash string) (ExportArtifact, bool, error) {
	return s.inner.RecordExport(artifact, attempt, requestHash)
}
