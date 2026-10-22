package workspace

// 语言评审的用例：报告的五要素、预览与拒绝不动工作副本、显式应用复用既有机制、
// 逐条处置必填理由、基线变化给版本冲突、撤权后不可应用，以及评审报告**不是**
// 候选（它进不了采纳那条路）。
//
// 夹具自包含：本文件不借用上一张票的夹具，让这一票的切片能独立判绿。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/clienterror"
	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/lingdoc/writingaction"
	"github.com/Tencent/WeKnora/internal/types"
)

// reviewWorkspace 记下工作副本的写入。
//
// 它只实现选段改写那条窄口（授权、读项目、读工作副本、写回结果）——这四个
// 方法就是改写用例真正用得着的全部，主循环之外的聚合体方法它一个也不需要。
type reviewWorkspace struct {
	copy   WorkingCopy
	writes []reviewWrite
}

// reviewWrite 是一次工作副本写入。
type reviewWrite struct {
	key   string
	body  string
	items []core.ReviewItem
}

func (w *reviewWorkspace) Authorize(context.Context, Actor, string, string) error { return nil }

func (w *reviewWorkspace) GetProject(context.Context, Actor, string) (Project, error) {
	return Project{ID: "project", Status: "active", SpecRevision: 1}, nil
}

func (w *reviewWorkspace) GetWorkingCopy(context.Context, Actor, string, string) (WorkingCopy, error) {
	return w.copy, nil
}

// ApplyRewriteResult 模拟工作副本侧的四元 CAS：修订号对不上就冲突。
func (w *reviewWorkspace) ApplyRewriteResult(_ context.Context, _ Actor, _, _, key string,
	input core.SaveWorkingCopyInput, items []core.ReviewItem) (WorkingCopyMutationResult, error) {
	w.writes = append(w.writes, reviewWrite{key: key, body: input.BodyMarkdown, items: items})
	if input.ExpectedWorkingCopyRevision != w.copy.WorkingCopyRevision {
		return WorkingCopyMutationResult{}, core.ErrVersionConflict
	}
	w.copy.WorkingCopyRevision++
	w.copy.BodyMarkdown = input.BodyMarkdown
	w.copy.ReviewItems = items
	return WorkingCopyMutationResult{WorkingCopy: w.copy}, nil
}

// reviewSources 是一个只放行一个来源的来源端口。
type reviewSources struct {
	SourceApplicationService
	denied bool
}

func (s *reviewSources) GetSource(_ context.Context, _ Actor, projectID, id string) (evidence.Source, error) {
	if s.denied || id != "existing" {
		return evidence.Source{}, ErrRewriteSourceDenied
	}
	return evidence.Source{ID: id, ProjectID: projectID, Status: evidence.SourceAvailable,
		AssetID: "asset", AssetRevision: 1, Locator: "page/1", QuotedTextHash: "hash",
		QuotedText: "supported fact"}, nil
}

// reviewModel 交回一份评审意见。
type reviewModel struct {
	calls  int
	prompt SelectedRewritePrompt
}

func (*reviewModel) RunMode() string { return "mock" }

func (m *reviewModel) RewriteSelected(_ context.Context, _ Actor, p SelectedRewritePrompt) (SelectedRewriteOutput, error) {
	m.calls++
	m.prompt = p
	return SelectedRewriteOutput{
		SourceIDs:   []string{"existing"},
		ReviewItems: []candidateadoption.ReviewItem{{Statement: "这一句的主语与后文不一致"}},
		RunMode:     "mock",
	}, nil
}

// reviewFixture 造一个语言评审的夹具。
func reviewFixture(t *testing.T) (*SelectedRewriteService, *reviewWorkspace, *reviewSources, *reviewModel) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "review.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	body := "研究问题：保留事实 [[source:existing]]"
	workspace := &reviewWorkspace{copy: WorkingCopy{ProjectID: "project", ChapterID: "chapter",
		SpecRevision: 1, WorkingCopyRevision: 1, BodyMarkdown: body, SourceIDs: []string{"existing"}}}
	sources := &reviewSources{}
	model := &reviewModel{}
	service := NewSelectedRewriteService(db, workspace, nil, sources, model)
	require.NoError(t, service.store.AutoMigrate(context.Background()))
	require.NoError(t, service.reviewStore().autoMigrate(context.Background()))
	return service, workspace, sources, model
}

// reviewRequest 造一次选段评审的请求。
func reviewRequest() ReviewReportRequest {
	selected := "研究问题：保留事实 [[source:existing]]"
	return ReviewReportRequest{
		Scope:                       writingaction.ScopeSection,
		Selection:                   &RewriteSelection{EndUTF16: len(utf16.Encode([]rune(selected))), SelectedText: selected},
		ExpectedSpecRevision:        1,
		ExpectedWorkingCopyRevision: 1,
	}
}

func reviewActor() Actor { return Actor{TenantID: 1, UserID: "owner"} }

// TestLanguageReviewProducesAReportWithItsFiveElements 断言评审报告带着五要素：
// 评审意见、来源、基线版本、报告版本与撤权状态。
//
// 这是矩阵里的 A35：报告可读、可预览、可处置。
func TestLanguageReviewProducesAReportWithItsFiveElements(t *testing.T) {
	service, workspace, _, model := reviewFixture(t)

	report, err := service.CreateReviewReport(context.Background(), reviewActor(), "project", "chapter",
		"review-report-key", reviewRequest())
	require.NoError(t, err)

	// 评审意见。
	require.Len(t, report.Findings, 1)
	require.Equal(t, "这一句的主语与后文不一致", report.Findings[0].Statement)
	require.NotEmpty(t, report.Findings[0].ID)
	// 来源。
	require.Equal(t, []string{"existing"}, report.SourceIDs)
	require.Len(t, report.AuthorizedSources, 1)
	// 基线版本：应用时要复核的那一份工作副本。
	require.Equal(t, int64(1), report.Baseline.WorkingCopyRevision)
	require.Equal(t, int64(1), report.Baseline.SpecRevision)
	require.NotEmpty(t, report.Baseline.BodyHash)
	// 报告版本：可复算。
	require.NotEmpty(t, report.ReportVersion)
	// 撤权状态：生成时的授权状态。
	require.Equal(t, candidateadoption.ValidityFresh, report.AuthorizationState)
	// 运行模式分离：报告带运行模式（开发计划 A27）。
	require.Equal(t, candidateadoption.RunModeMock, report.RunMode)
	// 报告的产出不是候选：动作与来源照样记下来。
	require.Equal(t, writingaction.ActionReview, report.WritingAction)
	require.Equal(t, writingaction.OriginGenerated, report.Origin)
	require.Equal(t, ReviewReportReady, report.State)

	// 生成报告**不改工作副本**。
	require.Empty(t, workspace.writes)
	require.Equal(t, int64(1), workspace.copy.WorkingCopyRevision)
	require.Equal(t, 1, model.calls)
	// 写给模型的是评审的提示：产出形状是评审意见，不是改好的正文。
	require.Equal(t, string(writingaction.ProduceFindings), model.prompt.Produce)
}

// TestLanguageReviewReportIsNotACandidate 断言评审报告**进不了采纳那条路**。
//
// 评审没有可采纳物：动作的许可位是空集，报告也不在候选表里。任何采纳目标都
// 会被拒（开发计划 D-348-3）。
func TestLanguageReviewReportIsNotACandidate(t *testing.T) {
	service, _, _, _ := reviewFixture(t)
	report, err := service.CreateReviewReport(context.Background(), reviewActor(), "project", "chapter",
		"review-not-candidate", reviewRequest())
	require.NoError(t, err)

	targets, err := writingaction.TargetsFor(writingaction.ActionReview)
	require.NoError(t, err)
	require.Empty(t, targets, "评审没有可采纳物：许可位必须是空集")
	require.NotEmpty(t, report.ReportID)
}

// TestLanguageReviewPreviewAndRejectLeaveTheWorkingCopyUntouched 断言预览与拒绝
// 都不改工作副本（开发计划 A24）。
func TestLanguageReviewPreviewAndRejectLeaveTheWorkingCopyUntouched(t *testing.T) {
	service, workspace, _, _ := reviewFixture(t)
	report, err := service.CreateReviewReport(context.Background(), reviewActor(), "project", "chapter",
		"review-reject-key", reviewRequest())
	require.NoError(t, err)

	previewed, err := service.GetReviewReport(context.Background(), reviewActor(), "project", report.ReportID)
	require.NoError(t, err)
	require.Equal(t, report.ReportID, previewed.ReportID)
	require.Len(t, previewed.Findings, 1)

	rejected, err := service.RejectReviewReport(context.Background(), reviewActor(), "project", report.ReportID,
		"review-reject-action")
	require.NoError(t, err)
	require.Equal(t, ReviewReportRejected, rejected.State)

	// 拒绝之后工作副本一个字符都没动。
	require.Empty(t, workspace.writes)
	require.Equal(t, int64(1), workspace.copy.WorkingCopyRevision)
	require.Equal(t, "研究问题：保留事实 [[source:existing]]", workspace.copy.BodyMarkdown)

	// 被拒绝的报告不能再应用。
	_, _, err = service.ApplyReviewReport(context.Background(), reviewActor(), "project", "chapter",
		report.ReportID, "review-apply-after-reject", ApplyReviewInput{
			ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 1,
			ReviewDecisions: []candidateadoption.ReviewDecision{{
				ReviewItemID: report.Findings[0].ID, Disposition: "resolved", Reason: "已改写"}},
		})
	require.Error(t, err)
	require.Empty(t, workspace.writes)
}

// TestApplyingTheReviewReportWritesItsFindingsIntoTheWorkingCopy 断言显式应用把
// 评审意见写进工作副本的待核事项，并且正文一个��动。
func TestApplyingTheReviewReportWritesItsFindingsIntoTheWorkingCopy(t *testing.T) {
	service, workspace, _, _ := reviewFixture(t)
	report, err := service.CreateReviewReport(context.Background(), reviewActor(), "project", "chapter",
		"review-apply-key", reviewRequest())
	require.NoError(t, err)
	before := workspace.copy.BodyMarkdown

	applied, replayed, err := service.ApplyReviewReport(context.Background(), reviewActor(), "project", "chapter",
		report.ReportID, "review-apply-action", ApplyReviewInput{
			ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 1,
			ReviewDecisions: []candidateadoption.ReviewDecision{{
				ReviewItemID: report.Findings[0].ID, Disposition: "resolved", Reason: "已按意见改写"}},
		})
	require.NoError(t, err)
	require.False(t, replayed)

	// 正文没动，意见进了待核事项。
	require.Equal(t, before, applied.BodyMarkdown)
	require.Len(t, applied.ReviewItems, 1)
	require.Equal(t, report.Findings[0].Statement, applied.ReviewItems[0].Statement)
	require.Len(t, workspace.writes, 1)

	// 幂等键：同一个键重放，不再写一次。
	_, replayed, err = service.ApplyReviewReport(context.Background(), reviewActor(), "project", "chapter",
		report.ReportID, "review-apply-action", ApplyReviewInput{
			ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 1,
			ReviewDecisions: []candidateadoption.ReviewDecision{{
				ReviewItemID: report.Findings[0].ID, Disposition: "resolved", Reason: "已按意见改写"}},
		})
	require.NoError(t, err)
	require.True(t, replayed)
	require.Len(t, workspace.writes, 1, "重放不得再写一次工作副本")

	// 报告的处置被记下来了。
	stored, err := service.reviewStore().byID(context.Background(), 1, "project", report.ReportID)
	require.NoError(t, err)
	require.Equal(t, string(ReviewReportApplied), stored.State)
	require.Contains(t, stored.DecisionsJSON, "已按意见改写")
}

// TestReviewDispositionNeedsAReason 断言逐条处置必填理由。
func TestReviewDispositionNeedsAReason(t *testing.T) {
	service, workspace, _, _ := reviewFixture(t)
	report, err := service.CreateReviewReport(context.Background(), reviewActor(), "project", "chapter",
		"review-reason-key", reviewRequest())
	require.NoError(t, err)

	// 没有理由。
	_, _, err = service.ApplyReviewReport(context.Background(), reviewActor(), "project", "chapter",
		report.ReportID, "review-reason-action", ApplyReviewInput{
			ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 1,
			ReviewDecisions: []candidateadoption.ReviewDecision{{
				ReviewItemID: report.Findings[0].ID, Disposition: "resolved"}},
		})
	require.Error(t, err)
	require.Empty(t, workspace.writes)

	// 少一条处置。
	_, _, err = service.ApplyReviewReport(context.Background(), reviewActor(), "project", "chapter",
		report.ReportID, "review-reason-action", ApplyReviewInput{
			ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 1,
		})
	require.Error(t, err)
	require.Empty(t, workspace.writes)

	// 处置值不在这套模型里。
	_, _, err = service.ApplyReviewReport(context.Background(), reviewActor(), "project", "chapter",
		report.ReportID, "review-reason-action", ApplyReviewInput{
			ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 1,
			ReviewDecisions: []candidateadoption.ReviewDecision{{
				ReviewItemID: report.Findings[0].ID, Disposition: "ignore", Reason: "不管"}},
		})
	require.Error(t, err)
	require.Empty(t, workspace.writes)
}

// TestReviewApplicationRefusesAChangedBaseline 断言基线变化时应用被拒并给版本
// 冲突，而不是覆盖。
func TestReviewApplicationRefusesAChangedBaseline(t *testing.T) {
	service, workspace, _, _ := reviewFixture(t)
	report, err := service.CreateReviewReport(context.Background(), reviewActor(), "project", "chapter",
		"review-baseline-key", reviewRequest())
	require.NoError(t, err)

	// 报告之后，工作副本被别处推进了。
	workspace.copy.WorkingCopyRevision = 2
	workspace.copy.BodyMarkdown = "别人改过的正文"

	_, _, err = service.ApplyReviewReport(context.Background(), reviewActor(), "project", "chapter",
		report.ReportID, "review-baseline-action", ApplyReviewInput{
			ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 2,
			ReviewDecisions: []candidateadoption.ReviewDecision{{
				ReviewItemID: report.Findings[0].ID, Disposition: "resolved", Reason: "已改"}},
		})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrReviewBaselineConflict)
	require.Empty(t, workspace.writes, "基线冲突不得写工作副本")
	require.Equal(t, "别人改过的正文", workspace.copy.BodyMarkdown)
}

// TestReviewApplicationRefusesAReportWhoseAuthorizationIsGone 断言授权失效的
// 报告不可应用，并指向「恢复授权后重新运行」。
func TestReviewApplicationRefusesAReportWhoseAuthorizationIsGone(t *testing.T) {
	service, workspace, sources, _ := reviewFixture(t)
	report, err := service.CreateReviewReport(context.Background(), reviewActor(), "project", "chapter",
		"review-revoked-key", reviewRequest())
	require.NoError(t, err)

	// 授权没了。
	sources.denied = true
	_, _, err = service.ApplyReviewReport(context.Background(), reviewActor(), "project", "chapter",
		report.ReportID, "review-revoked-action", ApplyReviewInput{
			ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 1,
			ReviewDecisions: []candidateadoption.ReviewDecision{{
				ReviewItemID: report.Findings[0].ID, Disposition: "resolved", Reason: "已改"}},
		})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrReviewNotAuthorized)
	form, ok := clienterror.FormOf(err)
	require.True(t, ok)
	require.Contains(t, form.Message, "恢复授权后重新运行")
	require.Equal(t, 403, form.Status)
	require.Empty(t, workspace.writes)
}

// TestReviewApplicationNeedsAHumanDecision 断言应用入口复用人工门禁。
func TestReviewApplicationNeedsAHumanDecision(t *testing.T) {
	service, workspace, _, _ := reviewFixture(t)
	report, err := service.CreateReviewReport(context.Background(), reviewActor(), "project", "chapter",
		"review-machine-key", reviewRequest())
	require.NoError(t, err)

	// 机器身份（API key 作用域）不得替人做处置。
	machine := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		Capabilities: types.StringArray{string(types.APIKeyCapabilityChat)}})
	_, _, err = service.ApplyReviewReport(machine, reviewActor(), "project", "chapter",
		report.ReportID, "review-machine-action", ApplyReviewInput{
			ExpectedSpecRevision: 1, ExpectedWorkingCopyRevision: 1,
			ReviewDecisions: []candidateadoption.ReviewDecision{{
				ReviewItemID: report.Findings[0].ID, Disposition: "resolved", Reason: "已改"}},
		})
	require.Error(t, err)
	require.ErrorIs(t, err, core.ErrHumanDecisionRequired)
	require.Empty(t, workspace.writes)
}

// TestReviewReportHTTPLayer 断言评审的 HTTP 面：生成、预览、拒绝、应用。
func TestReviewReportHTTPLayer(t *testing.T) {
	service, workspace, _, _ := reviewFixture(t)
	engine := rewriteHTTPRouter(t, service)

	post := func(path, key, body string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		request = rewriteHTTPContext(request)
		request.Header.Set("Idempotency-Key", key)
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		return recorder
	}

	selected := "研究问题：保留事实 [[source:existing]]"
	created := post("/api/lingdoc/projects/project/chapters/chapter/review-reports", "http-review-create",
		`{"scope":"section","selection":{"start_utf16":0,"end_utf16":`+
			strconv.Itoa(len(utf16.Encode([]rune(selected))))+
			`,"selected_text":"研究问题：保留事实 [[source:existing]]"},`+
			`"expected_spec_revision":1,"expected_working_copy_revision":1}`)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	require.Contains(t, created.Body.String(), "report_id")
	require.Contains(t, created.Body.String(), "findings")
	require.Contains(t, created.Body.String(), "baseline")
	require.Contains(t, created.Body.String(), "report_version")
	require.Contains(t, created.Body.String(), "authorization_state")

	reportID := reportIDOf(t, created.Body.String())

	// 预览。
	preview := httptest.NewRequest(http.MethodGet, "/api/lingdoc/projects/project/review-reports/"+reportID, nil)
	preview = rewriteHTTPContext(preview)
	previewRecorder := httptest.NewRecorder()
	engine.ServeHTTP(previewRecorder, preview)
	require.Equal(t, http.StatusOK, previewRecorder.Code, previewRecorder.Body.String())

	// 应用：逐条处置必填理由，缺了就 400，而且工作副本不动。
	applied := post("/api/lingdoc/projects/project/chapters/chapter/review-reports/"+reportID+"/apply",
		"http-review-apply", `{"expected_spec_revision":1,"expected_working_copy_revision":1,"review_decisions":[]}`)
	require.Equal(t, http.StatusUnprocessableEntity, applied.Code, applied.Body.String())
	require.Empty(t, workspace.writes)

	noReason := post("/api/lingdoc/projects/project/chapters/chapter/review-reports/"+reportID+"/apply",
		"http-review-apply-no-reason",
		`{"expected_spec_revision":1,"expected_working_copy_revision":1,"review_decisions":[{"review_item_id":"x","disposition":"resolved"}]}`)
	require.Equal(t, http.StatusBadRequest, noReason.Code, noReason.Body.String())
	require.Empty(t, workspace.writes)
}

// reportIDOf 从回包里取出报告标识。
func reportIDOf(t *testing.T, body string) string {
	t.Helper()
	var envelope struct {
		Data struct {
			ReportID string `json:"report_id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &envelope))
	require.NotEmpty(t, envelope.Data.ReportID)
	return envelope.Data.ReportID
}
