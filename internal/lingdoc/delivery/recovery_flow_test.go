package delivery

import (
	"errors"
	"testing"
)

type recoveryRenderer func(DeliveryInput) ([]byte, error)

func (f recoveryRenderer) RenderFrozen(input DeliveryInput) ([]byte, error) { return f(input) }

func recoveryExportService(snapshots SnapshotStore, exports ExportStore, renderer FrozenRenderer) *ExportService {
	return NewExportService(snapshots, exports, renderer, CurrentnessFunc(func(DeliveryInput) (bool, error) {
		return true, nil
	}), ExportAccessFunc(func(actorUserID, projectID string) error {
		if actorUserID != "owner" || projectID != "project-1" {
			return errors.New("project access denied")
		}
		return nil
	}))
}

// These are deliberately cross-component tests rather than more unit cases
// for either snapshot.go or export.go. They are the first runnable T15 entry
// point and cover recovery behavior a UI must be able to explain.
func TestRecoveryFlowBlockedInputDoesNotInvokeRenderer(t *testing.T) {
	snapshots := NewMemorySnapshotStore()
	releases := NewReleaseService(snapshots)
	input := validDeliveryInput()
	input.Chapters[0].ChapterVersionID = nil // an empty/unwritten chapter is a valid blocked snapshot input
	input.Chapters[0].Confirmation = nil
	snapshot, err := releases.Prepare(input)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Check.Status != CheckBlocked {
		t.Fatalf("status = %s, want blocked", snapshot.Check.Status)
	}
	calls := 0
	exports := NewMemoryExportStore()
	service := recoveryExportService(snapshots, exports, recoveryRenderer(func(DeliveryInput) ([]byte, error) {
		calls++
		return []byte("must not render"), nil
	}))
	if _, err := service.Start("owner", snapshot.ProjectID, snapshot.ID); !errors.Is(err, ErrExportPreflightBlocked) {
		t.Fatalf("blocked start = %v", err)
	}
	if calls != 0 {
		t.Fatalf("renderer was invoked %d times for a blocked snapshot", calls)
	}
	if _, err := exports.GetExport(snapshot.ProjectID, "export-000001"); err == nil {
		t.Fatal("blocked snapshot left an export artifact")
	}
}

func TestRecoveryFlowFailedRenderCanRetryWithoutExposingOldBytes(t *testing.T) {
	snapshots, snapshot := preparedSnapshot(t)
	exports := NewMemoryExportStore()
	failed := recoveryExportService(snapshots, exports, recoveryRenderer(func(DeliveryInput) ([]byte, error) {
		return nil, errors.New("temporary renderer outage")
	}))
	first, err := failed.Start("owner", snapshot.ProjectID, snapshot.ID)
	if err != nil || first.Status != ExportFailed {
		t.Fatalf("failed export = %+v, %v", first, err)
	}
	if _, err := failed.Download("owner", snapshot.ProjectID, first.ID); !errors.Is(err, ErrExportUnavailable) {
		t.Fatalf("failed artifact became downloadable: %v", err)
	}

	// A retry is a new explicit export service, not a mutation of a failed file.
	retry := recoveryExportService(snapshots, exports, recoveryRenderer(func(DeliveryInput) ([]byte, error) {
		return []byte("PK\\x03\\x04recovered docx"), nil
	}))
	second, err := retry.Start("owner", snapshot.ProjectID, snapshot.ID)
	if err != nil || second.Status != ExportVerified || second.ID == first.ID {
		t.Fatalf("retried export = %+v, %v", second, err)
	}
	if _, err := retry.Download("owner", snapshot.ProjectID, second.ID); err != nil {
		t.Fatalf("verified retry cannot download: %v", err)
	}
	storedFirst, err := exports.GetExport(snapshot.ProjectID, first.ID)
	if err != nil || storedFirst.Status != ExportFailed {
		t.Fatalf("retry changed failed artifact: %+v, %v", storedFirst, err)
	}
}

func TestRecoveryFlowFrozenDigestIsUnaffectedByLaterSourcePresentation(t *testing.T) {
	snapshots := NewMemorySnapshotStore()
	releases := NewReleaseService(snapshots)
	input := validDeliveryInput()
	first, err := releases.Prepare(input)
	if err != nil {
		t.Fatal(err)
	}
	// Mutating the caller's current value must not rewrite saved history.
	input.Sources[0].DisplayTitle = "资料的新展示标题"
	historical, err := snapshots.Get(first.ProjectID, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if historical.FrozenInput.Sources[0].DisplayTitle == input.Sources[0].DisplayTitle {
		t.Fatal("historical frozen source followed current presentation")
	}
	if digest, err := digestFrozenInput(historical.FrozenInput); err != nil || digest != first.SnapshotDigest {
		t.Fatalf("historical digest = %q, %v; want %q", digest, err, first.SnapshotDigest)
	}

	current, err := releases.Prepare(input)
	if err != nil {
		t.Fatal(err)
	}
	if current.SnapshotDigest == first.SnapshotDigest {
		t.Fatal("a new frozen source value did not produce a new digest")
	}
}

