# 本轮实施方案与契约

实施协作说明版本 0.5：当前任务按 T01–T16 领取，全部待领取，不预分人员或 A/B/C 工作包。v0.4 的前端任务已并入对应领域任务；contracts 中 HTTP、场景与连续规格的版本独立维护，任务重编号本身不代表接口或业务语义升级。范围和合作方式先读文档，不要求成员先看全部 JSON。

- [范围与任务流程](01-范围与任务流程.md)
- [待领取任务池](../TEAM_DEVELOPMENT_TASKS.md)
- [协作与代码边界](../06-灵档产品开发规划/05-团队分工与代码边界.md)
- [接口与 Mock 约定](02-接口与Mock约定.md)

## 契约文件

| 文件 | 用途 |
|---|---|
| [openapi.json](contracts/openapi.json) | 拟议 HTTP：31 操作，28 core / 3 optional；x-domain 表示服务职责，不指定人 |
| [scenarios.json](contracts/scenarios.json) | 22 个行为场景（F01–F22）及合成样例；providers 表示参与服务 |
| [workflow.json](contracts/workflow.json) | F01 连续规格，共 23 步，动态捕获 ID/版本与幂等重放 |
| [frozen-input.canonical.json](contracts/frozen-input.canonical.json)、[sha256](contracts/frozen-input.sha256) | 冻结内容与摘要对照 |
| [validate_artifacts.py](contracts/validate_artifacts.py) | 形状、引用、连续参考状态和关键反例静态检查 |
| [validation-result.json](contracts/validation-result.json) | 本次静态检查结果及未运行范围 |

## 复现静态检查

环境：Python 3.10+，已安装 `jsonschema`。从仓库根运行：

```text
python -X utf8 docs/08-本轮实施方案/contracts/validate_artifacts.py
```

脚本只读取同目录合成资料并写回 validation-result.json，不依赖作者本机目录、旧稿或私密数据，不调用模型和真实服务。

检查包含确认章节/模板/资料版本对应、待核及处置 ID 唯一、确认返回与快照对应、整章替换留存、幂等参考顺序和历史标签行为，以及起点状态声明（只许造得出来的部分、章节引用与正文标记一致、草稿不得有章节）。共 22 个静态反例应被拒绝。

## 提供方联调执行器（按场景驱动）

`scripts/lingdoc_mock/run_f01.py` 从 OpenAPI 与规格文档读取请求定义，对隔离的提供方测试环境逐步发请求。规格有两种给法：默认的 workflow.json 是 F01 的 23 步连续规格；`--scenario` 则按 id 从 scenarios.json 取一条，F01 在契约里指向 workflow.json，所以两种给法等价。先启动提供方测试环境，并只使用合成资料与短期测试凭证：

```text
python scripts/lingdoc_mock/run_f01.py --base-url http://127.0.0.1:8080/api/v1/lingdoc --report artifacts/f01-run.json
python scripts/lingdoc_mock/run_f01.py --scenario F01 --base-url http://127.0.0.1:8080/api/v1/lingdoc
```

执行器只按传进来的规格驱动，不对场景 id 做内置判断。除 F01 外的场景目前**还不能执行**，且原因会被明确报出，不会静默跑出空结论：F02–F22 的步骤只有操作名、期望状态码与断言文本，没有请求定义（拒绝语为 `F22 FAILED: scenario F22 step 1 has no request definition in the contract`），F15–F21 连步骤都没有（`F17 has no executable steps`）。把请求定义补进契约是后续任务，不是本执行器的省略。改动范围、回归证据与边界见 [T15-02 执行器场景驱动](T15-执行器场景驱动.md)。

如需凭证，可通过 `LINGDOC_TEST_TOKEN` 环境变量传入；不要把令牌写入命令历史或仓库。带令牌时，执行器只允许 HTTPS，HTTP 仅放行 localhost/loopback 测试地址。轮询对 generation/export 只发 GET，不会因等待而重复提交创建请求。下载步骤会验证 DOCX Content-Type，并比较实际文件字节的 SHA-256 与 getExport 返回值。

`--report` 写出不含访问令牌和动态业务 ID 的脱敏 JSON。执行器会在任何提供方请求之前创建父目录并写入 `not_started` 记录；无法写入则直接失败，不执行写请求。成功后原子替换为 `completed`，流程中途失败则尽力保存 `failed`、已完成步骤和失败步骤标识，不写原始响应或异常内容。写报告失败会明确报错，不用 traceback 冒充流程结果。

报告的 `verification_scope=http_smoke_only`、`provider_semantics_status=not_verified` 和 `manual_assertions` 明确标出尚未验证的业务语义。`completed_steps=23` 只证明请求步骤与文件传输检查完成，不等于模型质量、数据库副作用或 DOCX 可编辑性通过。

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

`.github/workflows/lingdoc-f01-runner.yml` 对执行器、装载器、相关测试和契约变更运行该命令，保留测试日志；它仅使用合成响应，不读取凭据或调用真实模型。

这些接口是待实现方案。Schema 不能独自验证全部跨字段规则，静态轨迹也不能证明数据库副作用。Prism、完整 OpenAPI meta-schema、真实服务/数据库/浏览器/模型/DOCX 均未由本脚本验证；文件 hash 占位值不代表已生成真实文件。

正式业务模板未确认时使用清楚标注的演示模板。保留警示的导出仅用于 internal_demo，不宣称正式申报通过。
