package workspace

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// flipTestAssetAuthorizer 是**可翻转**的资料授权判据。不复用 allowTestAssetAuthorizer：
// 那一条假定恒真，而这里要的是同一份绑定先放行、后拒绝——撤权前后走的是同一条复核路径，
// 差别只在网关怎么答。verdict 用来让网关自己也答不出来（那一条路径的结论是 unknown）。
type flipTestAssetAuthorizer struct {
	allow   bool
	verdict error
}

func (a *flipTestAssetAuthorizer) CanAccessAsset(context.Context, evidence.Actor, string, evidence.Asset) (bool, error) {
	return a.allow, a.verdict
}

type saveChapterFixture struct {
	handler    *Handler
	db         *gorm.DB
	authorizer *flipTestAssetAuthorizer
	router     *gin.Engine
}

// newSaveChapterFixture 与 currentness 那条用例共用同一套种子（租户 7 / reader），
// 但 workspace 那几张表走**生产迁移**建：这支用例断言的正是「写进
// lingdoc_chapter_versions.source_ids_json 的东西」，手写 DDL 一旦与迁移分家，
// 测试就会对着一个生产里不存在的 schema 撒谎。
//
// 应用侧的表（tenant_members / knowledges / chunks）不归 lingdoc 迁移管，按需手写。
func newSaveChapterFixture(t *testing.T) saveChapterFixture {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "save-chapter.db")),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql database: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	runLingdocMigrations(t, db)
	for _, statement := range []string{
		`CREATE TABLE tenant_members (
			tenant_id INTEGER NOT NULL,
			user_id TEXT NOT NULL,
			status TEXT NOT NULL,
			deleted_at DATETIME,
			PRIMARY KEY (tenant_id, user_id)
		)`,
		`CREATE TABLE knowledges (
			id TEXT PRIMARY KEY,
			tenant_id INTEGER NOT NULL,
			knowledge_base_id TEXT NOT NULL,
			type TEXT NOT NULL,
			title TEXT NOT NULL,
			file_type TEXT NOT NULL,
			file_size INTEGER NOT NULL,
			file_hash TEXT NOT NULL,
			file_path TEXT NOT NULL,
			parse_status TEXT NOT NULL,
			processed_at DATETIME,
			deleted_at DATETIME
		)`,
		`CREATE TABLE chunks (
			id TEXT PRIMARY KEY,
			tenant_id INTEGER NOT NULL,
			knowledge_id TEXT NOT NULL,
			knowledge_base_id TEXT NOT NULL,
			content TEXT NOT NULL,
			content_revision INTEGER NOT NULL DEFAULT 0,
			chunk_index INTEGER NOT NULL,
			start_at INTEGER NOT NULL,
			end_at INTEGER NOT NULL,
			deleted_at DATETIME
		)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("create application schema: %v", err)
		}
	}
	if err := db.Exec("INSERT INTO tenant_members (tenant_id, user_id, status) VALUES (?, ?, ?)", 7, "reader", "active").Error; err != nil {
		t.Fatalf("seed tenant member: %v", err)
	}

	handler := NewHandler(db, nil, nil)
	authorizer := &flipTestAssetAuthorizer{allow: true}
	// 装配**之后**再换掉 gateway。复核适配器要是装配时抓了一份快照，这一行就白写了：
	// 它注入的东西复核永远看不到，症状是「复核结论与产出侧对不上」。
	handler.gateway = evidence.NewAssetGateway(handler.bindings, authorizer)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), types.UserIDContextKey, "reader")
		ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(7))
		c.Request = c.Request.WithContext(ctx)
	})
	handler.Register(router.Group("/api/v1"))
	return saveChapterFixture{handler: handler, db: db, authorizer: authorizer, router: router}
}

// runLingdocMigrations 逐条执行生产迁移。按 ";" 切分对这几份脚本够用：它们没有触发器、
// 没有函数体，唯一的分号都落在语句末尾。
func runLingdocMigrations(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, name := range []string{
		"000018_lingdoc_workspace.up.sql",
		"000019_lingdoc_evidence_assets.up.sql",
		"000020_lingdoc_candidate_adoption.up.sql",
	} {
		migration, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite", name))
		if err != nil {
			t.Fatal(err)
		}
		for _, statement := range strings.Split(dropSQLLineComments(string(migration)), ";") {
			if strings.TrimSpace(statement) == "" {
				continue
			}
			if err := db.Exec(statement).Error; err != nil {
				t.Fatalf("production SQLite migration %s: %v", name, err)
			}
		}
	}
}

func dropSQLLineComments(script string) string {
	var uncommented strings.Builder
	for _, line := range strings.Split(script, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		uncommented.WriteString(line)
		uncommented.WriteByte('\n')
	}
	return uncommented.String()
}

// bindTestSource 复用 currentness 用例的种子形状：知识行必须先于绑定写入
// （AssetGateway 会刷新资料版本，缺行会被读成「正在删除」）。
func bindTestSource(t *testing.T, db *gorm.DB, bindings *evidence.Bindings, projectID string) {
	t.Helper()
	ctx := context.Background()
	processedAt := time.Date(2026, time.September, 26, 10, 0, 0, 0, time.UTC)

	if err := db.Exec(
		"INSERT INTO knowledges (id, tenant_id, knowledge_base_id, type, title, file_type, file_size, file_hash, file_path, parse_status, processed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		"knowledge-1", 7, "kb-1", "file", "synthetic PDF", "pdf",
		len([]rune(sourcePolicyQuote)), "hash-1", "", types.ParseStatusCompleted, processedAt,
	).Error; err != nil {
		t.Fatalf("seed knowledge: %v", err)
	}

	if _, err := bindings.Bind(ctx, evidence.BindInput{
		TenantID:        7,
		ProjectID:       projectID,
		KnowledgeID:     "knowledge-1",
		KnowledgeBaseID: "kb-1",
		Title:           "synthetic PDF",
		CreatedBy:       "reader",
		Signal: evidence.KnowledgeSignal{
			KnowledgeID: "knowledge-1",
			ParseStatus: types.ParseStatusCompleted,
			FileHash:    "hash-1",
			FileSize:    int64(len([]rune(sourcePolicyQuote))),
			ProcessedAt: processedAt,
		},
	}); err != nil {
		t.Fatalf("bind asset: %v", err)
	}
	seedChunk(t, db, "source-1", "knowledge-1", sourcePolicyQuote, 0, 0, len([]rune(sourcePolicyQuote)))
}

func doJSON(t *testing.T, router *gin.Engine, method, path, key string, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		payload = strings.NewReader(string(encoded))
	}
	request := httptest.NewRequest(method, path, payload)
	request.Header.Set("Content-Type", "application/json")
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	decoded := map[string]any{}
	if recorder.Body.Len() > 0 {
		if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("decode %s %s response: %v (%s)", method, path, err, recorder.Body.String())
		}
	}
	return recorder, decoded
}

func nested(t *testing.T, body map[string]any, keys ...string) any {
	t.Helper()
	var current any = body
	for _, key := range keys {
		object, ok := current.(map[string]any)
		if !ok {
			t.Fatalf("response has no object at %v: %v", keys, body)
		}
		current = object[key]
	}
	return current
}

func equalStrings(raw any, want ...string) bool {
	items, ok := raw.([]any)
	if !ok || len(items) != len(want) {
		return false
	}
	for index, item := range items {
		if item != want[index] {
			return false
		}
	}
	return true
}

func TestSaveChapterAcceptsCitationsAndRechecksBeforeReplay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fixture := newSaveChapterFixture(t)

	// 走一遍 T07 的真实入口：新建 → 填研究条件 → 激活。章节由激活时按模板铺出来。
	created, createdBody := doJSON(t, fixture.router, http.MethodPost, "/api/v1/lingdoc/projects", "sc-create-1",
		map[string]string{"name": "引用复核", "template_id": "template-demo"})
	if created.Code != http.StatusCreated {
		t.Fatalf("create project: %d %s", created.Code, created.Body.String())
	}
	projectID, _ := nested(t, createdBody, "data", "id").(string)
	if projectID == "" {
		t.Fatalf("create project returned no id: %s", created.Body.String())
	}
	chaptersPath := "/api/v1/lingdoc/projects/" + projectID + "/chapters"

	if recorder, _ := doJSON(t, fixture.router, http.MethodPut, "/api/v1/lingdoc/projects/"+projectID+"/spec", "sc-spec-1",
		map[string]any{"expected_spec_revision": 0, "fields": map[string]string{"research_subject": "样本", "research_goal": "验证"}},
	); recorder.Code != http.StatusOK {
		t.Fatalf("save spec: %d %s", recorder.Code, recorder.Body.String())
	}
	if recorder, _ := doJSON(t, fixture.router, http.MethodPost, "/api/v1/lingdoc/projects/"+projectID+"/activate", "sc-activate-1",
		map[string]any{"expected_spec_revision": 1},
	); recorder.Code != http.StatusOK {
		t.Fatalf("activate: %d %s", recorder.Code, recorder.Body.String())
	}

	bindTestSource(t, fixture.db, fixture.handler.bindings, projectID)

	_, chapters := doJSON(t, fixture.router, http.MethodGet, chaptersPath, "", nil)
	items, ok := nested(t, chapters, "data").([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("chapters: %v", chapters)
	}
	first, _ := items[0].(map[string]any)
	chapterID, _ := first["id"].(string)
	if chapterID == "" {
		t.Fatalf("chapter has no id: %v", first)
	}
	versionsPath := chaptersPath + "/" + chapterID + "/versions"

	cited := map[string]any{
		"expected_chapter_version_id": nil, "expected_spec_revision": 1,
		"body_markdown": "[[source:source-1]] 的摘录", "source_ids": []string{"source-1"},
	}
	saved, savedBody := doJSON(t, fixture.router, http.MethodPost, versionsPath, "sc-cite-1", cited)
	if saved.Code != http.StatusCreated {
		t.Fatalf("cite a bound source: %d %s", saved.Code, saved.Body.String())
	}
	if got := nested(t, savedBody, "data", "source_ids"); !equalStrings(got, "source-1") {
		t.Fatalf("response lost the citation: %v", got)
	}
	versionID, _ := nested(t, savedBody, "data", "current_version_id").(string)
	if versionID == "" {
		t.Fatalf("cited chapter has no version: %s", saved.Body.String())
	}

	// 读回来的章节也得带着引用——落库的是真值，不再是写死的 "[]"。
	_, listed := doJSON(t, fixture.router, http.MethodGet, chaptersPath, "", nil)
	listedItems, _ := nested(t, listed, "data").([]any)
	listedFirst, _ := listedItems[0].(map[string]any)
	if !equalStrings(listedFirst["source_ids"], "source-1") {
		t.Fatalf("stored citation not read back: %v", listedFirst["source_ids"])
	}

	// 撤销资料授权，再提交一份**新的**带引用正文：复核必须在写之前把它挡回去。
	fixture.authorizer.allow = false
	denied, deniedBody := doJSON(t, fixture.router, http.MethodPost, versionsPath, "sc-cite-2", map[string]any{
		"expected_chapter_version_id": versionID, "expected_spec_revision": 1,
		"body_markdown": "改写后的 [[source:source-1]] 摘录", "source_ids": []string{"source-1"},
	})
	if denied.Code != http.StatusForbidden {
		t.Fatalf("revoked source accepted: %d %s", denied.Code, denied.Body.String())
	}
	if code, _ := nested(t, deniedBody, "error", "code").(string); code != "source_access_denied" {
		t.Fatalf("revoked source answered %q: %s", code, denied.Body.String())
	}
	var chapter struct {
		CurrentVersionID *string `gorm:"column:current_version_id"`
	}
	if err := fixture.db.Table("lingdoc_chapters").Where("id = ?", chapterID).Scan(&chapter).Error; err != nil {
		t.Fatal(err)
	}
	if chapter.CurrentVersionID == nil || *chapter.CurrentVersionID != versionID {
		t.Fatalf("rejected save moved the chapter to %v, want %s", chapter.CurrentVersionID, versionID)
	}

	// 拿第一枚操作键原样重放：授权已经没了，重放不能把那次写下的正文换回来。
	replay, replayBody := doJSON(t, fixture.router, http.MethodPost, versionsPath, "sc-cite-1", cited)
	if replay.Code != http.StatusForbidden {
		t.Fatalf("replay bypassed the recheck: %d %s", replay.Code, replay.Body.String())
	}
	if code, _ := nested(t, replayBody, "error", "code").(string); code != "source_access_denied" {
		t.Fatalf("replay answered %q: %s", code, replay.Body.String())
	}
	// 错误响应里没有 data，更没有重放标记——重放要是被服务了，形状会是 201 + data。
	if _, present := replayBody["data"]; present {
		t.Fatalf("a replayed payload was served for a revoked source: %s", replay.Body.String())
	}
}

// TestSaveChapterWithoutPolicyAnswersServiceUnavailable 钉住「装配漏了端口」在传输层的答法：
// 不是 403（那会让一次装配失误看着像用户的权限出了问题），而是可重试的 503。
func TestSaveChapterWithoutPolicyAnswersServiceUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fixture := newSaveChapterFixture(t)

	created, createdBody := doJSON(t, fixture.router, http.MethodPost, "/api/v1/lingdoc/projects", "np-create-1",
		map[string]string{"name": "漏装端口", "template_id": "template-demo"})
	if created.Code != http.StatusCreated {
		t.Fatalf("create project: %d %s", created.Code, created.Body.String())
	}
	projectID, _ := nested(t, createdBody, "data", "id").(string)
	chaptersPath := "/api/v1/lingdoc/projects/" + projectID + "/chapters"

	if recorder, _ := doJSON(t, fixture.router, http.MethodPut, "/api/v1/lingdoc/projects/"+projectID+"/spec", "np-spec-1",
		map[string]any{"expected_spec_revision": 0, "fields": map[string]string{"research_subject": "样本", "research_goal": "验证"}},
	); recorder.Code != http.StatusOK {
		t.Fatalf("save spec: %d %s", recorder.Code, recorder.Body.String())
	}
	if recorder, _ := doJSON(t, fixture.router, http.MethodPost, "/api/v1/lingdoc/projects/"+projectID+"/activate", "np-activate-1",
		map[string]any{"expected_spec_revision": 1},
	); recorder.Code != http.StatusOK {
		t.Fatalf("activate: %d %s", recorder.Code, recorder.Body.String())
	}
	_, chapters := doJSON(t, fixture.router, http.MethodGet, chaptersPath, "", nil)
	items, _ := nested(t, chapters, "data").([]any)
	if len(items) == 0 {
		t.Fatalf("chapters: %v", chapters)
	}
	first, _ := items[0].(map[string]any)
	chapterID, _ := first["id"].(string)

	// 把网关抽掉：复核要问的那台判定器没了，装配该答 503 而不是放行。
	fixture.handler.gateway = nil
	recorder, body := doJSON(t, fixture.router, http.MethodPost, chaptersPath+"/"+chapterID+"/versions", "np-cite-1",
		map[string]any{"expected_chapter_version_id": nil, "expected_spec_revision": 1,
			"body_markdown": "[[source:source-1]] 的摘录", "source_ids": []string{"source-1"}})
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing dependency answered %d: %s", recorder.Code, recorder.Body.String())
	}
	if code, _ := nested(t, body, "error", "code").(string); code != "dependency_unavailable" {
		t.Fatalf("missing dependency answered %q: %s", code, recorder.Body.String())
	}
	if retryable, _ := nested(t, body, "error", "retryable").(bool); !retryable {
		t.Fatalf("dependency_unavailable must be retryable: %s", recorder.Body.String())
	}
}
