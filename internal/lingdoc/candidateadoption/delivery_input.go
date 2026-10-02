package candidateadoption

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// WorkspaceDeliveryInput is the workspace-owned, versioned portion of the
// input consumed by T13. Template rules and current source records are resolved
// by their owning providers after this consistent read.
type WorkspaceDeliveryInput struct {
	ProjectID       string                     `json:"project_id"`
	ProjectName     string                     `json:"project_name"`
	ProjectVersion  int64                      `json:"project_version"`
	SpecRevision    int                        `json:"spec_revision"`
	Spec            map[string]string          `json:"spec"`
	TemplateID      string                     `json:"template_id"`
	TemplateVersion string                     `json:"template_version"`
	Chapters        []WorkspaceDeliveryChapter `json:"chapters"`
}

// WorkspaceDeliveryChapter deliberately allows a nil chapter version and a
// nil confirmation. Empty or unconfirmed chapters must reach T13 so it can
// persist an actionable blocked snapshot rather than hiding incomplete work.
type WorkspaceDeliveryChapter struct {
	ChapterID        string        `json:"chapter_id"`
	SectionID        string        `json:"section_id"`
	Title            string        `json:"title"`
	ChapterVersionID *string       `json:"chapter_version_id"`
	BodyMarkdown     string        `json:"body_markdown"`
	SourceIDs        []string      `json:"source_ids"`
	ReviewItems      []ReviewItem  `json:"review_items"`
	Confirmation     *Confirmation `json:"confirmation,omitempty"`
}

// DeliveryInputReader is the T12 handoff to T13. Implementations must return
// project, spec, current chapter versions, and confirmations from one
// workspace snapshot. Callers must authorize the actor before reading and
// revalidate workspace/source versions around later cross-provider reads.
type DeliveryInputReader interface {
	ReadDeliveryInput(context.Context, string) (WorkspaceDeliveryInput, error)
}

// DeliveryInputProjectAuthorizer keeps project membership checks ahead of any
// workspace read. The application adapter should delegate this to T07 rather
// than inventing a second membership store.
type DeliveryInputProjectAuthorizer interface {
	AuthorizeProject(context.Context, string, string) error
}

type DeliveryInputService struct {
	Reader     DeliveryInputReader
	Authorizer DeliveryInputProjectAuthorizer
}

func (s *DeliveryInputService) Read(ctx context.Context, actorID, projectID string) (WorkspaceDeliveryInput, error) {
	if actorID == "" || projectID == "" {
		return WorkspaceDeliveryInput{}, ErrInvalidRequest
	}
	if s.Reader == nil || s.Authorizer == nil {
		return WorkspaceDeliveryInput{}, ErrInvalidState
	}
	if err := s.Authorizer.AuthorizeProject(ctx, actorID, projectID); err != nil {
		return WorkspaceDeliveryInput{}, err
	}
	return s.Reader.ReadDeliveryInput(ctx, projectID)
}

// ReadDeliveryInput reads all workspace-owned delivery inputs in one
// repeatable-read transaction. It includes empty chapters and only returns a
// confirmation that still matches the project's current spec/template and the
// chapter's current immutable version.
func (s *SQLiteCandidateAdoptionStore) ReadDeliveryInput(ctx context.Context, projectID string) (WorkspaceDeliveryInput, error) {
	var result WorkspaceDeliveryInput
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var project projectRow
		if err := tx.Where("id = ?", projectID).First(&project).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		spec, err := decodeWorkspaceSpec(project.SpecJSON)
		if err != nil {
			return err
		}
		result = WorkspaceDeliveryInput{
			ProjectID: project.ID, ProjectName: project.Name, ProjectVersion: project.ProjectVersion,
			SpecRevision: int(project.SpecRevision), Spec: spec,
			TemplateID: project.TemplateID, TemplateVersion: project.TemplateVersion,
			Chapters: []WorkspaceDeliveryChapter{},
		}
		var rows []chapterRow
		if err := tx.Where("project_id = ?", projectID).Order("section_id ASC, id ASC").Find(&rows).Error; err != nil {
			return err
		}
		result.Chapters = make([]WorkspaceDeliveryChapter, 0, len(rows))
		for _, row := range rows {
			chapter, err := s.chapterFromRow(tx, row)
			if err != nil {
				return err
			}
			frozen := WorkspaceDeliveryChapter{
				ChapterID: chapter.ID, SectionID: chapter.SectionID, Title: chapter.Title,
				ChapterVersionID: cloneString(chapter.CurrentVersionID), BodyMarkdown: chapter.BodyMarkdown,
				SourceIDs: append([]string{}, chapter.SourceIDs...), ReviewItems: append([]ReviewItem{}, chapter.ReviewItems...),
			}
			if chapter.CurrentVersionID != nil {
				confirmation, err := currentConfirmation(tx, chapter.ID, *chapter.CurrentVersionID, int(project.SpecRevision), project.TemplateVersion)
				if err != nil {
					return err
				}
				frozen.Confirmation = confirmation
			}
			result.Chapters = append(result.Chapters, frozen)
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return WorkspaceDeliveryInput{}, err
	}
	return result, nil
}

func currentConfirmation(tx *gorm.DB, chapterID, versionID string, specRevision int, templateVersion string) (*Confirmation, error) {
	var rows []confirmationRow
	if err := tx.Where("chapter_id = ? AND chapter_version_id = ? AND valid = ?", chapterID, versionID, true).
		Order("created_at DESC, id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		var confirmation Confirmation
		if err := json.Unmarshal([]byte(row.DetailsJSON), &confirmation); err != nil {
			return nil, fmt.Errorf("decode chapter confirmation %q: %w", row.ID, err)
		}
		if confirmation.Valid && confirmation.ID == row.ID && confirmation.ChapterID == chapterID &&
			confirmation.ChapterVersionID == versionID && confirmation.SpecRevision == specRevision &&
			confirmation.TemplateVersion == templateVersion {
			return &confirmation, nil
		}
	}
	return nil, nil
}

func decodeWorkspaceSpec(raw string) (map[string]string, error) {
	var spec map[string]string
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		return nil, fmt.Errorf("decode workspace spec for delivery input: %w", err)
	}
	if spec == nil {
		spec = map[string]string{}
	}
	return spec, nil
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
