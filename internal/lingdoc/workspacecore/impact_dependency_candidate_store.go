package workspacecore

import (
	"encoding/json"
	"time"
)

// Persistence for the AI-proposed dependency candidates (G3 gap closure #252).
//
// A candidate is a proposal, not a dependency edge, so it does NOT live in the
// shared lingdoc_g3_records seam where the edges live: the edges are the
// formal dependency graph, and a proposal must never be readable as one.
//
// Two tables:
//
//   - the batch, which owns the idempotency key, the range the user selected
//     and the candidates that were suppressed (and why);
//   - the candidates, one row per proposal.
//
// The batch key is unique per (project, actor): two actors may use the same
// key without colliding, and the same actor may never produce two batches
// with one key.

// dependencyCandidateStore is the persistence port of the candidate module.
// It is declared here, separately from the shared transaction interfaces, so
// that adding the module does not widen interfaces other features assert on.
type dependencyCandidateStore interface {
	InsertDependencyCandidateBatch(DependencyCandidateBatch) error
	DependencyCandidateBatchByKey(project, actor, key string) (DependencyCandidateBatch, error)
	DependencyCandidateBatches(project string) ([]DependencyCandidateBatch, error)
	InsertDependencyCandidate(DependencyCandidate) error
	DependencyCandidate(project, id string) (DependencyCandidate, error)
	DependencyCandidates(project string) ([]DependencyCandidate, error)
	UpdateDependencyCandidate(previous, next DependencyCandidate) error
}

type dependencyCandidateBatchRow struct {
	ID              string    `gorm:"primaryKey;size:36"`
	ProjectID       string    `gorm:"not null;size:64;index"`
	ActorID         string    `gorm:"not null;size:64"`
	IdempotencyKey  string    `gorm:"column:idempotency_key;not null;size:128"`
	ScopeJSON       string    `gorm:"column:scope_json;type:text;not null"`
	Status          string    `gorm:"not null;size:16"`
	SuppressedJSON  string    `gorm:"column:suppressed_json;type:text;not null"`
	ContentHash     string    `gorm:"column:content_hash;not null;size:64"`
	ProjectVersion  int64     `gorm:"not null"`
	ContextRevision int64     `gorm:"not null"`
	CandidateCount  int       `gorm:"not null"`
	SuppressedCount int       `gorm:"not null"`
	Reason          string    `gorm:"type:text;not null"`
	CreatedAt       time.Time `gorm:"not null"`
	UpdatedAt       time.Time `gorm:"not null"`
}

func (dependencyCandidateBatchRow) TableName() string {
	return "lingdoc_g3_dependency_candidate_batches"
}

type dependencyCandidateRow struct {
	ID                 string     `gorm:"primaryKey;size:36"`
	ProjectID          string     `gorm:"not null;size:64;index"`
	BatchID            string     `gorm:"not null;size:36;index"`
	SourceKind         string     `gorm:"column:source_kind;not null;size:32"`
	SourceID           string     `gorm:"column:source_id;not null;size:128"`
	SourceVersion      string     `gorm:"column:source_version;not null;size:128"`
	TargetKind         string     `gorm:"column:target_kind;not null;size:32"`
	TargetID           string     `gorm:"column:target_id;not null;size:128"`
	TargetVersion      string     `gorm:"column:target_version;not null;size:128"`
	Relation           string     `gorm:"not null;size:128"`
	Action             string     `gorm:"not null;size:64"`
	Evidence           string     `gorm:"type:text;not null"`
	Reason             string     `gorm:"type:text;not null"`
	Confidence         float64    `gorm:"not null"`
	Status             string     `gorm:"not null;size:16"`
	Decision           string     `gorm:"size:16"`
	DecisionReason     string     `gorm:"column:decision_reason;type:text"`
	DecidedBy          string     `gorm:"column:decided_by;size:64"`
	DecidedAt          *time.Time `gorm:"column:decided_at"`
	EdgeID             string     `gorm:"column:edge_id;size:36"`
	SupersededRelation string     `gorm:"column:superseded_relation;size:128"`
	SupersededAction   string     `gorm:"column:superseded_action;size:64"`
	ProjectVersion     int64      `gorm:"not null"`
	ContextRevision    int64      `gorm:"not null"`
	ModelRunID         string     `gorm:"column:model_run_id;size:128"`
	ActorID            string     `gorm:"not null;size:64"`
	Version            int64      `gorm:"not null"`
	CreatedAt          time.Time  `gorm:"not null"`
	UpdatedAt          time.Time  `gorm:"not null"`
}

func (dependencyCandidateRow) TableName() string { return "lingdoc_g3_dependency_candidates" }

func encodeDependencyCandidateBatch(batch DependencyCandidateBatch) (dependencyCandidateBatchRow, error) {
	scope, err := json.Marshal(batch.Scope)
	if err != nil {
		return dependencyCandidateBatchRow{}, err
	}
	suppressed, err := json.Marshal(batch.Suppressed)
	if err != nil {
		return dependencyCandidateBatchRow{}, err
	}
	return dependencyCandidateBatchRow{
		ID:              batch.ID,
		ProjectID:       batch.ProjectID,
		ActorID:         batch.ActorID,
		IdempotencyKey:  batch.IdempotencyKey,
		ScopeJSON:       string(scope),
		Status:          batch.Status,
		SuppressedJSON:  string(suppressed),
		ContentHash:     batch.ContentHash,
		ProjectVersion:  batch.ProjectVersion,
		ContextRevision: batch.ContextRevision,
		CandidateCount:  len(batch.Candidates),
		SuppressedCount: len(batch.Suppressed),
		Reason:          batch.Reason,
		CreatedAt:       batch.CreatedAt,
		UpdatedAt:       batch.UpdatedAt,
	}, nil
}

func decodeDependencyCandidateBatch(row dependencyCandidateBatchRow) (DependencyCandidateBatch, error) {
	batch := DependencyCandidateBatch{
		ID:              row.ID,
		ProjectID:       row.ProjectID,
		IdempotencyKey:  row.IdempotencyKey,
		Status:          row.Status,
		Candidates:      []DependencyCandidate{},
		Suppressed:      []DependencyCandidateSuppression{},
		ProjectVersion:  row.ProjectVersion,
		ContextRevision: row.ContextRevision,
		ActorID:         row.ActorID,
		Reason:          row.Reason,
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
		ContentHash:     row.ContentHash,
	}
	if err := json.Unmarshal([]byte(row.ScopeJSON), &batch.Scope); err != nil {
		return DependencyCandidateBatch{}, err
	}
	if row.SuppressedJSON != "" {
		if err := json.Unmarshal([]byte(row.SuppressedJSON), &batch.Suppressed); err != nil {
			return DependencyCandidateBatch{}, err
		}
	}
	return batch, nil
}

func dependencyCandidateFromRow(row dependencyCandidateRow) DependencyCandidate {
	return DependencyCandidate{
		ID:                 row.ID,
		ProjectID:          row.ProjectID,
		BatchID:            row.BatchID,
		Source:             ObjectVersionRef{Kind: row.SourceKind, ID: row.SourceID, Version: row.SourceVersion},
		Target:             ObjectVersionRef{Kind: row.TargetKind, ID: row.TargetID, Version: row.TargetVersion},
		Relation:           row.Relation,
		Action:             row.Action,
		Evidence:           row.Evidence,
		Reason:             row.Reason,
		Confidence:         row.Confidence,
		Status:             row.Status,
		Decision:           row.Decision,
		DecisionReason:     row.DecisionReason,
		DecidedBy:          row.DecidedBy,
		DecidedAt:          row.DecidedAt,
		EdgeID:             row.EdgeID,
		SupersededRelation: row.SupersededRelation,
		SupersededAction:   row.SupersededAction,
		ProjectVersion:     row.ProjectVersion,
		ContextRevision:    row.ContextRevision,
		ModelRunID:         row.ModelRunID,
		ActorID:            row.ActorID,
		Version:            row.Version,
		CreatedAt:          row.CreatedAt,
		UpdatedAt:          row.UpdatedAt,
	}
}

func dependencyCandidateToRow(candidate DependencyCandidate) dependencyCandidateRow {
	return dependencyCandidateRow{
		ID:                 candidate.ID,
		ProjectID:          candidate.ProjectID,
		BatchID:            candidate.BatchID,
		SourceKind:         candidate.Source.Kind,
		SourceID:           candidate.Source.ID,
		SourceVersion:      candidate.Source.Version,
		TargetKind:         candidate.Target.Kind,
		TargetID:           candidate.Target.ID,
		TargetVersion:      candidate.Target.Version,
		Relation:           candidate.Relation,
		Action:             candidate.Action,
		Evidence:           candidate.Evidence,
		Reason:             candidate.Reason,
		Confidence:         candidate.Confidence,
		Status:             candidate.Status,
		Decision:           candidate.Decision,
		DecisionReason:     candidate.DecisionReason,
		DecidedBy:          candidate.DecidedBy,
		DecidedAt:          candidate.DecidedAt,
		EdgeID:             candidate.EdgeID,
		SupersededRelation: candidate.SupersededRelation,
		SupersededAction:   candidate.SupersededAction,
		ProjectVersion:     candidate.ProjectVersion,
		ContextRevision:    candidate.ContextRevision,
		ModelRunID:         candidate.ModelRunID,
		ActorID:            candidate.ActorID,
		Version:            candidate.Version,
		CreatedAt:          candidate.CreatedAt,
		UpdatedAt:          candidate.UpdatedAt,
	}
}

func (t gormTransaction) InsertDependencyCandidateBatch(batch DependencyCandidateBatch) error {
	row, err := encodeDependencyCandidateBatch(batch)
	if err != nil {
		return err
	}
	return t.db.Create(&row).Error
}

func (t gormTransaction) DependencyCandidateBatchByKey(project, actor, key string) (DependencyCandidateBatch, error) {
	var row dependencyCandidateBatchRow
	if err := t.db.Where("project_id = ? AND actor_id = ? AND idempotency_key = ?", project, actor, key).First(&row).Error; err != nil {
		return DependencyCandidateBatch{}, storageError(err)
	}
	batch, err := decodeDependencyCandidateBatch(row)
	if err != nil {
		return DependencyCandidateBatch{}, err
	}
	batch.Candidates, err = t.DependencyCandidatesOfBatch(project, batch.ID)
	if err != nil {
		return DependencyCandidateBatch{}, err
	}
	return batch, nil
}

func (t gormTransaction) DependencyCandidateBatches(project string) ([]DependencyCandidateBatch, error) {
	var rows []dependencyCandidateBatchRow
	if err := t.db.Where("project_id = ?", project).Order("created_at, id").Find(&rows).Error; err != nil {
		return nil, err
	}
	batches := []DependencyCandidateBatch{}
	for _, row := range rows {
		batch, err := decodeDependencyCandidateBatch(row)
		if err != nil {
			return nil, err
		}
		batch.Candidates, err = t.DependencyCandidatesOfBatch(project, batch.ID)
		if err != nil {
			return nil, err
		}
		batches = append(batches, batch)
	}
	return batches, nil
}

func (t gormTransaction) DependencyCandidatesOfBatch(project, batchID string) ([]DependencyCandidate, error) {
	var rows []dependencyCandidateRow
	if err := t.db.Where("project_id = ? AND batch_id = ?", project, batchID).Order("created_at, id").Find(&rows).Error; err != nil {
		return nil, err
	}
	candidates := []DependencyCandidate{}
	for _, row := range rows {
		candidates = append(candidates, dependencyCandidateFromRow(row))
	}
	return candidates, nil
}

func (t gormTransaction) InsertDependencyCandidate(candidate DependencyCandidate) error {
	row := dependencyCandidateToRow(candidate)
	return t.db.Create(&row).Error
}

func (t gormTransaction) DependencyCandidate(project, id string) (DependencyCandidate, error) {
	var row dependencyCandidateRow
	if err := t.db.Where("project_id = ? AND id = ?", project, id).First(&row).Error; err != nil {
		return DependencyCandidate{}, storageError(err)
	}
	return dependencyCandidateFromRow(row), nil
}

func (t gormTransaction) DependencyCandidates(project string) ([]DependencyCandidate, error) {
	var rows []dependencyCandidateRow
	if err := t.db.Where("project_id = ?", project).Order("created_at, id").Find(&rows).Error; err != nil {
		return nil, err
	}
	candidates := []DependencyCandidate{}
	for _, row := range rows {
		candidates = append(candidates, dependencyCandidateFromRow(row))
	}
	return candidates, nil
}

func (t gormTransaction) UpdateDependencyCandidate(previous, next DependencyCandidate) error {
	row := dependencyCandidateToRow(next)
	row.CreatedAt = previous.CreatedAt
	return affected(t.db.Model(&dependencyCandidateRow{}).
		Where("id = ? AND project_id = ? AND version = ?", previous.ID, previous.ProjectID, previous.Version).
		Updates(map[string]any{
			"status":              row.Status,
			"decision":            row.Decision,
			"decision_reason":     row.DecisionReason,
			"decided_by":          row.DecidedBy,
			"decided_at":          row.DecidedAt,
			"edge_id":             row.EdgeID,
			"superseded_relation": row.SupersededRelation,
			"superseded_action":   row.SupersededAction,
			"confidence":          row.Confidence,
			"reason":              row.Reason,
			"version":             next.Version,
			"updated_at":          next.UpdatedAt,
		}))
}
