
package delivery

import (
	"errors"
	"testing"
	"time"
)

type frozenRenderer func(DeliveryInput) ([]byte, error)

func (f frozenRenderer) RenderFrozen(input DeliveryInput) ([]byte, error) { return f(input) }

func preparedSnapshot(t *testing.T) (*MemorySnapshotStore, ReleaseSnapshot) {
	t.Helper()
	store := NewMemorySnapshotStore()
	service := NewReleaseService(store)
	service.now = func() time.Time { return time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC) }
	snapshot, err := service.Prepare(validDeliveryInput())
	if err != nil {
		t.Fatal(err)
	}
	return store, snapshot
}

func TestExportOnlyRendersPassingCurrentSnapshot(t *testing.T) {
	snapshots, snapshot := preparedSnapshot(t)
	exports := NewMemoryExportStore()
	renderer := frozenRenderer(func(input DeliveryInput) ([]byte, error) {
		if input.ProjectID != snapshot.ProjectID {
			t.Fatalf("renderer got project %q", input.ProjectID)
		}
		return []byte("PK\\x03\\x04minimal docx"), nil
	})
	service := NewExportService(snapshots, exports, renderer)
	service.now = func() time.Time { return time.Date(2026, 9, 24, 1, 0, 0, 0, time.UTC) }
	artifact, err := service.Start(snapshot.ProjectID, snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Status != ExportVerified || artifact.FileSHA256 == "" {
		t.Fatalf("artifact = %+v", artifact)
	}
	file, err := service.Download(snapshot.ProjectID, artifact.ID)
	if err != nil || string(file) != "PK\\x03\\x04minimal docx" {
		t.Fatalf("download = %q, %v", file, err)
	}
	file[0] = 'x'
	again, err := service.Download(snapshot.ProjectID, artifact.ID)
	if err != nil || again[0] != 'P' {
		t.Fatalf("download leaked mutable bytes: %q, %v", again, err)
	}
}

func TestExportPersistsFailureButNeverDownloadsIt(t *testing.T) {
	snapshots, snapshot := preparedSnapshot(t)
	service := NewExportService(snapshots, NewMemoryExportStore(), frozenRenderer(func(DeliveryInput) ([]byte, error) { return nil, errors.New("broken docx") }))
	artifact, err := service.Start(snapshot.ProjectID, snapshot.ID)
	if err != nil || artifact.Status != ExportFailed || artifact.FailureCode != "render_failed" {
		t.Fatalf("artifact = %+v, err = %v", artifact, err)
	}
	if _, err := service.Download(snapshot.ProjectID, artifact.ID); !errors.Is(err, ErrExportUnavailable) {
		t.Fatalf("failed file downloaded: %v", err)
	}
}

func TestExportRejectsBlockedAndHistoricalSnapshots(t *testing.T) {
	snapshots, snapshot := preparedSnapshot(t)
	service := NewExportService(snapshots, NewMemoryExportStore(), frozenRenderer(func(DeliveryInput) ([]byte, error) { return []byte("docx"), nil }))
	blocked := snapshot
	blocked.ID, blocked.Check.Status = "blocked", CheckBlocked
	if err := snapshots.Save(blocked); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Start(blocked.ProjectID, blocked.ID); !errors.Is(err, ErrExportPreflightBlocked) {
		t.Fatalf("blocked export = %v", err)
	}
	historical := snapshot
	historical.ID, historical.IsCurrent = "historical", false
	if err := snapshots.Save(historical); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Start(historical.ProjectID, historical.ID); !errors.Is(err, ErrExportStaleInput) {
		t.Fatalf("historical export = %v", err)
	}
}

