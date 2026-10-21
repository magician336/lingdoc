package candidateadoption

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/lingdoc/writingaction"
)

func TestAdoptionAdvancesCleanWorkingCopyAndReplaysWithoutRevisionBump(t *testing.T) {
	store := newCandidateAdoptionStore(t)
	require.NoError(t, store.DB().AutoMigrate(&adoptionWorkingCopyRow{}))
	candidate := Candidate{ID: "candidate", ProjectID: "project", ChapterID: "chapter", BodyMarkdown: "new [[source:source]]", SourceIDs: []string{"source"}, ReviewItems: []ReviewItem{{ID: "review", Statement: "check"}}, WritingAction: fixtureWritingAction, Origin: fixtureOrigin}
	seedCandidateAdoptionWorkspace(t, store, Chapter{ID: "chapter", ProjectID: "project"}, candidate, 2)
	require.NoError(t, store.DB().Create(&adoptionWorkingCopyRow{ProjectID: "project", ChapterID: "chapter", SpecRevision: 2, WorkingCopyRevision: 4, SourceIDsJSON: "[]", CitationUsagesJSON: "[]", ReviewItemsJSON: "[]"}).Error)
	input := AcceptCandidateInput{ProjectID: "project", ChapterID: "chapter", CandidateID: "candidate", ActorID: "actor", IdempotencyKey: "adoption-working-copy", ExpectedSpecRevision: 2, Confirmation: fixtureConfirmation("actor")}
	result, err := store.AcceptCandidate(context.Background(), input, candidate)
	require.NoError(t, err)
	var copy adoptionWorkingCopyRow
	require.NoError(t, store.DB().First(&copy).Error)
	require.Equal(t, result.Chapter.CurrentVersionID, copy.BaseChapterVersionID)
	require.Equal(t, int64(5), copy.WorkingCopyRevision)
	require.Equal(t, int64(2), copy.SpecRevision)
	require.Equal(t, candidate.BodyMarkdown, copy.BodyMarkdown)
	require.True(t, equalAdoptionJSON(copy.SourceIDsJSON, candidate.SourceIDs))
	require.True(t, equalAdoptionJSON(copy.ReviewItemsJSON, candidate.ReviewItems))
	require.JSONEq(t, `[{"source_id":"source"}]`, copy.CitationUsagesJSON)
	replay, err := store.AcceptCandidate(context.Background(), input, candidate)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	require.NoError(t, store.DB().First(&copy).Error)
	require.Equal(t, int64(5), copy.WorkingCopyRevision)
}

func TestAdoptionRejectsDirtyOrStaleWorkingCopyAtomically(t *testing.T) {
	for _, field := range []string{"body_markdown", "source_ids_json", "review_items_json", "citation_usages_json", "base_chapter_version_id"} {
		t.Run(field, func(t *testing.T) {
			store := newCandidateAdoptionStore(t)
			require.NoError(t, store.DB().AutoMigrate(&adoptionWorkingCopyRow{}))
			old := "old"
			candidate := Candidate{ID: "candidate", ProjectID: "project", ChapterID: "chapter", BodyMarkdown: "new", WritingAction: fixtureWritingAction, Origin: fixtureOrigin}
			seedCandidateAdoptionWorkspace(t, store, Chapter{ID: "chapter", ProjectID: "project", CurrentVersionID: &old}, candidate, 2)
			require.NoError(t, store.DB().Create(&chapterVersionRow{ID: old, ProjectID: "project", ChapterID: "chapter", BodyMarkdown: "old body", SourceIDsJSON: "[]", CitationUsagesJSON: "[]", ReviewItemsJSON: "[]"}).Error)
			require.NoError(t, store.DB().Create(&adoptionWorkingCopyRow{ProjectID: "project", ChapterID: "chapter", BaseChapterVersionID: &old, SpecRevision: 2, WorkingCopyRevision: 4, BodyMarkdown: "old body", SourceIDsJSON: "[]", CitationUsagesJSON: "[]", ReviewItemsJSON: "[]"}).Error)
			value := `[{"id":"unsaved"}]`
			if field == "body_markdown" || field == "base_chapter_version_id" {
				value = "unsaved"
			}
			require.NoError(t, store.DB().Model(&adoptionWorkingCopyRow{}).Where("chapter_id = ?", "chapter").Update(field, value).Error)
			_, err := store.AcceptCandidate(context.Background(), AcceptCandidateInput{ProjectID: "project", ChapterID: "chapter", CandidateID: "candidate", ActorID: "actor", IdempotencyKey: "dirty", ExpectedSpecRevision: 2, ExpectedChapterVersionID: &old, ReplaceExisting: true, Confirmation: fixtureConfirmation("actor")}, candidate)
			require.ErrorIs(t, err, ErrVersionConflict)
			var count int64
			require.NoError(t, store.DB().Model(&chapterVersionRow{}).Count(&count).Error)
			require.Equal(t, int64(1), count)
			require.NoError(t, store.DB().Model(&adoptionIdempotencyRow{}).Count(&count).Error)
			require.Zero(t, count)
			var chapter chapterRow
			require.NoError(t, store.DB().First(&chapter).Error)
			require.Equal(t, &old, chapter.CurrentVersionID)
			var copy adoptionWorkingCopyRow
			require.NoError(t, store.DB().First(&copy).Error)
			require.Equal(t, int64(4), copy.WorkingCopyRevision)
		})
	}
}

// TestARejectedAdoptionLeavesTheWorkingCopyUntouched 断言拒绝与取消之后工作
// 副本读回不变（A24）。
//
// 拒绝的理由有好几种（缺确认载荷、越权目标、代他人、脏工作副本），但结果
// 必须一样：工作副本一个字都不动，用户在编辑器里看到的还是他刚才的内容。
func TestARejectedAdoptionLeavesTheWorkingCopyUntouched(t *testing.T) {
	for _, reason := range []string{"missing-confirmation", "forbidden-target", "other-confirmer"} {
		t.Run(reason, func(t *testing.T) {
			store := newCandidateAdoptionStore(t)
			require.NoError(t, store.DB().AutoMigrate(&adoptionWorkingCopyRow{}))
			candidate := Candidate{
				ID: "candidate", ProjectID: "project", ChapterID: "chapter",
				BodyMarkdown: "new [[source:source]]", SourceIDs: []string{"source"},
				Validity: "fresh", WritingAction: fixtureWritingAction, Origin: fixtureOrigin,
			}
			seedCandidateAdoptionWorkspace(t, store, Chapter{ID: "chapter", ProjectID: "project"}, candidate, 2)
			require.NoError(t, store.DB().Create(&adoptionWorkingCopyRow{
				ProjectID: "project", ChapterID: "chapter", SpecRevision: 2, WorkingCopyRevision: 4,
				BodyMarkdown: "old body", SourceIDsJSON: "[]", CitationUsagesJSON: "[]", ReviewItemsJSON: "[]",
			}).Error)
			input := AcceptCandidateInput{
				ProjectID: "project", ChapterID: "chapter", CandidateID: "candidate", ActorID: "actor",
				IdempotencyKey: "rejected-adoption", ExpectedSpecRevision: 2,
				Confirmation: fixtureConfirmation("actor"),
			}
			switch reason {
			case "missing-confirmation":
				input.Confirmation = nil
			case "forbidden-target":
				input.Confirmation.Target = writingaction.TargetFormalVersionWhole
				input.Confirmation.WritingAction = writingaction.ActionOutline
			case "other-confirmer":
				input.Confirmation.ConfirmedBy = "somebody-else"
			}
			service := NewCandidateAdoptionService(store,
				sourcePolicyFunc(func(context.Context, string, string, []string) error { return nil }), allowAuthorizer{})
			_, err := service.AcceptCandidate(context.Background(), input)
			require.Error(t, err)

			var copy adoptionWorkingCopyRow
			require.NoError(t, store.DB().First(&copy).Error)
			require.Equal(t, "old body", copy.BodyMarkdown, "被拒绝的采纳不得改动工作副本正文")
			require.Equal(t, int64(4), copy.WorkingCopyRevision, "被拒绝的采纳不得推进工作副本修订号")
			var versions int64
			require.NoError(t, store.DB().Model(&chapterVersionRow{}).Count(&versions).Error)
			require.Zero(t, versions, "被拒绝的采纳不得留下章节版本")
		})
	}
}

func TestProductionAdoptionFailsWhenWorkingCopySchemaOrRowMissing(t *testing.T) {
	for _, tableExists := range []bool{false, true} {
		store := newCandidateAdoptionStore(t)
		if tableExists {
			require.NoError(t, store.DB().AutoMigrate(&adoptionWorkingCopyRow{}))
		}
		candidate := Candidate{ID: "candidate", ProjectID: "project", ChapterID: "chapter", BodyMarkdown: "new", WritingAction: fixtureWritingAction, Origin: fixtureOrigin}
		seedCandidateAdoptionWorkspace(t, store, Chapter{ID: "chapter", ProjectID: "project"}, candidate, 1)
		productionStore := NewSQLiteCandidateAdoptionStore(store.DB())
		_, err := productionStore.AcceptCandidate(context.Background(), AcceptCandidateInput{ProjectID: "project", ChapterID: "chapter", ExpectedSpecRevision: 1, Confirmation: fixtureConfirmation("actor")}, candidate)
		require.Error(t, err)
		var count int64
		require.NoError(t, store.DB().Model(&chapterVersionRow{}).Count(&count).Error)
		require.Zero(t, count)
	}
}

func TestAdoptionInitializesUnknownUsagesAndPreservesPriorVersion(t *testing.T) {
	for _, withSources := range []bool{true, false} {
		t.Run(map[bool]string{true: "cited", false: "uncited"}[withSources], func(t *testing.T) {
			store := newCandidateAdoptionStore(t)
			require.NoError(t, store.DB().AutoMigrate(&adoptionWorkingCopyRow{}))
			oldID := "old"
			oldBody := "old [[source:retained]] [[source:removed]]"
			oldSources := `["removed","retained"]`
			oldUsages := `[{"source_id":"removed","purpose":"old purpose"},{"source_id":"retained","purpose":"old interpretation","limitation":"old limitation"}]`
			candidate := Candidate{ID: "candidate", ProjectID: "project", ChapterID: "chapter", BodyMarkdown: "new", SourceIDs: []string{}, ReviewItems: []ReviewItem{}, WritingAction: fixtureWritingAction, Origin: fixtureOrigin}
			expected := []CitationUsage{}
			if withSources {
				candidate.BodyMarkdown = "new [[source:retained]] [[source:new]] [[source:retained]]"
				candidate.SourceIDs = []string{"retained", "new", "retained"}
				expected = []CitationUsage{{SourceID: "new"}, {SourceID: "retained"}}
			}
			seedCandidateAdoptionWorkspace(t, store, Chapter{ID: "chapter", ProjectID: "project", CurrentVersionID: &oldID}, candidate, 2)
			require.NoError(t, store.DB().Create(&chapterVersionRow{ID: oldID, ProjectID: "project", ChapterID: "chapter", BodyMarkdown: oldBody, SourceIDsJSON: oldSources, CitationUsagesJSON: oldUsages, ReviewItemsJSON: "[]"}).Error)
			require.NoError(t, store.DB().Create(&adoptionWorkingCopyRow{ProjectID: "project", ChapterID: "chapter", BaseChapterVersionID: &oldID, SpecRevision: 2, WorkingCopyRevision: 4, BodyMarkdown: oldBody, SourceIDsJSON: oldSources, CitationUsagesJSON: oldUsages, ReviewItemsJSON: "[]"}).Error)
			input := AcceptCandidateInput{ProjectID: "project", ChapterID: "chapter", CandidateID: "candidate", ActorID: "actor", IdempotencyKey: "citation-usages", ExpectedSpecRevision: 2, ExpectedChapterVersionID: &oldID, ReplaceExisting: true, Confirmation: fixtureConfirmation("actor")}
			result, err := store.AcceptCandidate(context.Background(), input, candidate)
			require.NoError(t, err)
			require.Equal(t, expected, result.Chapter.CitationUsages)
			require.Equal(t, candidate.BodyMarkdown, result.Chapter.BodyMarkdown)
			persisted, err := store.GenerationContext(context.Background(), "project", "chapter")
			require.NoError(t, err)
			require.Equal(t, expected, persisted.Chapter.CitationUsages)
			var copy adoptionWorkingCopyRow
			require.NoError(t, store.DB().First(&copy).Error)
			require.True(t, equalAdoptionJSON(copy.CitationUsagesJSON, expected))
			require.Equal(t, int64(5), copy.WorkingCopyRevision)
			var prior chapterVersionRow
			require.NoError(t, store.DB().Where("id = ?", oldID).First(&prior).Error)
			require.JSONEq(t, oldUsages, prior.CitationUsagesJSON)
			replay, err := store.AcceptCandidate(context.Background(), input, candidate)
			require.NoError(t, err)
			require.True(t, replay.Replayed)
			require.Equal(t, result.Chapter, replay.Chapter)
			require.NoError(t, store.DB().First(&copy).Error)
			require.Equal(t, int64(5), copy.WorkingCopyRevision)
		})
	}
}
