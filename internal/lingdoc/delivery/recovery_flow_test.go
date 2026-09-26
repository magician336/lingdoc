package delivery

import (
	"errors"
	"testing"
)

type recoveryRenderer func(DeliveryInput) ([]byte, error)

func (f recoveryRenderer) RenderFrozen(input DeliveryInput) ([]byte, error) { return f(input) }

func recoveryExportService(snapshots SnapshotStore, exports ExportStore, renderer FrozenRenderer) *ExportService {
	return NewExportService(snapshots, exports, renderer, FrozenValidatorFunc(fileValidationNotUnderTest), CurrentnessFunc(func(DeliveryInput) (bool, error) {
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
	forEachStorePair(t, testRecoveryFlowBlockedInputDoesNotInvokeRenderer)
}

func testRecoveryFlowBlockedInputDoesNotInvokeRenderer(t *testing.T, snapshots snapshotStoreUnderTest, exports exportStoreUnderTest) {
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
	service := recoveryExportService(snapshots, exports, recoveryRenderer(func(DeliveryInput) ([]byte, error) {
		calls++
		return []byte("must not render"), nil
	}))
	if _, _, err := service.Start("owner", snapshot.ProjectID, snapshot.ID, exportActionKey); !errors.Is(err, ErrExportPreflightBlocked) {
		t.Fatalf("blocked start = %v", err)
	}
	if calls != 0 {
		t.Fatalf("renderer was invoked %d times for a blocked snapshot", calls)
	}
	// IDs are opaque: looking up one guessed ID cannot prove no write occurred.
	// So the assertion goes through the contract («这次动作一份都没落下») rather than
	// through the in-memory map — which also makes it run against the stored one.
	if listed := listExports(t, exports, snapshot.ProjectID); len(listed) != 0 {
		t.Fatalf("blocked snapshot persisted %d export artifacts", len(listed))
	}
}

func TestRecoveryFlowFailedRenderCanRetryWithoutExposingOldBytes(t *testing.T) {
	forEachStorePair(t, testRecoveryFlowFailedRenderCanRetryWithoutExposingOldBytes)
}

func testRecoveryFlowFailedRenderCanRetryWithoutExposingOldBytes(t *testing.T, snapshots snapshotStoreUnderTest, exports exportStoreUnderTest) {
	snapshot := preparedSnapshotOn(t, snapshots)
	failed := recoveryExportService(snapshots, exports, recoveryRenderer(func(DeliveryInput) ([]byte, error) {
		return nil, errors.New("temporary renderer outage")
	}))
	first, _, err := failed.Start("owner", snapshot.ProjectID, snapshot.ID, exportActionKey)
	if err != nil || first.Status != ExportFailed {
		t.Fatalf("failed export = %+v, %v", first, err)
	}
	if _, _, err := failed.Download("owner", snapshot.ProjectID, first.ID); !errors.Is(err, ErrExportUnavailable) {
		t.Fatalf("failed artifact became downloadable: %v", err)
	}

	// A retry is a new explicit action — §6「重试新任务是明确的新动作」—— so it
	// carries a new key and produces its own artifact, never a mutation of the
	// failed one. (Replaying the *same* key is covered in export_action_test.go.)
	retry := recoveryExportService(snapshots, exports, recoveryRenderer(func(DeliveryInput) ([]byte, error) {
		return []byte("PK\\x03\\x04recovered docx"), nil
	}))
	second, _, err := retry.Start("owner", snapshot.ProjectID, snapshot.ID, "export-action-retry")
	if err != nil || second.Status != ExportVerified || second.ID == first.ID {
		t.Fatalf("retried export = %+v, %v", second, err)
	}
	if _, _, err := retry.Download("owner", snapshot.ProjectID, second.ID); err != nil {
		t.Fatalf("verified retry cannot download: %v", err)
	}
	storedFirst, err := exports.GetExport(snapshot.ProjectID, first.ID)
	if err != nil || storedFirst.Status != ExportFailed {
		t.Fatalf("retry changed failed artifact: %+v, %v", storedFirst, err)
	}
}

func TestRecoveryFlowFrozenDigestIsUnaffectedByLaterSourcePresentation(t *testing.T) {
	forEachSnapshotStore(t, testRecoveryFlowFrozenDigestIsUnaffectedByLaterSourcePresentation)
}

func testRecoveryFlowFrozenDigestIsUnaffectedByLaterSourcePresentation(t *testing.T, snapshots snapshotStoreUnderTest) {
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

func TestRecoveryFlowCurrentnessErrorAfterRenderingLeavesNoArtifact(t *testing.T) {
	forEachStorePair(t, testRecoveryFlowCurrentnessErrorAfterRenderingLeavesNoArtifact)
}

func testRecoveryFlowCurrentnessErrorAfterRenderingLeavesNoArtifact(t *testing.T, snapshots snapshotStoreUnderTest, exports exportStoreUnderTest) {
	snapshot := preparedSnapshotOn(t, snapshots)
	currentnessErr := errors.New("currentness store unavailable after rendering")
	checks, renders := 0, 0
	service := NewExportService(snapshots, exports, recoveryRenderer(func(DeliveryInput) ([]byte, error) {
		renders++
		// A state-flow fixture, not proof of a valid DOCX; T14 owns validation.
		return []byte("rendered state-flow fixture"), nil
	}), FrozenValidatorFunc(fileValidationNotUnderTest), CurrentnessFunc(func(DeliveryInput) (bool, error) {
		checks++
		if checks == 1 {
			return true, nil
		}
		return false, currentnessErr
	}), ExportAccessFunc(allowExport))

	artifact, _, err := service.Start("owner", snapshot.ProjectID, snapshot.ID, exportActionKey)
	if !errors.Is(err, currentnessErr) {
		t.Fatalf("Start error = %v, want %v", err, currentnessErr)
	}
	if checks != 2 || renders != 1 {
		t.Fatalf("currentness/render calls = %d/%d, want 2/1", checks, renders)
	}
	if artifact.ID != "" || len(artifact.file) != 0 {
		t.Fatalf("currentness failure exposed an artifact: %+v", artifact)
	}
	if listed := listExports(t, exports, snapshot.ProjectID); len(listed) != 0 {
		t.Fatalf("post-render currentness error persisted %d artifacts", len(listed))
	}
}
