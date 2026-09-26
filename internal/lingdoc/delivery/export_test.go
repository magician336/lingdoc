package delivery

import (
	"errors"
	"testing"
	"time"
)

type frozenRenderer func(DeliveryInput) ([]byte, error)

func (f frozenRenderer) RenderFrozen(input DeliveryInput) ([]byte, error) { return f(input) }

// fileValidationNotUnderTest 让编排用例跳过文件校验。它叫这个名字是因为这些用例
// 问的是「谁在什么时候被调用、什么被落存」，它们喂给渲染器的是 "docx" 这种占位
// 字节，真校验器本来就该拒绝。文件本身的形状由 docx 包与 export_action_test.go 负责。
func fileValidationNotUnderTest(DeliveryInput, []byte) error { return nil }

// exportActionKey 是这些用例共用的一次用户动作键，长度要过 8..128 那一关。
const exportActionKey = "export-action-1"

func allowExport(actorUserID, projectID string) error {
	if actorUserID != "owner" || projectID != "project-1" {
		return errors.New("project access denied")
	}
	return nil
}

func currentExportInput(DeliveryInput) (bool, error) { return true, nil }

// preparedSnapshot 给只跑内存实现的用例用；一致性套件那几条走
// `forEachStorePair` + `preparedSnapshotOn`（同一路径，库由夹具给）。
func preparedSnapshot(t *testing.T) (*MemorySnapshotStore, ReleaseSnapshot) {
	t.Helper()
	store := NewMemorySnapshotStore()
	return store, preparedSnapshotOn(t, store)
}

func TestExportOnlyRendersPassingCurrentSnapshot(t *testing.T) {
	forEachStorePair(t, testExportOnlyRendersPassingCurrentSnapshot)
}

func testExportOnlyRendersPassingCurrentSnapshot(t *testing.T, snapshots snapshotStoreUnderTest, exports exportStoreUnderTest) {
	snapshot := preparedSnapshotOn(t, snapshots)
	renderer := frozenRenderer(func(input DeliveryInput) ([]byte, error) {
		if input.ProjectID != snapshot.ProjectID {
			t.Fatalf("renderer got project %q", input.ProjectID)
		}
		return []byte("PK\\x03\\x04minimal docx"), nil
	})
	service := NewExportService(snapshots, exports, renderer, FrozenValidatorFunc(fileValidationNotUnderTest), CurrentnessFunc(currentExportInput), ExportAccessFunc(allowExport))
	service.now = func() time.Time { return time.Date(2026, 9, 24, 1, 0, 0, 0, time.UTC) }
	artifact, replayed, err := service.Start("owner", snapshot.ProjectID, snapshot.ID, exportActionKey)
	if err != nil {
		t.Fatal(err)
	}
	if replayed {
		t.Fatal("the first export of an action reports a replay")
	}
	if artifact.Status != ExportVerified || artifact.FileSHA256 == "" {
		t.Fatalf("artifact = %+v", artifact)
	}
	_, file, err := service.Download("owner", snapshot.ProjectID, artifact.ID)
	if err != nil || string(file) != "PK\\x03\\x04minimal docx" {
		t.Fatalf("download = %q, %v", file, err)
	}
	file[0] = 'x'
	_, again, err := service.Download("owner", snapshot.ProjectID, artifact.ID)
	if err != nil || again[0] != 'P' {
		t.Fatalf("download leaked mutable bytes: %q, %v", again, err)
	}
}

func TestExportPersistsFailureButNeverDownloadsIt(t *testing.T) {
	forEachStorePair(t, testExportPersistsFailureButNeverDownloadsIt)
}

func testExportPersistsFailureButNeverDownloadsIt(t *testing.T, snapshots snapshotStoreUnderTest, exports exportStoreUnderTest) {
	snapshot := preparedSnapshotOn(t, snapshots)
	service := NewExportService(snapshots, exports, frozenRenderer(func(DeliveryInput) ([]byte, error) { return nil, errors.New("broken docx") }), FrozenValidatorFunc(fileValidationNotUnderTest), CurrentnessFunc(currentExportInput), ExportAccessFunc(allowExport))
	artifact, _, err := service.Start("owner", snapshot.ProjectID, snapshot.ID, exportActionKey)
	if err != nil || artifact.Status != ExportFailed || artifact.FailureCode != "render_failed" {
		t.Fatalf("artifact = %+v, err = %v", artifact, err)
	}
	if _, _, err := service.Download("owner", snapshot.ProjectID, artifact.ID); !errors.Is(err, ErrExportUnavailable) {
		t.Fatalf("failed file downloaded: %v", err)
	}
}

func TestExportRejectsBlockedAndHistoricalSnapshots(t *testing.T) {
	forEachStorePair(t, testExportRejectsBlockedAndHistoricalSnapshots)
}

func testExportRejectsBlockedAndHistoricalSnapshots(t *testing.T, snapshots snapshotStoreUnderTest, exports exportStoreUnderTest) {
	snapshot := preparedSnapshotOn(t, snapshots)
	service := NewExportService(snapshots, exports, frozenRenderer(func(DeliveryInput) ([]byte, error) { return []byte("docx"), nil }), FrozenValidatorFunc(fileValidationNotUnderTest), CurrentnessFunc(currentExportInput), ExportAccessFunc(allowExport))
	blocked := snapshot
	blocked.ID, blocked.Check.Status = "blocked", CheckBlocked
	if err := snapshots.Save(blocked); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Start("owner", blocked.ProjectID, blocked.ID, exportActionKey); !errors.Is(err, ErrExportPreflightBlocked) {
		t.Fatalf("blocked export = %v", err)
	}
	historical := snapshot
	historical.ID, historical.IsCurrent = "historical", false
	if err := snapshots.Save(historical); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Start("owner", historical.ProjectID, historical.ID, exportActionKey); !errors.Is(err, ErrExportStaleInput) {
		t.Fatalf("historical export = %v", err)
	}
}

func TestExportRechecksCurrentnessAndAccess(t *testing.T) {
	forEachStorePair(t, testExportRechecksCurrentnessAndAccess)
}

func testExportRechecksCurrentnessAndAccess(t *testing.T, snapshots snapshotStoreUnderTest, exports exportStoreUnderTest) {
	snapshot := preparedSnapshotOn(t, snapshots)
	current := true
	service := NewExportService(snapshots, exports, frozenRenderer(func(DeliveryInput) ([]byte, error) {
		current = false
		return []byte("docx"), nil
	}), FrozenValidatorFunc(fileValidationNotUnderTest), CurrentnessFunc(func(DeliveryInput) (bool, error) { return current, nil }), ExportAccessFunc(allowExport))
	if _, _, err := service.Start("owner", snapshot.ProjectID, snapshot.ID, exportActionKey); !errors.Is(err, ErrExportStaleInput) {
		t.Fatalf("export after source changed during render = %v", err)
	}
	if _, _, err := service.Start("other-user", snapshot.ProjectID, snapshot.ID, exportActionKey); err == nil {
		t.Fatal("export should require current project access")
	}
}

func TestDownloadRechecksCurrentAccess(t *testing.T) {
	forEachStorePair(t, testDownloadRechecksCurrentAccess)
}

func testDownloadRechecksCurrentAccess(t *testing.T, snapshots snapshotStoreUnderTest, exports exportStoreUnderTest) {
	snapshot := preparedSnapshotOn(t, snapshots)
	allowed := true
	service := NewExportService(snapshots, exports, frozenRenderer(func(DeliveryInput) ([]byte, error) { return []byte("docx"), nil }), FrozenValidatorFunc(fileValidationNotUnderTest), CurrentnessFunc(currentExportInput), ExportAccessFunc(func(actorUserID, projectID string) error {
		if allowed {
			return nil
		}
		return errors.New("project access denied")
	}))
	artifact, _, err := service.Start("owner", snapshot.ProjectID, snapshot.ID, exportActionKey)
	if err != nil {
		t.Fatal(err)
	}
	allowed = false
	if _, _, err := service.Download("owner", snapshot.ProjectID, artifact.ID); err == nil {
		t.Fatal("download should require current project access")
	}
}
