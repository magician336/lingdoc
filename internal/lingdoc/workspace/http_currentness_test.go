package workspace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type allowTestAssetAuthorizer struct{}

func (allowTestAssetAuthorizer) CanAccessAsset(context.Context, evidence.Actor, string, evidence.Asset) (bool, error) {
	return true, nil
}

func newSourceCurrentnessHandler(t *testing.T) (*Handler, *gorm.DB) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "workspace.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql database: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	statements := []string{
		`CREATE TABLE tenant_members (
			tenant_id INTEGER NOT NULL,
			user_id TEXT NOT NULL,
			status TEXT NOT NULL,
			deleted_at DATETIME,
			PRIMARY KEY (tenant_id, user_id)
		)`,
		`CREATE TABLE lingdoc_projects (
			id TEXT PRIMARY KEY,
			tenant_id INTEGER NOT NULL,
			name TEXT NOT NULL,
			status TEXT NOT NULL,
			project_version INTEGER NOT NULL,
			spec_revision INTEGER NOT NULL,
			spec_json TEXT NOT NULL,
			template_id TEXT NOT NULL,
			template_version TEXT NOT NULL,
			created_at DATETIME
		)`,
		`CREATE TABLE lingdoc_members (
			project_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			role TEXT NOT NULL,
			PRIMARY KEY (project_id, user_id)
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
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("create test schema: %v", err)
		}
	}
	if err := db.AutoMigrate(&evidence.ProjectAsset{}, &evidence.ProjectAssetRevision{}); err != nil {
		t.Fatalf("migrate evidence schema: %v", err)
	}
	if err := db.Exec("INSERT INTO tenant_members (tenant_id, user_id, status) VALUES (?, ?, ?)", 7, "reader", "active").Error; err != nil {
		t.Fatalf("seed tenant member: %v", err)
	}
	if err := db.Exec(
		"INSERT INTO lingdoc_projects (id, tenant_id, name, status, project_version, spec_revision, spec_json, template_id, template_version) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		"project-1", 7, "currentness test", "active", 1, 0, "{}", "template-demo", "1",
	).Error; err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if err := db.Exec("INSERT INTO lingdoc_members (project_id, user_id, role) VALUES (?, ?, ?)", "project-1", "reader", "owner").Error; err != nil {
		t.Fatalf("seed project member: %v", err)
	}

	handler := NewHandler(db, nil, nil)
	handler.gateway = evidence.NewAssetGateway(handler.bindings, allowTestAssetAuthorizer{})
	return handler, db
}

func TestGetSourceUsesCurrentAssetAfterRefreshingKnowledgeSignal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, db := newSourceCurrentnessHandler(t)
	ctx := context.Background()
	processedAt := time.Date(2026, time.September, 25, 10, 0, 0, 0, time.UTC)

	asset, err := handler.bindings.Bind(ctx, evidence.BindInput{
		TenantID:        7,
		ProjectID:       "project-1",
		KnowledgeID:     "knowledge-1",
		KnowledgeBaseID: "kb-1",
		Title:           "synthetic PDF",
		CreatedBy:       "reader",
		Signal: evidence.KnowledgeSignal{
			KnowledgeID: "knowledge-1",
			ParseStatus: types.ParseStatusCompleted,
			FileHash:    "hash-before-reparse",
			FileSize:    20,
			ProcessedAt: processedAt.Add(-time.Hour),
		},
	})
	if err != nil {
		t.Fatalf("bind asset: %v", err)
	}

	// Simulate a completed reparse before the first getSource request. The
	// current row is still the same knowledge ID, but its fingerprint advanced.
	if err := db.Exec(
		"INSERT INTO knowledges (id, tenant_id, knowledge_base_id, type, title, file_type, file_size, file_hash, file_path, parse_status, processed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		"knowledge-1", 7, "kb-1", "file", "synthetic PDF", "pdf", 24, "hash-after-reparse", "", types.ParseStatusCompleted, processedAt,
	).Error; err != nil {
		t.Fatalf("seed current knowledge: %v", err)
	}
	const quote = "current synthetic excerpt"
	if err := db.Exec(
		"INSERT INTO chunks (id, tenant_id, knowledge_id, knowledge_base_id, content, content_revision, chunk_index, start_at, end_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		"source-1", 7, "knowledge-1", "kb-1", quote, 0, 0, 0, len([]rune(quote)),
	).Error; err != nil {
		t.Fatalf("seed source chunk: %v", err)
	}

	requestContext := context.WithValue(ctx, types.UserIDContextKey, "reader")
	requestContext = context.WithValue(requestContext, types.TenantIDContextKey, uint64(7))
	request := httptest.NewRequest(http.MethodGet, "/projects/project-1/sources/source-1", nil).WithContext(requestContext)
	recorder := httptest.NewRecorder()
	router := gin.New()
	router.GET("/projects/:projectId/sources/:sourceId", handler.getSource)
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("first getSource after reparse returned %d: %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data struct {
			AssetRevision int    `json:"asset_revision"`
			Status        string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode getSource response: %v", err)
	}
	if response.Data.AssetRevision != 2 {
		t.Fatalf("first getSource returned revision %d, want refreshed revision 2", response.Data.AssetRevision)
	}
	if response.Data.Status != string(evidence.SourceAvailable) {
		t.Fatalf("first getSource status = %q, want %q", response.Data.Status, evidence.SourceAvailable)
	}
	current, err := handler.bindings.CurrentAsset(ctx, "project-1", asset.ID)
	if err != nil {
		t.Fatalf("read current asset: %v", err)
	}
	if current.AssetRevision != 2 {
		t.Fatalf("persisted current revision = %d, want 2", current.AssetRevision)
	}
}
