package workspacecore

import (
	"context"

	"github.com/Tencent/WeKnora/internal/lingdoc/sourcepolicy"
)

// SourcePolicy 是「这批已经产出的引用此刻还能不能当证据」在核心域的共享端口别名。
//
// 端口的共享声明位于 sourcepolicy 包，核心域通过别名消费它，理由有三条：
//
//   - 调用者是核心域自己的写路径。SaveChapter 要在落笔之前核一遍引用，而复核必须跑在
//     幂等重放之前（见 operation 的注释）——那是核心域自己的那笔事务，只有核心域能在
//     那个位置插进去。
//   - 又不能反向 import 具体实现所在的包：那等于让核心域依赖它的一个消费者。
//     装配层同时认识两边，适配器放在那里。
//   - 返回 error 而不是 bool，因为结论不止一种（未授权 / 坐标失效 / 协作方坏了）。
//     核心域只负责把结论原样转交，怎么归类由传输层统一决定；在这里分档就等于把
//     线上语义抄进领域。
//
// 实现方可以返回 nil 之外的任何 error；核心域不解释它，只把它交给调用者。
type SourcePolicy = sourcepolicy.SourcePolicy

// legacySourcePolicyAdapter is the explicit compatibility boundary for the
// pre-review all-or-nothing source port. New composition roots expose the
// shared sourcepolicy.Reviewer; this adapter exists only for older callers and
// keeps the legacy Validate representation out of workspace operations.
type legacySourcePolicyAdapter struct{ policy SourcePolicy }

func (a legacySourcePolicyAdapter) Revalidate(ctx context.Context, projectID, actorID string, sourceIDs []string) error {
	if a.policy == nil {
		return ErrSourceUnavailable
	}
	return a.policy.Validate(ctx, projectID, actorID, sourceIDs)
}

func legacySourceRevalidate(ctx context.Context, policy SourcePolicy, projectID, actorID string, sourceIDs []string) error {
	return legacySourcePolicyAdapter{policy: policy}.Revalidate(ctx, projectID, actorID, sourceIDs)
}

// CitationStatusPolicy is an optional read-side extension for workspaces that
// can report one current verdict per cited source. Implementations must still
// use the same SourcePolicy判据; this interface only changes presentation from
// all-or-nothing failure to item-level status.
type CitationStatusPolicy interface {
	CitationStatuses(ctx context.Context, projectID, actorID string, sourceIDs []string) ([]CitationStatus, error)
}

type CitationStatus struct {
	SourceID string `json:"source_id"`
	Status   string `json:"status"`
	Detail   string `json:"detail,omitempty"`
}

// 引用状态展示词表的唯一拥有者在此（#597）。
//
// 这是一个**独立于** evidence.SourceStatus 的词表：面向用户的引用三态
// （available / needs_review / unavailable），由 Review 的逐项 verdict 投影而来
// （Usable→available，stale→needs_review，其余→unavailable），且 needs_review
// 不在 evidence 词表里。它的取值不得散落字面量：产出侧（source_policy 投影）与
// 消费侧（service_facade 的遮蔽判定）都引用这里的常量。也正因为它有自己的
// owner，来源状态词表的守护（source_status_vocabulary_guard_test.go）不会把它
// 误判成 evidence.SourceAvailable 的抄写。
const (
	// CitationStatusAvailable 表示该引用此刻可以指回可用来源。
	CitationStatusAvailable = "available"
	// CitationStatusNeedsReview 表示来源坐标或版本已变化，需要人工核对。
	CitationStatusNeedsReview = "needs_review"
	// CitationStatusUnavailable 表示该引用此刻取不回可用来源。
	CitationStatusUnavailable = "unavailable"
)
