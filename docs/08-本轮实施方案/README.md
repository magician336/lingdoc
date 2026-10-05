# 本轮实施方案与契约

实施协作说明版本 0.5：当前任务按 T01–T16 领取，全部待领取，不预分人员或 A/B/C 工作包。v0.4 的前端任务已并入对应领域任务；contracts 中 HTTP、场景与连续规格的版本独立维护，任务重编号本身不代表接口或业务语义升级。范围和合作方式先读文档，不要求成员先看全部 JSON。

- [范围与任务流程](01-范围与任务流程.md)
- [T01–T16 全景状态与下一步](T01-T16-全景状态与下一步.md)
- [待领取任务池](../TEAM_DEVELOPMENT_TASKS.md)
- [协作与代码边界](../06-灵档产品开发规划/05-团队分工与代码边界.md)
- [接口与 Mock 约定](02-接口与Mock约定.md)

## 契约文件

| 文件 | 用途 |
|---|---|
| [openapi.json](contracts/openapi.json) | 拟议 HTTP：31 操作，28 core / 3 optional；x-domain 表示服务职责，不指定人 |
| [scenarios.json](contracts/scenarios.json) | 24 个行为场景（F01–F23、G7-01 基线读写）及合成样例；providers 表示参与服务；F22 已带请求定义与可求值检查 |
| [workflow.json](contracts/workflow.json) | F01 连续规格，共 23 步，动态捕获 ID/版本与幂等重放 |
| [T15-读回式副作用断言](T15-读回式副作用断言.md) | issue #33 读回契约、报告来源标记与 F11/F21 场景 |
| [T15-F02 白盒观察](T15-F02-白盒观察.md) | issue #34 的 F02 专属负断言观察点与真实服务结论 |
| [T15-09 验证报告](T15-09-验证报告.json) | issue #36 的 F10/F16/F19 实跑证据、F13 未运行原因与已登记缺口 |
| [T15-10 验证报告](T15-10-验证报告.json) | issue #37 的 F03/F15/F18/F20 实跑证据、F06/F09/F14 未运行原因与已登记缺口 |
| [T15-11 验证报告](T15-11-验证报告.json) / [证据](T15-11-证据.json) | issue #38 的 F01–F22 完整状态矩阵、缺口责任归属与确定性构建证据 |
| [T16 closeout](T16-closeout-20260930.md) / [验收材料](acceptance/T16-closeout-20261001/README.md) | issue #42 的交付闭环、真实模型演示与验收证据 |
| [G7 首版持续写作计划](../06-灵档产品开发规划/20-G7持续写作开发计划.md) / [G7 写作契约](contracts/g7-writing-contract.json) | issues #53–#61；实现集中在 PR #75，真实服务、数据库、模型和第二开发者验收仍需补证 |
| [frozen-input.canonical.json](contracts/frozen-input.canonical.json)、[sha256](contracts/frozen-input.sha256) | 冻结内容与摘要对照 |
| [validate_artifacts.py](contracts/validate_artifacts.py) | 形状、引用、连续参考状态和关键反例静态检查 |
| [validate_g7_contract.py](contracts/validate_g7_contract.py) | G7 工作副本、版本历史/恢复、选段候选、局部采纳、显式提交契约样例及反例检查 |
| [validation-result.json](contracts/validation-result.json) | 本次静态检查结果及未运行范围 |

## 复现静态检查

环境：Python 3.10+，已安装 `jsonschema`。从仓库根运行：

```text
python -X utf8 docs/08-本轮实施方案/contracts/validate_artifacts.py
python -X utf8 docs/08-本轮实施方案/contracts/validate_g7_contract.py
```

其中 G7 单独契约检查仅依赖 Python 标准库；上面的仓库级设计检查仍需要 `jsonschema`。G7 检查只验证契约声明子集与合成样例，不会请求本地服务，也不证明任何新路由已经实现。

脚本只读取同目录合成资料并写回 validation-result.json，不依赖作者本机目录、旧稿或私密数据，不调用模型和真实服务。

检查包含确认章节/模板/资料版本对应、待核及处置 ID 唯一、确认返回与快照对应、整章替换留存、幂等参考顺序和历史标签行为，以及起点状态声明（只许造得出来的部分、章节引用与正文标记一致、草稿不得有章节）。共 44 个静态反例应被拒绝。

## G6-01 质量证据报告

场景驱动器可在报告中生成 `quality_evidence`，把现有的起点读回、步骤断言和脱敏结果整理成稳定的 G6 证据记录。记录包含 `fixture_id`、模板/规则版本、对象版本、权限快照、`runtime_mode`、契约依赖摘要、输入/输出摘要、失败归因、责任人和证据引用；`result` 只使用 `PASS`、`FAIL`、`NOT RUN` 或 `BLOCKED`。报告不会写入正文、来源原文、访问令牌或动态业务 ID。

运行模式通过 `--runtime-mode` 指定，默认是 `mock`，可选 `real_api_fake_model` 和 `real`。例如：

```text
python scripts/lingdoc_mock/run_scenario.py --scenario F22 \
  --runtime-mode mock \
  --base-url http://127.0.0.1:8080/api/v1/lingdoc \
  --knowledge k-demo=<真实知识 id> --knowledge k-notready=<真实知识 id> \
  --identity u-owner=<该账号令牌> \
  --report artifacts/g6-01-f22.json
```

`mock` 只证明契约和失败语义；当前 `real_api_fake_model` 和 `real` 在尚未接入依赖证据时会保留 `BLOCKED`，不能把 mock 结果当成真实能力通过。多场景运行时，`quality_evidence` 是按执行顺序排列的每条场景证据列表。

## G6-02 运行模式矩阵

同一组场景可以重复传入 `--runtime-mode`，执行器会为每种模式保留独立报告，并在顶层输出 `modes`、`reports`、`fixture_ids` 和 `shared_fixture`。只有所有模式共用同一个 `fixture_id` 时，矩阵才可用于横向比较；矩阵结果按 `FAIL`、`BLOCKED`、`NOT RUN`、`PASS` 的优先级汇总，真实依赖尚未提供证据时仍为 `BLOCKED`：

```text
python scripts/lingdoc_mock/run_scenario.py --scenario F22 \
  --runtime-mode mock --runtime-mode real_api_fake_model --runtime-mode real \
  --knowledge k-demo=<真实知识 id> --knowledge k-notready=<真实知识 id> \
  --identity u-owner=<该账号令牌> \
  --report artifacts/g6-02-f22-matrix.json
```

矩阵不把 mock 的 `PASS` 合并成真实模式的通过结论；每个模式的 `quality_evidence`、执行摘要和验证范围都在自己的条目中保存。

矩阵还输出 `dependency_matrix`：`mock` 要求契约证据，`real_api_fake_model` 要求 API、权限、队列和文件依赖，`real` 另外要求模型、WeKnora 和 DOCX 依赖；缺任一项即为 `BLOCKED`。每个模式还必须提交脱敏的 `scenario_evidence.main_path` 与 `scenario_evidence.key_failure`：主路径需有正步数和外部观察范围，关键失败需有 4xx/5xx、`no_formal_side_effect=true` 及 `readback_status=unchanged`；任一模式缺少这两段证据，矩阵不能为 `PASS`。

真实模式还必须显式提供每个依赖和 `provider_semantics=verified` 证据；只有命令行/报告同时满足这些条件时，模式才可能得到 `PASS`。仅填写依赖存在布尔值或仅有 mock 结果不能冒充真实接入。

G6-01 至 G6-06 的主路径、运行矩阵、安全矩阵、结构化观测、迁移/回退和 Beta 指标门禁见 [G6 质量运行门禁](G6-质量运行门禁.md)。统一入口 `g6_run.py` 会保留六个门禁的独立结果；这些入口默认在缺少真实观测时输出 `BLOCKED`，Beta 指标用 `readiness=NOT READY` 表示样本或分母不足，并保留可审计的原始计数与失败动作。

## 提供方联调执行器（按场景驱动）

`scripts/lingdoc_mock/run_f01.py` 从 OpenAPI 与规格文档读取请求定义，对隔离的提供方测试环境逐步发请求。规格有两种给法：默认的 workflow.json 是 F01 的 23 步连续规格；`--scenario` 则按 id 从 scenarios.json 取一条，F01 在契约里指向 workflow.json，所以两种给法等价。先启动提供方测试环境，并只使用合成资料与短期测试凭证：

```text
python scripts/lingdoc_mock/run_f01.py --base-url http://127.0.0.1:8080/api/v1/lingdoc --report artifacts/f01-run.json
python scripts/lingdoc_mock/run_f01.py --scenario F01 --base-url http://127.0.0.1:8080/api/v1/lingdoc
```

执行器只按传进来的规格驱动，不对场景 id 做内置判断。通用执行器目前覆盖 F01、F03、F05、F08、F10、F11、F12、F16、F18、F21、F22；其他场景若没有请求定义或步骤，会逐条报告具体原因，不会静默给出空结论。执行器允许重复传入 `--scenario`，每条场景从自己的声明起点独立构建，并在一份报告中分别记录执行轨迹；跨步响应捕获通过 `{{变量}}` 供后续请求与检查使用。F15/F20 的真实模型候选和额外证据检查由下方 T15-10 专用驱动完成，F19 的跨租户撤权与额外摘要检查由 T15-09 驱动完成。F05/F08/F12 的多步结果及约束见 [T15-06 多步与跨步捕获](T15-多步与跨步捕获.md)；F11/F21 的读回断言见 [T15-读回式副作用断言](T15-读回式副作用断言.md)。

### T15-09 交付场景实跑

F10/F16 可由通用驱动分别从各自声明的起点执行。F19 还需要一个临时消费者租户、跨租户资料分享、快照复算和撤权后重试，因此使用专用驱动；本机已有测试 fixture helper 时可这样运行：

```text
python scripts/lingdoc_mock/run_t15_09_live.py --local-facts-path <本地fixture-helper路径>
```

驱动会清理临时组织、分享和消费者租户，并把 F10/F16/F19/F13 的结果写入 [T15-09 验证报告](T15-09-验证报告.json)，保留 T15-08 的报告不变。报告区分场景通过、实跑失败和因缺少公开注入点而未运行；当前 F19 会记录撤权后下载仍返回 200 的 T14 授权缺口，F13 记录缺少损坏字节注入入口。

### T15-10 证据族与剩余场景

F03/F18 可由通用场景执行器运行；F15/F20 需要真实生成候选、轮询和额外快照/数据库读回，因此使用专用驱动：

```text
python scripts/lingdoc_mock/run_t15_10_live.py --local-facts-path <本地fixture-helper路径>
```

报告写入 [T15-10 验证报告](T15-10-验证报告.json)。F06/F09/F14 即使本机能生成普通候选，也因分别需要生成期间变更、可控超时/中断和特定质量样例而记录为 `not_run`。F20 会经本地 PostgreSQL 对旧版和确认记录做替换前后指纹核对；报告仅保留布尔结果，不保存行内容或动态 ID。完整运行可能因真实模型输出或服务检查暴露失败；退出码会反映失败场景。

如需凭证，可通过 `LINGDOC_TEST_TOKEN` 环境变量传入；不要把令牌写入命令历史或仓库。带令牌时，执行器只允许 HTTPS，HTTP 仅放行 localhost/loopback 测试地址。轮询对 generation/export 只发 GET，不会因等待而重复提交创建请求。下载步骤会验证 DOCX Content-Type，并比较实际文件字节的 SHA-256 与 getExport 返回值。

`--report` 写出不含访问令牌和动态业务 ID 的脱敏 JSON。执行器会在任何提供方请求之前创建父目录并写入 `not_started` 记录；无法写入则直接失败，不执行写请求。成功后原子替换为 `completed`，流程中途失败则尽力保存 `failed`、已完成步骤和失败步骤标识，不写原始响应或异常内容。写报告失败会明确报错，不用 traceback 冒充流程结果。

多场景可重复 `--scenario`，例如 `--scenario F08 --scenario F05 --scenario F12`。F05/F08 是双身份场景，必须分别通过 `--identity u-owner=<令牌>` 和 `--identity u-member=<令牌>` 提供凭证；报告中的响应捕获使用声明别名呈现，不发布真实 ID。

报告的 `verification_scope=http_smoke_only`、`provider_semantics_status=not_verified` 和 `manual_assertions` 明确标出尚未验证的业务语义。`completed_steps=23` 只证明请求步骤与文件传输检查完成，不等于模型质量、数据库副作用或 DOCX 可编辑性通过。

### T15-11 全矩阵报告

从仓库根运行下面的命令，合并 T15 已保存的场景证据，生成完整的 F01–F22 矩阵和输入摘要；F23 的验收预算与模型边界由 T16 代码测试覆盖：

```text
python scripts/lingdoc_mock/build_t15_matrix.py
```

这是离线证据整理，不会启动服务、发送请求、运行模型或访问数据库。每个场景都给出 `passed`、`failed` 或 `not_run` 及其证据来源；失败项列出责任任务和任务负责人角色。当前任务池不预填个人姓名，矩阵会明确标出尚无个人 assignee。F15 保留修复提交前的历史失败结论，并注明修复后尚待真实场景复测。报告和证据不包含时间戳，构建器会在单次运行中重复渲染并核对字节一致；输入文件 SHA-256 及矩阵摘要见 [T15-11 证据](T15-11-证据.json)。矩阵明确保留未运行原因以及边界：资料上下文使用分块表文本、未从原文件重新解析；UI 尚未通过真实后端浏览器验证；完整 F01 流程由 T16 负责。

当前契约标记为 `specification_not_executed_against_provider`：真实服务已能按 [T15-01 的启动配方](T15-真实服务启动.md) 在本机起来并接受真实请求，但**尚未对着它跑过这条 23 步流程**。执行器不解释契约里的 `mode`（workflow.json 的 `mode` 是这份规格的执行状态，场景条目里的 `mode` 是「真接口 / 假模型」这类运行方式，两个是不同层面的记账，都由契约维护者随进度更新，由人按它决定怎么跑），因此别把这两个字段当成执行器的开关。所以本执行器自身的单元测试不等于 F01 已通过；接通提供方后仍须运行整条流程并处理输出中的 MANUAL 语义断言，包括 DOCX 可打开、章节和警示内容正确。

执行器单元测试（包含报告与大小写响应头回归）：

```text
python -m unittest discover -s scripts/lingdoc_mock -p 'test_workflow*.py' -v
```

## 起点状态装载器（把声明的状态灌进提供方并读回）

场景要跑就得有起点。起点写在 scenarios.json 的 `starting_states` 里，只声明**能靠写请求造出来**的部分：项目（含条件、协作者、是否立项）、绑定的资料、章节正文（`body_markdown` 为空表示「这一节存在但还没有正文」）。template、source、candidate、confirmation、release、export 造不出来——它们是服务固定提供或流程产物，写进声明会被连理由一起拒绝，不会被静默忽略。

```text
python scripts/lingdoc_mock/load_state.py --state S1 --base-url http://127.0.0.1:8080/api/v1/lingdoc --report artifacts/S1-run.json
python scripts/lingdoc_mock/load_state.py --state S5 --knowledge k-demo=<真实知识 id>
python scripts/lingdoc_mock/load_state.py --state S3 --knowledge k-notready=<真实知识 id>
```

`--knowledge` / `--member` 只给**这份声明用到**的名字：S5 只用 k-demo、S3 只用 k-notready，多给的（比如把名字打错成 `k-dmeo`）会被拒绝，不会被静默忽略。

装载器按声明顺序发前置写请求（建项目 → 存条件 → 加协作者 → 立项 → 写章节 → 绑资料），每一步的版本前置条件都取自本次运行看到的响应（建档答 `spec_revision=0`，存条件就必须送 0），不在任何地方写死版本。绑定是独立一步、不并进立项：草稿项目也能绑资料（S6 就是这种起点），所以草稿声明了资料就照样造、照样读回。然后**读回**：`getProject` / `listChapters` / `listAssets`，把读到的与声明的逐条比对；资料这一项用 `listAssets` 的否定面（不可用资料不会被列出）+ 检索预检（`retrieveSources` 在检索前按 `asset_not_authorized` 拒绝，带 `details.denied[].reason ∈ not_authorized / not_ready / not_found`，不触达模型）来区分「不可用」与「无权」。任何一处不一致都会以 `MISMATCH <指针>: declared …, read back …` 打到 stderr 并让进程以 1 退出，不静默继续。

资料与成员用契约里的名字（`k-demo`、`k-notready`、`u-member`）声明，真实 ID 由环境用 `--knowledge` / `--member` 注入；`--token` 或 `LINGDOC_TEST_TOKEN` 传凭证。报告格式与执行器一致地脱敏：不含令牌、不含提供方 ID。`snapshot` 是声明里那些被读回确认过的事实，`observed` 是读回本身产生的事实（`project_version`、章节是否已有版本、资料可用性），两者各有摘要；**同一份声明跑两遍，`observed` 与它的摘要相同、两次都不一致清单为空**，才是「起点可重复构造」——只比 `snapshot` 等于拿声明跟它自己比，证不了提供方那一侧。读回只能证到通道能证的那一层：提供方对不可用资料只答「不可用」，所以声明里写 `pending` / `failed` 时，那句更细的话会进报告的 `unverified`（附上为什么），不会冒充已验证。改动范围、实跑证据与边界见 [T15-03 起点状态装载](T15-起点状态装载.md)。

## 场景驱动（一条场景从契约跑到报告）

`scripts/lingdoc_mock/run_scenario.py` 把上面两件事接起来：按声明把起点灌进提供方并读回核对，再按步骤声明的身份发出那一步，逐条求值断言，写出一份逐项报告。

```text
python scripts/lingdoc_mock/run_scenario.py --scenario F22 \
  --base-url http://127.0.0.1:8080/api/v1/lingdoc \
  --knowledge k-demo=<真实知识 id> --knowledge k-notready=<真实知识 id> \
  --identity u-owner=<该账号令牌>
```

契约里的请求用 `{{变量}}` 引用运行时的值，只有三个命名空间：`project.id`（本次建出来的项目）、`asset.<声明名>`（`bindAsset` 返回的**绑定 id**，不是知识 id）、`chapter.<节 id>`。真实值由 `--knowledge` / `--member` / `--identity` 注入，于是契约文件里只出现契约自己的名字。步骤声明 `actor`；一次运行里步骤声明的 actor 多于一个时，每一个都必须给凭证——缺一个就是把两个调用者当成同一个人，那样跑出来的通过没有意义；只声明一个 actor 的规格可以退回 `--token`。

断言是「一个 `path`（JSON 指针）+ 一个算子 + 一句 `intent`」，算子有 `equals` / `length` / `keys` / `one_of`。`intent` 与步骤的 `assertion` 是中文意图说明，原样进报告但不参与求值；报告另写 `expected` 与 `actual`。期望值的现成来源是契约已发布的响应例子，静态校验逐条比对「这条 check 的 `path` 在声明的那个例子里是否存在、值是否一致」，比对不上就拒绝；例子里给不出的运行时值（比如绑定 id）记作留给运行。

报告默认写在 `docs/08-本轮实施方案/T15-验证报告.json`，不含令牌、不含提供方 ID、不含时间戳，**同一输入两次运行的字节相同**。逐条判定分三种：`passed`；`failed` 带 `why`（先写期望与实际的 HTTP 状态码，再逐条写「哪个指针、期望什么、实际读到什么」，读不到的位置明说读不到）；`not_run` 带原因（没有请求定义 / 没有步骤），23 条场景全部出现，没有一条留空。起点读不回来时那一步**不发**，结论直接为失败。改动范围、实跑结论与边界见 [T15-04 场景到报告](T15-场景到报告.md)。

`.github/workflows/lingdoc-f01-runner.yml` 对执行器、装载器、场景驱动、相关测试与契约变更运行 `python -m unittest discover -s scripts/lingdoc_mock -p 'test_workflow*.py'`（并对这几个模块做 `py_compile`），保留测试日志；它仅使用合成响应，**不运行上面的真实服务命令**，不读取凭据，也不调用真实模型。

这些接口是待实现方案。Schema 不能独自验证全部跨字段规则，静态轨迹也不能证明数据库副作用。Prism、完整 OpenAPI meta-schema、真实服务/数据库/浏览器/模型/DOCX 均未由本脚本验证；文件 hash 占位值不代表已生成真实文件。

正式业务模板未确认时使用清楚标注的演示模板。保留警示的导出仅用于 internal_demo，不宣称正式申报通过。
