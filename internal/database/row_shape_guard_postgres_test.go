package database

// 本文件（#710）是表形状守护的 **PostgreSQL 侧孪生**：同一份登记清单
// （guardedRowShapes，#709 的门与三元组原样复用，清单内容零改动）、同一套判定
// 语义（列存在性 + 可空性双向 + 类型归一化 + 长度，唯一出处都在
// row_shape_guard_test.go），对 migrations/versioned 的**真实 PostgreSQL 迁移
// 链**执行同样的逐列比对：
//
//	链在隔离演练 schema 建到最新 → information_schema.columns 取列形状 →
//	与 gorm 声明侧比对。
//
// 隔离演练 schema 复用既有 PG 演练助手（newPostgresRehearsalSchema 的说明）：
// 演练绝不碰用户自己的库，schema 名带演练前缀，结束时整块删掉。search_path
// 首位即演练 schema，链建的表全部落在其内，所以产物侧查询按
// table_schema = 演练 schema 过滤——在指向专属空库的 DSN 上，这与查询
// public 是同一个语义（CI 的 postgres:17 服务库 lingdoc_test 即此形态）。
//
// 类型比对按 #709 的裁定分方言：PostgreSQL 有真正的 boolean 类型，不存在
// SQLite 的存储类亲和问题，所以这一侧用**同族直比**——SQLite 侧那条
// bool↔INTEGER 方向性等价不携带过来（#709 风险①记录的方言分档）。
//
// 测试函数全部以 TestPostgres 前缀命名，直接被既有集成工作流
// （lingdoc-interface-integration.yml，job integration）的 '^TestPostgres'
// 选择器与 postgres:17 服务选中执行——本票不新增、不修改任何工作流或服务。
//
// PostgreSQL 侧只在 LINGDOC_TEST_POSTGRES_DSN 指向隔离测试库时运行。没有配置
// 时明确跳过——跳过不是通过（与 migration_full_down_test.go 的 skip 语义同源）。

import (
	"database/sql"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	// postgresAdoptionNullableVersion 是 000135_lingdoc_adoption_version_nullable
	// 的版本号：#693 修复在 PostgreSQL 侧把 chapter_version_id 改为可空。红夹具
	// 的语义站在它上面，所以链至少要建到这一版——建不到就红，不许静默通过。
	postgresAdoptionNullableVersion = 135
)

// postgresShapeGuardDSN 读取 DSN 门控。没有配置 LINGDOC_TEST_POSTGRES_DSN 时
// 显式跳过——跳过不是通过：验收矩阵按 NOT RUN 口径记录，由 integration 工作
// 流的真实运行兜底，skip 从不计为通过。
func postgresShapeGuardDSN(t *testing.T) string {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("LINGDOC_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("NOT RUN: LINGDOC_TEST_POSTGRES_DSN is unset; the PostgreSQL twin guard requires an isolated disposable PostgreSQL server. 跳过不是通过（a skip is never counted as a pass）")
	}
	return dsn
}

// migratedPostgresChainForShapeGuard 用真实的 versioned 迁移链在隔离演练
// schema 里建到最新，再打开它。产物侧永远不来自手写的 schema。
func migratedPostgresChainForShapeGuard(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dsn := postgresShapeGuardDSN(t)
	repoRoot := sqliteRepoRoot(t)
	chdirAndRestore(t, repoRoot)

	schema := newPostgresRehearsalSchema(t, dsn)
	db := openPostgresSchemaDB(t, dsn, schema)

	migrator := newPostgresMigrator(t, dsn, schema)
	require.NoError(t, migrator.Up(), "versioned 链必须能在隔离演练 schema 里建到最新")
	version, dirty, err := migrator.Version()
	require.NoError(t, err)
	require.False(t, dirty)
	require.GreaterOrEqual(t, version, uint(postgresAdoptionNullableVersion),
		"链必须至少建到 000135：红夹具的 chapter_version_id 可空语义站在它上面")
	return db, schema
}

// postgresTableShapes 从真实链产物（information_schema.columns）读出一个表的
// 全部列形状。is_nullable 是 'YES'/'NO'；character_maximum_length 喂进
// migrationColumnShape.Length，激活规则 4（长度）——#713 已把 delivery 两处
// 声明宽度漂移修正为与真实列一致，沉默即宽度相符，不是被跳过的比对。
func postgresTableShapes(t *testing.T, db *sql.DB, schema, table string) map[string]migrationColumnShape {
	t.Helper()
	rows, err := db.Query(
		"SELECT column_name, data_type, is_nullable, character_maximum_length "+
			"FROM information_schema.columns "+
			"WHERE table_schema = $1 AND table_name = $2 "+
			"ORDER BY ordinal_position",
		schema, table,
	)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	shapes := map[string]migrationColumnShape{}
	for rows.Next() {
		var shape migrationColumnShape
		var isNullable string
		require.NoError(t, rows.Scan(&shape.Name, &shape.DeclType, &isNullable, &shape.Length))
		shape.NotNull = isNullable == "NO"
		shapes[shape.Name] = shape
	}
	require.NoError(t, rows.Err())
	return shapes
}

// TestPostgresGuardedRowShapesMatchTheVersionedMigrationChain 是 PostgreSQL 侧
// 的绿样本：全部登记行结构（#709 的清单原样复用）对真实 versioned 链产物逐列
// 零违规。前置断言把 #694 绿样本的身份在两个方言上同时钉住——可空指向列 +
// 指针字段，证明可空性方向的比对真的执行过，而不是碰巧空集。
func TestPostgresGuardedRowShapesMatchTheVersionedMigrationChain(t *testing.T) {
	// #708 的绿样本按身份钉住：采纳幂等行必须留在真实表上。
	entry := guardedRowShapeEntry("adoptionIdempotencyRow")
	require.NotNil(t, entry, "the #694 green sample must stay on the checklist")
	require.Equal(t, "github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption", entry.PackagePath)
	require.Equal(t, "lingdoc_candidate_adoptions", entry.TableName)

	parsed, err := parseRowShape(entry.model)
	require.NoError(t, err)
	field, ok := parsed.FieldsByDBName["chapter_version_id"]
	require.True(t, ok, "the adoption idempotency row must declare chapter_version_id")
	require.Equal(t, reflect.Pointer, field.FieldType.Kind(),
		"the #694 fix made the field a pointer (*string)")
	require.False(t, field.NotNull, "the green sample's pointer must not carry a not null tag")

	db, schema := migratedPostgresChainForShapeGuard(t)
	violations := runRowShapeGuardAgainst(t, dialectPostgreSQL, guardedRowShapes, func(t *testing.T, table string) map[string]migrationColumnShape {
		return postgresTableShapes(t, db, schema, table)
	})
	require.Empty(t, violations,
		"every registered row structure and the real versioned chain must agree on every guarded column")
}

// TestPostgresGuardedTablesCoverEveryVersionedMigrationColumn 是规则 1 的反向
// 在 PostgreSQL 侧的孪生（#713）：versioned 链建出的每一列都必须被同表至少
// 一个登记行结构声明——同表多结构体按并集合并，部分投影合法；没有任何结构体
// 声明的列才是违规。长度规则在本侧激活（character_maximum_length）。
// 末尾的表清单网与 SQLite 侧同源（#713 评审 F4）：链建出的每张 lingdoc 表
// 都必须有清单登记，否则新建无行结构的表会两边静默。
func TestPostgresGuardedTablesCoverEveryVersionedMigrationColumn(t *testing.T) {
	db, schema := migratedPostgresChainForShapeGuard(t)
	violations := runRowShapeColumnCoverageAgainst(t, dialectPostgreSQL, guardedRowShapes, func(t *testing.T, table string) map[string]migrationColumnShape {
		return postgresTableShapes(t, db, schema, table)
	})
	require.Empty(t, violations,
		"every column the versioned migration chain builds must be declared by at least one registered row structure")

	registeredTables := map[string]bool{}
	for _, reg := range guardedRowShapes {
		registeredTables[reg.TableName] = true
	}
	names, err := db.Query(
		"SELECT table_name FROM information_schema.tables "+
			"WHERE table_schema = $1 AND table_name LIKE 'lingdoc%'",
		schema,
	)
	require.NoError(t, err)
	defer func() { _ = names.Close() }()
	for names.Next() {
		var table string
		require.NoError(t, names.Scan(&table))
		require.Truef(t, registeredTables[table],
			"versioned migration chain builds table %s but no checklist entry registers it — register a row structure (or adjudicate explicitly), never silently", table)
	}
	require.NoError(t, names.Err())
}

// --- 守护自测红夹具 ----------------------------------------------------------
//
// 以下夹具是守护自测，永远不进登记清单：每一个都是故意写错的声明，指向真实
// versioned 链的产物（不是手写假 schema），证明守护在 PostgreSQL 侧真的会红。

// postgresRowShapeGuardNotNullOnNullableFixture 复刻 #693 形态（PostgreSQL 侧）：
// 值类型 + `not null` 声明，指向 000135 真的改成可空的那一列（可空性第一方向）。
type postgresRowShapeGuardNotNullOnNullableFixture struct {
	ChapterVersionID string `gorm:"not null;size:36"`
}

func (postgresRowShapeGuardNotNullOnNullableFixture) TableName() string {
	return "lingdoc_candidate_adoptions"
}

func TestPostgresRowShapeGuardFlagsNotNullDeclarationAgainstANullableColumn(t *testing.T) {
	db, schema := migratedPostgresChainForShapeGuard(t)

	registrations := []rowShapeRegistration{{
		PackagePath: "github.com/Tencent/WeKnora/internal/database",
		TypeName:    "postgresRowShapeGuardNotNullOnNullableFixture",
		TableName:   "lingdoc_candidate_adoptions",
		model:       &postgresRowShapeGuardNotNullOnNullableFixture{},
	}}
	violations := runRowShapeGuardAgainst(t, dialectPostgreSQL, registrations, func(t *testing.T, table string) map[string]migrationColumnShape {
		return postgresTableShapes(t, db, schema, table)
	})

	violation, found := findRowShapeViolation(violations, "chapter_version_id", rowShapeNullability)
	require.Truef(t, found,
		"the PostgreSQL twin must flag a not-null declaration over a column the versioned chain allows NULL on; got %v", violations)

	message := violation.Error()
	require.Contains(t, message, "lingdoc_candidate_adoptions", "failure must name the table")
	require.Contains(t, message, "chapter_version_id", "failure must name the column")
	require.Contains(t, message, "NOT NULL", "failure must show the declared nullability")
	require.Contains(t, message, "nullable", "failure must show the artifact's nullability")
}

// postgresRowShapeGuardPointerOnNotNullFixture 是反向可空性方向：指针字段、无
// `not null` 标签，指向 000099 建为 varchar(36) NOT NULL 的 project_id（可空性
// 第二方向）。
type postgresRowShapeGuardPointerOnNotNullFixture struct {
	ProjectID *string
}

func (postgresRowShapeGuardPointerOnNotNullFixture) TableName() string {
	return "lingdoc_candidate_adoptions"
}

func TestPostgresRowShapeGuardFlagsPointerDeclarationAgainstANotNullColumn(t *testing.T) {
	db, schema := migratedPostgresChainForShapeGuard(t)

	registrations := []rowShapeRegistration{{
		PackagePath: "github.com/Tencent/WeKnora/internal/database",
		TypeName:    "postgresRowShapeGuardPointerOnNotNullFixture",
		TableName:   "lingdoc_candidate_adoptions",
		model:       &postgresRowShapeGuardPointerOnNotNullFixture{},
	}}
	violations := runRowShapeGuardAgainst(t, dialectPostgreSQL, registrations, func(t *testing.T, table string) map[string]migrationColumnShape {
		return postgresTableShapes(t, db, schema, table)
	})

	violation, found := findRowShapeViolation(violations, "project_id", rowShapeNullability)
	require.Truef(t, found,
		"the PostgreSQL twin must flag a pointer declaration over a NOT NULL column; got %v", violations)

	message := violation.Error()
	require.Contains(t, message, "lingdoc_candidate_adoptions", "failure must name the table")
	require.Contains(t, message, "project_id", "failure must name the column")
	require.Contains(t, message, "nullable", "failure must show the declared nullability")
	require.Contains(t, message, "NOT NULL", "failure must show the artifact's nullability")
}

// postgresRowShapeGuardLengthFixture 是规则 4 的常驻红夹具（#713 评审 F2）：
// `type:varchar(35)` 拼写（gorm 不把它的长度解析进 Size），指向真实
// varchar(36) 的 lingdoc_release_snapshots.project_id（000103）。它同时钉住
// declaredRowShapeWidth 的宽度提取管道——没有它，type: 拼写的列在 PG 侧
// 静默绕过长度比对（假绿），这正是评审抓到的形态。
type postgresRowShapeGuardLengthFixture struct {
	ProjectID string `gorm:"column:project_id;not null;type:varchar(35)"`
}

func (postgresRowShapeGuardLengthFixture) TableName() string {
	return "lingdoc_release_snapshots"
}

func TestPostgresRowShapeGuardFlagsDeclaredWidthAgainstAMismatchingColumn(t *testing.T) {
	db, schema := migratedPostgresChainForShapeGuard(t)

	registrations := []rowShapeRegistration{{
		PackagePath: "github.com/Tencent/WeKnora/internal/database",
		TypeName:    "postgresRowShapeGuardLengthFixture",
		TableName:   "lingdoc_release_snapshots",
		model:       &postgresRowShapeGuardLengthFixture{},
	}}
	violations := runRowShapeGuardAgainst(t, dialectPostgreSQL, registrations, func(t *testing.T, table string) map[string]migrationColumnShape {
		return postgresTableShapes(t, db, schema, table)
	})

	violation, found := findRowShapeViolation(violations, "project_id", rowShapeLengthMismatch)
	require.Truef(t, found,
		"the PostgreSQL twin must flag a declared width (from the type: spelling) over a column of a different enforced length; got %v", violations)

	message := violation.Error()
	require.Contains(t, message, "lingdoc_release_snapshots", "failure must name the table")
	require.Contains(t, message, "project_id", "failure must name the column")
	require.Contains(t, message, "width 35", "failure must show the declared width")
	require.Contains(t, message, "length 36", "failure must show the artifact's enforced length")
}

// postgresRowShapeGuardBooleanFixture 钉住 PostgreSQL 侧的同族直比：声明的
// bool 对上真实链的 boolean 列（000103 的 is_current boolean NOT NULL）——
// 同族直比下这不是违规，也不需要 SQLite 侧那条方向性等价。
type postgresRowShapeGuardBooleanFixture struct {
	IsCurrent bool `gorm:"column:is_current;not null"`
}

func (postgresRowShapeGuardBooleanFixture) TableName() string { return "lingdoc_release_snapshots" }

func TestPostgresRowShapeGuardComparesDeclaredBoolAgainstBooleanColumnsDirectly(t *testing.T) {
	// 方言分档的棘轮：PostgreSQL 侧同族直比——SQLite 侧的 bool↔INTEGER 等价
	// 不携带过来，整数声明在 boolean 列上借不了道。
	require.True(t, typeFamiliesAgreeOnDialect(dialectPostgreSQL, "bool", "bool"))
	require.False(t, typeFamiliesAgreeOnDialect(dialectPostgreSQL, "integer", "bool"))
	require.False(t, typeFamiliesAgreeOnDialect(dialectPostgreSQL, "bool", "integer"))
	require.False(t, typeFamiliesAgreeOnDialect(dialectPostgreSQL, "string", "integer"))

	db, schema := migratedPostgresChainForShapeGuard(t)
	registrations := []rowShapeRegistration{{
		PackagePath: "github.com/Tencent/WeKnora/internal/database",
		TypeName:    "postgresRowShapeGuardBooleanFixture",
		TableName:   "lingdoc_release_snapshots",
		model:       &postgresRowShapeGuardBooleanFixture{},
	}}
	violations := runRowShapeGuardAgainst(t, dialectPostgreSQL, registrations, func(t *testing.T, table string) map[string]migrationColumnShape {
		return postgresTableShapes(t, db, schema, table)
	})
	require.Empty(t, violations,
		"a declared bool over the versioned chain's boolean column must not be a violation on PostgreSQL; got %v", violations)
}
