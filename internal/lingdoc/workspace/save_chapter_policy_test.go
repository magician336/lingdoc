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

	handler := newTestHandler(db, nil, nil)
	authorizer := &flipTestAssetAuthorizer{allow: true}
	// 装配**之后**再换掉 gateway。复核适配器要是装配时抓了一份快照，这一行就白写了：
	// 它注入的东西复核永远看不到，症状是「复核结论与产出侧对不上」。
	testRuntime(handler).gateway = evidence.NewAssetGateway(testRuntime(handler).bindings, authorizer)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), types.UserIDContextKey, "reader")
		ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(7))
		ctx = context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleContributor)
		c.Request = c.Request.WithContext(ctx)
	})
	handler.Register(RouteGroups{Read: router.Group("/api/v1/lingdoc"), Write: router.Group("/api/v1/lingdoc")})
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
		"000026_lingdoc_working_copies.up.sql",
		"000027_lingdoc_selected_rewrites.up.sql",
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
		r