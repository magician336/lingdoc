package evidence

import (
	"context"
	"testing"
)

// fakeBindings 记录被查询次数，用于断言"空范围不得触达存储"。
type fakeBindings struct {
	assets []Asset
	calls  int
}

func (f *fakeBindings) BoundAssets(_ context.Context, _ string) ([]Asset, error) {
	f.calls++
	return f.assets, nil
}

// fakeAuthorizer 用拒绝名单模拟"当前调用者能访问哪些资料"。
type fakeAuthorizer struct {
	denied map[string]bool
}

func (f *fakeAuthorizer) CanAccessAsset(_ context.Context, _ Actor, _ string, asset Asset) (bool, error) {
	return !f.denied[asset.ID], nil
}

func testAsset(id string, state AssetState) Asset {
	return Asset{
		ID:              id,
		ProjectID:       "p-demo",
		KnowledgeID:     "k-" + id,
		Title:           "合成资料 " + id,
		AssetRevision:   1,
		ProcessingState: state,
	}
}

// 部分授权不得静默当全部成功：被拒项必须逐条出现在 Denied 里，
// 而不是只返回允许的子集让调用方以为全成功。
func TestResolveAllowedPartitionsRequestedSet(t *testing.T) {
	ctx := context.Background()
	bindings := &fakeBindings{assets: []Asset{
		testAsset("a-1", AssetStateReady),
		testAsset("a-2", AssetStateReady),
		testAsset("a-3", AssetStateReady),
	}}
	gw := NewAssetGateway(bindings, &fakeAuthorizer{denied: map[string]bool{"a-3": true}})

	got, err := gw.ResolveAllowed(ctx, "p-demo", Actor{UserID: "u-1", TenantID: "t-1"}, []string{"a-1", "a-2", "a-3"})
	if err != nil {
		t.Fatalf("ResolveAllowed: %v", err)
	}

	if len(got.Requested) != 3 {
		t.Errorf("Requested = %v, want 3 项回显", got.Requested)
	}
	if len(got.Allowed) != 2 {
		t.Errorf("Allowed = %d 项, want 2", len(got.Allowed))
	}
	if len(got.Denied) != 1 {
		t.Fatalf("Denied = %d 项, want 1（部分授权必须逐项报出）", len(got.Denied))
	}
	if got.Denied[0].AssetID != "a-3" || got.Denied[0].Reason != DenyNotAuthorized {
		t.Errorf("Denied[0] = %+v, want {a-3, %s}", got.Denied[0], DenyNotAuthorized)
	}
}

// 未就绪的资料不得用空解析结果充当证据。
func TestResolveAllowedReportsNotReady(t *testing.T) {
	ctx := context.Background()
	bindings := &fakeBindings{assets: []Asset{
		testAsset("a-1", AssetStateReady),
		testAsset("a-2", AssetStateProcessing),
	}}
	gw := NewAssetGateway(bindings, &fakeAuthorizer{})

	got, err := gw.ResolveAllowed(ctx, "p-demo", Actor{UserID: "u-1"}, []string{"a-1", "a-2"})
	if err != nil {
		t.Fatalf("ResolveAllowed: %v", err)
	}

	if len(got.Allowed) != 1 || got.Allowed[0].ID != "a-1" {
		t.Errorf("Allowed = %+v, want 仅 a-1", got.Allowed)
	}
	if len(got.Denied) != 1 || got.Denied[0].Reason != DenyNotReady {
		t.Errorf("Denied = %+v, want 1 项 not_ready", got.Denied)
	}
}

// 未绑定到本项目的资料与"不存在"对外收敛为同一结果，不泄露存在性。
func TestResolveAllowedReportsMissingAsNotFound(t *testing.T) {
	ctx := context.Background()
	bindings := &fakeBindings{assets: []Asset{testAsset("a-1", AssetStateReady)}}
	gw := NewAssetGateway(bindings, &fakeAuthorizer{})

	got, err := gw.ResolveAllowed(ctx, "p-demo", Actor{UserID: "u-1"}, []string{"a-1", "a-other-project"})
	if err != nil {
		t.Fatalf("ResolveAllowed: %v", err)
	}

	if len(got.Denied) != 1 || got.Denied[0].AssetID != "a-other-project" || got.Denied[0].Reason != DenyNotFound {
		t.Errorf("Denied = %+v, want 1 项 not_found", got.Denied)
	}
}

// 空范围不得扩大为全库：既不返回任何资料，也不触达存储。
func TestResolveAllowedEmptyRequestTouchesNothing(t *testing.T) {
	ctx := context.Background()
	bindings := &fakeBindings{assets: []Asset{testAsset("a-1", AssetStateReady), testAsset("a-2", AssetStateReady)}}
	gw := NewAssetGateway(bindings, &fakeAuthorizer{})

	got, err := gw.ResolveAllowed(ctx, "p-demo", Actor{UserID: "u-1"}, nil)
	if err != nil {
		t.Fatalf("ResolveAllowed: %v", err)
	}

	if len(got.Requested) != 0 || len(got.Allowed) != 0 || len(got.Denied) != 0 {
		t.Errorf("空请求必须返回空结果, got %+v", got)
	}
	if bindings.calls != 0 {
		t.Errorf("空请求触达绑定存储 %d 次, want 0（空范围 != 全库）", bindings.calls)
	}
}
