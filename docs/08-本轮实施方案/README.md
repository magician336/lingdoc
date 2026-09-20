# 本轮实施方案与契约

版本 0.4：按工作项拆分，全部待领取，不预分人员或 A/B/C 工作包。范围和合作方式先读文档，不要求成员先看全部 JSON。

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

这些接口是待实现方案。Schema 不能独自验证全部跨字段规则，静态轨迹也不能证明数据库副作用。Prism、完整 OpenAPI meta-schema、真实服务/数据库/浏览器/模型/DOCX 均未由本脚本验证；文件 hash 占位值不代表已生成真实文件。

正式业务模板未确认时使用清楚标注的演示模板。保留警示的导出仅用于 internal_demo，不宣称正式申报通过。
