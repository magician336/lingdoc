package workspacecore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TD-05 (#730 / Spec #142): the upgrade chain's PostgreSQL half. The SQLite
// side of these guarantees is pinned by
// change_set_upgrade_acceptance_test.go; this file proves the same upgrade
// chain -- atomic apply, restart read-back, idempotent replay and the
// no-partial-writes guarantee -- against a real PostgreSQL database built by
// the production versioned migration chain.
//
// The test is DSN-gated: an unset LINGDOC_TEST_POSTGRES_DSN is explicitly
// NOT RUN (a skip is never a pass -- judgement rules §4.3). It creates one
// disposable database per run and drops it afterwards.

const postgresSkipReason = "NOT RUN: LINGDOC_TEST_POSTGRES_DSN is unset"

// postgresTestDatabase mints one disposable database and returns a DSN to it.
type postgresTestDatabase struct {
	root   *gorm.DB
	rootDB interface{ Close() error }
	name   string
	dsn    string
}

func newPostgresTestDatabase(t *testing.T) *postgresTestDatabase {
	t.Helper()
	dsn := os.Getenv("LINGDOC_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip(postgresSkipReason)
	}
	root, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	rootSQL, err := root.DB()
	if err != nil {
		t.Fatalf("root pool: %v", err)
	}
	name := fmt.Sprintf("lingdoc_upgrade_%d", time.Now().UnixNano())
	if err := root.Exec("DROP DATABASE IF EXISTS " + name).Error; err != nil {
		t.Fatalf("drop stale database: %v", err)
	}
	if err := root.Exec("CREATE DATABASE " + name).Error; err != nil {
		t.Fatalf("create test database: %v", err)
	}
	// The embeddings migration skips itself when this database-level setting is
	// present: a vanilla postgres image has no vector extension, and CI's
	// postgres:17 service takes the same branch (the T139 note records it).
	if err := root.Exec(fmt.Sprintf("ALTER DATABASE %s SET app.skip_embedding = 'true'", name)).Error; err != nil {
		t.Fatalf("set skip_embedding: %v", err)
	}
	t.Cleanup(func() {
		// Drop through a fresh connection: the root pool may already be closed
		// by an earlier cleanup, and a closed pool cannot drop anything.
		dropConn, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		if err != nil {
			t.Errorf("reopen postgres for drop: %v", err)
			_ = rootSQL.Close()
			return
		}
		if err := dropConn.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)").Error; err != nil {
			t.Errorf("drop test database %s: %v", name, err)
		}
		if sqlDB, err := dropConn.DB(); err == nil {
			_ = sqlDB.Close()
		}
		_ = rootSQL.Close()
	})
	return &postgresTestDatabase{root: root, rootDB: rootSQL, name: name, dsn: withPostgresDatabase(dsn, name)}
}

// withPostgresDatabase replaces (or appends) the dbname key of a key=value
// DSN, so the fixture points at its own disposable database.
func withPostgresDatabase(dsn, name string) string {
	trimmed := regexp.MustCompile(`\s*dbname=\S+`).ReplaceAllString(dsn, "")
	trimmed = strings.TrimSpace(trimmed)
	if trimmed == "" {
		return "dbname=" + name
	}
	return trimmed + " dbname=" + name
}

func openSilentPostgres(t *testing.T, dsn string) (*gorm.DB, func()) {
	t.Helper()
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatalf("postgres pool: %v", err)
	}
	return db, func() { _ = pool.Close() }
}

func applyVersionedMigrations(t *testing.T, db *gorm.DB) {
	t.Helper()
	// The FULL versioned chain (core app tables included): the lingdoc-prefixed
	// migrations assume tenants/audit_logs/projects exist, and those come from
	// the chain's own early files. T139's harness hand-built its minimal
	// tables; this fixture rebuilds the whole production schema.
	matches, err := filepath.Glob(filepath.Join("..", "..", "..", "migrations", "versioned", "*.up.sql"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("find versioned migrations: count=%d error=%v", len(matches), err)
	}
	for _, file := range matches {
		migration, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read migration %s: %v", file, err)
		}
		if err := db.Exec(string(migration)).Error; err != nil {
			t.Fatalf("production PostgreSQL migration %s: %v", filepath.Base(file), err)
		}
	}
}

// The membership fixture table the SQLite harness also appends: the versioned
// chain carries the domain tables, tenant membership is wired at composition
// time. PostgreSQL dialect: no DATETIME, BIGINT ids.
const tenantMembersFixtureDDLPostgres = "CREATE TABLE IF NOT EXISTS tenant_members (" +
	"role TEXT NOT NULL DEFAULT 'owner', tenant_id BIGINT NOT NULL, user_id TEXT NOT NULL, " +
	"status TEXT NOT NULL, deleted_at TIMESTAMPTZ, PRIMARY KEY (tenant_id, user_id))"

// TD-05: the upgrade chain on PostgreSQL -- atomic apply, restart read-back,
// idempotent replay, and the audit-failure no-partial-writes guarantee.
func TestPostgresTemplateUpgradeTransactionReplayAndRestartReadBack(t *testing.T) {
	fixture := newPostgresTestDatabase(t)
	pg, closePool := openSilentPostgres(t, fixture.dsn)
	applyVersionedMigrations(t, pg)
	if err := pg.Exec(tenantMembersFixtureDDLPostgres).Error; err != nil {
		t.Fatalf("tenant members fixture: %v", err)
	}
	svc := NewServiceWithSources(NewGORMRepository(pg), ContractDemoTemplate{}, &fakeSources{})

	owner := Actor{TenantID: 309, UserID: "owner"}
	project := seedActiveImpactProject(t, svc, owner)
	_, change, _ := upgradeChangeFixture(t, svc, owner, project.ID, "pg-upgrade-create")

	if _, status, replay, err := svc.ApplyChangeSet(context.Background(), owner, project.ID, change.ID, "pg-upgrade-apply"); err != nil || status != 200 || replay {
		t.Fatalf("apply upgrade on postgres: status=%d replay=%v err=%v", status, replay, err)
	}
	applied, err := svc.GetProject(context.Background(), owner, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if applied.TemplateCopyVersion != project.TemplateCopyVersion+1 || applied.CurrentContextRevision != project.CurrentContextRevision+1 {
		t.Fatalf("postgres apply did not advance the upgrade: %+v", applied)
	}
	tasks := tasksInProject(t, svc, owner, project.ID)
	if len(tasks) == 0 {
		t.Fatal("postgres apply created no ImpactTasks")
	}

	// Restart read-back: drop the pool, reopen a fresh connection, rebuild the
	// service, and verify the applied upgrade is the persisted truth.
	closePool()
	reopened, closeReopened := openSilentPostgres(t, fixture.dsn)
	defer closeReopened()
	restarted := NewServiceWithSources(NewGORMRepository(reopened), ContractDemoTemplate{}, &fakeSources{})
	afterRestart, err := restarted.GetProject(context.Background(), owner, project.ID)
	if err != nil {
		t.Fatalf("restart read-back: %v", err)
	}
	if afterRestart.TemplateCopyVersion != applied.TemplateCopyVersion ||
		afterRestart.CurrentContextRevision != applied.CurrentContextRevision ||
		afterRestart.TemplateCopy.Definition.Fields[0].Label != applied.TemplateCopy.Definition.Fields[0].Label {
		t.Fatalf("restart lost the applied upgrade: %+v -> %+v", applied, afterRestart)
	}

	// Idempotent replay across the restart: the same apply key returns the
	// original result and moves nothing.
	if _, status, replay, err := restarted.ApplyChangeSet(context.Background(), owner, project.ID, change.ID, "pg-upgrade-apply"); err != nil || status != 200 || !replay {
		t.Fatalf("postgres apply replay: status=%d replay=%v err=%v", status, replay, err)
	}
	afterReplay, err := restarted.GetProject(context.Background(), owner, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterReplay.ProjectVersion != afterRestart.ProjectVersion {
		t.Fatalf("replay moved the project: %+v -> %+v", afterRestart, afterReplay)
	}

	// No partial writes: an injected audit failure on a second upgrade rolls
	// the whole transaction back (revision, copy version, tasks, audits).
	secondProject, secondChange, _ := upgradeChangeFixture(t, restarted, owner, project.ID, "pg-upgrade-second")
	before, err := restarted.GetProject(context.Background(), owner, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	tasksBefore := countImpactTasks(t, restarted, owner, project.ID)
	auditsBefore := countApplyAudits(t, restarted, owner, project.ID)
	restarted.InstallFaultInjection(FaultInjection{Point: FaultPointAuditWrite, TenantID: owner.TenantID,
		Action: "project.change_set.apply", Reason: "合成注入：PG 升级 apply 的审计写入不可用"})
	if _, _, _, err := restarted.ApplyChangeSet(context.Background(), owner, project.ID, secondChange.ID, "pg-upgrade-second-apply"); err == nil || !IsInjectedFault(err) {
		t.Fatalf("injected audit failure did not fail the postgres apply: %v", err)
	}
	after, err := restarted.GetProject(context.Background(), owner, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.TemplateCopyVersion != before.TemplateCopyVersion || after.CurrentContextRevision != before.CurrentContextRevision {
		t.Fatalf("postgres apply partially survived the audit failure: %+v -> %+v", before, after)
	}
	if got := countImpactTasks(t, restarted, owner, project.ID); got != tasksBefore {
		t.Fatalf("task count moved on a rolled-back apply: %d -> %d", tasksBefore, got)
	}
	if got := countApplyAudits(t, restarted, owner, project.ID); got != auditsBefore {
		t.Fatalf("audit count moved on a rolled-back apply: %d -> %d", auditsBefore, got)
	}
	_ = secondProject
}
