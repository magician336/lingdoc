package workspace

import (
	"context"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/lingdoc/writingaction"
	"github.com/stretchr/testify/require"
)

// continueConfirmation 造一份「采纳到工作副本选段」的确认载荷。
func continueConfirmation(actor string) *candidateadoption.AdoptionConfirmation {
	return &candidateadoption.AdoptionConfirmation{
		WritingAction: writingaction.ActionContinue,
		Target:        writingaction.TargetWorkingCopyPartial,
		Scope: &candidateadoption.AdoptionScope{
			StartUTF16: len(utf16.Encode([]rune(continueBefore))),
			EndUTF16:   len(utf16.Encode([]rune(continueBefore + continueSelected))),
		},
		ConfirmedAt: time.Date(2026, 10, 22, 1, 0, 0, 0, time.UTC),
		ConfirmedBy: actor,
		Origin:      writingaction.OriginGenerated,
	}
}

// TestSelectedRewriteContinueNeedsAnExplicitAdoptionConfirmation 断言续写候选
// 的采纳必须带显式确认载荷。
//
// 缺载荷被拒；确认的动作、来源、确认人、目标范围对不上被拒；载荷齐全才写得
// 进去。
func TestSelectedRewriteContinueNeedsAnExplicitAdoptionConfirmation(t *testing.T) {
	actor := Actor{TenantID: 1, UserID: "owner"}
	service, workspace, _, base := continueFixture(t)
	input := continueRequest(base)
	candidate, err := service.Create(context.Background(), actor, "project", "chapter", "continue-key", input)
	require.NoError(t, err)
	require.Equal(t, candidateadoption.ValidityFresh, candidate.Validity)

	apply := func(t *testing.T, key string, confirmation *candidateadoption.AdoptionConfirmation) error {
		t.Helper()
		_, _, err := service.Apply(context.Background(), actor, "project", "chapter",
			candidate.CandidateID, key, ApplySelectedRewriteInput{
				ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 1, Confirmation: confirmation})
		return err
	}

	// 缺确认载荷 ⇒ 拒。
	require.ErrorIs(t, apply(t, "apply-missing", nil), ErrRewriteConfirmationRequired)
	require.Empty(t, workspace.writes, "缺确认载荷不得写工作副本")

	// 确认的动作与候选对不上 ⇒ 拒。
	wrongAction := continueConfirmation(actor.UserID)
	wrongAction.WritingAction = writingaction.ActionRewrite
	require.ErrorIs(t, apply(t, "apply-wrong-action", wrongAction), ErrRewriteInvalidRequest)

	// 确认的来源与候选对不上 ⇒ 拒。
	wrongOrigin := continueConfirmation(actor.UserID)
	wrongOrigin.Origin = writingaction.OriginHuman
	require.ErrorIs(t, apply(t, "apply-wrong-origin", wrongOrigin), ErrRewriteInvalidRequest)

	// 确认人不是这次请求的认证身份 ⇒ 拒。
	otherActor := continueConfirmation("someone-else")
	require.ErrorIs(t, apply(t, "apply-other-actor", otherActor), ErrRewriteConfirmationForbidden)

	// 确认的目标范围与候选的选区对不上 ⇒ 拒。
	wrongScope := continueConfirmation(actor.UserID)
	wrongScope.Scope = &candidateadoption.AdoptionScope{StartUTF16: 0, EndUTF16: 3}
	require.ErrorIs(t, apply(t, "apply-wrong-scope", wrongScope), ErrRewriteInvalidRequest)

	// 载荷齐全 ⇒ 采纳成立：工作副本被写了，写进去的是候选正文。
	require.NoError(t, apply(t, "apply-confirmed", continueConfirmation(actor.UserID)))
	require.Len(t, workspace.writes, 1)
	require.Equal(t, continueBefore+"续写出来的内容 [[source:existing]]"+continueAfter, workspace.writes[0].BodyMarkdown)
}

// TestSelectedRewriteContinueRetryWithANewKeyDoesNotWriteTwice 断言重试用新的
// 幂等键不重复写工作副本。
//
// 同一个键重放是同一次操作：工作副本不再被写。换一个键是另一次尝试：它被拒
// （工作副本已经动过了，候选的依据不再成立），工作副本仍然只被写过一次。
func TestSelectedRewriteContinueRetryWithANewKeyDoesNotWriteTwice(t *testing.T) {
	actor := Actor{TenantID: 1, UserID: "owner"}
	service, workspace, _, base := continueFixture(t)
	input := continueRequest(base)
	candidate, err := service.Create(context.Background(), actor, "project", "chapter", "continue-key", input)
	require.NoError(t, err)

	apply := func(t *testing.T, key string) (core.WorkingCopy, bool, error) {
		t.Helper()
		copy, replayed, err := service.Apply(context.Background(), actor, "project", "chapter", candidate.CandidateID, key,
			ApplySelectedRewriteInput{ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 1,
				Confirmation: continueConfirmation(actor.UserID)})
		return copy, replayed, err
	}

	_, _, err = apply(t, "apply-first")
	require.NoError(t, err)
	require.Len(t, workspace.writes, 1)

	// 同一个键重放：同一次操作，不再写工作副本。
	_, replayed, err := apply(t, "apply-first")
	require.NoError(t, err)
	require.True(t, replayed)
	require.Len(t, workspace.writes, 1)

	// 换一个键重试：候选的依据已经不成立，被拒；工作副本仍然只写过一次。
	_, _, err = apply(t, "apply-second")
	require.ErrorIs(t, err, ErrRewriteStale)
	require.Len(t, workspace.writes, 1, "重试不得重复写工作副本")
}

// TestSelectedRewriteContinueRevocationBeforeAdoptionIsDenied 覆盖撤权的第四个
// 时机：采纳之前。
//
// 候选还在，它依据的授权没了 ⇒ 采纳被拒，工作副本不被写。
func TestSelectedRewriteContinueRevocationBeforeAdoptionIsDenied(t *testing.T) {
	actor := Actor{TenantID: 1, UserID: "owner"}
	service, workspace, _, base := continueFixture(t)
	candidate, err := service.Create(context.Background(), actor, "project", "chapter", "revoked-before-apply", continueRequest(base))
	require.NoError(t, err)
	require.Equal(t, candidateadoption.ValidityFresh, candidate.Validity)

	service.sources.(*inheritedRewriteSources).denied = true
	_, _, err = service.Apply(context.Background(), actor, "project", "chapter", candidate.CandidateID,
		"apply-revoked", ApplySelectedRewriteInput{ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 1,
			Confirmation: continueConfirmation(actor.UserID)})
	require.ErrorIs(t, err, ErrRewriteSourceDenied)
	require.Empty(t, workspace.writes)
}
