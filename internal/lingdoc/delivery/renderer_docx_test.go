package delivery

import (
	"archive/zip"
	"bytes"
	"errors"
	"testing"
)

func TestDeliveryRulesSurviveRendererSubstitution(t *testing.T) {
	for _, renderer := range []FrozenRenderer{DOCXRenderer{}, frozenRenderer(func(DeliveryInput) ([]byte, error) { return []byte("synthetic alternate format"), nil })} {
		store := NewMemorySnapshotStore()
		releases := NewReleaseService(store, CurrentnessFunc(currentExportInput))
		snapshot, err := releases.Prepare(validDeliveryInput())
		if err != nil {
			t.Fatal(err)
		}
		allowed := true
		exports := NewExportService(store, NewMemoryExportStore(), renderer, CurrentnessFunc(currentExportInput), ExportAccessFunc(func(string, string) error {
			if !allowed {
				return errors.New("revoked")
			}
			return nil
		}))
		artifact, err := exports.Start("owner", snapshot.ProjectID, snapshot.ID)
		if err != nil || artifact.Status != ExportVerified {
			t.Fatalf("renderer %T: %+v %v", renderer, artifact, err)
		}
		data, err := exports.Download("owner", snapshot.ProjectID, artifact.ID)
		if err != nil || len(data) == 0 {
			t.Fatalf("download %T: %v", renderer, err)
		}
		if _, docx := renderer.(DOCXRenderer); docx {
			if _, err := zip.NewReader(bytes.NewReader(data), int64(len(data))); err != nil {
				t.Fatalf("not DOCX: %v", err)
			}
		}
		allowed = false
		if _, err := exports.Download("owner", snapshot.ProjectID, artifact.ID); err == nil {
			t.Fatalf("renderer %T bypassed revoked access", renderer)
		}
		allowed = true
		blockedInput := validDeliveryInput()
		blockedInput.Chapters[0].BodyMarkdown = ""
		blocked, err := releases.Prepare(blockedInput)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := exports.Start("owner", blocked.ProjectID, blocked.ID); !errors.Is(err, ErrExportPreflightBlocked) {
			t.Fatalf("renderer %T bypassed blocked snapshot: %v", renderer, err)
		}
	}
}
