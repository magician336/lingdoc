package database

// 本文件（#568）守住一件事：000001_agent.down.sql 撤销 up 侧那次
// models.tenant_id 回填时，用的**清单来源必须与 up 侧同源**。
//
// 背景：000001_agent.up.sql 的数据迁移段用一份 tenant_source 清单
// （knowledge_bases.embedding_model_id ＋ knowledge_bases.summary_model_id）
// 把「被知识库引用的模型」的 models.tenant_id 从 0 抬成知识库的租户号。
// down 侧要把这件事倒回去，就必须用**同一份清单**。
//
// 原来的 down 侧写的是
//
//	UPDATE models m SET tenant_id = 0 FROM knowledges k
//	WHERE m.id = k.id AND m.tenant_id = k.tenant_id;
//
// models.id 与 knowledges.id 是两张表各自独立生成的随机 UUIDv4 主键（中间
// 没有外键），所以这个关联键几乎匹配不到任何行 ⇒ up 侧那次回填**从来没有被
// 撤销过**。「迁移可回滚」在数据语义这一层是假的。
//
// 本文件有四道断言，分两层：
//
//  1. 静态层（不需要数据库）：down 的清单必须与 up 的清单**逐条同源**，
//     两条 UPDATE 必须是同一关联键、同一过滤条件的镜像。有人改了 up 的清单
//     却不同步 down，这里立刻转红。
//
//  2. 行为层（真实 PostgreSQL）：摆出「被知识库引用的模型」与「id 撞车的
//     模型」两类行，跑 up 再跑 down，断言 up 抬高的行被**精确复位**、撞车的
//     行**一动不动**。
//
// PostgreSQL 侧只在 LINGDOC_TEST_POSTGRES_DSN 指向隔离测试库时运行。没有配置
// 时明确跳过——跳过不是通过。

import (
	"database/sql"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	// agentUpMigration 是 up 侧那条迁移的文件名。
	agentUpMigration = "000001_agent.up.sql"
	// agentDownMigration 是 down 侧那条迁移的文件名。
	agentDownMigration = "000001_agent.down.sql"
)

// TestAgentDownTenantSourceMirrorsUpSourceList 断言 down 侧的清单与 up 侧
// 同源，两条 UPDATE 互为镜像。
//
// 这道断言是**结构**上的，不是行号上的：迁移文件怎么排版都不影响它。有人
// 把 up 的清单改了而没同步 down（或反过来），它立刻转红。
func TestAgentDownTenantSourceMirrorsUpSourceList(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)
	upBranches, upStatement := agentParseTenantSource(t,
		fullDownReadMigrationFile(t, repoRoot, agentUpMigration))
	downBranches, downStatement := agentParseTenantSource(t,
		fullDownReadMigrationFile(t, repoRoot, agentDownMigration))

	// 1. 同一份清单：逐条比对「模型 id 从哪来」与「租户号从哪来」。
	require.Equal(t,
		agentSortedBranches(upBranches),
		agentSortedBranches(downBranches),
		"down 的 tenant_source 清单必须与 up 同源：up=%v down=%v",
		upBranches, downBranches)

	// 2. 裁定点名的两条来源必须都在（清单不能被掏空成空集）。
	require.Subset(t,
		agentBranchSources(upBranches),
		[]string{
			"knowledge_bases.embedding_model_id",
			"knowledge_bases.summary_model_id",
		},
		"up 侧的清单必须包含裁定点名的两条来源")

	// 3. 镜像：同一个关联键（m.id = ts.model_id）、同一个过滤条件
	//    （m.tenant_id = ts.tenant_id ↔ m.tenant_id = 0）、相反的写回方向。
	require.Contains(t, upStatement,
		"update models m set tenant_id = ts.tenant_id from tenant_source ts",
		"up 侧必须把 tenant_id 抬成 ts.tenant_id")
	require.Contains(t, upStatement,
		"where m.id = ts.model_id and m.tenant_id = 0",
		"up 侧只抬 tenant_id 还是 0 的模型")
	require.Contains(t, downStatement,
		"update models m set tenant_id = 0 from tenant_source ts",
		"down 侧必须把 tenant_id 复位成 0")
	require.Contains(t, downStatement,
		"where m.id = ts.model_id and m.tenant_id = ts.tenant_id",
		"down 侧只复位 up 抬过的那一行（m.tenant_id 必须等于 ts.tenant_id）")
}

// TestAgentDownDoesNotJoinModelsToKnowledgesByID 断言 down 侧不再按
// "m.id = k.id" 关联 models 与 knowledges。
//
// models.id 与 knowledges.id 是两张表各自独立生成的随机 UUIDv4 主键，把它们
// 当成同一个东西的关联键，是 #568 修掉的原始缺陷。这道断言单独钉住它，
// 免得有人日后把旧写法抄回来。
func TestAgentDownDoesNotJoinModelsToKnowledgesByID(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)
	down := agentNormalizeSQL(
		fullDownStripSQLComments(fullDownReadMigrationFile(t, repoRoot, agentDownMigration)))

	require.NotContains(t, down, "m.id = k.id",
		"down 侧不得按 models.id = knowledges.id 关联：两个 UUID 空间各自独立")
	require.NotContains(t, down, "join knowledges k",
		"down 侧不得把 knowledges 按 id 接到 models 上")
}

// TestPostgresAgentDownRestoresModelsTenantID 在**真实 PostgreSQL** 上演练
// 一次 up → down，断言「up 抬高的行被精确复位、没被 up 碰过的行一动不动」。
//
// 这道用例与 #564 的那道是两条不同的断言：#564 断言「down 链能走到 0 且不留
// 残骸」，本道断言「**数据**回到 up 之前」。链能走到 0 并不代表数据被还原了
// —— 原先那条语句就是链走到底、数据却原封不动。
//
// 没有配置 LINGDOC_TEST_POSTGRES_DSN 时明确跳过：跳过不是通过。
func TestPostgresAgentDownRestoresModelsTenantID(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("LINGDOC_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("NOT RUN: LINGDOC_TEST_POSTGRES_DSN is unset; requires an isolated disposable PostgreSQL server")
	}
	repoRoot := sqliteRepoRoot(t)
	chdirAndRestore(t, repoRoot)

	schema := newPostgresRehearsalSchema(t, dsn)
	db := openPostgresSchemaDB(t, dsn, schema)
	migrator := newPostgresMigrator(t, dsn, schema)

	// 只跑 000000（建表），这样夹具是摆在 000001 的 up **之前** 的。
	require.NoError(t, migrator.Steps(1))
	version, dirty, err := migrator.Version()
	require.NoError(t, err)
	require.False(t, dirty)
	require.Equal(t, uint(0), version)

	agentSeedRehearsalRows(t, db)
	before := agentModelTenantIDs(t, db)
	require.Equal(t, map[string]int{
		// 被知识库引用（embedding / summary 两条来源）
		"model-alpha": 0,
		"model-beta":  0,
		// 只被某条知识引用、没有被知识库引用
		"model-gamma": 42,
		// id 与某条知识的 id 撞车
		"model-delta": 7,
	}, before, "夹具摆错了")

	// --- 跑 000001 的 up -----------------------------------------------------
	require.NoError(t, migrator.Steps(1))
	version, dirty, err = migrator.Version()
	require.NoError(t, err)
	require.False(t, dirty)
	require.Equal(t, uint(1), version)

	afterUp := agentModelTenantIDs(t, db)
	require.Equal(t, map[string]int{
		"model-alpha": 42,
		"model-beta":  42,
		"model-gamma": 42,
		"model-delta": 7,
	}, afterUp, "up 侧只应该抬高被知识库引用的那两行")

	// --- 跑 000001 的 down ---------------------------------------------------
	require.NoError(t, migrator.Steps(-1))
	version, dirty, err = migrator.Version()
	require.NoError(t, err)
	require.False(t, dirty)
	require.Equal(t, uint(0), version)

	afterDown := agentModelTenantIDs(t, db)
	require.Equal(t, before, afterDown,
		"down 之后 models.tenant_id 必须逐行回到 up 之前：撞车的行不能动，"+
			"只被知识引用的行不能被多复位")

	t.Logf("models.tenant_id 前后对照：\nbefore up  = %v\nafter up   = %v\nafter down = %v",
		before, afterUp, afterDown)
}

// agentSeedRehearsalRows 摆出演练要用的行。
//
// 四类模型行：
//
//   - model-alpha / model-beta：tenant_id = 0，被知识库 kb-alpha 引用
//     （一条走 embedding_model_id，一条走 summary_model_id）⇒ up 会抬高它们。
//   - model-gamma：tenant_id = 42，只被某条**知识**引用，没有任何知识库引用它
//     ⇒ up 不碰它，down 也不许碰它（它守着「不许多复位」）。
//   - model-delta：tenant_id = 7，**没有任何**引用它；但有一条知识的 id 恰好
//     叫 "model-delta" ⇒ id 撞车。up 不碰它，down 也不许碰它。
//
// 知识库两条：kb-alpha（tenant 42，引用 alpha 与 beta）、kb-collision
// （tenant 7，不引用任何模型）。
//
// 所有模型都被引用过，所以 000001 的 up 里那段「软删没被引用的模型」不会给
// 本次演练引入噪声（它只动 deleted_at，与本道断言无关）。
func agentSeedRehearsalRows(t *testing.T, db *sql.DB) {
	t.Helper()

	_, err := db.Exec(`
		INSERT INTO knowledge_bases
			(id, name, tenant_id, embedding_model_id, summary_model_id, rerank_model_id)
		VALUES
			('kb-alpha', 'kb alpha', 42, 'model-alpha', 'model-beta', ''),
			('kb-collision', 'kb collision', 7, '', '', '')
	`)
	require.NoError(t, err)

	_, err = db.Exec(`
		INSERT INTO knowledges
			(id, tenant_id, knowledge_base_id, type, title, source, embedding_model_id)
		VALUES
			('k-ref', 42, 'kb-alpha', 'file', 'ref', 'source', 'model-alpha'),
			('k-gamma', 42, 'kb-alpha', 'file', 'gamma', 'source', 'model-gamma'),
			('model-delta', 7, 'kb-collision', 'file', 'collision', 'source', 'model-delta')
	`)
	require.NoError(t, err)

	_, err = db.Exec(`
		INSERT INTO models (id, tenant_id, name, type, source, parameters)
		VALUES
			('model-alpha', 0, 'alpha', 'embedding', 'local', '{}'),
			('model-beta', 0, 'beta', 'embedding', 'local', '{}'),
			('model-gamma', 42, 'gamma', 'embedding', 'local', '{}'),
			('model-delta', 7, 'delta', 'embedding', 'local', '{}')
	`)
	require.NoError(t, err)
}

// agentModelTenantIDs 读出 models 表此刻的 id → tenant_id。
func agentModelTenantIDs(t *testing.T, db *sql.DB) map[string]int {
	t.Helper()

	rows, err := db.Query("SELECT id, tenant_id FROM models ORDER BY id")
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	tenants := map[string]int{}
	for rows.Next() {
		var id string
		var tenantID int
		require.NoError(t, rows.Scan(&id, &tenantID))
		tenants[id] = tenantID
	}
	require.NoError(t, rows.Err())
	return tenants
}

// agentTenantSourceBranch 描述 tenant_source CTE 里的一个来源分支：
// 「模型 id 从哪张表的哪一列来」＋「租户号从哪张表的哪一列来」。
type agentTenantSourceBranch struct {
	Source string
	Tenant string
}

// agentParseTenantSource 解析一个 SQL 文件里的 tenant_source CTE。
//
// 它返回两样东西：分支清单，以及紧跟其后的那条 UPDATE 语句（空白归一化后的
// 小写文本）。两者都是**结构**，不是行号：文件怎么排版都不影响解析结果。
func agentParseTenantSource(t *testing.T, sqlText string) ([]agentTenantSourceBranch, string) {
	t.Helper()

	cleaned := fullDownStripSQLComments(sqlText)
	lower := strings.ToLower(cleaned)

	const marker = "tenant_source as ("
	index := strings.Index(lower, marker)
	require.NotEqual(t, -1, index,
		"这个文件里没有 tenant_source CTE：down 侧必须与 up 侧用同一份清单")
	bodyStart := index + len(marker)

	depth := 1
	end := -1
	for i := bodyStart; i < len(cleaned) && end < 0; i++ {
		switch cleaned[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				end = i
			}
		}
	}
	require.NotEqual(t, -1, end, "tenant_source CTE 没有闭合")

	branches := []agentTenantSourceBranch{}
	for _, raw := range agentSplitTopLevelUnion(cleaned[bodyStart:end]) {
		branch, ok := agentParseTenantSourceBranch(raw)
		require.True(t, ok, "读不出 tenant_source 的分支：%s", strings.TrimSpace(raw))
		branches = append(branches, branch)
	}
	require.NotEmpty(t, branches, "tenant_source CTE 至少要有一个来源分支")

	statement := cleaned[end+1:]
	if semicolon := strings.Index(statement, ";"); semicolon >= 0 {
		statement = statement[:semicolon]
	}
	return branches, agentNormalizeSQL(statement)
}

// agentParseTenantSourceBranch 解析一个分支：
//
//	SELECT kb.embedding_model_id AS model_id, kb.tenant_id
//	FROM knowledge_bases kb
//	WHERE ...
//
// 它把别名还原成表名，得到 "knowledge_bases.embedding_model_id" 这种与写法
// 无关的来源。
func agentParseTenantSourceBranch(raw string) (agentTenantSourceBranch, bool) {
	text := agentNormalizeSQL(raw)
	lower := strings.ToLower(text)

	if !strings.HasPrefix(lower, "select ") {
		return agentTenantSourceBranch{}, false
	}
	fromIndex := agentIndexTopLevel(text, " from ")
	if fromIndex < 0 {
		return agentTenantSourceBranch{}, false
	}
	selectList := text[len("select "):fromIndex]
	fromPart := text[fromIndex+len(" from "):]

	items := agentSplitTopLevelCommas(selectList)
	if len(items) != 2 {
		return agentTenantSourceBranch{}, false
	}
	modelExpr := agentStripAliasKeyword(items[0], "model_id")
	tenantExpr := items[1]

	table, alias, ok := agentParseFromClause(fromPart)
	if !ok {
		return agentTenantSourceBranch{}, false
	}
	return agentTenantSourceBranch{
		Source: agentQualify(modelExpr, alias, table),
		Tenant: agentQualify(tenantExpr, alias, table),
	}, true
}

// agentSplitTopLevelUnion 按**顶层** UNION 切开 CTE 的分支。
//
// 顶层 = 不在任何括号里；分支内部子查询里的 UNION 不算。
func agentSplitTopLevelUnion(body string) []string {
	parts := []string{}
	depth := 0
	start := 0
	lower := strings.ToLower(body)

	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '(':
			depth++
			continue
		case ')':
			depth--
			continue
		}
		if depth != 0 || !strings.HasPrefix(lower[i:], "union") {
			continue
		}
		if i > 0 && agentIsIdentChar(body[i-1]) {
			continue
		}
		if i+len("union") < len(body) && agentIsIdentChar(body[i+len("union")]) {
			continue
		}
		parts = append(parts, body[start:i])
		i += len("union") - 1
		start = i + 1
	}
	return append(parts, body[start:])
}

// agentSplitTopLevelCommas 按顶层逗号切开 SELECT 列表。
func agentSplitTopLevelCommas(list string) []string {
	parts := []string{}
	depth := 0
	start := 0
	inString := false

	for i := 0; i < len(list); i++ {
		switch list[i] {
		case '\'':
			inString = !inString
			continue
		case '(':
			if !inString {
				depth++
			}
			continue
		case ')':
			if !inString {
				depth--
			}
			continue
		}
		if inString || depth != 0 || list[i] != ',' {
			continue
		}
		parts = append(parts, list[start:i])
		start = i + 1
	}
	return append(parts, list[start:])
}

// agentIndexTopLevel 找出关键字在**顶层**（不在括号里、不在字符串里）第一次
// 出现的位置；找不到返回 -1。
func agentIndexTopLevel(text, keyword string) int {
	lower := strings.ToLower(text)
	depth := 0
	inString := false

	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '\'':
			inString = !inString
			continue
		case '(':
			if !inString {
				depth++
			}
			continue
		case ')':
			if !inString {
				depth--
			}
			continue
		}
		if inString || depth != 0 {
			continue
		}
		if strings.HasPrefix(lower[i:], keyword) {
			return i
		}
	}
	return -1
}

// agentStripAliasKeyword 去掉 SELECT 项尾部的 "AS <keyword>"。
func agentStripAliasKeyword(item, keyword string) string {
	lower := strings.ToLower(item)
	marker := " as " + keyword
	index := strings.LastIndex(lower, marker)
	if index < 0 {
		return item
	}
	if strings.TrimSpace(item[index+len(marker):]) != "" {
		return item
	}
	return strings.TrimSpace(item[:index])
}

// agentParseFromClause 解析 FROM 子句的表名与别名。
func agentParseFromClause(fromPart string) (table string, alias string, ok bool) {
	fields := strings.Fields(fromPart)
	if len(fields) == 0 {
		return "", "", false
	}
	table = fields[0]
	if !agentIsIdentChar(table[0]) {
		return "", "", false
	}
	if len(fields) > 1 && agentIsIdentChar(fields[1][0]) {
		alias = fields[1]
	}
	return table, alias, true
}

// agentQualify 把 "kb.embedding_model_id" 这种别名限定名还原成
// "knowledge_bases.embedding_model_id"。
//
// 还原不了就原样返回（不猜）：解析不出来的地方由调用方的断言去报。
func agentQualify(expr, alias, table string) string {
	expr = strings.TrimSpace(expr)
	prefix, column, found := strings.Cut(expr, ".")
	if !found {
		return expr
	}
	if alias != "" && strings.EqualFold(prefix, alias) {
		return strings.ToLower(table) + "." + strings.ToLower(column)
	}
	return strings.ToLower(expr)
}

// agentBranchSources 取出一份清单里的全部来源列。
func agentBranchSources(branches []agentTenantSourceBranch) []string {
	sources := make([]string, 0, len(branches))
	for _, branch := range branches {
		sources = append(sources, branch.Source)
	}
	return sources
}

// agentSortedBranches 把清单按来源列排序，让比对与书写顺序无关。
func agentSortedBranches(branches []agentTenantSourceBranch) []agentTenantSourceBranch {
	sorted := make([]agentTenantSourceBranch, len(branches))
	copy(sorted, branches)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Source != sorted[j].Source {
			return sorted[i].Source < sorted[j].Source
		}
		return sorted[i].Tenant < sorted[j].Tenant
	})
	return sorted
}

// agentNormalizeSQL 把 SQL 归一化成小写单空格文本，让比对与排版无关。
func agentNormalizeSQL(text string) string {
	return strings.Join(strings.Fields(strings.ToLower(text)), " ")
}

// agentIsIdentChar 判断一个字节是不是标识符字符。
func agentIsIdentChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z':
		return true
	case c >= 'A' && c <= 'Z':
		return true
	case c >= '0' && c <= '9':
		return true
	case c == '_':
		return true
	}
	return false
}
