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
		Confirmation: fixtureConfirmation("user-1"),
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
