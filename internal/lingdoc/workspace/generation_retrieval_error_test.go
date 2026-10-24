package workspace

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/clienterror"
	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/generation"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/models/acceptancebudget"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ── #593：检索侧的受控 profile 策略拒绝有自己的形态 ────────────────────────
//
// F01-07 那条 503 的真实根因是「embedding 模型不在受控 profile 的模型白名单里，
// 被确定性拒绝」：重试一万次也无效。同一个事实在 chat 侧有自己的形态
// （`generation_model_mismatch` / retryable=false），检索侧此前没有——任何检索错误
// 都被无条件压成 `dependency_unavailable` / retryable=true，把调用方导向无限重试，
// 而真正要做的（改 profile 授权）重试多少次都不会发生。
//
// 下面的守护分别钉住三件事：
// ① 策略拒绝有自己的、不可重试的形态；
// ② 依赖确实没装配时仍然答 `dependency_unavailable` / retryable=true；
// ③ 「模型被拒」这件事的身份在出门时没有丢（`errors.Is` 仍找得到）。

// generationFailingKnowledgeStub 让检索失败，并原样交回失败原因。
type generationFailingKnowledgeStub struct {
	interfaces.KnowledgeBaseService
	err error
}

func (s generationFailingKnowledgeStub) HybridSearch(context.Context, string, types.SearchParams) ([]*types.SearchResult, error) {
	return nil, s.err
}

// generationRetrievalResolver 装一个只差「检索这一步」的生成输入解析器。
//
// 装配与 model_use_consent_source_scope_test.go 的那份一致：资料绑定、证据网关、
// 模板与来源解析器都是真的，只有检索这一步由用例决定。
func generationRetrievalResolver(t *testing.T, knowledge interfaces.KnowledgeBaseService) (*generationInputResolver, string) {
	t.Helper()
	_, db, assetID := seedBoundSource(t)
	asset := evidence.Asset{
		ID: assetID, ProjectID: "project-1", KnowledgeID: "knowledge-1",
		AssetRevision: 1, ProcessingState: evidence.AssetStateReady,
	}
	resolver := &generationInputResolver{
		workspace: generationContextStub{context: core.GenerationContext{
			ProjectID: "project-1", ChapterID: "chapter-1", SpecRevision: 1,
			TemplateID: "template-demo", TemplateVersion: "1",
			Chapter: core.Chapter{ID: "chapter-1", ProjectID: "project-1", SectionID: "question", Title: "研究问题"},
		}},
		gateway:   &assetGatewayStub{result: &evidence.ResolveResult{Allowed: []evidence.Asset{asset}}},
		bindings:  evidence.NewBindings(db),
		knowledge: knowledge,
		templates: core.ContractDemoTemplate{},
		origins:   evidence.NewSourceResolver(evidence.NewOriginReader(dbKnowledgeReader{db: db})),
	}
	return resolver, assetID
}

// retrievalInput 走一次生成输入装配（它同步触发检索）。
func retrievalInput(t *testing.T, resolver *generationInputResolver, assetID string) error {
	t.Helper()
	_, err := resolver.ResolveGenerationInput(policyContext(), generation.Actor{TenantID: 7, UserID: deliveryActorID}, "project-1", generation.Request{
		ChapterID: "chapter-1", AssetIDs: []string{assetID}, Instruction: "合成检索指令", ExpectedSpecRevision: 1,
	})
	return err
}

// TestRetrievalRefusedByTheAcceptanceProfileKeepsItsOwnForm 钉住策略拒绝的形态。
//
// 受控 profile 拒绝模型是**确定性拒绝**：它在请求到达 provider 之前就发生了，
// 而且再发一次还是同样的答案。所以它不能答「依赖暂时不可用、可重试」——
// 那个答案会把调用方导向无限重试。它答自己的形态，并且不可重试。
//
// 拒绝的原因包在别处（`embed: %w`）时也要认得出：身份在出门时不能丢。
func TestRetrievalRefusedByTheAcceptanceProfileKeepsItsOwnForm(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
	}{
		{name: "裸的拒绝", err: acceptancebudget.ErrWrongModel},
		{name: "包过一次的拒绝", err: fmt.Errorf("embed: %w", acceptancebudget.ErrWrongModel)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			resolver, assetID := generationRetrievalResolver(t, generationFailingKnowledgeStub{err: tt.err})
			err := retrievalInput(t, resolver, assetID)
			if err == nil {
				t.Fatal("检索被拒时必须报错，却拿到了成功")
			}
			if !errors.Is(err, acceptancebudget.ErrWrongModel) {
				t.Fatalf("err = %v，丢了「模型不在 profile 内」这个身份", err)
			}
			if errors.Is(err, generation.ErrDependencyUnavailable) {
				t.Fatalf("策略拒绝仍被认成依赖不可用：%v", err)
			}
			form, ok := clienterror.FormOf(err)
			if !ok {
				t.Fatalf("err = %T，没有对外形态", err)
			}
			if form.Code == "dependency_unavailable" {
				t.Fatalf("策略拒绝仍答 dependency_unavailable：%+v", form)
			}
			if form.Retryable {
				t.Fatalf("策略拒绝仍被标成可重试：%+v", form)
			}
			if form.Status != 422 || form.Code != "acceptance_model_policy_rejected" {
				t.Fatalf("对外形态 = %d %s，want 422 acceptance_model_policy_rejected", form.Status, form.Code)
			}
		})
	}
}

// TestRetrievalFailureThatIsNotAPolicyRefusalStaysRetryable 钉住另一半。
//
// 不是策略拒绝的检索失败（向量库挂了之类）仍然是「依赖暂时不可用」：它可重试。
// 这一条是上一条的对偶——把两者混成一条码，就是把「重试一万次也无效」和
// 「再试一次可能就好」说成同一件事。
func TestRetrievalFailureThatIsNotAPolicyRefusalStaysRetryable(t *testing.T) {
	resolver, assetID := generationRetrievalResolver(t, generationFailingKnowledgeStub{err: errors.New("vector store is down")})
	err := retrievalInput(t, resolver, assetID)
	if err == nil {
		t.Fatal("检索失败时必须报错，却拿到了成功")
	}
	form, ok := clienterror.FormOf(err)
	if !ok {
		t.Fatalf("err = %T，没有对外形态", err)
	}
	if form.Status != 503 || form.Code != "dependency_unavailable" || !form.Retryable {
		t.Fatalf("对外形态 = %d %s retryable=%v，want 503 dependency_unavailable retryable=true", form.Status, form.Code, form.Retryable)
	}
}

// TestRetrievalWithoutAnAssembledDependencyStaysRetryable 钉住「依赖没装配」。
//
// 依赖确实没装配时，生成输入装配在检索之前就停了，答的仍然是可重试的
// 依赖不可用——这一支不能被上一条的变化顺手改掉。
func TestRetrievalWithoutAnAssembledDependencyStaysRetryable(t *testing.T) {
	resolver, assetID := generationRetrievalResolver(t, nil)
	err := retrievalInput(t, resolver, assetID)
	if err == nil {
		t.Fatal("依赖没装配时必须报错，却拿到了成功")
	}
	if !errors.Is(err, generation.ErrDependencyUnavailable) {
		t.Fatalf("err = %v，want dependency_unavailable", err)
	}
	form, ok := clienterror.FormOf(err)
	if !ok {
		t.Fatalf("err = %T，没有对外形态", err)
	}
	if form.Status != 503 || form.Code != "dependency_unavailable" || !form.Retryable {
		t.Fatalf("对外形态 = %d %s retryable=%v，want 503 dependency_unavailable retryable=true", form.Status, form.Code, form.Retryable)
	}
}
