package delivery

// ReleaseApplication is the check/freeze/read boundary. Prepare saves even a
// blocked result; frozen input and digest never change with IsCurrent.
type ReleaseApplication interface {
	Prepare(DeliveryInput) (ReleaseSnapshot, error)
	Get(projectID, snapshotID string) (ReleaseSnapshot, error)
}

// ExportApplication renders only passing, current frozen snapshots. Start and
// Download both check current access. Failed artifacts have no download bytes.
// Authorization must include sources as well as project membership.
type ExportApplication interface {
	Start(actorUserID, projectID, snapshotID string) (ExportArtifact, error)
	Download(actorUserID, projectID, exportID string) ([]byte, error)
}

var _ ReleaseApplication = (*ReleaseService)(nil)
var _ ExportApplication = (*ExportService)(nil)
