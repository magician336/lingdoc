package generation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

type generationStartApplicationStub struct {
	Application
	err error
}

func (s generationStartApplicationStub) Start(context.Context, Actor, string, string, Request) (Run, error) {
	return Run{}, s.err
}

func TestGenerationRoutesMatchContractAndRejectMissingActor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterRoutes(router.Group("/api/v1/lingdoc"), NewHandler(nil, nil))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/lingdoc/projects/p-demo/generations", strings.NewReader(`{}`))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("POST status = %d, want %d; body=%s", response.Code, http.StatusUnauthorized, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"code":"unauthenticated"`) {
		t.Fatalf("response missing contract error envelope: %s", response.Body.String())
	}

	get := httptest.NewRequest(http.MethodGet, "/api/v1/lingdoc/projects/p-demo/generations/run-1", nil)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, get)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("GET status = %d, want %d", response.Code, http.StatusUnauthorized)
	}

	cancel := httptest.NewRequest(http.MethodPost, "/api/v1/lingdoc/projects/p-demo/generations/run-1/cancel", nil)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, cancel)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("POST cancel status = %d, want %d", response.Code, http.StatusUnauthorized)
	}

	list := httptest.NewRequest(http.MethodGet, "/api/v1/lingdoc/projects/p-demo/chapters/ch-1/candidates", nil)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, list)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("GET candidates status = %d, want %d", response.Code, http.StatusUnauthorized)
	}

	getCandidate := httptest.NewRequest(http.MethodGet, "/api/v1/lingdoc/projects/p-demo/generations/run-1/candidate", nil)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, getCandidate)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("GET candidate status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestGenerationStartStrictlyValidatesRequestBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	validResolver := func(*gin.Context) (Actor, bool) {
		return Actor{TenantID: 1, UserID: "user-1"}, true
	}
	oversized := fmt.Sprintf(`{"instruction":"%s"}`, strings.Repeat("x", maxGenerationRequestBytes))
	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{name: "unknown field", body: `{"chapter_id":"chapter-1","unexpected":true}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_request"},
		{name: "non-json unicode whitespace", body: "\u00a0{\"chapter_id\":\"chapter-1\"}", wantStatus: http.StatusBadRequest, wantCode: "invalid_request"},
		{name: "second json value", body: `{"chapter_id":"chapter-1"} {}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_request"},
		{name: "trailing data", body: `{"chapter_id":"chapter-1"} garbage`, wantStatus: http.StatusBadRequest, wantCode: "invalid_request"},
		{name: "null is not an object", body: `null`, wantStatus: http.StatusBadRequest, wantCode: "invalid_request"},
		{name: "body too large", body: oversized, wantStatus: http.StatusBadRequest, wantCode: "invalid_request"},
		{name: "valid request", body: `{"chapter_id":"chapter-1"}`, wantStatus: http.StatusServiceUnavailable, wantCode: "dependency_unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := gin.New()
			RegisterRoutes(router.Group("/api/v1/lingdoc"), NewHandler(nil, validResolver))
			request := httptest.NewRequest(http.MethodPost, "/api/v1/lingdoc/projects/p-demo/generations", strings.NewReader(tt.body))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != tt.wantStatus {
				t.Fatalf("POST status = %d, want %d; body=%s", response.Code, tt.wantStatus, response.Body.String())
			}
			if !strings.Contains(response.Body.String(), `"code":"`+tt.wantCode+`"`) {
				t.Fatalf("response missing %s error: %s", tt.wantCode, response.Body.String())
			}
		})
	}
}

func TestGenerationStartMapsContractPreflightErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	actor := Actor{TenantID: 1, UserID: "user-1"}
	validBody := `{"chapter_id":"chapter-1","asset_ids":["asset-1"],"instruction":"Draft","expected_spec_revision":1}`
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
		wantDetail string
	}{
		{name: "insufficient evidence", err: ErrInsufficientEvidence, wantStatus: http.StatusUnprocessableEntity, wantCode: "insufficient_evidence"},
		{name: "asset not ready", err: ErrAssetNotReady, wantStatus: http.StatusUnprocessableEntity, wantCode: "asset_not_ready"},
		{name: "preflight blocked", err: ErrPreflightBlocked, wantStatus: http.StatusUnprocessableEntity, wantCode: "preflight_blocked"},
		{name: "invalid state", err: ErrInvalidState, wantStatus: http.StatusUnprocessableEntity, wantCode: "invalid_state"},
		{name: "asset not authorized", err: &AssetNotAuthorizedError{Denied: []DeniedAsset{{AssetID: "asset-1", Reason: "not_authorized"}}}, wantStatus: http.StatusUnprocessableEntity, wantCode: "asset_not_authorized", wantDetail: `"asset_id":"asset-1"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewHandler(generationStartApplicationStub{err: tt.err}, func(*gin.Context) (Actor, bool) { return actor, true })
			router := gin.New()
			RegisterRoutes(router.Group("/lingdoc"), handler)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/lingdoc/projects/p1/generations", strings.NewReader(validBody)))
			if response.Code != tt.wantStatus || !strings.Contains(response.Body.String(), `"code":"`+tt.wantCode+`"`) {
				t.Fatalf("status=%d body=%s, want status=%d code=%s", response.Code, response.Body.String(), tt.wantStatus, tt.wantCode)
			}
			if tt.wantDetail != "" && !strings.Contains(response.Body.String(), tt.wantDetail) {
				t.Fatalf("response=%s, want details containing %s", response.Body.String(), tt.wantDetail)
			}
		})
	}
}

// TestGenerationStartTellsTheProfileRefusalApartFromADependencyOutage 钉住对外形态。
//
// 检索被受控 profile 拒绝（模型不在白名单里）时，生成面答 422
// `acceptance_model_policy_rejected` 且**不可重试**：那是确定性拒绝，再发一万次
// 也是同一个答案。依赖确实不可用时，答 503 `dependency_unavailable` 且**可重试**。
//
// 两者必须能被调用方分开：把「改 profile 授权」说成「稍后重试」，就是让调用方
// 在一件重试解决不了的事情上重试到天荒地老。
func TestGenerationStartTellsTheProfileRefusalApartFromADependencyOutage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	actor := Actor{TenantID: 1, UserID: "user-1"}
	validBody := `{"chapter_id":"chapter-1","asset_ids":["asset-1"],"instruction":"Draft","expected_spec_revision":1}`
	tests := []struct {
		name          string
		err           error
		wantStatus    int
		wantCode      string
		wantRetryable bool
	}{
		{name: "profile refuses the model", err: ErrAcceptanceModelRejected, wantStatus: http.StatusUnprocessableEntity, wantCode: "acceptance_model_policy_rejected", wantRetryable: false},
		{name: "dependency is unavailable", err: ErrDependencyUnavailable, wantStatus: http.StatusServiceUnavailable, wantCode: "dependency_unavailable", wantRetryable: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewHandler(generationStartApplicationStub{err: tt.err}, func(*gin.Context) (Actor, bool) { return actor, true })
			router := gin.New()
			RegisterRoutes(router.Group("/lingdoc"), handler)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/lingdoc/projects/p1/generations", strings.NewReader(validBody)))
			if response.Code != tt.wantStatus {
				t.Fatalf("status=%d body=%s, want status=%d", response.Code, response.Body.String(), tt.wantStatus)
			}
			var envelope struct {
				Error struct {
					Code      string `json:"code"`
					Retryable bool   `json:"retryable"`
				} `json:"error"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("解析响应失败：%v body=%s", err, response.Body.String())
			}
			if envelope.Error.Code != tt.wantCode || envelope.Error.Retryable != tt.wantRetryable {
				t.Fatalf("error = %+v，want code=%s retryable=%v", envelope.Error, tt.wantCode, tt.wantRetryable)
			}
		})
	}
}
