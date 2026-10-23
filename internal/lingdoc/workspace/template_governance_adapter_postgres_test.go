package workspace

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	governance "github.com/Tencent/WeKnora/internal/lingdoc/templategovernance"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// This runs the production TemplateGovernanceRuntime against PostgreSQL and
// real tenant_members rows. The package-level templategovernance PostgreSQL
// test intentionally uses testAuth for SQL projection only; it is not role
// normalization or production authorization evidence.
func postgresCatalogFixture(t *testing.T) (*gorm.DB, core.Actor, core.Actor) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("LINGDOC_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("NOT RUN: LINGDOC_TEST_POSTGRES_DSN is unset")
	}

	root, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	rootSQL, err := root.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rootSQL.Close() })

	schema := "template_governance_catalog_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := root.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Exec("DROP SCHEMA " + schema + " CASCADE").Error; err != nil {
			t.Error(err)
		}
	})
	db, err := gorm.Open(postgres.Open(dsn+" search_path="+schema), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.Exec("CREATE TABLE tenants (id BIGINT PRIMARY KEY)").Error; err != nil {
		t.Fatal(err)
	}
	migrations, err := filepath.Glob(filepath.Join("..", "..", "..", "migrations", "versioned", "*_lingdoc_*.up.sql"))
	if err != nil || len(migrations) == 0 {
		t.Fatalf("find production LingDoc migrations: count=%d error=%v", len(migrations), err)
	}
	sort.Strings(migrations)
	for _, migration := range migrations {
		raw, err := os.ReadFile(migration)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sqlDB.ExecContext(context.Background(), string(raw)); err != nil {
			t.Fatalf("%s: %v", filepath.Base(migration), err)
		}
	}
	if err := db.Exec(`CREATE TABLE tenant_members (
		tenant_id BIGINT NOT NULL,
		user_id TEXT NOT NULL,
		role TEXT NOT NULL,
		status TEXT NOT NULL,
		deleted_at TIMESTAMPTZ NULL,
		PRIMARY KEY (tenant_id, user_id)
	)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE audit_logs (
		id BIGSERIAL PRIMARY KEY,
		tenant_id BIGINT NOT NULL,
		actor_user_id TEXT NOT NULL,
		actor_role TEXT NOT NULL,
		action TEXT NOT NULL,
		target_type TEXT NOT NULL,
		target_id TEXT NOT NULL,
		outcome TEXT NOT NULL,
		details TEXT NOT NULL,
		scope_type TEXT NOT NULL,
		scope_id TEXT NOT NULL,
		created_at TIMESTAMPTZ NOT NULL
	)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO tenants(id) VALUES (?)", 7).Error; err != nil {
		t.Fatal(err)
	}
	owner := core.Actor{TenantID: 7, UserID: "pg-catalog-owner", Role: types.TenantRoleOwner}
	reader := core.Actor{TenantID: 7, UserID: "pg-catalog-reader", Role: types.TenantRoleViewer}
	for _, member := range []core.Actor{owner, reader} {
		if err := db.Exec("INSERT INTO tenant_members(tenant_id,user_id,role,status) VALUES(?,?,?,'active')", member.TenantID, member.UserID, member.Role).Error; err != nil {
			t.Fatal(err)
		}
	}

	return db, owner, reader
}

func TestPostgresTemplateCatalogVisibilityUsesProductionMembership(t *testing.T) {
	db, owner, reader := postgresCatalogFixture(t)
	projects := core.NewService(core.NewGORMRepository(db))
	runtime, err := NewTemplateGovernanceRuntime(db, projects, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ownerCtx := governanceCallerContext(context.Background(), owner)
	set, fixtures := seedDemoRuleSetFixture(t, runtime, owner, ownerCtx)
	if _, err := runtime.Service().CreateProfile(ownerCtx, governanceActor(owner), "pg-catalog-create-profile", governance.CreateProfileInput{
		ID: template.DemoTemplateID, Name: "PostgreSQL private catalog", Reason: "production membership visibility test",
	}); err != nil {
		t.Fatal(err)
	}
	draft := saveDemoTemplateDraftFixture(t, runtime, owner, set, ownerCtx)
	readerCtx := governanceCallerContext(context.Background(), reader)
	profiles, err := runtime.Service().ListProfiles(readerCtx, governanceActor(reader), governance.CatalogFilter{})
	if err != nil || len(profiles) != 0 {
		t.Fatalf("real viewer should read an empty public catalog while the only version is a draft: profiles=%+v err=%v", profiles, err)
	}
	published, err := runtime.Service().Publish(ownerCtx, governanceActor(owner), "pg-catalog-publish-template", governance.PublishInput{
		TemplateID: draft.TemplateID, Version: draft.Version, ExpectedRevision: draft.Revision,
		ReplayFixtureIDs: fixtures, Reason: "publish exact replay-verified version",
	})
	if err != nil {
		t.Fatal(err)
	}
	profiles, err = runtime.Service().ListProfiles(readerCtx, governanceActor(reader), governance.CatalogFilter{State: template.RuleSetPublished})
	if err != nil || len(profiles) != 1 || profiles[0].ID != published.TemplateID {
		t.Fatalf("real viewer should read only the published profile: profiles=%+v err=%v", profiles, err)
	}
	if _, err := runtime.Service().GetVersion(ownerCtx, governanceActor(owner), published.TemplateID, published.Version); err != nil {
		t.Fatalf("owner cannot read published version through production runtime: %v", err)
	}
}
