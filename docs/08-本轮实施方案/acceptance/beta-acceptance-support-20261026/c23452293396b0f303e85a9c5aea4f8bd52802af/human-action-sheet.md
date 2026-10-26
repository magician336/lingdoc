# 需要**你本人**完成的动作单（机器不能代替的部分）

> 本文件里的每一条都**不**由机器代判。机器只负责把原始事实摆出来、把选项列清楚、把后果写明白。
> 证据目录：`D:\wt\lingdoc-485-evidence\track-d-experiment\run-final-20261025-093259-c2345229\`
> 运行批次：`f01-20261025T013341Z-5105ae32`｜项目 `0a6c35d0-9f5d-4a70-b9cf-3887179b8cd0`｜代码 head `c2345229`

---

## 动作 A：四条契约散文断言的真实人工判定

### 为什么需要你

这四句是 F01 契约里**唯一**需要人判断的部分（机器断言 64 条已全过）。服务端目前**没有**「人工判定」的审计记录类型（#649 在补），所以在补齐前，**任何**判定都只能停在「声明」档（`claimed`），**不能**被核验为权威 ⇒ 两条放行判定路径都判 `NOT RUN`。

### 判定口径（跑批器核验权威性时逐字段比对）

`actor` / `authorization` / `channel` / `occurred_at` / `assertion_id` / `run_id` / `evidence_ref`
——服务端落下的审计记录必须与判定记录**逐字段相等**（`scripts/lingdoc_mock/manual_decision_audit.py` 的 `AUDIT_FIELDS` ＋ `verify_against_audit`）。

### 四个选项的语义

| 判定 | 含义 |
|---|---|
| `passed` | 断言成立（事实与断言相符） |
| `held` | 有人判定了，但断言**不成立**（判了但没过） |
| `rejected` | 同 `held` 的语义（跑批器把两者都映射为 `held`，原判定保留在 `verdict` 字段） |

⚠️ **不允许**因为「机器断言全过」就替这四句判 `passed`；也**不允许**把 `machine_evaluation` 伪装成人工判定。

---

### A-1 `F01-06b`（`createChangeDraftPostPolicyProposal`，期望 201）

**契约原文（逐字）**
> 把「允许用这份资料做模型生成」写成一条显式提案，绑定该资料当前的修订号与项目的上下文修订号；版本不符即冲突，不猜版本。

**要求你判断的事实**：这条「允许用这份资料做模型生成」是否被写成了一条**显式提案**，并且该提案**绑定**了资料当时的修订号与项目的上下文修订号（而不是事后补记、或绑到别的版本）。

**本轮真实证据**（全部来自本轮跑批的库，不是演示数据）
- 显式提案行：`lingdoc_change_sets` 的 `id = 23548d59-e305-4803-83cc-36c2ad4d3cf9`
  - `reason` = 「Owner 明确同意把这份合成资料用于模型生成（许可钉在这一版资料修订上）。」
  - `created_by` = `619a1613-bd14-4eae-b4b8-5d3518e0c2ab`（本轮 owner 身份）
  - `base_context_revision` = 1 → `target_context_revision` = **2**（**绑定了项目的上下文修订号**）
  - `base_spec_revision` = 1 → `target_spec_revision` = 1
  - `fields_json.decision_by` = 同上 owner，`decided_at` = `2026-10-25T01:33:42.5535793Z`
- 提案**钉住**的资料版本：`lingdoc_g3_records` 里 `kind = semantic_plan`、`record_key` = 上述变更集 id
  - `expected_version` = **1**（资料当时的版本）
  - `before_hash` = `3355ae93a4338f339064849dcbdb400b5a49f48556497dcca15f9e797b86978a`（当时内容哈希）
  - `before.source_version_hash` = `33998f4916422c7960ce6f945c59d21d6a8d0c62a020d2a43787db1a8ef2ad9c`（当时的内容指纹）
  - `proposed_asset_policy.model_use_allowed` = **true**，`scope` = `personal`，`purpose` = 「合成材料上的模型生成许可：F01 契约显式签发，绑定该资料的当前修订。」
- 审计序列（本轮）：`change_set.draft` → `g2.asset_policy.semantic_delta`（`lingdoc_project_audits`）

**支持该断言的证据**：提案是**独立的一行**（`change_set.draft` 审计行），带 owner 的 reason 与 decided_at；`semantic_plan` 把资料版本（1）与上下文修订（1→2）**钉在提案里**；`before_hash` 与 `before.source_version_hash` 都在。
**反对/需要注意的证据**：提案与「应用」发生在**同一秒**（`01:33:42.55`）—— 审计序列里 `change_set.draft` 与 `semantic.apply` 相邻，**没有**明显的时间间隔来区分「先写提案、后应用」；「不猜版本」这半句在机器上体现为 `expected_version` ＋ 乐观锁（应用前会校验版本与内容哈希未变），而不是一句断言。
**当前证据的缺口**：数据库行**不能**证明「写这条提案的人当时心里想的就是这句话」；这属于业务意图，只能由人判断。

---

### A-2 `F01-06c`（`assessChangeSet`，期望 200）

**契约原文（逐字）**
> 变更集评估后才允许应用；服务端的合法前态只有 assessed。

**要求你判断的事实**：变更集**是否在评估之后**才被应用；服务端的合法前态**是否只有 assessed**（不存在「未评估也能应用」或「评估结果被绕过」）。

**本轮真实证据**
- 变更集最终 `status` = **stabilized**（经历 assessed → applied → stabilized）
- 审计序列（本轮，`lingdoc_project_audits` 的 action 列，按时间序）：
  `change_set.draft` → **`change_set.assess`** → `g2.asset_policy.semantic_delta` → `semantic.apply` → **`project.change_set.apply`** → …
  ⇒ `assess` 在 `apply` **之前**。
- 评估记录：`lingdoc_g3_records` 里 `kind = assessment`、`record_key` = 变更集 id（`version = 1`）
- 应用前后的授权：`authorization.manage`（owner）出现在 `change_set.assess` 之后、`project.change_set.apply` 之前

**支持该断言的证据**：审计序列里 `assess` 先于 `apply`；存在一条独立的 `assessment` 记录；应用前有一次 `authorization.manage`。
**反对/需要注意的证据**：**审计行的顺序不是因果证明** —— 它只说明「评估这件事发生过」且「应用也发生过」，不能单独证明「应用被评估**门禁**挡住过」（即服务端是否真的拒绝过「未评估就应用」）。
**当前证据的缺口**：「合法前态**只有** assessed」是一个**服务端行为**断言 ⇒ 要证明它，需要一条**负向**证据：拿一个**未评估**的变更集去应用，看服务端是否拒绝。**本轮没有做这条负向验证**（不在本轮授权范围内跑破坏性操作）⇒ 若你判 `passed`，请知悉这一条是**推理 + 代码路径**而非本轮实测；或让我另开一条负向验证（可用另一个一次性变更集，不碰本轮这条）。

---

### A-3 `F01-06d`（`applyChangeSet`，期望 200）

**契约原文（逐字）**
> 应用这一步才真正写下 model-use 许可记录（对象 kind = project_asset_policy），并把资料推进到新一版修订。

**要求你判断的事实**：**应用这一步**是否才真正写下 model-use 许可记录（对象 kind = `project_asset_policy`），并**把资料推进到新一版修订**（版本确实前进了，不是记录与版本各说各话）。

**本轮真实证据**
- ⚠️ **这一条在契约里没有任何机器断言**（`F01-06d` 的 `checks` 为空）⇒ **它是四条里最纯的人工判断**。
- 许可记录确实存在：变更集行 `status = stabilized`；`lingdoc_project_audits` 有 `semantic.apply` 与 `project.change_set.apply` 两行；`semantic_plan` 的 `proposed_asset_policy.model_use_allowed = true`
- 资料**确实前进了一版**：`lingdoc_project_assets.asset_revision` 由 **1 → 2**（`updated_at = 01:33:42.5520025`，与 apply 同一时刻）
- `lingdoc_asset_revisions` 两行：
  - `revision_no = 1`，`content_hash = 33998f49…`，`status = **superseded**`
  - `revision_no = 2`，`content_hash = 33998f49…`，`status = **active**`
  ⇒ 前进的是**版本号**，内容指纹**未变**（换的是许可，不是材料内容）
- A1 的精确相等关系成立：声明 `expected_version = 1`，共享常量推进量 `assetPolicyVersionAdvance = 1` ⇒ 目标版本 = 2 = 实际的 `asset_revision` ✔

**支持该断言的证据**：许可记录与版本推进在**同一次 apply**（同一秒、同一审计对）里发生；旧修订被标 `superseded`、新修订 `active`；A1 的声明与实际精确相等。
**反对/需要注意的证据**：`asset_revision` 由 **1 变 2**，但 `content_hash` **两版相同** ⇒ 「推进到新一版修订」在**版本号**上成立、在**内容**上没有变化（这正是「许可变更」的性质：改的是许可，不是材料）。另：`proposed_asset_policy` 里 `source_version_hash` 是**空串**（提案阶段还没绑定指纹），指纹是在**应用**时落的 ⇒ 「许可钉住内容指纹」这件事在 F01-06d 这一步**尚不可见**，它是 A-4 的对象。

---

### A-4 `F01-06e`（`getProjectAssetPolicy`，期望 200）

**契约原文（逐字）**
> 把许可读回来看：它带 model_use_allowed=true，且钉在这一版资料修订上。

**要求你判断的事实**：**读回**的许可是否真的带 `model_use_allowed = true`，且**钉在这一版资料修订**上（许可绑定的内容指纹与资料当时的内容一致，不是旧版本的许可）。

**本轮真实证据**
- 本节点**有两条机器断言**：`/data/model_use_allowed` = `true`（`equals`）、`/data/version` = `version_bound`
  ⇒ 机器侧已判**通过**（本轮 64/64 全过）
- 读回对应的库事实：
  - 许可的内容指纹 = `33998f4916422c7960ce6f945c59d21d6a8d0c62a020d2a43787db1a8ef2ad9c`
  - `lingdoc_asset_revisions` 的 **active** 修订（`revision_no = 2`）的 `content_hash` = **同一个** `33998f49…`
  ⇒ 许可钉住的指纹与**当前 active 修订**的内容**逐字相等** ✔
- 冻结快照里也带着这一份许可：`lingdoc_release_snapshots.frozen_input_json` 里的 `asset_versions` = `[{asset_id: cac1fd1f…, asset_revision: 2}]`、`policy_asset_ids` = `[cac1fd1f…]`

**支持该断言的证据**：机器断言已过；许可指纹与 active 修订的内容哈希**逐字相等**；快照里记录的资产版本是 2（与许可同版）。
**反对/需要注意的证据**：`version_bound` 断言**只**核「版本号落在允许范围」，不核「指纹相等」；指纹相等是我从库里逐字比对出来的，**不是**契约断言直接覆盖的。
**当前证据的缺口**：「读回」的**响应体**按脱敏约定**不在**跑批报告里 ⇒ 若你要看原始响应 JSON，需要在服务端日志里取那一段（`server-trackd.log` 的 `getProjectAssetPolicy` 响应行）。

---

### 你要给我的东西

对 A-1～A-4 各给一行：**断言 id ｜ 你的判定（`passed` / `held` / `rejected`）｜ 理由（一句话）｜ 你依据的具体证据位置**。
再给一个**判定主体与渠道**的声明（谁判的、经什么渠道、依据哪份证据），我据此在 #649 的服务端能力落地后**由服务端落审计记录**（不手写审计行）。

⚠️ 在你给出判定前，四条断言的机器状态**保持 `not_run`**（当前即是），我不做任何自动补齐。

---

## 动作 B：Word 人工检查（#480）

**对象（已落盘，哈希已核）**
- 文件：`D:\wt\lingdoc-485-evidence\track-d-experiment\run-final-20261025-093259-c2345229\export-196b61e0de1242e6247ddb6bf2e91747.docx`
- **完整 SHA-256**：`df5549b044261b6c30463e63cd4e639f0e7f59d116e27e6e429e571a3865639a`（与库里 `lingdoc_export_artifacts.file_sha256` **逐字相等**；字节数 3176）
- 导出产物 id `export-196b61e0de1242e6247ddb6bf2e91747`，`status = verified`，快照 `snapshot-09c6230b71d5024ed19e155e840f8635`

**要做的**（用**真实 Microsoft Word**；不能因为「DOCX 已生成且下载校验通过」就算通过）
1. 打开是否正常（无「内容有问题」提示、无修复提示）
2. 内容完整性：两章正文、**「100」这个数字**、来源标注（`[[source:…]]`）是否都在
3. 排版：标题层级、表格/列表是否可读
4. 附录：**3 条待核项**（`review-1`／`review-2`／`review-3`）是否按「保留并随文件明确列出」呈现
5. 保留警告是否被明确列出（机器侧记的是 `review-items-decided` 的 **warning** 级 issue：3 条 `retained_warning`）

**回填格式**：仓内模板 `docs/08-本轮实施方案/acceptance/beta-first/<sha>/<run>/word-check.md`，**五个必填字段**：Word 产品版本 / Windows 版本 / 原始 DOCX SHA-256 / 修改后 SHA-256 / 检查结果。
⇒ 原始 SHA-256 用上面那个**完整**值，不要用截断前缀。

---

## 动作 C：#546 embedding 授权（**本轮不执行**）

**为什么需要授权**：F01-07（`startGeneration`）的检索需要向量；受控 profile **只放行 chat**，embedding 在 transport 之前就被拒（`model is outside the acceptance budget profile`）⇒ 向量 KB 的检索行为至今**没有**真实验证过。

**若要授权，需要你明确批复的范围**
| 项 | 值 |
|---|---|
| 用途 | 在**向量开启**的 KB 上验证 F01-07 的检索路径（生成链的检索前置） |
| 模型 | 受控 profile v2 之外**单独**放行 `qwen-embedding`（**不得**与 chat 混用同一额度） |
| 预计调用次数 | **1 次**（一个 embedding 批次，绑定一份已解析的合成材料） |
| 费用 | 按 provider 报价；**必须**与 chat 额度**分开**记账（`calls_max` 独立、token 上限独立） |
| 额度上限 | 建议 **1 次调用**、累计 token ≤ profile v2 的 90,000 |
| 材料 | 已在隔离环境的合成材料 `c1197b70-525a…`（`synth-chapter-c.md`，全合成、不含真实实验数据） |
| 验证方法 | 建一个带 embedding 模型的向量 KB → 走 F01-07 → 记录 provider 请求数、账本行、向量表维度>0 的行数 ⇒ 证明「检索命中」是事实 |
| 风险 | 会产生**真实** embedding 请求与费用；向量表会出现维度>0 的行（当前 24 行全为 0） |

⚠️ 在你明确批复前，**不发起任何 embedding 请求、不新建 embedding 模型账本行、不改 profile**。

---

## 动作 D：第二人独立复现（#489）—— **需要一位真人**

#489 的原文要求：**「另一位开发者」**、**干净工作树**、**新测试身份**、**新隔离数据库**、**独立报告**，且**不复用第一人的数据库、动态 ID、令牌或结论**。

⇒ **我（AI 会话）不算「另一位开发者」** ⇒ 这一项必须由**你或另一位真人**完成；我不会用「我自己的重复运行」冒充它。

**我已经准备好的部分**（省你的时间）
- 干净工作树：`D:\wt\lingdoc-final-main`（当前在 `c2345229`）
- 新身份的取得方式（走**真实邀请路径**，不是直接改库）：`D:\wt\lingdoc-485-evidence\scripts\exp_trackd_10_acquire_owner_484.ps1 -Port <端口> -TokenName <新文件名>`
- 新隔离数据库：PostgreSQL 容器 `WeKnora-postgres-dev` 里**新建**一个库（不要动 `weknora_iso`）
- 前置 fail-closed 检查：`D:\wt\lingdoc-485-evidence\scripts\exp_622_preflight.py`（用**新身份**的 token 跑）
- 零 embedding 核对：`exp_620_zero_embedding.py`（before/after）
- 逐节点表与放行结论表：`exp_620_per_node.py`、`exp_619_release_conclusion.py`

**复现记录要写的**（#489 的验收项）：复现人 ｜ 复现所用的 **SHA** ｜ 使用的材料标识与哈希 ｜ 复现结论（独立记 PASS / FAIL / NOT RUN）｜ 与第一人的差异。

⚠️ 若由真人复现，**额度已 4/4 用尽**：复现跑到 F01-07 时**不会**产生新的 provider 调用（会命中幂等记录或被预算门禁拒绝）⇒ **复现结论里 F01-07 及之后必须如实记 NOT RUN**，并说明原因。**不得**为了「让复现跑完」而申请或动用新额度。
