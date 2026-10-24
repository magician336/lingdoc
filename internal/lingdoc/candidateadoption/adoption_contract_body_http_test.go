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

// 契约请求体的 HTTP 层用例（#608）。
//
// F01 契约的采纳节点（F01-11 与它的重投 F01-12）必须与服务端的 HTTP 契约对得
// 上：缺确认载荷的请求体一律 400，放行链在采纳这一步就断了，而且断在契约这一
// 侧——服务端早就在按「确认载荷必填」拒。
//
// 这一层把契约里那份请求体**逐字**发进真的 handler，让「契约写的」与「服务端
// 认的」是同一件事。别的用例证明的是某条规则成立；这一层证明的是契约本身能被
// 服务端接受。

// contractEmptyBodyHash 是契约里那份请求体声明的正文摘要：空章节的工作副本正文
// 是空的，摘要就是正文字节 SHA-256 的小写十六进制。
//
// 它在这里写成字面量而不是调用 workingCopyBodyHash，是为了让这条用例成为
// 「契约与算法一致」的证据：下面那条断言把两者对上，算法改了这里就红。
const contractEmptyBodyHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// contractAcceptanceFixture 造 F01 采纳节点的现场。
//
// 现场：章节还没有正式版本（空章节），工作副本是项目激活时随章节骨架一起建出来
// 的那一行——修订号 1、正文为空。候选是生成端点产出的（动作 generate、来源
// ai_generated），与契约里确认载荷确认的那两件事是同一件。
func contractAcceptanceFixture(t *testing.T) (*gin.Engine, *SQLiteCandidateAdoptionStore) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store := newCandidateAdoptionStore(t)
	require.NoError(t, store.DB().AutoMigrate(&adoptionWorkingCopyRow{}))
	seedCandidateAdoptionWorkspace(t, store,
		Chapter{ID: "chapter-1", ProjectID: "project-1", SectionID: "question", Title: "问题"},
		Candidate{
			ID: "candidate-1", ProjectID: "project-1", ChapterID: "chapter-1", RunID: "run-1",
			BodyMarkdown: "候选正文 [[source:source-1]]", SourceIDs: []string{"source-1"},
			Basis:    Basis{SpecRevision: 1, TemplateID: "template-demo", TemplateVersion: "1"},
			Validity: "fresh", WritingAction: writingaction.ActionGenerate, Origin: writingaction.OriginGenerated,
		}, 1)
	require.NoError(t, store.DB().Create(&adoptionWorkingCopyRow{
		ProjectID: "project-1", ChapterID: "chapter-1",
		SpecRevision: 1, WorkingCopyRevision: 1, BodyMarkdown: "",
		SourceIDsJSON: "[]", CitationUsagesJSON: "[]", ReviewItemsJSON: "[]",
	}).Error)
	allowSources := sourcePolicyFunc(func(context.Context, string, string, []string) error { return nil })
	service := NewCandidateAdoptionService(store, allowSources, allowAuthorizer{})
	handler := NewCandidateAdoptionHandler(service, nil, func(*gin.Context) (string, bool) { return "user-1", true })
	router := gin.New()
	RegisterRoutes(router, handler)
	return router, store
}

// contractAcceptanceBody 造契约里那份请求体。
//
// 值与契约逐字对应：候选、规格修订与章节版本沿用契约的形状；工作副本声明是
// 「修订号 1、正文为空」——这正是空章节在采纳那一刻的真实状态。
func contractAcceptanceBody(t *testing.T, overrides map[string]any) *bytes.Reader {
	t.Helper()
	body := map[string]any{
		"candidate_id":                   "candidate-1",
		"expected_chapter_version_id":    nil,
		"expected_spec_revision":         1,
		"replace_existing":               false,
		"expected_working_copy_revision": 1,
		"expected_body_hash":             contractEmptyBodyHash,
		"confirmation": map[string]any{
			"writing_action":  string(writingaction.ActionGenerate),
			"adoption_target": string(writingaction.TargetFormalVersionWhole),
			"confirmed_at":    "2026-10-24T00:00:00Z",
			"confirmed_by":    "user-1",
			"origin":          string(writingaction.OriginGenerated),
		},
	}
	for key, value := range overrides {
		if value == nil {
			delete(body, key)
			continue
		}
		body[key] = value
	}
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	return bytes.NewReader(encoded)
}

// postContractAcceptance 发一次采纳请求。
func postContractAcceptance(t *testing.T, router *gin.Engine, key string, overrides map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/projects/project-1/chapters/chapter-1/acceptances",
		contractAcceptanceBody(t, overrides))
	request.Header.Set("Idempotency-Key", key)
	router.ServeHTTP(recorder, request)
	return recorder
}

// chapterVersionCount 数库里现在有几个章节版本。
func chapterVersionCount(t *testing.T, store *SQLiteCandidateAdoptionStore) int64 {
	t.Helper()
	var versions int64
	require.NoError(t, store.DB().Model(&chapterVersionRow{}).Count(&versions).Error)
	return versions
}

// TestContractBodyHashIsTheServerAlgorithm 断言契约里写下的那个摘要与服务端算
// 出来的是同一个值。
//
// 这一条是契约与算法之间的那根钉子：算法改了（换编码、换大小写、换截断），这里
// 就红，契约里那个字面量也就不能再被当成「服务端认的那个值」。
func TestContractBodyHashIsTheServerAlgorithm(t *testing.T) {
	require.Equal(t, contractEmptyBodyHash, workingCopyBodyHash(""))
}

// TestAcceptanceEndpointAcceptsTheContractRequestBody 断言契约那份请求体能被
// 服务端接受：空章节采纳成功，且**恰好**产生一个版本。
func TestAcceptanceEndpointAcceptsTheContractRequestBody(t *testing.T) {
	router, store := contractAcceptanceFixture(t)
	recorder := postContractAcceptance(t, router, "contract-body-accept", nil)
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())

	require.Equal(t, int64(1), chapterVersionCount(t, store), "空章节采纳只建一个版本")
	var chapter chapterRow
	require.NoError(t, store.DB().First(&chapter).Error)
	require.NotNil(t, chapter.CurrentVersionID, "采纳要推进章节版本指针")
}

// TestAcceptanceEndpointRefusesTheContractBodyWithoutAConfirmation 断言缺确认
// 载荷的契约请求体被拒，且**零版本**。
//
// 这是 F01-11 原来那份请求体的下场：服务端按「确认载荷必填」拒，没有例外路径。
func TestAcceptanceEndpointRefusesTheContractBodyWithoutAConfirmation(t *testing.T) {
	router, store := contractAcceptanceFixture(t)
	recorder := postContractAcceptance(t, router, "contract-body-no-confirmation",
		map[string]any{"confirmation": nil})
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "invalid_request")

	require.Zero(t, chapterVersionCount(t, store), "被拒的采纳不得留下版本")
	var idem int64
	require.NoError(t, store.DB().Model(&adoptionIdempotencyRow{}).Count(&idem).Error)
	require.Zero(t, idem, "被拒的采纳不得留下幂等行")
	var chapter chapterRow
	require.NoError(t, store.DB().First(&chapter).Error)
	require.Nil(t, chapter.CurrentVersionID, "被拒的采纳不得推进章节版本指针")
}

// TestAcceptanceEndpointRefusesAStaleWorkingCopyRevision 断言过期的工作副本修订
// 号被拒，且零版本。
//
// 修订号对不上说明别人改过这份草稿：服务端要调用者重新读取，而不是拿旧草稿
// 替换用户正在看的内容。
func TestAcceptanceEndpointRefusesAStaleWorkingCopyRevision(t *testing.T) {
	router, store := contractAcceptanceFixture(t)
	recorder := postContractAcceptance(t, router, "contract-body-stale-revision",
		map[string]any{"expected_working_copy_revision": 0})
	require.Equal(t, http.StatusConflict, recorder.Code)
	require.Contains(t, recorder.Body.String(), "version_conflict")

	require.Zero(t, chapterVersionCount(t, store), "被拒的采纳不得留下版本")
	var chapter chapterRow
	require.NoError(t, store.DB().First(&chapter).Error)
	require.Nil(t, chapter.CurrentVersionID, "被拒的采纳不得推进章节版本指针")
}

// TestAcceptanceEndpointRefusesAMismatchedBodyHash 断言正文哈希不匹配被拒，且零
// 版本。
//
// 哈希对不上说明用户编辑器里有没保存的内容，或者草稿已经变了：整段替换会把那
// 一份内容悄悄吃掉，所以服务端先拒。
func TestAcceptanceEndpointRefusesAMismatchedBodyHash(t *testing.T) {
	router, store := contractAcceptanceFixture(t)
	recorder := postContractAcceptance(t, router, "contract-body-dirty-hash",
		map[string]any{"expected_body_hash": workingCopyBodyHash("用户编辑器里还没保存的内容")})
	require.Equal(t, http.StatusConflict, recorder.Code)
	require.Contains(t, recorder.Body.String(), "working_copy_dirty")

	require.Zero(t, chapterVersionCount(t, store), "被拒的采纳不得留下版本")
	var copy adoptionWorkingCopyRow
	require.NoError(t, store.DB().First(&copy).Error)
	require.Equal(t, int64(1), copy.WorkingCopyRevision, "被拒的采纳不得推进工作副本")
	require.Empty(t, copy.BodyMarkdown, "被拒的采纳不得改动工作副本正文")
}

// TestAcceptanceEndpointReplaysTheContractRequestBody 断言同一个幂等键重投契约
// 那份请求体不会产生第二个版本。
//
// 重投的判据是「同一个键 + 同一份请求」：确认时刻不进摘要，所以两次请求可以带
// 同一个确认时刻，而结果必须是同一次采纳。
func TestAcceptanceEndpointReplaysTheContractRequestBody(t *testing.T) {
	router, store := contractAcceptanceFixture(t)

	first := postContractAcceptance(t, router, "contract-body-replay", nil)
	require.Equal(t, http.StatusCreated, first.Code, first.Body.String())

	replay := postContractAcceptance(t, router, "contract-body-replay", nil)
	require.Equal(t, http.StatusCreated, replay.Code, replay.Body.String())
	require.Contains(t, replay.Body.String(), `"replayed":true`)
	require.Contains(t, replay.Body.String(), `"refresh_required":true`)

	require.Equal(t, int64(1), chapterVersionCount(t, store), "同键重投不得写第二版")
}
