# T756 取证与收口:internal/types TestPostgres 方言测试的 CI 覆盖真空

- 票:magician336/lingdoc#756(治理,Parent #735,治理线 #719)
- 日期:2026-11-03
- 性质:取证 + 收口(走票面「若 integration 现结构即可容纳 ⇒ 独立小改」分支;#735 的实施票尚未建立)

## 1. 覆盖真空取证

### 1.1 被测对象清单

`internal/types` 下全部 `TestPostgres*` 测试共 **1 个**:

| 测试 | 位置 | 依赖 | 副作用 |
| --- | --- | --- | --- |
| `TestPostgresJSONColumnCarrierTypes` | `internal/types/json_column_dialect_test.go:112` | 环境变量 `LINGDOC_TEST_POSTGRES_DSN`(缺省即 `t.Skip("NOT RUN: LINGDOC_TEST_POSTGRES_DSN is unset")`,第 113–116 行) | 无:只 `SELECT` 字面量(`'…'::jsonb` / `'…'::text`),不触碰连接库的任何表与行(第 123 行注释即此声明) |

它度量的是共享读出器(`json_column.go`)赖以成立的双载体事实:pgx 的 `database/sql` 层把 jsonb 列解码成 `[]byte`、text 列解码成 `string`。

### 1.2 CI 无处真跑的证据

1. **integration closeout 不含该包**:`scripts/acceptance/lingdoc_backend_closeout.py:23`
   `PACKAGES = ["./internal/database", "./internal/lingdoc/...", "./internal/router", "./internal/container", "./internal/evidence"]` —— 无 `./internal/types`。
2. **integration 工作流的显式演练步只覆盖 internal/database**:`.github/workflows/lingdoc-interface-integration.yml` 收口前唯一带 DSN 的 Go 演练步是 `go test -count=1 -v -run '^TestPostgres' ./internal/database/...`。
3. **其余工作流无 DSN 注入**:全仓 workflows 中 `LINGDOC_TEST_POSTGRES_DSN` 仅出现在 `lingdoc-interface-integration.yml` 的 integration job(`grep -rn LINGDOC_TEST_POSTGRES_DSN .github/workflows/` 佐证)。PR 面按需裁剪、app.yml 不注入 ⇒ `internal/types` 的 TestPostgres 在 CI 上**恒 skip**。
4. **skip 语义**(收口前口径即如此,未改动):无 DSN 时明示 `NOT RUN: LINGDOC_TEST_POSTGRES_DSN is unset` 后 skip——skip 从不计作 pass,所以真空是「没有验证」,不是「假绿」。

结论:PG 依赖的方言行为(jsonb→[]byte / text→string)没有持续验证,与票面描述一致。

## 2. 收口方案与理由

**方案**:在 integration job 的既有 internal/database 演练步之后新增一步:

```yaml
- name: Rehearse internal/types PostgreSQL dialect behavior
  run: go test -count=1 -v -run '^TestPostgres' ./internal/types/...
```

**为什么现结构即可容纳(独立小改分支成立)**:该 job 已有 postgres:17 服务、`LINGDOC_TEST_POSTGRES_DSN` 注入与「演练步先于 closeout sweep」的编排(慢 sweep 不能吃掉演练证据)。新步骤逐字复用既有演练模式,不新增服务、不改分段、不动 closeout 语义。

**防回退守护**:`scripts/ci/test_lingdoc_types_dialect_rehearsal.py`(scripts/ci 的 PR 门禁 unittest discover `test_lingdoc_*.py` 会拾取)断言:① 工作流必须包含该演练命令;② 工作流必须注入 `LINGDOC_TEST_POSTGRES_DSN`。删步即红,防止方言行为静默回到覆盖真空。

**红→绿记录**(本仓 TDD 口径):

- 红:仅提交守护测试时,`python3 -m unittest scripts.ci.test_lingdoc_types_dialect_rehearsal` → `AssertionError: "go test -count=1 -v -run '^TestPostgres' ./internal/types/..." not found in <workflow 文本>`(1 failure)。
- 绿:加入演练步后同命令 → OK(2 tests)。

## 3. 本地红绿证据(与 CI 步骤等价命令)

| 场景 | 命令 | 结果 |
| --- | --- | --- |
| 无 DSN(失败/skip 语义) | `env -u LINGDOC_TEST_POSTGRES_DSN CGO_ENABLED=1 go test -count=1 -v -run '^TestPostgres' ./internal/types/...` | `NOT RUN: LINGDOC_TEST_POSTGRES_DSN is unset` → `SKIP: TestPostgresJSONColumnCarrierTypes`,包级 `ok`(skip 未计 pass) |
| 有 DSN(真跑) | 本机 docker postgres:17(daocloud 镜像),DSN 指向之,同上命令 | `--- PASS: TestPostgresJSONColumnCarrierTypes`;`jsonb value -> []uint8, text value -> string` |
| 守护测试 | `python3 -m unittest scripts.ci.test_lingdoc_types_dialect_rehearsal -v` | OK(2 tests) |

CI 绿记录:PR 的 LingDoc Interface Integration job(本收口的正式「真实运行绿记录」,run URL 见 PR checks 与 issue 关闭评论)。

## 4. 残留与跟进(不阻塞本票,登记为 DEFERRED 口径的跟进项)

- `internal/types/**` 不在 integration 门禁路径表(`scripts/ci/lingdoc_gate_surfaces.py`)中 ⇒ **internal/types 自身的变更不触发 integration job**,方言演练步在其变更上不会跑(其他后端 PR 上照常跑)。
- 按票面,把该测试集「纳入 #735 选定方案的矩阵分段并随该实施票落地」或扩展路径表,归 **#735**(分段结构定型后归位)。本票不动路径表,避免在 #735 取证/设计之前单方面放大 integration job 的触发面。

## 5. 无删减与语义不回退

- 未删除、未改写任何既有测试;closeout PACKAGES、既有演练步与判定语义一字未动。
- scripts/ci 全量 unittest 在本机的 12 failures + 2 errors 经与干净 `origin/main` 检出对照为**预存环境限制**(本地 python 3.14 / gofmt 版本与 CI 的 3.12 不同),与本票改动无关(本票新增 2 用例,失败集合逐项相同)。
