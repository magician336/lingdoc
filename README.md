# LingDoc

LingDoc 是面向科研人员、课题组和实验室的科研项目写作与交付工作台，构建在 WeKnora 能力之上。产品把研究条件、项目资料、AI 调研、章节写作、证据核查、版本确认和 Word 交付连接成一条可追溯流程。

## 当前开发主线

当前工作分为两条并行路线：

- **Beta 首版验收**：验证当前已合入能力能否在真实服务、合成材料和 Microsoft Word 环境中完成约定的连续流程。验收协议见 [`docs/08-本轮实施方案/Beta首版真实验收协议.md`](docs/08-本轮实施方案/Beta首版真实验收协议.md)。
- **完整 G1–G7 开发**：继续实现各功能性 Spec。每个 Spec 都是独立功能单元，需要独立开发计划、实现、验证和 PR；Spec 之间的阻塞关系以 GitHub [Issue #144](https://github.com/magician336/lingdoc/issues/144) 的并行依赖图为准。

当前 G1–G7 入口与 Spec 索引见 [`docs/06-灵档产品开发规划/beta-gap-development-plans/README.md`](docs/06-灵档产品开发规划/beta-gap-development-plans/README.md)。目标是在 2026 年 10 月结束前完成当前开放范围。

## 开发前先读

1. [`CONTEXT.md`](CONTEXT.md)：产品术语、责任人重合、AI 辅助和权限语义。
2. [`docs/README.md`](docs/README.md)：文档分层、当前路线和维护规则。
3. [`docs/06-灵档产品开发规划/beta-gap-development-plans/README.md`](docs/06-灵档产品开发规划/beta-gap-development-plans/README.md)：G1–G7 Spec 与依赖入口。
4. [Issue #144](https://github.com/magician336/lingdoc/issues/144)：并行依赖图、阻塞关系和开发顺序。
5. [`docs/agents/issue-tracker.md`](docs/agents/issue-tracker.md)：认领 Issue、关联 PR 和关闭 Issue 的协作约定。

历史 T01–T16 任务池和验收报告仍用于追溯已有实现与证据，不替代当前 Spec 路线：[`docs/TEAM_DEVELOPMENT_TASKS.md`](docs/TEAM_DEVELOPMENT_TASKS.md)。

## 本地开发

```powershell
make build
make test
cd frontend
npm ci
npm run dev
```

更完整的启动、数据库和环境说明见 [`docs/08-本轮实施方案/README.md`](docs/08-本轮实施方案/README.md) 及 [`docs/DEVELOPMENT_PLAN.md`](docs/DEVELOPMENT_PLAN.md)。

## 交付流程

开发前在飞书说明计划并在对应 GitHub Issue 认领；开发结束后提交关联 Issue 的 PR，写明验证证据；PR 合并到 `main` 后再关闭 Issue。不要在未完成验证或合并前关闭 Issue。
