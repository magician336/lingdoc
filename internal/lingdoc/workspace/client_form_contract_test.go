package workspace

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/clienterror"
	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/gin-gonic/gin"
)

// clientFormContract pins the outward shape of every business failure the
// workspace transport reports.
//
// The mapper used to hold these shapes in a table of one branch per error.
// That table was the contract; the errors themselves were not. This test is
// the contract now: it states, for each error, the status, code, message and
// retryable flag the caller must see. Changing a shape here is a contract
// change, not a refactor.
func clientFormContract() []struct {
	err       error
	status    int
	code      string
	message   string
	retryable bool
} {
	return []struct {
		err       error
		status    int
		code      string
		message   string
		retryable bool
	}{
		{core.ErrHumanDecisionRequired, 403, "human_decision_required", "此操作需要实际成员本人确认。", false},
		{core.ErrProjectInvitationRequired, 422, "project_invitation_required", "请使用邀请接受与逐成员分配流程；批量直接加入已停用。", false},
		{core.ErrRuntimePaused, 423, "runtime_paused", "当前范围已暂停，请由负责人核对并恢复。", false},
		{core.ErrRuntimeRecoveryRequired, 422, "runtime_recovery_required", "恢复前需要核对当前检查、快照和导出文件。", false},
		{core.ErrG2ConsentRequired, 422, "confirmation_required", "请明确核对范围并确认该操作。", false},
		{core.ErrG2EvidenceIncomplete, 422, "evidence_incomplete", "证据卡、原文定位或当前版本尚不完整。", false},
		{core.ErrG2OutcomeUnknown, 409, "outcome_unknown", "原操作结果尚待核对，请勿重复执行。", false},
		{core.ErrG2Unavailable, 503, "dependency_unavailable", "资料或模型能力暂不可用。", true},
		{core.ErrSemanticBatchStagingRequired, 400, "invalid_request", "关联主张与证据卡请分两次正式变更：先应用主张，再按新主张版本复核并提交证据卡变更。", false},
		{core.ErrRevisionNotStable, 422, "context_unstable", "正式变更尚未完成稳定化。请用当前交付检查回执闭环影响任务，再由 Owner 检查稳定化后冻结。", false},
		{core.ErrNotFound, 404, "not_found", "资源不存在或不可访问。", false},
		{core.ErrInvalidRequest, 400, "invalid_request", "请求字段不符合约定。", false},
		{core.ErrVersionConflict, 409, "version_conflict", "内容已变化，请先读取当前版本。", false},
		{core.ErrInvalidState, 422, "invalid_state", "当前项目状态或研究条件不满足操作要求。", false},
		{core.ErrDependencyDecided, 409, "dependency_decided", "该依赖已有人工结论，自动运行不得覆盖。", false},
		{core.ErrIdempotencyConflict, 409, "idempotency_conflict", "同一个操作键对应不同请求。", false},
		{core.ErrRequestInProgress, 409, "request_in_progress", "原请求仍在提交，请稍后用相同操作键重试。", true},
		{core.ErrSourceUnavailable, 403, "source_access_denied", "资料不可用或未获授权。", false},
		{ErrRewriteInvalidRequest, 400, "invalid_request", "改写请求字段不符合约定。", false},
		{ErrRewriteNotFound, 404, "not_found", "候选不存在或不可访问。", false},
		{ErrRewriteStale, 409, "stale_input", "选区或工作副本已变化，请重新读取后再试。", false},
		{ErrRewriteConflict, 409, "stale_input", "选区或工作副本已变化，请重新读取后再试。", false},
		{ErrRewriteKeyConflict, 409, "idempotency_conflict", "同一个操作键对应不同请求。", false},
		{ErrRewriteSourceDenied, 403, "source_access_denied", "改写候选的来源已失效或当前不可访问。", false},
		{ErrRewriteUnavailable, 503, "dependency_unavailable", "改写模型暂不可用，请稍后重试。", true},
		{ErrResearchProviderInvalid, 400, "invalid_provider", "provider_id 无效：当前租户没有可用的检索来源，请核对 provider_id 是否正确且属于本租户。", false},
		{evidence.ErrInvalidBinding, 400, "invalid_request", "请求字段不符合约定。", false},
		{evidence.ErrIdempotencyConflict, 409, "idempotency_conflict", "同一个操作键对应不同请求。", false},
		{evidence.ErrAssetNotFound, 404, "not_found", "资源不存在或不可访问。", false},
		{delivery.ErrTemplateNotFound, 404, "not_found", "资源不存在或不可访问。", false},
		{delivery.ErrInvalidRequest, 400, "invalid_request", "请求字段不符合约定。", false},
		{delivery.ErrIdempotencyConflict, 409, "idempotency_conflict", "同一个操作键对应不同请求。", false},
		{delivery.ErrSnapshotStaleInput, 409, "stale_input", "快照基于的输入已变化，请重新准备。", false},
		{delivery.ErrExportNotFound, 404, "not_found", "资源不存在或不可访问。", false},
		{delivery.ErrExportPreflightBlocked, 422, "preflight_blocked", "请先处理交付阻断项。", false},
		{delivery.ErrExportStaleInput, 409, "stale_input", "快照基于的输入已变化，请重新准备。", false},
		{delivery.ErrExportUnavailable, 422, "invalid_state", "导出文件不可用或完整性校验失败，请重新导出。", false},
		{delivery.ErrSnapshotNotFound, 404, "not_found", "资源不存在或不可访问。", false},
		{candidateadoption.ErrInvalidRequest, 400, "invalid_request", "请求字段不符合约定。", false},
		{candidateadoption.ErrNotFound, 404, "not_found", "资源不存在或不可访问。", false},
		{candidateadoption.ErrSourceAccessDenied, 403, "source_access_denied", "资料授权已不可用。", false},
		{candidateadoption.ErrVersionConflict, 409, "version_conflict", "内容已变化，请先读取当前版本。", false},
		{candidateadoption.ErrIdempotencyConflict, 409, "idempotency_conflict", "同一个操作键对应不同请求。", false},
		{candidateadoption.ErrStaleInput, 409, "stale_input", "快照基于的输入已变化，请重新准备。", false},
		{candidateadoption.ErrInvalidState, 422, "invalid_state", "当前项目状态或研究条件不满足操作要求。", false},
		{candidateadoption.ErrDependencyUnavailable, 503, "dependency_unavailable", "依赖的服务此刻不可用，请稍后重试。", true},
	}
}

func TestBusinessErrorsDeclareTheirOwnClientForm(t *testing.T) {
	for _, item := range clientFormContract() {
		form, ok := clienterror.FormOf(item.err)
		if !ok {
			t.Fatalf("%v 没有声明对外形态", item.err)
		}
		if form.Status != item.status || form.Code != item.code ||
			form.Message != item.message || form.Retryable != item.retryable {
			t.Fatalf("%v 的对外形态 = %+v，期望 status=%d code=%q message=%q retryable=%v",
				item.err, form, item.status, item.code, item.message, item.retryable)
		}
	}
}

// A wrapped error keeps the presentation of the error that carries it, and the
// cause stays reachable for the domain's own checks.
func TestWrappedBusinessErrorKeepsItsFormAndItsCause(t *testing.T) {
	wrapped := core.ErrRevisionNotStable
	form, ok := clienterror.FormOf(wrapped)
	if !ok || form.Code != "context_unstable" {
		t.Fatalf("wrapped form = %+v, %v", form, ok)
	}
	if !errors.Is(wrapped, core.ErrInvalidState) {
		t.Fatal("包装后的错误丢了它包裹的原因")
	}
}

// TestRequestInProgressCarriesTheRetryAfterHeader pins the one response header
// the workspace mapper used to set.
//
// The old mapper set "Retry-After: 1" on the request-in-progress branch, and
// the contract still declares that header. Dropping it silently would tell the
// caller to retry immediately when the request is still in flight, so the
// header is part of the outward shape and is declared with it.
func TestRequestInProgressCarriesTheRetryAfterHeader(t *testing.T) {
	form, ok := clienterror.FormOf(core.ErrRequestInProgress)
	if !ok {
		t.Fatal("ErrRequestInProgress 没有声明对外形态")
	}
	if form.RetryAfter != 1 {
		t.Fatalf("RetryAfter = %d, want 1", form.RetryAfter)
	}
}

// TestSendErrorSetsTheRetryAfterHeader checks the header actually reaches the
// caller, not just that the error declares it.
func TestSendErrorSetsTheRetryAfterHeader(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/lingdoc/projects/p", nil)

	sendError(ctx, core.ErrRequestInProgress)

	if got := recorder.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want %q", got, "1")
	}
}

// TestSendErrorOmitsTheRetryAfterHeaderWhenNoneIsDeclared makes sure the
// header is only set when the failure says so.
func TestSendErrorOmitsTheRetryAfterHeaderWhenNoneIsDeclared(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/lingdoc/projects/p", nil)

	sendError(ctx, core.ErrInvalidRequest)

	if got := recorder.Header().Get("Retry-After"); got != "" {
		t.Fatalf("Retry-After = %q, want empty", got)
	}
}

// TestSendErrorHoldsNoBusinessErrorTable keeps the mapper free of a table of
// business errors.
//
// A branch per error in the transport was the reason a new business failure
// had to be remembered in two packages, and the reason a forgotten branch
// turned a 400 into a 500.
func TestSendErrorHoldsNoBusinessErrorTable(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "http.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("解析 http.go 失败：%v", err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "sendError" {
			continue
		}
		ast.Inspect(fn, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if ident.Name == "errors" && (sel.Sel.Name == "Is" || sel.Sel.Name == "As") {
				t.Errorf("sendError 仍按业务错误逐个分支（第 %d 行）", fset.Position(call.Pos()).Line)
			}
			return true
		})
		return
	}
	t.Fatal("http.go 里没有找到 sendError")
}
