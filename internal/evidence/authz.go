package evidence

import "context"

// AssetScopeRef 是判权需要、而契约 `Asset` 上没有的两个事实：
// 这份资料在底座里属于哪个知识库、那个知识库属于哪个租户。
//
// 契约的 Asset 是 additionalProperties: false，加不了字段；所以判权回
// 绑定存储取这两个事实，而不是把它们塞进对外的资料形状里。
type AssetScopeRef struct {
	KnowledgeBaseID string
	OwnerTenantID   uint64
}

// ScopeReader 按（项目, 资料 ID）取出上述两个事实。资料不在本项目/不存在时返回
// ErrAssetNotFound——判权不区分「不存在」与「别的项目」，不泄露存在性。
type ScopeReader interface {
	AssetScope(ctx context.Context, projectID, assetID string) (AssetScopeRef, error)
}

// KBReadChecker 回答一个知识库问题：此刻这个操作者能不能读这个知识库。
//
// 它是资料服务与仓库既有权限体系之间的缝。实现在应用层（`internal/application/access`
// 的 KBPermissions），由接线处注入：本包不反向依赖应用层，也就不会把
// `types.Caller` 这类运行上下文语义带进资料域。
type KBReadChecker interface {
	CanReadKB(ctx context.Context, actor Actor, knowledgeBaseID string, ownerTenantID uint64) (bool, error)
}

// NewFixedAuthorizer 组装资料级授权判定。
//
// **使用前提（接线时必须满足）**：调用方已确认操作者可见该项目——那是工作区
// 服务（T07 的 ProjectAccess）的事，`02-接口与Mock约定.md` §2 也要求资料服务
// 不再调用包含来源检查的总入口。本类型只回答「这份资料的当前授权」。
//
// 项目可见性放在这里是**错的**，不只是越界：本端口只能回 true/false，而
// 「不可见」在 HTTP 上必须与「不存在」收敛成同一个答案（§8 不泄露存在性）。
// 若由这里把不可见答成 not_authorized，就变成了「403 说明这个项目存在」。
func NewFixedAuthorizer(scopes ScopeReader, kb KBReadChecker) Authorizer {
	return &fixedAuthorizer{scopes: scopes, kb: kb}
}

type fixedAuthorizer struct {
	scopes ScopeReader
	kb     KBReadChecker
}

// CanAccessAsset 问一次底座当前能不能读。不缓存、不记结果（B5）：
// 撤权之后旧结果不得复用，所以每次判定都必须落到这里。
//
// projectID 会一路带到取 scope 的那一步：本类型只回答 true/false，
// 「别的项目的资料」在这里必须与「不存在」收敛成同一个错误（ErrAssetNotFound），
// 不能靠调用方先过滤——那样这个端口就成了跨项目探测的口子。
func (a *fixedAuthorizer) CanAccessAsset(
	ctx context.Context, actor Actor, projectID string, asset Asset,
) (bool, error) {
	scope, err := a.scopes.AssetScope(ctx, projectID, asset.ID)
	if err != nil {
		// 含 ErrAssetNotFound：资料在两次调用之间被解开绑定了。如实报错，
		// 不翻译成「没有权限」——那是另一个原因。
		return false, err
	}
	return a.kb.CanReadKB(ctx, actor, scope.KnowledgeBaseID, scope.OwnerTenantID)
}
