# LingDoc PR 审查与门禁

适用于 T01–T16 开发与本轮契约；不改变任务范围、HTTP 协议或人员分工。任务入口仍是[任务池](../TEAM_DEVELOPMENT_TASKS.md)，协作边界见[协作约定](../06-灵档产品开发规划/05-团队分工与代码边界.md)。

## 1. 四件事分开记录

| 记录 | 证明什么 | 不证明什么 |
| --- | --- | --- |
| PR 模板/作者勾选 | 作者声明范围、依赖、验证与风险 | 命令真的运行或功能正确 |
| CI 在当前提交上的结果 | 指定自动检查的实际结果 | 没有覆盖到的权限/并发/业务语义正确 |
| 独立 Review | 另一人核查改动与证据；消费者确认接口能用 | 所有产品验收都完成 |
| 用户结果验收 | 该切片按任务卡接真并完成规定操作 | 整个 T01–T16 或完整产品已经完成 |

**Merged 不等于任务 Done；SKIPPED/NOT RUN 不等于 PASS。** 一个小接口可以先合入、供下游接真，完整任务仍按任务卡验收。

## 2. 谁审、审什么

普通 PR 至少由一位非作者成员评审。改变跨任务契约时，评审人应包含实际消费者；同一人可以同时满足这两个条件。共享身份/权限、跨域版本或全局交付规则变化由所有受影响负责人确认，不把每个 schema 小修改都升级成全员审批。

消费者尚未领取时，明确指定临时消费者代表，否则契约决策保持待确认；不能用“没人负责”免除审查。AI 可辅助审查，但用作者账号提交的自评不算独立审批，机器人评论也不自动等于消费者确认。

优先审查：资料归属和当前授权；事务、预期版本和幂等副作用；契约及消费者兼容；UI 是否保留用户输入；测试能否在错误实现下失败；证据是否属于本次被审提交。流程/CI 本身的改动也需要独立评审，不能以“检查是我写的且全绿”自批自合。

Review 统一标注：

- **阻塞**：注明文件/行、触发输入、预期与实际、影响、需要补的回归测试。安全/证据归属、数据丢失、已定义契约冲突和未经确认的跨域决策须先处理。
- **建议**：注明可后续原因和接手任务，不能伪装成必须返工。
- **待确认**：区分产品选择、接口缺口与实现 bug，由受影响方答复。

作者逐项回复“修改提交 + 测试证据”或解释异议；原评审人复查后改变 Review 结论。点击 Resolve 只关闭讨论，不等于已批准。新代码不能沿用不再适用的旧批准或旧测试结论。

## 3. 自动检查：`LingDoc PR gate`

工作流位于 `.github/workflows/lingdoc-review.yml`，所有指向 main 的 PR 都产生同名检查；不使用顶层 paths 过滤，以免 required check 永久 pending。检查范围由实际 `base...head` 文件差异选择，不相信 PR 标题、标签或勾选框。

| 变化 | 自动动作 |
| --- | --- |
| 每个 PR | 门禁选择器自身测试、`git diff --check`；无法读 diff 时失败而非当作无变化 |
| 本轮计划/契约、LingDoc/shared backend、前端或门禁工具 | 运行现有 `validate_artifacts.py` |
| `internal/**`、`cmd/**/*.go`、迁移/配置、根 Go 依赖、contracts 或门禁工具 | 对已存在的 `internal/lingdoc/...`、`internal/evidence/...` 跑 `go test -race -count=1 -json` 和 `go vet` |
| `frontend/**`、contracts 或门禁工具 | `npm ci`、`npm test`、`npm run type-check`、`npm run build` |
| 无关变更 | 不安装 Go/Node 或跑产品测试，明确记录未运行，不伪造功能验证 |

契约文件变化也重测现有消费者；新增目录/公共接入点时，应同步选择器和测试。初始 main 只有设计文档、没有 provider 包时不假装完成运行时验证；存在 provider 代码却无任何通过的测试事件则失败。现有 App/Frontend/Go Lint/CLI 等工作流保留，本门禁不是全仓构建、Docker、PostgreSQL或跨平台验收的替代。

契约校验在临时副本运行，先删除历史 `validation-result.json` 再生成本次结果，不修改仓内报告，不硬编码场景数（F22 等增量可以正常加入）。日志、选中范围、本次结果保留在 Actions artifact `lingdoc-checks-<run_id>-<attempt>`，保留 14 天；长期交接另外保存脱敏摘要和对应 run URL/SHA。

本门禁只运行合成/自动测试，不注入真实模型、业务账号或生产密钥，不自动触发付费模型调用。单测若读取仓内历史 S7 文件，只证明历史记录被解析，不能证明本次重新完成真实检索/生成，也不能证明引用确实支持结论。真实运行须另记命令、提交、模型、资料、时间和可核查结果。

安全边界：使用普通 `pull_request`、GitHub-hosted runner、`contents: read` 和 `persist-credentials: false`，不使用带写权限的 `pull_request_target` 执行 PR 代码。门禁代码本身可随 PR 变化，因此仍须独立审查；不宣称这能对有权修改规则的人提供不可绕过的保护。

## 4. 合并前清单与既有红灯

先确认最新 head/测试的 merge SHA；相关检查通过，阻塞项修复并经复审，跨域决定已由消费者确认，验证范围与未接真部分公开，才合并。普通代码任务不要等待全产品完成；但未接真功能须隐藏入口或清楚标注，不进入正式演示。

既有红灯不能仅凭“以前也失败”放行。需在 PR 中列出失败 job/日志、相同命令在 base 与 head 的结果、与本改动的关系、跟进任务及接受风险的维护者。新增回归、无法归因的失败、权限/数据一致性问题不能豁免。无法修正的相关关键检查应保持阻塞或拆出依赖修复。

已观察到的 CLI `cli/AGENTS.md` 缺失与 shell 可执行位问题应单独定位/修复；本 PR 不删除测试、不开 continue-on-error、不为消红关闭原工作流。恢复稳定前，不把已有路径过滤且可能不触发的全部工作流直接勾为 required。

## 5. 管理员一次性启用（代码 PR 不会自动修改 GitHub 设置）

工作流合并不等于主分支已受保护。仓库管理员在本门禁至少成功运行一次、通过独立评审后，进入：

`Repository → Settings → Rules → Rulesets → New branch ruleset`

目标选 `main`，Enforcement 选 `Active`，建议最小规则：

1. Require a pull request before merging；Required approvals = 1；Dismiss stale approvals when new commits are pushed；Require conversation resolution。
2. Require status checks to pass，选择本工作流实际出现的 **LingDoc PR gate**（来源 GitHub Actions）。首次未出现时先运行再选，不虚构 check 名称。其他 path-filtered 工作流不能直接全选，避免无关 PR 永久等待。
3. 禁止 force push 与删除 main；不要给开发者或 Agent 配置常规 bypass。不选 Restrict updates 代替 PR 规则，以免意外禁止正常合并。紧急例外需管理员记录原因与补验。

也可使用 `Settings → Branches` 的经典保护规则配置同等要求。API 的普通仓库写权限不等于 Administration 权限；未由管理员保存 Active 规则前，这一层仍未启用。第一版不自动审批/自动合并，不改已有成员权限，不提交含密钥的管理脚本。

## 6. 已在开发的 PR 如何接入

不强行重写正在开发的 PR 提交历史，也不修改他人的 PR 描述。此 PR 合并后，各作者把最新 main 合入自己的工作分支，再正常推送触发新门禁，补齐缺少的证据/消费者说明即可。旧 PR 不要求为了匹配标题而重开，新增模板可以沿用原文结构表达同等信息。

门禁不自动检查 PR 正文格式或证明声明为真；先避免把三人团队拖入“填写表格才能开发”。人工 reviewer 负责检查 Task ID、消费者、逐路径 Mock/Real、未运行项与回滚；稳定后再根据实际漏检新增针对性测试，不先建审批平台。

### 本地最小复核

```bash
python -m unittest discover -s scripts/ci -p 'test_lingdoc_pr_scope.py' -v
python -m pip install -r scripts/ci/requirements-lingdoc.txt
python -X utf8 docs/08-本轮实施方案/contracts/validate_artifacts.py
# 下列只对该分支实际存在的包执行：
go test -race -count=1 ./internal/lingdoc/... ./internal/evidence/...
```

最后一条需要可用 Go/CGO 环境；不要把包不存在或环境失败写成通过。本地契约脚本按原行为会更新报告，先看 diff；CI 已用临时副本隔离该副作用。

依据：[Required checks 排障](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/troubleshooting-required-status-checks)、[分支保护](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches)、[安全使用 pull_request_target](https://docs.github.com/en/actions/reference/security/securely-using-pull_request_target)。
