package candidateadoption

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/lingdoc/writingaction"
)

// 采纳目标各自走自己的写入路径（#375）。
//
// 这一组用例盯的是「目标改变了写入路径」这件事：三条路径各自的结果、各自留下
// 的痕迹，以及各自拒绝时的**零写入**。断言都落在公开接缝上（采纳服务与
// 采纳端点），不碰内部结构。

// seedDraftWorkspace 造一个「章节有正式版本、工作副本里有草稿」的现场。
//
// 正式版本的正文与工作副本正文一致，所以「章节已有版本」与「草稿未提交」这两
// 件事在这里是分开的：工作副本修订号 4 表示这份草稿已经被保存过。
func seedDraftWorkspace(t *testing.T, store *SQLiteCandidateAdoptionStore, candidate Candidate, workingCopy adoptionWorkingCopyRow) {
	t.Helper()
	require.NoError(t, store.DB().AutoMigrate(&adoptionWorkingCopyRow{}))
	old := "old-version"
	seedCandidateAdoptionWorkspace(t, store,
		Chapter{ID: "chapter", ProjectID: "project", CurrentVersionID: &old}, candidate, 2)
	require.NoError(t, store.DB().Create(&chapterVersionRow{
		ID: old, ProjectID: "project", ChapterID: "chapter", BodyMarkdown: workingCopy.BodyMarkdown,
		SourceIDsJSON: workingCopy.SourceIDsJSON, CitationUsagesJSON: workingCopy.CitationUsagesJSON,
		ReviewItemsJSON: workingCopy.ReviewItemsJSON,
	}).Error)
	workingCopy.ProjectID = "project"
	workingCopy.ChapterID = "chapter"
	workingCopy.BaseChapterVersionID = &old
	require.NoError(t, store.DB().Create(&workingCopy).Error)
}

// draftWorkingCopy 造一条干净的工作副本行。
func draftWorkingCopy(body string, revision int64) adoptionWorkingCopyRow {
	return adoptionWorkingCopyRow{
		SpecRevision: 2, WorkingCopyRevision: revision, BodyMarkdown: body,
		SourceIDsJSON: "[]", CitationUsagesJSON: "[]", ReviewItemsJSON: "[]",
	}
}

// TestWorkingCopyWholeAdoptionWritesTheDraftAndLeavesTheFormalVersionAlone 断言
// 「工作副本·整段」只写草稿，不建正式版本、不动章节版本指针（A10）。
func TestWorkingCopyWholeAdoptionWritesTheDraftAndLeavesTheFormalVersionAlone(t *testing.T) {
	store := newCandidateAdoptionStore(t)
	candidate := Candidate{
		ID: "candidate", ProjectID: "project", ChapterID: "chapter",
		BodyMarkdown: "new body", WritingAction: fixtureWritingAction, Origin: fixtureOrigin,
	}
	seedDraftWorkspace(t, store, candidate, draftWorkingCopy("old body", 4))

	input := AcceptCandidateInput{
		ProjectID: "project", ChapterID: "chapter", CandidateID: "candidate", ActorID: "actor",
		IdempotencyKey: "adopt-whole-draft", ExpectedSpecRevision: 2,
		ExpectedChapterVersionID:    stringPtr("old-version"),
		ExpectedWorkingCopyRevision: 4, ExpectedBodyHash: workingCopyBodyHash("old body"),
		Confirmation: fixtureConfirmationFor(writingaction.TargetWorkingCopyWhole, "actor"),
	}
	result, err := store.AcceptCandidate(context.Background(), input, candidate)
	require.NoError(t, err)

	// 章节版本指针不动。
	require.Equal(t, "old-version", *result.Chapter.CurrentVersionID)
	var chapter chapterRow
	require.NoError(t, store.DB().First(&chapter).Error)
	require.Equal(t, "old-version", *chapter.CurrentVersionID)
	// 正式版本一行都不多。
	var versions int64
	require.NoError(t, store.DB().Model(&chapterVersionRow{}).Count(&versions).Error)
	require.Equal(t, int64(1), versions)
	// 项目版本不动：写草稿不是一次正式变更。
	var project projectRow
	require.NoError(t, store.DB().First(&project).Error)
	require.Equal(t, int64(0), project.ProjectVersion)
	// 草稿被整篇替换，修订号推进一格。
	var copy adoptionWorkingCopyRow
	require.NoError(t, store.DB().First(&copy).Error)
	require.Equal(t, "new body", copy.BodyMarkdown)
	require.Equal(t, int64(5), copy.WorkingCopyRevision)
	require.Equal(t, "old-version", *copy.BaseChapterVersionID)
}

// TestWholeAdoptionRefusesADirtyWorkingCopyWithoutWritingAnything 断言整段替换
// 遇到 dirty 工作副本 ⇒ working_copy_dirty，且零写入。
func TestWholeAdoptionRefusesADirtyWorkingCopyWithoutWritingAnything(t *testing.T) {
	store := newCandidateAdoptionStore(t)
	candidate := Candidate{
		ID: "candidate", ProjectID: "project", ChapterID: "chapter",
		BodyMarkdown: "new body", WritingAction: fixtureWritingAction, Origin: fixtureOrigin,
	}
	seedDraftWorkspace(t, store, candidate, draftWorkingCopy("old body", 4))

	input := AcceptCandidateInput{
		ProjectID: "project", ChapterID: "chapter", CandidateID: "candidate", ActorID: "actor",
		IdempotencyKey: "adopt-dirty-draft", ExpectedSpecRevision: 2,
		ExpectedChapterVersionID:    stringPtr("old-version"),
		ExpectedWorkingCopyRevision: 4, ExpectedBodyHash: workingCopyBodyHash("用户在编辑器里还没保存的内容"),
		Confirmation: fixtureConfirmationFor(writingaction.TargetWorkingCopyWhole, "actor"),
	}
	_, err := store.AcceptCandidate(context.Background(), input, candidate)
	require.ErrorIs(t, err, ErrWorkingCopyDirty)

	// 零写入：草稿没动，版本没多，幂等行没留。
	var copy adoptionWorkingCopyRow
	require.NoError(t, store.DB().First(&copy).Error)
	require.Equal(t, "old body", copy.BodyMarkdown)
	require.Equal(t, int64(4), copy.WorkingCopyRevision)
	var versions int64
	require.NoError(t, store.DB().Model(&chapterVersionRow{}).Count(&versions).Error)
	require.Equal(t, int64(1), versions)
	var idem int64
	require.NoError(t, store.DB().Model(&adoptionIdempotencyRow{}).Count(&idem).Error)
	require.Zero(t, idem)
	var chapter chapterRow
	require.NoError(t, store.DB().First(&chapter).Error)
	require.Equal(t, "old-version", *chapter.CurrentVersionID)
}

// TestWholeAdoptionRefusesAStaleWorkingCopyRevision 断言修订号对不上 ⇒
// version_conflict（别人改过，请重新读取），且零写入。
func TestWholeAdoptionRefusesAStaleWorkingCopyRevision(t *testing.T) {
	store := newCandidateAdoptionStore(t)
	candidate := Candidate{
		ID: "candidate", ProjectID: "project", ChapterID: "chapter",
		BodyMarkdown: "new body", WritingAction: fixtureWritingAction, Origin: fixtureOrigin,
	}
	seedDraftWorkspace(t, store, candidate, draftWorkingCopy("old body", 4))

	input := AcceptCandidateInput{
		ProjectID: "project", ChapterID: "chapter", CandidateID: "candidate", ActorID: "actor",
		IdempotencyKey: "adopt-stale-draft", ExpectedSpecRevision: 2,
		ExpectedChapterVersionID:    stringPtr("old-version"),
		ExpectedWorkingCopyRevision: 3, ExpectedBodyHash: workingCopyBodyHash("old body"),
		Confirmation: fixtureConfirmationFor(writingaction.TargetWorkingCopyWhole, "actor"),
	}
	_, err := store.AcceptCandidate(context.Background(), input, candidate)
	require.ErrorIs(t, err, ErrVersionConflict)

	var copy adoptionWorkingCopyRow
	require.NoError(t, store.DB().First(&copy).Error)
	require.Equal(t, "old body", copy.BodyMarkdown)
	require.Equal(t, int64(4), copy.WorkingCopyRevision)
}

// TestWholeAdoptionWithASnapshotContinuesAndLeavesARecoverableTrace 断言带快照
// 的替换可以在 dirty 的情况下继续，并且替换前的工作副本正文被记进审计详情。
func TestWholeAdoptionWithASnapshotContinuesAndLeavesARecoverableTrace(t *testing.T) {
	store := newCandidateAdoptionStore(t)
	require.NoError(t, store.DB().AutoMigrate(&projectAuditRow{}))
	candidate := Candidate{
		ID: "candidate", ProjectID: "project", ChapterID: "chapter",
		BodyMarkdown: "new body", WritingAction: fixtureWritingAction, Origin: fixtureOrigin,
	}
	seedDraftWorkspace(t, store, candidate, draftWorkingCopy("old body", 4))

	input := AcceptCandidateInput{
		ProjectID: "project", ChapterID: "chapter", CandidateID: "candidate", ActorID: "actor",
		IdempotencyKey: "adopt-snapshot-draft", ExpectedSpecRevision: 2,
		ExpectedChapterVersionID:    stringPtr("old-version"),
		ExpectedWorkingCopyRevision: 4, ExpectedBodyHash: workingCopyBodyHash("用户在编辑器里还没保存的内容"),
		SnapshotID:   "snapshot-1",
		Confirmation: fixtureConfirmationFor(writingaction.TargetWorkingCopyWhole, "actor"),
	}
	_, err := store.AcceptCandidate(context.Background(), input, candidate)
	require.NoError(t, err)

	var copy adoptionWorkingCopyRow
	require.NoError(t, store.DB().First(&copy).Error)
	require.Equal(t, "new body", copy.BodyMarkdown)
	require.Equal(t, int64(5), copy.WorkingCopyRevision)

	var audit projectAuditRow
	require.NoError(t, store.DB().Order("created_at DESC").First(&audit).Error)
	require.Equal(t, "project.candidate.adopt", audit.Action)
	require.Contains(t, audit.DetailsJSON, "snapshot-1")
	require.Contains(t, audit.DetailsJSON, "old body", "替换前的正文必须留在审计详情里，人工据此可恢复")
	require.Contains(t, audit.DetailsJSON, "working_copy_whole")
}

// TestTheFormalVersionPathStillBuildsAVersion 断言正式版本路径保持今天的语义：
// 建正式版本并 CAS 推进章节版本指针。
func TestTheFormalVersionPathStillBuildsAVersion(t *testing.T) {
	store := newCandidateAdoptionStore(t)
	candidate := Candidate{
		ID: "candidate", ProjectID: "project", ChapterID: "chapter",
		BodyMarkdown: "new body", WritingAction: fixtureWritingAction, Origin: fixtureOrigin,
	}
	seedDraftWorkspace(t, store, candidate, draftWorkingCopy("old body", 4))

	input := AcceptCandidateInput{
		ProjectID: "project", ChapterID: "chapter", CandidateID: "candidate", ActorID: "actor",
		IdempotencyKey: "adopt-formal-version", ExpectedSpecRevision: 2,
		ExpectedChapterVersionID:    stringPtr("old-version"),
		ExpectedWorkingCopyRevision: 4, ExpectedBodyHash: workingCopyBodyHash("old body"),
		ReplaceExisting: true,
		Confirmation:    fixtureConfirmationFor(writingaction.TargetFormalVersionWhole, "actor"),
	}
	result, err := store.AcceptCandidate(context.Background(), input, candidate)
	require.NoError(t, err)
	require.NotNil(t, result.Chapter.CurrentVersionID)
	require.NotEqual(t, "old-version", *result.Chapter.CurrentVersionID)

	var versions int64
	require.NoError(t, store.DB().Model(&chapterVersionRow{}).Count(&versions).Error)
	require.Equal(t, int64(2), versions)
	var chapter chapterRow
	require.NoError(t, store.DB().First(&chapter).Error)
	require.Equal(t, *result.Chapter.CurrentVersionID, *chapter.CurrentVersionID)
	var project projectRow
	require.NoError(t, store.DB().First(&project).Error)
	require.Equal(t, int64(1), project.ProjectVersion)
}

// TestTheFormalVersionPathRefusesAnActionWithoutThePermissionBit 断言写路径
// 这一层自己查许可位（纵深防御的第二层）。
//
// 许可位在请求层已经查过一次（确认载荷校验）。这一层是给「即使请求层被绕过」
// 兜底的：写路径本身不给那些动作建正式版本的机会。
//
// 端到端看不出来这一层——请求层先拒了。所以这一层在**它自己的接缝**上断言。
func TestTheFormalVersionPathRefusesAnActionWithoutThePermissionBit(t *testing.T) {
	require.ErrorIs(t, guardFormalVersionPath(AcceptCandidateInput{
		Confirmation: &AdoptionConfirmation{WritingAction: writingaction.ActionOutline},
	}), ErrInvalidRequest)
	require.NoError(t, guardFormalVersionPath(AcceptCandidateInput{
		Confirmation: &AdoptionConfirmation{WritingAction: writingaction.ActionGenerate},
	}))
}

// TestTheWorkingCopyPathsNeverBuildFormalVersions 断言两条只写工作副本的路径
// 在**代码上**就没有建正式版本的入口。
//
// 这不是「建了再回滚」，是压根走不到那段代码：路径由目标分派，正式版本的
// 建表语句只在正式版本那条分支里。
func TestTheWorkingCopyPathsNeverBuildFormalVersions(t *testing.T) {
	require.False(t, pathWorkingCopyPartial.buildsFormalVersion())
	require.False(t, pathWorkingCopyWhole.buildsFormalVersion())
	require.True(t, pathFormalVersionWhole.buildsFormalVersion())
}

// TestTheFormalVersionPathIsAlsoProtectedAgainstADirtyWorkingCopy 断言正式版本
// 路径同样受 dirty 保护。
//
// 「整段替换」有两个目标：写草稿与建正式版本。两个都要挡——建正式版本那一路
// 更狠：它盖掉的是用户已经写下的正式正文，用户编辑器里没保存的内容也跟着
// 一起没了。
func TestTheFormalVersionPathIsAlsoProtectedAgainstADirtyWorkingCopy(t *testing.T) {
	store := newCandidateAdoptionStore(t)
	candidate := Candidate{
		ID: "candidate", ProjectID: "project", ChapterID: "chapter",
		BodyMarkdown: "new body", WritingAction: fixtureWritingAction, Origin: fixtureOrigin,
	}
	seedDraftWorkspace(t, store, candidate, draftWorkingCopy("old body", 4))

	input := AcceptCandidateInput{
		ProjectID: "project", ChapterID: "chapter", CandidateID: "candidate", ActorID: "actor",
		IdempotencyKey: "adopt-formal-dirty", ExpectedSpecRevision: 2,
		ExpectedChapterVersionID:    stringPtr("old-version"),
		ExpectedWorkingCopyRevision: 4, ExpectedBodyHash: workingCopyBodyHash("用户在编辑器里还没保存的内容"),
		ReplaceExisting: true,
		Confirmation:    fixtureConfirmationFor(writingaction.TargetFormalVersionWhole, "actor"),
	}
	_, err := store.AcceptCandidate(context.Background(), input, candidate)
	require.ErrorIs(t, err, ErrWorkingCopyDirty)

	// 零写入：正式版本没多，章节版本指针没动，幂等行没留。
	var versions int64
	require.NoError(t, store.DB().Model(&chapterVersionRow{}).Count(&versions).Error)
	require.Equal(t, int64(1), versions)
	var chapter chapterRow
	require.NoError(t, store.DB().First(&chapter).Error)
	require.Equal(t, "old-version", *chapter.CurrentVersionID)
	var idem int64
	require.NoError(t, store.DB().Model(&adoptionIdempotencyRow{}).Count(&idem).Error)
	require.Zero(t, idem)
	var copy adoptionWorkingCopyRow
	require.NoError(t, store.DB().First(&copy).Error)
	require.Equal(t, "old body", copy.BodyMarkdown)
	require.Equal(t, int64(4), copy.WorkingCopyRevision)
}

// TestTheFormalVersionPathContinuesWithASnapshot 断言带快照的替换在正式版本
// 路径上同样可以继续。
func TestTheFormalVersionPathContinuesWithASnapshot(t *testing.T) {
	store := newCandidateAdoptionStore(t)
	candidate := Candidate{
		ID: "candidate", ProjectID: "project", ChapterID: "chapter",
		BodyMarkdown: "new body", WritingAction: fixtureWritingAction, Origin: fixtureOrigin,
	}
	seedDraftWorkspace(t, store, candidate, draftWorkingCopy("old body", 4))

	input := AcceptCandidateInput{
		ProjectID: "project", ChapterID: "chapter", CandidateID: "candidate", ActorID: "actor",
		IdempotencyKey: "adopt-formal-snapshot", ExpectedSpecRevision: 2,
		ExpectedChapterVersionID:    stringPtr("old-version"),
		ExpectedWorkingCopyRevision: 4, ExpectedBodyHash: workingCopyBodyHash("用户在编辑器里还没保存的内容"),
		ReplaceExisting: true,
		SnapshotID:      "snapshot-formal-1",
		Confirmation:    fixtureConfirmationFor(writingaction.TargetFormalVersionWhole, "actor"),
	}
	result, err := store.AcceptCandidate(context.Background(), input, candidate)
	require.NoError(t, err)
	require.NotNil(t, result.Chapter.CurrentVersionID)
	require.NotEqual(t, "old-version", *result.Chapter.CurrentVersionID)

	var versions int64
	require.NoError(t, store.DB().Model(&chapterVersionRow{}).Count(&versions).Error)
	require.Equal(t, int64(2), versions)
}

// stringPtr 取一个字符串的指针（夹具用）。
func stringPtr(value string) *string { return &value }
