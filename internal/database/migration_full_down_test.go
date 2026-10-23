package database

// 本文件（#564）在**真实 PostgreSQL** 上守住一件事：migrations/versioned 的
// down 链能从当前版本一路走到 0，而且走到 0 时不留下半迁移的残骸。
//
// 为什么需要它：000001_agent.down.sql 里那条
//
//	UPDATE models m JOIN knowledges k ON m.id = k.id SET m.tenant_id = 0 ...
//
// 是 MySQL 写法。PostgreSQL 的 UPDATE 不接受 JOIN 子句（等价写法是
// UPDATE ... SET ... FROM ... WHERE），它在事务里报语法错 ⇒ 整条迁移回滚 ⇒
// 迁移停在 (version=0, dirty=true)，一批表留在库里。修掉语法之后，这条守护把
// 「能一路走到 0」钉住，防止同类回归。
//
// 守护里有三段断言，说的都是**真实行为**，不是文本匹配：
//
//  1. 000043 / 000044 的 down 里那两道 weknora.allow_destructive_migration
//     守卫是**有意为之的安全设计**：不显式放行，down 链就停在那里。停在那里是
//     **预期行为，不是缺陷**——所以守护先证明它拦得住、而且拦下来时它保护的数据
//     原样还在（守卫是「先拒绝、后动手」），再显式放行、走完剩下的路。
//
//  2. 放行之后，down 链要真的走到 0（版本表空 = 没有版本了），并且**不留下任何
//     应用表**：只剩 golang-migrate 自己的记账表 schema_migrations。
//
//  3. 000011 与 000000 的 down 是**语义不可逆**的（见 assertFullDownIrreversible
//     的注释），守护把这一点写成断言，而不是假装它们能回滚。
//
// 两种环境下的期望边界（**两种都到 0，都不残留应用表**）：
//
//   - 服务器给得出 vector / pg_search（生产形态、paradedb 镜像）：整条链跑完，
//     含 embeddings 那张表。down 之后**扩展仍在**——那是 000000 的 down 里
//     DROP EXTENSION 被注释掉的设计选择，**不是回滚失败**。
//   - 服务器给不出 vector / pg_search（**CI 的 postgres:17 service**）：迁移
//     000002 整体跳过（app.skip_embedding=true），embeddings 这张表**从来没有被
//     建过**，所以它的 down 也就没有东西可删。000002 的 down 全是
//     DROP ... IF EXISTS，缺表不报错 ⇒ down 链照样走到 0，照样不残留应用表。
//
// ⇒ 000011 / 000000 的「语义不可逆」**不会**让 down 链到不了 0：前者是空操作
// （不报错），后者只影响扩展、且扩展本来就不删。这两点是**设计上不回滚**，不是
// 「回滚失败」，与「链能不能走到 0」是两条不同的性质。
//
// PostgreSQL 侧只在 LINGDOC_TEST_POSTGRES_DSN 指向隔离测试库时运行。没有配置
// 时明确跳过——跳过不是通过。

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/stretchr/testify/require"
)

const (
	// fullDownAuditLogGuardVersion 是带 weknora.allow_destructive_migration 守卫的
	// 000044_audit_log 的版本号。
	fullDownAuditLogGuardVersion = 44
	// fullDownTenantRBACGuardVersion 是紧挨着它的下一道守卫
	// 000043_tenant_rbac 的版本号。
	fullDownTenantRBACGuardVersion = 43
	// fullDownMigrationVersionTable 是 golang-migrate 自己的记账表。它由驱动建、
	// 由驱动留着（走完 down 之后是空的），它不是应用表。
	fullDownMigrationVersionTable = "schema_migrations"
)

// TestPostgresFullDownChainReachesNilVersionWithoutResidualTables 证明
// migrations/versioned 的 down 链能从当前版本一路走到 0，且不留下应用表。
//
// 没有配置 LINGDOC_TEST_POSTGRES_DSN 时明确跳过：跳过不是通过。
func TestPostgresFullDownChainReachesNilVersionWithoutResidualTables(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("LINGDOC_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("NOT RUN: LINGDOC_TEST_POSTGRES_DSN is unset; requires an isolated disposable PostgreSQL server")
	}
	repoRoot := sqliteRepoRoot(t)
	chdirAndRestore(t, repoRoot)

	// 不可逆的两处 down 先说清楚，免得后来的人以为「能走到 0」等于「全都能回滚」。
	assertFullDownIrreversible(t, repoRoot)

	schema := newPostgresRehearsalSchema(t, dsn)
	db := openPostgresSchemaDB(t, dsn, schema)

	// 两个迁移器，同一个 schema：
	//   - guarded  ：不放行 weknora.allow_destructive_migration。它用来证明
	//                守卫拦得住（守卫是**有意为之的安全设计**，停在那里是预期行为）。
	//   - released：显式放行。它用来走完剩下的路。
	// 放行是**这次演练自己做的选择**，守卫文件本身一个字都没动。
	guarded := newFullDownMigrator(t, dsn, schema, false)
	released := newFullDownMigrator(t, dsn, schema, true)

	// --- 1. 升到当前最新版本 ------------------------------------------------
	require.NoError(t, guarded.Up())
	upVersion, upDirty, err := guarded.Version()
	require.NoError(t, err)
	require.False(t, upDirty)
	require.Greater(t, upVersion, uint(fullDownAuditLogGuardVersion),
		"演练要求迁移链至少有 000044 那一道守卫")

	// --- 2. 000044 的守卫必须拦得住 down 链 --------------------------------
	// 停在这里是**预期行为**：000044 的 down 会删掉 audit_logs（不可恢复的审计
	// 历史），所以它要求操作者显式放行。守护断言的是「守卫存在且生效」，不是
	// 「守卫不该拦」。
	err = guarded.Down()
	require.Error(t, err, "不放行 weknora.allow_destructive_migration 时，000044 的 down 必须拒绝执行")
	require.Contains(t, err.Error(), "[Migration 000044 down] BLOCKED")
	require.Contains(t, err.Error(), "audit_logs")

	version, dirty, err := guarded.Version()
	require.NoError(t, err)
	require.True(t, dirty, "被守卫拒绝的那一步必须把版本标成 dirty，不能装作成功")
	require.Equal(t, uint(fullDownAuditLogGuardVersion-1), version)

	// 守卫是「先拒绝、后动手」：它保护的数据必须原样还在。
	require.True(t, fullDownPostgresTableExists(t, db, schema, "audit_logs"),
		"000044 的守卫拒绝之后，audit_logs 必须原样还在")

	// --- 3. 显式放行，让 000044 的 down 真的执行 ----------------------------
	// 版本拨回 44 再走一步：000044 的 up 还在库里，只是它的 down 被守卫挡下了，
	// 所以 44 才是「当前版本」。Force 只改记账，不执行任何迁移，因此这里没有
	// 任何一条 down 被跳过。
	require.NoError(t, guarded.Force(fullDownAuditLogGuardVersion))
	require.NoError(t, released.Steps(-1))
	version, dirty, err = released.Version()
	require.NoError(t, err)
	require.False(t, dirty)
	require.Equal(t, uint(fullDownAuditLogGuardVersion-1), version)
	require.False(t, fullDownPostgresTableExists(t, db, schema, "audit_logs"),
		"显式放行之后，000044 的 down 必须真的把 audit_logs 删掉")

	// --- 4. 000043 的守卫同样存在 -------------------------------------------
	err = guarded.Steps(-1)
	require.Error(t, err, "不放行守卫时，000043 的 down 必须拒绝执行")
	require.Contains(t, err.Error(), "[Migration 000043 down] BLOCKED")
	require.Contains(t, err.Error(), "tenant_members")

	version, dirty, err = guarded.Version()
	require.NoError(t, err)
	require.True(t, dirty)
	require.Equal(t, uint(fullDownTenantRBACGuardVersion-1), version)
	require.True(t, fullDownPostgresTableExists(t, db, schema, "tenant_members"),
		"000043 的守卫拒绝之后，tenant_members 必须原样还在")

	// --- 5. 放行 000043 的 down，然后一路走到 0 ----------------------------
	// 同样把版本拨回 43（43 的 up 还在库里，只是它的 down 被挡下了），再放行。
	require.NoError(t, guarded.Force(fullDownTenantRBACGuardVersion))
	require.NoError(t, released.Steps(-1))
	version, dirty, err = released.Version()
	require.NoError(t, err)
	require.False(t, dirty)
	require.Equal(t, uint(fullDownTenantRBACGuardVersion-1), version)
	require.False(t, fullDownPostgresTableExists(t, db, schema, "tenant_members"),
		"显式放行之后，000043 的 down 必须真的把 tenant_members 删掉")

	// --- 6. 剩下的 down 链一路走到 0 ----------------------------------------
	require.NoError(t, released.Down())

	version, dirty, err = released.Version()
	require.ErrorIs(t, err, migrate.ErrNilVersion,
		"down 链走完之后必须没有版本了（版本表是空的）")
	require.Equal(t, uint(0), version, "没有版本 = 版本号 0")
	require.False(t, dirty)

	// --- 7. 不留残骸 ---------------------------------------------------------
	// 迁移链建的表必须一张都不剩：这一轮里每一条 down 都真的执行过（被守卫挡下
	// 的那两条在放行后补执行了），所以没有表被留在半路。
	// golang-migrate 自己的记账表 schema_migrations 由驱动建、由驱动留着
	//（空的），它不是应用表。
	residual := fullDownPostgresTablesInSchema(t, db, schema)
	require.ElementsMatch(t, []string{fullDownMigrationVersionTable}, residual,
		"down 链走到 0 之后不得留下任何应用表")
}

// assertFullDownIrreversible 如实标注两处**语义不可逆**的 down。
//
// 它们不是「回滚失败」，是**设计上不回滚**：
//
//   - 000011_pg_search_update.down.sql：up 侧做的是
//     ALTER EXTENSION pg_search UPDATE，PostgreSQL 没有反向操作，所以 down 是
//     空操作（文件里写明了这一点）。断言它的可执行内容为空。
//
//   - 000000_init.down.sql：down 侧本来会删掉 pg_search / pg_trgm / vector /
//     uuid-ossp 四个扩展，但那几行是**注释掉的**——扩展可能被别的库或 schema
//     共用，删掉它们不是回滚该做的事。断言所有 DROP EXTENSION 行都仍是注释。
//
// 这两条刻意不修：它们是「不可逆」而不是「坏掉了」。
func assertFullDownIrreversible(t *testing.T, repoRoot string) {
	t.Helper()

	pgSearchDown := fullDownReadMigrationFile(t, repoRoot, "000011_pg_search_update.down.sql")
	require.Empty(t, strings.TrimSpace(fullDownStripSQLComments(pgSearchDown)),
		"000011 的 down 必须是空操作（ALTER EXTENSION UPDATE 没有反向操作）")
	require.Contains(t, strings.ToLower(pgSearchDown), "no-op",
		"000011 的 down 必须把「不回滚」的理由写在文件里")

	initDown := fullDownReadMigrationFile(t, repoRoot, "000000_init.down.sql")
	for _, line := range strings.Split(initDown, "\n") {
		if !strings.Contains(strings.ToUpper(line), "DROP EXTENSION") {
			continue
		}
		require.True(t, strings.HasPrefix(strings.TrimSpace(line), "--"),
			"000000 的 down 里的 DROP EXTENSION 必须保持注释状态（扩展共用，删不得）")
	}
}

// newFullDownMigrator 开一个指向隔离 schema 的 PostgreSQL 迁移器。
//
// allowDestructive 为真时，连接带上 weknora.allow_destructive_migration=true，
// 也就是 000043 / 000044 那两道守卫要求的显式放行。放行是**这次演练自己做的
// 选择**，不是把守卫改松：守卫文件本身一个字都没动。
func newFullDownMigrator(t *testing.T, dsn, schema string, allowDestructive bool) *migrate.Migrate {
	t.Helper()

	migrationURL := postgresRehearsalURL(t, dsn, schema)
	if option := fullDownMigrationOptions(t, dsn, allowDestructive); option != "" {
		migrationURL = postgresRehearsalURLWithOption(t, migrationURL, option)
	}
	migrator, err := migrate.New("file://migrations/versioned", migrationURL)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = migrator.Close()
	})
	return migrator
}

// fullDownMigrationOptions 拼出这次演练要交给服务器端的启动参数。
//
//   - app.skip_embedding：服务器给不出 vector / pg_search 时才加（见
//     postgresEmbeddingOption 的说明）。给得出的服务器仍走完整的链。
//   - weknora.allow_destructive_migration：只在显式放行破坏性迁移时加。
func fullDownMigrationOptions(t *testing.T, dsn string, allowDestructive bool) string {
	t.Helper()

	options := []string{}
	if embedding := postgresEmbeddingOption(t, dsn); embedding != "" {
		options = append(options, embedding)
	}
	if allowDestructive {
		options = append(options, "-c weknora.allow_destructive_migration=true")
	}
	return strings.Join(options, " ")
}

// postgresTableExists 判断某个 schema 下有没有这张表。
func fullDownPostgresTableExists(t *testing.T, db *sql.DB, schema, table string) bool {
	t.Helper()

	var count int
	require.NoError(t, db.QueryRow(
		"SELECT COUNT(*) FROM information_schema.tables "+
			"WHERE table_schema = $1 AND table_name = $2",
		schema, table,
	).Scan(&count))
	return count == 1
}

// postgresTablesInSchema 列出某个 schema 下所有基表。
func fullDownPostgresTablesInSchema(t *testing.T, db *sql.DB, schema string) []string {
	t.Helper()

	rows, err := db.Query(
		"SELECT table_name FROM information_schema.tables "+
			"WHERE table_schema = $1 AND table_type = 'BASE TABLE' ORDER BY table_name",
		schema,
	)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	names := []string{}
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		names = append(names, name)
	}
	require.NoError(t, rows.Err())
	return names
}

// fullDownReadMigrationFile 读一条迁移文件的内容。
func fullDownReadMigrationFile(t *testing.T, repoRoot, name string) string {
	t.Helper()

	content, err := os.ReadFile(filepath.Join(repoRoot, "migrations", "versioned", name))
	require.NoError(t, err)
	return string(content)
}

// fullDownStripSQLComments 去掉 SQL 注释与空白，只留下可执行的内容。
//
// 它只处理 `--` 行注释；引号里的内容原样保留（它读的那两个迁移文件里没有把
// 注释符写进字符串字面量的情况）。
func fullDownStripSQLComments(sql string) string {
	var out strings.Builder
	for _, line := range strings.Split(sql, "\n") {
		if index := strings.Index(line, "--"); index >= 0 {
			line = line[:index]
		}
		out.WriteString(line)
		out.WriteString("\n")
	}
	return out.String()
}
