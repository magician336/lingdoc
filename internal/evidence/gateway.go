package evidence

import (
	"context"
	"strings"
)

// DenyReason 说明某个被请求的资料为什么没有进入允许集合。
//
// 没有 separate 的"跨项目"原因：BindingSource 只返回本项目的绑定资料，
// 别的项目的 ID 与不存在的 ID 在这里收敛成同一个 not_found，不泄露存在性。
type DenyReason string

const (
	DenyNotAuthorized DenyReason = "not_authorized"
	DenyNotReady      DenyReason = "not_ready"
	DenyNotFound      DenyReason = "not_found"
)

// DeniedAsset 是一条拒绝记录，必须带原因。
type DeniedAsset struct {
	AssetID string
	Reason  DenyReason
}

// ResolveResult 一次说清"请求了什么、允许了什么、拒绝了什么"。
//
// 三个字段缺一不可：只返回 Allowed，就是契约 §8 禁止的"把返回无错误
// 当成全部授权成功"。
type ResolveResult struct {
	Requested []string
	Allowed   []Asset
	Denied    []DeniedAsset
}

// BindingSource 提供"该项目当前绑定了哪些资料"。
type BindingSource interface {
	BoundAssets(ctx context.Context, projectID string) ([]Asset, error)
}

// Authorizer 判定调用者此刻能否访问某份资料。授权是否仍有效必须现查，
// 不能用历史缓存——否则撤权后仍会从旧结果里放行。
type Authorizer interface {
	CanAccessAsset(ctx context.Context, actor Actor, projectID string, asset Asset) (bool, error)
}

// AssetGateway 是契约 §2 里的 AssetGateway.ResolveAllowed。
type AssetGateway interface {
	ResolveAllowed(ctx context.Context, projectID string, actor Actor, assetIDs []string) (*ResolveResult, error)
}

// NewAssetGateway 组装固定实现。真实依赖（绑定表、权限检查）由调用方注入。
func NewAssetGateway(bindings BindingSource, authz Authorizer) AssetGateway {
	return &assetGateway{bindings: bindings, authz: authz}
}

type assetGateway struct {
	bindings BindingSource
	authz    Authorizer
}

func (g *assetGateway) ResolveAllowed(
	ctx context.Context, projectID string, actor Actor, assetIDs []string,
) (*ResolveResult, error) {
	requested := normalizeIDs(assetIDs)
	if len(requested) == 0 {
		// 空范围是"没有资料"，不是"全部资料"：不触达存储，返回空集合。
		return &ResolveResult{Requested: []string{}, Allowed: []Asset{}, Denied: []DeniedAsset{}}, nil
	}

	bound, err := g.bindings.BoundAssets(ctx, projectID)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]Asset, len(bound))
	for _, asset := range bound {
		byID[asset.ID] = asset
	}

	res := &ResolveResult{
		Requested: requested,
		Allowed:   make([]Asset, 0, len(requested)),
		Denied:    make([]DeniedAsset, 0, len(requested)),
	}
	for _, id := range requested {
		asset, ok := byID[id]
		if !ok {
			res.Denied = append(res.Denied, DeniedAsset{AssetID: id, Reason: DenyNotFound})
			continue
		}
		allowed, err := g.authz.CanAccessAsset(ctx, actor, projectID, asset)
		if err != nil {
			return nil, err
		}
		if !allowed {
			res.Denied = append(res.Denied, DeniedAsset{AssetID: id, Reason: DenyNotAuthorized})
			continue
		}
		if asset.ProcessingState != AssetStateReady {
			// 授权必须先于状态判定，避免向无权调用者泄露资料状态。
			res.Denied = append(res.Denied, DeniedAsset{AssetID: id, Reason: DenyNotReady})
			continue
		}
		res.Allowed = append(res.Allowed, asset)
	}
	return res, nil
}

// normalizeKey 是「两个 ID 算不算同一个」的唯一判据。网关与来源复核都经它：
// 两处各判一次时，同一个 ID 会在一个入口里算数、在另一个里被丢掉，
// 而两边的报错都说得通——最难查的一类错。
func normalizeKey(id string) string { return strings.TrimSpace(id) }

// normalizeIDs 去重并保持请求顺序，与契约 asset_ids 的 uniqueItems 约束一致。
func normalizeIDs(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, item := range in {
		item = normalizeKey(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}
