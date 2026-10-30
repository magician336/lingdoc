package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/lingdoc/writingaction"
	"github.com/stretchr/testify/require"
)

func academicReviewRequest() ReviewReportRequest {
	input := reviewRequest()
	input.Context = &writingaction.ContextSelection{Include: map[writingaction.ContextItem][]writingaction.ContextEntry{
		writingaction.ContextCards: {{ID: "card", Version: 2}},
	}}
	return input
}

type readOnlyAcademicReviewWorkspace struct{ *reviewWorkspace }

func (w readOnlyAcademicReviewWorkspace) Authorize(_ context.Context, _ Actor, _ string, capability string) error {
	if strings.HasPrefix(capability, "write:") {
		return core.ErrNotFound
	}
	return nil
}

func TestAcademicReviewHTTPReadRejectsCorruptMissingOrRevokedProof(t *testing.T) {
	service, workspace, _, _ := reviewFixture(t)
	selector := &academicSnapshotSelectorStub{selected: snapshotSelectionFixture()}
	service.selector = selector
	report, err := service.CreateReviewReport(context.Background(), reviewActor(), "project", "chapter", "review-read-proof", academicReviewRequest())
	require.NoError(t, err)
	original, err := service.reviewStore().byID(context.Background(), reviewActor().TenantID, "project", report.ReportID)
	require.NoError(t, err)
	service.workspace = readOnlyAcademicReviewWorkspace{workspace}
	audit := &sourceAuditSinkStub{}
	service.audit = audit
	engine := rewriteHTTPRouter(t, service)
	read := func() *httptest.ResponseRecorder {
		req := rewriteHTTPContext(httptest.NewRequest(http.MethodGet, "/api/lingdoc/projects/project/review-reports/"+report.ReportID, nil))
		out := httptest.NewRecorder()
		engine.ServeHTTP(out, req)
		return out
	}
	allowed := read()
	require.Equal(t, http.StatusOK, allowed.Code, allowed.Body.String(), "read must not require write permission")
	require.Contains(t, allowed.Body.String(), "这一句的主语与后文不一致")
	require.Equal(t, "allow", audit.events[len(audit.events)-1].Decision)
	for _, tc := range []struct{ name, sources, snapshots string }{
		{"malformed", original.SourceIDsJSON, "{"},
		{"missing snapshots", original.SourceIDsJSON, "[]"},
		{"both omitted", "[]", "[]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, service.reviewStore().db.Model(&reviewReportRow{}).Where("id = ?", report.ReportID).Updates(map[string]any{"source_ids_json": tc.sources, "authorized_sources_json": tc.snapshots}).Error)
			response := read()
			require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
			require.NotContains(t, response.Body.String(), "这一句的主语与后文不一致")
			require.Equal(t, "deny", audit.events[len(audit.events)-1].Decision)
		})
	}
	require.NoError(t, service.reviewStore().db.Model(&reviewReportRow{}).Where("id = ?", report.ReportID).Updates(map[string]any{"source_ids_json": original.SourceIDsJSON, "authorized_sources_json": original.AuthorizedSourcesJSON}).Error)
	selector.denied = true
	denied := read()
	require.Equal(t, http.StatusForbidden, denied.Code, denied.Body.String())
	require.NotContains(t, denied.Body.String(), "这一句的主语与后文不一致")
	require.Equal(t, "deny", audit.events[len(audit.events)-1].Decision)
}

func TestAcademicReviewHTTPReplayRechecksStoredCardBeforeModel(t *testing.T) {
	service, _, _, model := reviewFixture(t)
	selector := &academicSnapshotSelectorStub{selected: snapshotSelectionFixture()}
	service.selector = selector
	engine := rewriteHTTPRouter(t, service)
	requestBody, err := json.Marshal(academicReviewRequest())
	require.NoError(t, err)
	post := func() *httptest.ResponseRecorder {
		req := rewriteHTTPContext(httptest.NewRequest(http.MethodPost, "/api/lingdoc/projects/project/chapters/chapter/review-reports", bytes.NewReader(requestBody)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "academic-review-replay")
		out := httptest.NewRecorder()
		engine.ServeHTTP(out, req)
		return out
	}
	created := post()
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	require.Contains(t, created.Body.String(), "这一句的主语与后文不一致")
	require.Equal(t, 1, model.calls)
	selector.denied = true
	denied := post()
	require.Equal(t, http.StatusForbidden, denied.Code, denied.Body.String())
	require.NotContains(t, denied.Body.String(), "这一句的主语与后文不一致")
	require.Equal(t, 1, model.calls, "replay must not call the model again")
	selector.denied = false
	replayed := post()
	require.Equal(t, http.StatusCreated, replayed.Code, replayed.Body.String())
	require.Equal(t, reportIDOf(t, created.Body.String()), reportIDOf(t, replayed.Body.String()))
	require.Equal(t, 1, model.calls)
	changed := academicReviewRequest()
	changed.Instruction = "different request"
	requestBody, err = json.Marshal(changed)
	require.NoError(t, err)
	conflict := post()
	require.Equal(t, http.StatusConflict, conflict.Code, conflict.Body.String())
	require.Equal(t, 1, model.calls, "a key conflict must not call the model")
}

type racingReviewModel struct {
	*reviewModel
	onCall func()
}

func (m *racingReviewModel) RewriteSelected(ctx context.Context, actor Actor, p SelectedRewritePrompt) (SelectedRewriteOutput, error) {
	out, err := m.reviewModel.RewriteSelected(ctx, actor, p)
	if m.onCall != nil {
		m.onCall()
	}
	return out, err
}

func TestAcademicReviewConcurrentReplayChecksActualStoredRecord(t *testing.T) {
	ctx := context.Background()
	service, _, _, model := reviewFixture(t)
	selector := &academicSnapshotSelectorStub{selected: snapshotSelectionFixture()}
	service.selector = selector
	input := academicReviewRequest()
	_, err := service.CreateReviewReport(ctx, reviewActor(), "project", "chapter", "original-review", input)
	require.NoError(t, err)
	prior, found, err := service.reviewStore().lookupKey(ctx, reviewActor(), "project", "chapter", "original-review")
	require.NoError(t, err)
	require.True(t, found)
	selector.denied = true
	service.model = &racingReviewModel{reviewModel: model, onCall: func() {
		prior.ID = "concurrent-review"
		prior.IdempotencyKey = "concurrent-review-key"
		require.NoError(t, service.reviewStore().db.Create(&prior).Error)
	}}
	result, err := service.CreateReviewReport(ctx, reviewActor(), "project", "chapter", "concurrent-review-key", input)
	require.ErrorIs(t, err, ErrReviewNotAuthorized)
	require.Empty(t, result.Findings)
	require.Equal(t, 2, model.calls, "the racing request was already invoking the model")
}

func TestAcademicReviewHTTPRejectAfterRevocationReturnsRedactedReceipt(t *testing.T) {
	service, workspace, _, model := reviewFixture(t)
	selector := &academicSnapshotSelectorStub{selected: snapshotSelectionFixture()}
	service.selector = selector
	engine := rewriteHTTPRouter(t, service)
	input, err := json.Marshal(academicReviewRequest())
	require.NoError(t, err)
	post := func(path, key string, body []byte) *httptest.ResponseRecorder {
		req := rewriteHTTPContext(httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		out := httptest.NewRecorder()
		engine.ServeHTTP(out, req)
		return out
	}
	created := post("/api/lingdoc/projects/project/chapters/chapter/review-reports", "rejected-review-create", input)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	id := reportIDOf(t, created.Body.String())
	selector.denied = true
	path := "/api/lingdoc/projects/project/review-reports/" + id + "/reject"
	for i := 0; i < 2; i++ {
		response := post(path, "rejected-review-key", []byte(`{}`))
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		require.NotContains(t, response.Body.String(), "这一句的主语与后文不一致")
		require.NotContains(t, response.Body.String(), "historical verified passage")
		require.NotContains(t, response.Body.String(), "existing")
		require.NotContains(t, response.Body.String(), "研究问题：保留事实")
		var envelope struct {
			Data ReviewReport `json:"data"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
		require.Equal(t, ReviewReportRejected, envelope.Data.State)
		require.Equal(t, "source_access_unverified", envelope.Data.ErrorCode)
		require.Empty(t, envelope.Data.Findings)
		require.Empty(t, envelope.Data.AuthorizedSources)
	}
	require.Equal(t, 1, model.calls)
	require.Empty(t, workspace.writes)
	stored, err := service.reviewStore().byID(context.Background(), reviewActor().TenantID, "project", id)
	require.NoError(t, err)
	require.Equal(t, string(ReviewReportRejected), stored.State)
}
