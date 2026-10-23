package workspace

import (
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

type Repository = core.Repository
type SourcePolicy = core.SourcePolicy
type Actor = core.Actor
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
type BindTemplateCopyRuleSetInput = core.BindTemplateCopyRuleSetInput
type TemplateSectionChange = core.TemplateSectionChange
type CoreProjectAuthorizer = core.ProjectAuthorizer
type AuthorizationMode = core.AuthorizationMode
type AuthorizationAction = core.AuthorizationAction
type ResourceKind = core.ResourceKind
type ResourceRef = core.ResourceRef
type ResourceAuthorizationDecision = core.ResourceAuthorizationDecision
type AuditSink = core.AuditSink
type ContractDemoTemplate = core.ContractDemoTemplate
type TemplateField = core.TemplateField
type ChangeSet = core.ChangeSet
type DependencyEdge = core.DependencyEdge
type TemplateUpgrade = core.TemplateUpgrade
type TemplateUpgradeInput = core.TemplateUpgradeInput
type TemplateUpgradeImpact = core.TemplateUpgradeImpact
type ChangeImpact = core.ChangeImpact
type ChangeFieldInput = core.ChangeFieldInput
type ChangeFieldDelta = core.ChangeFieldDelta
type CreateChangeSetInput = core.CreateChangeSetInput
type ValidationIssueBinding = core.ValidationIssueBinding
type ValidationIssueDispositionInput = core.ValidationIssueDispositionInput
type ValidationIssueDisposition = core.ValidationIssueDisposition
type SaveSpecResult = core.SaveSpecResult
type CreateProjectResult = core.CreateProjectResult
type ProjectMutationResult = core.ProjectMutationResult
type OwnerTransferResult = core.OwnerTransferResult
type ChapterMutationResult = core.ChapterMutationResult
type WorkingCopyMutationResult = core.WorkingCopyMutationResult
type CommittedChapterMutationResult = core.CommittedChapterMutationResult
type ChangeSetMutationResult = core.ChangeSetMutationResult
type DraftCandidateMutationResult = core.DraftCandidateMutationResult
type ValidationIssueDispositionResult = core.ValidationIssueDispositionResult
type ValidationIssueDispositionReplayResult = core.ValidationIssueDispositionReplayResult

const (
	AuthorizationModeLog      = core.AuthorizationModeLog
	AuthorizationModeEnforce  = core.AuthorizationModeEnforce
	AuthorizationModeRollback = core.AuthorizationModeRollback
	AuthorizationResearch     = core.AuthorizationResearch
	ResourceChapter           = core.ResourceChapter
	ResourceAsset             = core.ResourceAsset
)

var (
	ErrNotFound                    = core.ErrNotFound
	ErrInvalidRequest              = core.ErrInvalidRequest
	ErrInvalidState                = core.ErrInvalidState
	ErrVersionConflict             = core.ErrVersionConflict
	ErrDependencyDecided           = core.ErrDependencyDecided
	ErrIdempotencyConflict         = core.ErrIdempotencyConflict
	ErrRequestInProgress           = core.ErrRequestInProgress
	ErrSourceUnavailable           = core.ErrSourceUnavailable
	ErrPermissionSchemaUnavailable = core.ErrPermissionSchemaUnavailable
)

func NewChapterResourceRef(id string) (ResourceRef, error) { return core.NewChapterResourceRef(id) }
func NewAssetResourceRef(id string) (ResourceRef, error)   { return core.NewAssetResourceRef(id) }
func NewAssetResourceRefForChapter(id, chapterID string) (ResourceRef, error) {
	return core.NewAssetResourceRefForChapter(id, chapterID)
}
