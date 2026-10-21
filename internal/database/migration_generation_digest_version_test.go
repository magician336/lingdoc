package database

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 这条测试证明「生成运行的摘要版本」迁移在**真实数据库**上真的能升上去、
// 降回来。
//
// 迁移号是共享序列资源：别的面后面还要加迁移。所以这里钉住的是**自己的**
// 版本号，不是「最新的那个」——这样后来再加迁移不会把这条测试碰坏。
const (
	// generationDigestVersionSQLiteVersion 是本迁移在 SQLite 链上的版本号。
	generationDigestVersionSQLiteVersion = 48
	// generationDigestVersionSQLitePrevious 是它在 SQLite 链上的前一个版本号。
	generationDigestVersionSQLitePrevious = 47
	// generationDigestVersionPostgresVersion 是本迁移在 PostgreSQL 链上的版本号。
	generationDigestVersionPostgresVersion = 127
	// generationDigestVersionPostgresPrevious 是它在 PostgreSQL 链上的前一个版本号。
	generationDigestVersionPostgresPrevious = 126
)

// TestSQLiteGenerationDigestVersionMigrationGoesUpAndDown 证明 SQLite 侧的摘要
// 版本迁移可升可降，且历史行带着当前规则版本继续重放。
func TestSQLiteGenerationDigestVersionMigrationGoesUpAndDown(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)
	chdirAndRestore(t, repoRoot)

	dbPath := filepath.Join(t.TempDir(), "generation-digest-version.db")
	migrator := newSQLiteMigrator(t, dbPath)
	require.NoError(t, migrator.Migrate(generationDigestVersionSQLitePrevious))

	db := openSQLiteDB(t, dbPath)
	require.False(t, sqliteColumnExists(t, db, "lingdoc_generation_runs", "digest_version"),
		"迁移之前生成运行表不得带 digest_version 列")
	insertGenerationRunRow(t, db, "run-1")

	require.NoError(t, migrator.Migrate(generationDigestVersionSQLiteVersion))
	version, dirty, err := migrator.Version()
	require.NoError(t, err)
	require.Equal(t, uint(generationDigestVersionSQLiteVersion), version)
	require.False(t, dirty)
	require.True(t, sqliteColumnExists(t, db, "lingdoc_generation_runs", "digest_version"))

	// 历史行带着当前规则版本：它的摘要没变，重放的语义没变。
	var digestVersion int
	require.NoError(t, db.QueryRow("SELECT digest_version FROM lingdoc_generation_runs WHERE id = 'run-1'").
		Scan(&digestVersion))
	require.Equal(t, 1, digestVersion, "历史行必须带着当前规则版本，重放语义不变")

	// 新行也是当前规则版本。
	insertGenerationRunRow(t, db, "run-2")
	require.NoError(t, db.QueryRow("SELECT digest_version FROM lingdoc_generation_runs WHERE id = 'run-2'").
		Scan(&digestVersion))
	require.Equal(t, 1, digestVersion)

	// 回滚：列连同它的值一起消失，别的行不受影响。
	require.NoError(t, migrator.Migrate(generationDigestVersionSQLitePrevious))
	version, dirty, err = migrator.Version()
	require.NoError(t, err)
	require.Equal(t, uint(generationDigestVersionSQLitePrevious), version)
	require.False(t, dirty)
	require.False(t, sqliteColumnExists(t, db, "lingdoc_generation_runs", "digest_version"),
		"回滚之后生成运行表不得再带 digest_version 列")
	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM lingdoc_generation_runs").Scan(&count))
	require.Equal(t, 2, count, "回滚不得动别的行")
}

// TestPostgresGenerationDigestVersionMigrationGoesUpAndDown 证明 PostgreSQL 侧的
// 摘要版本迁移在**真实旧库**上真的能升上去、降回来。
//
// 没有配置 LINGDOC_TEST_POSTGRES_DSN 时明确跳过：跳过不是通过。
func TestPostgresGenerationDigestVersionMigrationGoesUpAndDown(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("LINGDOC_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("NOT RUN: LINGDOC_TEST_POSTGRES_DSN is unset; requires an isolated disposable PostgreSQL server")
	}
	repoRoot := sqliteRepoRoot(t)
	chdirAndRestore(t, repoRoot)

	schema := newPostgresRehearsalSchema(t, dsn)
	migrator := newPostgresMigrator(t, dsn, schema)
	require.NoError(t, migrator.Migrate(generationDigestVersionPostgresPrevious))

	db := openPostgresSchemaDB(t, dsn, schema)
	require.False(t, postgresColumnExists(t, db, schema, "lingdoc_generation_runs", "digest_version"),
		"迁移之前生成运行表不得带 digest_version 列")
	insertGenerationRunPostgresRow(t, db, "run-1")

	require.NoError(t, migrator.Migrate(generationDigestVersionPostgresVersion))
	version, dirty, err := migrator.Version()
	require.NoError(t, err)
	require.Equal(t, uint(generationDigestVersionPostgresVersion), version)
	require.False(t, dirty)
	require.True(t, postgresColumnExists(t, db, schema, "lingdoc_generation_runs", "digest_version"))

	var digestVersion int
	require.NoError(t, db.QueryRow("SELECT digest_version FROM lingdoc_generation_runs WHERE id = $1",
		generationRunPostgresID).Scan(&digestVersion))
	require.Equal(t, 1, digestVersion, "历史行必须带着当前规则版本，重放语义不变")

	// 回滚：列连同它的值一起消失。
	require.NoError(t, migrator.Migrate(generationDigestVersionPostgresPrevious))
	version, dirty, err = migrator.Version()
	require.NoError(t, err)
	require.Equal(t, uint(generationDigestVersionPostgresPrevious), version)
	require.False(t, dirty)
	require.False(t, postgresColumnExists(t, db, schema, "lingdoc_generation_runs", "digest_version"),
		"回滚之后生成运行表不得再带 digest_version 列")
}

// insertGenerationRunRow 写一行生成运行。
func insertGenerationRunRow(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	_, err := db.Exec(
		"INSERT INTO lingdoc_generation_runs "+
			"(id, tenant_id, actor_id, project_id, chapter_id, idempotency_key, request_hash, status, input_json) "+
			"VALUES (?, 1, 'user-1', 'project-1', 'chapter-1', ?, 'digest', 'queued', '{}')",
		id, "key-"+id,
	)
	require.NoError(t, err)
}

// generationRunPostgresID 是用例写入的生成运行 ID（PostgreSQL 侧是 UUID 类型）。
const generationRunPostgresID = "11111111-1111-1111-1111-111111111111"

// insertGenerationRunPostgresRow 写一行生成运行。
//
// PostgreSQL 侧的 id / project_id / chapter_id 是 UUID 类型，input_json 是 JSONB。
func insertGenerationRunPostgresRow(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	_, err := db.Exec(
		"INSERT INTO lingdoc_generation_runs "+
			"(id, tenant_id, actor_id, project_id, chapter_id, idempotency_key, request_hash, status, input_json) "+
			"VALUES ($1, 1, 'user-1', '22222222-2222-2222-2222-222222222222', "+
			"'33333333-3333-3333-3333-333333333333', $2, 'digest', 'queued', '{}'::jsonb)",
		generationRunPostgresID, "key-"+id,
	)
	require.NoError(t, err)
}
