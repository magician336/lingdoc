package candidateadoption

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAcceptCandidateRefusesWhenTheAuthorizationIsGone 覆盖撤权的第四个时机：
// 采纳之前。
//
// 候选依据的授权没了 ⇒ 采纳被拒，而且**零写入**：没有正式版本，没有幂等行，
// 没有依赖边。写进去再回滚不算「拒」——那会留下一个用户看不见的半截采纳。
func TestAcceptCandidateRefusesWhenTheAuthorizationIsGone(t *testing.T) {
	store := newCandidateAdoptionStore(t)
	seedCandidateAdoptionWorkspace(t, store,
		Chapter{ID: "chapter-1", ProjectID: "project-1", SectionID: "question", Title: "问题"},
		Candidate{
			ID: "candidate-1", ProjectID: "project-1", ChapterID: "chapter-1", RunID: "run-1",
			BodyMarkdown: "候选正文 [[source:source-1]]", SourceIDs: []string{"source-1"},
			Basis: Basis{SpecRevision: 1, TemplateID: "template-demo", TemplateVersion: "1"}, Validity: ValidityFresh,
			WritingAction: fixtureWritingAction, Origin: fixtureOrigin,
		}, 1)

	revoked := sourcePolicyFunc(func(context.Context, string, string, []string) error { return ErrSourceAccessDenied })
	service := NewCandidateAdoptionService(store, revoked, allowAuthorizer{})
	input := AcceptCandidateInput{
		ProjectID: "project-1", ChapterID: "chapter-1", CandidateID: "candidate-1", ActorID: "user-1",
		IdempotencyKey: "candidate-adoption-revoked-0001", ExpectedSpecRevision: 1,
		ExpectedWorkingCopyRevision: 0,
		ExpectedBodyHash:            workingCopyBodyHash(""),
		Confirmation:                fixtureConfirmation("user-1"),
	}
	_, err := service.AcceptCandidate(context.Background(), input)
	require.ErrorIs(t, err, ErrSourceAccessDenied)

	var versionCount int64
	require.NoError(t, store.DB().Model(&chapterVersionRow{}).Count(&versionCount).Error)
	require.Zero(t, versionCount, "撤权后的采纳不得建正式版本")
	var adoptionCount int64
	require.NoError(t, store.DB().Model(&adoptionIdempotencyRow{}).Count(&adoptionCount).Error)
	require.Zero(t, adoptionCount, "撤权后的采纳不得留下幂等行")
}

// TestRetryingAnAdoptionWithANewKeyDoesNotWriteASecondVersion 覆盖 A21 的
// 重试语义：新幂等键重试不得把工作副本再写一遍。
//
// 重试是「再试一次同一个意图」，不是「再来一次采纳」。写进去两次会让用户
// 以为自己只采纳了一次，而正文已经被推进了两轮。
func TestRetryingAnAdoptionWithANewKeyDoesNotWriteASecondVersion(t *testing.T) {
	store := newCandidateAdoptionStore(t)
	seedCandidateAdoptionWorkspace(t, store,
		Chapter{ID: "chapter-1", ProjectID: "project-1", SectionID: "question", Title: "问题"},
		Candidate{
			ID: "candidate-1", ProjectID: "project-1", ChapterID: "chapter-1", RunID: "run-1",
			BodyMarkdown: "候选正文 [[source:source-1]]", SourceIDs: []string{"source-1"},
			Basis: Basis{SpecRevision: 1, TemplateID: "template-demo", TemplateVersion: "1"}, Validity: ValidityFresh,
			WritingAction: fixtureWritingAction, Origin: fixtureOrigin,
		}, 1)
	allowSources := sourcePolicyFunc(func(context.Context, string, string, []string) error { return nil })
	service := NewCandidateAdoptionService(store, allowSources, allowAuthorizer{})
	input := AcceptCandidateInput{
		ProjectID: "project-1", ChapterID: "chapter-1", CandidateID: "candidate-1", ActorID: "user-1",
		IdempotencyKey: "candidate-adoption-retry-0001", ExpectedSpecRevision: 1,
		ExpectedWorkingCopyRevision: 0,
		ExpectedBodyHash:            workingCopyBodyHash(""),
		Confirmation:                fixtureConfirmation("user-1"),
	}
	_, err := service.AcceptCandidate(context.Background(), input)
	require.NoError(t, err)

	var versionCount int64
	require.NoError(t, store.DB().Model(&chapterVersionRow{}).Count(&versionCount).Error)
	require.Equal(t, int64(1), versionCount)

	// 新幂等键：这是另一次尝试，不是同一次的重放。它不得再写一遍。
	input.IdempotencyKey = "candidate-adoption-retry-0002"
	_, err = service.AcceptCandidate(context.Background(), input)
	require.Error(t, err)
	require.NoError(t, store.DB().Model(&chapterVersionRow{}).Count(&versionCount).Error)
	require.Equal(t, int64(1), versionCount, "重试不得建第二个正式版本")
}
