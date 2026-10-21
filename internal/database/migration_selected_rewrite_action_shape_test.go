package database

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 这条测试证明「改写候选的动作形状」迁移在**真实数据库**上真的能升上去、
// 降回来。
//
// 迁移号是共享序列资源：别的面后面还要加迁移。所以这里钉住的是**自己的**
// 版本号，不是「最新的那个」——这样后来再加迁移不会把这条测试碰坏。
const (
	// rewriteActionShapeSQLiteVersion 是本迁移在 SQLite 链上的版本号。
	rewriteActionShapeSQLiteVersion = 47
	// rewriteActionShapeSQLitePrevious 是它在 SQLite 链上的前一个版本号。
	rewriteActionShapeSQLitePrevious = 46
	// rewriteActionShapePostgresVersion 是本迁移在 PostgreSQL 链上的版本号。
	rewriteActionShapePostgresVersion = 126
	// rewriteActionShapePostgresPrevious 是它在 PostgreSQL 链上的前一个版本号。
	rewriteActionShapePostgresPrevious = 125
)

// TestSQLiteSelectedRewriteActionShapeMigrationGoesUpAndDown 证明 SQLite
// 侧的动作形状迁移可升可降，且来源是闭集、动作没有约束。
func TestSQLiteSelectedRewriteActionShapeMigrationGoesUpAndDown(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)
	chdirAndRestore(t, repoRoot)

	dbPath := filepath.Join(t.TempDir(), "rewrite-action-shape.db")
	migrator := newSQLiteMigrator(t, dbPath)
	require.NoError(t, migrator.Migrate(rewriteActionShapeSQLitePrevious))

	db := openSQLiteDB(t, dbPath)
	require.False(t, sqliteColumnExists(t, db, "lingdoc_selected_rewrites", "writing_action"),
		"迁移之前改写候选表不得带 writing_action 列")
	require.False(t, sqliteColumnExists(t, db, "lingdoc_selected_rewrites", "origin"),
		"迁移之前改写候选表不得带 origin 列")

	require.NoError(t, migrator.Migrate(rewriteActionShapeSQLiteVersion))
	version, dirty, err := migrator.Version()
	require.NoError(t, err)
	require.Equal(t, uint(rewriteActionShapeSQLiteVersion), version)
	require.False(t, dirty)

	require.True(t, sqliteColumnExists(t, db, "lingdoc_selected_rewrites", "writing_action"))
	require.True(t, sqliteColumnExists(t, db, "lingdoc_selected_rewrites", "origin"))

	// 来源是闭集：空值（未标注）与三个来源取值都写得进，越界取值被拒。
	insertSelectedRewriteRow(t, db, "rewrite-1", "ai_generated", "rewrite")
	insertSelectedRewriteRow(t, db, "rewrite-2", "", "rewrite")
	_, err = db.Exec(
		"INSERT INTO lingdoc_selected_rewrites " +
			"(id, tenant_id, actor_id, project_id, chapter_id, idempotency_key, request_hash, status, run_mode, " +
			" spec_revision, working_copy_revision, start_utf16, end_utf16, selected_text, instruction, origin) " +
			"VALUES ('rewrite-3', 1, 'user-1', 'project-1', 'chapter-1', 'key-3', 'digest', 'ready', 'mock', " +
			"1, 1, 0, 4, '选段', '说明', 'written_by_hand')",
	)
	require.Error(t, err, "来源取值必须落在既有三值词表里")

	// 动作没有 CHECK：动作词表归 writingaction 独占，随词表增长。
	insertSelectedRewriteRow(t, db, "rewrite-4", "human", "an_action_that_does_not_exist_yet")

	// 回滚：两列连同它们的值一起消失，别的行不受影响。
	require.NoError(t, migrator.Migrate(rewriteActionShapeSQLitePrevious))
	version, dirty, err = migrator.Version()
	require.NoError(t, err)
	require.Equal(t, uint(rewriteActionShapeSQLitePrevious), version)
	require.False(t, dirty)
	require.False(t, sqliteColumnExists(t, db, "lingdoc_selected_rewrites", "writing_action"),
		"回滚之后改写候选表不得再带 writing_action 列")
	require.False(t, sqliteColumnExists(t, db, "lingdoc_selected_rewrites", "origin"),
		"回滚之后改写候选表不得再带 origin 列")
}

// TestPostgresSelectedRewriteActionShapeMigrationGoesUpAndDown 证明 PostgreSQL
// 侧的动作形状迁移在**真实旧库**上真的能升上去、降回来。
//
// 没有配置 LINGDOC_TEST_POSTGRES_DSN 时明确跳过：跳过不是通过。
func TestPostgresSelectedRewriteActionShapeMigrationGoesUpAndDown(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("LINGDOC_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("NOT RUN: LINGDOC_TEST_POSTGRES_DSN is unset; requires an isolated disposable PostgreSQL server")
	}
	repoRoot := sqliteRepoRoot(t)
	chdirAndRestore(t, repoRoot)

	schema := newPostgresRehearsalSchema(t, dsn)
	migrator := newPostgresMigrator(t, dsn, schema)
	require.NoError(t, migrator.Migrate(rewriteActionShapePostgresPrevious))

	db := openPostgresSchemaDB(t, dsn, schema)
	seedSelectedRewritePostgresParents(t, db)
	require.False(t, postgresColumnExists(t, db, schema, "lingdoc_selected_rewrites", "writing_action"),
		"迁移之前改写候选表不得带 writing_action 列")
	require.False(t, postgresColumnExists(t, db, schema, "lingdoc_selected_rewrites", "origin"),
		"迁移之前改写候选表不得带 origin 列")

	require.NoError(t, migrator.Migrate(rewriteActionShapePostgresVersion))
	version, dirty, err := migrator.Version()
	require.NoError(t, err)
	require.Equal(t, uint(rewriteActionShapePostgresVersion), version)
	require.False(t, dirty)

	require.True(t, postgresColumnExists(t, db, schema, "lingdoc_selected_rewrites", "writing_action"))
	require.True(t, postgresColumnExists(t, db, schema, "lingdoc_selected_rewrites", "origin"))
	require.True(t, postgresHasCheckConstraint(t, db, schema, "lingdoc_selected_rewrites", "lingdoc_selected_rewrites_origin_check"),
		"来源是闭集，PostgreSQL 侧必须有 CHECK 约束")
	require.False(t, postgresHasCheckConstraint(t, db, schema, "lingdoc_selected_rewrites", "lingdoc_selected_rewrites_writing_action_check"),
		"动作没有 CHECK：动作词表归 writingaction 独占，随词表增长")

	// 来源是闭集，动作没有约束。
	insertSelectedRewritePostgresRow(t, db, "rewrite-1", "ai_generated", "rewrite")
	insertSelectedRewritePostgresRow(t, db, "rewrite-2", "", "rewrite")
	insertSelectedRewritePostgresRow(t, db, "rewrite-3", "human", "an_action_that_does_not_exist_yet")
	_, err = db.Exec(
		"INSERT INTO lingdoc_selected_rewrites " +
			"(id, tenant_id, actor_id, project_id, chapter_id, idempotency_key, request_hash, status, run_mode, " +
			" spec_revision, working_copy_revision, start_utf16, end_utf16, selected_text, instruction, origin) " +
			"VALUES ('rewrite-4', 1, 'user-1', 'project-1', 'chapter-1', 'key-4', 'digest', 'ready', 'mock', " +
			"1, 1, 0, 4, '选段', '说明', 'written_by_hand')",
	)
	require.Error(t, err, "来源取值必须落在既有三值词表里")

	// 回滚：两列连同它们的值一起消失。
	require.NoError(t, migrator.Migrate(rewriteActionShapePostgresPrevious))
	version, dirty, err = migrator.Version()
	require.NoError(t, err)
	require.Equal(t, uint(rewriteActionShapePostgresPrevious), version)
	require.False(t, dirty)
	require.False(t, postgresColumnExists(t, db, schema, "lingdoc_selected_rewrites", "writing_action"),
		"回滚之后改写候选表不得再带 writing_action 列")
	require.False(t, postgresColumnExists(t, db, schema, "lingdoc_selected_rewrites", "origin"),
		"回滚之后改写候选表不得再带 origin 列")
}

// insertSelectedRewriteRow 写一行改写候选，来源与动作由用例给出。
func insertSelectedRewriteRow(t *testing.T, db *sql.DB, id, origin, action string) {
	t.Helper()
	_, err := db.Exec(
		"INSERT INTO lingdoc_selected_rewrites "+
			"(id, tenant_id, actor_id, project_id, chapter_id, idempotency_key, request_hash, status, run_mode, "+
			" spec_revision, working_copy_revision, start_utf16, end_utf16, selected_text, instruction, origin, writing_action) "+
			"VALUES (?, 1, 'user-1', 'project-1', 'chapter-1', ?, 'digest', 'ready', 'mock', "+
			"1, 1, 0, 4, '选段', '说明', ?, ?)",
		id, "key-"+id, origin, action,
	)
	require.NoError(t, err)
}

// insertSelectedRewritePostgresRow 写一行改写候选，来源与动作由用例给出。
func insertSelectedRewritePostgresRow(t *testing.T, db *sql.DB, id, origin, action string) {
	t.Helper()
	_, err := db.Exec(
		"INSERT INTO lingdoc_selected_rewrites "+
			"(id, tenant_id, actor_id, project_id, chapter_id, idempotency_key, request_hash, status, run_mode, "+
			" spec_revision, working_copy_revision, start_utf16, end_utf16, selected_text, instruction, origin, writing_action) "+
			"VALUES ($1, 1, 'user-1', 'project-1', 'chapter-1', $2, 'digest', 'ready', 'mock', "+
			"1, 1, 0, 4, '选段', '说明', $3, $4)",
		id, "key-"+id, origin, action,
	)
	require.NoError(t, err)
}

// seedSelectedRewritePostgresParents 写下改写候选的外键父行。
func seedSelectedRewritePostgresParents(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(
		"INSERT INTO lingdoc_projects (id, tenant_id, name, status, project_version, spec_revision, template_id, template_version) " +
			"VALUES ('project-1', 1, '动作形状项目', 'active', 3, 7, 'tpl-1', '3')",
	)
	require.NoError(t, err)
	_, err = db.Exec(
		"INSERT INTO lingdoc_chapters (id, project_id, section_id, title) " +
			"VALUES ('chapter-1', 'project-1', 'section-1', '第一节')",
	)
	require.NoError(t, err)
}
