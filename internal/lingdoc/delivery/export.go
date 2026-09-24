package delivery

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	// ErrExportPreflightBlocked means the snapshot is a deliberately saved
	// blocked check result. Callers must show its issues instead of offering a
	// fake download.
	ErrExportPreflightBlocked = errors.New("release snapshot is blocked")
	// ErrExportStaleInput prevents a new export from being created from a
	// snapshot that no longer represents the current project input.
	ErrExportStaleInput = errors.New("release snapshot is no longer current")
	// ErrExportUnavailable is returned for failed or unknown artifacts.
	ErrExportUnavailable = errors.New("export file is not available")
)

type ExportStatus string

const (
	ExportVerified ExportStatus = "verified"
	ExportFailed   ExportStatus = "failed"
)

// ExportArtifact records the outcome without exposing any bytes for failed
// output. Authorization on download belongs to the caller's current access
// check; this small service enforces the independent verified-file boundary.
type ExportArtifact struct {
	ID          string       `json:"id"`
	ProjectID   string       `json:"project_id"`
	SnapshotID  string       `json:"snapshot_id"`
	Status      ExportStatus `json:"status"`
	FileSHA256  string       `json:"file_sha256,omitempty"`
	FailureCode string       `json:"failure_code,omitempty"`
	CreatedAt   time.Time    `json:"created_at"`
	file        []byte
}

// FrozenRenderer is supplied by the T05 DOCX adapter. It receives only the
// saved input, never mutable chapter or source storage.
type FrozenRenderer interface {
	RenderFrozen(DeliveryInput) ([]byte, error)
}

type ExportStore interface {
	SaveExport(ExportArtifact) error
	GetExport(projectID, exportID string) (ExportArtifact, error)
}

type MemoryExportStore struct {
	mu      sync.RWMutex
	exports map[string]ExportArtifact
}

func NewMemoryExportStore() *MemoryExportStore {
	return &MemoryExportStore{exports: make(map[string]ExportArtifact)}
}

func (s *MemoryExportStore) SaveExport(artifact ExportArtifact) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.exports[artifact.ProjectID+"/"+artifact.ID] = cloneExport(artifact)
	return nil
}

func (s *MemoryExportStore) GetExport(projectID, exportID string) (ExportArtifact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	artifact, ok := s.exports[projectID+"/"+exportID]
	if !ok {
		return ExportArtifact{}, fmt.Errorf("export artifact not found")
	}
	return cloneExport(artifact), nil
}

type ExportService struct {
	snapshots SnapshotStore
	exports   ExportStore
	renderer  FrozenRenderer
	now       func() time.Time
	mu        sync.Mutex
	next      int
}

func NewExportService(snapshots SnapshotStore, exports ExportStore, renderer FrozenRenderer) *ExportService {
	return &ExportService{snapshots: snapshots, exports: exports, renderer: renderer, now: time.Now}
}

// Start renders a frozen, passing, current snapshot. Renderer errors become a
// persisted failed artifact so the UI can show a recovery state; they never
// create a downloadable file.
func (s *ExportService) Start(projectID, snapshotID string) (ExportArtifact, error) {
	snapshot, err := s.snapshots.Get(projectID, snapshotID)
	if err != nil {
		return ExportArtifact{}, err
	}
	if snapshot.Check.Status != CheckPassed {
		return ExportArtifact{}, ErrExportPreflightBlocked
	}
	if !snapshot.IsCurrent {
		return ExportArtifact{}, ErrExportStaleInput
	}
	s.mu.Lock()
	s.next++
	id := fmt.Sprintf("export-%06d", s.next)
	s.mu.Unlock()
	artifact := ExportArtifact{ID: id, ProjectID: projectID, SnapshotID: snapshotID, CreatedAt: s.now().UTC()}
	data, renderErr := s.renderer.RenderFrozen(snapshot.FrozenInput)
	if renderErr != nil {
		artifact.Status = ExportFailed
		artifact.FailureCode = "render_failed"
		if err := s.exports.SaveExport(artifact); err != nil {
			return ExportArtifact{}, err
		}
		return artifact, nil
	}
	if len(data) == 0 {
		artifact.Status = ExportFailed
		artifact.FailureCode = "empty_file"
		if err := s.exports.SaveExport(artifact); err != nil {
			return ExportArtifact{}, err
		}
		return artifact, nil
	}
	sum := sha256.Sum256(data)
	artifact.Status = ExportVerified
	artifact.FileSHA256 = hex.EncodeToString(sum[:])
	artifact.file = append([]byte(nil), data...)
	if err := s.exports.SaveExport(artifact); err != nil {
		return ExportArtifact{}, err
	}
	return cloneExport(artifact), nil
}

// Download returns bytes only for a verified export. A transport layer must
// re-run its current authorization check before calling this method.
func (s *ExportService) Download(projectID, exportID string) ([]byte, error) {
	artifact, err := s.exports.GetExport(projectID, exportID)
	if err != nil {
		return nil, err
	}
	if artifact.Status != ExportVerified || len(artifact.file) == 0 {
		return nil, ErrExportUnavailable
	}
	return append([]byte(nil), artifact.file...), nil
}

func cloneExport(artifact ExportArtifact) ExportArtifact {
	artifact.file = append([]byte(nil), artifact.file...)
	return artifact
}

