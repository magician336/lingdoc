package workspace

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 弱档判据就够用：测试里的资料取不回原文（FilePath 为空），此时可用与否只看
// 坐标自洽——与 http_currentness_test.go 的既有用例同一条判据。
const sourcePolicyQuote = "current synthetic excerpt"

// seedBoundSource 复用 currentness 用例的库与绑定，再补上一份可用知识与分块行。
// 知识行必须先于绑定写入：AssetGateway 会刷新资料版本，缺行会被读成「正在删除」。
func seedBoundSource(t *testing.T) (*Handler, *gorm.DB, string) {
	t.Helper()
	handler, db := newSourceCurrentnessHandler(t)
	ctx := context.Background()
	processedAt := time.Date(2026, time.September, 26, 10, 0, 0, 0, time.UTC)

	if err := db.Exec(
		"INSERT INTO knowledges (id, tenant_id, knowledge_base_id, type, title, file_type, file_size, file_hash, file_path, parse_status, processed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		"knowledge-1", 7, "kb-1", "file", "synthetic PDF", "pdf",
		len([]rune(sourcePolicyQuote)), "hash-1", "", types.ParseStatusCompleted, processedAt,
	).Error; err != nil {
		t.Fatalf("seed knowledge: %v", err)
	}

	asset, err := handler.bindings.Bind(ctx, evidence.BindInput{
		TenantID:        7,
		ProjectID:       "project-1",
		KnowledgeID:     "knowledge-1",
		KnowledgeBaseID: "kb-1",
		Title:           "synthetic PDF",
		CreatedBy:       "reader",
		Signal: evidence.KnowledgeSignal{
			KnowledgeID: "knowledge-1",
			ParseStatus: types.ParseStatusCompleted,
			FileHash:    "hash-1",
			FileSize:    int64(len([]rune(sourcePolicyQuote))),
			ProcessedAt: processedAt,
		},
	})
	if err != nil {
		t.Fatalf("bind asset: %v", err)
	}

	seedChunk(t, db, "source-1", "knowledge-1", sourcePolicyQuote, 0, 0, len([]rune(sourcePolicyQuote)))
	return handler, db, asset.ID
}

func seedChunk(t *testing.T, db *gorm.DB, sourceID, knowledgeID, content string, revision, startAt, endAt int) {
	t.Helper()
	if err := db.Exec(
		"INSERT INTO chunks (id, tenant_id, knowledge_id, knowledge_base_id, content, content_revision, chunk_index, start_at, end_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		sourceID, 7, knowledgeID, "kb-1", content, revision, 0, startAt, endAt,
	).Error; err != nil {
		t.Fatalf("seed chunk %s: %v", sourceID, err)
	}
}

// policyContext 带上租户：复核要拿它构造 evidence.Actor，缺了就只能默认拒绝。
func policyContext() context.Context { return policyContextWithUser("reader") }

func policyContextWithUser(userID string) context.Context {
	ctx := context.WithValue(context.Background(), types.UserIDContextKey, userID)
	return context.WithValue(ctx, types.TenantIDContextKey, uint64(7))
}

func TestCandidateSourcePolicyAcceptsACurrentBoundSource(t *testing.T) {
	handler, _, assetID := seedBoundSource(t)
	policy := handler.CandidateAdoptionSourcePolicy()
	if policy == nil {
		t.Fatal("装配处拿不到复核实现，nil 仍在")
	}

	// 装配处靠类型断言才能把复核接给确认服务，而断言失败是静默的：这里在值上再确认一次。
	confirmation, ok := policy.(candidateadoption.ConfirmationSourcePolicy)
	if !ok {
		t.Fatal("复核没有实现 ConfirmationSourcePolicy，确认服务拿到的仍是 nil")
	}

	ctx := policyContext()
	if err := policy.Validate(ctx, "project-1", "reader", []string{"source-1"}); err != nil {
		t.Fatalf("Validate rejected a current bound source: %v", err)
	}
	versions, err := confirmation.ValidateCurrent(ctx, "project-1", "reader", []string{"source-1"})
	if err != nil {
		t.Fatalf("ValidateCurrent rejected a current bound source: %v", err)
	}
	current, err := handler.bindings.CurrentAsset(ctx, "project-1", assetID)
	if err != nil {
		t.Fatalf("read current asset: %v", err)
	}
	want := []candidateadoption.AssetVersion{{AssetID: assetID, AssetRevision: current.AssetRevision}}
	if len(versions) != len(want) || versions[0] != want[0] {
		t.Fatalf("ValidateCurrent = %+v, want %+v", versions, want)
	}
}

func TestCandidateSourcePolicyCollapsesDuplicateSourceIDs(t *testing.T) {
	handler, _, assetID := seedBoundSource(t)
	policy := handler.CandidateAdoptionSourcePolicy()
	ctx := policyContext()

	// 同一枚引用的多种写法（重复、带空白）只该判一次，也只该交回一个资料版本。
	versions, err := policy.(candidateadoption.ConfirmationSourcePolicy).
		ValidateCurrent(ctx, "project-1", "reader", []string{"source-1", " source-1 ", "source-1"})
	if err != nil {
		t.Fatalf("ValidateCurrent rejected repeated ids: %v", err)
	}
	if len(versions) != 1 || versions[0].AssetID != assetID {
		t.Fatalf("ValidateCurrent = %+v, want a single %s", versions, assetID)
	}
}

func TestCandidateSourcePolicyRejectsAnEditedSource(t *testing.T) {
	handler, db, _ := seedBoundSource(t)
	policy := handler.CandidateAdoptionSourcePolicy()

	// 该块被编辑过：ContentRevision 非零即坐标不再描述当前正文（术语表 §2「默认拒绝」）。
	if err := db.Exec("UPDATE chunks SET content_revision = 1 WHERE id = ?", "source-1").Error; err != nil {
		t.Fatalf("edit chunk: %v", err)
	}
	err := policy.Validate(policyContext(), "project-1", "reader", []string{"source-1"})
	if !errors.Is(err, candidateadoption.ErrStaleInput) {
		t.Fatalf("Validate = %v, want ErrStaleInput", err)
	}
}

func TestCandidateSourcePolicyRejectsCoordinatesThatDoNotResolve(t *testing.T) {
	handler, db, _ := seedBoundSource(t)
	policy := handler.CandidateAdoptionSourcePolicy()

	// 长度不自洽：坐标取不回正文，与产出侧的弱档判据同一条。
	seedChunk(t, db, "source-2", "knowledge-1", sourcePolicyQuote, 0, 0, 99)
	err := policy.Validate(policyContext(), "project-1", "reader", []string{"source-2"})
	if !errors.Is(err, candidateadoption.ErrStaleInput) {
		t.Fatalf("Validate = %v, want ErrStaleInput", err)
	}
}

func TestCandidateSourcePolicyRejectsAnUnknownSource(t *testing.T) {
	handler, _, _ := seedBoundSource(t)
	policy := handler.CandidateAdoptionSourcePolicy()
	err := policy.Validate(policyContext(), "project-1", "reader", []string{"missing-source"})
	if !errors.Is(err, candidateadoption.ErrSourceAccessDenied) {
		t.Fatalf("Validate = %v, want ErrSourceAccessDenied", err)
	}
}

func TestCandidateSourcePolicyRejectsAKnowledgeOutsideTheProject(t *testing.T) {
	handler, db, _ := seedBoundSource(t)
	policy := handler.CandidateAdoptionSourcePolicy()

	// 分块行在，但它指向的知识没有绑到本项目：不属于本项目的引用一律拒绝，
	// 不能因为「分块行存在」就放行。
	seedChunk(t, db, "source-3", "knowledge-elsewhere", sourcePolicyQuote, 0, 0, len([]rune(sourcePolicyQuote)))
	err := policy.Validate(policyContext(), "project-1", "reader", []string{"source-3"})
	if !errors.Is(err, candidateadoption.ErrSourceAccessDenied) {
		t.Fatalf("Validate = %v, want ErrSourceAccessDenied", err)
	}
}

func TestCandidateSourcePolicyRejectsADeletedKnowledge(t *testing.T) {
	handler, db, _ := seedBoundSource(t)
	policy := handler.CandidateAdoptionSourcePolicy()

	if err := db.Exec("DELETE FROM knowledges WHERE id = ?", "knowledge-1").Error; err != nil {
		t.Fatalf("delete knowledge: %v", err)
	}
	err := policy.Validate(policyContext(), "project-1", "reader", []string{"source-1"})
	if !errors.Is(err, candidateadoption.ErrSourceAccessDenied) {
		t.Fatalf("Validate = %v, want ErrSourceAccessDenied", err)
	}
}

// 装配处那行 nil 的验收：适配器必须一路走到确认服务上。
// NewCandidateAdoptionHandler 里的类型断言失败是**静默**的（candidate_adoption_http.go:26），
// 断言没过时 Confirmations.Sources 仍是 nil，T12 于是对每一章带引用的确认返回 422。
func TestCandidateAdoptionHandlerReceivesTheSourcePolicy(t *testing.T) {
	handler, db, _ := seedBoundSource(t)
	store := candidateadoption.NewSQLiteCandidateAdoptionStore(db)
	service := candidateadoption.NewCandidateAdoptionService(store, handler.CandidateAdoptionSourcePolicy(), nil)
	adoption := candidateadoption.NewCandidateAdoptionHandler(service, func(*gin.Context) (string, bool) {
		return "reader", true
	})
	if adoption.Confirmations == nil {
		t.Fatal("NewCandidateAdoptionHandler 没有建出确认服务")
	}
	if adoption.Confirmations.Sources == nil {
		t.Fatal("确认服务拿到的仍是 nil：带引用的章节确认会恒返回 422")
	}
}

func TestCandidateSourcePolicyPrefersStaleOverDenied(t *testing.T) {
	handler, db, _ := seedBoundSource(t)
	policy := handler.CandidateAdoptionSourcePolicy()

	// 一批里同时有「资料层拒绝」与「坐标失效」：可修的那一类优先，
	// 且与请求顺序无关（被拒的那条排在前）。与生成侧同一条读法。
	seedChunk(t, db, "source-2", "knowledge-1", sourcePolicyQuote, 1, 0, len([]rune(sourcePolicyQuote)))
	err := policy.Validate(policyContext(), "project-1", "reader", []string{"missing-source", "source-2"})
	if !errors.Is(err, candidateadoption.ErrStaleInput) {
		t.Fatalf("Validate = %v, want ErrStaleInput", err)
	}
}

func TestCandidateSourcePolicyWithoutTenantIsDenied(t *testing.T) {
	handler, _, _ := seedBoundSource(t)
	policy := handler.CandidateAdoptionSourcePolicy()

	// 身份不全就无从判定「此刻还授不授权」：默认拒绝，不去猜一个更宽松的答案。
	err := policy.Validate(context.Background(), "project-1", "reader", []string{"source-1"})
	if !errors.Is(err, candidateadoption.ErrSourceAccessDenied) {
		t.Fatalf("Validate = %v, want ErrSourceAccessDenied", err)
	}
}
