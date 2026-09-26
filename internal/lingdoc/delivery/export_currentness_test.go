package delivery

import (
	"errors"
	"testing"
)

func TestExportPropagatesCurrentnessErrorBeforeRendering(t *testing.T) {
	snapshots, snapshot := preparedSnapshot(t)
	exports := NewMemoryExportStore()
	currentnessErr := errors.New("currentness store unavailable")
	renderCalls := 0
	service := NewExportService(
		snapshots,
		exports,
		frozenRenderer(func(DeliveryInput) ([]byte, error) {
			renderCalls++
			return []byte("should not render"), nil
		}),
		FrozenValidatorFunc(fileValidationNotUnderTest),
		CurrentnessFunc(func(DeliveryInput) (bool, error) {
			return false, currentnessErr
		}),
		ExportAccessFunc(allowExport),
	)

	if _, _, err := service.Start("owner", snapshot.ProjectID, snapshot.ID, exportActionKey); !errors.Is(err, currentnessErr) {
		t.Fatalf("Start error = %v, want currentness error %v", err, currentnessErr)
	}
	if renderCalls != 0 {
		t.Fatalf("renderer called %d times while currentness was unknown", renderCalls)
	}
	exports.mu.RLock()
	defer exports.mu.RUnlock()
	if len(exports.exports) != 0 {
		t.Fatalf("currentness failure persisted %d export artifacts", len(exports.exports))
	}
}
