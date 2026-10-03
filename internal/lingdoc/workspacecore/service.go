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
func projectView(tx *gorm.DB, row projectRow) (Project, error) {
	spec, err := decodeSpec(row.SpecJSON)
	if err != nil {
		return Project{}, err
	}
	specFields, err := decodeSpecFields(row.SpecMetadataJSON, spec)
	if err != nil {
		return Project{}, err
	}
	var members []memberRow
	if err := tx.Where("project_id = ?", row.ID).Order("user_id").Find(&members).Error; err != nil {
		return Project{}, err
	}
	p := Project{ID: row.ID, Name: row.Name, Status: row.Status, ProjectVersion: row.ProjectVersion, SpecRevision: row.SpecRevision, CurrentContextRevision: row.CurrentContextRevision, Spec: spec, SpecFields: specFields, TemplateID: row.TemplateID, TemplateVersion: row.TemplateVersion, Members: make([]Member, 0, len(members))}
	for _, m := range members {
		p.Members = append(p.Members, Member{UserID: m.UserID, Role: m.Role})
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
	metadata, err := json.Marshal(p.SpecFields)
	if err != nil {
		return err
	}
	row := projectRow{ID: p.ID, TenantID: tenantID, Name: p.Name, Status: p.Status, ProjectVersion: p.ProjectVersion, SpecRevision: p.SpecRevision, CurrentContextRevision: p.CurrentContextRevision, SpecJSON: string(raw), SpecMetadataJSON: string(metadata), TemplateID: p.TemplateID, TemplateVersion: p.TemplateVersion}
	if err := t.db.Create(&row).Error; err != nil {
		return err
	}
	for _, m := range p.Members {
		if err := t.db.Create(&memberRow{ProjectID: p.ID, UserID: m.UserID, Role: m.Role}).Error; err != nil {
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
	metadata, err := json.Marshal(next.SpecFields)
	if err != nil {
		return err
	}
	return affected(t.db.Model(&projectRow{}).Where("id = ? AND tenant_id = ? AND project_version = ? AND spec_revision = ? AND status = ?", previous.ID, tenantID, previous.ProjectVersion, previous.SpecRevision, previous.Status).Updates(map[string]any{"spec_json": string(raw), "spec_metadata_json": string(metadata), "spec_revision": next.SpecRevision, "current_context_revision": next.CurrentContextRevision, "project_version": next.ProjectVersion, "status": next.Status, "template_id": next.TemplateID, "template_version": next.TemplateVersion}))
}
func (t gormTransaction) ReplaceCollaborators(projectID string, ids []string) error {
	if err := t.db.Where("project_id = ? AND role = ?", projectID, "collaborator").Delete(&memberRow{}).Error; err != nil {
		return err
	}
	for _, id := range ids {
		if err := t.db.Create(&memberRow{ProjectID: projectID, UserID: id, Role: "collaborator"}).Error; err != nil {
			return err
		}
	}
	return nil
}

func (t gormTransaction) InsertDraftCandidate(candidate DraftCandidate) error {
	provenance, err := json.Marshal(candidate.Provenance)
	if err != nil {
		return err
	}
	return t.db.Create(&draftCandidateRow{ID: candidate.ID, ProjectID: candidate.ProjectID, Kind: candidate.Kind, Title: candidate.Title, Content: candidate.Content, Level: candidate.Level, BasedOnContextRevision: candidate.BasedOnContextRevision, ProvenanceJSON: string(provenance), CreatedBy: candidate.CreatedBy, CreatedAt: candidate.CreatedAt}).Error
}

func (t gormTransaction) DraftCandidates(projectID string) ([]DraftCandidate, error) {
	var rows []draftCandidateRow
	if err := t.db.Where("project_id = ?", projectID).Order("created_at, id").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]DraftCandidate, 0, len(rows))
	for _, row := range rows {
		var provenance *ProvenanceRecord
		if row.ProvenanceJSON != "" && row.ProvenanceJSON != "{}" && row.ProvenanceJSON != "null" {
			provenance = &ProvenanceRecord{}
			if err := json.Unmarshal([]byte(row.ProvenanceJSON), provenance); err != nil {
				return nil, err
			}
		}
		result = append(result, DraftCandidate{ID: row.ID, ProjectID: row.ProjectID, Kind: row.Kind, Title: row.Title, Content: row.Content, Level: row.Level, BasedOnContextRevision: row.BasedOnContextRevision, Provenance: provenance, CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt})
	}
	return result, nil
}

func (t gormTransaction) RecordAudit(event AuditEvent) error {
	details, err := json.Marshal(event.Details)
	if err != nil {
		return err
	}
	return t.db.Create(&auditEventRow{ID: event.ID, ProjectID: event.ProjectID, ActorID: event.ActorID, Action: event.Action, Target: event.Target, DetailsJSON: string(details), CreatedAt: event.CreatedAt}).Error
}

func (t gormTransaction) AuditEvents(projectID string) ([]AuditEvent, error) {
	var rows []auditEventRow
	if err := t.db.Where("project_id = ?", projectID).Order("created_at, id").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]AuditEvent, 0, len(rows))
	for _, row := range rows {
		details := map[string]any{}
		if row.DetailsJSON != "" && row.DetailsJSON != "{}" {
			if err := json.Unmarshal([]byte(row.DetailsJSON), &details); err != nil {
				return nil, err
			}
		}
		result = append(result, AuditEvent{ID: row.ID, ProjectID: row.ProjectID, ActorID: row.ActorID, Action: row.Action, Target: row.Target, Details: details, CreatedAt: row.CreatedAt})
	}
	return result, nil
}

func (t gormTransaction) InsertOwnerTransfer(transfer OwnerTransfer) error {
	return t.db.Create(&ownerTransferRow{ID: transfer.ID, ProjectID: transfer.ProjectID, FromUserID: transfer.FromUserID, ToUserID: transfer.ToUserID, Status: transfer.Status, ExpectedProjectVersion: transfer.ExpectedProjectVersion, CreatedAt: transfer.CreatedAt}).Error
}

func (t gormTransaction) OwnerTransfer(projectID, id string) (OwnerTransfer, error) {
	var row ownerTransferRow
	if err := t.db.Where("project_id = ? AND id = ?", projectID, id).First(&row).Error; err != nil {
		return OwnerTransfer{}, storageError(err)
	}
	return OwnerTransfer{ID: row.ID, ProjectID: row.ProjectID, FromUserID: row.FromUserID, ToUserID: row.ToUserID, Status: row.Status, ExpectedProjectVersion: row.ExpectedProjectVersion, CreatedAt: row.CreatedAt, AcceptedAt: row.AcceptedAt}, nil
}

func (t gormTransaction) AcceptOwnerTransfer(projectID, id string, version int64, acceptedAt time.Time) error {
	result := t.db.Model(&ownerTransferRow{}).Where("project_id = ? AND id = ? AND status = ? AND expected_project_version = ?", projectID, id, "pending", version).Updates(map[string]any{"status": "accepted", "accepted_at": acceptedAt})
	return affected(result)
}

func (t gormTransaction) SetMemberRole(projectID, userID, role string) error {
	result := t.db.Model(&memberRow{}).Where("project_id = ? AND user_id = ?", projectID, userID).Update("role", role)
	return affected(result)
}

func (t gormTransaction) EnsureMember(projectID, userID, role string) error {
	var row memberRow
	err := t.db.Where("project_id = ? AND user_id = ?", projectID, userID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return t.db.Create(&memberRow{ProjectID: projectID, UserID: userID, Role: role}).Error
	}
	return err
}
func chapterView(tx *gorm.DB, row chapterRow, project projectRow) (Chapter, error) {
	view := Chapter{ID: row.ID, ProjectID: row.ProjectID, SectionID: row.SectionID, Title: row.Title,
		CurrentVersionID: row.CurrentVersionID, BodyMarkdown: "", SourceIDs: []string{}, ReviewItems: []ReviewItem{}, ConfirmationValid: false}
	if row.CurrentVersionID == nil {
		return view, nil
	}
	var version chapterVersionRow
	if err := tx.Where("id = ? AND project_id = ? AND chapter_id = ?", *row.CurrentVersionID, row.ProjectID, row.ID).First(&version).Error; err != nil {
		return Chapter{}, err
	}
	view.BodyMarkdown = version.BodyMarkdown
	if version.ConfirmationValid {
		var confirmations []chapterConfirmationRow
		if err := tx.Where("chapter_id = ? AND chapter_version_id = ? AND valid = ?", row.ID, version.ID, true).
			Order("created_at DESC, id DESC").Find(&confirmations).Error; err != nil {
			return Chapter{}, err
		}
		for _, confirmation := range confirmations {
			var details chapterConfirmationDetails
			if err := json.Unmarshal([]byte(confirmation.DetailsJSON), &details); err != nil {
				return Chapter{}, fmt.Errorf("decode chapter confirmation %q: %w", confirmation.ID, err)
			}
			if details.Valid && details.ID == confirmation.ID && details.ChapterVersionID == version.ID &&
				details.SpecRevision == project.SpecRevision && details.TemplateVersion == project.TemplateVersion {
				view.ConfirmationValid = true
				break
			}
		}
	}
	if err := json.Unmarshal([]byte(version.SourceIDsJSON), &view.SourceIDs); err != nil {
		return Chapter{}, err
	}
	if view.SourceIDs == nil {
		// 这一列理论上只由 SaveChapter 写入，而它写的一定是 [] 或 ["…"]。留着这一行是
		// 因为 json.Unmarshal 会把 null 解成 nil，而 nil 一旦漏出去，上面建立的
		//「没有引用时是空切片」不变式就断了——调用方按 nil 与空切片分不出同一件事。
		view.SourceIDs = []string{}
	}
	if err := json.Unmarshal([]byte(version.ReviewItemsJSON), &view.ReviewItems); err != nil {
		return Chapter{}, err
	}
	return view, nil
}

func (t gormTransaction) Chapter(projectID, chapterID string) (Chapter, error) {
	var row chapterRow
	if err := t.db.Where("id = ? AND project_id = ?", chapterID, projectID).First(&row).Error; err != nil {
		return Chapter{}, storageError(err)
	}
	var project projectRow
	if err := t.db.Where("id = ?", projectID).First(&project).Error; err != nil {
		return Chapter{}, storageError(err)
	}
	return chapterView(t.db, row, project)
}
func (t gormTransaction) Chapters(projectID string) ([]Chapter, error) {
	var rows []chapterRow
	if err := t.db.Where("project_id = ?", projectID).Order("section_id").Find(&rows).Error; err != nil {
		return nil, err
	}
	var project projectRow
	if err := t.db.Where("id = ?", projectID).First(&project).Error; err != nil {
		return nil, storageError(err)
	}
	views := make([]Chapter, 0, len(rows))
	for _, row := range rows {
		view, err := chapterView(t.db, row, project)
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	return views, nil
}
func (t gormTransaction) InsertChapter(c Chapter) error {
	return t.db.Create(&chapterRow{ID: c.ID, ProjectID: c.ProjectID, SectionID: c.SectionID, Title: c.Title}).Error
}
func (t gormTransaction) AppendChapter(previous, next Chapter, specRevision int64) error {
	sources, err := json.Marshal(next.SourceIDs)
	if err != nil {
		return err
	}
	review, err := json.Marshal(next.ReviewItems)
	if err != nil {
		return err
	}
	version := chapterVersionRow{ID: *next.CurrentVersionID, ProjectID: next.ProjectID, ChapterID: next.ID, ParentVersionID: previous.CurrentVersionID, BodyMarkdown: next.BodyMarkdown, SourceIDsJSON: string(sources), ReviewItemsJSON: string(review), SpecRevision: specRevision}
	if err := t.db.Create(&version).Error; err != nil {
		return err
	}
	query := t.db.Model(&chapterRow{}).Where("id = ? AND project_id = ?", previous.ID, previous.ProjectID)
	if previous.CurrentVersionID == nil {
		query = query.Where("current_version_id IS NULL")
	} else {
		query = query.Where("current_version_id = ?", *previous.CurrentVersionID)
	}
	return affected(query.Update("current_version_id", *next.CurrentVersionID))
}
func (t gormTransaction) Operation(id OperationIdentity) (OperationResult, bool, error) {
	var row operationRow
	err := t.db.Where("tenant_id = ? AND user_id = ? AND operation = ? AND target = ? AND key = ?", id.Actor.TenantID, id.Actor.UserID, id.Operation, id.Target, id.Key).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return OperationResult{}, false, nil
	}
	if err != nil {
		return OperationResult{}, false, err
	}
	return OperationResult{BodyHash: row.BodyHash, Body: json.RawMessage(row.ResponseJSON), Status: row.ResponseCode}, true, nil
}
func (t gormTransaction) SaveOperation(id OperationIdentity, result OperationResult) error {
	row := operationRow{TenantID: id.Actor.TenantID, UserID: id.Actor.UserID, Operation: id.Operation, Target: id.Target, Key: id.Key, BodyHash: result.BodyHash, ResponseJSON: string(result.Body), ResponseCode: result.Status}
	if err := t.db.Create(&row).Error; err != nil {
		return fmt.Errorf("%w: %v", ErrRequestInProgress, err)
	}
	return nil
}
func isSQLiteLockError(err error) bool {
	if err == nil {
		return false
	}
	var codedError interface{ Code() int }
	if errors.As(err, &codedError) {
		code := codedError.Code() & 0xff
		return code == 5 || code == 6
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "database is locked") || strings.Contains(message, "database table is locked")
}

var _ Repository = (*GORMRepository)(nil)
var _ Transaction = gormTransaction{}
