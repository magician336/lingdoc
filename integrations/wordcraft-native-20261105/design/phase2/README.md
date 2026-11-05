# 二期总览：布局级重设计（Q1–Q3）

> 基线：main @ `22eddb87`（PR #6，一期已验收合入）。集成分支：`codex/ui-phase2-20261105`。
> 交互稿：`design/phase2/mock-v4.html`（已经 owner 评审通过；标注"未接真实接口"）。
> 合同红线不变：`native-reading/KIMI-CONTRACT.md`——AI 不读正文、不自动写作、保存态只来自回执。

## 方向决策记录（owner 确认）

1. **不做章节 rail**：产品不声称懂申报书内容结构，定位为"围绕写作的外围"。
2. **左栏 = VS Code 式文件资源管理器**；文件图标按 OS 约定分格式着色（PDF 红 / DOCX 蓝 / TXT 灰）。
3. **会话历史进 AI 面板顶栏**（UPDF 式 ⏱），不进左栏、不进 composer。
4. **划词上下文挂起**（越出"AI 不读正文"合同，待产品决策）。
5. 现有两条"伪页签带"（项目资源/问答/正文·可编辑；视图/已最大化/文档标签）拆解，各归其位。

## 包划分

| 包 | 分支 | 范围 | 依赖 |
|---|---|---|---|
| Q1 | `codex/ui-q1-activity-explorer` | Activity Bar + 文件侧栏（格式图标、导入入口） | — |
| Q2 | `codex/ui-q2-doctabs-statusbar` | 真文档标签条 + VS Code 式状态栏 | — |
| Q3 | `codex/ui-q3-ai-panel` | AI 面板 UPDF 化（顶栏历史/新建、快捷动作、免责行） | Q1（面板开合态挂在 Activity Bar） |

合并顺序：Q1 → Q2 → Q3，每步本地 `cargo check -p wordcraft-ui-egui` + 既有基线（34/54/48/22+6+5）。全部草稿 PR 回集成分支，最终由 gong 决定合集进 main。
