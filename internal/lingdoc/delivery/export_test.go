package delivery

import (
	"errors"
	"testing"
	"time"
)

type frozenRenderer func(DeliveryInput) ([]byte, error)

func (f frozenRenderer) RenderFrozen(input DeliveryInput) ([]byte, error) { return f(input) }

func allowExport(actorUserID, projectID string) error {
	if actorUserID != "owner" || projectID != "project-1" {
		return errors.New("project access denied")
	}
	return nil
}

func currentExportInput(DeliveryInput) (bool, error) { return true, nil }

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
	service := NewExportService(snapshots, exports, renderer, CurrentnessFunc(currentExportInput), ExportAccessFunc(allowExport))
	service.now = func() time.Time { return time.Date(2026, 9, 24, 1, 0, 0, 0, time.UTC) }
	artifact, err := service.Start("owner", snapshot.ProjectID, snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Status != ExportVerified || artifact.FileSHA256 == "" {
		t.Fatalf("artifact = %+v", artifact)
	}
	file, err := service.Download("owner", snapshot.ProjectID, artifact.ID)
	if err != nil || string(file) != "PK\\x03\\x04minimal docx" {
		t.Fatalf("download = %q, %v", file, err)
	}
	file[0] = 'x'
	again, err := service.Download("owner", snapshot.ProjectID, artifact.ID)
	if err != nil || again[0] != 'P' {
		t.Fatalf("download leaked mutable bytes: %q, %v", again, err)
	}
}

func TestExportPersistsFailureButNeverDownloadsIt(t *testing.T) {
	snapshots, snapshot := preparedSnapshot(t)
	service := NewExportService(snapshots, NewMemoryExportStore(), frozenRenderer(func(DeliveryInput) ([]byte, error) { return nil, errors.New("broken docx") }), CurrentnessFunc(currentExportInput), ExportAccessFunc(allowExport))
	artifact, err := service.Start("owner", snapshot.ProjectID, snapshot.ID)
	if err != nil || artifact.Status != ExportFailed || artifact.FailureCode != "render_failed" {
		t.Fatalf("artifact = %+v, err = %v", artifact, err)
	}
	if _, err := service.Download("owner", snapshot.ProjectID, artifact.ID); !errors.Is(err, ErrExportUnavailable) {
		t.Fatalf("failed file downloaded: %v", err)
	}
}

func TestExportRejectsBlockedAndHistoricalSnapshots(t *testing.T) {
	snapshots, snapshot := preparedSnapshot(t)
	service := NewExportService(snapshots, NewMemoryExportStore(), frozenRenderer(func(DeliveryInput) ([]byte, error) { return []byte("docx"), nil }), CurrentnessFunc(currentExportInput), ExportAccessFunc(allowExport))
	blocked := snapshot
	blocked.ID, blocked.Check.Status = "blocked", CheckBlocked
	if err := snapshots.Save(blocked); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Start("owner", blocked.ProjectID, blocked.ID); !errors.Is(err, ErrExportPreflightBlocked) {
		t.Fatalf("blocked export = %v", err)
	}
	historical := snapshot
	historical.ID, historical.IsCurrent = "historical", false
	if err := snapshots.Save(historical); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Start("owner", historical.ProjectID, historical.ID); !errors.Is(err, ErrExportStaleInput) {
		t.Fatalf("historical export = %v", err)
	}
}

func TestExportRechecksCurrentnessAndAccess(t *testing.T) {
	snapshots, snapshot := preparedSnapshot(t)
	current := true
	service := NewExportService(snapshots, NewMemoryExportStore(), frozenRenderer(func(DeliveryInput) ([]byte, error) {
		current = false
		return []byte("docx"), nil
	}), CurrentnessFunc(func(DeliveryInput) (bool, error) { return current, nil }), ExportAccessFunc(allowExport))
	if _, err := service.Start("owner", snapshot.ProjectID, snapshot.ID); !errors.Is(err, ErrExportStaleInput) {
		t.Fatalf("export after source changed during render = %v", err)
	}
	if _, err := service.Start("other-user", snapshot.ProjectID, snapshot.ID); err == nil {
		t.Fatal("export should require current project access")
	}
}

func TestDownloadRechecksCurrentAccess(t *testing.T) {
	snapshots, snapshot := preparedSnapshot(t)
	allowed := true
	service := NewExportService(snapshots, NewMemoryExportStore(), frozenRenderer(func(DeliveryInput) ([]byte, error) { return []byte("docx"), nil }), CurrentnessFunc(currentExportInput), ExportAccessFunc(func(actorUserID, projectID string) error {
		if allowed {
			return nil
		}
		return errors.New("project access denied")
	}))
	artifact, err := service.Start("owner", snapshot.ProjectID, snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	allowed = false
	if _, err := service.Download("owner", snapshot.ProjectID, artifact.ID); err == nil {
		t.Fatal("download should require current project access")
	}
}
