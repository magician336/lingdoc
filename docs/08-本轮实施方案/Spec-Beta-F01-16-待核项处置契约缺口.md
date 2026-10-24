# Spec-Beta-F01-16-待核项处置契约缺口

- 票号：#618
- 基线：`origin/main = 0dad5012d`（合并提交 `0dad5012`，含 #615 的 F01-07 口径变更与 #617 的 BETA_STATUS 第五轮）
- 隔离环境：484（SQLite，端口 18087，pid 58932，二进制由 `96d9320e` 重建）
- 跑批现场：`D:\wt\lingdoc-485-evidence\track-d-experiment\run-20261024-195932-96d9320e\f01-report.json`
- 性质：**契约表达缺口**（不是服务端缺陷）。本 Spec 不改服务端、不改验收门槛、不改判定口径。

---

## 1. 结论（先写不可变契约）

### 1.1 裁定

**结论 B：契约（`workflow.json`）F01-16 的请求体与真实用户流程不一致，服务端行为符合设计。**

- 服务端 `confirmChapter` 要求：提交的 `review_decisions` 与章节的 `review_items` **严格双射**（每个待核项一条处置），否则 `ErrInvalidState` ⇒ HTTP 422 `invalid_state`「当前阶段不能执行该动作。」。
- 契约 `workflow.json` 的 F01-16 把 `review_decisions` **写死成 1 条**（`review_item_id` 取 `{{review_id}}`，即 F01-09 从候选 `/data/review_items/0/id` 捕获的第 1 条）。这只在「模型恰好只产出 1 条待核项」时成立。
- 真实跑批中候选产出 **3 条**待核项 ⇒ 服务端 `len(seen)=1 != len(known)=3` ⇒ 422。

### 1.2 服务端无缺陷：四重独立佐证

| # | 佐证 | 位置 |
|---|---|---|
| 1 | 服务端把 `retained_warning` 与 `resolved` 并列为合法处置，且要求逐项双射 | `internal/lingdoc/candidateadoption/confirmation.go:135-166`（`validateReviewDecisions`；第 155 行列出两种 disposition，第 162-164 行 `len(seen) != len(known)` ⇒ `ErrInvalidState`） |
| 2 | 契约语义层已明确规定该行为 | `docs/08-本轮实施方案/contracts/scenarios.json` F15：`review_decisions: []` ⇒ 断言「任意待核项缺少处置决定时拒绝确认。」、`response_example: invalid_state`；断言表另有一条「确认缺少任何一项 review_decision 则 422。」 |
| 3 | 真实用户流程逐条处置每个待核项 | `frontend/src/views/lingdoc/Workspace.vue:2404-2408`：`current.review_items.map(item => ({review_item_id: item.id, disposition, reason}))` |
| 4 | 仓内既有的真实活跑脚本已用同样写法 | `scripts/lingdoc_mock/run_t15_10_live.py:85-87`（`decisions()` 对全部 `review_items` 逐条生成 `retained_warning`） |

### 1.3 真实 HTTP 实测（零 provider 额度，隔离环境 484）

对上一轮跑批留下的章节版本 `f3a0cf81`（project `e8522a4e`，spec_revision 1）发真实请求：

| 用例 | 决策条数 | 实测 | 期望 | 判定 |
|---|---|---|---|---|
| A | 2 / 3 | **HTTP 422** `invalid_state`「当前阶段不能执行该动作。」 | 422（契约 F15 明确规定） | **PASS** |
| B | 3 / 3 | **HTTP 201**，confirmation `8e0beca6`，`valid=true`，3 条决策全部 `retained_warning` | 201 | **PASS** |

证据：`D:\wt\lingdoc-485-evidence\track-d-experiment\verify-618-confirm\confirm-dual-mapping-verdict.json`。

**推论**：把第 2 条决策换成第 3 条即可通过；**唯一**的阻塞是契约提交了 1 条而不是 N 条。服务端逻辑一字未改即可满足。

### 1.4 `retained_warning` 的精确定义（三处独立佐证）

**「保留该待核警告，不阻断放行，但必须连同原文与理由冻结，并出现在交付文件附录中。」**

1. 冻结层：`internal/lingdoc/delivery/snapshot.go:736-755` —— 处置与待核项必须双射；`retained_warning` **不阻断**，只产出 advisory `retained_review_item`（注释原文：「A retained item does not block: it is reported so the caller keeps it in the released file instead of dropping it silently.」）。
2. 契约语义层：`scenarios.json` F15 断言「retained_warning 连同原文和理由冻结，并出现在内部演示文件附录。」
3. 真实实测：`POST /projects/{id}/checks`（交付检查）→ HTTP 200，`status=passed`，3 条 `retained_warning` 被记为 **`severity: warning`** 的 `review-items-decided` issue（不是 blocking）。

### 1.5 保留警告后是否继续 F01-17～F01-23

**应当继续。** `retained_warning` 不阻断任何后续节点。实测：F01-17（`confirmChapter` method，0 待核项）→ **201**；F01-18（`getProject`）→ **200**，`project_version=9`。

上一轮跑批停在 F01-16 是**执行器的既定失败中止行为**（状态码与契约声明的 201 不符即中止，后续节点为 NOT RUN），不是契约要求在此停下。

### 1.6 遗留：F01-19 稳定化段缺失（**另开票 #619，不在本 Spec 范围**）

F01-19 `prepareRelease` 的服务端前置（`internal/lingdoc/workspacecore/release_gate.go:207-241` `AssertRevisionStabilized`）要求：若 `current_context_revision > 1`，必须存在「已应用且已稳定化」的 ChangeSet，否则 422 `context_unstable`。实测该 project 的 `current_context_revision = 2`，而 F01 的 28 个节点里**没有**任何稳定化节点 ⇒ F01-19 必然 422。实测的稳定化路径见 #619 票面。

**本 Spec 不碰这一段**，但必须记录：#618 修好后，F01-19 仍是下一个阻塞点。

---

## 2. 设计裁定

### 2.1 要解决的问题

契约无法表达「**对该章节的全部待核项逐条给出处置**」——因为：

- 契约变量语法只有 `{{name}}` 单值占位（`workflow.json` 的 `variable_syntax` 字段；`scripts/lingdoc_mock/run_f01.py:57` 的 `VARIABLE` 正则）；
- 待核项的**数量与 ID 由模型运行时决定**，静态契约无法写死；
- 但契约语义层（F15）要求逐项处置 ⇒ **契约的表达能力落后于它自己的语义**。

### 2.2 裁定：引入一个显式的「按运行时集合展开」指令

在契约请求体里允许一个展开指令节点：

```json
"review_decisions": {
  "$expand": {
    "collection": "question_review_items",
    "item": {
      "review_item_id": {"$item": "id"},
      "disposition": "retained_warning",
      "reason": "仅作内部演示，保留该项并随文件明确列出。"
    }
  }
}
```

语义：

- `collection`：一个**已捕获变量名**，其运行时值是数组；
- `item`：元素模板。其中 `{"$item": "<字段名>"}` 表示「取当前元素的该字段」；其余值按现有 `{{var}}` 规则替换；
- 展开结果替换整个 `$expand` 节点，**类型是数组** ⇒ 与 openapi 的 `review_decisions` 数组 schema 兼容，wire 格式不变。

### 2.3 关键约束（每条都有否决理由）

| 约束 | 理由 |
|---|---|
| **不改服务端** | 服务端行为已被四重佐证证明正确；改它会削弱「每个待核项都必须处置」的人工复核语义 |
| **不放宽判定** | 展开出来的条数必须**恰好等于**章节待核项数；少一条仍应被服务端 422 拒绝 |
| **不静默** | `collection` 变量不存在、不是数组、元素缺字段 ⇒ **抛错停止**，绝不补一条、绝不静默转成 PASS |
| **规则只有一份** | 静态校验器（`validate_artifacts.py` 的 `expand()`）与跑批执行器（`run_f01.py` 的 `substitute()`）必须调用**同一个**展开实现；否则两处规则会漂移 |
| **执行器不理解业务** | 展开实现是通用的「集合→数组投影」，不含 `review_decisions`/`review_items` 之类业务名 |
| **契约语义不变** | `scenarios.json` 的 F15 规则、服务端语义、验收门槛全部不动；只是让 workflow 能表达它自己已经声明的语义 |

### 2.4 展开语义的唯一实现放在哪

新增 `scripts/lingdoc_mock/workflow_expand.py`，提供 `expand_value(value, variables)`：

- `scripts/lingdoc_mock/run_f01.py` 的 `substitute()` 改为委托给它；
- `docs/08-本轮实施方案/contracts/validate_artifacts.py` 的 `expand()` 改为委托给它（沿用该文件已有的 `importlib.util.spec_from_file_location` 加载 `check_lingdoc_routes.py` 的既有做法）。

### 2.5 契约侧的具体改动

| 步骤 | 改动 | 理由 |
|---|---|---|
| F01-13 `saveChapter` | `capture` 增加 `question_review_items: /data/review_items` | 保存章节后的响应体含该章节**当前的全部待核项**（采纳继承来的那几条） |
| F01-15 `saveChapter` | `capture` 增加 `method_review_items: /data/review_items` | 同上；method 章节运行时为 0 条 ⇒ 展开为空数组 |
| F01-16 `confirmChapter` | `review_decisions` 改为 `$expand`（collection=`question_review_items`） | 对全部待核项逐条 `retained_warning` |
| F01-17 `confirmChapter` | `review_decisions` 由 `[]` 改为 `$expand`（collection=`method_review_items`） | 与 F01-16 同一条规则；method 展开后仍是空数组，但若将来有待核项也能正确处置 |

**F01-13 的 `expected_chapter_version_id` 仍是 `{{accepted_question_version}}`**，不受影响。

**openapi.json 不改**（wire 格式不变，`$expand` 是发请求前才展开的契约内部表达）。

---

## 3. 验收矩阵

| ID | 断言 | 判定方式 | 证据来源 |
|---|---|---|---|
| A1 | 服务端语义保持：缺一条处置仍返回 422 `invalid_state` | Go 现有用例 + 本 Spec §1.3 实测 A | `internal/lingdoc/candidateadoption/confirmation_test.go`；`confirm-dual-mapping-verdict.json` |
| A2 | 真实用户顺序可完成章节确认（逐条处置全部待核项 ⇒ 201） | 本 Spec §1.3 实测 B | `confirm-dual-mapping-verdict.json` |
| A3 | 契约静态校验器接受改动后的 `workflow.json` | `python docs/08-本轮实施方案/contracts/validate_artifacts.py` 退出码 0 | 校验器输出 |
| A4 | 静态校验器**仍然拒绝**「决策集合 ≠ 待核项集合」 | 负向用例：把 F01-16 的 collection 指向一个只含 1 条的集合 ⇒ 校验器必须报错 | 新增守护测试 |
| A5 | 跑批执行器把 `$expand` 展开成 N 条决策 | 单元测试：给定 3 个待核项 ⇒ 展开结果 3 条、ID 与集合一致 | 新增守护测试 |
| A6 | `collection` 变量缺失 / 非数组 / 元素缺字段 ⇒ **抛错停止**，不产生请求 | 负向单元测试 | 新增守护测试 |
| A7 | 静态校验器与执行器用的是**同一份**展开实现（规则只有一份） | 守护测试：断言两处都从 `scripts/lingdoc_mock/workflow_expand.py` 导入 | 新增守护测试 |
| A8 | 既有候选采纳、版本冲突、哈希校验、重放幂等测试继续通过 | `go test ./internal/lingdoc/candidateadoption/...` ＋ `go test ./internal/lingdoc/workspace/...` | 测试输出 |
| A9 | 既有 F01 runner 回归与契约守护测试继续通过 | `python scripts/lingdoc_mock/test_workflow_runner.py` 等现有脚本全绿 | 测试输出 |
| A10 | `retained_warning` 的判定与执行器行为一致，不得静默转成 PASS | 守护测试：展开出的每条决策 `disposition == retained_warning`；且契约里不得出现把 422 当 201 的分支 | 新增守护测试 ＋ 跑批报告 |
| A11 | 真实跑批中 F01-16 与 F01-17 均取 2xx | 修复后完整跑批的 `f01-report.json` | 跑批证据目录 |
| A12 | 改动未越过路径所有权 | 登记处归属核对：`workflow.json`/`run_f01.py` = PF-10；`validate_artifacts.py` = PF-14（跨面借用需注记）；新文件登记归 PF-10 | 登记处补记 PR |

**三视角分离**：A1/A2 是**契约判定**；A3/A4/A5/A6/A7/A9 是**执行器与静态校验器结果**；A8/A10/A11 是**自动化测试与真实跑批**。三者不互相替代。

---

## 4. 风险与改回成本

| 风险 | 影响 | 改回成本 |
|---|---|---|
| 新增 `$expand` 语法被误用成「隐式补齐」 | 会掩盖漏处置 | 守护测试 A6 强制「缺变量即抛错」；改回 = 删掉 `workflow_expand.py` 与两处委托，契约改回字面量数组 |
| `validate_artifacts.py` 属 PF-14 面，本次由 PF-10 借用 | 所有权越界 | 在登记处补记跨面借用（owner 仍归 PF-14）；改回 = 还原该文件的 `expand()` |
| `validate_artifacts.py` 的 `expand()` 是**静态**校验用的（用契约自己的 reference_response 当运行时） | 展开依赖 capture 已声明 | 契约改动与校验器改动必须在同一个 PR 内落地，否则静态校验会因缺变量而失败 |
| F01-19 稳定化缺口不在本 Spec 范围 | 修好 #618 后 F01-19 仍会 422 | 另开 #619 单独处理；本 PR 不得为此放宽 prepareRelease 的前置 |

---

## 5. 不做的事

- 不改服务端状态机、不改 `validateReviewDecisions`、不改 `delivery/snapshot.go`。
- 不改 `scenarios.json` 的 F15/F20 规则与负向用例。
- 不改 `openapi.json`。
- 不改 `docs/08-本轮实施方案/contracts/frozen-input.canonical.json` 与 `frozen-input.sha256`（`check_frozen` 的口径未变）。
- 不改任何验收门槛，不把 NOT RUN 改判 PASS。
- 不动 F01-19 及其后的稳定化段（另开 #619）。
