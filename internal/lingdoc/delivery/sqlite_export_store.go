package delivery

import (
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SQLiteExportStore 是交付产物（含 DOCX 字节）的落地实现。
//
// 与 SQLiteSnapshotStore 同一条：store 与领域同包，因为 ExportArtifact.file 是包内
// 私有字段（无 json tag），跨包的 store 碰不到它，而为一个持久化细节开公开访问器
// 不划算。同一处已知限制也照抄：三个方法都没有 ctx，查询不随请求取消。
type SQLiteExportStore struct {
	db *gorm.DB
}

var (
	_ ExportStore    = (*SQLiteExportStore)(nil)
	_ ExportRecorder = (*SQLiteExportStore)(nil)
	_ ExportLister   = (*SQLiteExportStore)(nil)
)

func NewSQLiteExportStore(db *gorm.DB) *SQLiteExportStore {
	return &SQLiteExportStore{db: db}
}

// exportArtifactRow 是 lingdoc_export_artifacts 的一行（含字节）。
//
// 每列都写 column 标签的理由与快照行逐字相同：表是手写迁移建的，不经 AutoMigrate。
type exportArtifactRow struct {
	ID                string    `gorm:"column:id;primaryKey;size:64"`
	ProjectID         string    `gorm:"column:project_id;not null;size:64"`
	SnapshotID        string    `gorm:"column:snapshot_id;not null;size:64"`
	Status            string    `gorm:"column:status;not null;size:16"`
	FileSHA256        string    `gorm:"column:file_sha256;not null;size:64"`
	FailureCode       string    `gorm:"column:failure_code;not null;size:64"`
	FileBlob          []byte    `gorm:"column:file_blob"`
	CreatedAt         time.Time `gorm:"column:created_at;not null"`
	ActionActorID     *string   `gorm:"column:action_actor_id;size:128"`
	ActionKey         *string   `gorm:"column:action_key;size:128"`
	ActionRequestHash *string   `gorm:"column:action_request_hash;size:64"`
}

// exportArtifactListRow 是**同一张表**去掉 file_blob 的投影。
//
// 两个行结构、一份表，是为了让「列表」与「下载」读的不是同一种东西：交付历史一页
// 要列 50 份产物，用带 file_blob 的行去读会把每份 DOCX 都拉进内存（几 MB × 50），
// 而列表根本不显示字节。两者的其余字段由**同一个**转换函数产出
// （exportArtifactListRow.artifact 先还原成完整行再走 exportArtifactRow.artifact），
// 免得两条读取路径对同一行给出不同状态。
type exportArtifactListRow struct {
	ID          string    `gorm:"column:id"`
	ProjectID   string    `gorm:"column:project_id"`
	SnapshotID  string    `gorm:"column:snapshot_id"`
	Status      string    `gorm:"column:status"`
	FileSHA256  string    `gorm:"column:file_sha256"`
	FailureCode string    `gorm:"column:failure_code"`
	CreatedAt   time.Time `gorm:"column:created_at"`
}

func (exportArtifactRow) TableName() string     { return "lingdoc_export_artifacts" }
func (exportArtifactListRow) TableName() string { return "lingdoc_export_artifacts" }

// exportListColumns 是投影读出的列清单，与 exportArtifactListRow 的字段一一对应。
const exportListColumns = "id, project_id, snapshot_id, status, file_sha256, failure_code, created_at"

func (row exportArtifactRow) artifact() ExportArtifact {
	return ExportArtifact{
		ID: row.ID, ProjectID: row.ProjectID, SnapshotID: row.SnapshotID,
		Status: ExportStatus(row.Status), FileSHA256: row.FileSHA256,
		FailureCode: row.FailureCode, CreatedAt: row.CreatedAt.UTC(),
		// 归一与 cloneExport 一致：空字节落成 nil，否则两个实现的 DeepEqual 会在
		// 「NULL 读回来是 nil」与「空 blob 读回来是 []byte{}」上分家。
		file: append([]byte(nil), row.FileBlob...),
	}
}

func (row exportArtifactListRow) artifact() ExportArtifact {
	return exportArtifactRow{
		ID: row.ID, ProjectID: row.ProjectID, SnapshotID: row.SnapshotID,
		Status: row.Status, FileSHA256: row.FileSHA256,
		FailureCode: row.FailureCode, CreatedAt: row.CreatedAt,
	}.artifact()
}

// SaveExport 是**建或替换**，与内存实现的 `map[k] = v` 同语义。理由与
// SQLiteSnapshotStore.Save 逐字相同：只更新 payload 列，绝不碰 action_* 三列。
func (s *SQLiteExportStore) SaveExport(artifact ExportArtifact) error {
	return s.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"project_id", "snapshot_id", "status", "file_sha256", "failure_code", "file_blob", "created_at"}),
	}).Create(&exportArtifactRow{
		ID: artifact.ID, ProjectID: artifact.ProjectID, SnapshotID: artifact.SnapshotID,
		Status: string(artifact.Status), FileSHA256: artifact.FileSHA256,
		FailureCode: artifact.FailureCode, FileBlob: artifact.file,
		CreatedAt: artifact.CreatedAt.UTC(),
	}).Error
}

func (s *SQLiteExportStore) GetExport(projectID, exportID string) (ExportArtifact, error) {
	var row exportArtifactRow
	if err := s.db.Where("id = ? AND project_id = ?", exportID, projectID).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ExportArtifact{}, fmt.Errorf("%w: %s", ErrExportNotFound, exportID)
		}
		return ExportArtifact{}, err
	}
	return row.artifact(), nil
}

// ListExports 返回该项目下的产物（**不含字节**），新的在前。排序兜底与
// ListSnapshots 同一条：created_at 相等时按 id DESC，两个实现同一个次序。
func (s *SQLiteExportStore) ListExports(projectID string) ([]ExportArtifact, error) {
	var rows []exportArtifactListRow
	if err := s.db.Model(&exportArtifactRow{}).Select(exportListColumns).
		Where("project_id = ?", projectID).Order("created_at DESC, id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	// 显式 make：没有产物时返回 `[]` 而不是 nil（见 ExportLister 注释）。
	artifacts := make([]ExportArtifact, 0, len(rows))
	for _, row := range rows {
		artifacts = append(artifacts, row.artifact())
	}
	return artifacts, nil
}

func (s *SQLiteExportStore) ReplayExport(attempt ExportAttempt, requestHash string) (ExportArtifact, bool, error) {
	row, err := s.lookupExport(attempt)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ExportArtifact{}, false, nil
	}
	if err != nil {
		return ExportArtifact{}, false, err
	}
	return s.replay(row, requestHash)
}

// RecordExport 与 RecordFreeze 同构，理由也逐字相同：产物与「哪一次动作产出了它」
// 必须一起落，冲突目标是那条动作索引，RowsAffected 判定谁落笔。（为什么只覆盖动作
// 索引、以及「先 Save 再 Record」会怎样，见 SQLiteSnapshotStore.RecordFreeze 的注释。）
//
// 失败产物同样入账。§6 说「重试新任务是明确的新动作」，所以重试要换新键；同一个键
// 于是永远换回同一个结果，包括失败。
func (s *SQLiteExportStore) RecordExport(artifact ExportArtifact, attempt ExportAttempt, requestHash string) (ExportArtifact, bool, error) {
	row := exportArtifactRow{
		ID: artifact.ID, ProjectID: artifact.ProjectID, SnapshotID: artifact.SnapshotID,
		Status: string(artifact.Status), FileSHA256: artifact.FileSHA256,
		FailureCode: artifact.FailureCode, FileBlob: artifact.file,
		CreatedAt: artifact.CreatedAt.UTC(),
	}
	row.ActionActorID, row.ActionKey, row.ActionRequestHash = &attempt.ActorID, &attempt.Key, &requestHash
	result := s.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "project_id"}, {Name: "action_actor_id"}, {Name: "action_key"}},
		DoNothing: true,
	}).Create(&row)
	if result.Error != nil {
		return ExportArtifact{}, false, result.Error
	}
	if result.RowsAffected == 0 {
		winner, err := s.lookupExport(attempt)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ExportArtifact{}, false, fmt.Errorf("%w: export %s was skipped without an action record", ErrExportNotFound, attempt.Key)
		}
		if err != nil {
			return ExportArtifact{}, false, err
		}
		return s.replay(winner, requestHash)
	}
	// 落笔的是这一次：把库里那一份交回去（理由与 RecordFreeze 逐字相同）。
	stored, err := s.readExport(artifact.ProjectID, artifact.ID)
	if err != nil {
		return ExportArtifact{}, false, err
	}
	return stored, false, nil
}

func (s *SQLiteExportStore) lookupExport(attempt ExportAttempt) (exportArtifactRow, error) {
	var row exportArtifactRow
	err := s.db.Where("project_id = ? AND action_actor_id = ? AND action_key = ?",
		attempt.ProjectID, attempt.ActorID, attempt.Key).First(&row).Error
	return row, err
}

func (s *SQLiteExportStore) replay(row exportArtifactRow, requestHash string) (ExportArtifact, bool, error) {
	if row.ActionRequestHash == nil || *row.ActionRequestHash != requestHash {
		return ExportArtifact{}, false, ErrIdempotencyConflict
	}
	artifact, err := s.readExport(row.ProjectID, row.ID)
	if err != nil {
		return ExportArtifact{}, false, err
	}
	return artifact, true, nil
}

func (s *SQLiteExportStore) readExport(projectID, exportID string) (ExportArtifact, error) {
	var row exportArtifactRow
	if err := s.db.Where("id = ? AND project_id = ?", exportID, projectID).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ExportArtifact{}, fmt.Errorf("%w: %s", ErrExportNotFound, exportID)
		}
		return ExportArtifact{}, err
	}
	return row.artifact(), nil
}
