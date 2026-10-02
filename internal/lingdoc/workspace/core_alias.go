package workspace

import (
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

type Service = core.Service
type Repository = core.Repository
type SourcePolicy = core.SourcePolicy
type Actor = core.Actor
type Project = core.Project
type Chapter = core.Chapter
type GenerationContext = core.GenerationContext
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

func NewService(repository Repository) *Service {
	return core.NewService(repository)
}
