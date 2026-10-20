package candidateadoption

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestUnknownFailureFallsBackWithoutLeaking pins the fallback of the candidate
// adoption surface: a failure that declares no form answers 500
// internal_error with the generic message, and the body leaks neither the
// internal text nor a package name.
func TestUnknownFailureFallsBackWithoutLeaking(t *testing.T) {
	internal := errors.New("pq: duplicate key value violates unique constraint \"lingdoc_candidate_adoptions\"")

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/lingdoc/projects/p/chapters/c/acceptances", nil)

	writeCandidateAdoptionError(ctx, "req-1", internal)

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
	if body.Error.Message != "服务器暂时无法完成请求。" {
		t.Fatalf("message = %q", body.Error.Message)
	}
	for _, leak := range []string{"pq:", "SQLSTATE", "duplicate key", "candidateadoption", "gorm"} {
		if strings.Contains(recorder.Body.String(), leak) {
			t.Fatalf("响应泄漏了内部机制 %q：%s", leak, recorder.Body.String())
		}
	}
}

// TestTheSurfaceWordsItsOwnFailures pins the wording the candidate adoption
// surface answers with: it is the surface's own, and the domain sentinel keeps
// the wording of the workspace surface.
func TestTheSurfaceWordsItsOwnFailures(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/lingdoc/projects/p/chapters/c/acceptances", nil)

	writeCandidateAdoptionError(ctx, "req-1", surfaceError(ErrStaleInput))

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", recorder.Code)
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应失败：%v", err)
	}
	if body.Error.Code != "stale_input" || body.Error.Message != "候选稿或快照基于旧输入。" {
		t.Fatalf("error = %+v", body.Error)
	}
}
