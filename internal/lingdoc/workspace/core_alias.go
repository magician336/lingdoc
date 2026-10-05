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
type TransferOwnerInput = core.TransferOwnerInput
type SpecField = core.SpecField
type SpecFieldInput = core.SpecFieldInput
type ProvenanceRecord = core.ProvenanceRecord
type DraftCandidate = core.DraftCandidate
type DraftCandidateInput = core.DraftCandidateInput
type AuditEvent = core.AuditEvent
type SpecFieldChange = core.SpecFieldChange
type ActivationDiff = core.ActivationDiff
type OwnerTransfer = core.OwnerTransfer
type OwnerTransferInput = core.OwnerTransferInput
type TemplateMigrationInput = core.TemplateMigrationInput
type TemplateMigrationField = core.TemplateMigrationField
type TemplateMigrationPreview = core.TemplateMigrationPreview
type ProjectTemplateCopy = core.ProjectTemplateCopy
type TemplateCopyDefinitionInput = core.TemplateCopyDefinitionInput
type TemplateSectionChange = core.TemplateSectionChange
type CoreProjectAuthorizer = core.ProjectAuthorizer
type AuthorizationMode = core.AuthorizationMode
type AuditSink = core.AuditSink
type ContractDemoTemplate = core.ContractDemoTemplate
type TemplateField = core.TemplateField
type ChangeSet = core.ChangeSet
type ChangeImpact = core.ChangeImpact
type ChangeFieldInput = core.ChangeFieldInput
type ChangeFieldDelta = core.ChangeFieldDelta
type CreateChangeSetInput = core.CreateChangeSetInput

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
