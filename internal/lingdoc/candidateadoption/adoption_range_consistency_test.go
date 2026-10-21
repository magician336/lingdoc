package candidateadoption

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// 确认范围与实际写入范围一致（#375）。
//
// 「只改确认的那一段」是一句可以被逐字核对的话：范围之外的内容一个字都不动，
// 范围之内就是候选正文。评审事项按**新正文**重锚点，重复 ID 仍被拒。

// TestWorkingCopyPartialAdoptionWritesOnlyTheConfirmedRange 断言「工作副本·选段」
// 只写确认范围内的那一段，范围之外一字不动（A8）。
func TestWorkingCopyPartialAdoptionWritesOnlyTheConfirmedRange(t *testing.T) {
	store := newCandidateAdoptionStore(t)
	candidate := Candidate{
		ID: "candidate", ProjectID: "project", ChapterID: "chapter",
		BodyMarkdown: "新段", WritingAction: fixtureWritingAction, Origin: fixtureOrigin,
	}
	oldBody := "第一段第二段第三段"
	seedDraftWorkspace(t, store, candidate, draftWorkingCopy(oldBody, 4))

	input := AcceptCandidateInput{
		ProjectID: "project", ChapterID: "chapter", CandidateID: "candidate", ActorID: "actor",
		IdempotencyKey: "adopt-partial-draft", ExpectedSpecRevision: 2,
		ExpectedChapterVersionID:    stringPtr("old-version"),
		ExpectedWorkingCopyRevision: 4, ExpectedBodyHash: workingCopyBodyHash(oldBody),
		Confirmation: fixturePartialConfirmation("actor", 3, 6),
	}
	_, err := store.AcceptCandidate(context.Background(), input, candidate)
	require.NoError(t, err)

	var copy adoptionWorkingCopyRow
	require.NoError(t, store.DB().First(&copy).Error)
	require.Equal(t, "第一段新段第三段", copy.BodyMarkdown)
	// 范围之外逐字未变。
	require.Equal(t, "第一段", copy.BodyMarkdown[:len("第一段")])
	require.Equal(t, "第三段", copy.BodyMarkdown[len(copy.BodyMarkdown)-len("第三段"):])
	// 正式版本一行都不多，章节版本指针不动。
	var versions int64
	require.NoError(t, store.DB().Model(&chapterVersionRow{}).Count(&versions).Error)
	require.Equal(t, int64(1), versions)
	var chapter chapterRow
	require.NoError(t, store.DB().First(&chapter).Error)
	require.Equal(t, "old-version", *chapter.CurrentVersionID)
}

// TestPartialAdoptionRebasesReviewItemsAndRefusesDuplicateIDs 断言部分写入时
// 评审事项按**新正文**重锚点，重复 ID 仍被拒。
func TestPartialAdoptionRebasesReviewItemsAndRefusesDuplicateIDs(t *testing.T) {
	t.Run("rebases onto the new body", func(t *testing.T) {
		store := newCandidateAdoptionStore(t)
		candidate := Candidate{
			ID: "candidate", ProjectID: "project", ChapterID: "chapter",
			BodyMarkdown: "新段", WritingAction: fixtureWritingAction, Origin: fixtureOrigin,
			ReviewItems: []ReviewItem{{ID: "review-candidate", Statement: "待核", OriginCandidateID: "candidate"}},
		}
		oldBody := "第一段第二段第三段"
		workingCopy := draftWorkingCopy(oldBody, 4)
		workingCopy.ReviewItemsJSON = `[{"id":"review-old","statement":"旧的待核","origin_candidate_id":"candidate-old","anchor":{"start_utf16":3,"end_utf16":6,"quote_markdown":"第二段","working_copy_revision":4,"status":"located"}}]`
		seedDraftWorkspace(t, store, candidate, workingCopy)

		input := AcceptCandidateInput{
			ProjectID: "project", ChapterID: "chapter", CandidateID: "candidate", ActorID: "actor",
			IdempotencyKey: "adopt-partial-anchors", ExpectedSpecRevision: 2,
			ExpectedChapterVersionID:    stringPtr("old-version"),
			ExpectedWorkingCopyRevision: 4, ExpectedBodyHash: workingCopyBodyHash(oldBody),
			Confirmation: fixturePartialConfirmation("actor", 3, 6),
		}
		_, err := store.AcceptCandidate(context.Background(), input, candidate)
		require.NoError(t, err)

		var copy adoptionWorkingCopyRow
		require.NoError(t, store.DB().First(&copy).Error)
		var items []ReviewItem
		require.NoError(t, json.Unmarshal([]byte(copy.ReviewItemsJSON), &items))
		require.Len(t, items, 2)
		// 旧事项的引文被换掉了：它不再是「located」，但也不该凭空消失。
		require.Equal(t, "review-old", items[0].ID)
		require.NotNil(t, items[0].Anchor)
		require.Equal(t, "stale", items[0].Anchor.Status)
		require.Equal(t, int64(5), items[0].Anchor.WorkingCopyRevision)
		// 候选的事项并进来了。
		require.Equal(t, "review-candidate", items[1].ID)
	})

	t.Run("refuses a duplicate id", func(t *testing.T) {
		store := newCandidateAdoptionStore(t)
		candidate := Candidate{
			ID: "candidate", ProjectID: "project", ChapterID: "chapter",
			BodyMarkdown: "新段", WritingAction: fixtureWritingAction, Origin: fixtureOrigin,
			ReviewItems: []ReviewItem{{ID: "review-old", Statement: "待核", OriginCandidateID: "candidate"}},
		}
		oldBody := "第一段第二段第三段"
		workingCopy := draftWorkingCopy(oldBody, 4)
		workingCopy.ReviewItemsJSON = `[{"id":"review-old","statement":"旧的待核","origin_candidate_id":"candidate-old"}]`
		seedDraftWorkspace(t, store, candidate, workingCopy)

		input := AcceptCandidateInput{
			ProjectID: "project", ChapterID: "chapter", CandidateID: "candidate", ActorID: "actor",
			IdempotencyKey: "adopt-partial-duplicate", ExpectedSpecRevision: 2,
			ExpectedChapterVersionID:    stringPtr("old-version"),
			ExpectedWorkingCopyRevision: 4, ExpectedBodyHash: workingCopyBodyHash(oldBody),
			Confirmation: fixturePartialConfirmation("actor", 3, 6),
		}
		_, err := store.AcceptCandidate(context.Background(), input, candidate)
		require.ErrorIs(t, err, ErrInvalidRequest)

		var copy adoptionWorkingCopyRow
		require.NoError(t, store.DB().First(&copy).Error)
		require.Equal(t, oldBody, copy.BodyMarkdown, "被拒的采纳不得改动工作副本正文")
		require.Equal(t, int64(4), copy.WorkingCopyRevision)
		var versions int64
		require.NoError(t, store.DB().Model(&chapterVersionRow{}).Count(&versions).Error)
		require.Equal(t, int64(1), versions)
	})
}

// TestPartialAdoptionRefusesAnOutOfRangeScope 断言越界范围被拒，且零写入。
func TestPartialAdoptionRefusesAnOutOfRangeScope(t *testing.T) {
	store := newCandidateAdoptionStore(t)
	candidate := Candidate{
		ID: "candidate", ProjectID: "project", ChapterID: "chapter",
		BodyMarkdown: "新段", WritingAction: fixtureWritingAction, Origin: fixtureOrigin,
	}
	oldBody := "第一段第二段第三段"
	seedDraftWorkspace(t, store, candidate, draftWorkingCopy(oldBody, 4))

	input := AcceptCandidateInput{
		ProjectID: "project", ChapterID: "chapter", CandidateID: "candidate", ActorID: "actor",
		IdempotencyKey: "adopt-partial-out-of-range", ExpectedSpecRevision: 2,
		ExpectedChapterVersionID:    stringPtr("old-version"),
		ExpectedWorkingCopyRevision: 4, ExpectedBodyHash: workingCopyBodyHash(oldBody),
		Confirmation: fixturePartialConfirmation("actor", 3, 99),
	}
	_, err := store.AcceptCandidate(context.Background(), input, candidate)
	require.ErrorIs(t, err, ErrVersionConflict)

	var copy adoptionWorkingCopyRow
	require.NoError(t, store.DB().First(&copy).Error)
	require.Equal(t, oldBody, copy.BodyMarkdown)
	require.Equal(t, int64(4), copy.WorkingCopyRevision)
}
