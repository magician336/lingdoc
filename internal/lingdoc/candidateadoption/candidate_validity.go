package candidateadoption

import (
	"context"
	"errors"
)

// candidateValidityUnderCurrentAuthorization 按**当前授权**算出候选还能不能用。
//
// 两种失效要能分开说：「输入变了」与「授权没了」。前者重跑动作就行，后者
// 先恢复授权再重跑——所以判据也分开：一个看候选自己记下的可用性，一个看
// 候选依据的来源现在还在不在授权内。
//
// 授权端口没配时不算「授权没了」：那是依赖没就位，由后面的步骤按依赖不可用
// 报错。把依赖缺失误报成「授权没了」会让用户去恢复一个从来没给过的授权。
func (s *CandidateAdoptionService) candidateValidityUnderCurrentAuthorization(ctx context.Context, in AcceptCandidateInput, candidate Candidate) (Validity, error) {
	if candidate.Validity != ValidityFresh {
		return candidate.Validity, nil
	}
	if len(candidate.SourceIDs) == 0 || (s.Reviewer == nil && s.Sources == nil) {
		return ValidityFresh, nil
	}
	err := s.validateSources(ctx, in.ProjectID, in.ActorID, candidate.SourceIDs)
	switch {
	case err == nil:
		return ValidityFresh, nil
	case errors.Is(err, ErrSourceAccessDenied):
		return ValidityNotAuthorized, nil
	case errors.Is(err, ErrStaleInput):
		return ValidityStale, nil
	default:
		return ValidityFresh, err
	}
}
