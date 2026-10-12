# Spec：G6 真实试用窗口、指标与扩大决策

> 本 Spec 负责把真实试用结果变成可审计的扩大、继续试用或暂停决策。它消费 G6 运行观测和 G1–G5 的质量证据，不把一次报告或单次演示当作总体产品结论。

## Problem Statement

当前 G6 指标脚本可以根据输入样本计算人工投入、来源支撑率、漏改、误报、未确认项和导出损失，但真实试用窗口的执行时间、对象版本、审核人、人工裁定和补救复跑可能缺失。没有锁定的窗口和可追溯样本，`expand` 只表示脚本算出了结果，不能代表产品适合扩大试用。

## Solution

建立版本化 Beta 试用窗口。窗口锁定纳入用户/项目、模板/RuleSet、runtime mode、起止时间、目标样本量、最低可报告样本量、人工支持、脱敏方式和退出条件。每个样本绑定真实运行证据、权限快照、版本、人工裁定、失败归因、修复/回退和关键场景复跑。只有依赖 verified、质量 PASS、runtime mode real 且证据完整的样本进入效果统计；样本不足、缺分母、P0/P1 或证据不完整时输出 NOT READY/BLOCKED。

## User Stories

1. As a 产品负责人, I want 创建一个版本化试用窗口, so that 统计范围不会事后改变。
2. As a 产品负责人, I want 锁定纳入用户和项目, so that 样本边界清楚。
3. As a QA 负责人, I want 锁定模板、RuleSet、运行模式和时间窗口, so that 不同版本结果不会混在一起。
4. As a 试用执行者, I want 每个样本有脱敏 project_id、fixture_id 和 sample_version, so that 结果可以复查。
5. As a 试用执行者, I want 记录主路径、关键失败、人工复盘、修复/回退和复跑, so that 成功结果不会掩盖失败。
6. As a QA 负责人, I want 区分 mock、real_api_fake_model 和 real, so that 只有真实样本进入效果汇总。
7. As a 产品负责人, I want 记录人工投入时间, so that 我知道用户成本。
8. As a QA 负责人, I want 记录来源支撑率、漏改、误报、未确认项和导出损失, so that 产品价值和风险可比较。
9. As a QA 负责人, I want 把规则误报和模型误报分开, so that 修复责任明确。
10. As a 负责人, I want P2 问题有人工补救和复跑记录, so that `expand` 不会忽略已知缺陷。
11. As a 产品、QA 和安全负责人, I want 共同签署扩大、继续试用或暂停决策, so that 决策责任明确。
12. As a 产品负责人, I want 样本不足时只看到原始计数和 NOT READY, so that 系统不会制造统计确定性。
13. As a 审计人员, I want 查看决策使用的样本、分母、失败和回退证据, so that 决策可追溯。
14. As a 试用执行者, I want Word 文件人工往返结果关联到样本, so that 文件质量不被程序化 roundtrip 替代。
15. As a 产品负责人, I want 关闭或暂停窗口而不删除历史样本, so that 后续复盘仍可进行。

## Implementation Decisions

- 复用 G6 质量证据格式、G6 运行观测、Beta 首版协议和现有脱敏 fixture；新增 TrialWindow、TrialSample、MetricObservation 和 DecisionRecord 的最小服务接口。
- TrialWindow 保存窗口 ID、起止时间、纳入项目/用户、目标样本、最低样本、模板/RuleSet、runtime modes、人工支持、脱敏方式、退出条件和状态。
- TrialSample 必须绑定 fixture digest、sample_version、脱敏 project/tenant、template_version、ruleset_hash、context_revision、运行模式、依赖版本、权限快照、输入/输出 hash、执行时间、执行者和审核者。
- 每个样本必须记录主路径、关键失败、人工复盘、修复/回退和关键场景复跑的 process evidence；缺少任一必需项不得进入效果分母。
- 只有 `runtime_mode=real`、依赖 verified、quality_result=PASS、没有 P0/P1 且证据完整的样本进入效果统计。
- 指标字典固定为：人工投入时间、来源支撑率、漏改数、规则/模型误报率、未确认项数、导出损失数。每项保存分子、分母、缺失值和版本。
- 误报必须按规则误报/模型误报区分；来源引用不自动计为“来源支撑”。
- 样本或分母不足输出 `NOT READY`；依赖未验证、权限/数据完整性风险或 P0/P1 输出 `BLOCKED`；只有满足窗口门槛和复跑条件才允许 `expand`。
- P2 问题可以继续试用，但必须有人工补救、责任人、修复版本和关键场景复跑记录。
- DecisionRecord 记录产品、QA、安全责任人、决策时间、窗口、样本统计、失败/回退证据和决定（expand/continue/pause）。
- 文件质量当前只要求 Microsoft Word 打开、编辑、保存、关闭、重开；WPS 兼容性作为后续专项，不进入当前 Beta/G6 主门禁。
- 关闭或暂停窗口只改变窗口状态，不能删除样本、审计、运行事件或历史导出证据。

## Testing Decisions

- 最高 seam 是 TrialWindow/Decision ApplicationService 和 G6 运行证据查询接口。
- 必验成功：创建/锁定窗口、纳入样本、real 样本筛选、指标计算、P2 补救复跑、NOT READY、BLOCKED、expand、pause 和历史读取。
- 必验失败：事后扩大样本范围、混入 mock、缺分母输出比例、依赖未验证进入效果统计、无人工裁定标 PASS、P0/P1 仍存在却 expand、删除历史样本。
- 使用已存在 G6-01～G6-06 fixture 和新建的缺证据/样本不足/分母缺失反例；验证结果必须能回指原始脱敏证据。
- PostgreSQL/SQLite 持久化、重启读回、并发窗口更新和幂等重试需要验证。
- 前端若提供窗口页面，覆盖锁定范围、样本状态、缺失证据、指标分母、决策责任人和暂停/关闭状态。

## Out of Scope

自动判断学术质量、替专家裁定证据、通用 A/B 测试平台、生产计费、组织审批流、G1–G5 领域实现、运行控制/暂停 API 的底层实现和 Word 以外的 Office 兼容性。

## Further Notes

当前 G6-06 的 10 个 real 样本和 `expand` 只作为一个历史试用窗口输入；本 Spec 要求后续窗口补齐真实时间、对象版本、责任人、人工裁定、P2 补救和关键场景复跑，不自动继承旧结论。
