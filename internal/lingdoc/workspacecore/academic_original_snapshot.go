package workspacecore

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"strings"

	"github.com/Tencent/WeKnora/internal/evidence"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// AcademicOriginalSnapshotRef is a content address, not an authorization grant
// or a proof that a source belongs to an academic record.
type AcademicOriginalSnapshotRef struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// academicOriginalSnapshotRow mirrors the immutable migration-owned table.
// Access remains through the transaction-scoped snapshot functions below.
type academicOriginalSnapshotRow struct {
	TenantID  int64  `gorm:"not null;primaryKey"`
	ProjectID string `gorm:"not null;primaryKey"`
	SHA256    string `gorm:"column:sha256;not null;primaryKey"`
	Size      int64  `gorm:"not null"`
	Content   []byte `gorm:"not null"`
}

func (academicOriginalSnapshotRow) TableName() string { return "lingdoc_academic_original_snapshots" }

func snapshotDatabase(tx Transaction, tenantID uint64, projectID string) (*gorm.DB, error) {
	if tenantID == 0 || tenantID > math.MaxInt64 || projectID == "" || strings.TrimSpace(projectID) != projectID {
		return nil, ErrInvalidRequest
	}
	db, err := TransactionDatabase(tx)
	if err != nil || db == nil {
		return nil, ErrDependencyUnavailable
	}
	// The payload is private: GORM must not interpolate its bytes into SQL
	// diagnostics, even if an insert fails or the caller enabled debug logging.
	db = db.Session(&gorm.Session{Logger: logger.Discard})
	var count int64
	if err := db.Table("lingdoc_projects").Where("id = ? AND tenant_id = ?", projectID, tenantID).Count(&count).Error; err != nil {
		return nil, err
	}
	if count != 1 {
		return nil, ErrNotFound
	}
	return db, nil
}

// PutAcademicOriginalSnapshot stores bytes inside the caller's already
// authorized business transaction. It never opens a separate transaction.
// The caller must bind the ref to source/asset and human-decision evidence.
func PutAcademicOriginalSnapshot(tx Transaction, tenantID uint64, projectID string, raw []byte) (AcademicOriginalSnapshotRef, error) {
	if len(raw) == 0 || len(raw) > evidence.MaxOriginBytes {
		return AcademicOriginalSnapshotRef{}, ErrInvalidRequest
	}
	db, err := snapshotDatabase(tx, tenantID, projectID)
	if err != nil {
		return AcademicOriginalSnapshotRef{}, err
	}
	digest := sha256.Sum256(raw)
	ref := AcademicOriginalSnapshotRef{SHA256: hex.EncodeToString(digest[:]), Size: int64(len(raw))}
	stored, err := readAcademicOriginalSnapshot(db, tenantID, projectID, ref)
	if err == nil {
		if !bytes.Equal(stored, raw) {
			return AcademicOriginalSnapshotRef{}, ErrInvalidState
		}
		return ref, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return AcademicOriginalSnapshotRef{}, err
	}
	result := db.Exec("INSERT INTO lingdoc_academic_original_snapshots (tenant_id, project_id, sha256, size, content) VALUES (?, ?, ?, ?, ?) ON CONFLICT (tenant_id, project_id, sha256) DO NOTHING", tenantID, projectID, ref.SHA256, ref.Size, raw)
	if result.Error != nil {
		return AcademicOriginalSnapshotRef{}, result.Error
	}
	stored, err = readAcademicOriginalSnapshot(db, tenantID, projectID, ref)
	if err != nil {
		return AcademicOriginalSnapshotRef{}, err
	}
	if !bytes.Equal(stored, raw) {
		return AcademicOriginalSnapshotRef{}, ErrInvalidState
	}
	return ref, nil
}

// ReadAcademicOriginalSnapshot requires the caller's authorized transaction;
// the ref is never sufficient to read another project or bypass source policy.
func ReadAcademicOriginalSnapshot(tx Transaction, tenantID uint64, projectID string, ref AcademicOriginalSnapshotRef) ([]byte, error) {
	db, err := snapshotDatabase(tx, tenantID, projectID)
	if err != nil {
		return nil, err
	}
	return readAcademicOriginalSnapshot(db, tenantID, projectID, ref)
}

func readAcademicOriginalSnapshot(db *gorm.DB, tenantID uint64, projectID string, ref AcademicOriginalSnapshotRef) ([]byte, error) {
	if len(ref.SHA256) != 64 || strings.ToLower(ref.SHA256) != ref.SHA256 || ref.Size < 1 || ref.Size > evidence.MaxOriginBytes {
		return nil, ErrInvalidRequest
	}
	if _, err := hex.DecodeString(ref.SHA256); err != nil {
		return nil, ErrInvalidRequest
	}
	var row academicOriginalSnapshotRow
	if err := db.Table("lingdoc_academic_original_snapshots").Select("size", "content").Where("tenant_id = ? AND project_id = ? AND sha256 = ?", tenantID, projectID, ref.SHA256).Take(&row).Error; err != nil {
		return nil, storageError(err)
	}
	digest := sha256.Sum256(row.Content)
	if row.Size != ref.Size || int64(len(row.Content)) != ref.Size || hex.EncodeToString(digest[:]) != ref.SHA256 {
		return nil, ErrInvalidState
	}
	return row.Content, nil
}
