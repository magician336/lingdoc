package workspace

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	adoption "github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	tpl "github.com/Tencent/WeKnora/internal/lingdoc/template"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type docxHTTPSaveResponse struct {
	Version  DOCXSavedVersion `json:"version"`
	Replayed bool             `json:"replayed"`
}

type docxHTTPConfirmResponse struct {
	Confirmation adoption.Confirmation `json:"confirmation"`
	Replayed     bool                  `json:"replayed"`
}

func bindDOCXHTTPRuleSet(t *testing.T, f candidateOneConnFixture) {
	t.Helper()
	definition, err := f.runtime.templates.Get("template-demo", "1")
	require.NoError(t, err)
	set := tpl.RuleSet{ID: "docx-http-rules", Version: "1", State: tpl.RuleSetDraft}
	for _, name := range []string{"nonempty_chapter", "source_available", "review_items_decided"} {
		set.Bindings = append(set.Bindings, tpl.RuleBinding{RuleID: "docx-http-" + name, DefinitionID: "computed/" + name, DefinitionVersion: "1", Origin: "builtin", Severity: "blocking", TriggerScenes: []string{"preflight", "write_check"}, Enabled: true, Owner: "lingdoc"})
	}
	published, err := tpl.PublishRuleSet(set)
	require.NoError(t, err)
	definition.Rules = nil
	definition.Sections = make([]tpl.Section, 0, len(docxSectionSpecs))
	for i, spec := range docxSectionSpecs {
		definition.Sections = append(definition.Sections, tpl.Section{ID: spec.ID, Title: spec.Token, Order: i + 1, Required: true})
	}
	definition.RuleSetSnapshot = &published
	definition.RuleSetRef = &tpl.RuleSetReference{ID: published.ID, Version: published.Version, Hash: published.RulesetHash}
	definition, err = tpl.WithHashes(definition)
	require.NoError(t, err)
	raw, err := json.Marshal(definition)
	require.NoError(t, err)
	require.NoError(t, f.db.Exec("UPDATE lingdoc_project_template_copies SET definition_json=?,content_hash=?,ruleset_hash=? WHERE id='actual-copy'", string(raw), definition.ContentHash, definition.RulesetHash).Error)
	require.NoError(t, f.db.Exec("ALTER TABLE lingdoc_project_template_copies ADD COLUMN ruleset_ref_json TEXT NOT NULL DEFAULT 'null'").Error)
	refJSON, err := json.Marshal(definition.RuleSetRef)
	require.NoError(t, err)
	require.NoError(t, f.db.Exec("UPDATE lingdoc_project_template_copies SET ruleset_ref_json=? WHERE id='actual-copy'", string(refJSON)).Error)
}

func newDOCXHTTPFixture(t *testing.T) (candidateOneConnFixture, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	f := newCandidateOneConnFixture(t)
	seedDOCXChapterMappings(t, f)
	bindDOCXHTTPRuleSet(t, f)
	require.NoError(t, NewDOCXVersionStore(f.db).AutoMigrate(f.ctx))
	router := gin.New()
	(&DOCXHTTPHandler{Runtime: f.runtime}).Register(router)
	return f, router
}

func performDOCXHTTPRequest(t *testing.T, router http.Handler, ctx context.Context, method, path, key string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	var err error
	if body != nil {
		raw, err = json.Marshal(body)
		require.NoError(t, err)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(raw))
	request = request.WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func decodeDOCXHTTPResponse[T any](t *testing.T, response *httptest.ResponseRecorder) T {
	t.Helper()
	var result T
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result), response.Body.String())
	return result
}

func saveDOCXOverHTTP(t *testing.T, f candidateOneConnFixture, router http.Handler, key string, expected int64, data []byte) docxHTTPSaveResponse {
	t.Helper()
	response := performDOCXHTTPRequest(t, router, f.ctx, http.MethodPost, "/api/docx/projects/project-1/versions", key, map[string]any{
		"expectedDiskVersion": expected,
		"data":                base64.StdEncoding.EncodeToString(data),
	})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	return decodeDOCXHTTPResponse[docxHTTPSaveResponse](t, response)
}

func docxHTTPConfirmationRequest(version DOCXSavedVersion, sectionID string, specRevision int) DOCXChapterConfirmationRequest {
	for _, section := range version.Projection.Sections {
		if section.SectionID == sectionID {
			return DOCXChapterConfirmationRequest{
				SavedVersionID:              version.ID,
				ExpectedDocSHA256:           version.DocSHA256,
				ExpectedManifestHash:        version.ManifestHash,
				ExpectedSectionDeliveryHash: section.SectionDeliveryHash,
				SectionID:                   sectionID,
				ExpectedSpecRevision:        specRevision,
				Decisions:                   []adoption.ReviewDecision{},
			}
		}
	}
	return DOCXChapterConfirmationRequest{}
}

func TestDOCXHTTPSaveConfirmReplayAndNegativeBoundaries(t *testing.T) {
	f, router := newDOCXHTTPFixture(t)
	data := replaceDOCXDocument(t, readDOCXFixture(t), func(document string) string {
		marker := "字以内）。"
		index := bytes.Index([]byte(document), []byte(marker))
		require.Greater(t, index, 0)
		insertAt := index + len(marker)
		return document[:insertAt] + "真实路由合成正文 [[source:source-1]]" + document[insertAt:]
	})
	firstSave := saveDOCXOverHTTP(t, f, router, "http-save-v1", 0, data)
	require.False(t, firstSave.Replayed)
	require.EqualValues(t, 1, firstSave.Version.DiskVersion)

	latest := performDOCXHTTPRequest(t, router, f.ctx, http.MethodGet, "/api/docx/projects/project-1/versions/latest", "", nil)
	require.Equal(t, http.StatusOK, latest.Code, latest.Body.String())
	require.Equal(t, firstSave.Version.ID, decodeDOCXHTTPResponse[DOCXSavedVersion](t, latest).ID)

	replayedSave := saveDOCXOverHTTP(t, f, router, "http-save-v1", 0, data)
	require.True(t, replayedSave.Replayed)
	require.Equal(t, firstSave.Version.ID, replayedSave.Version.ID)

	confirmationRequest := docxHTTPConfirmationRequest(firstSave.Version, "topic-summary", f.input.ExpectedSpecRevision)
	confirm := func(key string, request DOCXChapterConfirmationRequest) *httptest.ResponseRecorder {
		return performDOCXHTTPRequest(t, router, f.ctx, http.MethodPost, "/api/docx/projects/project-1/confirmations", key, request)
	}
	firstResponse := confirm("http-confirm-v1", confirmationRequest)
	require.Equal(t, http.StatusOK, firstResponse.Code, firstResponse.Body.String())
	first := decodeDOCXHTTPResponse[docxHTTPConfirmResponse](t, firstResponse)
	require.False(t, first.Replayed)
	require.NotEmpty(t, first.Confirmation.ID)

	replayedResponse := confirm("http-confirm-v1", confirmationRequest)
	require.Equal(t, http.StatusOK, replayedResponse.Code, replayedResponse.Body.String())
	replayed := decodeDOCXHTTPResponse[docxHTTPConfirmResponse](t, replayedResponse)
	require.True(t, replayed.Replayed)
	require.Equal(t, first.Confirmation.ID, replayed.Confirmation.ID)

	secondSave := saveDOCXOverHTTP(t, f, router, "http-save-v2", 1, data)
	require.EqualValues(t, 2, secondSave.Version.DiskVersion)
	oldReceiptResponse := confirm("http-confirm-v1", confirmationRequest)
	require.Equal(t, http.StatusOK, oldReceiptResponse.Code, oldReceiptResponse.Body.String())
	oldReceipt := decodeDOCXHTTPResponse[docxHTTPConfirmResponse](t, oldReceiptResponse)
	require.True(t, oldReceipt.Replayed)
	require.Equal(t, first.Confirmation.ID, oldReceipt.Confirmation.ID)

	staleConfirmation := confirm("http-confirm-old-version", confirmationRequest)
	require.Equal(t, http.StatusConflict, staleConfirmation.Code, staleConfirmation.Body.String())
	require.Equal(t, "VERSION_CHANGED", decodeDOCXHTTPResponse[map[string]string](t, staleConfirmation)["error"])

	wrongIdentity := docxHTTPConfirmationRequest(secondSave.Version, "topic-summary", f.input.ExpectedSpecRevision)
	wrongIdentity.ExpectedDocSHA256 = "wrong"
	wrongIdentityResponse := confirm("http-confirm-wrong-identity", wrongIdentity)
	require.Equal(t, http.StatusConflict, wrongIdentityResponse.Code, wrongIdentityResponse.Body.String())

	require.NoError(t, f.db.Exec("UPDATE tenant_members SET status='removed' WHERE user_id=?", f.actor.UserID).Error)
	revokedConfirmation := confirm("http-confirm-v1", confirmationRequest)
	require.Equal(t, http.StatusForbidden, revokedConfirmation.Code, revokedConfirmation.Body.String())
	revokedLatest := performDOCXHTTPRequest(t, router, f.ctx, http.MethodGet, "/api/docx/projects/project-1/versions/latest", "", nil)
	require.Equal(t, http.StatusForbidden, revokedLatest.Code, revokedLatest.Body.String())
	revokedSave := performDOCXHTTPRequest(t, router, f.ctx, http.MethodPost, "/api/docx/projects/project-1/versions", "http-save-v1", map[string]any{
		"expectedDiskVersion": 0,
		"data":                base64.StdEncoding.EncodeToString(data),
	})
	require.Equal(t, http.StatusForbidden, revokedSave.Code, revokedSave.Body.String())
	require.NoError(t, f.db.Exec("UPDATE tenant_members SET status='active' WHERE user_id=?", f.actor.UserID).Error)

	require.NoError(t, f.db.Exec("UPDATE chunks SET content_revision=1 WHERE id='source-1'").Error)
	currentConfirmation := docxHTTPConfirmationRequest(secondSave.Version, "topic-summary", f.input.ExpectedSpecRevision)
	revokedSource := confirm("http-confirm-source-revoked", currentConfirmation)
	require.Equal(t, http.StatusUnprocessableEntity, revokedSource.Code, revokedSource.Body.String())
	require.Equal(t, "SOURCE_INVALID", decodeDOCXHTTPResponse[map[string]string](t, revokedSource)["error"])

	unauthenticated := performDOCXHTTPRequest(t, router, context.Background(), http.MethodGet, "/api/docx/projects/project-1/versions/latest", "", nil)
	require.Equal(t, http.StatusUnauthorized, unauthenticated.Code, unauthenticated.Body.String())
	outsider := types.WithExecutionTenant(types.WithCaller(context.Background(), types.Caller{TenantID: 7, UserID: "outsider", Role: types.TenantRoleOwner}), 7)
	forbidden := performDOCXHTTPRequest(t, router, outsider, http.MethodGet, "/api/docx/projects/project-1/versions/latest", "", nil)
	require.Equal(t, http.StatusForbidden, forbidden.Code, forbidden.Body.String())
}

func TestDOCXHTTPConfirmationRunsBoundRules(t *testing.T) {
	f, router := newDOCXHTTPFixture(t)
	saved := saveDOCXOverHTTP(t, f, router, "http-rule-save", 0, readDOCXFixture(t))
	request := docxHTTPConfirmationRequest(saved.Version, "topic-summary", f.input.ExpectedSpecRevision)
	var before int64
	require.NoError(t, f.db.Table("lingdoc_chapter_confirmations").Count(&before).Error)
	response := performDOCXHTTPRequest(t, router, f.ctx, http.MethodPost, "/api/docx/projects/project-1/confirmations", "http-rule-confirm", request)
	require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
	problem := decodeDOCXHTTPResponse[struct {
		Error   string            `json:"error"`
		Details []docxRuleFailure `json:"details"`
	}](t, response)
	require.Equal(t, "RULE_CHECK_FAILED", problem.Error)
	require.Len(t, problem.Details, 1)
	require.Equal(t, "docx-http-nonempty_chapter", problem.Details[0].RuleID)
	require.Equal(t, "chapter/topic-summary", problem.Details[0].TargetRef)
	require.Equal(t, "topic-summary", problem.Details[0].SectionID)
	require.Contains(t, problem.Details[0].Message, "正文为空")
	var confirmations int64
	require.NoError(t, f.db.Table("lingdoc_chapter_confirmations").Count(&confirmations).Error)
	require.Equal(t, before, confirmations, "a helpful error must not bypass the confirmation gate")
}
