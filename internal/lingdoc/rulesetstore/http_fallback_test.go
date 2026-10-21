package rulesetstore

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestUnknownFailureFallsBackWithoutLeaking pins the fallback of the rule set
// storage surface: a failure that declares no form answers 500
// internal_error with the generic message, and the body leaks neither the
// internal text nor a package name.
func TestUnknownFailureFallsBackWithoutLeaking(t *testing.T) {
	internal := errors.New("pq: duplicate key value violates unique constraint \"lingdoc_rule_set_versions\"")

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/lingdoc/rule-sets", nil)

	writeApplicationError(ctx, internal, "req-1")

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
	if body.Error.Message != "操作失败，请稍后再试。" {
		t.Fatalf("message = %q", body.Error.Message)
	}
	if !body.Error.Retryable {
		t.Fatal("retryable = false, want true")
	}
	if body.RequestID != "req-1" {
		t.Fatalf("request_id = %q, want req-1", body.RequestID)
	}
	for _, leak := range []string{"pq:", "SQLSTATE", "duplicate key", "rulesetstore", "gorm"} {
		if strings.Contains(recorder.Body.String(), leak) {
			t.Fatalf("响应泄漏了内部机制 %q：%s", leak, recorder.Body.String())
		}
	}
}
