package workspacecore

import (
	"context"
)

// 这四个口原本声明在传输层（internal/lingdoc/workspace/contracts.go）——
// 端口当前声明在消费方。按 Spec #47 决策 33，端口应当由它所描述的那门语言的
// 拥有者声明，所以它们随实现迁到这里。
//
// 迁移只搬声明：方法集合与方法签名逐字不变。传输层那边留了类型别名，
// 所以那边一行都不用改就能继续编译。

// ProjectApplication 是 项目 上下文的窄口。
type ProjectApplication interface {
	ListProjects(context.Context, Actor) ([]Project, bool, error)
	GetProject(context.Context, Actor, string) (Project, error)
	CreateProjectResult(context.Context, Actor, string, CreateProjectInput) (CreateProjectResult, error)
	SaveSpecResult(context.Context, Actor, string, string, SaveSpecInput) (SaveSpecResult, error)
	ActivateProjectResult(context.Context, Actor, string, string, ActivateProjectInput) (ProjectMutationResult, error)
	ListDraftCandidates(context.Context, Actor, string) ([]DraftCandidate, error)
	CreateDraftCandidateResult(context.Context, Actor, string, string, DraftCandidateInput) (DraftCandidateMutationResult, error)
	ListAuditEvents(context.Context, Actor, string) ([]AuditEvent, error)
	ActivationDiff(context.Context, Actor, string, int64) (ActivationDiff, error)
	DiscardProjectResult(context.Context, Actor, string, string, int64) (ProjectMutationResult, error)
	RestoreProjectResult(context.Context, Actor, string, string, int64) (ProjectMutationResult, error)
	ArchiveProjectResult(context.Context, Actor, string, string, int64) (ProjectMutationResult, error)
}

// ChapterApplication 是 章节与工作副本 上下文的窄口。
type ChapterApplication interface {
	ListChapters(context.Context, Actor, string) ([]Chapter, error)
	GetWorkingCopy(context.Context, Actor, string, string) (WorkingCopy, error)
	ListChapterVersions(context.Context, Actor, string, string) ([]ChapterVersion, error)
	SaveChapterResult(context.Context, Actor, string, string, string, SaveChapterInput) (ChapterMutationResult, error)
	SaveWorkingCopyResult(context.Context, Actor, string, string, string, SaveWorkingCopyInput) (WorkingCopyMutationResult, error)
	ApplyRewriteResult(context.Context, Actor, string, string, string, SaveWorkingCopyInput, []ReviewItem) (WorkingCopyMutationResult, error)
	CommitWorkingCopyResult(context.Context, Actor, string, string, string, CommitWorkingCopyInput) (CommittedChapterMutationResult, error)
	RestoreWorkingCopyResult(context.Context, Actor, string, string, string, RestoreWorkingCopyInput) (WorkingCopyMutationResult, error)
}

// TemplateApplication 是 模板 上下文的窄口。
type TemplateApplication interface {
	PreviewTemplateMigration(context.Context, Actor, string, TemplateMigrationInput) (TemplateMigrationPreview, error)
	GetTemplateCopy(context.Context, Actor, string, int64) (ProjectTemplateCopy, error)
	PreviewTemplateCopyEdit(context.Context, Actor, string, TemplateCopyDefinitionInput) (TemplateMigrationPreview, error)
	ChangeTemplateResult(context.Context, Actor, string, string, TemplateMigrationInput) (ProjectMutationResult, error)
	SaveTemplateCopyResult(context.Context, Actor, string, string, TemplateCopyDefinitionInput) (ProjectMutationResult, error)
	BindTemplateCopyRuleSetResult(context.Context, Actor, string, string, BindTemplateCopyRuleSetInput) (ProjectMutationResult, error)
	ApplyLegacyRuleOverridesResult(context.Context, Actor, string, string, LegacyRuleOverridesInput) (ProjectMutationResult, error)
	CreateFieldArchiveResult(context.Context, Actor, string, string, CreateFieldArchiveInput) (FieldArchiveResult, error)
	RemoveArchivedFieldResult(context.Context, Actor, string, string, string, RemoveArchivedFieldInput) (FieldArchiveResult, error)
	ListFieldArchives(context.Context, Actor, string) ([]FieldArchiveView, error)
	GetFieldArchive(context.Context, Actor, string, string) (FieldArchiveView, error)
}

// ChangeSetApplication 是 变更集 上下文的窄口。
type ChangeSetApplication interface {
	CreateChangeSetResult(context.Context, Actor, string, string, CreateChangeSetInput) (ChangeSetMutationResult, error)
	ListChangeSets(context.Context, Actor, string) ([]ChangeSet, error)
	GetChangeSet(context.Context, Actor, string, string) (ChangeSet, error)
	ReassessChangeSetResult(context.Context, Actor, string, string, string) (ChangeSetMutationResult, error)
	ApplyChangeSetResult(context.Context, Actor, string, string, string) (ChangeSetMutationResult, error)
	RejectChangeSetResult(context.Context, Actor, string, string, string) (ChangeSetMutationResult, error)
}

// CapabilityAuthorizer 授权一个能力。
//
// 它是「授权」这一门语言里最窄的那一形态：给一个主体对一个项目授权一个能力。
// 聚合体上的同名方法签名逐字照抄。
//
// 注意它与 core.ProjectAuthorizer 不是同一个口：后者是事务型的两方法口
// （AuthorizeTenant / AuthorizeProject），给的是事务边界上的判定；这个口给的是
// 传输层与运行时直接问的那一句「这个人此刻能不能做这件事」。
type CapabilityAuthorizer interface {
	Authorize(ctx context.Context, actor Actor, projectID, capability string) error
}

// ResourceBatchAuthorizer owns the resource decision language and its stable
// read/audit transaction. Source consumers receive it explicitly at assembly.
type ResourceBatchAuthorizer interface {
	AuthorizeResourceBatch(context.Context, Actor, string, AuthorizationAction, []ResourceRef) ([]ResourceAuthorizationDecision, error)
}

// AssetUseAuthorizer is the complete port for source and research consumers.
type AssetUseAuthorizer interface {
	CapabilityAuthorizer
	ResourceBatchAuthorizer
}

// GenerationContextApplication 读回一个章节的生成上下文。
//
// 它只带一个操作：生成这一门语言里，生成输入的组装只需要「章节此刻的生成上下文」。
type GenerationContextApplication interface {
	GenerationContext(ctx context.Context, actor Actor, projectID, chapterID string) (GenerationContext, error)
}

// ValidationIssueDispositionApplication 是校验问题处置这一门语言的窄口：
// 处置的写入、处置结果的重放，以及处置的读回投影。
//
// 它原本声明在传输层（internal/lingdoc/workspace/delivery_contracts.go）。按
// Spec #47 决策 33，端口由它所描述的那门语言的拥有者声明，所以随实现迁到这里；
// 迁移只搬声明，方法集合与方法签名逐字不变，传输层那边留了类型别名。
type ValidationIssueDispositionApplication interface {
	ReplayValidationIssueDispositionResult(context.Context, Actor, string, string, string, string, string, int64) (ValidationIssueDispositionReplayResult, error)
	RecordValidationIssueDispositionResult(context.Context, Actor, string, string, ValidationIssueDispositionInput) (ValidationIssueDispositionResult, error)
	ValidationIssueDispositions(context.Context, Actor, string, []ValidationIssueBinding) (map[string]ValidationIssueDisposition, error)
}

// ProjectLifecycleApplication 是项目上下文里的两个操作：弃用项目列表与激活读回执。
//
// 它们**不进** ProjectApplication：ProjectApplication 是主路由用的项目生命周期口，
// 它的方法集合与语义不能变（Spec #47 决策 33）。更重要的是，遗留宽口
// ApplicationService（37 方法）与它的读写拆分对都提供不了这两个操作——把它们塞进
// ProjectApplication 就等于逼着遗留宽口变宽，与本段「让遗留面越来越窄」的方向相反。
//
// 所以这两个操作自己成口：它窄到只有两个方法，只有全流程路由消费它。
type ProjectLifecycleApplication interface {
	ListDiscardedProjects(ctx context.Context, a Actor) ([]Project, error)
	RecordActivationRead(ctx context.Context, actor Actor, pid string, version int64) (ActivationReadReceipt, error)
}

var (
	_ ProjectApplication                    = (*Service)(nil)
	_ ChapterApplication                    = (*Service)(nil)
	_ TemplateApplication                   = (*Service)(nil)
	_ ChangeSetApplication                  = (*Service)(nil)
	_ ProjectLifecycleApplication           = (*Service)(nil)
	_ CapabilityAuthorizer                  = (*Service)(nil)
	_ GenerationContextApplication          = (*Service)(nil)
	_ ValidationIssueDispositionApplication = (*Service)(nil)
)
