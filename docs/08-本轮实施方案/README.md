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

多场景可重复 `--scenario`，例如 `--scenario F08 --scenario F05 --scenario F12`。F05/F08 是双身份场�