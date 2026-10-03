package workspacecore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"strings"
	"time"
)

// GORMRepository implements atomic persistence, not application rules.
type GORMRepository struct{ db *gorm.DB }

func NewGORMRepository(db *gorm.DB) *GORMRepository { return &GORMRepository{db: db} }
func (r *GORMRepository) Transaction(ctx context.Context, options TransactionOptions, fn func(Transaction) error) error {
	run := func(ctx context.Context) error {
		var sqlOptions *sql.TxOptions
		if options.ReadOnly {
			sqlOptions = &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
		}
		return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return fn(gormTransaction{tx}) }, sqlOptions)
	}
	if !options.RetryLocks {
		return run(ctx)
	}
	_, _, _, err := retrySQLiteSpecOperation(ctx, specLockRetryBudget, func(ctx context.Context) (json.RawMessage, int, bool, error) { return nil, 0, false, run(ctx) })
	return err
}

type gormTransaction struct{ db *gorm.DB }

func (t gormTransaction) ActiveMember(actor Actor) (bool, error) {
	var count int64
	err := t.db.Table("tenant_members").Where("tenant_id = ? AND user_id = ? AND status = ? AND deleted_at IS NULL", actor.TenantID, actor.UserID, "active").Count(&count).Error
	return count > 0, err
}
func storageError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	return err
}
func optionalPermissionTable(err error) error {
	if err != nil && strings.Contains(err.Error(), "no such table: lingdoc_member_permissions") {
		return nil
	}
	return err
}
func projectView(tx *gorm.DB, row projectRow) (Project, error) {
	spec, err := decodeSpec(row.SpecJSON)
	if err != nil {
		return Project{}, err
	}
	var members []memberRow
	if err := tx.Where("project_id = ?", row.ID).Order("user_id").Find(&members).Error; err != nil {
		return Project{}, err
	}
	permissions := make(map[string]memberPermissionRow)
	var permissionRows []memberPermissionRow
	if err := tx.Where("project_id = ?", row.ID).Find(&permissionRows).Error; err == nil {
		for _, permission := range permissionRows {
			permissions[permission.UserID] = permission
		}
	} else if !strings.Contains(err.Error(), "no such table: lingdoc_member_permissions") {
		return Project{}, err
	}
	p := Project{ID: row.ID, Name: row.Name, Status: row.Status, ProjectVersion: row.ProjectVersion, SpecRevision: row.SpecRevision, Spec: spec, TemplateID: row.TemplateID, TemplateVersion: row.TemplateVersion, Members: make([]Member, 0, len(members))}
	for _, m := range members {
		member := Member{UserID: m.UserID, Role: m.Role}
		if m.Role == "owner" {
			member.GovernanceRole = "owner"
		} else if m.Role == "collaborator" {
			member.GovernanceRole = "member"
			member.FunctionRoles = []string{"author"}
		}
		if permission, ok := permissions[m.UserID]; ok {
			member.GovernanceRole, member.Status = permission.GovernanceRole, permission.Status
			if err := json.Unmarshal([]byte(permission.FunctionRolesJSON), &member.FunctionRoles); err != nil {
				return Project{}, err
			}
			if err := json.Unmarshal([]byte(permission.FunctionScopesJSON), &member.FunctionScopes); err != nil {
				return Project{}, err
			}
		}
		p.Members = append(p.Members, member)
	}
	return p, nil
}
func (t gormTransaction) Project(tenantID uint64, id string) (Project, error) {
	var row projectRow
	if err := t.db.Where("id = ? AND tenant_id = ?", id, tenantID).First(&row).Error; err != nil {
		return Project{}, storageError(err)
	}
	return projectView(t.db, row)
}
func (t gormTransaction) Projects(actor Actor, limit int) ([]Project, error) {
	var rows []projectRow
	if err := t.db.Table("lingdoc_projects AS p").Select("p.*").Joins("JOIN lingdoc_members AS m ON m.project_id = p.id").Where("p.tenant_id = ? AND m.user_id = ?", actor.TenantID, actor.UserID).Order("p.created_at DESC, p.id DESC").Limit(limit).Scan(&rows).Error; err != nil {
		return nil, err
	}
	views := make([]Project, 0, len(rows))
	for _, row := range rows {
		p, err := projectView(t.db, row)
		if err != nil {
			return nil, err
		}
		views = append(views, p)
	}
	return views, nil
}
func (t gormTransaction) InsertProject(tenantID uint64, p Project) error {
	raw, err := json.Marshal(p.Spec)
	if err != nil {
		return err
	}
	row := projectRow{ID: p.ID, TenantID: tenantID, Name: p.Name, Status: p.Status, ProjectVersion: p.ProjectVersion, SpecRevision: p.SpecRevision, SpecJSON: string(raw), TemplateID: p.TemplateID, TemplateVersion: p.TemplateVersion}
	if err := t.db.Create(&row).Error; err != nil {
		return err
	}
	for _, m := range p.Members {
		if err := t.db.Create(&memberRow{ProjectID: p.ID, UserID: m.UserID, Role: m.Role}).Error; err != nil {
			return err
		}
		roles, _ := json.Marshal(m.FunctionRoles)
		scopes, _ := json.Marshal(m.FunctionScopes)
		governance := m.GovernanceRole
		if governance == "" {
			if m.Role == "owner" {
				governance = "owner"
			} else {
				governance = "member"
			}
		}
		status := m.Status
		if status == "" {
			status = "active"
		}
		if err := optionalPermissionTable(t.db.Create(&memberPermissionRow{ProjectID: p.ID, UserID: m.UserID, GovernanceRole: governance, FunctionRolesJSON: string(roles), FunctionScopesJSON: string(scopes), Status: status}).Error); err != nil {
			return err
		}
	}
	return nil
}
func affected(result *gorm.DB) error {
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrVersionConflict
	}
	return nil
}
func (t gormTransaction) UpdateProject(tenantID uint64, previous, next Project) error {
	raw, err := json.Marshal(next.Spec)
	if err != nil {
		return err
	}
	return affected(t.db.Model(&projectRow{}).Where("id = ? AND tenant_id = ? AND project_version = ? AND spec_revision = ? AND status = ?", previous.ID, tenantID, previous.ProjectVersion, previous.SpecRevision, previous.Status).Updates(map[string]any{"spec_json": string(raw), "spec_revision": next.SpecRevision, "project_version": next.ProjectVersion, "status": next.Status}))
}
func (t gormTransaction) ReplaceCollaborators(projectID string, ids []string) error {
	var existing []memberPermissionRow
	if err := optionalPermissionTable(t.db.Where("project_id = ?", projectID).Find(&existing).Error); err != nil {
		return err
	}
	retained := map[string]memberPermissionRow{}
	for _, row := range existing {
		retained[row.UserID] = row
	}
	if err := t.db.Where("project_id = ? AND role = ?", projectID, "collaborator").Delete(&memberRow{}).Error; err != nil {
		return err
	}
	if err := optionalPermissionTable(t.db.Where("project_id = ? AND governance_role <> ?", projectID, "owner").Delete(&memberPermissionRow{}).Error); err != nil {
		return err
	}
	for _, id := range ids {
		if err := t.db.Create(&memberRow{ProjectID: projectID, UserID: id, Role: "collaborator"}).Error; err != nil {
			return err
		}
		// Legacy collaborators retain project-level write compatibility; chapter
		// writes still require an explicit chapter scope through the sidecar.
		permission, ok := retained[id]
		if !ok {
			permission = memberPermissionRow{ProjectID: projectID, UserID: id, GovernanceRole: "member", FunctionRolesJSON: "[\"author\"]", FunctionScopesJSON: "{}", Status: "active"}
		}
		if err := optionalPermissionTable(t.db.Create(&permission).Error); err != nil {
			return err
		}
	}
	return nil
}

func (t gormTransaction) ReplaceMembers(projectID string, members []Member) error {
	sidecar := true
	if err := t.db.Where("project_id = ?", projectID).Delete(&memberPermissionRow{}).Error; err != nil {
		if strings.Contains(err.Error(), "no such table: lingdoc_member_permissions") {
			sidecar = false
		} else {
			return err
		}
	}
	if err := t.db.Where("project_id = ?", projectID).Delete(&memberRow{}).Error; err != nil {
		return err
	}
	for _, member := range members {
		if err := t.db.Create(&memberRow{ProjectID: projectID, UserID: member.UserID, Role: member.Role}).Error; err != nil {
			return err
		}
		if !sidecar {
			continue
		}
		go