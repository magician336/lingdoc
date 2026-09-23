package workspace

import (
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"gorm.io/gorm"
)

type Service = core.Service
type Actor = core.Actor
type CreateProjectInput = core.CreateProjectInput
type SaveSpecInput = core.SaveSpecInput
type ActivateProjectInput = core.ActivateProjectInput
type SaveChapterInput = core.SaveChapterInput
type SaveMembersInput = core.SaveMembersInput
type ContractDemoTemplate = core.ContractDemoTemplate

var (
	ErrNotFound            = core.ErrNotFound
	ErrInvalidRequest      = core.ErrInvalidRequest
	ErrInvalidState        = core.ErrInvalidState
	ErrVersionConflict     = core.ErrVersionConflict
	ErrIdempotencyConflict = core.ErrIdempotencyConflict
	ErrRequestInProgress   = core.ErrRequestInProgress
	ErrSourceUnavailable   = core.ErrSourceUnavailable
)

func NewService(db *gorm.DB, templates core.TemplateReader) *Service {
	return core.NewService(db, templates)
}
