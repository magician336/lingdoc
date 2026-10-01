package workspacecore

import (
	"context"
	"encoding/json"

	"gorm.io/gorm"
)

// Repository is the domain-facing persistence port. Implementations return
// stable domain values and sentinel errors; database rows and ORM types never
// cross this boundary. The current GORM adapter is replaceable by a fake or a
// different storage backend without changing Service or its callers.
type Repository interface {
	Authorize(ctx context.Context, actor Actor, projectID, capability string) error
	GetProject(ctx context.Context, actor Actor, id string) (Project, error)
	ListProjects(ctx context.Context, actor Actor) ([]Project, bool, error)
	CreateProject(ctx context.Context, actor Actor, key string, input CreateProjectInput) (json.RawMessage, int, bool, error)
	SaveSpec(ctx context.Context, actor Actor, projectID, key string, input SaveSpecInput) (json.RawMessage, int, bool, error)
	SaveMembers(ctx context.Context, actor Actor, projectID, key string, input SaveMembersInput) (json.RawMessage, int, bool, error)
	ActivateProject(ctx context.Context, actor Actor, projectID, key string, input ActivateProjectInput) (json.RawMessage, int, bool, error)
	ListChapters(ctx context.Context, actor Actor, projectID string) ([]Chapter, error)
	SaveChapter(ctx context.Context, actor Actor, projectID, chapterID, key string, input SaveChapterInput) (json.RawMessage, int, bool, error)
	GenerationContext(ctx context.Context, actor Actor, projectID, chapterID string) (GenerationContext, error)
}

// Service is the stable application-facing facade for workspace operations.
// It intentionally knows neither GORM nor any persistence models.
type Service struct {
	repository Repository
}

func NewService(db *gorm.DB, templates TemplateReader) *Service {
	return NewServiceWithRepository(NewGORMRepository(db, templates))
}

func NewServiceWithRepository(repository Repository) *Service {
	return &Service{repository: repository}
}

func (s *Service) Authorize(ctx context.Context, actor Actor, projectID, capability string) error {
	return s.repository.Authorize(ctx, actor, projectID, capability)
}

func (s *Service) GetProject(ctx context.Context, actor Actor, id string) (Project, error) {
	return s.repository.GetProject(ctx, actor, id)
}

func (s *Service) ListProjects(ctx context.Context, actor Actor) ([]Project, bool, error) {
	return s.repository.ListProjects(ctx, actor)
}

func (s *Service) CreateProject(ctx context.Context, actor Actor, key string, input CreateProjectInput) (json.RawMessage, int, bool, error) {
	return s.repository.CreateProject(ctx, actor, key, input)
}

func (s *Service) SaveSpec(ctx context.Context, actor Actor, projectID, key string, input SaveSpecInput) (json.RawMessage, int, bool, error) {
	return s.repository.SaveSpec(ctx, actor, projectID, key, input)
}

func (s *Service) SaveMembers(ctx context.Context, actor Actor, projectID, key string, input SaveMembersInput) (json.RawMessage, int, bool, error) {
	return s.repository.SaveMembers(ctx, actor, projectID, key, input)
}

func (s *Service) ActivateProject(ctx context.Context, actor Actor, projectID, key string, input ActivateProjectInput) (json.RawMessage, int, bool, error) {
	return s.repository.ActivateProject(ctx, actor, projectID, key, input)
}

func (s *Service) ListChapters(ctx context.Context, actor Actor, projectID string) ([]Chapter, error) {
	return s.repository.ListChapters(ctx, actor, projectID)
}

func (s *Service) SaveChapter(ctx context.Context, actor Actor, projectID, chapterID, key string, input SaveChapterInput) (json.RawMessage, int, bool, error) {
	return s.repository.SaveChapter(ctx, actor, projectID, chapterID, key, input)
}

func (s *Service) GenerationContext(ctx context.Context, actor Actor, projectID, chapterID string) (GenerationContext, error) {
	return s.repository.GenerationContext(ctx, actor, projectID, chapterID)
}

var _ Repository = (*GORMRepository)(nil)
