package candidateadoption

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/writingaction"
	"github.com/stretchr/testify/require"
)

// TestTheReviewActionHasNoAdoptionTarget 断言语言评审没有可采纳物。
//
// 评审的产出是一份报告，不是候选：它不进候选表，也进不了采纳那条路。许可位
// 是空集，因此**任何**采纳目标都会被拒——这��这条断言的判据（开发计划
// D-348-3 / §3.3）。
func TestTheReviewActionHasNoAdoptionTarget(t *testing.T) {
	targets, err := writingaction.TargetsFor(writingaction.ActionReview)
	require.NoError(t, err)
	require.Empty(t, targets, "评审没有可采纳物：许可位必须是空集")
}

// TestTheReviewActionCannotBeAdopted 断言评审的确认载荷在任何采纳目标下都被拒。
//
// 采纳端不按动作名分支：它查的是许可位。许可位是空集，于是「这个动作允许这样
// 被采纳」这一问的答案永远是「不允许」。
func TestTheReviewActionCannotBeAdopted(t *testing.T) {
	for _, target := range []writingaction.AdoptionTarget{
		writingaction.TargetWorkingCopyPartial,
		writingaction.TargetWorkingCopyWhole,
		writingaction.TargetFormalVersionWhole,
	} {
		in := AcceptCandidateInput{
			ProjectID: "project", ChapterID: "chapter", ActorID: "owner",
			IdempotencyKey: "review-adoption-key",
			Confirmation: &AdoptionConfirmation{
				WritingAction: writingaction.ActionReview,
				Target:        target,
				ConfirmedAt:   time.Now().UTC(),
				ConfirmedBy:   "owner",
				Origin:        writingaction.OriginGenerated,
			},
		}
		require.Error(t, validateAdoptionConfirmation(in),
			"评审动作不得以采纳目标 %q 被采纳", target)
	}
}
