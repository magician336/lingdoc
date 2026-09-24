package workspacecore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Service struct {
	db        *gorm.DB
	templates TemplateReader
}

func NewService(db *gorm.DB, templates TemplateReader) *Service {
	return &Service{db: db, templates: templates}
}

func validActor(actor Actor) bool { return actor.TenantID != 0 && actor.UserID != "" }

func activeTenantMember(tx *gorm.DB, actor Actor) error {
	if !validActor(actor) {
		return ErrNotFound
	}
	var count int64
	if err := tx.Table("tenant_members").Where("tenant_id = ? AND user_id = ? AND status = ? AND deleted_at IS NULL",
		actor.TenantID, actor.UserID, "active").Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) findProject(tx *gorm.DB, actor Actor, projectID, capability string) (projectRow, error) {
	if err := activeTenantMember(tx, actor); err != nil {
		return projectRow{}, err
	}
	var project projectRow
	if err := tx.Where("id = ? AND tenant_id = ?", projectID, actor.TenantID).First(&project).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return projectRow{}, ErrNotFound
		}
		return projectRow{}, err
	}
	var member memberRow
	if err := tx.Where("project_id = ? AND user_id = ?", projectID, actor.UserID).First(&member).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return projectRow{}, ErrNotFound // Hide existence from non-members.
		}
		return projectRow{}, err
	}
	if capability == "manage" && member.Role != "owner" {
		return projectRow{}, ErrNotFound
	}
	return project, nil
}

// Authorize is deliberately only a project/member check. Source authorization
// belongs to T09; callers must check sources separately.
func (s *Service) Authorize(ctx context.Context, actor Actor, projectID, capability string) error {
	if capability != "read" && capability != "write" && capability != "manage" {
		return ErrInvalidRequest
	}
	_, err := s.findProject(s.db.WithContext(ctx), actor, projectID, capability)
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
	view := Project{
		ID: row.ID, Name: row.Name, Status: row.Status,
		ProjectVersion: row.ProjectVersion, SpecRevision: row.SpecRevision,
		Spec: spec, TemplateID: row.TemplateID, TemplateVersion: row.TemplateVersion,
		Members: make([]Member, 0, len(members)),
	}
	for _, m := range members {
		view.Members = append(view.Members, Member{UserID: m.UserID, Role: m.Role})
	}
	return view, nil
}

func (s *Service) GetProject(ctx context.Context, actor Actor, id string) (Project, error) {
	tx := s.db.WithContext(ctx)
	row, err := s.findProject(tx, actor, id, "read")
	if err != nil {
		return Project{}, err
	}
	return projectView(tx, row)
}

func (s *Service) ListProjects(ctx context.Context, actor Actor) ([]Project, bool, error) {
	if err := activeTenantMember(s.db.WithContext(ctx), actor); err != nil {
		return nil, false, err
	}
	var rows []projectRow
	err := s.db.WithContext(ctx).Table("lingdoc_projects AS p").
		Select("p.*").Joins("JOIN lingdoc_members AS m ON m.project_id = p.id").
		Where("p.tenant_id = ? AND m.user_id = ?", actor.TenantID, actor.UserID).
		Order("p.created_at DESC, p.id DESC").Limit(51).Scan(&rows).Error
	if err != nil {
		return nil, false, err
	}
	truncated := len(rows) > 50
	if truncated {
		rows = rows[:50]
	}
	views := make([]Project, 0, len(rows))
	for _, row := range rows {
		view, err := projectView(s.db.WithContext(ctx), row)
		if err != nil {
			return nil, false, err
		}
		views = append(views, view)
	}
	return views, truncated, nil
}

// operation stores the domain write and exact response in one transaction.
// Current project authorization runs before replay, so revoked users cannot
// recover cached content by presenting an old Idempotency-Key.
func (s *Service) operation(
	ctx context.Context, actor Actor, op, target, key string, body any,
	authorize func(*gorm.DB) error,
	write func(*gorm.DB) (any, int, error),
) (json.RawMessage, int, bool, error) {
	if !validActor(actor) || len(key) < 8 || len(key) > 128 {
		return nil, 0, false, ErrInvalidRequest
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, 0, false, ErrInvalidRequest
	}
	hash := sha256.Sum256(encoded)
	hashString := hex.EncodeToString(hash[:])
	identity := operationRow{TenantID: actor.TenantID, UserID: actor.UserID, Operation: op, Target: target, Key: key}
	var result json.RawMessage
	var status int
	var replayed bool
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if authorize != nil {
			if err := authorize(tx); err != nil {
				return err
			}
		}
		var previous operationRow
		err := tx.Where("tenant_id = ? AND user_id = ? AND operation = ? AND target = ? AND key = ?",
			actor.TenantID, actor.UserID, op, target, key).First(&previous).Error
		if err == nil {
			if previous.BodyHash != hashString {
				return ErrIdempotencyConflict
			}
			result, status, replayed = json.RawMessage(previous.ResponseJSON), previous.ResponseCode, true
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		value, code, err := write(tx)
		if err != nil {
			return err
		}
		result, err = json.Marshal(value)
		if err != nil {
			return err
		}
		identity.BodyHash, identity.ResponseJSON, identity.ResponseCode = hashString, string(result), code
		if err := tx.Create(&identity).Error; err != nil {
			// A concurrent request with this key may still be committing.
			return fmt.Errorf("%w: %v", ErrRequestInProgress, err)
		}
		status = code
		return nil
	})
	return result, status, replayed, err
}

type CreateProjectInput struct {
	Name       string `json:"name"`
	TemplateID string `json:"template_id"`
}

func (s *Service) CreateProject(ctx context.Context, actor Actor, key string, input CreateProjectInput) (json.RawMessage, int, bool, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || len([]rune(input.Name)) > 120 {
		return nil, 0, false, ErrInvalidRequest
	}
	template, err := s.templates.Get(input.TemplateID, "")
	if err != nil {
		return nil, 0, false, err
	}
	return s.operation(ctx, actor, "createProject", "projects", key, input, func(tx *gorm.DB) error {
		return activeTenantMember(tx, actor)
	}, func(tx *gorm.DB) (any, int, error) {
		row := projectRow{
			ID: uuid.NewString(), TenantID: actor.TenantID, Name: input.Name,
			Status: "draft", ProjectVersion: 1, SpecRevision: 0, SpecJSON: "{}",
			TemplateID: template.ID, TemplateVersion: template.Version,
		}
		if err := tx.Create(&row).Error; err != nil {
			return nil, 0, err
		}
		if err := tx.Create(&memberRow{ProjectID: row.ID, UserID: actor.UserID, Role: "owner"}).Error; err != nil {
			return nil, 0, err
		}
		view, err := projectView(tx, row)
		return view, 201, err
	})
}

type SaveSpecInput struct {
	ExpectedSpecRevision int64             `json:"expected_spec_revision"`
	Fields               map[string]string `json:"fields"`
}

func (s *Service) SaveSpec(ctx context.Context, actor Actor, projectID, key string, input SaveSpecInput) (json.RawMessage, int, bool, error) {
	if input.ExpectedSpecRevision < 0 || input.Fields == nil {
		return nil, 0, false, ErrInvalidRequest
	}
	for k, v := range input.Fields {
		if strings.TrimSpace(k) == "" || len(k) > 100 || len(v) > 10000 {
			return nil, 0, false, ErrInvalidRequest
		}
	}
	auth := func(tx *gorm.DB) error {
		_, err := s.findProject(tx, actor, projectID, "write")
		return err
	}
	raw, status, replayed, err := s.operation(ctx, actor, "saveSpec", projectID, key, input, auth, func(tx *gorm.DB) (any, int, error) {
		row, err := s.findProject(tx, actor, projectID, "write")
		if err != nil {
			return nil, 0, err
		}
		if row.SpecRevision != input.ExpectedSpecRevision {
			return nil, 0, ErrVersionConflict
		}
		current, err := decodeSpec(row.SpecJSON)
		if err != nil {
			return nil, 0, err
		}
		if !maps.Equal(current, input.Fields) {
			raw, err := json.Marshal(input.Fields)
			if err != nil {
				return nil, 0, err
			}
			res := tx.Model(&projectRow{}).Where("id = ? AND tenant_id = ? AND spec_revision = ? AND project_version = ?", projectID, actor.TenantID, row.SpecRevision, row.ProjectVersion).
				Updates(map[string]any{"spec_json": string(raw), "spec_revision": row.SpecRevision + 1, "project_version": row.ProjectVersion + 1})
			if res.Error != nil {
				return nil, 0, res.Error
			}
			if res.RowsAffected != 1 {
				return nil, 0, ErrVersionConflict
			}
			row.SpecJSON, row.SpecRevision, row.ProjectVersion = string(raw), row.SpecRevision+1, row.ProjectVersion+1
		}
		view, err := projectView(tx, row)
		return view, 200, err
	})
	if err != nil && !isWorkspaceDomainError(err) {
		// SQLite can reject a transaction that read the old row before another
		// writer committed (SQLITE_BUSY_SNAPSHOT). Once the failed transaction
		// is rolled back, report the optimistic-concurrency conflict instead of
		// leaking a driver-specific lock error when the expected revision is now
		// stale.
		current, readErr := s.GetProject(ctx, actor, projectID)
		if readErr == nil && current.SpecRevision != input.ExpectedSpecRevision {
			return nil, 0, false, ErrVersionConflict
		}
	}
	return raw, status, replayed, err
}

func isWorkspaceDomainError(err error) bool {
	for _, domainErr := range []error{
		ErrInvalidRequest, ErrInvalidState, ErrVersionConflict, ErrIdempotencyConflict,
		ErrRequestInProgress, ErrSourceUnavailable, ErrNotFound,
		gorm.ErrRecordNotFound, sql.ErrNoRows, context.Canceled, context.DeadlineExceeded,
	} {
		if errors.Is(err, domainErr) {
			return true
		}
	}
	return false
}

type ActivateProjectInput struct {
	ExpectedSpecRevision int64 `json:"expected_spec_revision"`
}

type SaveMembersInput struct {
	ExpectedProjectVersion int64    `json:"expected_project_version"`
	CollaboratorUserIDs    []string `json:"collaborator_user_ids"`
}

// SaveMembers is the optional fixed test group operation. There is no member
// management UI; only the project owner can select active tenant users.
func (s *Service) SaveMembers(ctx context.Context, actor Actor, projectID, key string, input SaveMembersInput) (json.RawMessage, int, bool, error) {
	if input.ExpectedProjectVersion < 1 || input.CollaboratorUserIDs == nil || len(input.CollaboratorUserIDs) > 20 {
		return nil, 0, false, ErrInvalidRequest
	}
	ids := slices.Clone(input.CollaboratorUserIDs)
	slices.Sort(ids)
	if len(slices.Compact(slices.Clone(ids))) != len(ids) {
		return nil, 0, false, ErrInvalidRequest
	}
	for _, id := range ids {
		if strings.TrimSpace(id) != id || id == "" || len(id) > 64 || id == actor.UserID {
			return nil, 0, false, ErrInvalidRequest
		}
	}
	auth := func(tx *gorm.DB) error {
		_, err := s.findProject(tx, actor, projectID, "manage")
		return err
	}
	return s.operation(ctx, actor, "saveMembers", projectID, key, input, auth, func(tx *gorm.DB) (any, int, error) {
		row, err := s.findProject(tx, actor, projectID, "manage")
		if err != nil {
			return nil, 0, err
		}
		if row.ProjectVersion != input.ExpectedProjectVersion {
			return nil, 0, ErrVersionConflict
		}
		if len(ids) > 0 {
			var active []string
			if err := tx.Table("tenant_members").Distinct().Where("tenant_id = ? AND user_id IN ? AND status = ? AND deleted_at IS NULL", actor.TenantID, ids, "active").Pluck("user_id", &active).Error; err != nil {
				return nil, 0, err
			}
			if len(active) != len(ids) {
				return nil, 0, ErrInvalidState
			}
		}
		var existing []string
		if err := tx.Model(&memberRow{}).Where("project_id = ? AND role = ?", projectID, "collaborator").Pluck("user_id", &existing).Error; err != nil {
			return nil, 0, err
		}
		slices.Sort(existing)
		if !slices.Equal(existing, ids) {
			res := tx.Model(&projectRow{}).Where("id = ? AND tenant_id = ? AND project_version = ?", projectID, actor.TenantID, row.ProjectVersion).
				Update("project_version", row.ProjectVersion+1)
			if res.Error != nil {
				return nil, 0, res.Error
			}
			if res.RowsAffected != 1 {
				return nil, 0, ErrVersionConflict
			}
			if err := tx.Where("project_id = ? AND role = ?", projectID, "collaborator").Delete(&memberRow{}).Error; err != nil {
				return nil, 0, err
			}
			for _, id := range ids {
				if err := tx.Create(&memberRow{ProjectID: projectID, UserID: id, Role: "collaborator"}).Error; err != nil {
					return nil, 0, err
				}
			}
			row.ProjectVersion++
		}
		view, err := projectView(tx, row)
		return view, 200, err
	})
}

func (s *Service) ActivateProject(ctx context.Context, actor Actor, projectID, key string, input ActivateProjectInput) (json.RawMessage, int, bool, error) {
	if input.ExpectedSpecRevision < 0 {
		return nil, 0, false, ErrInvalidRequest
	}
	auth := func(tx *gorm.DB) error {
		_, err := s.findProject(tx, actor, projectID, "write")
		return err
	}
	return s.operation(ctx, actor, "activateProject", projectID, key, input, auth, func(tx *gorm.DB) (any, int, error) {
		row, err := s.findProject(tx, actor, projectID, "write")
		if err != nil {
			return nil, 0, err
		}
		if row.SpecRevision != input.ExpectedSpecRevision {
			return nil, 0, ErrVersionConflict
		}
		if row.Status != "draft" {
			return nil, 0, ErrInvalidState
		}
		template, err := s.templates.Get(row.TemplateID, row.TemplateVersion)
		if err != nil || template.Version != row.TemplateVersion {
			return nil, 0, ErrInvalidState
		}
		spec, err := decodeSpec(row.SpecJSON)
		if err != nil {
			return nil, 0, err
		}
		for _, field := range template.RequiredFields {
			if strings.TrimSpace(spec[field]) == "" {
				return nil, 0, ErrInvalidState
			}
		}
		res := tx.Model(&projectRow{}).Where("id = ? AND tenant_id = ? AND spec_revision = ? AND project_version = ? AND status = ?", projectID, actor.TenantID, row.SpecRevision, row.ProjectVersion, "draft").
			Updates(map[string]any{"status": "active", "project_version": row.ProjectVersion + 1})
		if res.Error != nil {
			return nil, 0, res.Error
		}
		if res.RowsAffected != 1 {
			return nil, 0, ErrVersionConflict
		}
		for _, section := range template.Sections {
			chapter := chapterRow{ID: uuid.NewString(), ProjectID: projectID, SectionID: section.ID, Title: section.Title}
			if err := tx.Create(&chapter).Error; err != nil {
				return nil, 0, err
			}
		}
		row.Status, row.ProjectVersion = "active", row.ProjectVersion+1
		view, err := projectView(tx, row)
		return view, 200, err
	})
}

func chapterView(tx *gorm.DB, row chapterRow) (Chapter, error) {
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
	if err := json.Unmarshal([]byte(version.SourceIDsJSON), &view.SourceIDs); err != nil {
		return Chapter{}, err
	}
	if err := json.Unmarshal([]byte(version.ReviewItemsJSON), &view.ReviewItems); err != nil {
		return Chapter{}, err
	}
	return view, nil
}

func (s *Service) ListChapters(ctx context.Context, actor Actor, projectID string) ([]Chapter, error) {
	tx := s.db.WithContext(ctx)
	if _, err := s.findProject(tx, actor, projectID, "read"); err != nil {
		return nil, err
	}
	var rows []chapterRow
	if err := tx.Where("project_id = ?", projectID).Order("section_id").Find(&rows).Error; err != nil {
		return nil, err
	}
	views := make([]Chapter, 0, len(rows))
	for _, row := range rows {
		view, err := chapterView(tx, row)
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	return views, nil
}

type SaveChapterInput struct {
	ExpectedChapterVersionID *string  `json:"expected_chapter_version_id"`
	ExpectedSpecRevision     int64    `json:"expected_spec_revision"`
	BodyMarkdown             string   `json:"body_markdown"`
	SourceIDs                []string `json:"source_ids"`
}

var sourceMarker = regexp.MustCompile(`\[\[source:([A-Za-z0-9_-]+)\]\]`)

func (s *Service) SaveChapter(ctx context.Context, actor Actor, projectID, chapterID, key string, input SaveChapterInput) (json.RawMessage, int, bool, error) {
	if input.ExpectedSpecRevision < 0 || input.SourceIDs == nil || len(input.BodyMarkdown) > 200000 {
		return nil, 0, false, ErrInvalidRequest
	}
	markers := sourceMarker.FindAllStringSubmatch(input.BodyMarkdown, -1)
	if strings.Count(input.BodyMarkdown, "[[source:") != len(markers) {
		return nil, 0, false, ErrInvalidRequest
	}
	refs := make([]string, 0, len(markers))
	for _, marker := range markers {
		refs = append(refs, marker[1])
	}
	slices.Sort(refs)
	refs = slices.Compact(refs)
	declared := slices.Clone(input.SourceIDs)
	slices.Sort(declared)
	if len(slices.Compact(slices.Clone(declared))) != len(declared) || !slices.Equal(refs, declared) {
		return nil, 0, false, ErrInvalidRequest
	}
	auth := func(tx *gorm.DB) error {
		_, err := s.findProject(tx, actor, projectID, "write")
		return err
	}
	return s.operation(ctx, actor, "saveChapter", projectID+"/"+chapterID, key, input, auth, func(tx *gorm.DB) (any, int, error) {
		if len(declared) != 0 {
			return nil, 0, ErrSourceUnavailable
		} // T09 must provide current SourcePolicy first.
		project, err := s.findProject(tx, actor, projectID, "write")
		if err != nil {
			return nil, 0, err
		}
		if project.Status != "active" {
			return nil, 0, ErrInvalidState
		}
		if project.SpecRevision != input.ExpectedSpecRevision {
			return nil, 0, ErrVersionConflict
		}
		var chapter chapterRow
		if err := tx.Where("id = ? AND project_id = ?", chapterID, projectID).First(&chapter).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, 0, ErrNotFound
			}
			return nil, 0, err
		}
		if (chapter.CurrentVersionID == nil) != (input.ExpectedChapterVersionID == nil) ||
			(chapter.CurrentVersionID != nil && *chapter.CurrentVersionID != *input.ExpectedChapterVersionID) {
			return nil, 0, ErrVersionConflict
		}
		old, err := chapterView(tx, chapter)
		if err != nil {
			return nil, 0, err
		}
		if len(old.SourceIDs) != 0 {
			return nil, 0, ErrSourceUnavailable // T09 must revalidate existing citations first.
		}
		// Serialize chapter and spec writes through the project version row.
		res := tx.Model(&projectRow{}).Where("id = ? AND tenant_id = ? AND project_version = ? AND spec_revision = ?", projectID, actor.TenantID, project.ProjectVersion, project.SpecRevision).
			Update("project_version", project.ProjectVersion+1)
		if res.Error != nil {
			return nil, 0, res.Error
		}
		if res.RowsAffected != 1 {
			return nil, 0, ErrVersionConflict
		}
		newVersionID := uuid.NewString()
		reviewJSON, err := json.Marshal(old.ReviewItems) // Manual edits retain every unresolved item.
		if err != nil {
			return nil, 0, err
		}
		version := chapterVersionRow{ID: newVersionID, ProjectID: projectID, ChapterID: chapterID,
			ParentVersionID: chapter.CurrentVersionID, BodyMarkdown: input.BodyMarkdown,
			SourceIDsJSON: "[]", ReviewItemsJSON: string(reviewJSON)}
		if err := tx.Create(&version).Error; err != nil {
			return nil, 0, err
		}
		query := tx.Model(&chapterRow{}).Where("id = ? AND project_id = ?", chapterID, projectID)
		if chapter.CurrentVersionID == nil {
			query = query.Where("current_version_id IS NULL")
		} else {
			query = query.Where("current_version_id = ?", *chapter.CurrentVersionID)
		}
		res = query.Update("current_version_id", newVersionID)
		if res.Error != nil {
			return nil, 0, res.Error
		}
		if res.RowsAffected != 1 {
			return nil, 0, ErrVersionConflict
		}
		chapter.CurrentVersionID = &newVersionID
		view, err := chapterView(tx, chapter)
		return view, 201, err
	})
}

// GenerationContext is the T08 handoff to T10. Repeatable-read binds the
// project spec and the current chapter to one database snapshot.
func (s *Service) GenerationContext(ctx context.Context, actor Actor, projectID, chapterID string) (GenerationContext, error) {
	var result GenerationContext
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		project, err := s.findProject(tx, actor, projectID, "read")
		if err != nil {
			return err
		}
		var chapter chapterRow
		if err := tx.Where("id = ? AND project_id = ?", chapterID, projectID).First(&chapter).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		view, err := chapterView(tx, chapter)
		if err != nil {
			return err
		}
		spec, err := decodeSpec(project.SpecJSON)
		if err != nil {
			return err
		}
		result = GenerationContext{ProjectID: projectID, ProjectVersion: project.ProjectVersion,
			SpecRevision: project.SpecRevision, Spec: spec, TemplateID: project.TemplateID,
			TemplateVersion: project.TemplateVersion, ChapterID: chapterID,
			ChapterVersionID: view.CurrentVersionID, ChapterBody: view.BodyMarkdown}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return result, err
}
