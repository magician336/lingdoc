package generation

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// sourceStaleErrorBody 是这个面回答失败时的报文形状。
type sourceStaleErrorBody struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		Retryable bool   `json:"retryable"`
	} `json:"error"`
	RequestID string `json:"request_id"`
}

// TestHTTPReplayOfADriftedSourceAnswers409SourceStale 钉住「来源漂移的重放」
// 在 HTTP 上的样子。
//
// 这条用例走的是**真实接缝**：真实的重放路径（同一个幂等键、同一个请求体），
// 真实的路由与真实的错误渲染。漂移的来源由来源校验器回答（资料被修订、引文变了、
// 锚点动了），渲染出来的必须是 409 source_stale，且 retryable=false。
//
// 它曾经答 500 internal_error（retryable=true）：那个承诺把调用方引向重试，
// 而重试救不了漂移——捕获时的那份快照不会自己变回去。
func TestHTTPReplayOfADriftedSourceAnswers409SourceStale(t *testing.T) {
	gin.SetMode(gin.TestMode)
	actor := Actor{TenantID: 1, UserID: "user-1"}
	request := replayTestRequest("chapter-1")
	input := replayTestInput(actor, "project-1", "chapter-1")
	repo := &replayRepository{storedInput: input}
	repo.run = Run{ID: "run-1", ProjectID: "project-1", ChapterID: "chapter-1", Status: StatusQueued}
	repo.idempotencyKey = "f01-generate-0001"
	repo.requestHash = replayRequestHash(t, request)
	// 复核的结果与捕获时的快照不一致 ⇒ 来源漂移。
	policy := &replayPolicyStub{allowed: true, current: true}
	sources := &replaySourceValidator{err: ErrSourceStale}
	model := &countingModel{}
	enqueuer := &testEnqueuer{}
	svc := replayService(policy, repo, input, model, enqueuer)
	svc.Sources = sources
	router := gin.New()
	RegisterRoutes(router.Group("/api/v1/lingdoc"), NewHandler(svc, func(*gin.Context) (Actor, bool) {
		return actor, true
	}))
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	response := httptest.NewRecorder()
	replay := httptest.NewRequest(http.MethodPost, "/api/v1/lingdoc/projects/project-1/generations", bytes.NewReader(body))
	replay.Header.Set("Idempotency-Key", "f01-generate-0001")
	router.ServeHTTP(response, replay)

	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d，want %d；body=%s", response.Code, http.StatusConflict, response.Body.String())
	}
	var decoded sourceStaleErrorBody
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("解析响应失败：%v；body=%s", err, response.Body.String())
	}
	if decoded.Error.Code != "source_stale" {
		t.Fatalf("code = %q，want source_stale；body=%s", decoded.Error.Code, response.Body.String())
	}
	if decoded.Error.Retryable {
		t.Fatal("retryable = true，但重试同一个请求救不了来源漂移")
	}
	if decoded.Error.Message != "来源版本或定位已变化，请重新核对来源后再生成。" {
		t.Fatalf("message = %q", decoded.Error.Message)
	}
	// 这一条是本票的全部要害：回落形态说的是「再试一次」，那会把调用方引向
	// 一条烧额度却救不了漂移的重试路径。
	if bytes.Contains(response.Body.Bytes(), []byte("internal_error")) {
		t.Fatalf("来源漂移回落到 internal_error：%s", response.Body.String())
	}
	// 漂移是在重放接缝上被拒的：没有新 run、没有入队、没有模型调用。
	if sources.calls != 1 {
		t.Fatalf("source validations = %d，want the stored sources validated once", sources.calls)
	}
	if repo.createRunCalls != 0 {
		t.Fatalf("createRunCalls = %d，want no second run", repo.createRunCalls)
	}
	if enqueuer.calls != 0 {
		t.Fatalf("enqueuer calls = %d，want the refused replay to be re-armed zero times", enqueuer.calls)
	}
	if model.calls != 0 {
		t.Fatalf("model calls = %d，want the refused replay to reach no model", model.calls)
	}
}

// TestHTTPCarriedSourceStaleKeepsItsDeclaredShape 从传输层这一侧再钉一次形态。
//
// 上面那条用例证明「来源漂移的重放」真的会被这个错误答出来；这一条证明
// 「这个错误一旦答出来，就一定是这个形态」——传输层只渲染，不决定。
func TestHTTPCarriedSourceStaleKeepsItsDeclaredShape(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/lingdoc/projects/p/generations", nil)

	writeGenerationError(ctx, "req-1", ErrSourceStale)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d，want %d", recorder.Code, http.StatusConflict)
	}
	var decoded sourceStaleErrorBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("解析响应失败：%v；body=%s", err, recorder.Body.String())
	}
	if decoded.Error.Code != "source_stale" {
		t.Fatalf("code = %q，want source_stale", decoded.Error.Code)
	}
	if decoded.Error.Retryable {
		t.Fatal("retryable = true，但重试同一个请求救不了来源漂移")
	}
}
