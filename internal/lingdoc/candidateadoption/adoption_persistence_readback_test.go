package candidateadoption

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/lingdoc/writingaction"
)

// 采纳的持久化证据读回（#679）。
//
// 这一组用例盯的不是「采纳请求返回了什么」，而是「采纳**落库之后**，从库里
// 独立读回来的东西是什么」。两者是不同的东西：响应体是写路径在事务里顺手
// 组装出来的对象，独立的读回是另一次查询从物理行里重建出来的对象。两者
// 逐字一致，才说明「采纳成功」这件事真的落到了持久层，而不是只活在响应里。
//
// 现场（一条真实采纳的形状）：
//
//   - 项目 project-1 已经到过第 4 版（project_version = 4）。
//   - 章节 chapter-1 还没有正式版本；它有一份工作副本（修订号 1、正文为空），
//     与章节当前状态一致，所以整段采纳建正式版本是允许的。
//   - 候选 candidate-1 带一条来源与一条待核事项，正文里带对应的来源标记。
//   - 章节 chapter-2 有一份**与本次采纳无关**的章节版本，作为「别的东西没被动过」
//     的对照。
//
// 采纳走的是 HTTP 接缝（POST .../acceptances），读回走的是章节读取路径
// （GET .../chapters 与存储层的 ListChapters），两者不是同一条路：读回不
// 复用采纳响应体。

const (
	readbackProjectID    = "project-1"
	readbackChapterID    = "chapter-1"
	readbackOtherChapter = "chapter-2"
	// readbackOtherProjectID 是另一个项目：采纳只发生在 project-1，它的行必须不变。
	readbackOtherProjectID      = "project-2"
	readbackOtherProjectChapter = "chapter-9"
	readbackCandidateID         = "candidate-1"
	readbackActorID             = "user-1"
	readbackIdemKey             = "readback-adoption-key-0001"

	// readbackCandidateBody 是候选的正文：带一条来源标记，所以正文摘要、来源
	// 列表与工作副本三者都真的有内容可断言。
	readbackCandidateBody = "候选正文 [[source:source-1]]"

	// readbackOtherVersionBody 是无关章节的正文：采纳之后它必须逐字不变。
	readbackOtherVersionBody = "另一个章节的正文，与本次采纳无关"

	// readbackOtherProjectBody 是另一个项目的章节正文：同样必须逐字不变。
	readbackOtherProjectBody = "另一个项目的正文，与本次采纳无关"
)

// readbackFixture 是这一组用例的现场。
type readbackFixture struct {
	dbPath string
	db     *gorm.DB
	store  *SQLiteCandidateAdoptionStore
	router *gin.Engine
}

// newReadbackFixture 造一个「真的落在磁盘上的 SQLite 文件」现场。
//
// 用文件而不是内存库，是因为跨重启那条用例要把 store 关掉再打开：内存库在
// 最后一个连接断开时就没了，文件库才是「停掉进程再起一次」的真实形状。
func newReadbackFixture(t *testing.T) *readbackFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dbPath := filepath.Join(t.TempDir(), "lingdoc-adoption-readback.db")
	db := openReadbackDB(t, dbPath)
	store := NewSQLiteCandidateAdoptionStore(db)
	require.NoError(t, store.AutoMigrate(context.Background()))
	// 审计表与工作副本表在聚焦迁移里不建，但生产迁移会建。这里显式建出来，
	// 免得「审计行」与「工作副本」这两件事被聚焦夹具悄悄省掉。
	require.NoError(t, db.AutoMigrate(&projectAuditRow{}))
	require.NoError(t, db.AutoMigrate(&adoptionWorkingCopyRow{}))

	seedCandidateAdoptionWorkspace(t, store,
		Chapter{ID: readbackChapterID, ProjectID: readbackProjectID, SectionID: "question", Title: "问题"},
		Candidate{
			ID: readbackCandidateID, ProjectID: readbackProjectID, ChapterID: readbackChapterID, RunID: "run-1",
			BodyMarkdown: readbackCandidateBody, SourceIDs: []string{"source-1"},
			Basis:    Basis{SpecRevision: 1, TemplateID: "template-demo", TemplateVersion: "1"},
			Validity: "fresh", WritingAction: writingaction.ActionGenerate, Origin: writingaction.OriginGenerated,
			ReviewItems: []ReviewItem{{ID: "review-1", Statement: "需要人工核查", OriginCandidateID: readbackCandidateID}},
		}, 1)

	// 工作副本：修订号 1、正文为空——空章节的真实状态。
	require.NoError(t, db.Create(&adoptionWorkingCopyRow{
		ProjectID: readbackProjectID, ChapterID: readbackChapterID,
		SpecRevision: 1, WorkingCopyRevision: 1, BodyMarkdown: "",
		SourceIDsJSON: "[]", CitationUsagesJSON: "[]", ReviewItemsJSON: "[]",
	}).Error)

	// 无关章节：它有一份自己的章节版本，作为「别的东西没被动过」的对照。
	require.NoError(t, store.UpsertChapter(context.Background(),
		Chapter{ID: readbackOtherChapter, ProjectID: readbackProjectID, SectionID: "conclusion", Title: "结论",
			CurrentVersionID: stringPtr("version-other")}, 1))
	require.NoError(t, db.Create(&chapterVersionRow{
		ID: "version-other", ProjectID: readbackProjectID, ChapterID: readbackOtherChapter,
		BodyMarkdown: readbackOtherVersionBody, SourceIDsJSON: "[]", CitationUsagesJSON: "[]", ReviewItemsJSON: "[]",
	}).Error)

	// 另一个项目：它有自己的项目行与章节版本行。采纳只发生在 project-1，
	// 所以 project-2 的行必须逐字不变。
	require.NoError(t, store.UpsertProject(context.Background(), readbackOtherProjectID, 7))
	require.NoError(t, store.UpsertChapter(context.Background(),
		Chapter{ID: readbackOtherProjectChapter, ProjectID: readbackOtherProjectID, SectionID: "question", Title: "另一个项目的问题",
			CurrentVersionID: stringPtr("version-other-project")}, 1))
	require.NoError(t, db.Create(&chapterVersionRow{
		ID: "version-other-project", ProjectID: readbackOtherProjectID, ChapterID: readbackOtherProjectChapter,
		BodyMarkdown: readbackOtherProjectBody, SourceIDsJSON: "[]", CitationUsagesJSON: "[]", ReviewItemsJSON: "[]",
	}).Error)

	// 项目版本先落在 4：一次采纳只能把它推到 5。
	require.NoError(t, db.Model(&projectRow{}).Where("id = ?", readbackProjectID).
		Update("project_version", 4).Error)

	allowSources := sourcePolicyFunc(func(context.Context, string, string, []string) error { return nil })
	service := NewCandidateAdoptionService(store, allowSources, allowAuthorizer{})
	handler := NewCandidateAdoptionHandler(service, nil, func(*gin.Context) (string, bool) { return readbackActorID, true })
	router := gin.New()
	RegisterRoutes(router, handler)
	// 章节列表由工作区拥有（见 candidate_adoption_http_test.go），这里在测试路由
	// 上单独挂一条读回用的 GET，读的是同一个 ChapterReader 接缝。
	router.GET("/projects/:projectId/chapters", handler.ListChapters)
	return &readbackFixture{dbPath: dbPath, db: db, store: store, router: router}
}

// openReadbackDB 打开一个磁盘上的 SQLite 文件。
func openReadbackDB(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

// readbackAcceptanceBody 造一次采纳的请求体。
func readbackAcceptanceBody(t *testing.T, confirmation any) *bytes.Reader {
	t.Helper()
	body := map[string]any{
		"candidate_id":                readbackCandidateID,
		"expected_chapter_version_id": nil,
		"expected_spec_revision":      1,
		"replace_existing":            true,
		// 声明的是「用户看到的是哪一份工作副本」：修订号 1、正文为空。
		"expected_working_copy_revision": 1,
		"expected_body_hash":             workingCopyBodyHash(""),
	}
	if confirmation != nil {
		body["confirmation"] = confirmation
	}
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	return bytes.NewReader(encoded)
}

// readbackConfirmation 造一份确认载荷的 JSON 形状。
func readbackConfirmation(t *testing.T) map[string]any {
	t.Helper()
	return adoptionConfirmationBody(t, map[string]any{
		"writing_action":  string(writingaction.ActionGenerate),
		"adoption_target": string(writingaction.TargetFormalVersionWhole),
		"confirmed_by":    readbackActorID,
	})
}

// postReadbackAcceptance 发一次采纳请求。
func postReadbackAcceptance(t *testing.T, router *gin.Engine, key string, confirmation any) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost,
		"/projects/"+readbackProjectID+"/chapters/"+readbackChapterID+"/acceptances",
		readbackAcceptanceBody(t, confirmation))
	request.Header.Set("Idempotency-Key", key)
	router.ServeHTTP(recorder, request)
	return recorder
}

// readbackAcceptanceResponse 是采纳响应体里我们真正用到的那几项。
type readbackAcceptanceResponse struct {
	Data struct {
		CurrentVersionID  *string         `json:"current_version_id"`
		BodyMarkdown      string          `json:"body_markdown"`
		SourceIDs         []string        `json:"source_ids"`
		ReviewItems       []ReviewItem    `json:"review_items"`
		CitationUsages    []CitationUsage `json:"citation_usages"`
		ConfirmationValid bool            `json:"confirmation_valid"`
	} `json:"data"`
	Meta struct {
		Replayed bool `json:"replayed"`
	} `json:"meta"`
}

// decodeReadbackAcceptance 解析采纳响应体。
func decodeReadbackAcceptance(t *testing.T, recorder *httptest.ResponseRecorder) readbackAcceptanceResponse {
	t.Helper()
	var decoded readbackAcceptanceResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &decoded))
	return decoded
}

// readbackChapterHTTP 从章节读取路径读回一个章节。
//
// 这是**独立读回**：另一次 HTTP GET、另一次查询，读到的是物理行重建出来的
// 章节，而不是采纳响应体里那个对象。
func readbackChapterHTTP(t *testing.T, router *gin.Engine, chapterID string) Chapter {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/projects/"+readbackProjectID+"/chapters", nil)
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var decoded struct {
		Data []Chapter `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &decoded))
	for _, chapter := range decoded.Data {
		if chapter.ID == chapterID {
			return chapter
		}
	}
	t.Fatalf("章节读取路径没有返回章节 %s：%s", chapterID, recorder.Body.String())
	return Chapter{}
}

// readbackChapterStore 从存储层的章节读取路径读回一个章节。
func readbackChapterStore(t *testing.T, store *SQLiteCandidateAdoptionStore, chapterID string) Chapter {
	t.Helper()
	chapters, err := store.ListChapters(context.Background(), readbackProjectID)
	require.NoError(t, err)
	for _, chapter := range chapters {
		if chapter.ID == chapterID {
			return chapter
		}
	}
	t.Fatalf("存储层没有返回章节 %s", chapterID)
	return Chapter{}
}

// readbackVersionCount 数一个章节有几个章节版本。
func readbackVersionCount(t *testing.T, db *gorm.DB, chapterID string) int64 {
	t.Helper()
	var count int64
	require.NoError(t, db.Model(&chapterVersionRow{}).Where("chapter_id = ?", chapterID).Count(&count).Error)
	return count
}

// readbackAdoptionCount 数库里有几行采纳记录。
func readbackAdoptionCount(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var count int64
	require.NoError(t, db.Model(&adoptionIdempotencyRow{}).Count(&count).Error)
	return count
}

// readbackAuditRows 读出这次采纳写下的项目审计行。
func readbackAuditRows(t *testing.T, db *gorm.DB) []projectAuditRow {
	t.Helper()
	var rows []projectAuditRow
	require.NoError(t, db.Where("action = ?", "project.candidate.adopt").Order("created_at ASC, id ASC").Find(&rows).Error)
	return rows
}

// readbackProjectVersion 读项目版本号。
func readbackProjectVersion(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var project projectRow
	require.NoError(t, db.Where("id = ?", readbackProjectID).First(&project).Error)
	return project.ProjectVersion
}

// readbackFingerprint 算出库里相关行的内容指纹。
//
// 「零写入」这句话必须能被机器核对：不是「行数没变」，而是**内容**也没变。
// 指纹覆盖候选、章节版本、采纳行、审计行、项目行、章节行与工作副本行。
func readbackFingerprint(t *testing.T, db *gorm.DB) string {
	t.Helper()
	digest := sha256.New()
	write := func(label string, rows any) {
		encoded, err := json.Marshal(rows)
		require.NoError(t, err)
		digest.Write([]byte(label))
		digest.Write(encoded)
	}
	var candidates []candidateRow
	require.NoError(t, db.Order("id ASC").Find(&candidates).Error)
	write("candidates", candidates)
	var versions []chapterVersionRow
	require.NoError(t, db.Order("id ASC").Find(&versions).Error)
	write("chapter_versions", versions)
	var adoptions []adoptionIdempotencyRow
	require.NoError(t, db.Order("id ASC").Find(&adoptions).Error)
	write("adoptions", adoptions)
	var audits []projectAuditRow
	require.NoError(t, db.Order("id ASC").Find(&audits).Error)
	write("audits", audits)
	var projects []projectRow
	require.NoError(t, db.Order("id ASC").Find(&projects).Error)
	write("projects", projects)
	var chapters []chapterRow
	require.NoError(t, db.Order("id ASC").Find(&chapters).Error)
	write("chapters", chapters)
	var copies []adoptionWorkingCopyRow
	require.NoError(t, db.Order("project_id ASC, chapter_id ASC").Find(&copies).Error)
	write("working_copies", copies)
	return hex.EncodeToString(digest.Sum(nil))
}

// TestAdoptionPersistenceIsReadableBackFromTheChapterPath 断言 T1：一次成功采纳
// 之后，从**章节读取路径**独立读回来的章节，与采纳的结果逐字一致；库里恰好一
// 行章节版本、它指向被采纳的候选；采纳行指向那一版；并且有 project.candidate.adopt
// 审计行。
//
// 钉住的不变量：采纳成功 = 落库成功，而不只是「响应里有了」。
func TestAdoptionPersistenceIsReadableBackFromTheChapterPath(t *testing.T) {
	f := newReadbackFixture(t)
	recorder := postReadbackAcceptance(t, f.router, readbackIdemKey, readbackConfirmation(t))
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())

	accepted := decodeReadbackAcceptance(t, recorder)
	require.NotNil(t, accepted.Data.CurrentVersionID, "采纳响应必须给出新的章节版本 id")
	require.False(t, accepted.Meta.Replayed)
	require.Equal(t, readbackCandidateBody, accepted.Data.BodyMarkdown)

	// 独立读回：HTTP 章节读取路径。
	chapter := readbackChapterHTTP(t, f.router, readbackChapterID)
	require.NotNil(t, chapter.CurrentVersionID)
	require.Equal(t, *accepted.Data.CurrentVersionID, *chapter.CurrentVersionID,
		"读回来的当前版本 id 必须与采纳结果逐字一致")
	require.Equal(t, readbackCandidateBody, chapter.BodyMarkdown,
		"读回来的正文必须与采纳结果逐字一致")
	require.Equal(t, []string{"source-1"}, chapter.SourceIDs,
		"读回来的来源列表必须与采纳结果逐字一致")
	require.Len(t, chapter.ReviewItems, 1, "待核事项必须随采纳落库并读得回来")
	require.Equal(t, "review-1", chapter.ReviewItems[0].ID)
	require.Equal(t, "需要人工核查", chapter.ReviewItems[0].Statement)
	require.False(t, chapter.ConfirmationValid, "新版本尚未确认")

	// 独立读回：存储层的章节读取路径（与 HTTP 读回互为对照）。
	stored := readbackChapterStore(t, f.store, readbackChapterID)
	require.Equal(t, chapter.BodyMarkdown, stored.BodyMarkdown)
	require.Equal(t, chapter.SourceIDs, stored.SourceIDs)
	require.Equal(t, chapter.ReviewItems, stored.ReviewItems)
	require.Equal(t, chapter.CurrentVersionID, stored.CurrentVersionID)

	// 物理行：章节版本恰好一行，且指向被采纳的候选。
	require.Equal(t, int64(1), readbackVersionCount(t, f.db, readbackChapterID),
		"一次采纳只建一个章节版本")
	var version chapterVersionRow
	require.NoError(t, f.db.Where("id = ?", *accepted.Data.CurrentVersionID).First(&version).Error)
	require.Equal(t, readbackCandidateID, version.CandidateID,
		"章节版本必须记下它是被哪条候选采纳出来的")
	require.Equal(t, readbackCandidateBody, version.BodyMarkdown)
	require.JSONEq(t, `["source-1"]`, version.SourceIDsJSON)
	require.Contains(t, version.ReviewItemsJSON, "review-1")
	require.False(t, version.ConfirmationValid, "新版本尚未确认")
	require.Equal(t, readbackActorID, version.CreatedBy, "新版本必须记下是谁采纳的")

	// 物理行：采纳行指向那一版。
	require.Equal(t, int64(1), readbackAdoptionCount(t, f.db))
	var adoption adoptionIdempotencyRow
	require.NoError(t, f.db.Where("idempotency_key = ?", readbackIdemKey).First(&adoption).Error)
	require.Equal(t, *accepted.Data.CurrentVersionID, adoption.ChapterVersionID,
		"采纳行必须指向这次采纳产出的章节版本")

	// 物理行：审计行。
	audits := readbackAuditRows(t, f.db)
	require.Len(t, audits, 1, "一次采纳留下且只留下一条 project.candidate.adopt 审计行")
	require.Equal(t, readbackProjectID, audits[0].ProjectID)
	require.Equal(t, readbackActorID, audits[0].ActorID)
	var details map[string]any
	require.NoError(t, json.Unmarshal([]byte(audits[0].DetailsJSON), &details))
	require.Equal(t, readbackCandidateID, details["candidate_id"])
	require.Equal(t, *accepted.Data.CurrentVersionID, details["chapter_version_id"])
}

// TestAdoptionAdvancesTheProjectVersionByExactlyOne 断言 T2：一次采纳把项目
// 版本从 4 推到 5，恰好 +1。
//
// 钉住的不变量：采纳是工作区状态的一次变更，所以它必须留下一次版本推进；
// 推进的幅度只能是 1，多一次或少一次都说明写路径有别的副作用。
func TestAdoptionAdvancesTheProjectVersionByExactlyOne(t *testing.T) {
	f := newReadbackFixture(t)
	require.Equal(t, int64(4), readbackProjectVersion(t, f.db), "夹具现场：项目版本是 4")

	recorder := postReadbackAcceptance(t, f.router, readbackIdemKey, readbackConfirmation(t))
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())

	require.Equal(t, int64(5), readbackProjectVersion(t, f.db),
		"一次采纳只把项目版本推进一格")
}

// TestReplayingTheSameAdoptionKeyWritesNothingNew 断言 T3：同一个幂等键重放
// 仍然是 201 且 meta.replayed = true，而采纳行与章节版本行都**不增加**。
//
// 钉住的不变量：重放是同一次采纳的第二次回答，不是第二次采纳。
func TestReplayingTheSameAdoptionKeyWritesNothingNew(t *testing.T) {
	f := newReadbackFixture(t)
	first := postReadbackAcceptance(t, f.router, readbackIdemKey, readbackConfirmation(t))
	require.Equal(t, http.StatusCreated, first.Code, first.Body.String())
	require.Equal(t, int64(1), readbackVersionCount(t, f.db, readbackChapterID))
	require.Equal(t, int64(1), readbackAdoptionCount(t, f.db))
	versionID := *decodeReadbackAcceptance(t, first).Data.CurrentVersionID
	projectVersion := readbackProjectVersion(t, f.db)

	replay := postReadbackAcceptance(t, f.router, readbackIdemKey, readbackConfirmation(t))
	require.Equal(t, http.StatusCreated, replay.Code, replay.Body.String())
	require.True(t, decodeReadbackAcceptance(t, replay).Meta.Replayed,
		"同键重放必须被标成重放")

	require.Equal(t, int64(1), readbackAdoptionCount(t, f.db), "重放不得新增采纳行")
	require.Equal(t, int64(1), readbackVersionCount(t, f.db, readbackChapterID),
		"重放不得新增章节版本行")
	require.Equal(t, versionID, *decodeReadbackAcceptance(t, replay).Data.CurrentVersionID,
		"重放给出的必须是同一次采纳产出的版本")
	require.Equal(t, projectVersion, readbackProjectVersion(t, f.db),
		"重放不得再次推进项目版本")
}

// TestAcceptanceWithoutAConfirmationWritesNothing 断言 T4：缺确认载荷的请求被
// 拒（400 invalid_request），并且库里**一个字节都没变**。
//
// 钉住的不变量：被拒的采纳不是「部分发生」的采纳。指纹比对让「零写入」不是
// 一句口号：候选、版本、采纳行、审计行、项目行、章节行、工作副本行逐字不变。
func TestAcceptanceWithoutAConfirmationWritesNothing(t *testing.T) {
	f := newReadbackFixture(t)
	before := readbackFingerprint(t, f.db)

	recorder := postReadbackAcceptance(t, f.router, "readback-missing-confirmation", nil)
	require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
	require.Contains(t, recorder.Body.String(), "invalid_request")

	require.Equal(t, before, readbackFingerprint(t, f.db),
		"被拒的采纳不得改动库里任何一行")
	require.Equal(t, int64(0), readbackVersionCount(t, f.db, readbackChapterID))
	require.Equal(t, int64(0), readbackAdoptionCount(t, f.db))
	require.Empty(t, readbackAuditRows(t, f.db))
	require.Equal(t, int64(4), readbackProjectVersion(t, f.db),
		"被拒的采纳不得推进项目版本")
}

// TestAdoptionSurvivesAStoreRestart 断言 T5：同一个 SQLite 文件，关掉 store
// 再重新打开（模拟进程重启）之后，重新读回的东西与采纳后完全一致。
//
// 钉住的不变量：落库是落到了文件里，而不是留在进程内存里。
func TestAdoptionSurvivesAStoreRestart(t *testing.T) {
	f := newReadbackFixture(t)
	recorder := postReadbackAcceptance(t, f.router, readbackIdemKey, readbackConfirmation(t))
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	accepted := decodeReadbackAcceptance(t, recorder)
	versionID := *accepted.Data.CurrentVersionID

	beforeChapter := readbackChapterHTTP(t, f.router, readbackChapterID)
	beforeFingerprint := readbackFingerprint(t, f.db)
	beforeProjectVersion := readbackProjectVersion(t, f.db)

	// 停掉进程：关掉这一进程的数据库连接。
	sqlDB, err := f.db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	// 再起一次进程：同一个文件，重新建一个 store 与路由。
	restarted := openReadbackDB(t, f.dbPath)
	restartStore := NewSQLiteCandidateAdoptionStore(restarted)
	require.NoError(t, restartStore.AutoMigrate(context.Background()))
	allowSources := sourcePolicyFunc(func(context.Context, string, string, []string) error { return nil })
	restartService := NewCandidateAdoptionService(restartStore, allowSources, allowAuthorizer{})
	restartHandler := NewCandidateAdoptionHandler(restartService, nil, func(*gin.Context) (string, bool) { return readbackActorID, true })
	restartRouter := gin.New()
	RegisterRoutes(restartRouter, restartHandler)
	restartRouter.GET("/projects/:projectId/chapters", restartHandler.ListChapters)

	// 重新读回：T1 的断言在这里全部成立。
	chapter := readbackChapterHTTP(t, restartRouter, readbackChapterID)
	require.NotNil(t, chapter.CurrentVersionID)
	require.Equal(t, versionID, *chapter.CurrentVersionID,
		"重启之后读回来的当前版本 id 必须不变")
	require.Equal(t, readbackCandidateBody, chapter.BodyMarkdown,
		"重启之后读回来的正文必须不变")
	require.Equal(t, []string{"source-1"}, chapter.SourceIDs)
	require.Len(t, chapter.ReviewItems, 1)
	require.Equal(t, "review-1", chapter.ReviewItems[0].ID)
	require.Equal(t, beforeChapter.BodyMarkdown, chapter.BodyMarkdown)
	require.Equal(t, beforeChapter.SourceIDs, chapter.SourceIDs)
	require.Equal(t, beforeChapter.ReviewItems, chapter.ReviewItems)

	// 库里的一切也不变。
	require.Equal(t, beforeFingerprint, readbackFingerprint(t, restarted),
		"重启读回不得改变任何一行")
	require.Equal(t, beforeProjectVersion, readbackProjectVersion(t, restarted),
		"重启之后项目版本必须不变")
	require.Equal(t, int64(1), readbackVersionCount(t, restarted, readbackChapterID))
	require.Equal(t, int64(1), readbackAdoptionCount(t, restarted))

	// 工作副本也一致。
	var copy adoptionWorkingCopyRow
	require.NoError(t, restarted.Where("project_id = ? AND chapter_id = ?", readbackProjectID, readbackChapterID).
		First(&copy).Error)
	require.Equal(t, readbackCandidateBody, copy.BodyMarkdown,
		"重启之后工作副本正文必须不变")
	require.Equal(t, int64(2), copy.WorkingCopyRevision,
		"重启之后工作副本修订号必须不变")
}

// TestAdoptionLeavesUnrelatedRowsUntouched 断言 T6：无关数据逐字未变——候选行
// 没被改，无关章节的章节版本没被改。
//
// 钉住的不变量：采纳只动它该动的那几行。「只动该动的」这件事靠行数是测不
// 出来的，得逐字比。
func TestAdoptionLeavesUnrelatedRowsUntouched(t *testing.T) {
	f := newReadbackFixture(t)

	var candidateBefore candidateRow
	require.NoError(t, f.db.Where("id = ?", readbackCandidateID).First(&candidateBefore).Error)
	var otherBefore chapterVersionRow
	require.NoError(t, f.db.Where("id = ?", "version-other").First(&otherBefore).Error)
	var otherProjectBefore projectRow
	require.NoError(t, f.db.Where("id = ?", readbackOtherProjectID).First(&otherProjectBefore).Error)
	var otherProjectVersionBefore chapterVersionRow
	require.NoError(t, f.db.Where("id = ?", "version-other-project").First(&otherProjectVersionBefore).Error)

	recorder := postReadbackAcceptance(t, f.router, readbackIdemKey, readbackConfirmation(t))
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())

	var candidateAfter candidateRow
	require.NoError(t, f.db.Where("id = ?", readbackCandidateID).First(&candidateAfter).Error)
	require.Equal(t, candidateBefore, candidateAfter,
		"采纳不得改动候选行")

	var otherAfter chapterVersionRow
	require.NoError(t, f.db.Where("id = ?", "version-other").First(&otherAfter).Error)
	require.Equal(t, otherBefore, otherAfter,
		"采纳不得改动别的章节的章节版本")

	// 无关章节的章节版本仍然是**唯一**那一行：本次采纳没有给它建版本。
	require.Equal(t, int64(1), readbackVersionCount(t, f.db, readbackOtherChapter),
		"采纳不得给别的章节建版本")

	// 另一个项目：项目行与章节版本行都逐字不变。
	var otherProjectAfter projectRow
	require.NoError(t, f.db.Where("id = ?", readbackOtherProjectID).First(&otherProjectAfter).Error)
	require.Equal(t, otherProjectBefore, otherProjectAfter,
		"采纳不得改动另一个项目的项目行")
	var otherProjectVersionAfter chapterVersionRow
	require.NoError(t, f.db.Where("id = ?", "version-other-project").First(&otherProjectVersionAfter).Error)
	require.Equal(t, otherProjectVersionBefore, otherProjectVersionAfter,
		"采纳不得改动另一个项目的章节版本")
	require.Equal(t, int64(1), readbackVersionCount(t, f.db, readbackOtherProjectChapter),
		"采纳不得给另一个项目的章节建版本")
}
