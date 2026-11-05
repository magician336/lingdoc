package evidence

import (
	"context"
	"errors"
	"testing"
)

// fakeScope 提供「这份资料在底座里属于哪个知识库、哪个租户」。
// 键是（项目, 资料）——判权按项目收敛，替身也要能表达「项目不对」。
type fakeScope struct {
	refs map[string]AssetScopeRef
	err  error
}

func scopeKey(projectID, assetID string) string { return projectID + "/" + assetID }

func (f *fakeScope) AssetScope(_ context.Context, projectID, assetID string) (AssetScopeRef, error) {
	if f.err != nil {
		return AssetScopeRef{}, f.err
	}
	ref, ok := f.refs[scopeKey(projectID, assetID)]
	if !ok {
		return AssetScopeRef{}, ErrAssetNotFound
	}
	return ref, nil
}

// fakeKBRead 是知识库读取权的替身，可以中途翻脸——F07 的「撤权」就靠它。
type fakeKBRead struct {
	allowed  map[string]bool
	calls    int
	lastID   string
	err      error
	revokeAt map[string]int // 第 N 次问到时改口
}

func (f *fakeKBRead) CanReadKB(_ context.Context, _ Actor, kbID string, _ uint64) (bool, error) {
	f.calls++
	f.lastID = kbID
	if f.err != nil {
		return false, f.err
	}
	if at, ok := f.revokeAt[kbID]; ok && f.calls >= at {
		return false, nil
	}
	return f.allowed[kbID], nil
}

// 授权必须现查。这条测试同时钉住两件事：每次都问一次底座，
// 以及同一份资料在两次判定之间被撤权后，第二次必须拒。
func TestFixedAuthorizerAsksEveryTime(t *testing.T) {
	ctx := context.Background()
	scopes := &fakeScope{refs: map[string]AssetScopeRef{
		scopeKey("p-1", "a-1"): {KnowledgeBaseID: "kb-1", OwnerTenantID: 7},
	}}
	kb := &fakeKBRead{
		allowed:  map[string]bool{"kb-1": true},
		revokeAt: map[string]int{"kb-1": 2}, // 第二次问到时已撤权
	}
	authz := NewFixedAuthorizer(scopes, kb)
	actor := Actor{UserID: "u-1", TenantID: "7"}
	asset := Asset{ID: "a-1", ProjectID: "p-1"}

	first, err := authz.CanAccessAsset(ctx, actor, "p-1", asset)
	if err != nil || !first {
		t.Fatalf("撤权前应当放行，得到 (%v, %v)", first, err)
	}
	second, err := authz.CanAccessAsset(ctx, actor, "p-1", asset)
	if err != nil {
		t.Fatalf("撤权后判定失败: %v", err)
	}
	if second {
		t.Fatal("撤权后仍放行——判定被缓存了")
	}
	if kb.calls != 2 {
		t.Fatalf("底座被问了 %d 次, want 2（每次判定都要现查）", kb.calls)
	}
	if kb.lastID != "kb-1" {
		t.Fatalf("判权问的知识库是 %q, want kb-1（资料自己的库）", kb.lastID)
	}
}

// 判权依赖取不到时必须如实报错，不能悄悄回一个 false——那会把
// 「资料刚刚被删」说成「没有权限」，给出一个错的原因。
func TestFixedAuthorizerPropagatesScopeFailure(t *testing.T) {
	ctx := context.Background()
	broken := errors.New("scope 读不到")
	authz := NewFixedAuthorizer(&fakeScope{err: broken}, &fakeKBRead{allowed: map[string]bool{}})

	got, err := authz.CanAccessAsset(ctx, Actor{}, "p-1", Asset{ID: "a-1"})
	if !errors.Is(err, broken) {
		t.Fatalf("错误被吞了：%v", err)
	}
	if got {
		t.Fatal("出错时不得放行")
	}

	// 资料已不在绑定表里：同样如实报错，而不是回 false。
	authz = NewFixedAuthorizer(&fakeScope{refs: map[string]AssetScopeRef{}}, &fakeKBRead{allowed: map[string]bool{}})
	if _, err := authz.CanAccessAsset(ctx, Actor{}, "p-1", Asset{ID: "a-gone"}); !errors.Is(err, ErrAssetNotFound) {
		t.Fatalf("资料消失时返回 %v, want ErrAssetNotFound", err)
	}
}

func TestFixedAuthorizerPropagatesCheckerFailure(t *testing.T) {
	broken := errors.New("权限服务不可达")
	authz := NewFixedAuthorizer(
		&fakeScope{refs: map[string]AssetScopeRef{
			scopeKey("p-1", "a-1"): {KnowledgeBaseID: "kb-1", OwnerTenantID: 7},
		}},
		&fakeKBRead{err: broken},
	)
	if _, err := authz.CanAccessAsset(context.Background(), Actor{}, "p-1", Asset{ID: "a-1"}); !errors.Is(err, broken) {
		t.Fatalf("权限服务故障被吞了：%v", err)
	}
}

// F07：撤权后，绑定关系还在、资料也还就绪，但允许集合必须立刻空掉，
// 且原因是 not_authorized（HTTP 上映射 403 source_access_denied）。
func TestGatewayDeniesAfterRevocation(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	actor := Actor{UserID: "u-1", TenantID: "7"}

	in := bindInputKB("p-1", "k-1", "kb-1", nil)
	asset, err := store.Bind(ctx, in)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	kb := &fakeKBRead{allowed: map[string]bool{"kb-1": true}, revokeAt: map[string]int{"kb-1": 2}}
	gateway := NewAssetGateway(store, NewFixedAuthorizer(store, kb))

	before, err := gateway.ResolveAllowed(ctx, "p-1", actor, []string{asset.ID})
	if err != nil {
		t.Fatalf("ResolveAllowed(撤权前): %v", err)
	}
	if len(before.Allowed) != 1 {
		t.Fatalf("撤权前的允许集合 = %v", assetIDs(before.Allowed))
	}

	after, err := gateway.ResolveAllowed(ctx, "p-1", actor, []string{asset.ID})
	if err != nil {
		t.Fatalf("ResolveAllowed(撤权后): %v", err)
	}
	if len(after.Allowed) != 0 {
		t.Fatalf("撤权后仍允许：%v", assetIDs(after.Allowed))
	}
	if len(after.Denied) != 1 || after.Denied[0].Reason != DenyNotAuthorized {
		t.Fatalf("撤权后的拒绝原因 = %+v, want not_authorized", after.Denied)
	}
}

// F22：一部分资料没授权 → 那一份按 not_authorized 拒，其余照常允许；
// 请求集合与「允许 ∪ 拒绝」必须逐项相等，不允许静默丢掉任何一项。
func TestGatewayPartialAuthorizationIsExplicit(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	actor := Actor{UserID: "u-1", TenantID: "7"}

	ids := make([]string, 0, 3)
	for i, kbID := range []string{"kb-allowed", "kb-denied", "kb-allowed"} {
		in := bindInputKB("p-1", "k-"+string(rune('a'+i)), kbID, nil)
		asset, err := store.Bind(ctx, in)
		if err != nil {
			t.Fatalf("Bind: %v", err)
		}
		ids = append(ids, asset.ID)
	}

	kb := &fakeKBRead{allowed: map[string]bool{"kb-allowed": true, "kb-denied": false}}
	gateway := NewAssetGateway(store, NewFixedAuthorizer(store, kb))

	res, err := gateway.ResolveAllowed(ctx, "p-1", actor, ids)
	if err != nil {
		t.Fatalf("ResolveAllowed: %v", err)
	}
	if len(res.Allowed) != 2 {
		t.Fatalf("允许集合 = %v, want 2 项", assetIDs(res.Allowed))
	}
	if len(res.Denied) != 1 || res.Denied[0].AssetID != ids[1] || res.Denied[0].Reason != DenyNotAuthorized {
		t.Fatalf("被拒项 = %+v, want 仅 %s not_authorized", res.Denied, ids[1])
	}
	seen := map[string]bool{}
	for _, a := range res.Allowed {
		seen[a.ID] = true
	}
	for _, d := range res.Denied {
		seen[d.AssetID] = true
	}
	for _, id := range ids {
		if !seen[id] {
			t.Errorf("%s 既没允许也没拒绝——部分授权被当成了全部成功", id)
		}
	}
}

// 绑定存储要能回答「这份资料属于哪个知识库、哪个租户」——契约的 Asset
// 上没有这两个字段，判权只能回存储取。
func TestBindingsAssetScope(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	in := bindInput("p-1", "k-1", nil)
	in.TenantID = 42
	in.KnowledgeBaseID = "kb-1"
	asset, err := store.Bind(ctx, in)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	scope, err := store.AssetScope(ctx, "p-1", asset.ID)
	if err != nil {
		t.Fatalf("AssetScope: %v", err)
	}
	if scope.KnowledgeBaseID != "kb-1" || scope.OwnerTenantID != 42 {
		t.Fatalf("scope = %+v, want {kb-1 42}", scope)
	}
	if _, err := store.AssetScope(ctx, "p-1", "a-missing"); !errors.Is(err, ErrAssetNotFound) {
		t.Fatalf("未知资料返回 %v, want ErrAssetNotFound", err)
	}
	// 别的项目问同一份资料：必须与「不存在」收敛成同一个错误，
	// 否则这个存储就成了跨项目探测的口子。
	if _, err := store.AssetScope(ctx, "p-2", asset.ID); !errors.Is(err, ErrAssetNotFound) {
		t.Fatalf("跨项目问 scope 返回 %v, want ErrAssetNotFound", err)
	}
}
