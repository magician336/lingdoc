package candidateadoption

import (
	"slices"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/reviewanchor"
	"github.com/Tencent/WeKnora/internal/lingdoc/writingaction"
)

// AdoptionConfirmation 是采纳的显式确认载荷。
//
// 采纳不是「点一下就写进去」：用户必须确认这次采纳的是哪个动作的结果、
// 写到哪里、写到哪一段、确认时刻、确认人是谁、候选是什么来源。
// 六项缺任何一项，服务层拒。
//
// 载荷是**指针**：缺省即「没确认」，与「确认了但字段为空」要能分开——
// 前者是一次没确认的采纳，后者是一次填错的确认，两者的恢复动作不同。
type AdoptionConfirmation struct {
	// WritingAction 是被确认的那个动作。它必须与候选上记录的动作一致。
	WritingAction writingaction.Action `json:"writing_action"`
	// Target 是采纳目标：写到哪里。它必须落在该动作声明的许可位里。
	Target writingaction.AdoptionTarget `json:"adoption_target"`
	// Scope 是部分采纳写入工作副本的范围（UTF-16 偏移，与选区一致）。
	//
	// 部分采纳必填；整段采纳必须不带——带了就是「整段被悄悄缩小或扩大」。
	Scope *AdoptionScope `json:"scope,omitempty"`
	// ConfirmedAt 是确认时刻。
	ConfirmedAt time.Time `json:"confirmed_at"`
	// ConfirmedBy 是确认人。它必须等于这次请求的认证身份：代他人确认一律拒。
	ConfirmedBy string `json:"confirmed_by"`
	// Origin 是候选的来源标注。它必须与候选上记录的来源一致。
	Origin writingaction.Origin `json:"origin"`
}

// AdoptionScope 是部分采纳写入工作副本的范围（UTF-16 偏移，与选区一致）。
type AdoptionScope struct {
	StartUTF16 int `json:"start_utf16"`
	EndUTF16   int `json:"end_utf16"`
}

// validateAdoptionConfirmation 校验确认载荷本身。
//
// 这一步不依赖候选：它只看「这次请求有没有把三件事说清楚」——动作、采纳
// 目标、目标范围，以及确认时刻、确认人、来源。缺任何一项都拒。
//
// 许可位在这里查：动作自己声明它允许哪些采纳目标，采纳端只查这张表，不按
// 动作名写分支。
func validateAdoptionConfirmation(in AcceptCandidateInput) error {
	confirmation := in.Confirmation
	if confirmation == nil {
		return ErrInvalidRequest
	}
	if confirmation.WritingAction == "" || confirmation.Target == "" ||
		confirmation.ConfirmedAt.IsZero() || strings.TrimSpace(confirmation.ConfirmedBy) == "" ||
		confirmation.Origin == "" {
		return ErrInvalidRequest
	}
	// 代他人确认：确认人必须是这次请求的认证身份。
	if strings.TrimSpace(confirmation.ConfirmedBy) != strings.TrimSpace(in.ActorID) {
		return ErrForbidden
	}
	allowed, err := writingaction.TargetsFor(confirmation.WritingAction)
	if err != nil {
		return ErrInvalidRequest
	}
	if !slices.Contains(allowed, confirmation.Target) {
		return ErrInvalidRequest
	}
	return validateAdoptionScope(confirmation)
}

// validateAdoptionScope 校验目标范围与采纳目标相称。
//
// 部分采纳必须给出范围，且范围非空且不倒置；整段采纳必须不带范围——
// 带了范围就意味着「整段」这句话不可信。
func validateAdoptionScope(confirmation *AdoptionConfirmation) error {
	if confirmation.Target == writingaction.TargetWorkingCopyPartial {
		if confirmation.Scope == nil || confirmation.Scope.StartUTF16 < 0 ||
			confirmation.Scope.EndUTF16 <= confirmation.Scope.StartUTF16 {
			return ErrInvalidRequest
		}
		return nil
	}
	if confirmation.Scope != nil {
		return ErrInvalidRequest
	}
	return nil
}

// validateConfirmationAgainstCandidate 校验确认载荷与候选对得上。
//
// 确认的动作与候选实际产生的动作对不上，就是「确认了但没确认对东西」；
// 确认的来源与候选实际的来源对不上，就是「未确认的依据伪装成正式依据」。
//
// 候选没有记录动作或来源时，这一步**响亮地失败**：一个说不清自己是怎么来的
// 候选，没法被确认。它必须重新跑一次动作化路径，而不是被悄悄采纳。
func validateConfirmationAgainstCandidate(in AcceptCandidateInput, candidate Candidate) error {
	confirmation := in.Confirmation
	if confirmation == nil {
		return ErrInvalidRequest
	}
	if candidate.WritingAction == "" || candidate.Origin == "" {
		return ErrCandidateActionUnknown
	}
	if confirmation.WritingAction != candidate.WritingAction {
		return ErrInvalidRequest
	}
	if confirmation.Origin != candidate.Origin {
		return ErrInvalidRequest
	}
	return nil
}

// validateAdoptionScopeAgainstBody 复核确认的范围与工作副本当前正文对得上。
//
// 范围是用户**看着工作副本正文**选出来的。确认之后正文又变了，范围就落在
// 别的段落上了——那不是用户确认的那一段，越界即拒。
func validateAdoptionScopeAgainstBody(in AcceptCandidateInput, body string) error {
	confirmation := in.Confirmation
	if confirmation == nil || confirmation.Target != writingaction.TargetWorkingCopyPartial {
		return nil
	}
	if confirmation.Scope == nil {
		return ErrInvalidRequest
	}
	if _, _, ok := reviewanchor.ByteRange(body, confirmation.Scope.StartUTF16, confirmation.Scope.EndUTF16); !ok {
		return ErrVersionConflict
	}
	return nil
}

// confirmationDigestOf 把确认载荷折成进入请求摘要的投影。
//
// 投影里**只放**影响采纳结果的内容：动作、采纳目标、目标范围、确认人、
// 来源。确认时刻不放——它每次都不同，放进去会让同一次采纳永远无法重放，
// 幂等键就失效了。
func confirmationDigestOf(confirmation *AdoptionConfirmation) *adoptionConfirmationDigest {
	if confirmation == nil {
		return nil
	}
	projection := &adoptionConfirmationDigest{
		WritingAction: string(confirmation.WritingAction),
		Target:        string(confirmation.Target),
		ConfirmedBy:   confirmation.ConfirmedBy,
		Origin:        string(confirmation.Origin),
	}
	if confirmation.Scope != nil {
		start, end := confirmation.Scope.StartUTF16, confirmation.Scope.EndUTF16
		projection.StartUTF16 = &start
		projection.EndUTF16 = &end
	}
	return projection
}
