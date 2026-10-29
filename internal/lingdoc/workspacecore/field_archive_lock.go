package workspacecore

import (
	"encoding/json"
	"errors"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type lockedArchiveProject struct {
	Spec     map[string]string
	Metadata map[string]SpecField
}

type fieldArchiveProjectLocker interface {
	LockFieldArchiveProject(uint64, Project) (lockedArchiveProject, error)
}

// Lock the persisted row without passing through projectView's synthesized
// metadata. SQLite takes a write reservation with a conditional identity
// update; PostgreSQL holds a row lock without updating any project column.
func (t gormTransaction) LockFieldArchiveProject(tenant uint64, p Project) (lockedArchiveProject, error) {
	query := func() *gorm.DB {
		return t.db.Where("id = ? AND tenant_id = ? AND project_version = ? AND spec_revision = ? AND template_copy_version = ? AND status = ?",
			p.ID, tenant, p.ProjectVersion, p.SpecRevision, p.TemplateCopyVersion, p.Status)
	}
	if t.db.Name() == "sqlite" {
		if err := affected(query().Model(&projectRow{}).UpdateColumn("id", gorm.Expr("id"))); err != nil {
			return lockedArchiveProject{}, err
		}
	}
	where := query()
	if t.db.Name() != "sqlite" {
		where = where.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var row projectRow
	if err := where.Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return lockedArchiveProject{}, ErrVersionConflict
		}
		return lockedArchiveProject{}, err
	}
	spec, err := decodeSpec(row.SpecJSON)
	if err != nil {
		return lockedArchiveProject{}, ErrInvalidState
	}
	metadata := map[string]SpecField{}
	if strings.TrimSpace(row.SpecMetadataJSON) != "" {
		if err := json.Unmarshal([]byte(row.SpecMetadataJSON), &metadata); err != nil {
			return lockedArchiveProject{}, ErrInvalidState
		}
	}
	if metadata == nil {
		metadata = map[string]SpecField{}
	}
	return lockedArchiveProject{Spec: spec, Metadata: metadata}, nil
}
