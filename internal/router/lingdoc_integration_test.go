package router

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	appservice "github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/lingdoc/workspace"
	"github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	_ "modernc.org/sqlite"
)

// Only external tenant/catalog/share lookups are fixtures. JWT issuance and
// validation, token/key repositories, membership, workspace, evidence storage,
// handler and main route registration/guards are the production implementations.
type integrationTenant struct{ interfaces.TenantService }

func (integrationTenant) GetTenantByID(_ context.Context, id uint64) (*types.Tenant, error) {
	return &types.Tenant{ID: id}, nil
}

type integrationKnowledge struct {
	interfaces.KnowledgeBaseService
}

func (integrationKnowledge) GetKnowledgeBaseByIDOnly(context.Context, string) (*types.KnowledgeBase, error) {
	return &types.KnowledgeBase{ID: "shared-kb", TenantID: 2}, nil
}
func (integrationKnowledge) HybridSearch(context.Context, string, types.SearchParams) ([]*types.SearchResult, error) {
	return []*types.SearchResult{}, nil
}

type integrationShares struct {
	interfaces.KBShareService
	allowed bool
}

func (s *integrationShares) CheckTenantKBPermission(context.Context, string, uint64, types.TenantRole) (types.OrgMemberRole, bool, error) {
	return types.OrgRoleViewer, s.allowed, nil
}

func TestLingDocRealAPIThroughMainRegistration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Dialector{DriverName: "sqlite", DSN: filepath.Join(t.TempDir(), "api.db") + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"}, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&types.User{}, &types.AuthToken{}, &types.TenantMember{}, &types.TenantAPIKey{}); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"000018_lingdoc_workspace.up.sql", "000019_lingdoc_evidence_assets.up.sql"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "sqlite", file))
		if err != nil {
			t.Fatal(err)
		}
		for _, stmt := range strings.Split(string(raw), ";") {
			// A migration may end with comments after its last SQL statement.
			// Skip those chunks: modernc returns no result for comment-only SQL.
			hasSQL := false
			for _, line := range strings.Split(stmt, "\n") {
				if trimmed := strings.TrimSpace(line); trimmed != "" && !strings.HasPrefix(trimmed, "--") {
					hasSQL = true
					break
				}
			}
			if hasSQL {
				if err := db.Exec(stmt).Error; err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if err := db.Exec("CREATE TABLE knowledges (id TEXT PRIMARY KEY, tenant_id INTEGER, knowledge_base_id TEXT, title TEXT, parse_status TEXT, file_hash TEXT, file_size INTEGER, processed_at DATETIME, deleted_at DATETIME)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO knowledges (id, tenant_id, knowledge_base_id, title, parse_status, file_hash, file_size, processed_at) VALUES ('knowledge', 2, 'shared-kb', 'synthetic shared source', 'completed', 'hash-v1', 12, ?)", time.Now().UTC()).Error; err != nil {
		t.Fatal(err)
	}
	enabled := true
	cfg := &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enabled}}
	users := apprepo.NewUserRepository(db)
	tokens := apprepo.NewAuthTokenRepository(db)
	members := appservice.NewTenantMemberService(apprepo.NewTenantMemberRepository(db), nil, users, tokens)
	authUsers := appservice.NewUserService(cfg, users, tokens, integrationTenant{}, members, nil)
	jwtTokens := map[string]string{}
	for _, fixture := range []struct {
		id   string
		role types.TenantRole
	}{{"a-owner", types.TenantRoleOwner}, {"b-viewer", types.TenantRoleViewer}, {"c-outsider", types.TenantRoleContributor}} {
		user := &types.User{ID: fixture.id, Username: fixture.id, Email: fixture.id + "@example.invalid", TenantID: 1, IsActive: true}
		if err := users.CreateUser(context.Background(), user); err != nil {
			t.Fatal(err)
		}
		if _, err := members.AddMember(context.Background(), user.ID, 1, fixture.role, nil); err != nil {
			t.Fatal(err)
		}
		token, _, err := authUsers.GenerateTokens(context.Background(), user)
		if err != nil {
			t.Fatal(err)
		}
		jwtTokens[user.ID] = token
	}
	keys := appservice.NewTenantAPIKeyService(apprepo.NewTenantAPIKeyRepository(db))
	full, err := keys.CreateAPIKey(context.Background(), interfaces.TenantAPIKeyCreateRequest{TenantID: 1, Name: "synthetic full key", FullAccess: true})
	if err != nil {
		t.Fatal(err)
	}
	scoped, err := keys.CreateAPIKey(context.Background(), interfaces.TenantAPIKeyCreateRequest{TenantID: 1, Name: "synthetic chat key", Capabilities: []string{string(types.APIKeyCapabilityChat)}})
	if err != nil {
		t.Fatal(err)
	}
	projects := workspacecore.NewService(workspacecore.NewGORMRepository(db))
	shares := &integrationShares{allowed: true}
	ports := workspace.NewGORMSourcePorts(db, shares)
	sources := workspace.NewSourceService(projects, ports.Bindings, ports.Gateway, ports.Catalog, ports.Origins, shares, integrationKnowledge{})
	engine := gin.New()
	engine.Use(middleware.Auth(integrationTenant{}, authUsers, members, keys, cfg))
	v1 := engine.Group("/api/v1")
	guards := &rbacGuards{cfg: cfg, apiKeyAuthorizer: middleware.NewAPIKeyRouteAuthorizer()}
	v1.Use(guards.apiKeyAuthorizer.Middleware())
	registerLingDocWorkspaceRoutes(v1, guards, workspace.NewHandler(workspace.HandlerDependencies{Service: projects, Sources: sources}))
	guards.assertAPIKeyPoliciesMatchRoutes(engine)
	request := func(method, path, body, key, identity string, apiKey bool, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "/api/v1/lingdoc"+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		if apiKey {
			req.Header.Set("X-API-Key", identity)
		} else {
			req.Header.Set("Authorization", "Bearer "+identity)
		}
		res := httptest.NewRecorder()
		engine.ServeHTTP(res, req)
		if res.Code != want {
			t.Fatalf("%s %s: %d %s, want %d", method, path, res.Code, res.Body.String(), want)
		}
		return res
	}
	created := request(http.MethodPost, "/projects", `{"name":"real API synthetic project","template_id":"template-demo"}`, "create-integration", jwtTokens["a-owner"], false, 201)
	var envelope struct{ Data workspacecore.Project }
	if err := json.Unmarshal(created.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	id := envelope.Data.ID
	if id == "" {
		t.Fatal("missing server project ID")
	}
	path := "/projects/" + id
	request(http.MethodGet, path, "", "", jwtTokens["a-owner"], false, 200)
	request(http.MethodGet, path, "", "", jwtTokens["c-outsider"], false, 404)
	request(http.MethodPost, "/projects", `{"name":"denied","template_id":"template-demo"}`, "viewer-create", jwtTokens["b-viewer"], false, 403)
	request(http.MethodGet, path, "", "", "invalid-jwt", false, 401)
	request(http.MethodGet, path, "", "", scoped.Token, true, 403)
	request(http.MethodGet, path, "", "", full.Token, true, 200)
	input := `{"expected_spec_revision":0,"fields":{"research_subject":"synthetic","research_goal":"integration"}}`
	request(http.MethodPut, path+"/spec", input, "save-spec-integration", jwtTokens["a-owner"], false, 200)
	replayed := request(http.MethodPut, path+"/spec", input, "save-spec-integration", jwtTokens["a-owner"], false, 200)
	if !strings.Contains(replayed.Body.String(), `"replayed":true`) {
		t.Fatal("lost-response replay not reported")
	}
	request(http.MethodPut, path+"/spec", input, "save-stale-integration", jwtTokens["a-owner"], false, 409)
	bound := request(http.MethodPost, path+"/assets", `{"knowledge_id":"knowledge"}`, "bind-integration", jwtTokens["a-owner"], false, 201)
	var asset struct{ Data struct{ ID string } }
	if err := json.Unmarshal(bound.Body.Bytes(), &asset); err != nil {
		t.Fatal(err)
	}
	if asset.Data.ID == "" {
		t.Fatal("binding missing real ID")
	}
	retrieval := `{"query":"synthetic","asset_ids":["` + asset.Data.ID + `"]}`
	request(http.MethodPost, path+"/retrieval", retrieval, "", jwtTokens["a-owner"], false, 200)
	shares.allowed = false
	request(http.MethodPost, path+"/retrieval", retrieval, "", jwtTokens["a-owner"], false, 422)
	request(http.MethodPost, path+"/assets", `{"knowledge_id":"knowledge"}`, "bind-integration", jwtTokens["a-owner"], false, 404) // Reauthorization before replay.
	shares.allowed = true
	if err := db.Exec("UPDATE knowledges SET parse_status='processing' WHERE id='knowledge'").Error; err != nil {
		t.Fatal(err)
	}
	request(http.MethodPost, path+"/retrieval", retrieval, "", jwtTokens["a-owner"], false, 422)
	if err := db.Exec("DELETE FROM lingdoc_members WHERE project_id=? AND user_id='a-owner'", id).Error; err != nil {
		t.Fatal(err)
	}
	request(http.MethodPut, path+"/spec", input, "save-spec-integration", jwtTokens["a-owner"], false, 404)
	request(http.MethodGet, path, "", "", full.Token, true, 404) // FullAccess is not project membership.
	var count int64
	if err := db.Table("lingdoc_projects").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("denied write had side effects: %d %v", count, err)
	}
}
