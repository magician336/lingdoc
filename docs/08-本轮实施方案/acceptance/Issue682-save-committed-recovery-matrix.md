# Issue #682：证据卡保存回执恢复矩阵

范围：ResearchPanel 消费 SaveEvidenceCard 的 HTTP 409 `evidence_card_save_committed_unreadable`；服务端保存、权限及幂等真相仍由后端拥有。页面仅在同一项目、用户与租户会话内保留已提交请求的内存副本。

| 验收项 | 不变量及期望 | 证据 | 状态 |
| --- | --- | --- | --- |
| 已提交但不可读 | 显示已保存，不提示普通版本冲突，不自动重试或创建重复卡；保存原始请求和幂等键 | ResearchPanel.test.js：已提交 409、普通 409 和无自动重试 | PASS |
| 权限恢复 | 拒绝读取后仍可手动使用完全相同请求及键，成功后清除恢复入口；不覆盖新编辑 | ResearchPanel.test.js：403（含刷新被拒）、无效回执、原键原请求及保留新编辑 | PASS |
| 身份隔离 | 项目、用户、租户变化或卸载时清除；迟到响应不能恢复旧会话状态 | ResearchPanel.test.js：项目、双用户身份、租户、卸载及迟到响应 | PASS |
| API 契约 | SaveEvidenceCard 的 409 区分已提交不可读与普通冲突，并给出服务端错误结构 | OpenAPI 409 示例与 validate_artifacts.py | PASS |

本切片不修改后端、数据库、正式来源规则或权限；后端事务、幂等及数据库并发证据由本 PR 对应验收行覆盖。

## 本次工作树验证（2026-10-30）

- `cd frontend && npm test -- src/views/lingdoc/ResearchPanel.test.js`：42/42 PASS；测试实际编译并渲染 Vue 面板，覆盖拒绝、原键重放、损坏回执和身份隔离。
- `cd frontend && npm test -- src/views/lingdoc/ResearchPanel.specCandidate.test.js src/views/lingdoc/ResearchPanel.revokedInput.runtime.test.js src/views/lingdoc/ResearchPanel.feedback.runtime.test.js`：21/21 PASS。
- `cd frontend && npm run type-check`：PASS。
- `python -m unittest scripts.ci.test_lingdoc_routes`：32/32 PASS。
- `PYTHONPATH=D:\LingDocDeps python -X utf8 docs/08-本轮实施方案/contracts/validate_artifacts.py`：静态契约、示例及引用链 PASS，已刷新 `contracts/validation-result.json`。

上述结果针对本次未提交工作树，不代表远端最终提交 CI；完整浏览器链和后台真实事务由本 PR 的其他验收证据覆盖。

## 未闭合的来源并发边界

证据卡保存后的来源读取与项目、目录和撤稿的最终事务复验不是跨系统原子操作。若来源权限或原文证明在两者之间变化，当前实现不能保证响应反映该变化。生产资料可从导入候选推导默认策略；要关闭该窗口还需要统一相关策略写入的串行化，并将原件摘要、解析证明和 PDF 页证明绑定到事务复验。不能通过要求所有旧资料预先有显式策略来掩盖兼容性问题。该项仍为 NOT RUN / 未完成，PR 保持 Draft。
