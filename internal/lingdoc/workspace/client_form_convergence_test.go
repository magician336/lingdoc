package workspace

import (
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/clienterror"
	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/generation"
	"github.com/Tencent/WeKnora/internal/lingdoc/rulesetstore"
	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	governance "github.com/Tencent/WeKnora/internal/lingdoc/templategovernance"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/gin-gonic/gin"
)

// The outward shape of the failures the four remaining surfaces used to answer
// from a table.
//
// The tables were the contract; the errors themselves were not. These
// declarations are the contract now: for each failure, the status, the code,
// the message and the retryable flag the caller must see. Changing a shape here
// is a contract change, not a refactor.
//
// Every entry below is a form this change introduced or changed. The forms the
// workspace main route already declared are pinned by client_form_contract_test.go.

func convergedFormContract() []struct {
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
		// 规则集存储面。
		{rulesetstore.ErrActorRequired, 401, "unauthenticated", "需要有效的租户身份。", false},
		{rulesetstore.ErrRuleSetTenantRequired, 401, "unauthenticated", "需要有效的租户身份。", false},
		{rulesetstore.ErrRuleSetAccessDenied, 403, "forbidden", "当前租户成员无权执行此规则集操作。", false},
		{rulesetstore.ErrRuleSetNotFound, 404, "not_found", "资源不存在或不可访问。", false},
		{rulesetstore.ErrRuleSetVersionExists, 409, "version_conflict", "内容或基线已变化，请先读取当前版本。", false},
		{rulesetstore.ErrRuleSetStateConflict, 409, "version_conflict", "内容或基线已变化，请先读取当前版本。", false},
		{rulesetstore.ErrAuthorizerUnavailable, 503, "authorization_unavailable", "授权服务暂不可用，请稍后重试。", true},
		{rulesetstore.ErrInvalidRuleSetUseAudit, 400, "invalid_request", "规则集使用审计查询范围无效。", false},
		{rulesetstore.ErrRuleSetMetricsUnavailable, 503, "metrics_unavailable", "规则集运行指标暂不可用，请稍后再试。", true},
		{rulesetstore.ErrDraftHashNotEmpty, 400, "invalid_request", "规则集不符合安全约束。", false},
		{rulesetstore.ErrInvalidRuleSetConstraint, 400, "invalid_request", "规则集不符合安全约束。", false},
		{rulesetstore.ErrInvalidRequest, 400, "invalid_request", "请求字段不符合约定。", false},
		{rulesetstore.ErrDraftStateRequired, 400, "invalid_request", "只能创建草稿规则集版本。", false},
		{template.ErrInvalidRuleSet, 400, "invalid_request", "请求字段不符合约定。", false},
		{template.ErrInvalidDefinition, 400, "invalid_request", "请求字段不符合约定。", false},
		{template.ErrRuleSetNotDraft, 409, "invalid_state", "当前版本状态不允许此操作。", false},
		{template.ErrRuleSetNotPublished, 409, "invalid_state", "当前版本状态不允许此操作。", false},

		// 规则治理面。
		{governance.ErrUnavailable, 503, "template_governance_unavailable", "目录或规则建议服务尚未配置。", true},
		{governance.ErrModelUnavailable, 503, "template_governance_unavailable", "目录或规则建议服务尚未配置。", true},
		{governance.ErrUnauthorized, 403, "template_governance_authorization_unavailable", "目录治理授权不可用。", false},
		{governance.ErrSourceUnavailable, 403, "source_access_denied", "来源已不可用或无当前授权。", false},
		{governance.ErrNotFound, 404, "not_found", "资源不存在或不可访问。", false},
		{governance.ErrInvalidRequest, 400, "invalid_request", "请求字段不符合约定。", false},
		{governance.ErrIdempotencyConflict, 409, "idempotency_conflict", "同一个操作键对应不同请求。", false},
		{governance.ErrVersionConflict, 409, "version_conflict", "内容或基线已变化，请先读取当前版本。", false},
		{governance.ErrStale, 409, "version_conflict", "内容或基线已变化，请先读取当前版本。", false},
		{governance.ErrInvalidState, 409, "invalid_state", "当前版本状态不允许此操作。", false},
		{governance.ErrReplayRequired, 422, "template_replay_required", "须用服务器注册且绑定当前版本的正反例完成回放。", false},
		{governance.ErrInvalidOutput, 502, "invalid_model_output", "模型输出不符合当前目标和规则约束。", true},
		{ErrG4HumanDecisionRequired, 403, "human_decision_required", "请使用当前人工账号复核并提交。", false},
		{ErrG4CrossTenantSourceUnsupported, 422, "cross_tenant_source_unsupported", "当前目录流程无法在同一事务复核跨租户共享授权。", false},

		// 生成面。
		{generation.ErrInvalidRequest, 400, "invalid_request", "请求字段不符合约定。", false},
		{generation.ErrNotFound, 404, "not_found", "资源不存在或不可访问。", false},
		{generation.ErrForbidden, 403, "forbidden", "无权执行该动作。", false},
		{generation.ErrSourceAccessDenied, 403, "source_access_denied", "资料授权已不可用。", false},
		{generation.ErrInsufficientEvidence, 422, "insufficient_evidence", "允许资料不足以生成该章节。", false},
		{generation.ErrAssetNotReady, 422, "asset_not_ready", "资料尚未处理完成。", false},
		{generation.ErrPreflightBlocked, 422, "preflight_blocked", "请先处理交付阻断项。", false},
		{generation.ErrInvalidState, 422, "invalid_state", "当前阶段不能执行该动作。", false},
		{generation.ErrAssetNotAuthorized, 422, "asset_not_authorized", "请求中存在未获授权的资料，未开始处理。", false},
		{generation.ErrVersionConflict, 409, "version_conflict", "输入版本已变化，请重新读取。", false},
		{generation.ErrIdempotencyConflict, 409, "idempotency_conflict", "同一幂等键对应不同请求。", false},
		{generation.ErrStaleInput, 409, "stale_input", "候选稿或快照基于旧输入。", false},
		{generation.ErrDependencyUnavailable, 503, "dependency_unavailable", "依赖暂时不可用。", true},
		{generation.ErrUnauthenticated, 401, "unauthenticated", "请登录后重试。", false},

		// 候选采纳面。
		{candidateadoption.ErrInvalidRequest, 400, "invalid_request", "请求字段不符合约定。", false},
		{candidateadoption.ErrNotFound, 404, "not_found", "资源不存在或不可访问。", false},
		{candidateadoption.ErrForbidden, 403, "forbidden", "无权执行该动作。", false},
		{candidateadoption.ErrSourceAccessDenied, 403, "source_access_denied", "资料授权已不可用。", false},
		{candidateadoption.ErrVersionConflict, 409, "version_conflict", "内容已变化，请先读取当前版本。", false},
		{candidateadoption.ErrAdoptionInvalidState, 422, "invalid_state", "当前阶段不能执行该动作。", false},
		{candidateadoption.ErrAdoptionStaleInput, 409, "stale_input", "候选稿或快照基于旧输入。", false},
		{candidateadoption.ErrAdoptionIdempotencyConflict, 409, "idempotency_conflict", "同一幂等键对应不同请求。", false},
		{candidateadoption.ErrAdoptionDependencyUnavailable, 503, "dependency_unavailable", "候选采纳依赖尚未就绪，请稍后重试。", true},
		{candidateadoption.ErrUnauthenticated, 401, "unauthenticated", "请登录后重试。", false},
		{candidateadoption.ErrAdoptionServiceUnavailable, 503, "invalid_state", "当前阶段不能执行该动作。", false},

		// 工作区核心：规则集状态场景。
		{core.ErrRuleSetStateNotAllowed, 409, "invalid_state", "当前版本状态不允许此操作。", false},
	}
}

// TestConvergedFailuresDeclareTheirOwnForm pins the outward shape of every
// failure the four remaining surfaces report.
func TestConvergedFailuresDeclareTheirOwnForm(t *testing.T) {
	for _, item := range convergedFormContract() {
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

// A scenario value wraps the sentinel it stands for, so every existing check
// keeps working and the sentinel keeps its own form for the other surfaces.
func TestScenarioValuesKeepTheirCauseAndTheSentinelForm(t *testing.T) {
	for _, item := range []struct {
		scenario error
		sentinel error
	}{
		{candidateadoption.ErrAdoptionInvalidState, candidateadoption.ErrInvalidState},
		{candidateadoption.ErrAdoptionStaleInput, candidateadoption.ErrStaleInput},
		{candidateadoption.ErrAdoptionIdempotencyConflict, candidateadoption.ErrIdempotencyConflict},
		{candidateadoption.ErrAdoptionDependencyUnavailable, candidateadoption.ErrDependencyUnavailable},
		{rulesetstore.ErrDraftHashNotEmpty, rulesetstore.ErrDraftHashMustBeEmpty},
		{rulesetstore.ErrInvalidRuleSetConstraint, template.ErrInvalidRuleSet},
		{core.ErrRuleSetStateNotAllowed, core.ErrInvalidState},
	} {
		if !errors.Is(item.scenario, item.sentinel) {
			t.Fatalf("%v 丢了它包裹的 %v", item.scenario, item.sentinel)
		}
	}
}

// TestTheSameSemanticAnswersTheSameStatusAndCodeAcrossSurfaces is the point
// of the convergence: a failure of the same kind answers the same status code
// and the same error code on every surface that reports it.
//
// The groups are semantics, not error codes: a rule set state that does not
// allow an operation and a project state that does not allow an operation are
// different kinds of failure, and they keep their own status codes.
func TestTheSameSemanticAnswersTheSameStatusAndCodeAcrossSurfaces(t *testing.T) {
	for _, group := range []struct {
		name   string
		status int
		code   string
		errs   []error
	}{
		{"资源不存在", 404, "not_found", []error{
			rulesetstore.ErrRuleSetNotFound, governance.ErrNotFound,
			generation.ErrNotFound, candidateadoption.ErrNotFound,
		}},
		{"请求不合法", 400, "invalid_request", []error{
			governance.ErrInvalidRequest, template.ErrInvalidDefinition, template.ErrInvalidRuleSet,
			rulesetstore.ErrDraftHashMustBeEmpty, generation.ErrInvalidRequest,
			candidateadoption.ErrInvalidRequest, rulesetstore.ErrInvalidRequest,
		}},
		{"幂等冲突", 409, "idempotency_conflict", []error{
			governance.ErrIdempotencyConflict, generation.ErrIdempotencyConflict,
			candidateadoption.ErrAdoptionIdempotencyConflict,
		}},
		{"版本冲突", 409, "version_conflict", []error{
			rulesetstore.ErrRuleSetVersionExists, rulesetstore.ErrRuleSetStateConflict,
			governance.ErrVersionConflict, governance.ErrStale,
			generation.ErrVersionConflict, candidateadoption.ErrVersionConflict,
		}},
		{"规则集状态不允许", 409, "invalid_state", []error{
			template.ErrRuleSetNotDraft, template.ErrRuleSetNotPublished,
			governance.ErrInvalidState, core.ErrRuleSetStateNotAllowed,
		}},
		{"阶段状态不允许", 422, "invalid_state", []error{
			generation.ErrInvalidState, candidateadoption.ErrAdoptionInvalidState,
		}},
		{"资料授权不可用", 403, "source_access_denied", []error{
			generation.ErrSourceAccessDenied, candidateadoption.ErrSourceAccessDenied,
		}},
		{"无权执行", 403, "forbidden", []error{
			generation.ErrForbidden, candidateadoption.ErrForbidden,
		}},
		{"未登录", 401, "unauthenticated", []error{
			generation.ErrUnauthenticated, candidateadoption.ErrUnauthenticated,
		}},
		{"输入已变", 409, "stale_input", []error{
			generation.ErrStaleInput, candidateadoption.ErrAdoptionStaleInput,
		}},
		{"依赖不可用", 503, "dependency_unavailable", []error{
			generation.ErrDependencyUnavailable,
		}},
		{"候选采纳依赖未就绪", 503, "dependency_unavailable", []error{
			candidateadoption.ErrAdoptionDependencyUnavailable,
		}},
	} {
		for _, err := range group.errs {
			form, ok := clienterror.FormOf(err)
			if !ok {
				t.Fatalf("%s：%v 没有声明对外形态", group.name, err)
			}
			if form.Status != group.status || form.Code != group.code {
				t.Fatalf("%s：%v 答 %d %s，期望 %d %s",
					group.name, err, form.Status, form.Code, group.status, group.code)
			}
		}
	}
}

// transportMappers are the error rendering functions of the five surfaces that
// used to hold a table of business errors.
var transportMappers = []struct {
	path string
	fn   string
}{
	{"rulesetstore/http.go", "writeApplicationError"},
	{"rulesetstore/http.go", "writeError"},
	{"generation/http.go", "writeGenerationError"},
	{"candidateadoption/candidate_adoption_http.go", "writeCandidateAdoptionError"},
	{"workspace/g4_full_http.go", "sendG4Error"},
	{"workspace/g2_full_http.go", "g2FullError"},
}

// transportFiles are the five files that used to branch per business error.
var transportFiles = []string{
	"rulesetstore/http.go",
	"generation/http.go",
	"candidateadoption/candidate_adoption_http.go",
	"workspace/g4_full_http.go",
	"workspace/g2_full_http.go",
}

// TestTransportFilesHoldNoBusinessErrorBranch makes the retirement of the
// tables structural instead of a convention.
//
// A branch per error in the transport was the reason a new business failure
// had to be remembered in two packages, and the reason a forgotten branch
// turned a 400 into a 500. The mapper may only fall back.
//
// The check is context driven: it looks inside the switch statements, the
// conditions and the call arguments of the file, and it never tries to find a
// parent node.
func TestTransportFilesHoldNoBusinessErrorBranch(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	for _, rel := range transportFiles {
		path := filepath.Join(root, "internal", "lingdoc", rel)
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("解析 %s 失败：%v", rel, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			ast.Inspect(fn, func(node ast.Node) bool {
				switch typed := node.(type) {
				case *ast.SwitchStmt:
					for _, clause := range typed.Body.List {
						branch, ok := clause.(*ast.CaseClause)
						if !ok {
							continue
						}
						for _, expr := range branch.List {
							for _, name := range errorNames(expr) {
								t.Errorf("%s:%d 的分支按业务错误 %s 选择结果——传输层不得按业务错误分支",
									rel, fset.Position(expr.Pos()).Line, name)
							}
						}
					}
				case *ast.IfStmt:
					for _, name := range errorNames(typed.Cond) {
						t.Errorf("%s:%d 的条件按业务错误 %s 判断——传输层不得按业务错误分支",
							rel, fset.Position(typed.Cond.Pos()).Line, name)
					}
				case *ast.CallExpr:
					sel, ok := typed.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					pkg, ok := sel.X.(*ast.Ident)
					if !ok || pkg.Name != "errors" {
						return true
					}
					if sel.Sel.Name != "Is" && sel.Sel.Name != "As" {
						return true
					}
					// The one place a transport may look at a failure's
					// content is the denied asset list, which is part of the
					// answer and not a classification.
					if strings.Contains(fn.Name.Name, "Details") {
						return true
					}
					for _, arg := range typed.Args {
						for _, name := range errorNames(arg) {
							t.Errorf("%s:%d 的 %s 仍按业务错误 %s 判断——传输层不得按业务错误分支",
								rel, fset.Position(typed.Pos()).Line, fn.Name.Name, name)
						}
					}
				}
				return true
			})
		}
	}
}

// TestTransportMappersOnlyFallBack keeps the renderers free of every table: a
// mapper reads the form the error declares and falls back for the failures
// that declare nothing.
func TestTransportMappersOnlyFallBack(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	for _, mapper := range transportMappers {
		path := filepath.Join(root, "internal", "lingdoc", mapper.path)
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("解析 %s 失败：%v", mapper.path, err)
		}
		found := false
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name != mapper.fn {
				continue
			}
			found = true
			ast.Inspect(fn, func(node ast.Node) bool {
				switch typed := node.(type) {
				case *ast.SwitchStmt:
					t.Errorf("%s:%d 的 %s 还在按业务错误分支",
						mapper.path, fset.Position(typed.Pos()).Line, mapper.fn)
				case *ast.IfStmt:
					for _, name := range errorNames(typed.Cond) {
						t.Errorf("%s:%d 的 %s 还在按业务错误 %s 判断",
							mapper.path, fset.Position(typed.Cond.Pos()).Line, mapper.fn, name)
					}
				case *ast.CallExpr:
					sel, ok := typed.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					pkg, ok := sel.X.(*ast.Ident)
					if !ok || pkg.Name != "errors" {
						return true
					}
					if sel.Sel.Name != "Is" && sel.Sel.Name != "As" {
						return true
					}
					for _, arg := range typed.Args {
						for _, name := range errorNames(arg) {
							t.Errorf("%s:%d 的 %s 还在按业务错误 %s 判断",
								mapper.path, fset.Position(typed.Pos()).Line, mapper.fn, name)
						}
					}
				}
				return true
			})
		}
		if !found {
			t.Fatalf("%s 里没有找到 %s", mapper.path, mapper.fn)
		}
	}
}

// errorNames reports the business error names a syntax tree mentions.
//
// A business error is named by an identifier that starts with "Err", with or
// without a package qualifier. Anything else — a status code, a string, a
// struct field — is not a business error.
func errorNames(node ast.Node) []string {
	var out []string
	ast.Inspect(node, func(inner ast.Node) bool {
		ident, ok := inner.(*ast.Ident)
		if !ok {
			return true
		}
		name := ident.Name
		if idx := strings.LastIndex(name, "."); idx >= 0 {
			name = name[idx+1:]
		}
		if strings.HasPrefix(name, "Err") && len(name) > 3 {
			out = append(out, ident.Name)
		}
		return true
	})
	return out
}

// TestUnknownFailureFallsBackWithoutLeaking pins the fallback: a failure that
// declares no form answers 500 internal_error with the generic message, and
// the body leaks neither the internal text nor a package name.
//
// The ruleset storage, generation and candidate adoption surfaces have the same
// fallback; each one pins it in its own package, where its writer lives.
func TestUnknownFailureFallsBackWithoutLeaking(t *testing.T) {
	internal := errors.New("pq: duplicate key value violates unique constraint \"lingdoc_chapters\"")
	for _, tc := range []struct {
		name    string
		write   func(*gin.Context)
		message string
	}{
		{"治理面", func(c *gin.Context) {
			sendG4Error(c, internal)
		}, "操作失败，请稍后再试。"},
		{"G2 面", func(c *gin.Context) {
			g2FullError(c, internal)
		}, "操作失败，请稍后再试。"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/lingdoc/projects/p", nil)

			tc.write(ctx)

			if recorder.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500", recorder.Code)
			}
			var body struct {
				Error struct {
					Code      string `json:"code"`
					Message   string `json:"message"`
					Retryable bool   `json:"retryable"`
				} `json:"error"`
				RequestID string `json:"request_id"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("解析响应失败：%v", err)
			}
			if body.Error.Code != "internal_error" {
				t.Fatalf("code = %q, want internal_error", body.Error.Code)
			}
			if body.Error.Message != tc.message {
				t.Fatalf("message = %q, want %q", body.Error.Message, tc.message)
			}
			for _, leak := range []string{"pq:", "SQLSTATE", "duplicate key", "rulesetstore", "candidateadoption", "generation", "workspacecore"} {
				if strings.Contains(recorder.Body.String(), leak) {
					t.Fatalf("响应泄漏了内部机制 %q：%s", leak, recorder.Body.String())
				}
			}
		})
	}
}
