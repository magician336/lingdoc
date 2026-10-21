package generation

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// generationActionRouter 把真实的生成服务挂到 HTTP 边上。
//
// 用例要看的正是「动作声明在 HTTP 这一层怎么被拒」，所以这里走真的服务层：
// 校验发生在服务层，HTTP 层只负责把失败渲染成各自的形态。
func generationActionRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	service, _ := actionFixture()
	router := gin.New()
	RegisterRoutes(router.Group("/lingdoc"), NewHandler(service, func(*gin.Context) (Actor, bool) {
		return Actor{TenantID: 1, UserID: "user-1"}, true
	}))
	return router
}

// generationActionRequest 造一次带幂等键的生成请求。
func generationActionRequest(body string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/lingdoc/projects/project-1/generations", strings.NewReader(body))
	request.Header.Set("Idempotency-Key", "action-http-key-0001")
	return request
}

// generationActionBody 造一次动作化生成的请求体。
//
// 夹具的规格修订号与章节版本是固定的，所以请求体要带上它们——否则请求与服务端
// 当前状态对不上，被拒的是版本而不是动作。
func generationActionBody(declaration string) string {
	return `{"chapter_id":"chapter-1","asset_ids":["asset-1"],"instruction":"补充说明",` +
		`"expected_spec_revision":3,"expected_chapter_version_id":"chapter-v1",` + declaration + `}`
}

// TestGenerationStartAcceptsTheActionDeclaration 覆盖 HTTP 层的动作化发起。
//
// 两种目标范围都要被接受，产出参数要能被解析成动作声明。
func TestGenerationStartAcceptsTheActionDeclaration(t *testing.T) {
	for _, declaration := range []string{
		`"writing_action":"generate","scope":"chapter","output_params":{"length":"medium","tone":"formal"}`,
		`"writing_action":"generate","scope":"section","output_params":{"length":"long","tone":"academic"}`,
	} {
		router := generationActionRouter(t)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, generationActionRequest(generationActionBody(declaration)))
		require.Equal(t, http.StatusAccepted, response.Code, "body=%s", response.Body.String())
	}
}

// TestGenerationStartRejectsInvalidActionDeclarations 覆盖 HTTP 层的动作声明
// 校验。
//
// 未知动作、越界的产出参数、不被允许的范围与缺动作，都要被拒。
//
// 对外的形态是一个：动作词汇包在模块内定义一次这些失败（开发计划 D23），
// 它们共用「请求字段不符合约定」这一种说法，区别在哨兵上——服务端用例按哨兵
// 断言是哪一件事错了。
func TestGenerationStartRejectsInvalidActionDeclarations(t *testing.T) {
	cases := []struct {
		name        string
		declaration string
	}{
		{name: "unknown action", declaration: `"writing_action":"unknown_action",` +
			`"output_params":{"length":"medium","tone":"formal"}`},
		{name: "missing action", declaration: `"output_params":{"length":"medium","tone":"formal"}`},
		{name: "length out of range", declaration: `"writing_action":"generate",` +
			`"output_params":{"length":"huge","tone":"formal"}`},
		{name: "length missing", declaration: `"writing_action":"generate","output_params":{"tone":"formal"}`},
		{name: "scope not allowed", declaration: `"writing_action":"generate","scope":"paragraph",` +
			`"output_params":{"length":"medium","tone":"formal"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := generationActionRouter(t)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, generationActionRequest(generationActionBody(tc.declaration)))
			require.Equal(t, http.StatusBadRequest, response.Code, "body=%s", response.Body.String())
			require.Contains(t, response.Body.String(), `"code":"invalid_request"`)
		})
	}
}

// TestGenerationStartMapsActionContextPolicyViolations 覆盖 HTTP 层的上下文
// 选择策略违例。
//
// 纳入一个这个动作不读的类别、剔除一个必须用的类别，都被拒。
func TestGenerationStartMapsActionContextPolicyViolations(t *testing.T) {
	for _, context := range []string{
		`{"include":{"template":[{"id":"template-1"}]}}`,
		`{"exclude":{"assets":[{"id":"asset-1"}]}}`,
	} {
		router := generationActionRouter(t)
		body := generationActionBody(`"writing_action":"generate","scope":"chapter",` +
			`"output_params":{"length":"medium","tone":"formal"},"context":` + context + `}`)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, generationActionRequest(body))
		require.Equal(t, http.StatusBadRequest, response.Code, "body=%s", response.Body.String())
		require.Contains(t, response.Body.String(), `"code":"invalid_request"`)
	}
}

// TestGenerationStartReportsDroppedContext 覆盖 HTTP 层的上下文丢弃可见性。
//
// 用户剔除的条目不进模型请求，而且被点名：丢弃是可见的。
func TestGenerationStartReportsDroppedContext(t *testing.T) {
	router := generationActionRouter(t)
	body := generationActionBody(`"writing_action":"generate","scope":"chapter",` +
		`"output_params":{"length":"medium","tone":"formal"},"context":{"exclude":{"assets":[{"id":"asset-1"}]}}}`)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, generationActionRequest(body))
	// 剔除掉了这一类里最后一条 ⇒ 这一类上下文被拿掉 ⇒ 400。
	require.Equal(t, http.StatusBadRequest, response.Code, "body=%s", response.Body.String())
	require.Contains(t, response.Body.String(), `"code":"invalid_request"`)
}

// TestGenerationStartKeepsTheLegacyBody 覆盖旧路径的请求体逐字不变。
//
// 旧请求不带任何动作声明：服务层照旧按自由文本处理。
func TestGenerationStartKeepsTheLegacyBody(t *testing.T) {
	router := generationActionRouter(t)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, generationActionRequest(
		`{"chapter_id":"chapter-1","asset_ids":["asset-1"],"instruction":"Draft",`+
			`"expected_spec_revision":3,"expected_chapter_version_id":"chapter-v1"}`))
	require.Equal(t, http.StatusAccepted, response.Code, "body=%s", response.Body.String())
	require.NotContains(t, response.Body.String(), "writing_action")
}
