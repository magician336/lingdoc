package workspace

import (
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

type Service = core.Service
type Repository = core.Repository
type SourcePolicy = core.SourcePolicy
type CoreProjectAuthorizer = core.ProjectAuthorizer
type Actor = core.Actor
type AuthorizationMode = core.AuthorizationMode
type Project = core.Project
type Chapter = core.Chapter
type WorkingCopy = core.WorkingCopy
type ChapterVersion = core.ChapterVersion
type GenerationContext = core.GenerationContext
type CreateProjectInput = core.CreateProjectInput
type SaveSpecInput = core.SaveSpecInput
type ActivateProjectInput = core.ActivateProjectInput
type SaveChapterInput = core.SaveChapterInput
type SaveWorkingCopyInput = core.SaveWorkingCopyInput
type CommitWorkingCopyInput = core.CommitWorkingCopyInput
type RestoreWorkingCopyInput = core.RestoreWorkingCopyInput
type CommittedChapterVersion = core.CommittedChapterVersion
type SaveMembersInput = core.SaveMembersInput
type TransferOwnerInput = core.TransferOwnerInput
type AuditEvent = core.AuditEvent
type AuditSink = core.AuditSink
type ContractDemoTemplate = core.ContractDemoTemplate

const (
	AuthorizationModeLog      = core.AuthorizationModeLog
	AuthorizationModeEnforce  = core.AuthorizationModeEnforce
	AuthorizationModeRollback = core.AuthorizationModeRollback
)

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

func NewServiceWithAuthorizer(repository Repository, reader core.TemplateReader, sources SourcePolicy, authorizer core.ProjectAuthorizer) *Service {
	return core.NewServiceWithAuthorizer(repository, reader, sources, authorizer)
}

func NewServiceWithAudit(repository Repository, reader core.TemplateReader, sources SourcePolicy, authorizer core.ProjectAuthorizer, audit core.AuditSink) *Service {
	return core.NewServiceWithAudit(repository, reader, sources, authorizer, audit)
}

func NewServiceWithAuditMode(repository Repository, reader core.TemplateReader, sources SourcePolicy, authorizer core.ProjectAuthorizer, audit core.AuditSink, mode AuthorizationMode) *Service {
	return core.NewServiceWithAuditMode(repository, reader, sources, authorizer, audit, mode)
}
