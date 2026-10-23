# Spec：测试夹具与生产 schema 同源

> 发布副本：[Issue #454](https://github.com/magician336/lingdoc/issues/454)。
> 定稿依据：[#442](https://github.com/magician336/lingdoc/issues/442) §2.1（编号 **56**）｜研究底稿：`research/new-spec-candidates` @ `9ae5d9fb`（只推分支、未合入 `main`）。
> 施工并行面：**待切**——按 [#441](https://github.com/magician336/lingdoc/issues/441) §0.2，本 Spec 的仓内文档写出来之后，它的施工面才能切。
> 相关：[#416](https://github.com/magician336/lingdoc/issues/416)（本 Spec 解锁它）、[#386](https://github.com/magician336/lingdoc/issues/386)（同一类机制的另一处）、[#48](https://github.com/magician336/lingdoc/issues/48)（代码命名规范收口——**拥有命名，不拥有 schema 形状**）。

## Problem Statement

**测试夹具各自持有一份生产 schema 的形状**——这是机制性欠账，不是某一次事故。

PF-3（[#327](https://github.com/magician336/lingdoc/issues/327)）给 `lingdoc_project_audits` 加两列（`target_version` / `project_revision`）时，**29 条用例被打红**，而失败被 HTTP 层映射成不透明的 `500 internal_error`，看不出是 schema 漂移。PF-3 把本轮撞到的 6 处逐处修掉了（`docs/08-本轮实施方案/PF-3-审计告警接线与AuditEvent字段.md:262-268`），**但机制还在**：下一次给任何表加列，仍会重演。

**三种自持形态并存**：

1. **手抄列清单**——夹具自己写一份列名。仍存活的例子：`internal/lingdoc/candidateadoption/candidate_adoption_test.go:154`（`CREATE TABLE lingdoc_project_audits ( id TEXT PRIMARY KEY, project_id TEXT NOT NULL, …`）、`adoption_confirmation_test.go:223`（同形）、`internal/lingdoc/membership/fixture_test.go:253-265`（镜像的 `auditEventRow` + `TableName()`）。
2. **手抄迁移子集**——夹具只把迁移跑到某个号就停。仍存活的例子：`internal/router/lingdoc_integration_test.go:118,124,125`（sqlite 子集）与 `:352-353`、`internal/container/lingdoc_ruleset_delivery_e2e_test.go:162-164`、`internal/lingdoc/workspace/save_chapter_policy_test.go:144-145`、`internal/router/routes_lingdoc_owner_test.go:93-98`。
3. **版本常量**——`const expectedSQLiteMigrationVersion = 53`（`internal/database/migration_sqlite_versioned_schema_test.go:102`，断言于 `:113` 与 `:175`）。

形态 2 的隐蔽后果：**夹具看不出自己止于哪一步**。「跑到 000042」被当成「跑完了」——而生产已经到 000053 了。

**仓内已经有正确方向的先例，但只覆盖一处。** `internal/lingdoc/workspace/audit_event_table_fixture_test.go:21-41` 的 `ensureAuditEventTable()` 先建基表、再用 `os.ReadFile(filepath.Join("..","..","..","migrations","sqlite","000052_lingdoc_audit_version_fields.up.sql"))` **应用生产迁移**，并用 `PRAGMA table_info`（`:46-59`）防重复应用。⇒ 方向被验证过，但**它硬编码了单个迁移文件名**，仍然不是「整条迁移链」；而「整目录跑迁移」的先例只存在于 `internal/database/` 下的迁移测试里（`migration_candidate_shape_upgrade_test.go:815`、`migration_sqlite_candidate_shape_test.go:153` 的 `migrate.New("file://migrations/versioned", …)`）。

## Solution

让「**夹具应用生产迁移**」成为**默认约定**，并用机器守护「**模型字段集合 == 表列集合**」。

- 夹具不再手抄列清单、不再手抄迁移子集——它们**应用生产迁移**。
- 「止于哪一步」变成**显式事实**：夹具要应用到哪个迁移号是写出来的、且与生产当前版本一致，**不一致即红**。
- 一条机器守护：模型声明的字段集合 == 实际表列集合。

## User Stories

1. As a developer, I want my test fixture to apply the production migrations, so that adding a column does not silently break my fixture.
2. As a developer, I want to know exactly which migration my fixture stopped at, so that I do not mistake "applied up to 42" for "up to date".
3. As a developer, I want a fixture that hard-codes a column list to be flagged, so that the next copy of it never gets written.
4. As a developer, I want a fixture that hard-codes a migration file name to be flagged, so that the "apply one migration by hand" trick does not spread.
5. As a developer, I want the fixture's expected migration version to be checked against the production one, so that a stale constant is red rather than quietly wrong.
6. As a QA 负责人, I want a guard asserting that a model's declared fields equal the table's columns, so that a field added to the model but not the table is caught immediately.
7. As a QA 负责人, I want adding a column to a production table to break nothing in the test suite, so that a schema change does not become an incident.
8. As a QA 负责人, I want the column-set guard to run in CI, so that the invariant is checked on every change, not only when someone remembers.
9. As a 审阅者, I want the fixtures to read their shape from the production migrations, so that a reviewer can see there is no second source of truth.
10. As a 审阅者, I want the migration pair check to keep holding, so that adding a table does not leave a half-migrated dialect behind.
11. As a 项目负责人, I want the next schema change to cost one migration instead of a batch of red tests, so that the test suite stays a signal rather than a noise source.
12. As a developer, I want the fixtures I do not migrate in this round to still be listed, so that the remaining debt is visible rather than forgotten.

## Implementation Decisions

1. **夹具应用的是「整条生产迁移链」，不是「跑到某个号」。** 「跑迁移子集」这个形态本身要被消灭：它的隐蔽后果正是「看不出止于哪一步」。⇒ 夹具要么**跑完整链**，要么**显式声明它跑的是哪一段且被显式标记为「局部夹具」**——后者是**例外且要登记**，不是默认。

2. **「止于哪一步」变成可检查的显式事实。** 夹具声明它应用的迁移版本；该声明与生产当前版本不一致 ⇒ 红。仓内已有的 `expectedSQLiteMigrationVersion`（`internal/database/migration_sqlite_versioned_schema_test.go:102`）就是这条声明的现成形态，**沿用它**——但它的值必须**由生产迁移目录推导**，不能手改。

3. **「模型字段集合 == 表列集合」的守护是 Go 测试，放在 `internal/database/`。** 它要**反射读 Go 结构体**并与**实际表列**（`PRAGMA table_info` / `information_schema`）对照 ⇒ Go 是自然语言。放在 `internal/database/` 是因为它与被测的迁移/schema 同属一处（既有的 `migration_sqlite_versioned_schema_test.go` 也在那里）。**守护**：给表加一列而模型没加 ⇒ 红；反之亦然。

4. **「夹具不得手抄形状」的守护是 Python 测试，放 `scripts/ci/test_lingdoc_*.py`。** 它扫源码，发现**手抄的 `CREATE TABLE` 列清单**、**硬编码的迁移文件名**、**手改的迁移版本常量** ⇒ 红。放 `scripts/ci/` 的理由：CI 的 `Test CI scope scripts` 步骤按 `python -m unittest discover -s scripts/ci -p 'test_lingdoc_*.py' -v`（`.github/workflows/lingdoc-review.yml:55-57`）**自动发现** ⇒ **不用改 workflow 就进 CI**。这条是**静态扫描**，用 Python 扫 Go 源码最省事。

5. **两半守护各司其职，不合并成一个。** Go 守护管**语义**（模型 ↔ 表列），Python 守护管**形态**（有没有手抄）。理由：形态扫描用 Python 写一行正则就够，语义对照用反射才写得动；硬合成一个会让两边都别扭。

6. **「故意加一列 ⇒ 全仓不出现新增失败」是回归门槛，不是一条常驻守护。** 它需要**全仓跑一遍**才能判定 ⇒ 它是**一次性的验证动作**，落成验证报告与门槛记录，不是 CI job。判据照 #416 的验收原文。

7. **新文件的名字不能带编号。** 命名登记表（`scripts/lingdoc_naming_registry.py`，数据在 `NAMED_FILES`）的规则：`NUMBERED_NAME = ^[gGtT]\d`（新增代码文件不得以 `g<数字>` / `t<数字>` 开头）、`SEGMENT_NAME`（不得以 `seg<数字>_` 开头）、`TICKET_NUMBER`（文件名不得含 `issue<数字>` / `spec<数字>` / `ac<数字>`）。⇒ **本 Spec 的新测试文件里不能出现 `56`**（`test_lingdoc_56_*` 会被判违规）。名字按「词干命名被测模块」的既有约定（`lingdoc_naming_registry.py:170-171`）取，例如 `scripts/ci/test_lingdoc_fixture_schema_source.py`。

8. **共享维护点不归本面。** PF-3 方案档 `:270` 已记：`internal/router/lingdoc_integration_test.go` 与 `internal/database/migration_sqlite_versioned_schema_test.go` 按登记处的「共享维护点」约定修改，**不登记进任何面的路径集**。⇒ 本面**不需要**申请这两条路径。

9. **加表 / 加列要同步三处，不止两处。** `migrations/sqlite/`、`migrations/versioned/`，**以及 Go 内嵌 DDL**（`internal/lingdoc/templategovernance/schema.go:28-41` 的 sqlite 版与 `:72-85` 的 postgres 版各一份）。并且守护测试的**表清单**（`migration_sqlite_versioned_schema_test.go:50`）、**列映射**（`:90` `versionedSQLiteColumns`）与**版本常量**（`:107`）要同步更新。

10. **两方言的写法照抄既有形态，不重新发明。** SQLite：`TEXT PRIMARY KEY` / `INTEGER` / `DATETIME DEFAULT CURRENT_TIMESTAMP` / 内联 `UNIQUE` / 裸 `CREATE TABLE`；PostgreSQL：`UUID PRIMARY KEY` / `BIGINT` / `JSONB` / `TIMESTAMPTZ DEFAULT now()` / 具名 `CONSTRAINT` / `IF NOT EXISTS`（对照 `migrations/sqlite/000021:1-18` 与 `migrations/versioned/000100:1-18`）。另注：SQLite 改 CHECK 约束**必须重建表**（`sqlite/000047:8-57` 的建 `_next` → 拷贝 → guard 校验行数/主键 → DROP → RENAME），PostgreSQL 可以 `ADD COLUMN` + `ADD CONSTRAINT`。

11. **不追溯历史。** 存量夹具**逐步**迁移；本 Spec 交付**约定 + 两半守护 + 首批迁移**，并把**未迁移的清单列出来**（清单要可数、可核对）。理由：一次性全迁移会把一个机制票变成一个大工程票；而且**未迁移的清单本身就是下一轮的输入**。

12. **不新建第三种「跑迁移」方式。** 仓内已有两种可复用的形态：`golang-migrate` 的 file URL（`internal/database/migration.go:106-108` 的 `file://migrations/versioned` / `file://migrations/sqlite`，`:128` / `:137` / `:312`）与 `os.ReadFile` + 逐条执行（`audit_event_table_fixture_test.go:21-41`）。⇒ 夹具复用其中之一，**不发明第三种**。另注：迁移文件**没有** `go:embed`（`internal/database` 下零命中），它们靠工作目录相对路径读取 ⇒ 夹具的路径解析必须考虑测试的工作目录。

## Testing Decisions

好的测试断言的是**「机制能不能抓住下一次」**——尤其要能变红。

- **主接缝 —— 语义守护**（Go，`internal/database/`）：
  - 模型声明的字段集合 == 实际表列集合；
  - **故意给表加一列而模型不加 ⇒ 红**；
  - **故意给模型加一个字段而表不加 ⇒ 红**；
  - 夹具声明的迁移版本与生产迁移目录推导出的当前版本一致（不一致 ⇒ 红）。
- **形态守护**（Python，`scripts/ci/`，按 `test_lingdoc_*.py` glob 自动进 CI）：
  - 源码里出现**手抄的 `CREATE TABLE` 列清单** ⇒ 红；
  - 源码里出现**硬编码的迁移文件名**（形如 `0000NN_xxx.up.sql` 的字面量）⇒ 红；
  - 源码里出现**手改的迁移版本常量** ⇒ 红。
- **回归门槛（一次性验证，不是常驻 job）**：
  - **故意给 `lingdoc_project_audits` 加一列（临时）⇒ 全仓不出现新增失败**（#416 验收原文。现在会打红一批）；
  - 该门槛的验证结果落成验证报告，**并成为后续加列的准入条件**。
- **回归**：`scripts/check_migration_pairs.sh:9-36`（逐目录比对 `*.up.sql` / `*.down.sql` 集合，不成对即 `exit 1`）仍绿；既有夹具测试全绿；迁移守护的表清单 / 列映射 / 版本常量随新迁移更新后仍绿。
- **模式**：SQLite 与隔离 PostgreSQL 两侧都要覆盖**列集合**的对照（方言差异不该让守护失效）。**不跑**真实业务链路——那是 [候选 6](https://github.com/magician336/lingdoc/issues/454)（PostgreSQL 行为验证层）的事，本轮不开票。
- **先例**：`audit_event_table_fixture_test.go` 的 `ensureAuditEventTable()`（应用生产迁移 + `PRAGMA table_info` 防重复）是夹具侧的模型；`migration_candidate_shape_upgrade_test.go:815` 的整目录迁移是链侧的模型；`scripts/ci/test_lingdoc_naming_registry.py:121-131`（断言 CI 的发现机制与登记表位置）是**「放对位置就自动进 CI」**这一条的模型。

## Out of Scope

- **审计 5 个字段不落库**（`TenantID` / `UserID` / `Role` / `Capability` / `Decision`）——那是 [#415](https://github.com/magician336/lingdoc/issues/415)，属「审计链完整性」簇（PF-7），与夹具同源是两件事。
- **PostgreSQL 行为验证层**（候选 6）——#442 裁决与 #360 并列、本轮不开票。
- **夹具的测试数据构造**——本 Spec 只管 schema 形状，不管数据。
- **端到端测试的用例设计**。
- **命名规范的其他条款**——归 #48。
- **把存量夹具一次性全部迁移**——见决策 11。
- **SQLite 与 PostgreSQL 的迁移内容对齐**——两方言的**内容**本来就不同；本 Spec 只要求「列集合的对照在两侧都成立」。

## Further Notes

- 本 Spec 由 [#442](https://github.com/magician336/lingdoc/issues/442) 定稿编号 **56**。**筛选三条标准**：① 验收点能写成机器可判的断言 ✅（#416 验收原文）② 至少解锁一张下游票（#416、#386）✅ ③ 不与现有 G 线 Spec 重叠 ✅（#48 拥有**命名**，#130 拥有**审计字段语义**，都不是夹具同源）。
- **反例检查**：#416 票面把「模型字段集合 == 表列集合」的守护列为 PF-3 健康度复评的**回归门槛、尚未做**——说明它确实是**没人拥有的那一块**。
- **依赖边**：本 Spec **无上游**；解锁 #416 与 #386 ⇒ 原生边 `#416 blocked_by #454`、`#386 blocked_by #454`（已加）。边档：验收未满足档。
- **迁移编号水位**：sqlite 最新 `000053_lingdoc_project_archive`、versioned 最新 `000132_lingdoc_project_archive`，**两方言偏移量固定为 79**（与 PF-3 方案档 `:221` 一致）。⚠️ **注意**：PF-7（审计链完整性）已预留 `000054` / `000133`（见 #441 方案 §2.1），本 Spec 若要加迁移，**取号时必须重新核对**，两处不能撞号。
- **一个已过期的数字**：PF-3 方案档写 `expectedSQLiteMigrationVersion` 从 51 改到 52，而当前 head 已是 **53**（`000053_lingdoc_project_archive` 已合入）。⇒ 以实际文件为准。
- **两个整号不存在**：`migrations/sqlite/000038` 与 `migrations/versioned/000117`（up/down 都没有）⇒ 取号时不要把它们当成已用。

- **判定口径**：本 Spec 按 [`验收判定口径.md`](../08-本轮实施方案/验收判定口径.md) **v1** 判定——
  **验收点 = 一个可单独核验、可单独判 PASS / FAIL / NOT RUN 的原子要求**；
  `## User Stories` 每条 = 1 个验收点，`## Testing Decisions` 每条 = 1 个验收点（聚合条目按顿号 / 逗号切分成子项），
  `## Implementation Decisions` **不计入点数**、只作约束引用。
  本 Spec 的 `## Out of Scope` 与 `## Further Notes` 不计入验收点。
