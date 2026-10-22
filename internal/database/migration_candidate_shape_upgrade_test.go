package database

// 本文件在**真实旧库**上演练候选存储形状迁移（PF-2/1b 交付的
// migrations/versioned/000126 与 migrations/sqlite/000047）。
//
// #351 证明了脚本写得出来；这里证明旧库升得上去、降得回来。两件事必须成立：
//
//   - 升级与回滚都不得动数据：行数、主键、索引、外键、正文与来源 JSON 全部
//     原样保留。SQLite 侧靠临时表重建，任何一次重建都可能悄悄丢掉索引或外键，
//     而这要等到下一个查询真的需要它才会显形。
//   - 迁移中途失败不得留下半迁移状态，并且可以重跑。PostgreSQL 的迁移驱动
//     不把迁移包在事务里（SQLite 的驱动会），所以「先检查再动手」这件事
//     必须由脚本自己保证，不能指望驱动兜底。
//
// 旧库不是新建的空库：它已经停在迁移之前的版本号上，里面已经有候选、已经有
// 采纳幂等行、已经有 fresh 与 stale 两种状态、已经有正文与来源 JSON，也已经有
// 指向候选的正式章节版本（外键）。
//
// PostgreSQL 侧只在 LINGDOC_TEST_POSTGRES_DSN 指向隔离测试库时运行。没有配置
// 时明确跳过——跳过不是通过。

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/stretchr/testify/require"
)

const (
	// legacyShapeSQLiteVersion 是候选存储形状迁移之前的 SQLite 版本号。
	legacyShapeSQLiteVersion = 46
	// candidateShapeSQLiteVersion 是候选存储形状迁移之后的 SQLite 版本号。
	candidateShapeSQLiteVersion = 47
	// legacyShapePostgresVersion 是候选存储形状迁移之前的 PostgreSQL 版本号。
	legacyShapePostgresVersion = 125
	// candidateShapePostgresVersion 是候选存储形状迁移之后的 PostgreSQL 版本号。
	candidateShapePostgresVersion = 126
)

// candidateShapeNewColumns 是候选存储形状迁移新增的四列。
var candidateShapeNewColumns = []string{"model_id", "origin", "run_mode", "writing_action"}

// legacyCandidateRow 是旧库里的一条候选。
//
// 旧库里已经有正文、来源 JSON、依据 JSON 与待核事项，迁移必须把它们原样搬过去。
type legacyCandidateRow struct {
	ID       string
	Validity string
	Body     string
	Sources  string
	Basis    string
	Reviews  string
}

// legacyCandidateRows 是旧库形态里必须活过迁移的候选。
var legacyCandidateRows = []legacyCandidateRow{
	{
		ID:       "candidate-fresh",
		Validity: "fresh",
		Body:     "# 第一节\n\n旧库里的正文第一段，含中文与换行。\n",
		Sources:  `["source-a","source-b"]`,
		Basis:    `{"template_id":"tpl-1","template_version":3,"context_revision":7}`,
		Reviews:  `[{"id":"review-1","statement":"需要人工核查来源"}]`,
	},
	{
		ID:       "candidate-stale",
		Validity: "stale",
		Body:     "# 第二节\n\n旧库里的正文第二段，状态是 stale。\n",
		Sources:  `["source-c"]`,
		Basis:    `{"template_id":"tpl-1","template_version":3,"context_revision":8}`,
		Reviews:  `[]`,
	},
}

// legacyAdoptionRow 是旧库里的一条采纳幂等行。
type legacyAdoptionRow struct {
	ID  string
	Key string
	// Hash 是旧摘要规则算出来的摘要：没有版本前缀的那一种。
	Hash string
}

// legacyAdoptionRows 是旧库里必须活过迁移的幂等行。
var legacyAdoptionRows = []legacyAdoptionRow{
	{ID: "adoption-1", Key: "key-1", Hash: "2c26b46b68ffc68ff99b453c1d30413413422d706483bfa0f98a5e886266e7ae"},
	{ID: "adoption-2", Key: "key-2", Hash: "b5bb9d8014a0f9b1d61e21e796d78dccdf1352f23cd32812f4850b878ae4944c"},
}

// candidateShapeSnapshot 是一次迁移前后必须保持一致的那些东西。
type candidateShapeSnapshot struct {
	// RowCount 是候选表的行数。
	RowCount int
	// PrimaryKeys 是候选表的主键列名（有序）。
	PrimaryKeys []string
	// Keys 是候选表每一行的主键取值（有序）。
	Keys []string
	// Indexes 是候选表上的索引名（有序）。
	Indexes []string
	// ForeignKeys 是候选表的外键定义（有序）。
	ForeignKeys []string
	// Columns 是候选表的列名（有序）。
	Columns []string
	// Bodies 是每一行的正文、来源 JSON、依据 JSON 与待核事项（按主键取值）。
	Bodies map[string]string
}

// assertCandidateShapeCarriedOver 断言一次迁移把候选表原样搬了过去。
//
// 迁移只允许增列与改 CHECK；它不许改行数、不许改主键、不许丢索引、不许丢外键、
// 不许动正文与来源 JSON。
func assertCandidateShapeCarriedOver(t *testing.T, before, after candidateShapeSnapshot) {
	t.Helper()

	require.Equal(t, before.RowCount, after.RowCount, "迁移不得改变候选表的行数")
	require.Equal(t, before.PrimaryKeys, after.PrimaryKeys, "迁移不得改变候选表的主键列")
	require.Equal(t, before.Keys, after.Keys, "迁移不得改变候选表的主键取值")
	require.Equal(t, before.Indexes, after.Indexes, "迁移不得丢掉候选表上的索引")
	require.Equal(t, before.ForeignKeys, after.ForeignKeys, "迁移不得丢掉候选表上的外键")
	require.Equal(t, before.Bodies, after.Bodies, "迁移不得改动候选的正文与来源 JSON")
}

// assertCandidateShapeColumns 断言候选表的列集合是「旧列 + 指定的新增列」。
func assertCandidateShapeColumns(t *testing.T, before, after candidateShapeSnapshot, added []string) {
	t.Helper()

	expected := append([]string{}, before.Columns...)
	expected = append(expected, added...)
	sort.Strings(expected)
	require.Equal(t, expected, after.Columns, "候选表的列集合与预期不符")
}

// TestSQLiteLegacyDatabaseUpgradeAndRollbackKeepsEverything 证明 SQLite 侧的
// 候选存储形状迁移在**真实旧库**上真的能升上去、降回来，而且不丢任何东西。
func TestSQLiteLegacyDatabaseUpgradeAndRollbackKeepsEverything(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)
	chdirAndRestore(t, repoRoot)

	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	migrator := newSQLiteMigrator(t, dbPath)

	// 旧库：停在迁移之前的版本号上，不是新建的空库。
	require.NoError(t, migrator.Migrate(legacyShapeSQLiteVersion))
	version, dirty, err := migrator.Version()
	require.NoError(t, err)
	require.Equal(t, uint(legacyShapeSQLiteVersion), version)
	require.False(t, dirty)

	db := openSQLiteDB(t, dbPath)
	seedLegacySQLiteDatabase(t, db)

	before := snapshotSQLiteCandidateShape(t, db)
	require.Equal(t, len(legacyCandidateRows), before.RowCount)
	require.NotEmpty(t, before.Indexes, "旧库的候选表上必须已经有索引")
	require.NotEmpty(t, before.ForeignKeys, "旧库的候选表上必须已经有外键")
	t.Logf("旧库（SQLite v%d）候选表：行数=%d 主键=%v 索引=%v 外键=%d 条",
		legacyShapeSQLiteVersion, before.RowCount, before.PrimaryKeys, before.Indexes, len(before.ForeignKeys))

	// ---- 升级 ----
	require.NoError(t, migrator.Steps(1))
	version, dirty, err = migrator.Version()
	require.NoError(t, err)
	require.Equal(t, uint(candidateShapeSQLiteVersion), version)
	require.False(t, dirty)

	afterUp := snapshotSQLiteCandidateShape(t, db)
	assertCandidateShapeCarriedOver(t, before, afterUp)
	assertCandidateShapeColumns(t, before, afterUp, candidateShapeNewColumns)
	for _, column := range candidateShapeNewColumns {
		require.True(t, sqliteColumnExists(t, db, "lingdoc_candidates", column),
			"升级之后候选表必须带 %s 列", column)
	}
	require.True(t, sqliteColumnExists(t, db, "lingdoc_candidate_adoptions", "digest_version"))
	t.Logf("升级后（SQLite v%d）候选表：行数=%d 主键=%v 索引=%v 外键=%d 条 新增列=%v",
		candidateShapeSQLiteVersion, afterUp.RowCount, afterUp.PrimaryKeys, afterUp.Indexes,
		len(afterUp.ForeignKeys), candidateShapeNewColumns)

	// 旧幂等行按旧规则写成，回填成版本 1。
	for _, row := range legacyAdoptionRows {
		var digestVersion int
		require.NoError(t, db.QueryRow(
			"SELECT digest_version FROM lingdoc_candidate_adoptions WHERE id = ?", row.ID,
		).Scan(&digestVersion))
		require.Equal(t, 1, digestVersion, "旧幂等行必须回填成摘要版本 1")
	}
	t.Logf("升级后旧幂等行 %d 条，digest_version 全部回填为 1", len(legacyAdoptionRows))

	// 新的失效状态写得进去，越界的来源写不进去。
	probeSQLiteCandidateWrites(t, db)

	// ---- 回滚 ----
	require.NoError(t, migrator.Steps(-1))
	version, dirty, err = migrator.Version()
	require.NoError(t, err)
	require.Equal(t, uint(legacyShapeSQLiteVersion), version)
	require.False(t, dirty)

	afterDown := snapshotSQLiteCandidateShape(t, db)
	assertCandidateShapeCarriedOver(t, before, afterDown)
	assertCandidateShapeColumns(t, before, afterDown, nil)
	for _, column := range candidateShapeNewColumns {
		require.False(t, sqliteColumnExists(t, db, "lingdoc_candidates", column),
			"回滚之后候选表必须不再带 %s 列", column)
	}
	require.False(t, sqliteColumnExists(t, db, "lingdoc_candidate_adoptions", "digest_version"))
	t.Logf("回滚后（SQLite v%d）候选表：行数=%d 主键=%v 索引=%v 外键=%d 条 新增列已消失",
		legacyShapeSQLiteVersion, afterDown.RowCount, afterDown.PrimaryKeys, afterDown.Indexes,
		len(afterDown.ForeignKeys))

	// ---- 再升级一次 ----
	require.NoError(t, migrator.Steps(1))
	version, dirty, err = migrator.Version()
	require.NoError(t, err)
	require.Equal(t, uint(candidateShapeSQLiteVersion), version)
	require.False(t, dirty)

	afterSecondUp := snapshotSQLiteCandidateShape(t, db)
	assertCandidateShapeCarriedOver(t, before, afterSecondUp)
	assertCandidateShapeColumns(t, before, afterSecondUp, candidateShapeNewColumns)
	t.Logf("再次升级后（SQLite v%d）候选表：行数=%d 主键=%v 索引=%v 外键=%d 条",
		candidateShapeSQLiteVersion, afterSecondUp.RowCount, afterSecondUp.PrimaryKeys,
		afterSecondUp.Indexes, len(afterSecondUp.ForeignKeys))
}

// TestSQLiteLegacyUpgradeFailureLeavesNoHalfMigratedState 证明 SQLite 侧的
// 迁移中途失败不留半迁移状态，并且可以重跑。
//
// 失败是用一条主键为空白的候选造出来的：旧库的候选表把 id 声明成
// TEXT PRIMARY KEY，而 SQLite 允许主键列取空值，于是这一行复制之后无法用
// 主键找回——迁移脚本自带的键守卫会在这里拒绝。这次失败发生在「新表已经建好、
// 行已经复制」之后、「旧表还没被替换」之前，正是半迁移状态最容易出现的位置。
func TestSQLiteLegacyUpgradeFailureLeavesNoHalfMigratedState(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)
	chdirAndRestore(t, repoRoot)

	dbPath := filepath.Join(t.TempDir(), "legacy-failure.db")
	migrator := newSQLiteMigrator(t, dbPath)

	require.NoError(t, migrator.Migrate(legacyShapeSQLiteVersion))
	db := openSQLiteDB(t, dbPath)
	seedLegacySQLiteDatabase(t, db)

	// 一条主键为空白的候选：复制得进去，却找不回来。
	_, err := db.Exec(
		"INSERT INTO lingdoc_candidates (id, project_id, chapter_id, run_id, body_markdown, basis_json, validity) " +
			"VALUES (NULL, 'project-1', 'chapter-1', 'run-blank', '正文', '{}', 'fresh')",
	)
	require.NoError(t, err, "旧库允许主键取空值，这正是要用来造失败的那一行")

	before := snapshotSQLiteCandidateShape(t, db)

	// 升级必须失败。
	require.Error(t, migrator.Steps(1), "键守卫必须拒绝一条复制后找不回来的候选")

	// 半迁移状态：旧表必须原样还在，新表不能留下。
	afterFailure := snapshotSQLiteCandidateShape(t, db)
	assertCandidateShapeCarriedOver(t, before, afterFailure)
	assertCandidateShapeColumns(t, before, afterFailure, nil)
	require.False(t, sqliteColumnExists(t, db, "lingdoc_candidates", "writing_action"),
		"失败的迁移不得留下新增列")
	require.False(t, sqliteTableExists(t, db, "lingdoc_candidates_next"),
		"失败的迁移不得留下重建用的临时表")
	t.Logf("失败之后（SQLite）：行数=%d 主键=%v 索引=%v 外键=%d 条，临时表未留下",
		afterFailure.RowCount, afterFailure.PrimaryKeys, afterFailure.Indexes, len(afterFailure.ForeignKeys))

	// 版本记账停在脏状态上，这正是仓库里 recoverFromDirtyState 处理的情形。
	version, dirty, err := migrator.Version()
	require.NoError(t, err)
	require.True(t, dirty, "失败的迁移必须把版本标成脏的")
	require.Equal(t, uint(candidateShapeSQLiteVersion), version)

	// 修掉那条坏数据之后可以重跑。
	require.NoError(t, migrator.Force(legacyShapeSQLiteVersion))
	_, err = db.Exec("DELETE FROM lingdoc_candidates WHERE id IS NULL")
	require.NoError(t, err)

	afterRepair := snapshotSQLiteCandidateShape(t, db)

	require.NoError(t, migrator.Steps(1), "修掉坏数据之后必须能重跑")
	version, dirty, err = migrator.Version()
	require.NoError(t, err)
	require.Equal(t, uint(candidateShapeSQLiteVersion), version)
	require.False(t, dirty)

	afterRetry := snapshotSQLiteCandidateShape(t, db)
	assertCandidateShapeCarriedOver(t, afterRepair, afterRetry)
	assertCandidateShapeColumns(t, afterRepair, afterRetry, candidateShapeNewColumns)
	t.Logf("重跑之后（SQLite v%d）候选表：行数=%d 主键=%v 索引=%v 外键=%d 条",
		candidateShapeSQLiteVersion, afterRetry.RowCount, afterRetry.PrimaryKeys,
		afterRetry.Indexes, len(afterRetry.ForeignKeys))
}

// TestPostgresLegacyDatabaseUpgradeAndRollbackKeepsEverything 证明 PostgreSQL
// 侧的候选存储形状迁移在**真实旧库**上真的能升上去、降回来。
//
// 没有配置 LINGDOC_TEST_POSTGRES_DSN 时明确跳过：跳过不是通过。
func TestPostgresLegacyDatabaseUpgradeAndRollbackKeepsEverything(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("LINGDOC_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("NOT RUN: LINGDOC_TEST_POSTGRES_DSN is unset; requires an isolated disposable PostgreSQL server")
	}
	repoRoot := sqliteRepoRoot(t)
	chdirAndRestore(t, repoRoot)

	schema := newPostgresRehearsalSchema(t, dsn)
	migrator := newPostgresMigrator(t, dsn, schema)

	// 旧库：停在迁移之前的版本号上。
	require.NoError(t, migrator.Migrate(legacyShapePostgresVersion))
	version, dirty, err := migrator.Version()
	require.NoError(t, err)
	require.Equal(t, uint(legacyShapePostgresVersion), version)
	require.False(t, dirty)

	db := openPostgresSchemaDB(t, dsn, schema)
	seedLegacyPostgresDatabase(t, db)

	before := snapshotPostgresCandidateShape(t, db, schema)
	require.Equal(t, len(legacyCandidateRows), before.RowCount)
	require.NotEmpty(t, before.Indexes, "旧库的候选表上必须已经有索引")
	require.NotEmpty(t, before.ForeignKeys, "旧库的候选表上必须已经有外键")
	t.Logf("旧库（PostgreSQL v%d）候选表：行数=%d 主键=%v 索引=%v 外键=%d 条",
		legacyShapePostgresVersion, before.RowCount, before.PrimaryKeys, before.Indexes, len(before.ForeignKeys))

	// ---- 升级 ----
	require.NoError(t, migrator.Steps(1))
	version, dirty, err = migrator.Version()
	require.NoError(t, err)
	require.Equal(t, uint(candidateShapePostgresVersion), version)
	require.False(t, dirty)

	afterUp := snapshotPostgresCandidateShape(t, db, schema)
	assertCandidateShapeCarriedOver(t, before, afterUp)
	assertCandidateShapeColumns(t, before, afterUp, candidateShapeNewColumns)
	for _, column := range candidateShapeNewColumns {
		require.True(t, postgresColumnExists(t, db, schema, "lingdoc_candidates", column),
			"升级之后候选表必须带 %s 列", column)
	}
	require.True(t, postgresColumnExists(t, db, schema, "lingdoc_candidate_adoptions", "digest_version"))
	t.Logf("升级后（PostgreSQL v%d）候选表：行数=%d 主键=%v 索引=%v 外键=%d 条 新增列=%v",
		candidateShapePostgresVersion, afterUp.RowCount, afterUp.PrimaryKeys, afterUp.Indexes,
		len(afterUp.ForeignKeys), candidateShapeNewColumns)

	for _, row := range legacyAdoptionRows {
		var digestVersion int
		require.NoError(t, db.QueryRow(
			"SELECT digest_version FROM lingdoc_candidate_adoptions WHERE id = $1", row.ID,
		).Scan(&digestVersion))
		require.Equal(t, 1, digestVersion, "旧幂等行必须回填成摘要版本 1")
	}
	t.Logf("升级后旧幂等行 %d 条，digest_version 全部回填为 1", len(legacyAdoptionRows))

	probePostgresCandidateWrites(t, db)

	// ---- 回滚 ----
	require.NoError(t, migrator.Steps(-1))
	version, dirty, err = migrator.Version()
	require.NoError(t, err)
	require.Equal(t, uint(legacyShapePostgresVersion), version)
	require.False(t, dirty)

	afterDown := snapshotPostgresCandidateShape(t, db, schema)
	assertCandidateShapeCarriedOver(t, before, afterDown)
	assertCandidateShapeColumns(t, before, afterDown, nil)
	for _, column := range candidateShapeNewColumns {
		require.False(t, postgresColumnExists(t, db, schema, "lingdoc_candidates", column),
			"回滚之后候选表必须不再带 %s 列", column)
	}
	require.False(t, postgresColumnExists(t, db, schema, "lingdoc_candidate_adoptions", "digest_version"))
	t.Logf("回滚后（PostgreSQL v%d）候选表：行数=%d 主键=%v 索引=%v 外键=%d 条 新增列已消失",
		legacyShapePostgresVersion, afterDown.RowCount, afterDown.PrimaryKeys, afterDown.Indexes,
		len(afterDown.ForeignKeys))

	// ---- 再升级一次 ----
	require.NoError(t, migrator.Steps(1))
	version, dirty, err = migrator.Version()
	require.NoError(t, err)
	require.Equal(t, uint(candidateShapePostgresVersion), version)
	require.False(t, dirty)

	afterSecondUp := snapshotPostgresCandidateShape(t, db, schema)
	assertCandidateShapeCarriedOver(t, before, afterSecondUp)
	assertCandidateShapeColumns(t, before, afterSecondUp, candidateShapeNewColumns)
	t.Logf("再次升级后（PostgreSQL v%d）候选表：行数=%d 主键=%v 索引=%v 外键=%d 条",
		candidateShapePostgresVersion, afterSecondUp.RowCount, afterSecondUp.PrimaryKeys,
		afterSecondUp.Indexes, len(afterSecondUp.ForeignKeys))
}

// TestPostgresLegacyRollbackRefusalLeavesNoHalfMigratedState 证明 PostgreSQL
// 侧的回滚在**拒绝**时不留半迁移状态，并且修掉原因之后可以重跑。
//
// 一个授权已经失效的候选在旧的两值状态里没有 truthfully 的表示，所以回滚必须
// 拒绝它。关键在于拒绝的时机：PostgreSQL 的迁移驱动不把迁移包在事务里，
// 脚本必须自己保证「先检查、再动手」，否则表会停在「约束已经摘掉、新约束还没
// 装上」的半迁移状态上。
func TestPostgresLegacyRollbackRefusalLeavesNoHalfMigratedState(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("LINGDOC_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("NOT RUN: LINGDOC_TEST_POSTGRES_DSN is unset; requires an isolated disposable PostgreSQL server")
	}
	repoRoot := sqliteRepoRoot(t)
	chdirAndRestore(t, repoRoot)

	schema := newPostgresRehearsalSchema(t, dsn)
	migrator := newPostgresMigrator(t, dsn, schema)

	require.NoError(t, migrator.Migrate(legacyShapePostgresVersion))
	db := openPostgresSchemaDB(t, dsn, schema)
	seedLegacyPostgresDatabase(t, db)

	require.NoError(t, migrator.Steps(1))

	// 一个授权已经失效的候选。
	_, err := db.Exec(
		"INSERT INTO lingdoc_candidates " +
			"(id, project_id, chapter_id, run_id, body_markdown, source_ids_json, basis_json, validity, review_items_json) " +
			"VALUES ('candidate-not-authorized', 'project-1', 'chapter-1', 'run-na', '正文', '[]', '{}', 'not_authorized', '[]')",
	)
	require.NoError(t, err)

	before := snapshotPostgresCandidateShape(t, db, schema)

	// 回滚必须被拒绝。
	require.Error(t, migrator.Steps(-1), "旧形状表达不了授权失效的候选，回滚必须被拒绝")

	// 半迁移状态检查：新形状必须原样还在——新增列在、新的 CHECK 在。
	afterRefusal := snapshotPostgresCandidateShape(t, db, schema)
	assertCandidateShapeCarriedOver(t, before, afterRefusal)
	assertCandidateShapeColumns(t, before, afterRefusal, nil)
	for _, column := range candidateShapeNewColumns {
		require.True(t, postgresColumnExists(t, db, schema, "lingdoc_candidates", column),
			"被拒绝的回滚不得让 %s 列消失", column)
	}
	require.True(t, postgresHasCheckConstraint(t, db, schema, "lingdoc_candidates", "lingdoc_candidates_validity_check"),
		"被拒绝的回滚不得把 validity 的 CHECK 摘掉")
	require.True(t, postgresHasCheckConstraint(t, db, schema, "lingdoc_candidates", "lingdoc_candidates_origin_check"),
		"被拒绝的回滚不得把 origin 的 CHECK 摘掉")
	t.Logf("拒绝之后（PostgreSQL）：行数=%d 主键=%v 索引=%v 外键=%d 条，形状未被改动",
		afterRefusal.RowCount, afterRefusal.PrimaryKeys, afterRefusal.Indexes, len(afterRefusal.ForeignKeys))

	// 版本记账停在脏状态上。
	_, dirty, err := migrator.Version()
	require.NoError(t, err)
	require.True(t, dirty, "被拒绝的回滚必须把版本标成脏的")

	// 修掉原因之后可以重跑。
	require.NoError(t, migrator.Force(candidateShapePostgresVersion))
	_, err = db.Exec("DELETE FROM lingdoc_candidates WHERE id = 'candidate-not-authorized'")
	require.NoError(t, err)

	require.NoError(t, migrator.Steps(-1), "修掉授权失效的候选之后必须能重跑")
	version, dirty, err := migrator.Version()
	require.NoError(t, err)
	require.Equal(t, uint(legacyShapePostgresVersion), version)
	require.False(t, dirty)
	t.Logf("重跑之后（PostgreSQL v%d）回滚成功", legacyShapePostgresVersion)
}

// seedLegacySQLiteDatabase 把旧库形态的数据写进 SQLite 旧库。
func seedLegacySQLiteDatabase(t *testing.T, db *sql.DB) {
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
	_, err = db.Exec(
		"INSERT INTO lingdoc_chapter_versions (id, project_id, chapter_id, body_markdown, source_ids_json, review_items_json) " +
			"VALUES ('version-1', 'project-1', 'chapter-1', '旧版本正文', '[]', '[]')",
	)
	require.NoError(t, err)

	for _, row := range legacyCandidateRows {
		_, err := db.Exec(
			"INSERT INTO lingdoc_candidates "+
				"(id, project_id, chapter_id, run_id, body_markdown, source_ids_json, basis_json, validity, review_items_json) "+
				"VALUES (?, 'project-1', 'chapter-1', ?, ?, ?, ?, ?, ?)",
			row.ID, "run-"+row.ID, row.Body, row.Sources, row.Basis, row.Validity, row.Reviews,
		)
		require.NoError(t, err)
	}
	for _, row := range legacyAdoptionRows {
		_, err := db.Exec(
			"INSERT INTO lingdoc_candidate_adoptions "+
				"(id, project_id, chapter_id, actor_id, idempotency_key, request_hash, chapter_version_id, response_json) "+
				"VALUES (?, 'project-1', 'chapter-1', 'user-1', ?, ?, 'version-1', '{}')",
			row.ID, row.Key, row.Hash,
		)
		require.NoError(t, err)
	}
}

// seedLegacyPostgresDatabase 把旧库形态的数据写进 PostgreSQL 旧库。
func seedLegacyPostgresDatabase(t *testing.T, db *sql.DB) {
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
	_, err = db.Exec(
		"INSERT INTO lingdoc_chapter_versions (id, project_id, chapter_id, body_markdown, source_ids_json, review_items_json) " +
			"VALUES ('version-1', 'project-1', 'chapter-1', '旧版本正文', '[]', '[]')",
	)
	require.NoError(t, err)

	for _, row := range legacyCandidateRows {
		_, err := db.Exec(
			"INSERT INTO lingdoc_candidates "+
				"(id, project_id, chapter_id, run_id, body_markdown, source_ids_json, basis_json, validity, review_items_json) "+
				"VALUES ($1, 'project-1', 'chapter-1', $2, $3, $4, $5, $6, $7)",
			row.ID, "run-"+row.ID, row.Body, row.Sources, row.Basis, row.Validity, row.Reviews,
		)
		require.NoError(t, err)
	}
	for _, row := range legacyAdoptionRows {
		_, err := db.Exec(
			"INSERT INTO lingdoc_candidate_adoptions "+
				"(id, project_id, chapter_id, actor_id, idempotency_key, request_hash, chapter_version_id, response_json) "+
				"VALUES ($1, 'project-1', 'chapter-1', 'user-1', $2, $3, 'version-1', '{}')",
			row.ID, row.Key, row.Hash,
		)
		require.NoError(t, err)
	}
}

// probeSQLiteCandidateWrites 证明新形状的写入规则：授权失效写得进去，越界的
// 来源写不进去。探针跑在会回滚的事务里，不污染演练用的旧库数据。
func probeSQLiteCandidateWrites(t *testing.T, db *sql.DB) {
	t.Helper()

	tx, err := db.Begin()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	_, err = tx.Exec(
		"INSERT INTO lingdoc_candidates " +
			"(id, project_id, chapter_id, run_id, body_markdown, basis_json, validity, origin) " +
			"VALUES ('candidate-not-authorized', 'project-1', 'chapter-1', 'run-na', '正文', '{}', 'not_authorized', 'ai_generated')",
	)
	require.NoError(t, err, "授权失效的候选必须写得进去")

	_, err = tx.Exec(
		"INSERT INTO lingdoc_candidates " +
			"(id, project_id, chapter_id, run_id, body_markdown, basis_json, validity, origin) " +
			"VALUES ('candidate-bad-origin', 'project-1', 'chapter-1', 'run-bo', '正文', '{}', 'fresh', 'written_by_hand')",
	)
	require.Error(t, err, "来源取值必须落在既有三值词表里")
	t.Logf("写入探针：not_authorized 可写，越界 origin 被拒")
}

// probePostgresCandidateWrites 证明新形状的写入规则，语义同
// probeSQLiteCandidateWrites。
func probePostgresCandidateWrites(t *testing.T, db *sql.DB) {
	t.Helper()

	tx, err := db.Begin()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	_, err = tx.Exec(
		"INSERT INTO lingdoc_candidates " +
			"(id, project_id, chapter_id, run_id, body_markdown, basis_json, validity, origin) " +
			"VALUES ('candidate-not-authorized', 'project-1', 'chapter-1', 'run-na', '正文', '{}', 'not_authorized', 'ai_generated')",
	)
	require.NoError(t, err, "授权失效的候选必须写得进去")

	_, err = tx.Exec(
		"INSERT INTO lingdoc_candidates " +
			"(id, project_id, chapter_id, run_id, body_markdown, basis_json, validity, origin) " +
			"VALUES ('candidate-bad-origin', 'project-1', 'chapter-1', 'run-bo', '正文', '{}', 'fresh', 'written_by_hand')",
	)
	require.Error(t, err, "来源取值必须落在既有三值词表里")
	t.Logf("写入探针：not_authorized 可写，越界 origin 被拒")
}

// snapshotSQLiteCandidateShape 读出 SQLite 候选表的形状与内容。
func snapshotSQLiteCandidateShape(t *testing.T, db *sql.DB) candidateShapeSnapshot {
	t.Helper()
	snapshot := candidateShapeSnapshot{Bodies: map[string]string{}}

	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM lingdoc_candidates").Scan(&snapshot.RowCount))

	rows, err := db.Query("PRAGMA table_info(lingdoc_candidates)")
	require.NoError(t, err)
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		require.NoError(t, rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey))
		snapshot.Columns = append(snapshot.Columns, name)
		if primaryKey > 0 {
			snapshot.PrimaryKeys = append(snapshot.PrimaryKeys, name)
		}
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	sort.Strings(snapshot.Columns)
	sort.Strings(snapshot.PrimaryKeys)

	rows, err = db.Query(
		"SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = 'lingdoc_candidates' ORDER BY name",
	)
	require.NoError(t, err)
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		snapshot.Indexes = append(snapshot.Indexes, name)
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())

	rows, err = db.Query("PRAGMA foreign_key_list(lingdoc_candidates)")
	require.NoError(t, err)
	for rows.Next() {
		var id, seq int
		var table, from, to string
		var onUpdate, onDelete, match string
		require.NoError(t, rows.Scan(&id, &seq, &table, &from, &to, &onUpdate, &onDelete, &match))
		snapshot.ForeignKeys = append(snapshot.ForeignKeys,
			fmt.Sprintf("%s(%s)->%s(%s) ON UPDATE %s ON DELETE %s", table, from, to, from, onUpdate, onDelete))
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	sort.Strings(snapshot.ForeignKeys)

	rows, err = db.Query(
		"SELECT id, body_markdown, source_ids_json, basis_json, review_items_json FROM lingdoc_candidates ORDER BY id",
	)
	require.NoError(t, err)
	for rows.Next() {
		var id sql.NullString
		var body, sources, basis, reviews string
		require.NoError(t, rows.Scan(&id, &body, &sources, &basis, &reviews))
		key := candidateKey(id)
		snapshot.Keys = append(snapshot.Keys, key)
		snapshot.Bodies[key] = body + "\x00" + sources + "\x00" + basis + "\x00" + reviews
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	sort.Strings(snapshot.Keys)

	return snapshot
}

// candidateKey 把候选主键读成可比较的字符串。
//
// 旧库的候选表把 id 声明成 TEXT PRIMARY KEY，而 SQLite 允许主键列取空值，
// 所以主键读回来可能是空的。空主键在这里用可区分的标记表示，免得和真的
// 空字符串键混成同一个。
func candidateKey(id sql.NullString) string {
	if !id.Valid {
		return "\x00NULL"
	}
	return id.String
}

// snapshotPostgresCandidateShape 读出 PostgreSQL 候选表的形状与内容。
func snapshotPostgresCandidateShape(t *testing.T, db *sql.DB, schema string) candidateShapeSnapshot {
	t.Helper()
	snapshot := candidateShapeSnapshot{Bodies: map[string]string{}}

	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM lingdoc_candidates").Scan(&snapshot.RowCount))

	rows, err := db.Query(
		"SELECT column_name FROM information_schema.columns "+
			"WHERE table_schema = $1 AND table_name = 'lingdoc_candidates' ORDER BY column_name",
		schema,
	)
	require.NoError(t, err)
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		snapshot.Columns = append(snapshot.Columns, name)
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())

	rows, err = db.Query(
		"SELECT a.attname FROM pg_constraint c JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = ANY(c.conkey) "+
			"WHERE c.conrelid = $1::regclass AND c.contype = 'p' ORDER BY a.attname",
		"lingdoc_candidates",
	)
	require.NoError(t, err)
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		snapshot.PrimaryKeys = append(snapshot.PrimaryKeys, name)
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())

	rows, err = db.Query(
		"SELECT indexname FROM pg_indexes WHERE schemaname = $1 AND tablename = 'lingdoc_candidates' ORDER BY indexname",
		schema,
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
		"SELECT conname, pg_get_constraintdef(oid) FROM pg_constraint "+
			"WHERE conrelid = $1::regclass AND contype = 'f' ORDER BY conname",
		"lingdoc_candidates",
	)
	require.NoError(t, err)
	for rows.Next() {
		var name, definition string
		require.NoError(t, rows.Scan(&name, &definition))
		snapshot.ForeignKeys = append(snapshot.ForeignKeys, name+" "+definition)
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())

	rows, err = db.Query(
		"SELECT id, body_markdown, source_ids_json, basis_json, review_items_json FROM lingdoc_candidates ORDER BY id",
	)
	require.NoError(t, err)
	for rows.Next() {
		var id sql.NullString
		var body, sources, basis, reviews string
		require.NoError(t, rows.Scan(&id, &body, &sources, &basis, &reviews))
		key := candidateKey(id)
		snapshot.Keys = append(snapshot.Keys, key)
		snapshot.Bodies[key] = body + "\x00" + sources + "\x00" + basis + "\x00" + reviews
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())

	return snapshot
}

// postgresColumnExists 判断 PostgreSQL 表上有没有某一列。
func postgresColumnExists(t *testing.T, db *sql.DB, schema, table, column string) bool {
	t.Helper()
	var count int
	require.NoError(t, db.QueryRow(
		"SELECT COUNT(*) FROM information_schema.columns "+
			"WHERE table_schema = $1 AND table_name = $2 AND column_name = $3",
		schema, table, column,
	).Scan(&count))
	return count == 1
}

// postgresHasCheckConstraint 判断 PostgreSQL 表上有没有某个 CHECK 约束。
func postgresHasCheckConstraint(t *testing.T, db *sql.DB, schema, table, constraint string) bool {
	t.Helper()
	var count int
	require.NoError(t, db.QueryRow(
		"SELECT COUNT(*) FROM pg_constraint "+
			"WHERE conrelid = $1::regclass AND contype = 'c' AND conname = $2",
		table, constraint,
	).Scan(&count))
	return count == 1
}

// newPostgresRehearsalSchema 在隔离测试库里开一个本次演练专用的 schema。
//
// 演练绝不碰用户自己的库：schema 名带本次演练的前缀，结束时整块删掉。
func newPostgresRehearsalSchema(t *testing.T, dsn string) string {
	t.Helper()

	root, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = root.Close() })

	// schema 名要短：PostgreSQL 的标识符上限是 63 字节，超了会被截断，
	// 而截断后的名字和这里拼出来的名字对不上。
	schema := "pf2_352_" + strconv.FormatInt(time.Now().UnixNano()%1_000_000_000, 36)
	_, err = root.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
	require.NoError(t, err)
	_, err = root.ExecContext(context.Background(), "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = root.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
	})
	return schema
}

// newPostgresMigrator 开一个指向隔离 schema 的 PostgreSQL 迁移器。
func newPostgresMigrator(t *testing.T, dsn, schema string) *migrate.Migrate {
	t.Helper()

	migrator, err := migrate.New("file://migrations/versioned", postgresRehearsalURL(t, dsn, schema))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = migrator.Close()
	})
	return migrator
}

// openPostgresSchemaDB 打开一个只在隔离 schema 里工作的连接。
func openPostgresSchemaDB(t *testing.T, dsn, schema string) *sql.DB {
	t.Helper()

	db, err := sql.Open("postgres", postgresRehearsalURL(t, dsn, schema))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// postgresRehearsalURL 把测试 DSN 变成带 search_path 的 URL 形态。
//
// golang-migrate 的 PostgreSQL 驱动用 url.Parse 处理 DSN：key=value 形态里的
// 空格会被重新转义，lib/pq 就读不出这些参数了，所以这里统一转成 URL。search_path
// 同时带上 public，迁移链里用到的 vector / pg_trgm 扩展住在 public 里。
func postgresRehearsalURL(t *testing.T, dsn, schema string) string {
	t.Helper()

	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		parsed, err := url.Parse(dsn)
		require.NoError(t, err)
		query := parsed.Query()
		query.Set("search_path", schema+",public")
		parsed.RawQuery = query.Encode()
		return parsed.String()
	}

	values := map[string]string{}
	for _, field := range strings.Fields(dsn) {
		key, value, found := strings.Cut(field, "=")
		if !found {
			continue
		}
		values[key] = value
	}
	host := values["host"]
	if host == "" {
		host = "localhost"
	}
	port := values["port"]
	if port == "" {
		port = "5432"
	}
	parsed := &url.URL{
		Scheme: "postgres",
		Host:   net.JoinHostPort(host, port),
		Path:   "/" + values["dbname"],
	}
	if user := values["user"]; user != "" {
		if password, ok := values["password"]; ok {
			parsed.User = url.UserPassword(user, password)
		} else {
			parsed.User = url.User(user)
		}
	}
	query := parsed.Query()
	for key, value := range values {
		switch key {
		case "host", "port", "user", "password", "dbname":
			continue
		}
		query.Set(key, value)
	}
	query.Set("search_path", schema+",public")
	parsed.RawQuery = query.Encode()
	return parsed.String()
}
