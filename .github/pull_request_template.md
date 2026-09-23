<!-- Title should follow Conventional Commits, e.g. `feat: ...`, `fix: ...`, `docs: ...` -->

## Description
<!-- Describe the user result and the boundaries of this change. -->

## Type of Change
- [ ] Bug fix
- [ ] New feature
- [ ] Breaking change
- [ ] Documentation / Test
- [ ] Refactor / Performance
- [ ] Configuration / Build / CI

## Related Issue / Task
<!-- LingDoc: T01–T16 + slice + task/issue link. Other PRs: issue or “not applicable”. -->

## LingDoc Delivery
<!-- For non-LingDoc changes this section may be removed. This is a disclosure,
not machine-verified evidence. See docs/08-本轮实施方案/PR-审查与门禁.md. -->

- 用户可操作结果 / 本次只交出的最小接口：
- 提供方 / 实际消费者 / 独立评审人：
- 契约影响：无 / HTTP schema / 行为场景 / Go 小接口；受影响消费者与同步方式：
- 尚未接真的部分与后续任务：
- 已知风险 / 回退办法（迁移注明数据影响）：

| 本次验证的路径 | 实际模式：mock / real_api_fake_model / real / 不适用 | 证据与限制 |
| --- | --- | --- |
| 待填写 | 待填写 | 命令、提交 SHA、测试输入、结果；历史实测须注明原运行时间和 SHA |

<!-- 不把局部真实模型调用概括为整个功能 real；读取历史证据文件不等于本次又调用模型。
未运行用 NOT RUN + 原因；SKIPPED 不算 PASS。本机绝对路径不是其他人可访问的证据。
不得上传 token、密码、真实私密材料；使用合成数据和脱敏日志。 -->

## Testing
<!-- Exact commands, relevant CI runs/artifacts, positive and negative cases.
Record current failures and base-vs-head reproduction before calling them baseline failures.
Static contract validation does not test the HTTP provider, DB, browser or real model.
A new interface also needs its provider/consumer tests. -->

## Checklist
- [ ] `git diff --check origin/main...HEAD` passes
- [ ] Changed source files are formatted
- [ ] Targeted tests for changed packages/components pass, including key failure paths
- [ ] Applicable lint/type checks pass; unrelated baseline failures have reproducible evidence
- [ ] Verification identifies the tested commit and distinguishes PASS / FAIL / NOT RUN
- [ ] Self-reviewed the code; related docs/contracts/examples are synchronized
- [ ] Breaking changes, remaining Mock and rollback limits are disclosed
- [ ] An independent reviewer is requested; contract changes include the actual consumer

## Screenshots / Recordings
<!-- User-visible changes: show the operation and important error/recovery states.
Screenshots alone do not replace repeatable tests or a second-person walkthrough. -->
