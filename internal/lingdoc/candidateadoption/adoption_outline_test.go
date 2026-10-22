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
)

// 提纲动作的采纳切片（#347）。
//
// 这一组用例盯的是提纲与其它动作**不一样**的那条采纳规则：提纲的允许采纳目标
// 只有「工作副本·整段」。整段采纳今天直接建正式版本并整篇覆盖正文——用提纲去
// 替换用户已写的正文，语义上就是「删除用户文本」，那是 spec 明确划在范围外的。
//
// 夹具自带：它不复用别的动作的夹具，让这张票的切片能独立判绿（开发计划 D20）。

const (
	// outlineExistingBody 是用户已经写好的正文：它住在正式版本里，也住在工作
	// 副本里（草稿还没提交）。
	outlineExistingBody = "用户已经写好的正文"
	// outlineSkeletonBody 是提纲这一次执行的产出：章节骨架。
	outlineSkeletonBody = "## 1. 研究背景\n- 背景要点 [[source:source-1]]"
)

// outlineAdoptionFixture 造一个提纲采纳的现场。
//
// 章节有一份正式版本（正文是用户已经写好的正文），工作副本是它的草稿。提纲
// 采纳只写工作副本：正式版本、章节版本指针与用户已写正文都一个字不动。
func outlineAdoptionFixture(t *testing.T) (*gin.Engine, *SQLiteCandidateAdoptionStore) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store := newCandidateAdoptionStore(t)
	require.NoError(t, store.DB().AutoMigrate(&adoptionWorkingCopyRow{}))
	require.NoError(t, store.DB().AutoMigrate(&projectAuditRow{}))
	seedCandidateAdoptionWorkspace(t, store,
		Chapter{ID: "chapter-1", ProjectID: "project-1", SectionID: "question", Title: "问题",
			CurrentVersionID: stringPtr("version-1")},
		Candidate{
			ID: "candidate-outline", ProjectID: "project-1", ChapterID: "chapter-1", RunID: "run-1",
			BodyMarkdown: outlineSkeletonBody, SourceIDs: []string{"source-1"},
			Basis: Basis{SpecRevision: 1, ChapterVersionID: stringPtr("version-1"),
				TemplateID: "template-demo", TemplateVersion: "1"},
			Validity: "fresh", WritingAction: writingaction.ActionOutline,
			Origin: writingaction.OriginGenerated,
		}, 1)
	require.NoError(t, store.DB().Create(&chapterVersionRow{
		ID: "version-1", ProjectID: "project-1", ChapterID: "chapter-1",
		BodyMarkdown: outlineExistingBody, SourceIDsJSON: "[]", CitationUsagesJSON: "[]",
		ReviewItemsJSON: "[]",
	}).Error)
	require.NoError(t, store.DB().Create(&adoptionWorkingCopyRow{
		ProjectID: "project-1", ChapterID: "chapter-1", BaseChapterVersionID: stringPtr("version-1"),
		SpecRevision: 1, WorkingCopyRevision: 1, BodyMarkdown: outlineExistingBody,
		SourceIDsJSON: "[]", CitationUsagesJSON: "[]", ReviewItemsJSON: "[]",
	}).Error)
	allowSources := sourcePolicyFunc(func(context.Context, string, string, []string) error { return nil })
	service := NewCandidateAdoptionService(store, allowSources, allowAuthorizer{})
	handler := NewCandidateAdoptionHandler(service, nil, func(*gin.Context) (string, bool) { return "user-1", true })
	router := gin.New()
	RegisterRoutes(router, handler)
	return router, store
}

// outlineConfirmationBody 造一份提纲采纳的确认载荷。
//
// 确认载荷必带四件事：动作、采纳目标、确认时刻、确认人、来源。动作恒为提纲。
func outlineConfirmationBody(overrides map[string]any) map[string]any {
	confirmation := map[string]any{
		"writing_action":  string(writingaction.ActionOutline),
		"adoption_target": string(writingaction.TargetWorkingCopyWhole),
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

// outlineConfirmation 造一份提纲采纳的确认载荷（服务层用）。
//
// 确认载荷必带四件事：动作、采纳目标、确认时刻、确认人、来源。动作恒为提纲。
func outlineConfirmation(overrides map[string]any) *AdoptionConfirmation {
	confirmation := &AdoptionConfirmation{
		WritingAction: writingaction.ActionOutline,
		Target:        writingaction.TargetWorkingCopyWhole,
		ConfirmedAt:   time.Date(2026, 10, 22, 1, 0, 0, 0, time.UTC),
		ConfirmedBy:   "user-1",
		Origin:        writingaction.OriginGenerated,
	}
	for key, value := range overrides {
		switch key {
		case "writing_action":
			confirmation.WritingAction = writingaction.Action(value.(string))
		case "adoption_target":
			confirmation.Target = writingaction.AdoptionTarget(value.(string))
		case "confirmed_by":
			confirmation.ConfirmedBy = value.(string)
		}
	}
	return confirmation
}

// postOutlineAcceptance 发一次提纲采纳请求。
func postOutlineAcceptance(t *testing.T, router *gin.Engine, key string, confirmation any,
	bodyHash, snapshotID string) *httptest.ResponseRecorder {
	t.Helper()
	body := map[string]any{
		"candidate_id":                   "candidate-outline",
		"expected_spec_revision":         1,
		"expected_chapter_version_id":    "version-1",
		"replace_existing":               false,
		"expected_working_copy_revision": 1,
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
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/projects/project-1/chapters/chapter-1/acceptances",
		bytes.NewReader(encoded))
	request.Header.Set("Idempotency-Key", key)
	router.ServeHTTP(recorder, request)
	return recorder
}

// TestOutlineAdoptionWritesTheWorkingCopyAndLeavesTheFormalVersionAlone 断言
// 「提纲采纳只写工作副本」。
//
// 工作副本拿到的是章节骨架；正式版本一个字节都不动，章节版本指针不动，用户
// 已经写好的正文不丢。这正是提纲与其它动作不一样的那条规则（A10）。
func TestOutlineAdoptionWritesTheWorkingCopyAndLeavesTheFormalVersionAlone(t *testing.T) {
	router, store := outlineAdoptionFixture(t)
	recorder := postOutlineAcceptance(t, router, "outline-adopt-whole",
		outlineConfirmationBody(nil), workingCopyBodyHash(outlineExistingBody), "")
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())

	var copy adoptionWorkingCopyRow
	require.NoError(t, store.DB().First(&copy).Error)
	require.Equal(t, outlineSkeletonBody, copy.BodyMarkdown, "工作副本拿到的是章节骨架")
	require.Equal(t, int64(2), copy.WorkingCopyRevision)

	var versions int64
	require.NoError(t, store.DB().Model(&chapterVersionRow{}).Count(&versions).Error)
	require.Equal(t, int64(1), versions, "提纲采纳不得建正式版本")
	var chapter chapterRow
	require.NoError(t, store.DB().First(&chapter).Error)
	require.Equal(t, "version-1", *chapter.CurrentVersionID, "章节版本指针不得动")
	var version chapterVersionRow
	require.NoError(t, store.DB().Where("id = ?", "version-1").First(&version).Error)
	require.Equal(t, outlineExistingBody, version.BodyMarkdown, "用户已写的正式正文不得丢")
}

// TestOutlineAdoptionRefusesAFormalVersionTarget 断言「提纲请求建立正式版本被
// 拒」，且零写入。
//
// 提纲的允许采纳目标只有「工作副本·整段」：动作自己声明这张许可位，采纳端只
// 查它，不按动作名分支。
func TestOutlineAdoptionRefusesAFormalVersionTarget(t *testing.T) {
	for _, target := range []writingaction.AdoptionTarget{
		writingaction.TargetFormalVersionWhole,
		writingaction.TargetWorkingCopyPartial,
	} {
		t.Run(string(target), func(t *testing.T) {
			router, store := outlineAdoptionFixture(t)
			recorder := postOutlineAcceptance(t, router, "outline-adopt-"+string(target),
				outlineConfirmationBody(map[string]any{"adoption_target": string(target)}),
				workingCopyBodyHash(outlineExistingBody), "")
			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.Contains(t, recorder.Body.String(), "invalid_request")

			var versions int64
			require.NoError(t, store.DB().Model(&chapterVersionRow{}).Count(&versions).Error)
			require.Equal(t, int64(1), versions, "被拒的采纳不得建正式版本")
			var chapter chapterRow
			require.NoError(t, store.DB().First(&chapter).Error)
			require.Equal(t, "version-1", *chapter.CurrentVersionID, "被拒的采纳不得动章节版本指针")
			var copy adoptionWorkingCopyRow
			require.NoError(t, store.DB().First(&copy).Error)
			require.Equal(t, outlineExistingBody, copy.BodyMarkdown, "被拒的采纳不得改动工作副本正文")
			require.Equal(t, int64(1), copy.WorkingCopyRevision)
			var idem int64
			require.NoError(t, store.DB().Model(&adoptionIdempotencyRow{}).Count(&idem).Error)
			require.Zero(t, idem, "被拒的采纳不得留下幂等行")
		})
	}
}

// TestOutlineAdoptionRefusesADirtyWorkingCopyAndWritesNothing 断言 dirty 保护。
//
// 工作副本有未保存的编辑时，服务端拒绝整篇替换并说明原因，而且零写入：用户在
// 编辑器里没保存的内容一个字都不动。
func TestOutlineAdoptionRefusesADirtyWorkingCopyAndWritesNothing(t *testing.T) {
	router, store := outlineAdoptionFixture(t)
	recorder := postOutlineAcceptance(t, router, "outline-adopt-dirty",
		outlineConfirmationBody(nil), workingCopyBodyHash("用户在编辑器里还没保存的内容"), "")
	require.Equal(t, http.StatusConflict, recorder.Code)
	require.Contains(t, recorder.Body.String(), "working_copy_dirty")
	require.Contains(t, recorder.Body.String(), "保存", "拒绝必须说清下一步怎么做")

	var copy adoptionWorkingCopyRow
	require.NoError(t, store.DB().First(&copy).Error)
	require.Equal(t, outlineExistingBody, copy.BodyMarkdown, "被拒的采纳不得改动工作副本正文")
	require.Equal(t, int64(1), copy.WorkingCopyRevision)
	var versions int64
	require.NoError(t, store.DB().Model(&chapterVersionRow{}).Count(&versions).Error)
	require.Equal(t, int64(1), versions)
	var idem int64
	require.NoError(t, store.DB().Model(&adoptionIdempotencyRow{}).Count(&idem).Error)
	require.Zero(t, idem, "被拒的采纳不得留下幂等行")
}

// TestOutlineAdoptionContinuesWithASnapshotAndLeavesATrail 断言带快照的替换在
// dirty 的情况下可以继续，并且留痕。
//
// 留痕里有三样东西：替换前的工作副本正文、快照标识、这次采纳的目标与动作。
// 人工据此可以把内容恢复回去。
func TestOutlineAdoptionContinuesWithASnapshotAndLeavesATrail(t *testing.T) {
	router, store := outlineAdoptionFixture(t)
	recorder := postOutlineAcceptance(t, router, "outline-adopt-snapshot",
		outlineConfirmationBody(nil), workingCopyBodyHash("用户在编辑器里还没保存的内容"), "snapshot-outline-1")
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())

	var copy adoptionWorkingCopyRow
	require.NoError(t, store.DB().First(&copy).Error)
	require.Equal(t, outlineSkeletonBody, copy.BodyMarkdown)
	require.Equal(t, int64(2), copy.WorkingCopyRevision)

	var audit projectAuditRow
	require.NoError(t, store.DB().Order("created_at DESC").First(&audit).Error)
	require.Equal(t, "project.candidate.adopt", audit.Action)
	require.Contains(t, audit.DetailsJSON, "snapshot-outline-1")
	require.Contains(t, audit.DetailsJSON, outlineExistingBody,
		"替换前的正文必须留在审计详情里，人工据此可恢复")
	require.Contains(t, audit.DetailsJSON, string(writingaction.TargetWorkingCopyWhole))
	require.Contains(t, audit.DetailsJSON, string(writingaction.ActionOutline))
}

// TestOutlineAdoptionIsRefusedWhenTheAuthorizationIsGone 覆盖撤权的第四个时机：
// 采纳之前。
//
// 候选依据的授权没了 ⇒ 采纳被拒，而且**零写入**：没有正式版本、没有幂等行、
// 工作副本一个字都不动。提纲与其它动作走同一条路——撤权查的是来源授权，不是
// 动作。
func TestOutlineAdoptionIsRefusedWhenTheAuthorizationIsGone(t *testing.T) {
	store := newCandidateAdoptionStore(t)
	require.NoError(t, store.DB().AutoMigrate(&adoptionWorkingCopyRow{}))
	seedCandidateAdoptionWorkspace(t, store,
		Chapter{ID: "chapter-1", ProjectID: "project-1", SectionID: "question", Title: "问题"},
		Candidate{
			ID: "candidate-outline", ProjectID: "project-1", ChapterID: "chapter-1", RunID: "run-1",
			BodyMarkdown: outlineSkeletonBody, SourceIDs: []string{"source-1"},
			Basis:    Basis{SpecRevision: 1, TemplateID: "template-demo", TemplateVersion: "1"},
			Validity: "fresh", WritingAction: writingaction.ActionOutline,
			Origin: writingaction.OriginGenerated,
		}, 1)

	revoked := sourcePolicyFunc(func(context.Context, string, string, []string) error { return ErrSourceAccessDenied })
	service := NewCandidateAdoptionService(store, revoked, allowAuthorizer{})
	input := AcceptCandidateInput{
		ProjectID: "project-1", ChapterID: "chapter-1", CandidateID: "candidate-outline", ActorID: "user-1",
		IdempotencyKey: "outline-adoption-revoked-0001", ExpectedSpecRevision: 1,
		ExpectedWorkingCopyRevision: 0, ExpectedBodyHash: workingCopyBodyHash(""),
		Confirmation: outlineConfirmation(nil),
	}
	_, err := service.AcceptCandidate(context.Background(), input)
	require.ErrorIs(t, err, ErrSourceAccessDenied)

	var versionCount int64
	require.NoError(t, store.DB().Model(&chapterVersionRow{}).Count(&versionCount).Error)
	require.Zero(t, versionCount, "撤权后的采纳不得建正式版本")
	var adoptionCount int64
	require.NoError(t, store.DB().Model(&adoptionIdempotencyRow{}).Count(&adoptionCount).Error)
	require.Zero(t, adoptionCount, "撤权后的采纳不得留下幂等行")
}

// TestOutlineAdoptionRequiresTheConfirmationPayload 断言确认载荷必带。
//
// 缺载荷被拒；载荷的动作与候选对不上也被拒——确认了但没确认对东西。
func TestOutlineAdoptionRequiresTheConfirmationPayload(t *testing.T) {
	for _, tc := range []struct {
		name         string
		confirmation any
	}{
		{name: "missing confirmation", confirmation: nil},
		{name: "confirmation made for another action", confirmation: outlineConfirmationBody(
			map[string]any{"writing_action": string(writingaction.ActionGenerate)})},
		{name: "confirmation without a target", confirmation: outlineConfirmationBody(
			map[string]any{"adoption_target": nil})},
		{name: "confirmation without a confirmer", confirmation: outlineConfirmationBody(
			map[string]any{"confirmed_by": nil})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router, store := outlineAdoptionFixture(t)
			recorder := postOutlineAcceptance(t, router, "outline-adopt-"+tc.name,
				tc.confirmation, workingCopyBodyHash(outlineExistingBody), "")
			require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
			require.Contains(t, recorder.Body.String(), "invalid_request")

			var copy adoptionWorkingCopyRow
			require.NoError(t, store.DB().First(&copy).Error)
			require.Equal(t, outlineExistingBody, copy.BodyMarkdown, "被拒的采纳不得改动工作副本正文")
			require.Equal(t, int64(1), copy.WorkingCopyRevision)
			var versions int64
			require.NoError(t, store.DB().Model(&chapterVersionRow{}).Count(&versions).Error)
			require.Equal(t, int64(1), versions)
		})
	}
}
