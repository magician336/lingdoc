package workspacecore

import "context"

// AcademicSnapshotMaterial is internal evidence material. It must never be
// serialized in an API response or log; callers persist it only via the
// authorized immutable snapshot boundary.
type AcademicSnapshotMaterial struct {
	Source     ResearchSource `json:"-"`
	Raw        []byte         `json:"-"`
	SourceText string         `json:"-"`
	RawSHA256  string         `json:"-"`
}

// AcademicSnapshotMaterialReader captures and proves one uploaded source;
// chapterID, when supplied, constrains authorization to that chapter.
type AcademicSnapshotMaterialReader interface {
	ReadAcademicSnapshotMaterial(context.Context, Actor, string, string, string) (AcademicSnapshotMaterial, error)
}
