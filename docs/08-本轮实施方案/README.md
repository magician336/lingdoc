# 本轮实施方案与契约

实施协作说明版本 0.5：当前任务按 T01–T16 领取，全部待领取，不预分人员或 A/B/C 工作包。v0.4 的前端任务已并入对应领域任务；contracts 中 HTTP、场景与连续规格的版本独立维护，任务重编号本身不代表接口或业务语义升级。范围和合作方式先读文档，不要求成员先看全部 JSON。

- [范围与任务流程](01-范围与任务流程.md)
- [待领取任务池](../TEAM_DEVELOPMENT_TASKS.md)
- [协作与代码边界](../06-灵档产品开发规划/05-团队分工与代码边界.md)
- [接口与 Mock 约定](02-接口与Mock约定.md)

## 契约文件

| 文件 | 用途 |
|---|---|
| [openapi.json](contracts/openapi.json) | 拟议 HTTP：25 操作，22 core / 3 optional；x-domain 表示服务职责，不指定人 |
| [scenarios.json](contracts/scenarios.json) | 21 个行为场景及合成样例；providers 表示参与服务 |
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

检查包含确认章节/模板/资料版本对应、待核及处置 ID 唯一、确认返回与快照对应、整章替换留存、幂等参考顺序和历史标签行为。共 15 个静态反例应被拒绝。

## F01 提供方联调执行器

`scripts/lingdoc_mock/run_f01.py` 从 OpenAPI 和 workflow.json 读取请求定义，可对隔离的提供方测试环境运行 23 步 F01 连续流程。先启动提供方测试环境，并只使用合成资料与短期测试凭证：

```text
python scripts/lingdoc_mock/run_f01.py --base-url http://127.0.0.1:8080/api/v1/lingdoc --report artifacts/f01-run.json
```

如需凭证，可通过 `LINGDOC_TEST_TOKEN` 环境变量传入；不要把令牌写入命令历史或仓库。带令牌时，执行器只允许 HTTPS，HTTP 仅放行 localhost/loopback 测试地址。轮询对 generation/export 只发 GET，不会因等待而重复提交创建请求。下载步骤会验证 DOCX Content-Type，并比较实际文件字节的 SHA-256 与 getExport 返回值。

`--report` 写出不含访问令牌和动态业务 ID 的脱敏 JSON。执行器会在任何提供方请求之前创建父目录并写入 `not_started` 记录；无法写入则直接失败，不执行写请求。成功后原子替换为 `completed`，流程中途失败则尽力保存 `failed`、已完成步骤和失败步骤标识，不写原始响应或异常内容。写报告失败会明确报错，不用 traceback 冒充流程结果。

报告的 `verification_scope=http_smoke_only`、`provider_semantics_status=not_verified` 和 `manual_assertions` 明确标出尚未验证的业务语义。`completed_steps=23` 只证明请求步骤与文件传输检查完成，不等于模型质量、数据库副作用或 DOCX 可编辑性通过。

当前契约标记为 `specification_not_executed_against_provider`，仓库尚未提供这里所需的完整真实服务/隔离测试环境。因此本执行器自身的单元测试不等于 F01 已通过；接通提供方后仍须运行整条流程并处理输出中的 MANUAL 语义断言，包括 DOCX 可打开、章节和警示内容正确。

执行器单元测试（包含报告与大小写响应头回归）：

```text
python -m unittest discover -s scripts/lingdoc_mock -p 'test_workflow*.py' -v
```

`.github/workflows/lingdoc-f01-runner.yml` 对执行器、相关测试和契约变更运行该命令，保留测试日志；它仅使用合成响应，不读取凭据或调用真实模型。

这些接口是待实现方案。Schema 不能独自验证全部跨字段规则，静态轨迹也不能证明数据库副作用。Prism、完整 OpenAPI meta-schema、真实服务/数据库/浏览器/模型/DOCX 均未由本脚本验证；文件 hash 占位值不代表已生成真实文件。

正式业务模板未确认时使用清楚标注的演示模板。保留警示的导出仅用于 internal_demo，不宣称正式申报通过。
