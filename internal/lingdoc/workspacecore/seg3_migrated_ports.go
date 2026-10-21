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
	_ ProjectApplication          = (*Service)(nil)
	_ ChapterApplication          = (*Service)(nil)
	_ TemplateApplication         = (*Service)(nil)
	_ ChangeSetApplication        = (*Service)(nil)
	_ ProjectLifecycleApplication = (*Service)(nil)
)
