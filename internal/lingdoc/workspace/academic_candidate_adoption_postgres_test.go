package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Full production PostgreSQL migrations and real services; only the external
// upload bytes and the unrelated sharing service are synthetic fixtures.
func newAcademicPostgresFixture(t *testing.T) academicAdoptionFixture {
	t.Helper()
	dsn := os.Getenv("LINGDOC_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("NOT RUN: LINGDOC_TEST_POSTGRES_DSN is unset")
	}
	root, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	name := "lingdoc_mixed_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	require.NoError(t, root.Exec("CREATE DATABASE "+name).Error)
	t.Cleanup(func() {
		require.NoError(t, root.Exec("DROP DATABASE IF EXISTS "+name+" WITH (FORCE)").Error)
		sqlDB, e := root.DB()
		if e == nil {
			_ = sqlDB.Close()
		}
	})
	require.NoError(t, root.Exec(fmt.Sprintf("ALTER DATABASE %s SET app.skip_embedding = 'true'", name)).Error)
	// The test DSN is a key=value DSN; overriding dbname is compatible with the
	// existing PostgreSQL integration fixture in workspacecore.
	db, err := gorm.Open(postgres.Open(dsn+" dbname="+name), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	files, err := filepath.Glob(filepath.Join("..", "..", "..", "migrations", "versioned", "*.up.sql"))
	require.NoError(t, err)
	require.NotEmpty(t, files)
	for _, path := range files {
		data, e := os.ReadFile(path)
		require.NoError(t, e)
		require.NoErrorf(t, db.Exec(string(data)).Error, "production migration %s", filepath.Base(path))
	}
	exec := func(q string, args ...any) { t.Helper(); require.NoError(t, db.Exec(q, args...).Error) }
	exec("INSERT INTO tenants(id,name,business) VALUES(?,?,?)", 7, "synthetic", "test")
	exec("INSERT INTO tenant_members(tenant_id,user_id,role,status) VALUES(?,?,?,?)", 7, "reader", "owner", "active")
	exec("INSERT INTO lingdoc_projects(id,tenant_id,name,status,project_version,spec_revision,spec_json,template_id,template_version,current_context_revision,template_copy_version) VALUES(?,?,?,?,?,?,?,?,?,?,?)", "project-1", 7, "synthetic", "active", deliveryProjectVersion, deliverySpecRevision, `{"research_subject":"synthetic","research_goal":"test"}`, "template-demo", "1", 1, 1)
	exec("INSERT INTO lingdoc_members(project_id,user_id,role) VALUES(?,?,?)", "project-1", "reader", "owner")
	definition, err := core.ContractDemoTemplate{}.Get("template-demo", "1")
	require.NoError(t, err)
	definition, err = template.WithHashes(definition)
	require.NoError(t, err)
	definitionJSON, err := json.Marshal(definition)
	require.NoError(t, err)
	exec("INSERT INTO lingdoc_project_template_copies(id,project_id,source_template_id,source_template_version,version,status,content_hash,ruleset_hash,definition_json,created_by,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)", "pg-copy", "project-1", "template-demo", "1", 1, core.TemplateCopyBound, definition.ContentHash, definition.RulesetHash, string(definitionJSON), "reader", time.Now().UTC())
	exec("INSERT INTO lingdoc_chapters(id,project_id,section_id,title) VALUES(?,?,?,?)", "chapter-2", "project-1", "method", "synthetic")
	exec("INSERT INTO lingdoc_chapter_versions(id,project_id,chapter_id,body_markdown,source_ids_json,citation_usages_json,review_items_json,spec_revision,confirmation_valid,created_by) VALUES(?,?,?,?,?,?,?,?,?,?)", "version-2", "project-1", "chapter-2", "existing", `["source-1"]`, `[]`, `[]`, deliverySpecRevision, false, "reader")
	exec("UPDATE lingdoc_chapters SET current_version_id = ? WHERE id = ?", "version-2", "chapter-2")
	exec("INSERT INTO lingdoc_working_copies(project_id,chapter_id,base_chapter_version_id,spec_revision,working_copy_revision,body_markdown,source_ids_json,citation_usages_json,review_items_json) VALUES(?,?,?,?,?,?,?,?,?)", "project-1", "chapter-2", "version-2", deliverySpecRevision, 1, "existing", `["source-1"]`, `[]`, `[]`)
	exec("INSERT INTO knowledge_bases(id,tenant_id,name,embedding_model_id,summary_model_id) VALUES(?,?,?,?,?)", "kb-1", 7, "synthetic", "embed-1", "summary-1")
	raw := []byte(sourcePolicyQuote + "\n")
	exec("INSERT INTO knowledges(id,tenant_id,knowledge_base_id,type,title,source,file_type,file_path,file_size,file_hash,parse_status) VALUES(?,?,?,?,?,?,?,?,?,?,?)", "knowledge-1", 7, "kb-1", "file", "synthetic text", "upload", "txt", "resource://synthetic-storage-object", len(raw), "pending", "completed")
	setFixtureUploadHash(t, db, raw)
	exec("INSERT INTO chunks(id,tenant_id,knowledge_id,knowledge_base_id,content,source_content,content_revision,chunk_index,start_at,end_at) VALUES(?,?,?,?,?,?,?,?,?,?)", "source-1", 7, "knowledge-1", "kb-1", sourcePolicyQuote, sourcePolicyQuote, 0, 0, 0, len([]rune(sourcePolicyQuote)))
	exec("INSERT INTO chunk_origin_sha256(chunk_id,tenant_id,knowledge_id,sha256) VALUES(?,?,?,?)", "source-1", 7, "knowledge-1", sha256Hex(raw))
	storage := &adapterStorageStub{data: raw}
	shares := candidateOneConnShares{}
	runtime := NewGORMSourceRuntime(db, ProjectPorts{}, shares, nil, delivery.NewFixedTemplateReader())
	projects := core.NewServiceWithSources(core.NewGORMRepository(db), core.ContractDemoTemplate{}, runtime.WorkspaceSourcePolicy())
	runtime.ConnectProjects(ProjectPorts{Authorizer: projects, Resources: projects, Projects: projects, Context: projects})
	connectAggregateCapabilities(runtime, projects)
	runtime.EnableFullWorkflowPolicy()
	runtime.ConnectOriginFiles(KnowledgeFileOriginReader{Service: storage})
	asset, err := runtime.bindings.Bind(context.Background(), evidence.BindInput{TenantID: 7, ProjectID: "project-1", KnowledgeID: "knowledge-1", KnowledgeBaseID: "kb-1", Title: "synthetic text", CreatedBy: "reader", Signal: evidence.KnowledgeSignal{KnowledgeID: "knowledge-1", ParseStatus: "completed"}})
	require.NoError(t, err)
	require.NoError(t, runtime.bindings.RefreshAsset(context.Background(), "project-1", asset.ID))
	current, err := runtime.bindings.CurrentAsset(context.Background(), "project-1", asset.ID)
	require.NoError(t, err)
	policy := core.ProjectAssetPolicy{ID: asset.ID, ProjectID: "project-1", AssetID: asset.ID, Version: 1, AssetRevision: int64(current.AssetRevision), PolicyVersion: 1, Scope: "project", Purpose: "synthetic test", ModelUseAllowed: true, AllowedUserIDs: []string{}, CreatedBy: "reader", GrantedBy: "reader", Reason: "synthetic test"}
	policyJSON, err := json.Marshal(policy)
	require.NoError(t, err)
	require.NoError(t, core.NewTransactionAdapter(db).(core.ObjectStore).InsertObject(core.StoredObject{ProjectID: "project-1", Kind: core.AssetPolicyKind, ID: asset.ID, Version: 1, Payload: policyJSON}))
	adapter := NewResearchAdapter(db, projects, runtime, shares, storage, nil)
	require.NoError(t, adapter.ConfigureAcademicResearchPolicy(syntheticGlobalAcademicCatalog(), &syntheticResearchBudgetLedger{}))
	projects.ConfigureAgentRuntime(core.AgentRuntime{Sources: adapter, Research: adapter})
	engine := gin.New()
	RegisterAgentRoutes(engine.Group("/api"), AgentRoutePorts{Research: projects, Imports: projects, EvidenceCards: projects, Claims: projects, Anchors: projects, AgentSessions: projects, Proposals: projects, Context: projects, ChangeSets: projects})
	f := academicSnapshotHTTPFixture{db: db, engine: engine, projects: projects, adapter: adapter, runtime: runtime, actor: core.Actor{TenantID: 7, UserID: "reader", Role: types.TenantRoleOwner}, storage: storage, assetID: asset.ID, sourceID: "source-1", raw: raw}
	return buildAcademicAdoptionFixture(t, f)
}

func TestPostgresAcademicMixedCandidateAdoption(t *testing.T) {
	testAcademicCandidateAdoptionWithOneConnection(t, newAcademicPostgresFixture(t), true)
}
