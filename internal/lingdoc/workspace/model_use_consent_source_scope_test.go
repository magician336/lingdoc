package workspace

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/clienterror"
	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/generation"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ── #545：授权与被授权互为前提 ────────────────────────────────────────────
//
// 一条资料范围变更改的是「谁可以用这份资料」，不是「这份资料说了什么」。
// 所以这份资料自己的分块是这次变更的**对象**，不是这次变更所依据的证据。
// 把它们算进操作级来源复核，等于让「授权」这一步依赖「被授权的那个对象」：
// 真实后端上 F01-06b 因此恒 409 stale_input（#485 隔离环境实测：请求带的
// 三个版本就是库里的当前值，拒绝来自坐标层而不是授权层）。
//
// 资料自身的来源一致性没有被放弃，只是搬到了**资料内容真正进入模型的那道
// 边界**上：坐标取不回原文的分块拿不到 available 结论，因而不会成为来源，
// 也就不会进模型的上下文。下面的三条守护把这两端都钉住。
//
// unusableChunkText 的正文比它的坐标跨度长：坐标取不回这一段，来源复核
// 因此判它 unavailable（弱档：长度不自洽 ⇒ 取不回正文）。
const unusableChunkText = "unusable synthetic chunk text"

// TestModelUseConsentProposalIsNotBlockedByTheMaterialsOwnUnusableChunks
// 许可提案不被「被授权资料自己的分块」挡住。
//
// 修掉之前，这条链在真实后端上恒 409 stale_input：提案这一步的来源复核把
// 待授权资料的分块当成「已引用来源」去复核，而那份资料此刻还没有可用的
// 授权 —— 授权与被授权互为前提。
func TestModelUseConsentProposalIsNotBlockedByTheMaterialsOwnUnusableChunks(t *testing.T) {
	f := newModelUseConsentFixture(t)

	// 前提：这份资料真的有一个「坐标取不回正文」的分块。
	seedChunk(t, f.db, "source-unusable", "knowledge-1", unusableChunkText, 0, 0, 4)
	if err := f.handler.WorkspaceSourcePolicy().Validate(policyContext(), "project-1", deliveryActorID, []string{"source-unusable"}); !errors.Is(err, candidateadoption.ErrStaleInput) {
		t.Fatalf("前提不成立：坐标不自洽的分块应被判为不可用，err = %v", err)
	}

	// 许可链照跑：提案 → 评估 → 应用。
	f.grantModelUseConsent(t)

	policy := f.readPolicy(t)
	if !policy.ModelUseAllowed {
		t.Fatal("许可链跑完了，读回来的 policy 却没有模型使用许可")
	}
	if policy.AssetRevision != 2 {
		t.Fatalf("许可钉住的资料修订 = %d, want 2（应用这一步把资料推进到新一版）", policy.AssetRevision)
	}
}

// TestModelUseConsentAbsentIsRefusedWithTheNotAuthorizedContractShape
// 「没有显式同意就不放行」这条语义没有因为上面那条修复而松动，而且它的
// 对外形状被钉在这里：422 asset_not_authorized。
func TestModelUseConsentAbsentIsRefusedWithTheNotAuthorizedContractShape(t *testing.T) {
	f := newModelUseConsentFixture(t)

	result := f.modelUseDecision(t)
	if len(result.Allowed) != 0 {
		t.Fatalf("没有许可记录时模型用途仍放行：%+v", result.Allowed)
	}
	if len(result.Denied) != 1 || result.Denied[0].AssetID != f.assetID || result.Denied[0].Reason != evidence.DenyNotAuthorized {
		t.Fatalf("没有许可记录时的判定 = %+v，必须是拒绝（not_authorized）", result.Denied)
	}

	err := generationDeniedAssetsError(result.Denied)
	form, ok := clienterror.FormOf(err)
	if !ok {
		t.Fatalf("err = %T, want a client-facing form", err)
	}
	if form.Status != 422 || form.Code != "asset_not_authorized" {
		t.Fatalf("拒绝的对外形状 = %d %s, want 422 asset_not_authorized", form.Status, form.Code)
	}
}

// generationContextStub 只回答「这一章此刻的生成上下文」。
type generationContextStub struct {
	context core.GenerationContext
}

func (s generationContextStub) GenerationContext(context.Context, core.Actor, string, string) (core.GenerationContext, error) {
	return s.context, nil
}

// generationHitKnowledgeStub 回答检索：把给定的命中原样交回去。
type generationHitKnowledgeStub struct {
	interfaces.KnowledgeBaseService
	hits []*types.SearchResult
}

func (s generationHitKnowledgeStub) HybridSearch(context.Context, string, types.SearchParams) ([]*types.SearchResult, error) {
	return s.hits, nil
}

// TestAMaterialChunkThatDoesNotMatchItsCoordinatesNeverReachesTheModel
// 资料自身的来源一致性由**生成边界**保证：坐标取不回原文的分块拿不到
// available 结论，因而不会成为来源，也就不会进模型的上下文。
//
// 这条守护是上面那条修复的安全论证的落点：授权一份坐标不自洽的资料，
// 放行的是「坐标对得上的那部分」，放行不了「对不上的那部分」——所以
// 「授权」这一步不必、也不应该依赖「被授权的那个对象」。
func TestAMaterialChunkThatDoesNotMatchItsCoordinatesNeverReachesTheModel(t *testing.T) {
	_, db, assetID := seedBoundSource(t)
	good := &types.SearchResult{
		ID: "source-1", KnowledgeID: "knowledge-1", ChunkIndex: 0,
		StartAt: 0, EndAt: len([]rune(sourcePolicyQuote)), Content: sourcePolicyQuote,
	}
	bad := &types.SearchResult{
		ID: "source-unusable", KnowledgeID: "knowledge-1", ChunkIndex: 1,
		StartAt: 0, EndAt: 4, Content: unusableChunkText,
	}
	asset := evidence.Asset{
		ID: assetID, ProjectID: "project-1", KnowledgeID: "knowledge-1",
		AssetRevision: 1, ProcessingState: evidence.AssetStateReady,
	}
	resolve := func(hits []*types.SearchResult) (generation.Input, error) {
		resolver := &generationInputResolver{
			workspace: generationContextStub{context: core.GenerationContext{
				ProjectID: "project-1", ChapterID: "chapter-1", SpecRevision: 1,
				TemplateID: "template-demo", TemplateVersion: "1",
				Chapter: core.Chapter{ID: "chapter-1", ProjectID: "project-1", SectionID: "question", Title: "研究问题"},
			}},
			gateway:   &assetGatewayStub{result: &evidence.ResolveResult{Allowed: []evidence.Asset{asset}}},
			bindings:  evidence.NewBindings(db),
			knowledge: generationHitKnowledgeStub{hits: hits},
			templates: core.ContractDemoTemplate{},
			origins:   evidence.NewSourceResolver(evidence.NewOriginReader(dbKnowledgeReader{db: db})),
		}
		return resolver.ResolveGenerationInput(policyContext(), generation.Actor{TenantID: 7, UserID: deliveryActorID}, "project-1", generation.Request{
			ChapterID: "chapter-1", AssetIDs: []string{assetID}, Instruction: "合成检索指令", ExpectedSpecRevision: 1,
		})
	}

	// 坐标对得上的那一条进模型。
	input, err := resolve([]*types.SearchResult{good})
	if err != nil {
		t.Fatalf("resolve the generation input: %v", err)
	}
	if len(input.Sources) != 1 || input.Sources[0].ID != "source-1" {
		t.Fatalf("生成输入的来源 = %+v, want 恰好 source-1", input.Sources)
	}

	// 坐标对不上的那一条不进模型：它拿不到 available 结论。
	input, err = resolve([]*types.SearchResult{good, bad})
	if err != nil {
		t.Fatalf("resolve the generation input: %v", err)
	}
	if len(input.Sources) != 1 || input.Sources[0].ID != "source-1" {
		t.Fatalf("生成输入的来源 = %+v, want 恰好 source-1（坐标不自洽的那条必须被丢掉）", input.Sources)
	}

	// 只剩坐标不自洽的那一条时，整个生成被拒：模型拿不到任何可核的正文。
	if _, err = resolve([]*types.SearchResult{bad}); !errors.Is(err, generation.ErrInsufficientEvidence) {
		t.Fatalf("只有坐标不自洽的分块时 err = %v, want insufficient_evidence", err)
	}
}
