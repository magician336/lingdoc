package candidateadoption

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/clienterror"
	"github.com/stretchr/testify/require"
)

// TestAcceptCandidateSeparatesStaleInputFromLostAuthorization 覆盖 A38 的
// 核心：两种失效要能分开说，而且要说出下一步。
//
// 「输入变了」的下一步是重跑动作，「授权没了」的下一步是先恢复授权再重跑。
// 两者都落进同一个 409，说法就只剩「再来一次」，用户不知道该先做什么。
func TestAcceptCandidateSeparatesStaleInputFromLostAuthorization(t *testing.T) {
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
		IdempotencyKey: "candidate-adoption-invalidation-0001", ExpectedSpecRevision: 1,
		Confirmation: fixtureConfirmation("user-1"),
	}

	// 授权没了：候选还在，但它依据的授权不在了。
	revoked := sourcePolicyFunc(func(context.Context, string, string, []string) error { return ErrSourceAccessDenied })
	revokedService := NewCandidateAdoptionService(store, revoked, allowAuthorizer{})
	_, err := revokedService.AcceptCandidate(context.Background(), input)
	require.ErrorIs(t, err, ErrCandidateNotAuthorized)
	require.ErrorIs(t, err, ErrSourceAccessDenied, "资料级的拒权原因仍然可达")
	form, ok := clienterror.FormOf(err)
	require.True(t, ok)
	require.Equal(t, 403, form.Status)
	require.Equal(t, "candidate_not_authorized", form.Code)
	require.Contains(t, form.Message, "恢复授权")
	require.Contains(t, form.Message, "重新运行")

	// 输入变了：候选记下的输入已经不是当前的了。
	require.NoError(t, store.DB().Model(&candidateRow{}).Where("id = ?", "candidate-1").
		Update("validity", string(ValidityStale)).Error)
	input.IdempotencyKey = "candidate-adoption-invalidation-0002"
	_, err = service.AcceptCandidate(context.Background(), input)
	require.ErrorIs(t, err, ErrCandidateStale)
	require.ErrorIs(t, err, ErrStaleInput, "旧的 stale 语义仍然可达")
	staleForm, ok := clienterror.FormOf(err)
	require.True(t, ok)
	require.Equal(t, 409, staleForm.Status)
	require.Equal(t, "stale_input", staleForm.Code)
	require.Contains(t, staleForm.Message, "重新运行")

	// 两种失效必须能分开：状态码不同，错误码不同，下一步不同。
	require.NotEqual(t, form.Code, staleForm.Code)
	require.NotEqual(t, form.Status, staleForm.Status)
	require.NotEqual(t, form.Message, staleForm.Message)

	var versionCount int64
	require.NoError(t, store.DB().Model(&chapterVersionRow{}).Count(&versionCount).Error)
	require.Zero(t, versionCount, "被拒的采纳不得建正式版本")
}

// TestCandidateNotAuthorizedIsNotTheSameAsTheSourceLevelDeny 断言候选级的
// 「授权没了」与资料级的拒权原因不混用。
//
// 资料级说的是「这条来源你现在读不了」，候选级说的是「这个候选依据的授权
// 已经不在了」。两者的下一步不同，所以对外形态也必须不同。
func TestCandidateNotAuthorizedIsNotTheSameAsTheSourceLevelDeny(t *testing.T) {
	candidateForm, ok := clienterror.FormOf(ErrCandidateNotAuthorized)
	require.True(t, ok)
	sourceForm, ok := clienterror.FormOf(ErrSourceAccessDenied)
	require.True(t, ok)
	require.NotEqual(t, candidateForm.Code, sourceForm.Code)
	require.NotEqual(t, candidateForm.Message, sourceForm.Message)
	require.Equal(t, sourceForm.Status, candidateForm.Status)
}
