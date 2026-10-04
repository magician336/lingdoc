package workspace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func ensureG3HTTPTables(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS lingdoc_operations (
			tenant_id INTEGER NOT NULL, user_id TEXT NOT NULL, operation TEXT NOT NULL,
			target TEXT NOT NULL, key TEXT NOT NULL, body_hash TEXT NOT NULL,
			response_json TEXT NOT NULL, response_code INTEGER NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (tenant_id, user_id, operation, target, key)
		)`,
		`CREATE TABLE IF NOT EXISTS lingdoc_project_audits (
			id TEXT PRIMARY KEY, project_id TEXT NOT NULL, actor_id TEXT NOT NULL,
			action TEXT NOT NULL, target TEXT NOT NULL, details_json TEXT NOT NULL DEFAULT '{}',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS lingdoc_change_sets (
			id TEXT PRIMARY KEY, project_id TEXT NOT NULL, created_by TEXT NOT NULL,
			reason TEXT NOT NULL, status TEXT NOT NULL, base_context_revision INTEGER NOT NULL,
			target_context_revision INTEGER, base_spec_revision INTEGER NOT NULL,
			target_spec_revision INTEGER, fields_json TEXT NOT NULL DEFAULT '[]',
			impacts_json TEXT NOT NULL DEFAULT '[]', created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			applied_at DATETIME
		)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("seed G3 HTTP table: %v", err)
		}
	}
}

func g3HTTPContext(userID string) context.Context {
	ctx := context.WithValue(context.Background(), types.UserIDContextKey, userID)
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(7))
	return context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleAdmin)
}

func TestG3ChangeSetRoutesUseRealWorkspaceService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, db, assetID := seedBoundSource(t)
	seedDeliveryWorkspace(t, db,
		deliveryQuestionChapter(assetID, currentAssetRevision(t, handler, assetID), deliveryBody, []string{deliverySourceID}),
		deliveryMethodChapter(),
	)
	ensureG3HTTPTables(t, db)
	if err := db.Exec("UPDATE lingdoc_projects SET current_context_revision = ? WHERE id = ?", 1, "project-1").Error; err != nil {
		t.Fatalf("seed context revision: %v", err)
	}

	engine := gin.New()
	handler.Register(RouteGroups{Read: engine.Group("/api"), Write: engine.Group("/api")})
	body := `{"expected_context_revision":1,"fields":{"research_subject":{"old_value":"公开的合成调查样本","new_value":"新的研究对象"}},"affected_chapter_ids":["chapter-1","chapter-2"],"reason":"研究对象发生变化"}`
	request := httptest.NewRequest(http.MethodPost, "/api/projects/project-1/change-sets", strings.NewReader(body)).WithContext(g3HTTPContext("reader"))
	request.Header.Set("Idempotency-Key", "g3-http-create-1")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status = %d; body=%s", response.Code, response.Body.String())
	}
	var created struct {
		Data ChangeSet `json:"data"`
		Meta struct {
			Replayed bool `json:"replayed"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.Data.Status != "assessed" || len(created.Data.Impacts) != 2 || created.Meta.Replayed {
		t.Fatalf("create response = %+v", created)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/projects/project-1/change-sets", strings.NewReader(body)).WithContext(g3HTTPContext("reader"))
	request.Header.Set("Idempotency-Key", "g3-http-create-1")
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("replay status = %d; body=%s", response.Code, response.Body.String())
	}
	var replayed struct {
		Data ChangeSet `json:"data"`
		Meta struct {
			Replayed bool `json:"replayed"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &replayed); err != nil {
		t.Fatalf("decode replay response: %v", err)
	}
	if !replayed.Meta.Replayed || replayed.Data.ID != created.Data.ID {
		t.Fatalf("replay response = %+v, first=%+v", replayed, created)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/projects/project-1/change-sets", nil).WithContext(g3HTTPContext("reader"))
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("list status = %d; body=%s", response.Code, response.Body.String())
	}
	var listed struct {
		Data []ChangeSet `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listed.Data) != 1 || listed.Data[0].ID != created.Data.ID || listed.Data[0].Status != "assessed" {
		t.Fatalf("list response = %+v", listed)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/projects/project-1/change-sets/"+created.Data.ID, nil).WithContext(g3HTTPContext("reader"))
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("get status = %d; body=%s", response.Code, response.Body.String())
	}
	var fetched struct {
		Data ChangeSet `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &fetched); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if fetched.Data.ID != created.Data.ID || fetched.Data.Status != "assessed" || len(fetched.Data.Impacts) != 2 {
		t.Fatalf("get response = %+v", fetched)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/projects/project-1/change-sets/"+created.Data.ID+"/apply", strings.NewReader(`{}`)).WithContext(g3HTTPContext("reader"))
	request.Header.Set("Idempotency-Key", "g3-http-apply-1")
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("apply status = %d; body=%s", response.Code, response.Body.String())
	}
	var applied struct {
		Data ChangeSet `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &applied); err != nil {
		t.Fatalf("decode apply response: %v", err)
	}
	if applied.Data.Status != "applied" || applied.Data.TargetContextRevision == nil || *applied.Data.TargetContextRevision != 2 {
		t.Fatalf("apply response = %+v", applied)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/projects/project-1/change-sets/"+created.Data.ID+"/apply", strings.NewReader(`{}`)).WithContext(g3HTTPContext("reader"))
	request.Header.Set("Idempotency-Key", "g3-http-apply-1")
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("apply replay status = %d; body=%s", response.Code, response.Body.String())
	}
	var appliedReplay struct {
		Data ChangeSet `json:"data"`
		Meta struct {
			Replayed bool `json:"replayed"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &appliedReplay); err != nil {
		t.Fatalf("decode apply replay response: %v", err)
	}
	if !appliedReplay.Meta.Replayed || appliedReplay.Data.ID != applied.Data.ID || appliedReplay.Data.TargetContextRevision == nil || *appliedReplay.Data.TargetContextRevision != 2 {
		t.Fatalf("apply replay response = %+v, first=%+v", appliedReplay, applied)
	}
}
