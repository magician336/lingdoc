package candidateadoption

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/lingdoc/writingaction"
)

// 三个采纳目标各自的 HTTP 层用例（#375）。
//
// 这一层是「目标改变了写入路径」落到调用者身上的地方：写入结果、章节版本
// 指针动不动、工作副本读回来是什么，都在响应与库里看得见。

// draftAcceptanceFixture 造一个带草稿的采纳端点。
//
// 现场：章节有一个正式版本（正文「第一段第二段第三段」），工作副本是它的草稿
// （修订号 1、正文与正式版本一致）。所以「正式版本」与「草稿」这两件事在这里
// 是分开的。
func draftAcceptanceFixture(t *testing.T) (*gin.Engine, *SQLiteCandidateAdoptionStore) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store := newCandidateAdoptionStore(t)
	require.NoError(t, store.DB().AutoMigrate(&adoptionWorkingCopyRow{}))
	seedCandidateAdoptionWorkspace(t, store,
		Chapter{ID: "chapter-1", ProjectID: "project-1", SectionID: "question", Title: "问题", CurrentVersionID: stringPtr("version-1")},
		Candidate{
			ID: "candidate-1", ProjectID: "project-1", ChapterID: "chapter-1", RunID: "run-1",
			BodyMarkdown: "新正文", SourceIDs: []string{},
			Basis:    Basis{SpecRevision: 1, ChapterVersionID: stringPtr("version-1"), TemplateID: "template-demo", TemplateVersion: "1"},
			Validity: "fresh", WritingAction: writingaction.ActionGenerate, Origin: writingaction.OriginGenerated,
		}, 1)
	require.NoError(t, store.DB().Create(&chapterVersionRow{
		ID: "version-1", ProjectID: "project-1", ChapterID: "chapter-1",
		BodyMarkdown: draftBody, SourceIDsJSON: "[]", CitationUsagesJSON: "[]", ReviewItemsJSON: "[]",
	}).Error)
	require.NoError(t, store.DB().Create(&adoptionWorkingCopyRow{
		ProjectID: "project-1", ChapterID: "chapter-1", BaseChapterVersionID: stringPtr("version-1"),
		SpecRevision: 1, WorkingCopyRevision: 1, BodyMarkdown: draftBody,
		SourceIDsJSON: "[]", CitationUsagesJSON: "[]", ReviewItemsJSON: "[]",
	}).Error)
	allowSources := sourcePolicyFunc(func(context.Context, string, string, []string) error { return nil })
	service := NewCandidateAdoptionService(store, allowSources, allowAuthorizer{})
	handler := NewCandidateAdoptionHandler(service, nil, func(*gin.Context) (string, bool) { return "user-1", true })
	router := gin.New()
	RegisterRoutes(router, handler)
	return router, store
}

// draftBody 是夹具里的正文。
const draftBody = "第一段第二段第三段"

// draftAcceptanceBody 造一次采纳的请求体。
func draftAcceptanceBody(t *testing.T, confirmation any, revision int64, bodyHash, snapshotID string) *bytes.Reader {
	t.Helper()
	body := map[string]any{
		"candidate_id":                   "candidate-1",
		"expected_spec_revision":         1,
		"expected_chapter_version_id":    "version-1",
		"replace_existing":               true,
		"expected_working_copy_revision": revision,
		"expected_body_hash":             bodyHash,
	}
	if snapshotID != "" {
		body["snapshot_id"] = snapshotID
	}
	if confirmation != nil {
		body["confirmation"] = confirmation
	}
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	return bytes.NewReader(encoded)
}

// postDraftAcceptance 发一次采纳请求。
func postDraftAcceptance(t *testing.T, router *gin.Engine, key string, confirmation any, revision int64, bodyHash, snapshotID string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/projects/project-1/chapters/chapter-1/acceptances",
		draftAcceptanceBody(t, confirmation, revision, bodyHash, snapshotID))
	request.Header.Set("Idempotency-Key", key)
	router.ServeHTTP(recorder, request)
	return recorder
}

// draftConfirmationBody 造一份确认载荷的 JSON 形状。
func draftConfirmationBody(t *testing.T, overrides map[string]any) map[string]any {
	t.Helper()
	return adoptionConfirmationBody(t, overrides)
}

// TestAcceptanceEndpointWritesTheWorkingCopyWithoutAFormalVersion 断言「工作副本·
// 整段」在 HTTP 层也只写草稿：不建正式版本，章节版本指针不动（A10）。
func TestAcceptanceEndpointWritesTheWorkingCopyWithoutAFormalVersion(t *testing.T) {
	router, store := draftAcceptanceFixture(t)
	confirmation := draftConfirmationBody(t, map[string]any{
		"adoption_target": string(writingaction.TargetWorkingCopyWhole),
	})
	recorder := postDraftAcceptance(t, router, "http-whole-draft", confirmation, 1, workingCopyBodyHash(draftBody), "")
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())

	var versions int64
	require.NoError(t, store.DB().Model(&chapterVersionRow{}).Count(&versions).Error)
	require.Equal(t, int64(1), versions, "只写工作副本不得建正式版本")
	var chapter chapterRow
	require.NoError(t, store.DB().First(&chapter).Error)
	require.Equal(t, "version-1", *chapter.CurrentVersionID, "章节版本指针不得动")
	var copy adoptionWorkingCopyRow
	require.NoError(t, store.DB().First(&copy).Error)
	require.Equal(t, "新正文", copy.BodyMarkdown)
	require.Equal(t, int64(2), copy.WorkingCopyRevision)
}

// TestAcceptanceEndpointWritesOnlyTheConfirmedRange 断言「工作副本·选段」在 HTTP
// 层也只写确认范围内的那一段（A8）。
func TestAcceptanceEndpointWritesOnlyTheConfirmedRange(t *testing.T) {
	router, store := draftAcceptanceFixture(t)
	confirmation := draftConfirmationBody(t, map[string]any{
		"adoption_target": string(writingaction.TargetWorkingCopyPartial),
		"scope":           map[string]any{"start_utf16": 3, "end_utf16": 6},
	})
	recorder := postDraftAcceptance(t, router, "http-partial-draft", confirmation, 1, workingCopyBodyHash(draftBody), "")
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())

	var copy adoptionWorkingCopyRow
	require.NoError(t, store.DB().First(&copy).Error)
	require.Equal(t, "第一段新正文第三段", copy.BodyMarkdown)
	var versions int64
	require.NoError(t, store.DB().Model(&chapterVersionRow{}).Count(&versions).Error)
	require.Equal(t, int64(1), versions, "部分采纳不得建正式版本")
}

// TestAcceptanceEndpointStillBuildsAFormalVersion 断言「正式版本·整段」在 HTTP
// 层仍然建正式版本并推进章节版本指针。
func TestAcceptanceEndpointStillBuildsAFormalVersion(t *testing.T) {
	router, store := draftAcceptanceFixture(t)
	confirmation := draftConfirmationBody(t, map[string]any{
		"adoption_target": string(writingaction.TargetFormalVersionWhole),
	})
	recorder := postDraftAcceptance(t, router, "http-formal-version", confirmation, 1, workingCopyBodyHash(draftBody), "")
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())

	var versions int64
	require.NoError(t, store.DB().Model(&chapterVersionRow{}).Count(&versions).Error)
	require.Equal(t, int64(2), versions)
	var chapter chapterRow
	require.NoError(t, store.DB().First(&chapter).Error)
	require.NotEqual(t, "version-1", *chapter.CurrentVersionID)
}

// TestAcceptanceEndpointRefusesADirtyWorkingCopy 断言整段替换遇到 dirty 工作副本
// ⇒ working_copy_dirty，且零写入。
func TestAcceptanceEndpointRefusesADirtyWorkingCopy(t *testing.T) {
	router, store := draftAcceptanceFixture(t)
	confirmation := draftConfirmationBody(t, map[string]any{
		"adoption_target": string(writingaction.TargetWorkingCopyWhole),
	})
	recorder := postDraftAcceptance(t, router, "http-dirty-draft", confirmation, 1, workingCopyBodyHash("用户编辑器里还没保存的内容"), "")
	require.Equal(t, http.StatusConflict, recorder.Code)
	require.Contains(t, recorder.Body.String(), "working_copy_dirty")
	require.Contains(t, recorder.Body.String(), "保存")

	var copy adoptionWorkingCopyRow
	require.NoError(t, store.DB().First(&copy).Error)
	require.Equal(t, draftBody, copy.BodyMarkdown, "被拒的采纳不得改动工作副本正文")
	require.Equal(t, int64(1), copy.WorkingCopyRevision)
	var versions int64
	require.NoError(t, store.DB().Model(&chapterVersionRow{}).Count(&versions).Error)
	require.Equal(t, int64(1), versions)
	var idem int64
	require.NoError(t, store.DB().Model(&adoptionIdempotencyRow{}).Count(&idem).Error)
	require.Zero(t, idem, "被拒的采纳不得留下幂等行")
}

// TestAcceptanceEndpointContinuesWithASnapshot 断言带快照的替换可以在 dirty 的
// 情况下继续。
func TestAcceptanceEndpointContinuesWithASnapshot(t *testing.T) {
	router, store := draftAcceptanceFixture(t)
	confirmation := draftConfirmationBody(t, map[string]any{
		"adoption_target": string(writingaction.TargetWorkingCopyWhole),
	})
	recorder := postDraftAcceptance(t, router, "http-snapshot-draft", confirmation, 1, workingCopyBodyHash("用户编辑器里还没保存的内容"), "snapshot-1")
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())

	var copy adoptionWorkingCopyRow
	require.NoError(t, store.DB().First(&copy).Error)
	require.Equal(t, "新正文", copy.BodyMarkdown)
	require.Equal(t, int64(2), copy.WorkingCopyRevision)
}

// TestAcceptanceEndpointRefusesAnOutlineCandidateBuildingAFormalVersion 断言
// outline 的候选请求建正式版本被拒（许可位那一层），且零写入。
func TestAcceptanceEndpointRefusesAnOutlineCandidateBuildingAFormalVersion(t *testing.T) {
	router, store := draftAcceptanceFixture(t)
	confirmation := draftConfirmationBody(t, map[string]any{
		"writing_action":  string(writingaction.ActionOutline),
		"adoption_target": string(writingaction.TargetFormalVersionWhole),
	})
	recorder := postDraftAcceptance(t, router, "http-outline-formal", confirmation, 1, workingCopyBodyHash(draftBody), "")
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "invalid_request")

	var versions int64
	require.NoError(t, store.DB().Model(&chapterVersionRow{}).Count(&versions).Error)
	require.Equal(t, int64(1), versions, "被拒的采纳不得建正式版本")
	var copy adoptionWorkingCopyRow
	require.NoError(t, store.DB().First(&copy).Error)
	require.Equal(t, draftBody, copy.BodyMarkdown)
}
