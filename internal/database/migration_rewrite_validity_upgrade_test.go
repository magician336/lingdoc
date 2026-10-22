package database

// 本文件在**真实旧库**上演练选段候选的失效状态迁移（PF-2/9 交付的
// migrations/versioned/000129 与 migrations/sqlite/000050）。
//
// 迁移把选段候选的自带状态词表换成候选层的失效枚举。这件事只有脚本写得出来
// 是不够的：SQLite 侧靠临时表重建，任何一次重建都可能悄悄丢掉索引或外键，
// 而这要等到下一个查询真的需要它才会显形。所以这里在旧库上真的升一次、降一次，
// 断言行数、主键、索引、正文与状态翻译全都对得上。
//
// PostgreSQL 侧只在 LINGDOC_TEST_POSTGRES_DSN 指向隔离测试库时运行。没有配置
// 时明确跳过——跳过不是通过。

import (
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	// legacyRewriteValiditySQLiteVersion 是失效状态迁移之前的 SQLite 版本号。
	legacyRewriteValiditySQLiteVersion = 49
	// rewriteValiditySQLiteVersion 是失效状态迁移之后的 SQLite 版本号。
	rewriteValiditySQLiteVersion = 50
	// legacyRewriteValidityPostgresVersion 是迁移之前的 PostgreSQL 版本号。
	legacyRewriteValidityPostgresVersion = 128
	// rewriteValidityPostgresVersion 是迁移之后的 PostgreSQL 版本号。
	rewriteValidityPostgresVersion = 129
)

// legacyRewriteRow 是旧库里的一条选段候选。
type legacyRewriteRow struct {
	// ID 是候选标识。
	ID string
	// Status 是旧词表里的状态词。
	Status string
	// Body 是候选正文。
	Body string
}

// legacyRewriteRows 是旧库形态里必须活过迁移的候选。
//
// 三种状态各一行：可采纳的、输入变了��以及一次失败。失败的那一行没有可采纳的
// 产出，迁移后它带空状态，失败原因留在 error_code 里。
var legacyRewriteRows = []legacyRewriteRow{
	{ID: "rewrite-ready", Status: "ready", Body: "旧库里的改写正文第一段，含中文。"},
	{ID: "rewrite-stale", Status: "stale", Body: "旧库里的改写正文第二段。"},
	{ID: "rewrite-failed", Status: "failed", Body: ""},
}

// rewriteValidityByStatus 是旧状态词到新失效状态的翻译表。
var rewriteValidityByStatus = map[string]string{
	"ready":          "fresh",
	"stale":          "stale",
	"failed":         "",
	"queued":         "",
	"not_authorized": "not_authorized",
}

// rewriteShapeSnapshot 是一次迁移前后必须保持一致的那些东西。
type rewriteShapeSnapshot struct {
	// RowCount 是选段候选表的行数。
	RowCount int
	// Keys 是每一行的主键取值（有序）。
	Keys []string
	// Indexes 是表上的索引名（有序）。
	Indexes []string
	// Bodies 是每一行的正文（按主键取值）。
	Bodies map[string]string
	// States 是每一行的状态（按主键取值）。
	States map[string]string
}

// assertRewriteShapeCarriedOver 断言一次迁移把选段候选表原样搬了过去。
func assertRewriteShapeCarriedOver(t *testing.T, before, after rewriteShapeSnapshot) {
	t.Helper()

	require.Equal(t, before.RowCount, after.RowCount, "迁移不得改变选段候选表的行数")
	require.Equal(t, before.Keys, after.Keys, "迁移不得改变选段候选表的主键取值")
	require.Equal(t, before.Bodies, after.Bodies, "迁移不得改动选段候选的正文")
	require.Contains(t, after.Indexes, "idx_lingdoc_selected_rewrites_chapter",
		"迁移不得丢掉选段候选表上的章节索引")
}

// snapshotSQLiteRewriteShape 读出 SQLite 选段候选表的形状与内容。
func snapshotSQLiteRewriteShape(t *testing.T, db *sql.DB, stateColumn string) rewriteShapeSnapshot {
	t.Helper()
	snapshot := rewriteShapeSnapshot{Bodies: map[string]string{}, States: map[string]string{}}

	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM lingdoc_selected_rewrites").Scan(&snapshot.RowCount))

	rows, err := db.Query(
		"SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = 'lingdoc_selected_rewrites' ORDER BY name",
	)
	require.NoError(t, err)
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		snapshot.Indexes = append(snapshot.Indexes, name)
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())

	rows, err = db.Query(
		"SELECT id, replacement_markdown, " + stateColumn + " FROM lingdoc_selected_rewrites ORDER BY id",
	)
	require.NoError(t, err)
	for rows.Next() {
		var id, body, state string
		require.NoError(t, rows.Scan(&id, &body, &state))
		snapshot.Keys = append(snapshot.Keys, id)
		snapshot.Bodies[id] = body
		snapshot.States[id] = state
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	sort.Strings(snapshot.Keys)

	return snapshot
}

// seedLegacyRewriteDatabase 把旧库形态的数据写进旧库。
func seedLegacyRewriteDatabase(t *testing.T, db *sql.DB, placeholder string) {
	t.Helper()

	_, err := db.Exec(
		"INSERT INTO lingdoc_projects (id, tenant_id, name, status, project_version, spec_revision, template_id, template_version) " +
			"VALUES ('project-1', 1, '旧库项目', 'active', 3, 7, 'tpl-1', '3')",
	)
	require.NoError(t, err)
	_, err = db.Exec(
		"INSERT INTO lingdoc_chapters (id, project_id, section_id, title) " +
			"VALUES ('chapter-1', 'project-1', 'section-1', '第一节')",
	)
	require.NoError(t, err)

	for _, row := range legacyRewriteRows {
		_, err = db.Exec(
			"INSERT INTO lingdoc_selected_rewrites "+
				"(id, tenant_id, actor_id, project_id, chapter_id, idempotency_key, request_hash, status, run_mode, "+
				"spec_revision, working_copy_revision, start_utf16, end_utf16, selected_text, instruction, replacement_markdown) "+
				"VALUES ("+placeholder+"1, 1, 'user-1', 'project-1', 'chapter-1', "+placeholder+"2, 'hash', "+
				placeholder+"3, 'mock', 7, 3, 0, 4, '选区', '改写这段', "+placeholder+"4)",
			row.ID, "key-"+row.ID, row.Status, row.Body,
		)
		require.NoError(t, err)
	}
}

// TestSQLiteLegacyRewriteValidityUpgradeAndRollback 证明 SQLite 侧的失效状态
// 迁移在**真实旧库**上能升上去、降回来，而且不丢任何东西。
func TestSQLiteLegacyRewriteValidityUpgradeAndRollback(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)
	chdirAndRestore(t, repoRoot)

	dbPath := filepath.Join(t.TempDir(), "legacy-rewrite.db")
	migrator := newSQLiteMigrator(t, dbPath)

	// 旧库：停在迁移之前的版本号上，不是新建的空库。
	require.NoError(t, migrator.Migrate(legacyRewriteValiditySQLiteVersion))
	version, dirty, err := migrator.Version()
	require.NoError(t, err)
	require.Equal(t, uint(legacyRewriteValiditySQLiteVersion), version)
	require.False(t, dirty)

	db := openSQLiteDB(t, dbPath)
	seedLegacyRewriteDatabase(t, db, "?")

	before := snapshotSQLiteRewriteShape(t, db, "status")
	require.Equal(t, len(legacyRewriteRows), before.RowCount)
	require.NotEmpty(t, before.Indexes, "旧库的选段候选表上必须已经有索引")

	// ---- 升级 ----
	require.NoError(t, migrator.Steps(1))
	version, dirty, err = migrator.Version()
	require.NoError(t, err)
	require.Equal(t, uint(rewriteValiditySQLiteVersion), version)
	require.False(t, dirty)

	afterUp := snapshotSQLiteRewriteShape(t, db, "validity")
	assertRewriteShapeCarriedOver(t, before, afterUp)
	for _, row := range legacyRewriteRows {
		require.Equal(t, rewriteValidityByStatus[row.Status], afterUp.States[row.ID],
			"旧状态词 %q 必须翻译成失效状态", row.Status)
	}
	require.True(t, sqliteColumnExists(t, db, "lingdoc_selected_rewrites", "validity"),
		"升级之后选段候选表必须带 validity 列")
	require.False(t, sqliteColumnExists(t, db, "lingdoc_selected_rewrites", "status"),
		"升级之后旧状态列必须消失")

	// ---- 回滚 ----
	require.NoError(t, migrator.Steps(-1))
	version, dirty, err = migrator.Version()
	require.NoError(t, err)
	require.Equal(t, uint(legacyRewriteValiditySQLiteVersion), version)
	require.False(t, dirty)

	afterDown := snapshotSQLiteRewriteShape(t, db, "status")
	assertRewriteShapeCarriedOver(t, before, afterDown)
	for _, row := range legacyRewriteRows {
		expected := row.Status
		if expected == "failed" {
			expected = "queued"
		}
		require.Equal(t, expected, afterDown.States[row.ID], "回滚之后旧状态词必须回到旧词表")
	}
	require.False(t, sqliteColumnExists(t, db, "lingdoc_selected_rewrites", "validity"),
		"回滚之后 validity 列必须消失")
}

// TestPostgresLegacyRewriteValidityUpgradeAndRollback 证明 PostgreSQL 侧的失效
// 状态迁移在**真实旧库**上能升上去、降回来。
//
// 没有配置 LINGDOC_TEST_POSTGRES_DSN 时明确跳过：跳过不是通过。
func TestPostgresLegacyRewriteValidityUpgradeAndRollback(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("LINGDOC_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("NOT RUN: LINGDOC_TEST_POSTGRES_DSN is unset; requires an isolated disposable PostgreSQL server")
	}
	repoRoot := sqliteRepoRoot(t)
	chdirAndRestore(t, repoRoot)

	schema := newPostgresRehearsalSchema(t, dsn)
	migrator := newPostgresMigrator(t, dsn, schema)

	require.NoError(t, migrator.Migrate(legacyRewriteValidityPostgresVersion))
	version, dirty, err := migrator.Version()
	require.NoError(t, err)
	require.Equal(t, uint(legacyRewriteValidityPostgresVersion), version)
	require.False(t, dirty)

	db := openPostgresSchemaDB(t, dsn, schema)
	seedLegacyRewriteDatabase(t, db, "$")

	// ---- 升级 ----
	require.NoError(t, migrator.Steps(1))
	version, dirty, err = migrator.Version()
	require.NoError(t, err)
	require.Equal(t, uint(rewriteValidityPostgresVersion), version)
	require.False(t, dirty)

	for _, row := range legacyRewriteRows {
		var state string
		require.NoError(t, db.QueryRow(
			"SELECT validity FROM lingdoc_selected_rewrites WHERE id = $1", row.ID,
		).Scan(&state))
		require.Equal(t, rewriteValidityByStatus[row.Status], state,
			"旧状态词 %q 必须翻译成失效状态", row.Status)
	}
	require.True(t, postgresColumnExists(t, db, schema, "lingdoc_selected_rewrites", "validity"),
		"升级之后选段候选表必须带 validity 列")
	require.False(t, postgresColumnExists(t, db, schema, "lingdoc_selected_rewrites", "status"),
		"升级之后旧状态列必须消失")

	// ---- 回滚 ----
	require.NoError(t, migrator.Steps(-1))
	version, dirty, err = migrator.Version()
	require.NoError(t, err)
	require.Equal(t, uint(legacyRewriteValidityPostgresVersion), version)
	require.False(t, dirty)

	for _, row := range legacyRewriteRows {
		var state string
		require.NoError(t, db.QueryRow(
			"SELECT status FROM lingdoc_selected_rewrites WHERE id = $1", row.ID,
		).Scan(&state))
		expected := row.Status
		if expected == "failed" {
			expected = "queued"
		}
		require.Equal(t, expected, state, "回滚之后旧状态词必须回到旧词表")
	}
	require.False(t, postgresColumnExists(t, db, schema, "lingdoc_selected_rewrites", "validity"),
		"回滚之后 validity 列必须消失")
}
