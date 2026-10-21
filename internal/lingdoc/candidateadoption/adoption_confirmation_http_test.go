package candidateadoption

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/lingdoc/writingaction"
	"github.com/Tencent/WeKnora/internal/types"
)

// 采纳端点的 HTTP 层用例。
//
// 这一层是「服务层拒」真正落到调用者身上的地方：界面把按钮变灰不算数，
// 请求本身必须被拒，而且拒的方式要说清是哪一条。

// adoptionHTTPFixture 造一个真的跑着 SQLite 存储的采纳端点。
func adoptionHTTPFixture(t *testing.T, middleware ...gin.HandlerFunc) (*gin.Engine, *SQLiteCandidateAdoptionStore) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store := newCandidateAdoptionStore(t)
	seedCandidateAdoptionWorkspace(t, store,
		Chapter{ID: "chapter-1", ProjectID: "project-1", SectionID: "question", Title: "问题"},
		Candidate{
			ID: "candidate-1", ProjectID: "project-1", ChapterID: "chapter-1", RunID: "run-1",
			BodyMarkdown: "候选正文 [[source:source-1]]", SourceIDs: []string{"source-1"},
			Basis:    Basis{SpecRevision: 1, TemplateID: "template-demo", TemplateVersion: "1"},
			Validity: "fresh", WritingAction: writingaction.ActionGenerate, Origin: writingaction.OriginGenerated,
		}, 1)
	allowSources := sourcePolicyFunc(func(context.Context, string, string, []string) error { return nil })
	service := NewCandidateAdoptionService(store, allowSources, allowAuthorizer{})
	handler := NewCandidateAdoptionHandler(service, nil, func(*gin.Context) (string, bool) { return "user-1", true })
	router := gin.New()
	// 中间件必须在路由注册之前挂上：gin 的 Use 只影响之后注册的路由。
	router.Use(middleware...)
	RegisterRoutes(router, handler)
	return router, store
}

// adoptionRequestBody 造一次采纳的请求体。
func adoptionRequestBody(t *testing.T, key string, confirmation any) *bytes.Reader {
	t.Helper()
	body := map[string]any{
		"candidate_id":                "candidate-1",
		"expected_spec_revision":      1,
		"expected_chapter_version_id": nil,
		"replace_existing":            false,
	}
	if confirmation != nil {
		body["confirmation"] = confirmation
	}
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	return bytes.NewReader(encoded)
}

// adoptionConfirmationBody 造一份确认载荷的 JSON 形状。
func adoptionConfirmationBody(t *testing.T, overrides map[string]any) map[string]any {
	t.Helper()
	confirmation := map[string]any{
		"writing_action":  string(writingaction.ActionGenerate),
		"adoption_target": string(writingaction.TargetFormalVersionWhole),
		"confirmed_at":    time.Date(2026, 10, 22, 1, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
		"confirmed_by":    "user-1",
		"origin":          string(writingaction.OriginGenerated),
	}
	for key, value := range overrides {
		if value == nil {
			delete(confirmation, key)
			continue
		}
		confirmation[key] = value
	}
	return confirmation
}

// postAcceptance 发一次采纳请求。
func postAcceptance(t *testing.T, router *gin.Engine, key string, confirmation any) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/projects/project-1/chapters/chapter-1/acceptances", adoptionRequestBody(t, key, confirmation))
	request.Header.Set("Idempotency-Key", key)
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestAcceptanceEndpointRefusesAMissingConfirmation(t *testing.T) {
	router, _ := adoptionHTTPFixture(t)
	recorder := postAcceptance(t, router, "http-missing-confirmation", nil)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "invalid_request")
}

func TestAcceptanceEndpointRefusesATargetTheActionDoesNotAllow(t *testing.T) {
	router, _ := adoptionHTTPFixture(t)
	confirmation := adoptionConfirmationBody(t, map[string]any{
		"writing_action":  string(writingaction.ActionOutline),
		"adoption_target": string(writingaction.TargetFormalVersionWhole),
	})
	recorder := postAcceptance(t, router, "http-forbidden-target", confirmation)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "invalid_request")
}

func TestAcceptanceEndpointRefusesAConfirmationMadeForSomebodyElse(t *testing.T) {
	router, _ := adoptionHTTPFixture(t)
	confirmation := adoptionConfirmationBody(t, map[string]any{"confirmed_by": "somebody-else"})
	recorder := postAcceptance(t, router, "http-other-confirmer", confirmation)
	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), "forbidden")
}

func TestAcceptanceEndpointRefusesAMachineIdentity(t *testing.T) {
	router, _ := adoptionHTTPFixture(t, func(c *gin.Context) {
		c.Request = c.Request.WithContext(types.WithTenantAPIKeyScope(c.Request.Context(), types.TenantAPIKeyScope{
			KeyID: 9, ScopeType: types.APIKeyScopeTenant, FullAccess: true,
		}))
	})
	confirmation := adoptionConfirmationBody(t, nil)
	recorder := postAcceptance(t, router, "http-machine-identity", confirmation)
	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), "human_decision_required")
}

func TestAcceptanceEndpointAcceptsAWholeConfirmation(t *testing.T) {
	router, store := adoptionHTTPFixture(t)
	confirmation := adoptionConfirmationBody(t, nil)
	recorder := postAcceptance(t, router, "http-whole-confirmation", confirmation)
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())

	var versions int64
	require.NoError(t, store.DB().Model(&chapterVersionRow{}).Count(&versions).Error)
	require.Equal(t, int64(1), versions)
}

func TestAcceptanceEndpointReplaysTheSameKeyAndConflictsOnDifferentContent(t *testing.T) {
	router, store := adoptionHTTPFixture(t)
	confirmation := adoptionConfirmationBody(t, nil)

	first := postAcceptance(t, router, "http-same-key", confirmation)
	require.Equal(t, http.StatusCreated, first.Code, first.Body.String())

	replay := postAcceptance(t, router, "http-same-key", confirmation)
	require.Equal(t, http.StatusCreated, replay.Code, replay.Body.String())
	require.Contains(t, replay.Body.String(), `"replayed":true`)

	// 同一个键，别的确认内容：真冲突。
	other := adoptionConfirmationBody(t, map[string]any{"adoption_target": string(writingaction.TargetWorkingCopyWhole)})
	conflict := postAcceptance(t, router, "http-same-key", other)
	require.Equal(t, http.StatusConflict, conflict.Code)
	require.Contains(t, conflict.Body.String(), "idempotency_conflict")

	var versions int64
	require.NoError(t, store.DB().Model(&chapterVersionRow{}).Count(&versions).Error)
	require.Equal(t, int64(1), versions, "同键重试不得写第二版")
}

func TestAcceptanceEndpointAnswersAnOldDigestKeyWithExpiry(t *testing.T) {
	router, store := adoptionHTTPFixture(t)
	// 一条属于旧版摘要规则的行：答案是「键已过期」，不是「冲突」。
	require.NoError(t, store.DB().Create(&adoptionIdempotencyRow{
		ID: "http-legacy-row", ProjectID: "project-1", ChapterID: "chapter-1", ActorID: "user-1",
		Key: "http-legacy-key", RequestHash: "legacy-digest", DigestVersion: 1,
		ChapterVersionID: "version-1", ResponseJSON: "{}",
	}).Error)
	confirmation := adoptionConfirmationBody(t, nil)
	recorder := postAcceptance(t, router, "http-legacy-key", confirmation)
	require.Equal(t, http.StatusConflict, recorder.Code)
	require.Contains(t, recorder.Body.String(), "idempotency_key_expired")
}
