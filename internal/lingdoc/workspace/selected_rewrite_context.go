package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/Tencent/WeKnora/internal/clienterror"
	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/generation"
	"github.com/Tencent/WeKnora/internal/lingdoc/writingaction"
)

// 可选上下文被丢掉时的原因。
//
// 丢弃必须说得清是哪一类原因：授权没了与「压根没有可纳入的东西」不是
// 一回事，用户要做的下一步也不同。
const (
	// rewriteContextDroppedNotAuthorized 表示这条上下文当前没有授权。
	rewriteContextDroppedNotAuthorized = "not_authorized"
	// rewriteContextDroppedNotAvailable 表示这一类里没有可纳入的条目。
	rewriteContextDroppedNotAvailable = "not_available"
)

// RewriteContextSelection 是用户对这次执行要纳入的可选上下文的选择。
//
// 它按**类别**说话：键是上下文类别，值是这一类别的条目。类别必须落在
// 动作声明的 Plan.Context.Optional 里——客户端要求纳入一个这个动作根本
// 不读的东西，整个请求被拒；用户要剔除一个这个动作必须用的类别，同样
// 被拒。
//
// 具体条目是否真的进入模型请求，由服务端按当前授权解析：没授权的条目
// 不进模型请求，并在响应里被点名。丢弃是可见的，不是静默的。
//
// 来源归在「资料」这一类：来源是资料在授权范围内的抽取。
type RewriteContextSelection struct {
	// Include 是用户要纳入的条目，按上下文类别分列。
	Include map[writingaction.ContextItem][]RewriteContextItem `json:"include,omitempty"`
	// Exclude 是用户要剔除的条目，按上下文类别分列。
	Exclude map[writingaction.ContextItem][]RewriteContextItem `json:"exclude,omitempty"`
}

// RewriteContextItem 是一个上下文条目。
type RewriteContextItem struct {
	// ID 是条目 ID。资料、来源与证据卡都是它自己的 ID。
	ID string `json:"id"`
	// Version 是条目版本。证据卡必须带版本——没有版本的证据卡无从复核。
	Version int64 `json:"version,omitempty"`
}

// DroppedContextItem 是一个没有进入模型请求的可选上下文条目。
type DroppedContextItem struct {
	// Category 是这一类上下文。
	Category writingaction.ContextItem `json:"category"`
	// ID 是被丢弃的条目 ID。
	ID string `json:"id"`
	// Reason 说明它为什么没进去。
	Reason string `json:"reason"`
}

// resolveRewriteContext 解析用户对可选上下文的选择。
//
// 三条规则同时成立：类别必须落在动作声明的可选上下文里，越过策略的请求
// 被拒；没授权的条目不进模型请求，而且要被点名；用户剔除的条目不进模型
// 请求。
//
// 它只返回「要纳入什么」与「什么被丢掉」，不决定最终进模型请求的集合——
// 那要等来源解析完才知道。
func (s *SelectedRewriteService) resolveRewriteContext(ctx context.Context, actor Actor, projectID, chapterID string,
	plan writingaction.Plan, selection RewriteContextSelection) ([]string, []generation.EvidenceCardRef, []DroppedContextItem, error) {
	if err := checkRewriteContextPolicy(plan, selection); err != nil {
		return nil, nil, nil, err
	}
	var includedSources []string
	var includedCards []generation.EvidenceCardRef
	var dropped []DroppedContextItem
	for _, item := range selection.Include[writingaction.ContextAssets] {
		source, err := s.sources.GetSource(ctx, actor, projectID, item.ID)
		if err != nil || source.ProjectID != projectID || source.Status != evidence.SourceAvailable {
			dropped = append(dropped, DroppedContextItem{Category: writingaction.ContextAssets, ID: item.ID,
				Reason: rewriteContextDroppedNotAuthorized})
			continue
		}
		includedSources = append(includedSources, item.ID)
	}
	for _, item := range selection.Include[writingaction.ContextCards] {
		ref := generation.EvidenceCardRef{ID: item.ID, Version: item.Version}
		if err := s.checkRewriteCard(ctx, actor, projectID, chapterID, ref); err != nil {
			if errors.Is(err, generation.ErrDependencyUnavailable) {
				return nil, nil, nil, err
			}
			dropped = append(dropped, DroppedContextItem{Category: writingaction.ContextCards, ID: item.ID,
				Reason: rewriteContextDroppedNotAuthorized})
			continue
		}
		includedCards = append(includedCards, ref)
	}
	return includedSources, includedCards, dropped, nil
}

// checkRewriteContextPolicy 校验这次选择落在动作声明的上下文策略内。
//
// 纳入的类别必须在这个动作的可选上下文里；剔除的类别还必须不是这个动作
// 必需的上下文——「这一类必须用」是执行方的事实，用户不能把它拿掉。
func checkRewriteContextPolicy(plan writingaction.Plan, selection RewriteContextSelection) error {
	for category := range selection.Include {
		if !slices.Contains(plan.Context.Optional, category) {
			return rewriteContextNotAllowed(plan.Action, category)
		}
	}
	for category := range selection.Exclude {
		// 「这一类必须用」是执行方的事实，用户不能把它拿掉。
		if slices.Contains(plan.Context.Required, category) {
			return rewriteContextRequired(plan.Action, category)
		}
		if !slices.Contains(plan.Context.Optional, category) {
			return rewriteContextNotAllowed(plan.Action, category)
		}
	}
	return nil
}

// checkRewriteCard 按当前授权复核一张证据卡。
func (s *SelectedRewriteService) checkRewriteCard(ctx context.Context, actor Actor, projectID, chapterID string, ref generation.EvidenceCardRef) error {
	_, err := g2SelectFormal(ctx, s.workspace, actor, projectID, chapterID, g2GenerationRefs([]generation.EvidenceCardRef{ref}))
	return err
}

// authorizedSourcesOfAsset 取出一个资料下已授权的来源。
func (s *SelectedRewriteService) authorizedSourcesOfAsset(ctx context.Context, actor Actor, projectID, assetID string) []string {
	sources, err := s.sources.RetrieveSources(ctx, actor, projectID, RetrieveSourcesInput{AssetIDs: []string{assetID}})
	if err != nil {
		return nil
	}
	ids := make([]string, 0, len(sources))
	for _, source := range sources {
		if source.ProjectID == projectID && source.Status == evidence.SourceAvailable {
			ids = append(ids, source.ID)
		}
	}
	return ids
}

// removeRewriteExcludedSources 把用户剔除的来源从有效来源里去掉。
//
// 剔除先于取来源：用户剔除一条来源，往往正是因为它已经不可用了——留着
// 再取一次会让整个请求按「授权没了」失败，而用户要的正是不要它。
//
// 剔除 ID 既可以是来源 ID，也可以是资料 ID：资料 ID 把它下面的来源一并
// 剔掉。
func (s *SelectedRewriteService) removeRewriteExcludedSources(ctx context.Context, actor Actor, projectID string,
	sourceIDs []string, selection *RewriteContextSelection) []string {
	if selection == nil {
		return sourceIDs
	}
	excluded := contextItemIDs(selection.Exclude[writingaction.ContextAssets])
	if len(excluded) == 0 {
		return sourceIDs
	}
	kept := make([]string, 0, len(sourceIDs))
	for _, id := range sourceIDs {
		if slices.Contains(excluded, id) {
			continue
		}
		// 剔除 ID 也可以是资料 ID：它把该资料下面的来源一并剔掉。
		source, err := s.sources.GetSource(ctx, actor, projectID, id)
		if err == nil && slices.Contains(excluded, source.AssetID) {
			continue
		}
		kept = append(kept, id)
	}
	return kept
}

// removeRewriteExcludedCards 把用户剔除的证据卡从有效证据卡里去掉。
func removeRewriteExcludedCards(refs []generation.EvidenceCardRef, selection *RewriteContextSelection) []generation.EvidenceCardRef {
	if selection == nil {
		return refs
	}
	return removeRewriteCards(refs, contextItemIDs(selection.Exclude[writingaction.ContextCards]))
}

// contextItemIDs 取出一组上下文条目的 ID。
func contextItemIDs(items []RewriteContextItem) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if item.ID != "" {
			ids = append(ids, item.ID)
		}
	}
	return ids
}

// removeRewriteCards 把用户剔除的证据卡从有效证据卡里去掉。
func removeRewriteCards(refs []generation.EvidenceCardRef, excluded []string) []generation.EvidenceCardRef {
	if len(excluded) == 0 {
		return refs
	}
	kept := make([]generation.EvidenceCardRef, 0, len(refs))
	for _, ref := range refs {
		if !slices.Contains(excluded, ref.ID) {
			kept = append(kept, ref)
		}
	}
	return kept
}

// rewriteContextNotAllowed 造出「这个动作不读这一类上下文」的请求错误。
//
// 它是请求错误而不是内部错误：客户端要求纳入的东西这个动作根本不读，
// 服务端不会替它猜一个意思。
func rewriteContextNotAllowed(action writingaction.Action, item writingaction.ContextItem) error {
	return clienterror.Wrap(clienterror.InvalidRequest,
		fmt.Sprintf("invalid_request: action %q does not read the %q context", action, item), ErrRewriteContextNotAllowed)
}

// rewriteContextRequired 造出「这一类上下文这个动作必须用」的请求错误。
func rewriteContextRequired(action writingaction.Action, item writingaction.ContextItem) error {
	return clienterror.Wrap(clienterror.InvalidRequest,
		fmt.Sprintf("invalid_request: action %q requires the %q context", action, item), ErrRewriteContextRequired)
}

// encodeDroppedContext 把被丢掉的条目编码进候选行。
//
// 它只在这一刻有意义：丢弃发生在发起的那一刻，之后读回来的是候选本身。
func encodeDroppedContext(items []DroppedContextItem) string {
	if len(items) == 0 {
		return ""
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		return ""
	}
	return string(encoded)
}
