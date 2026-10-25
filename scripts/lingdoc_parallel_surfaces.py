"""The parallel work face manifest: the single place that says *who may touch what*.

What this file is
-----------------
LingDoc's G1-G7 development line is not one queue.  It is a set of **parallel
work faces** -- groups of paths that one executor may own end to end, so that
two executors can work at the same time without editing the same file.

This manifest is the one declaration of that ownership.  For every face it
records

* the face name (unique),
* the **tracker issue** that carries the face (the ticket an executor claims),
* the G1-G7 Spec issues the face delivers,
* the **path set** the face owns, and the kind of each path (``file`` for an
  exact path, ``dir`` for a directory prefix),
* the scheduling status (``available`` / ``claimed`` / ``blocked`` / ``done``),
* who claimed it, if anybody,
* the implementation dependencies the face resolves, if any.

The manifest is **declarative data**.  It answers ownership questions only:
which paths belong to which face.  It contains no decision logic, and it does
not decide whether a face may start -- the *status* field records that, and a
human keeps it current.

Why the path sets must not overlap
-----------------------------------
Two faces that own the same file cannot be worked on in parallel: the second
one to land silently overwrites or conflicts with the first.  The loader
rejects any manifest where two faces' path sets overlap, or where a face
claims a reserved path.  A broken record aborts with a non-zero exit status
-- it never degrades silently.

Terms
-----
``face``
    One parallel unit of work: a name, a path set, and the Spec issues it
    delivers.

``reserved path``
    A path that no face may own, because it is an integration point where
    several faces land at once.  It is integrated in a separate, explicit
    step.

``status``
    ``available`` -- nobody has claimed the face, it can be taken.
    ``claimed`` -- somebody is working on it.
    ``blocked`` -- the face cannot start; it is waiting on an implementation
    dependency it does not own.
    ``done`` -- the face has landed; its paths are part of ``main``.

Shared maintenance points
-------------------------
Some files are not owned by any face because *every* face legitimately has
to change them.  They are not reserved paths (a reserved path is an
integration point, integrated in one explicit step); they are shared
maintenance points, and they are deliberately left out of every face's path
set:

* ``internal/lingdoc/operationcore/ownership_test.go`` -- the guard that
  keeps the idempotency-key rule and the request-digest rule in one place.
  A face that adds a second digest or a second key rule edits it.
* ``internal/database/migration_sqlite_versioned_schema_test.go`` -- holds
  the single ``expectedSQLiteMigrationVersion`` constant.
* ``internal/router/lingdoc_integration_test.go`` -- holds the migration
  file list.
* ``migrations/`` -- the migration files themselves.  **The version number
  is a shared serial resource: whoever merges first takes the next number.**
  A face that adds a migration must rebase onto the integration branch tip
  and re-allocate its version number if the slot it picked was taken.
* ``docs/BETA_STATUS.md`` -- the Beta status document.  Every face reports its
  own four dimensions there, and the management session reconciles the
  rounds, so no single face may own it: declaring it would forbid the other
  faces from updating the status of their own work.
* ``migrations/versioned/000001_agent.down.sql`` -- the rollback side of the
  tenant_id back-fill.  Its list of rows must stay identical to the up side's
  ``tenant_source``, and ``internal/database/migration_agent_tenant_source_test.go``
  is the guard that says so.  A face that changes either one changes both, and
  neither belongs to a single face: they are shared by everyone who touches
  the migration chain (like ``internal/database/migration_sqlite_versioned_schema_test.go``
  above).

The path-overlap check cannot catch this kind of contention, because it is a
race for a *sequence*, not for a path.  It shows up as an explicit merge
conflict -- loud, not silent.  Do not register these files into a face's
path set: doing so would forbid the other faces from changing files they
must change.

How to add a face
-----------------
1. Open a tracker issue for the face and note its number -- the face's ticket
   is what an executor claims, and it carries the face's acceptance criteria.
2. Append a record to ``PARALLEL_FACES`` with the fields listed above,
   including that issue number.
3. Run ``python scripts/lingdoc_parallel_surfaces.py``.  The loader
   validates the record: the face name must be unique, the status must be
   legal, the path set must not be empty, the issues must be known G1-G7
   issues, and no two faces may own the same path.  A broken record aborts
   with a non-zero exit status.
4. Only then hand the face to an executor.

The path diagram (``docs/08-本轮实施方案/G1-G7-Spec-完成路径图.md``) explains
*what* the faces are and *why* the dependency graph does not serialise the
work.  This file is where the ownership is kept current.

Where this file lives
---------------------
It sits in ``scripts/`` and not in ``scripts/ci/``: that directory belongs to
the gate judgement layer, whose whole point is that exactly one module answers
"what runs".  This manifest answers a different question -- "who may touch
which path" -- and a second manifest in the judgement layer would blur that
boundary.  Its guard test lives in ``scripts/ci/`` because that is where the
CI runs the guard tests from.
"""
from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path
import sys
from typing import Any, Iterable, Mapping, Sequence

REPO_ROOT = Path(__file__).resolve().parents[1]

# The path diagram explains what the faces are and why the dependency graph does
# not serialise the work.  This file is where the ownership is kept current.
PATH_DIAGRAM_DOC = Path("docs/08-本轮实施方案/G1-G7-Spec-完成路径图.md")

# The G1-G7 Spec issue line.  #144 is the wayfinder map, not a Spec, so it is
# not part of this set.  A face may only deliver issues from this set.
#
# #393 is the 研究提案 Spec (the research proposal contract) that PF-5
# delivers: it is a G-line Spec like the others, so the set is extended
# rather than worked around.  A face that delivers it may name it.
#
# #391 is the owner_unavailable Spec that PF-4 delivers, for the same
# reason: the set lists the G-line Specs, and a face may only name one it
# actually delivers.
#
# #431 is the 上下文包前端消费 Spec that PF-6 delivers, for the same reason.
# #482 is the Beta 首版放行验收 Spec that PF-14 delivers, for the same reason.
# #451 / #452 / #453 are the three Beta-first acceptance Specs that PF-10
# delivers, for the same reason.
G_LINE_ISSUES = (
    frozenset(range(123, 144)) | {145, 146} | {391, 393, 431} | {451, 452, 453} | {482}
)

# Scheduling status vocabulary.  ``available`` is the only status that means
# "somebody may start this right now".
FACE_STATUSES = ("available", "claimed", "blocked", "done")

# Paths that no face may own.  ``Workspace.vue`` is the integration point where
# every frontend face lands, so it is integrated in one explicit step instead
# of being owned by one face and fought over by the rest.
RESERVED_PATHS = (
    ("frontend/src/views/lingdoc/Workspace.vue", "file"),
)

# The parallel work faces.  Declarative data -- see the module docstring.
PARALLEL_FACES: tuple[Mapping[str, Any], ...] = (
    {
        "name": "PF-1 G6 运维与试用前端",
        "issue": 325,
        "issues": (145, 146),
        "status": "done",
        "claimed_by": "",
        "paths": (
            # 2026-10-23：G6 前端独立化（PF-8 / #384）把 ImpactOpsPanel.vue、
            # runtimeMetrics.ts、runtimeMetrics.test.ts、mainlineChanges.test.js、
            # ImpactOpsPanel.test.js 五个路径的所有权转给了 PF-8 —— 它们随 G6 段
            # 一起搬走了。同日口径表的代码与测试也转给 PF-8：它要随后端补上的
            # 授权拒绝观测演进（PF-8 / #382）。本面已交面，只保留自己的方案档。
            ("docs/08-本轮实施方案/G6-运维前端-指标视图与契约-20261022.md", "file"),
        ),
        "resolves": (),
        "summary": (
            "G6 的运行观测、告警、暂停与试用窗口，后端与前端都已在 main 上："
            "后端 16 条路由在 internal/lingdoc/workspace/impact_routes.go:135-182，"
            "前端客户端在 api/lingdoc/mainlineChanges.ts:112-127，"
            "界面在 views/lingdoc/ImpactOpsPanel.vue:55-83。"
            "票面「前端零文件」的前提已过时，本面据此缩为补真实缺口："
            "把服务端 RuntimeMetrics.counts 投影成值班可读的指标视图，"
            "并给 G6 前端补一层契约保护。"
            "G6 前端的独立面板与 Workspace 集成不在本面，另开票。"
            "2026-10-22 由 magician336 认领并完成；"
            "完成后本面不保留 claimed_by（登记处只允许 claimed 状态带认领人）。"
            "2026-10-23 该独立化由 PF-8（#384）完成，"
            "本面交出 ImpactOpsPanel.vue 与两个测试文件的所有权；"
            "同日口径表 runtimeMetrics.ts / runtimeMetrics.test.ts 也交给 PF-8"
            "（它随后端补上的授权拒绝观测演进）。本面只保留自己的方案档。"
        ),
    },
    {
        "name": "PF-2 G7 AI 动作族",
        "issue": 326,
        "issues": (134,),
        "status": "done",
        "claimed_by": "",
        "paths": (
            # The two packages below are claimed as directories, so that a new
            # file inside them is covered by this face's ownership without a
            # manifest edit.  ``internal/lingdoc/workspace/`` is deliberately
            # NOT a directory here: PF-5 owns a file in it, and a directory
            # claim would overlap.
            ("internal/lingdoc/generation/", "dir"),
            ("internal/lingdoc/candidateadoption/", "dir"),
            ("internal/lingdoc/workspace/selected_rewrite.go", "file"),
            ("internal/lingdoc/workspace/selected_rewrite_model.go", "file"),
            ("internal/lingdoc/workspace/selected_rewrite_review.go", "file"),
            ("internal/lingdoc/workspace/selected_rewrite_review_test.go", "file"),
            # The validity normalization renames the selected rewrite
            # candidate's own status word list to the candidate layer's
            # validity enumeration, so the assertions that pinned the old
            # words move with it.  These files are claimed here for that
            # reason only.
            ("internal/lingdoc/workspace/selected_rewrite_test.go", "file"),
            ("internal/lingdoc/workspace/selected_rewrite_format_test.go", "file"),
            ("internal/lingdoc/workspace/selected_rewrite_observation_test.go", "file"),
            ("internal/lingdoc/workspace/g2_generation_evidence_test.go", "file"),
            ("internal/lingdoc/workspace/selected_rewrite_action_test.go", "file"),
            ("internal/lingdoc/workspace/selected_rewrite_continue_test.go", "file"),
            ("internal/lingdoc/workspace/selected_rewrite_compress_test.go", "file"),
            ("internal/lingdoc/workspace/selected_rewrite_expand_test.go", "file"),
            ("internal/lingdoc/workspace/selected_rewrite_transform_test.go", "file"),
            ("internal/lingdoc/workspace/selected_rewrite_confirmation_test.go", "file"),
            ("internal/lingdoc/workspace/selected_rewrite_revocation_test.go", "file"),
            ("internal/lingdoc/workspace/selected_rewrite_context_test.go", "file"),
            ("internal/lingdoc/workspace/selected_rewrite_context.go", "file"),
            ("internal/lingdoc/workspace/selected_rewrite_unsupported_reference_test.go", "file"),
            ("internal/lingdoc/workspace/candidate_oneconn_test.go", "file"),
            ("internal/lingdoc/workspace/selected_rewrite_source_guard_test.go", "file"),
            # These two files are changed by this branch but were left out of
            # the path set, so their edits were not covered by the overlap
            # check at all.  Claimed here for that reason only.
            ("internal/lingdoc/workspace/client_form_contract_test.go", "file"),
            ("internal/lingdoc/workspace/selected_rewrite_inherited_sources_test.go", "file"),
            # The lifecycle-stage / validity split gets its own assertion file.
            ("internal/lingdoc/workspace/selected_rewrite_lifecycle_test.go", "file"),
            ("internal/lingdoc/workspace/http.go", "file"),
            ("internal/lingdoc/workspace/generation.go", "file"),
            ("internal/lingdoc/workspace/model_run_mode.go", "file"),
            ("internal/database/migration_selected_rewrite_action_shape_test.go", "file"),
            ("internal/database/migration_generation_digest_version_test.go", "file"),
            ("internal/database/migration_rewrite_validity_upgrade_test.go", "file"),
            # The candidate shape migration tests follow the candidate shape
            # normalisation this branch landed.
            ("internal/database/migration_candidate_shape_upgrade_test.go", "file"),
            ("internal/database/migration_sqlite_candidate_shape_test.go", "file"),
            ("internal/lingdoc/candidateadoption/review_adoption_guard_test.go", "file"),
            ("internal/clienterror/catalog.go", "file"),
            # internal/router/lingdoc_routes_test.go moved to PF-8 on 2026-10-23:
            # PF-8's #382/#383 delivery added a comment there (the two new G6
            # endpoints do not move this composition's route count). The
            # registry's overlap check is path-level, so a path has to move to
            # the face that changed it -- one-way, and this face does not
            # claim it again.
            # internal/container/lingdoc_test.go moved to PF-4 on 2026-10-23:
            # it holds the production composition's route-count assertion, and
            # PF-4's archive endpoint adds a route to that composition. PF-2
            # is closed and no longer building, so the path moves with the
            # work rather than being fought over.
            ("internal/lingdoc/writingaction/", "dir"),
            ("frontend/src/views/lingdoc/generationAttempt.ts", "file"),
            ("frontend/src/views/lingdoc/candidateAdoption.ts", "file"),
            ("frontend/src/views/lingdoc/candidateAdoption.test.ts", "file"),
            ("frontend/src/views/lingdoc/GenerationActionPanel.vue", "file"),
            ("frontend/src/views/lingdoc/GenerationActionPanel.test.js", "file"),
            # 集成步新增的四个文件：改写候选的阶段映射（失效判据与「正在应用」
            # 各归其位）、语言评审报告的面板，以及它们各自的断言。
            ("frontend/src/views/lingdoc/rewriteStage.ts", "file"),
            ("frontend/src/views/lingdoc/rewriteStage.test.ts", "file"),
            ("frontend/src/views/lingdoc/LingDocReviewReportPanel.vue", "file"),
            ("frontend/src/views/lingdoc/LingDocReviewReportPanel.test.js", "file"),
            ("frontend/src/api/lingdoc/generation.ts", "file"),
            ("frontend/src/api/lingdoc/generation.test.ts", "file"),
            ("frontend/src/api/lingdoc/generationAction.ts", "file"),
            ("frontend/src/api/lingdoc/generationAction.test.ts", "file"),
            ("frontend/src/api/lingdoc/candidateAdoption.ts", "file"),
            ("frontend/src/api/lingdoc/workspace.ts", "file"),
            ("frontend/src/api/lingdoc/writingAction.ts", "file"),
            ("frontend/src/components/LingDocCandidateAdoptionDialog.vue", "file"),
            ("frontend/src/components/LingDocCandidateAdoptionDialog.test.js", "file"),
        ),
        "resolves": (),
        "summary": (
            "生成服务目前只有自由文本 Instruction 加一个 rewrite 服务，"
            "outline/continue/compress/expand/translate 五个动作没有枚举与各自约束，"
            "「人工 / AI / AI 辅助」来源标注也缺。路径集按 #326 票面实测的「新增纳入」清单登记："
            "动作词汇包 internal/lingdoc/writingaction/（新建）、candidateadoption/*、"
            "generation/store.go、workspace/http.go、clienterror/catalog.go、"
            "router/lingdoc_routes_test.go，以及前端的 generationAttempt.ts、"
            "candidateAdoption.ts、GenerationActionPanel.vue、api/lingdoc/{generation,candidateAdoption,workspace,writingAction}.ts "
            "与 components/LingDocCandidateAdoptionDialog.vue。"
            "workspacecore/human_decision.go 已移出本面：那份门禁名单只被 workspaceHumanGate() 消费，"
            "而采纳路径不走 workspacecore operation pipeline，加项是无消费者的死条目；"
            "改为在 AcceptCandidate 里直接调 workspacecore.RequireHumanDecision。"
            "证据那半边用 #138 已落地的生成接入，"
            "不必等 #137 或 #133 的新行为。"
            "2026-10-22 由 magician336 认领并完成："
            "十四张施工票（#350–#355、#375、#341–#345、#347、#348）、集成步、"
            "并入 main、迁移重编号与契约对齐已随 PR #357 合入 main，合并提交 c4c9ac93；"
            "Spec #134 随之关闭。两张残留票保持开放："
            "#385（动作表按目标范围给动作、执行按管道分）、"
            "#386（两处手抄迁移子集夹具）。"
            "**2026-10-23 一条路径一次性移交给 PF-8**："
            "internal/router/lingdoc_routes_test.go —— PF-8 在 PR #446 里给它加了一条注释"
            "（说明两个新 G6 端点不改变这张路由数量快照），"
            "而登记处的重叠检查是路径级的：改了别面的路径就必须显式移交。"
            "移交是单向的，本面不再声称拥有它（本面路径集 55 → 54 条）。"
            "完成后本面不保留 claimed_by（登记处只允许 claimed 状态带认领人）。"
            "**2026-10-24 #608 跨面借用（owner 不变，故不声明进本面 paths；重复声明会被登记处 loader 直接拒绝）**：#608（F01-11 采纳节点按服务端 HTTP 契约补齐确认载荷与工作副本声明，PR #612 已合入 main） 改动了下列**别的面拥有**的文件，本面**借用**它们、owner 不变：`internal/lingdoc/candidateadoption/adoption_confirmation_http_test.go`（internal/lingdoc/candidateadoption/）；`internal/lingdoc/candidateadoption/adoption_contract_body_http_test.go`（internal/lingdoc/candidateadoption/）。 **2026-10-24 #593 跨面借用（owner 不变，故不声明进本面 paths；重复声明会被登记处 loader 直接拒绝）**：#593（PR #595，检索侧的受控 profile 策略拒绝保留自己的错误形态 ＋ D14 的权威常量，已于 2026-10-24 合入 main，合并提交 5a3e3793）改动了下列**别的面拥有**的文件，本面**借用**它们、owner 不变：`internal/clienterror/catalog.go`（internal/clienterror/catalog.go）；`internal/lingdoc/generation/client_form_contract_test.go`（internal/lingdoc/generation/）；`internal/lingdoc/generation/http_test.go`（internal/lingdoc/generation/）；`internal/lingdoc/generation/service.go`（internal/lingdoc/generation/）；`internal/lingdoc/generation/source_status_vocabulary_guard_test.go`（internal/lingdoc/generation/）；`internal/lingdoc/workspace/generation.go`（internal/lingdoc/workspace/generation.go）。"
        ),
    },
    {
        "name": "PF-3 审计告警接线与 AuditEvent 字段",
        "issue": 327,
        "issues": (130,),
        "status": "done",
        "claimed_by": "",
        "paths": (
            ("internal/lingdoc/workspacecore/machine_read_grant.go", "file"),
            ("internal/lingdoc/workspacecore/audit_failure_alert_test.go", "file"),
            ("internal/lingdoc/workspacecore/audit_event_version_fields_test.go", "file"),
            # These fixtures hand-wrote the column list of lingdoc_project_audits.
            # Adding two columns to that table broke them, and the failure
            # surfaced as an opaque HTTP 500 instead of a schema error, so they
            # now apply the production migration instead of copying the shape.
            ("internal/lingdoc/workspace/change_set_http_test.go", "file"),
            ("internal/lingdoc/workspace/change_set_delivery_test.go", "file"),
            ("internal/lingdoc/workspace/delivery_http_test.go", "file"),
            # 2026-10-24: pr150_migration_history_test.go (the migration
            # subset fixture), audit_event_table_fixture_test.go,
            # save_chapter_policy_test.go and
            # lingdoc_ruleset_delivery_e2e_test.go moved on to PF-7 (#460):
            # #415 needed them there, and the registry's overlap check is
            # path-level.  The handover record is in PF-7's summary.
            ("migrations/sqlite/000052_lingdoc_audit_version_fields.up.sql", "file"),
            ("migrations/sqlite/000052_lingdoc_audit_version_fields.down.sql", "file"),
            ("migrations/versioned/000131_lingdoc_audit_version_fields.up.sql", "file"),
            ("migrations/versioned/000131_lingdoc_audit_version_fields.down.sql", "file"),
        ),
        "resolves": (),
        "summary": (
            "读类 fail-open 落点共 6 处，普通读只占授权审计与机器读取；"
            "平台事务外通道（internal/container/lingdoc.go）写失败返回 nil，"
            "失败连错误都不产生，那条通道本轮不接。"
            "审计事实归 AuditEvent 与 lingdoc_project_audits 表——"
            "该表现只有 7 列，TargetVersion / ProjectRevision 需要独立列加两套迁移，"
            "不是只改 model.go 的字段声明。"
            "路径集按 #327 认领后的实测清单补齐（先例：PF-1 与 PF-2 同样在认领后补齐）。"
            "pr150_migration_history_test.go 是实测补进来的：它演练一个停在 000043 之前的旧库，"
            "当前代码写审计版本列时那个旧库缺列，种数据前必须先把审计表带到当前形状。"
            "internal/lingdoc/workspace/ 下的三个夹具同样是实测补进来的：它们手抄了"
            "lingdoc_project_audits 的列清单，加列后审计写失败，而失败被 HTTP 层转成 500，"
            "看不出来是 schema 漂移；现在改为主动应用生产迁移，夹具与生产按构造一致。"
            "另有两个跑迁移子集的夹具也补进来了：子集停在 000052 之前，审计表没有版本列。"
            "internal/database/migration_sqlite_versioned_schema_test.go 与"
            "internal/router/lingdoc_integration_test.go 按上面的「共享维护点」约定修改，"
            "不登记进任何面的路径集。"
            "**2026-10-24 实测更正**：internal/router/lingdoc_integration_test.go 已由 PF-7（#460）"
            "按 #415 的读侧角色过滤断言登记进 **PF-7** 的路径集（见 PF-7 路径集 ⑤），"
            "不再按「无人登记的共享维护点」处理；"
            "internal/database/migration_sqlite_versioned_schema_test.go 现由 PF-10 登记"
            "（Beta 首版验收证据生产面），本面同样不再按共享维护点处理它。"
            "**本面让出两个文件**（2026-10-23 基线合并时）："
            "workspacecore/service_facade.go 与 workspacecore/typed_contracts.go —— "
            "PF-4 于 2026-10-22 先在 main 上声明了它们（PR #392），而 PF-4 仍是待施工的面；"
            "登记处要求一条路径只有一个拥有者，所以 PF-3 不再声明这两个文件。"
            "**但 PF-3 确实改了它们**：readWithAudit 接缝与 Authorize/GetProject 的接线在 "
            "service_facade.go，AuthorizeRequest 的接线在 typed_contracts.go。"
            "两面的改动由基线合并组合在同一棵树上（合并零冲突），"
            "PF-4 之后动这两个文件时冲突会显式报出来，不会静默覆盖。"
            "本面独占 model.go 与 runtime_control.go，PF-4 因此不得声明 model.go。"
            "本面产出文档：docs/08-本轮实施方案/PF-3-审计告警接线与AuditEvent字段.md 与 T327-验证报告.json。"
            "**2026-10-23 永久移交三条路径给 PF-7**（workspacecore/model.go、workspacecore/service.go、"
            "membership/fixture_test.go）——依据票 #415（AuditEvent 声明字段与落库列一致、读侧角色过滤生效）；"
            "PF-7 = #460。移交是单向的，本面不再声称拥有这三条，也不再需要它们 ⇒ 路径集 17 → 14 条。"
            "**本面已完成**（2026-10-23，认领与完成的人是 magician336）："
            "PR #389 已合入 main（合并提交 5c31fc9b），票 #327 随之关闭。"
            "交付两件事：读类审计写入失败接到 G6 告警源"
            "（EmitAuditFailureAlert 发 Category=audit 的运行事件并被告警规则命中），"
            "以及 AuditEvent 补 target_version / project_revision 两个一等字段并真正落库"
            "（sqlite/000052 与 versioned/000131）。"
            "验收 AC-327-01 至 AC-327-17 全部 passed，证据见 T327-验证报告.json；"
            "合并前健康度复评 H_L 64.4 / F_R 65.0（置信度 B，无独立维度阻断项）。"
            "**本面不释放路径**：done 状态仍占用这 10 条路径，后续面不要重复声明。"
            "**唯一的例外是 runtime_control.go**（2026-10-23 移交给 PF-8）："
            "#383 的运行控制回退动作要落在同一个文件里（回退目标挂在控制项上、"
            "白名单加 rollback），而 PF-8 是正在施工的那一面。"
            "登记处的重叠检查是路径级的（不看 status、不看改动行），"
            "两边同时声明就直接拒绝加载，所以本面把这个路径交出去；"
            "本面在 main 上已交付的 #327 内容（EmitAuditFailureAlert 与 "
            "AuditEvent 的两个独立列）原样保留，不受这次移交影响。"
            "**残留票保持开放**：#387（PostgreSQL 上普通读的 fail-open 不成立）、"
            "#414（平台事务外通道的审计写失败恒返回 nil）、"
            "#415（AuditEvent 声明字段与落库列不一致）、"
            "#416（测试夹具自持一份生产 schema 形状的机制治理）。"
            "Spec #130 保持开放：本面只交付了其中的告警接线与两个版本字段，"
            "角色过滤查询等其余验收点未交付（见 #415）。"
            "**2026-10-24 交面时再永久移交四条给 PF-7**（pr150_migration_history_test.go、"
            "audit_event_table_fixture_test.go、save_chapter_policy_test.go 与 "
            "lingdoc_ruleset_delivery_e2e_test.go）——依据票 #415（AuditEvent 声明字段与落库列一致、"
            "读侧角色过滤生效）：PR #503 在这些夹具上补了迁移子集与列断言，"
            "而登记处的重叠检查是路径级的（不看 status、不看改动行），两边同时声明就直接拒绝加载 "
            "⇒ 本面让出。本面已交面且不再需要这四条 ⇒ **不归还** ⇒ 本面路径集 14 → 10 条。"
            "本面在 main 上已交付的 #327 内容（EmitAuditFailureAlert 与 "
            "AuditEvent 的两个独立列）原样保留，不受这次移交影响。"
        ),
    },
    {
        "name": "PF-4 owner_unavailable 状态与 Owner-only 冻结",
        "issue": 328,
        "issues": (127, 391),
        "status": "done",
        "claimed_by": "",
        "paths": (
            ("internal/lingdoc/workspacecore/member_judgement.go", "file"),
            ("internal/lingdoc/workspacecore/owner_unavailable_single_source_test.go", "file"),
            ("internal/lingdoc/workspacecore/change_draft_edit.go", "file"),
            ("internal/lingdoc/workspacecore/chapter_review.go", "file"),
            ("internal/lingdoc/workspacecore/context_revision.go", "file"),
            ("internal/lingdoc/workspacecore/member_records.go", "file"),
            ("internal/lingdoc/workspacecore/human_decision.go", "file"),
            ("internal/lingdoc/workspacecore/migrated_context_ports.go", "file"),
            ("internal/lingdoc/workspacecore/project_archive.go", "file"),
            ("internal/lingdoc/workspacecore/delivery_export_guard.go", "file"),
            ("internal/lingdoc/workspacecore/membership.go", "file"),
            ("internal/lingdoc/membership/ports.go", "file"),
            ("internal/lingdoc/membership/ports_test.go", "file"),
            ("internal/lingdoc/membership/owner_transfer_test.go", "file"),
            ("internal/clienterror/clienterror.go", "file"),
            # Moved from PF-2 on 2026-10-23: the production composition's
            # route-count assertion follows the archive endpoint this face
            # adds. PF-2 is closed and no longer building.
            # 2026-10-23: this file moved on to PF-8, which added the comment
            # that says why the two new G6 endpoints do not move this
            # composition's route count. The registry's overlap check is
            # path-level, so a path has to move to the face that changed it.
            #
            # 2026-10-23: PF-8 gave the four paths below back after it landed
            # (service_facade.go / typed_contracts.go / context_ports_test.go
            # and contracts/openapi.json). PF-8 only needed them to wire
            # #382/#383; that work is merged, so this face owns them again.
            #
            # 2026-10-23: service_facade.go moves on to PF-13 (G4 模板目录与版本
            # 治理, 票 #248) under #441 §1.3 — the handover is permanent.
            # PF-8 kept typed_contracts.go, which #248 does not touch. The
            # handover record is in PF-4's summary.
            #
            # 2026-10-23: context_ports_test.go and contracts/openapi.json are
            # **borrowed by PF-13** (票 #248), and their owner role belongs to
            # PF-11 (票 #466 / PR #149) by the maintainer's ruling. PF-11 is
            # `done` but **not registered in the registry yet** — its own
            # handover has not landed — so this face still holds them: the
            # registry rejects two faces claiming one path, and an unowned
            # path is worse. They move to PF-11 when PF-11's own handover
            # registers it; the record is in PF-4's summary.
            ("internal/lingdoc/workspacecore/typed_contracts.go", "file"),
            ("internal/lingdoc/workspacecore/context_ports_test.go", "file"),
            ("docs/08-本轮实施方案/contracts/openapi.json", "file"),
            ("internal/lingdoc/workspace/full_workflow_http.go", "file"),
            ("internal/router/routes_lingdoc.go", "file"),
            # 2026-10-24: routes_lingdoc_owner_test.go moved on to PF-7 (#460)
            # -- #415's read-side role filter needed an assertion there, and
            # the registry's overlap check is path-level.  The handover record
            # is in PF-7's summary.
            ("internal/router/routes_lingdoc_owner_407_test.go", "file"),
            ("internal/router/routes_lingdoc_owner_408_test.go", "file"),
            ("internal/router/routes_lingdoc_owner_410_test.go", "file"),
            ("docs/08-本轮实施方案/Spec-49-owner_unavailable派生状态与Owner-only冻结.md", "file"),
            ("docs/08-本轮实施方案/contracts/validation-result.json", "file"),
            # 2026-10-25 #619 契约侧（PR #646，内容提交 389a8df5 ＋ 0b86cc01）：PF-10 **借用**本面
            #   持有的两条路径 ——
            #   docs/08-本轮实施方案/contracts/openapi.json：给 CheckResult 补 receipt_id（服务端真实
            #     返回，而 CheckResult 是 additionalProperties:false ⇒ 改之前真实响应违反契约）；
            #     给 ObjectVersionRef.kind 枚举补 DeliveryPreflight（服务端真的在发这个 kind ⇒
            #     真实响应违反契约）；给 ImpactBlocker.code 的描述登记新码
            #     impact_object_authorization_invalid。**owner 仍是本面**，不变更归属。
            #   docs/08-本轮实施方案/contracts/validation-result.json：契约静态校验器重新生成的产物。
            #   依据票：#619（PR #646）、#633（PR #643）。
            ("migrations/sqlite/000053_lingdoc_project_archive.up.sql", "file"),
            ("migrations/sqlite/000053_lingdoc_project_archive.down.sql", "file"),
            ("migrations/versioned/000132_lingdoc_project_archive.up.sql", "file"),
            ("migrations/versioned/000132_lingdoc_project_archive.down.sql", "file"),
            ("frontend/src/api/lingdoc/permissions.ts", "file"),
            ("frontend/src/api/lingdoc/collaboration.ts", "file"),
            ("frontend/src/api/lingdoc/projectArchive.ts", "file"),
            ("frontend/src/api/lingdoc/projectArchive.test.ts", "file"),
            ("frontend/src/composables/useOwnerAvailability.ts", "file"),
            ("frontend/src/composables/useOwnerAvailability.test.ts", "file"),
            ("frontend/src/views/lingdoc/OwnerUnavailableNotice.vue", "file"),
            ("frontend/src/views/lingdoc/ProjectMembers.vue", "file"),
            ("frontend/src/views/lingdoc/ProjectArchivePanel.vue", "file"),
            ("frontend/src/views/lingdoc/ProjectArchivePanel.test.ts", "file"),
            ("frontend/src/views/lingdoc/workspaceOwnerFreeze.test.js", "file"),
        ),
        "resolves": ((127, 143),),
        "summary": (
            "2026-10-23 交面：PF-4 依 #441 §1.3 的「显式移交」形态把 1 条路径交给 PF-13 G4 模板目录与版本治理（票 #248）：internal/lingdoc/workspacecore/service_facade.go。这条出自同日 PF-8（#382）落地后还给本面的 4 条；本面保留 typed_contracts.go，因为 #248 不碰它。**移交是永久的**：PF-13 交付完成（PR #248 已于 2026-10-23 合入 main，合并提交 19d9df9c）后该路径归 PF-13，PF-4 不再声称拥有；PF-4 的 done 状态与历史证据不变。"
            "**同一批里的另两条**：internal/lingdoc/workspacecore/context_ports_test.go 与 docs/08-本轮实施方案/contracts/openapi.json —— 管理会话裁定其 owner 归 PF-11（票 #466 / PR #149），而 PF-13（票 #248）**借用**这两条（补上下文端口断言 / 加模板目录相关接口）。**当前登记处的事实**：#149 已于 2026-10-23 合入 main（合并提交 4b716a1f），但 PF-11 是 done 却**尚未登记进登记处**（它自己的交面票还没落）⇒ 为不产生 loader 拒绝的重复路径声明、也不让路径无人拥有，这两条**仍由本面持有**。**归还条件**：PF-11 自己的交面把 PF-11 登记进来时，由本面把这两条移给 PF-11；PF-11 或 PF-13 任一方放弃该路径时，由放弃方显式移回本面，而不是让路径无人拥有。"

            "owner_unavailable 状态串全仓不存在，"
            "「租户身份失效 → 冻结 Owner-only 动作 → 租户恢复后自动解冻」"
            "只能用「再提名一次」代替。这是 66 条原生依赖里唯一一条"
            "「行为未稳定」级的实现依赖（#127 → #143），"
            "修完它就解开 G1 立项的一个验收点。状态串放独立新文件，避开 model.go。"
            "路径集按 2026-10-22 实测修正：Spec-47 把转让的 Service 方法搬进了 "
            "internal/lingdoc/membership/，workspacecore/membership.go 只剩 "
            "TransferOwnerInput；冻结判定要收口的出口是 member_judgement.go 与 "
            "delivery_export_guard.go。本面另按 #127 的冻结清单补上尚不存在的归档能力，"
            "并把五项 Owner-only 动作的可用性判定收口到一处。"
            "两处前端改动是集成步骤而非面拥有："
            "frontend/src/views/lingdoc/Workspace.vue 是所有前端面的集成点（保留路径，不得被任一面独占），"
            "frontend/src/views/lingdoc/ImpactOpsPanel.vue 属 PF-1 已交面的文件。"
            "**路径集按 2026-10-23 实测补齐**（实现合入前，PR 未开时）："
            "owner_transfer_proof.go 已随 #406 删除，判定内核改由 owner_unavailable.go 承担，"
            "原条目换成它；member_lifecycle_state.go 从未存在（路径图曾把它写成「新增」），"
            "一并从登记处删掉 —— 登记处只登记真实存在的路径。"
            "补进来的还有：判定内核周边（change_draft_edit.go、chapter_review.go、"
            "context_revision.go、member_records.go）、归档能力（project_archive.go、"
            "human_decision.go、migrated_context_ports.go、context_ports_test.go）、"
            "错误层的可选 details（internal/clienterror/clienterror.go）、"
            "权限读出口与接口（membership/ports.go、ports_test.go、workspace/full_workflow_http.go）、"
            "四张票各自的 HTTP 断言文件、归档迁移（sqlite/000053 与 versioned/000132）、"
            "契约产物与实施档、以及前端的项目级出口与归档面板。"
            "**本面确实改了下列跨面共享文件，按「共享维护点」约定不登记进任何面的路径集**："
            "internal/database/migration_sqlite_versioned_schema_test.go 与 "
            "internal/router/lingdoc_integration_test.go（迁移子集清单，PF-3 已定此约定）、"
            "frontend/src/views/lingdoc/firstPassIdentity.test.js、"
            "frontend/src/views/lingdoc/r26ComponentRuntimeFixture.js 与 "
            "frontend/src/views/lingdoc/workspaceHistory.test.js（模块桩清单，"
            "每个引入新 composable 的面都要在这里加一行）、"
            "scripts/lingdoc_naming_registry.py（命名登记表，全仓共用）。"
            "**本面确实改了下列已被别的面占有的文件**：internal/lingdoc/workspace/http.go 与 "
            "internal/router/lingdoc_routes_test.go 与 frontend/src/api/lingdoc/workspace.ts"
            "属 PF-2 已占有（PF-2 已交面 done，done 不释放路径）；"
            "frontend/src/views/lingdoc/ImpactOpsPanel.vue 与 ImpactOpsPanel.test.js 属 PF-1 已交面。"
            "internal/container/lingdoc_test.go 原属 PF-2，它钉着生产组合的路由总数，"
            "本面新增归档端点后它必须跟着改 —— PF-2 已交面不再施工，"
            "该路径于 2026-10-23 移交给本面（PF-2 路径集同步移除，两边 summary 都记了这次移交）。"
            "这些改动与基线合并组合在同一棵树上，后续冲突会显式报出，不会静默覆盖。"
            "**本面还改了一个 owner 冻结夹具**：internal/router/routes_lingdoc_owner_test.go 的"
            "迁移清单补上 PF-3 的 000052 审计版本列迁移 —— 清单停在 000041 时，"
            "夹具建不出 target_version / project_revision，任何带审计的写入都以 500 结束，"
            "被测的冻结拒绝根本没机会发生。"
            "**本面已完成**（2026-10-23，认领与完成的人是 magician336）："
            "PR #419 已合入 main（合并提交 2b1b098c），票 #406–#410 与 Spec 票 #391 随之关闭。"
            "交付三件事：① Owner 可用性收口到一处（internal/lingdoc/workspacecore/owner_unavailable.go，"
            "判定与门面同处，并有 AST 守护测试拒绝第二份内联判定）；"
            "② 派生的 owner_unavailable 读时算出、不落库，五项 Owner 专属动作"
            "（立项确认、模板升级、ChangeSet 应用、正式导出、归档）统一走这道闸门，"
            "拒绝可归因（既有 404 与 not_found 不变，原因只进可选 details.reason_code）；"
            "③ 归档能力落地（#127 冻结清单里原本不存在的一项，sqlite/000053 与 versioned/000132）。"
            "验收 AC-49-01 至 AC-49-10 全部 passed，证据见 T49-验证报告.json。"
            "**本面经过一次双轴独立评审**（Standards 轴 + Spec 轴），"
            "评审发现并修掉的问题：权限读出口曾对任何被拒能力都报 owner_unavailable"
            "（把「你无权」误标成「Owner 失效」，吞掉既有 capability_or_scope_denied 语义）、"
            "判定与门面分处两个文件、缺「散落写法被守护检查拒绝」这一条验收的守护、"
            "新领域词没进 GLOSSARY.md、登记处登记了一条从未存在的路径。"
            "**PR 的第一轮 CI 是红的**：internal/container 的路由表面快照被新增的归档端点打破，"
            "本地没抓到是因为验证范围只跑了四个相关包 —— 教训与修法记在实施档的「已踩坑清单」。"
            "**残留票保持开放**：#420（权限读出口按能力聚合）、"
            "#421（写侧冻结归因只在部分路径可得）。"
            "**Spec #127 保持开放**：本面交付了它「Owner 租户身份失效 ⇒ 冻结」这一支"
            "（实现决策 2/3/8 与对应的测试决策），转让流程、受让人角色清空、前任降级与 Beta 旁路"
            "由既有实现承担，不在本面范围；#143 同样保持开放（本面只交付了立项确认的冻结部分）。"
            "**本面不释放路径**：done 状态仍占用这 40 条路径，后续面不要重复声明。"
            "**2026-10-23 经维护者裁决，PF-8 先走，本面让出四个路径** ——"
            "internal/lingdoc/workspacecore/service_facade.go、"
            "internal/lingdoc/workspacecore/typed_contracts.go、"
            "internal/lingdoc/workspacecore/context_ports_test.go 与 "
            "docs/08-本轮实施方案/contracts/openapi.json。"
            "PF-8 要在这四处落 #382/#383 的接线（授权拒绝观测的收敛点、"
            "运行控制回退动作、窄口方法集登记与契约产物），"
            "而登记处的重叠检查是路径级的：两边同时声明就直接拒绝加载。"
            "本面在 main 上已交付的 owner_unavailable 能力不受这次让出影响；"
            "**2026-10-23 PF-8 随 PR #446 合入 main 后，这四个路径已归还本面**"
            "（本面路径集 44 → 47 条）。"
            "**2026-10-23 永久移交三条路径给 PF-9**（workspacecore/owner_unavailable.go、"
            "membership/policy.go、membership/results.go）——依据票 #420（Owner 专属入口能力 → "
            "读出口能力的对应关系）与 #421（写侧冻结归因）；PF-9 = #461。移交是单向的，本面不再声称拥有"
            "这三条 ⇒ 路径集 47 → 44 条。"
            "**2026-10-23 PF-9 开工时再移交两条**：internal/router/routes_lingdoc_owner_read_outlet_test.go 与 "
            "internal/router/routes_lingdoc_owner_409_test.go —— PF-9 指派里那三条 "
            "internal/lingdoc/router/… 写错了前缀（该目录不存在），真实路径在本面手里，"
            "而 PF-9 要在这两张断言文件上加断言 ⇒ 显式单向移交 ⇒ 路径集 44 → 42 条。"
            "internal/router/routes_lingdoc.go **留在本面**（PF-9 不改它）。"
            "**2026-10-23 另有一条路径一次性移交给 PF-8**："
            "internal/container/lingdoc_test.go —— PF-8 在 PR #446 里给它加了一条注释"
            "（说明两个新 G6 端点不改变这张路由数量快照），"
            "而登记处的重叠检查是路径级的：改了别面的路径就必须显式移交，"
            "移交是单向的，本面不再声称拥有它。"
            "**将来本面若要动那四个路径或这一条，需人工解一次冲突**"
            "（PF-4 与 PF-8 都在授权判定附近改过代码，"
            "冲突会显式报出来，不会静默覆盖）。"
            "**2026-10-24 交面时再永久移交一条给 PF-7**"
            "（internal/router/routes_lingdoc_owner_test.go）——依据票 #415"
            "（AuditEvent 声明字段与落库列一致、读侧角色过滤生效）："
            "PR #503 在这张 Owner 路由断言文件里补了读侧角色过滤的断言，"
            "而登记处的重叠检查是路径级的（不看 status、不看改动行），两边同时声明就直接拒绝加载 "
            "⇒ 本面让出。本面已交面且不再需要它 ⇒ **不归还** ⇒ 本面路径集 41 → 40 条。"
            "本面在 main 上已交付的 owner_unavailable 能力不受这次移交影响。"
            "**2026-10-24 #608 跨面借用（owner 不变，故不声明进本面 paths；重复声明会被登记处 loader 直接拒绝）**：#608（F01-11 采纳节点按服务端 HTTP 契约补齐确认载荷与工作副本声明，PR #612 已合入 main） 改动了下列**别的面拥有**的文件，本面**借用**它们、owner 不变：`docs/08-本轮实施方案/contracts/openapi.json`（docs/08-本轮实施方案/contracts/openapi.json）。 **2026-10-24 #576 跨面借用一条（owner 仍是本面，不变更归属）**：#576（PR #589，合并提交 a9374cab）把前端租户角色的中文说法收口为唯一来源；其中 frontend/src/views/lingdoc/ProjectMembers.vue（本面拥有）只被加了一处**维度标注**——说明该表是「项目治理角色」、不是租户角色词表。**行为未变、判定未变**，owner 仍在本面；PF-14 侧已把这条记为借用事实，两侧一致。"
        ),
    },
    {
        "name": "PF-5 Agent research purpose",
        "issue": 329,
        "issues": (135, 125, 393),
        "status": "done",
        "claimed_by": "",
        "paths": (
            ("internal/lingdoc/workspacecore/agent_action_service.go", "file"),
            # 2026-10-25 #619 服务端（PR #645，内容提交 295bc5f2）：PF-10 **借用**本面持有的这条路径 ——
            #   该 PR 改了 1 行：ApplyProjectAssetPolicyDelta 的 Version/AssetRevision 改为使用
            #   新增的共享常量 assetPolicyVersionAdvance（＝1），好让 apply 的推进量与 A1 的判定式
            #   **共用同一个常量**（两处不可能各自漂移）。**owner 仍是本面**，不变更归属；
            #   本面不把它借给别的面。
            #   依据票：#619（PR #645）、#634（A 的决策记录）。
            ("internal/lingdoc/workspacecore/agent_research_model.go", "file"),
            # The 研究提案 contract: the model-output contract that owns the
            # shape of a research proposal.  It is a module of its own,
            # alongside the two model-output contracts the agent surface
            # already has, because the large service module already holds
            # seven concerns and must not receive an eighth.
            ("internal/lingdoc/workspacecore/research_proposal.go", "file"),
            ("internal/lingdoc/workspacecore/research_proposal_test.go", "file"),
            # The proposal is delivered on the existing turn endpoint and
            # consumed by the 调研面板 that already adopts an editable
            # 调研任务建议; these files are claimed so the panel may render
            # the proposal's 候选来源, 理由 and 缺口 beside that draft.
            ("frontend/src/api/lingdoc/agentApi.ts", "file"),
            ("frontend/src/api/lingdoc/agentApi.test.js", "file"),
            # 类型层用例：助手那张更松的卡片必须能被 task_card 的类型接受。
            # 它是 .ts，所以 vue-tsc 会真的检查它（.js 用例不在类型检查里）。
            ("frontend/src/api/lingdoc/agentApi.types.test.ts", "file"),
            ("frontend/src/views/lingdoc/ResearchPanel.vue", "file"),
            ("frontend/src/views/lingdoc/ResearchPanel.test.js", "file"),
            # #404 的词汇归一（agent / purpose 常量）顺手改到了这两个文件：
            # 它们把裸字符串换成服务端常量。它们不被任何面拥有，所以没有
            # 所有权冲突；登记进来是为了让路径集与实际改动一致。
            ("internal/lingdoc/workspacecore/change_draft_action.go", "file"),
        ),
        "resolves": (),
        "summary": (
            "2026-10-23 交面：PF-5 把 1 条路径**单向移交**给 PF-13 G4 模板目录与版本治理（票 #248）：internal/lingdoc/workspace/template_governance_runtime.go（规则治理运行时，#248 要加 TenantPermissionAuthorizer 窄口与复制审计接线）。**移交是单向的，本面不再声称拥有**；**归还条件**：若 PF-13 放弃该路径，由 PF-13 显式移回本面，而不是让路径无人拥有。PF-5 的 done 状态与历史证据不变。"

            "「research purpose」在词汇上不存在：purpose 只有 conversation 与 rule_suggestion 两种取值，"
            "change_draft 是模型输出的键而不是 purpose，"
            "所以本面缺的是「一个可被服务端校验的研究提案契约」，"
            "不是执行侧——ResearchTask / ResearchRun / ResearchResult / SearchResearch / 导入候选都已齐备。"
            "现状是 HostAgentModel.Reply 按 request.Agent == \"research_agent\" 换一段提示词，"
            "但模型交回来的 task_card 没有任何解码器校验，"
            "「推荐查询词与来源并解释理由与缺口」因此没有形状、也没有边界。"
            "2026-10-22 实测更正了两条过时断言："
            "上下文包后端已不在 g2_model.go（该文件不存在），"
            "它现在住在 workspace/agent_model.go 与 workspacecore 的 context_ports.go、agent_research_model.go；"
            "不必等 #124 的前端消费（PF-6）。"
            "2026-10-22 由 magician336 认领。"
            "同日补交一张 G 线 Spec #393（调研智能体的研究提案，文档 "
            "docs/06-灵档产品开发规划/50-Spec-调研智能体研究提案.md）："
            "研究提案的形状由一份服务端契约拥有并校验，"
            "候选来源必须可追溯到该轮的授权上下文，"
            "提案永远是候选（不自动成为调研任务、导入候选、证据卡或研究主张）。"
            "路径集同日从 3 条扩到 9 条（新增研究提案契约模块与前端四处），"
            "因为该 Spec 的答案要真正被看到。"
            "2026-10-23 再扩到 11 条：#404 的词汇归一把两个文件里的裸字符串"
            "换成了服务端常量——internal/lingdoc/workspacecore/change_draft_action.go"
            "（助手草稿校验的两处 agent 比较）与 internal/lingdoc/workspace/template_governance_runtime.go"
            "（rule_suggestion 用途的两处 agent / purpose 比较）。这两个文件原先"
            "不被任何面拥有，所以没有所有权冲突；登记进来是为了让路径集与实际"
            "改动一致，路径图那一份副本同步改成 11 条。"
            "2026-10-23 复审后再扩到 12 条：新增 frontend/src/api/lingdoc/agentApi.types.test.ts"
            "（前端类型层用例，防止 task_card 被类型成一份更严的形状、从而对助手轮次谎报）；"
            "路径图那一份副本同步改成 12 条。"
            "2026-10-23 完成本面：四张纵切票 #401（一条最小研究提案纵切）、"
            "#402（候选来源必须可追溯）、#403（理由与缺口）、#404（三态收口与用途派生）"
            "随 PR #417 合入 main（合并提交 1b784a4f），Spec 票 #393 与四张施工票随之关闭，"
            "面票 #329 也随交面 PR 关闭。"
            "交付内容：研究提案的形状由一份服务端契约独占（提示词从它渲染，"
            "助手那张同名卡片按另一张卡渲染）、候选来源必须对得上该轮授权上下文"
            "（对不上即拒、不降级）、理由与缺口由结构强制、坏提案响亮失败但不连累那一轮的问答复、"
            "用途由服务端按会话 agent 派生（客户端不新增请求字段，幂等摘要钉死不变）。"
            "两轴 code-review（Spec 轴 / Standards 轴）都判 FAIL，12 条发现与 3 处记账全部修完，"
            "按最终代码复审判 PASS（12 条中 11 已解决、1 条部分、3 处记账全解决、无新增 P0/P1），"
            "复审后小修再修 5 条。"
            "验收矩阵 18 行全 PASS，引用的 26 个 Go 用例名与 11 条前端用例标题逐条复核零缺失。"
            "**本面不释放路径**：done 状态仍占用这 12 条路径，后续面不要重复声明。"
            "保持开放的：Spec #135 与 #125——本面只交付 research 能力里的"
            "「研究提案契约」这一部分，未关闭；#124（上下文包前端消费）归 PF-6，"
            "其面票 #330 仍未认领；#385 / #386 是 PF-2 的残留票，与本面无关。"
        ),
    },
    {
        "name": "PF-6 上下文包前端消费",
        "issue": 330,
        "issues": (124, 431),
        "status": "done",
        "claimed_by": "",
        "paths": (
            ("frontend/src/api/lingdoc/agentContext.ts", "file"),
            ("frontend/src/api/lingdoc/agentContext.test.ts", "file"),
            ("frontend/src/views/lingdoc/ContextPackagePanel.vue", "file"),
            ("frontend/src/views/lingdoc/ContextPackagePanel.test.ts", "file"),
            ("docs/08-本轮实施方案/PF-6-上下文包前端消费.md", "file"),
            ("docs/08-本轮实施方案/T431-验证报告.json", "file"),
        ),
        "resolves": (),
        "summary": (
            "上下文包、Session、Turn 的后端契约已落地"
            "（AgentContextPackage 在 workspacecore/agent_research_model.go，"
            "预览与轮次端点在 workspace/agent_routes.go）。"
            "**2026-10-23 实测更正了票面「前端零消费」这句过时断言**："
            "研究面板（PF-5 已交付的文件）里已有一份上下文表单与一处预览，"
            "显示来源定位、资料版本、模型使用许可与输出约束——"
            "**卡片、选择理由、卡片状态三样都没有**；"
            "增删只发生在表单多选框上，界面从不复述服务端的组装结果。"
            "本面只新增文件；与 PF-1 同属前端目录，两者不得互相改对方的文件。"
            "**2026-10-23 由 magician336 认领**，同日补交一张 G 线 Spec #431"
            "（上下文包的前端消费，文档 "
            "docs/06-灵档产品开发规划/51-Spec-上下文包前端消费.md）："
            "上下文包的契约类型由已有的传输层模块拥有，本面只读不改、不复制第二份；"
            "意图（用户勾了什么）与事实（服务端组装了什么）是两个不可互换的类型，"
            "事实区只由预览结果刷新，增删之后必须重新预览；"
            "卡片状态按 id 与 version 双重对齐，对不上显示「状态未知」而不是回退；"
            "选择理由按服务端原文逐条展示，前端不把它解析成条目映射。"
            "**2026-10-23 施工前实测修正了路径名**："
            "最初登记的两个实现文件里，API 模块名以交付批次代号开头"
            "（原名 g2Context.ts），而命名登记表把 ^[gGtT]\\d 判为编号命名——"
            "实测新增该文件会让守护测试报「breaks numbered but is not declared "
            "in the registry」并以退出码 1 失败，"
            "而改名后的 agentContext.ts 不报；"
            "PF-1 当年也把登记的 g6Runtime.ts 改成了 runtimeMetrics.ts，是同一类修正。"
            "两个测试文件同时从 .test.js 改成 .test.ts"
            "（仓内前端测试的主流形态，且已有组件用 .test.ts 的先例）。"
            "路径集 4 -> 6 条：另两条是本面的开发计划与验证报告。"
            "**本面还改了一条不在任何路径集里的路径**："
            "docs/08-本轮实施方案/下一批施工面切分方案.md（M-1 清单里那条"
            "「#434 票面写的 g2Context.ts 是旧名」——那份方案档明确把这条指给"
            "在飞的 PF-6，本面已把 #434 票面更正完：票面顶部补更正指针、"
            "正文那一处路径同步改成 agentContext.ts）；该文件属方案档性质，"
            "本面不把它登记进路径集，只在这里记录改动事实。"
            "**本面还改了 PF-8 声明在路径集里的路径图**"
            "（docs/08-本轮实施方案/G1-G7-Spec-完成路径图.md）——路径图是多个面"
            "共同维护的说明副本，PF-8 已声明它且已交面；本面在其上维护 PF-6 的"
            "说明段（合并上游时解了三轮文本冲突，按「以 main 为骨架、把本面事实"
            "叠加上去」处理）。本面**不**把它登记进路径集：登记处的重叠检查是"
            "路径级的，而 PF-8 已交面且「done 状态仍占用路径」，重复声明会被"
            "loader 直接拒绝——所以这里只记录改动事实，不动 PF-8 的 claim。"
            "**集成归属另落一张票**：面板的挂载与浏览器端到端验收归 #476"
            "（原记在 #384 票面，但该票已随 PF-8 交面关闭，它交付的是 G6 "
            "运维面板的集成、不含本面）。"
            "**2026-10-23 交面**：本面随 PR #443 合入 main（合并提交 "
            "52f5b615，合并时间 05:18:33Z），登记处 status 改 done、"
            "claimed_by 清空（完成态不保留认领人，认领与完成的人记在 summary 里）。"
            "交付四件事：① 上下文包门面上线（agentContext.ts）——契约类型由"
            "传输层独家拥有、本面只读不改不复制；意图（用户勾了什么）与事实"
            "（服务端组装了什么）是两个不可互换的类型；卡片状态按 id 与 version "
            "双重对齐，对不上显示「状态未知」而不是回退；选择理由按服务端原文"
            "逐条透传，不解析成条目映射。② 上下文包面板上线"
            "（ContextPackagePanel.vue）——事实区只由预览结果刷新，增删之后必须"
            "重新预览，预览失败或版本冲突即清空事实区，面板不自行判断"
            "「要不要确认」；身份（agent / sessionKey）一变即按硬时序清空意图与事实。"
            "③ ADR 0013 记下三条不可变契约，权威术语表 §5 补「上下文包」词条。"
            "④ 修登记处基线比对的一处误报：比较前剔除基线里「文件在基线提交中"
            "根本不存在」的 file claim——这类登记从未对应真实文件，撤下它不损失"
            "所有权；豁免显式传入，目录 claim 不豁免，基线里真实存在的文件被撤下"
            "仍照报。"
            "验收矩阵 36 行：34 PASS / 1 N/A（不接触模型调用）/ 1 NOT RUN"
            "（浏览器端到端——面板未挂载，集成归 #476）；两轴代码评审判 FAIL 的"
            "六条已全部修完并各自有阴性验证；合并前健康度复评 H_L 70.8 / F_R 75.0"
            "（置信度 B，无维度阻断项，最低 2/5 的两项同指「同一件事有两个拥有者」）。"
            "**残留票保持开放**：#432（选择理由没有结构化映射）、#433（低风险"
            "状态卡免额外确认在服务端不成立）、#434（研究面板里有一份重复的上下文"
            "构造）、#435（前端契约类型落后服务端两个字段）；"
            "**集成票 #476 保持开放**（面板挂进 Workspace + 浏览器端到端）；"
            "**Spec #124 保持开放**（本面只交付它的前端验收点）；"
            "本面新交付的 Spec #431 随之关闭。"
        ),
    },    {
        "name": "PF-10 Beta 首版验收证据生产",
        "issue": 465,
        "issues": (451, 452, 453),
        "status": "done",
        "claimed_by": "",
        "paths": (
            # ---- #453（55-Spec 模型调用账本与模型切换记账）----
            # 账本行结构、写入与读取；kind = call / switch 两行种。
            ("internal/lingdoc/workspacecore/model_call_ledger.go", "file"),
            ("internal/lingdoc/workspacecore/model_call_ledger_test.go", "file"),
            # 模型调用的唯一出口：账本在这里记一行
            # （2026-10-23 从 PF-5 移交；PF-5 已 done 且不释放路径）。
            # 组合根 internal/container/lingdoc.go **不**登记进本面：owner 仍是 PF-7
            # （#414 的 AuditSink 实现就在那里，PF-7 未开工且已声明它）。
            # 但本面**借用**它——在 NewHostAgentModel 那一行装上模型调用账本
            # （不装，账本在生产里就是死的）。**该路径已于 2026-10-23 随 #509（账本写入端）合入 main，路径归还 PF-7；PF-10 不再改它**
            # （先例：PF-8 借 PF-4 的四个路径，落地后归还）。
            # **PF-7 侧的对应记录已在 main 上**（PR #475 / commit 60ae2dca 的 PF-7 块 summary）。
            # 本面在这里装账本：workspace.NewHostAgentModel(models,
            # workspace.WithModelCallLedger(workspacecore.NewGORMModelCallLedger(db)))
            ("internal/lingdoc/workspace/agent_model.go", "file"),
            # 账本表的两方言迁移（sqlite / versioned 成对，
            # 成对性由 scripts/check_migration_pairs.sh 守）。
            ("migrations/sqlite/000055_lingdoc_model_call_ledger.up.sql", "file"),
            ("migrations/sqlite/000055_lingdoc_model_call_ledger.down.sql", "file"),
            ("migrations/versioned/000134_lingdoc_model_call_ledger.up.sql", "file"),
            ("migrations/versioned/000134_lingdoc_model_call_ledger.down.sql", "file"),
            # 迁移 schema 守护：sqlite / versioned 两套 schema 与版本常量同步
            # （main 上已存在、本仓无面声明它——自由路径；本面改动它是为了让
            #  000055 / 000134 两方言迁移的 schema 断言跟着账本表走）。
            ("internal/database/migration_sqlite_versioned_schema_test.go", "file"),
            # ---- #452（54-Spec 真实浏览器 × 真实后端对偶执行器）----
            # 编排器：起环境 → 跑 HTTP 侧 → 跑浏览器侧 → 比对 → 产证据。
            ("scripts/acceptance/beta_evidence_parity.py", "file"),
            # HTTP 侧执行器：产出一份可与浏览器侧比对的快照。
            ("scripts/acceptance/beta_http_readback.py", "file"),
            # 浏览器侧执行器：CDP 驱动真实前端对真实后端
            # （连接手法照 scripts/lingdoc_mock/browser_smoke.mjs）。
            ("scripts/acceptance/beta_browser_cdp.mjs", "file"),
            # 守护：六件产物逐一脱敏检查 + 证据目录结构检查。
            ("scripts/ci/test_lingdoc_beta_evidence.py", "file"),
            # ---- #451（53-Spec Beta 首版验收执行器与证据目录）----
            # 证据目录生成器：产出协议 §6 的六件产物（落点以协议为准）。
            ("scripts/acceptance/beta_evidence.py", "file"),
            # F01 执行器：只扩算子（新增的 check kind），不改文件名
            # （文件名被 CI 触发路径、py_compile 清单、
            # T15-执行器场景驱动.md:59,63 三处写死）。
            ("scripts/lingdoc_mock/run_f01.py", "file"),
            # 重启脚本：参数化隔离 PostgreSQL 与 SQLite（协议 §1 要求隔离 PG）。
            ("scripts/acceptance/lingdoc_full_server_restart.py", "file"),
            # F01 契约：协议 §3 的语义读回落成这里的 check
            # （steps 31 个、checks 64 条；一条 check 的断言键就是它的 kind：
            #  equals / ref_in / readback_equals / non_empty / length /
            #  version_bound / marked_or_absent / hash_equals）。
            #  31 个 ＝ 原 23 个业务步骤 ＋ activation-read 前置 ＋ #545 的四个
            #  model-use 许可节点（F01-06b～06e：为建立同意记录而存在，不是业务步骤）
            #  ＋ #619 的三个稳定化节点（F01-19a～19c：交付检查回执 → 闭环影响任务 → 稳定化）。
            ("docs/08-本轮实施方案/contracts/workflow.json", "file"),
            # 协议 §6 的证据产物目录：<SHA>/<运行标识>/ 下的六件产物。
            # 用**目录级**声明覆盖整棵子树——每次验收新增一个运行目录，
            # 逐文件登记会漏（本次 PR 产出了 12 个文件、2 个运行目录）。
            ("docs/08-本轮实施方案/acceptance/beta-first/", "dir"),
            ("scripts/lingdoc_mock/workflow_expand.py", "file"),
            ("scripts/ci/test_lingdoc_review_decision_expansion.py", "file"),
            # 2026-10-25 #633（PR #643，内容提交 e01487b5）：本面认领该 PR 新增的三条路径 ——
            #   scripts/lingdoc_mock/manual_decision_audit.py  （人工判定台账与权威性三档）
            #   scripts/lingdoc_mock/test_workflow_manual_decisions.py（42 条验收/负向/端到端用例）
            #   scripts/ci/test_lingdoc_manual_decision_audit.py  （跨路径守护，由 LingDoc PR gate 执行）
            ("scripts/lingdoc_mock/manual_decision_audit.py", "file"),
            ("scripts/lingdoc_mock/test_workflow_manual_decisions.py", "file"),
            ("scripts/ci/test_lingdoc_manual_decision_audit.py", "file"),
            # 2026-10-24 #618（PR #620，合并提交 f404fd02）：本面认领本 PR 新增的两条路径 ——
            #   scripts/lingdoc_mock/workflow_expand.py            （展开规则的唯一实现）
            #   scripts/ci/test_lingdoc_review_decision_expansion.py（守护测试，由 LingDoc PR gate 执行）。
            #   同 PR 还改了 PF-14 持有的 docs/08-本轮实施方案/contracts/validate_artifacts.py
            #   （让契约静态校验器与 F01 跑批执行器共用同一份展开实现）—— 那是**跨面借用**，
            #   owner 仍是 PF-14，本面不声明该路径（登记处 loader 拒绝两个面声明同一条路径）。
            #   依据票：#618（PR #620）、#490（放行结论表）。
            # 2026-10-24 #626（PR #637，内容提交 ab9fd141，合并提交 6b394ca7）：本面**再次借用**
            #   PF-14 持有的 docs/08-本轮实施方案/contracts/validate_artifacts.py —— 该 PR 在校验器里
            #   新增 is_marked()（逐条复刻 runner 的 _is_marked）并把 one_to_one 的 entry_non_empty
            #   改用同一口径（纯空白字符串在两边都算空）。**owner 仍是 PF-14**，本面不声明该路径
            #   （登记处 loader 拒绝两个面声明同一条路径）。
            #   同 PR 新增的三条路径 —— scripts/lingdoc_mock/test_check_operators.py、
            #   scripts/ci/test_lingdoc_check_operator_discovery.py、scripts/ci/test_lingdoc_nonempty_parity.py
            #   —— 经登记处自身的 PathClaim.overlaps 逐条核对为**自由路径**（本仓无面声明），
            #   按本轮约定不声明进任何面的路径集；此处只记录改动事实，不改本面既有路径集。
            #   依据票：#626（PR #637）、#621（引入 one_to_one 的那轮）、#490（放行结论表）。
            # 2026-10-25 #633（PR #643，内容提交 e01487b5）：本面**认领**该 PR 新增的三条路径 ——
            #   scripts/lingdoc_mock/manual_decision_audit.py  （人工判定台账与权威性三档）
            #   scripts/lingdoc_mock/test_workflow_manual_decisions.py（42 条验收/负向/端到端用例）
            #   scripts/ci/test_lingdoc_manual_decision_audit.py  （跨路径守护，由 LingDoc PR gate 执行）
            #   同 PR 还**借用** PF-14 持有的两条路径（owner 不变，故不声明进本面 paths；登记处 loader
            #   拒绝两个面声明同一条路径）：
            #   scripts/lingdoc_mock/test_workflow_runner.py、scripts/lingdoc_mock/test_workflow_reports.py
            #   —— 理由是「跑批器新增了 run_id / manual_decisions 两个报告键，那两处的精确 key 集合断言
            #   必须同步扩充」。改动只是**扩充**断言（未删未弱化）。
            #   依据票：#633（PR #643）、#490（放行结论表）。
            # 2026-10-25 #639（PR #644，内容提交 e0d1df5f）：本面**再次借用** PF-14 持有的
            #   docs/08-本轮实施方案/contracts/validate_artifacts.py —— 该 PR 把校验器里
            #   marked_or_absent / non_empty / all_non_empty 三处判断点改调 is_marked()，
            #   使校验器与跑批器对「什么算空」同一口径。**owner 仍是 PF-14**，本面不声明该路径。
            #   依据票：#639（PR #644）、#621（引入 one_to_one 的那轮）。
            # 2026-10-25 #619 契约侧（PR #646，内容提交 389a8df5 ＋ 0b86cc01）：本面认领
            #   docs/08-本轮实施方案/contracts/workflow.json（**本面自有路径**），并**借用**下列
            #   **别的面拥有**的路径（owner 全部不变，本面一律不声明）：
            #   docs/08-本轮实施方案/contracts/scenarios.json（PF-14）
            #   docs/08-本轮实施方案/contracts/openapi.json（PF-4）
            #   docs/08-本轮实施方案/contracts/validation-result.json（PF-4）
            #   scripts/lingdoc_mock/test_workflow_specs.py（PF-14）
            #   scripts/lingdoc_mock/test_workflow_twostep.py（PF-14）
            #   scripts/lingdoc_mock/test_workflow_runner.py（PF-14）
            #   docs/06-灵档产品开发规划/57-Spec-Beta首版放行验收.md（PF-14）
            #   —— 理由分别是：补稳定化链节点与两条场景；给 CheckResult 补 receipt_id、
            #   给 ObjectVersionRef.kind 补 DeliveryPreflight（服务端真在发 ⇒ 真实响应违反契约）、
            #   登记新阻断码 impact_object_authorization_invalid；节点数 28 → 31 的连锁断言；
            #   协议与放行 Spec 里的节点数与构成同步。
            #   依据票：#619（PR #646）、#633（PR #643）、#490（放行结论表）。
        ),
        "resolves": (),
        "summary": (
            "**2026-10-23 管理会话合并前补申报（15 → 17 条）**：本 PR 改了三个未申报的路径，现已补进路径集——① `internal/database/migration_sqlite_versioned_schema_test.go`（file）：main 上已存在、本仓无面声明它（自由路径），本面改动它是为了让 000055 / 000134 两方言迁移的 schema 断言跟着账本表走；② `docs/08-本轮实施方案/acceptance/beta-first/`（**dir**）：协议 §6 的证据产物目录，用目录级声明覆盖 `<SHA>/<运行标识>/` 下的六件产物（本次 PR 产出 12 个文件、2 个运行目录，逐文件登记必然漏）。另有一处**刻意不登记**：`docs/08-本轮实施方案/下一批施工面切分方案.md`（方案档性质，只记录改动事实——先例见 PF-6 交面）。"
            "Beta 首版放行路径上的第一张面：把协议 §2-§6 的人肉验收变成一条可复跑命令，"
            "产出证据目录与模型调用账本。交付 #451 + #452 + #453 三张新 G 线 Spec。"
            "**为什么三张合成一个面**（票面 #465 有全文）：#451 单独做是浅模块"
            "（删掉它复杂度既不消失也不散回调用方）；「一次真实模型调用的形状」在 #452 与 #453 "
            "各存一份是信息泄漏。合起来 interface = 「跑一次真实验收，产出证据目录与账本」。"
            "**内部次序**：#453（账本）→ #452（对偶执行器）→ #451（证据目录）。"
            "2026-10-23 由 magician336 认领。"
            "**路径移交（本面从 done 面接一条）**："
            "internal/lingdoc/workspace/agent_model.go（自 PF-5，模型调用的唯一出口）。"
            "**三处刻意不做（记为已知残留，不越界处理）**："
            "(1) 55-Spec 决策 5 的 run_mode / runtime_mode 词表收口**整体未做**——"
            "定义点 internal/lingdoc/candidateadoption/candidate_shape.go 在 PF-2 的"
            "**目录级**声明 internal/lingdoc/candidateadoption/ 内，产出点"
            "internal/lingdoc/workspace/model_run_mode.go 属 PF-2，"
            "另一侧 internal/lingdoc/workspacecore/runtime_control.go 由已交面的 PF-8 持有（PR #446）；"
            "⇒ 本面**复用**现有 candidateadoption 的 run_mode 取值，不新增第三套词表，"
            "词表收口待这些路径可改时再做。"
            "(2) internal/lingdoc/workspacecore/agent_research_model.go（PF-5 持有）不在本面路径集内"
            "——账本行靠外键关联产品动作，不需要改 AgentTurn。"
            "(3) internal/lingdoc/generation/store.go 不在本面路径集内——PF-2 持有"
            "internal/lingdoc/generation/ 这个**目录**；账本的来源引用改由调用方填字符串 id。"
            "**迁移号**：**sqlite 000055 / versioned 000134**——000054 / 000133 归 PF-7 的审计字段迁移，"
            "而那两条路径**已随 PR #472 记录在 main 上** ⇒ 按「已登记的面不再让号」的约定，"
            "让号方向由管理会话在 PR #475（commit 60ae2dca）修正为「**PF-10 改号**」。"
            "迁移守护的版本常量同步到 55。"
            "**判据的精确性（2026-10-23 实测，两条机器结论 + 一条运行期结论）**："
            "① **登记处的路径重叠检查拦不住编号争抢**——它是精确路径匹配，"
            "两个文件名不同（000054_lingdoc_audit_decision_fields 与 "
            "000054_lingdoc_model_call_ledger）就不算重叠"
            "（内存级实测：把本面改回 000054/000133 后 load_and_validate() 仍 PASS）；"
            "② **棘轮在 main 的新判据下也不拦**——基线比对现在剔除「悬空登记」"
            "（基线里只有声明、没有实体文件的路径，撤回它不算路径损失），"
            "PF-7 的 000054/000133 正属此类；"
            "③ **真正的硬约束在迁移器运行期**：golang-migrate v4.19.1 的 source/iofs 对"
            "**同版本号两个迁移文件**直接返回 ErrDuplicateMigration ⇒ 两方言各出现两个 000054 时"
            "后端起不来（不是静默降级）。"
            "⇒ **撞号必须在 PR 阶段靠人分开，机器不兜底**"
            "（本文件开头的注释也写明了这一点：编号争抢是「序列的竞争」而不是「路径的竞争」）。"
            "**2026-10-23 已完成**（认领与完成的人是 magician336）：把 Beta 首版真实验收从人肉变成一条可复跑命令。"
            "交付三件事：① #453 模型调用账本——一张 lingdoc_model_call_ledger 表，kind 分 call / switch 两行种，"
            "两方言迁移（sqlite/000055、versioned/000134）+ Go 行结构 + 10 个用例，账本装进组合根；"
            "② #452 浏览器 × 后端对偶执行器——编排器 + HTTP 侧 + CDP 浏览器侧三个独立 CLI + 32 条守护测试，"
            "真实后端 + 真实 Vite + 真实 Edge 无头跑通，10 项在「浏览器 == HTTP == 刷新后读回」三个快照上逐项相等，"
            "注入不一致能红；③ #451 验收执行器与证据目录——run_f01.py 扩六个领域断言 check kind"
            "（ref_in / marked_or_absent / version_bound / readback_equals / hash_equals / non_empty）、六件产物一次性产出、"
            "脱敏守护、协议 §5 三态一字不改。"
            "**一处跨面借用**：组合根 internal/container/lingdoc.go 的 owner 仍是 PF-7（未开工、已声明），"
            "本面于 2026-10-23 **借用**它（装模型调用账本，一处），**本面合入 main 后该路径归还 PF-7**"
            "（先例：PF-8 借 PF-4 的四个路径，落地后归还）；**PF-7 侧的对应记录已在 main 上**"
            "（PR #475 / commit 60ae2dca 的 PF-7 块 summary）。"
            "55-Spec 决策 5 的词表收口整体未做（candidate_shape.go 在 PF-2 的目录级声明内、"
            "runtime_control.go 属在飞的 PF-8）⇒ 本面复用既有 run_mode 取值，不新增第三套词表。"
            "**迁移号**：本面用 sqlite 000055 / versioned 000134（000054 / 000133 归 PF-7，"
            "**2026-10-24 #608 跨面借用（owner 不变，故不声明进本面 paths；重复声明会被登记处 loader 直接拒绝）**：#608（F01-11 采纳节点按服务端 HTTP 契约补齐确认载荷与工作副本声明，PR #612 已合入 main） 改动了下列**别的面拥有**的文件，本面**借用**它们、owner 不变：`docs/08-本轮实施方案/contracts/workflow.json`（docs/08-本轮实施方案/contracts/workflow.json）。 **2026-10-24 #546 跨面借用（owner 不变，故不声明进本面 paths；重复声明会被登记处 loader 直接拒绝）**：#546 口径②：F01-07 的检索方式声明由 vector_required 改为 keyword_only（裁定记录：docs/08-本轮实施方案/Spec-Beta-F01-07-检索方式口径变更.md） 改动了下列**别的面拥有**的文件，本面**借用**它们、owner 不变：`scripts/ci/test_lingdoc_beta_evidence.py`（PF-10 的文件级声明；本面把「恰好一个节点声明检索方式」那条守护改成新事实，并补 keyword_only 的三态守护）。 **2026-10-24 #546 跨面借用（owner 不变，故不声明进本面 paths；重复声明会被登记处 loader 直接拒绝）**：#546 口径②：F01-07 的检索方式声明由 vector_required 改为 keyword_only（裁定记录：docs/08-本轮实施方案/Spec-Beta-F01-07-检索方式口径变更.md） 改动了下列**别的面拥有**的文件，本面**借用**它们、owner 不变：`docs/08-本轮实施方案/contracts/workflow.json`（PF-10 的文件级声明；本面只改 F01-07 那一个节点的 retrieval 字段与 retrieval_modes 的两段说明文字）。 **2026-10-24 #590 跨面借用（owner 不变，故不声明进本面 paths；重复声明会被登记处 loader 直接拒绝）**：#590（材料分块与坐标逐字自洽 ＋ 分块与 embedding 解耦，已于 2026-10-24 合入 main，合并提交 781295c7） 改动了下列**别的面拥有**的文件，本面**借用**它们、owner 不变：`scripts/lingdoc_mock/run_f01.py`（scripts/lingdoc_mock/run_f01.py）。 **2026-10-24 #592 跨面借用（owner 仍是本面，不变更归属）**：#592（PR #601）让**本面拥有的**`scripts/acceptance/beta_evidence.py` 与 `scripts/ci/test_lingdoc_beta_evidence.py` 承担放行判定侧的「人工断言未判定 ⇒ 不得 PASS」「契约里没跑到的 checks ⇒ 明确 NOT RUN」「provider_semantics_status 参与整体判定」「受控窗口核验接入放行链」四件事。**owner 仍是本面**，本面按「owner 保持拥有、借用方落地后归还」的先例借给 PF-14 使用（先例：PF-8 借 PF-4 的四个路径、PF-10 借 PF-7 的组合根）；**行为由 PF-14 的需要决定，但归属不变**。⇒ 借用事实记在 PF-14 侧与本面两侧，两侧一致。 已记录在 main 上 ⇒ 按约定已登记的面不再让号；机器侧不兜底，判据见本面 summary）。"
        ),
    },
    {
        "name": "PF-14 Beta 首版放行执行",
        "issue": 492,
        "issues": (482,),
        "status": "claimed",
        "claimed_by": "magician336",
        "paths": (
            # #491 的 spec 落盘：Beta 首版放行验收的仓内副本（56 号之后的第一张）。
            ("docs/06-灵档产品开发规划/57-Spec-Beta首版放行验收.md", "file"),
            # #484 的材料接入脚本：真接入（/knowledge/file → 轮询 parse_status →
            # 查 chunks → 打 retrieval/hybrid-search）。名字照 #484 探索结论 §4.3。
            ("scripts/acceptance/beta_material_ingest.py", "file"),
            # #484 的守护测试（探索结论 §5.1 推荐落点：自由路径，PF-10 不拥有它）。
            ("scripts/ci/test_lingdoc_beta_material.py", "file"),
            # #484 的服务端能力（材料接入与处理就绪）：**目录级**声明覆盖整棵子树——
            # 这是一个新增包，后续加文件是常态，逐文件登记必然漏（同 PF-10 对
            # docs/08-本轮实施方案/acceptance/beta-first/ 的做法）。
            ("internal/lingdoc/materiallingest/", "dir"),
            # #494 的守护测试：资料标识参数化（入口 → 绑定动作，无第二处硬编码，
            # 不传则明确报错，阴性验证）。
            ("scripts/ci/test_lingdoc_beta_knowledge_id.py", "file"),
            # #504 的缺口登记表：稳定标识 → 票号/条目的**唯一**绑定处。
            # 代码与产物只引用标识（GAP-…），票号变了只改这一份文件。
            ("docs/08-本轮实施方案/acceptance/gaps.json", "file"),
            # #504 的守护测试：标识不漂（产出器里不出现票号）、标识齐全、
            # 「没跑」只有一种拼写。
            ("scripts/ci/test_lingdoc_beta_gap_ids.py", "file"),
            # #486 的账本写入端：SourceRuntime 持有那一个账本实例，所有适配器共用它。
            # 生成适配器所在的 workspace/generation.go 由 PF-2 持有 ⇒ 借用，不声明。
            ("internal/lingdoc/workspace/source_runtime_gorm.go", "file"),
            # #486 的守护测试：一次调用一行、失败也写、未注入账本不改变行为。
            ("internal/lingdoc/workspace/generation_model_call_ledger_test.go", "file"),
            ("internal/lingdoc/workspace/generation_model_call_ledger_table_test.go", "file"),
            # #499 的 check kind 词表单一权威：执行器、契约静态校验器与报告文案都从
            # 这里读，任何一处再写一份词表就是缺陷。
            ("scripts/lingdoc_check_kinds.py", "file"),
            # #499 的守护测试：单一权威（无第二份词表）、穷举校验（无闲置 kind、
            # 每条断言可判定、**词表里的每个算子都必须有求值分支**）、未知 kind 与
            # 多余键必须报错、以及阴性验证（破坏期望值必须转红）。
            ("scripts/ci/test_lingdoc_beta_check_kinds.py", "file"),
            # 守护的旧名：文件已随 #499 改名为 test_lingdoc_beta_check_kinds.py，但
            # **这条路径仍留在本面的路径集里**——登记处记的是归属，不是文件清单；
            # 一个活着的面不得缩自己的路径集（棘轮：path-set-shrunk / path-unowned），
            # 路径随本面结束（done）才释放。
            ("scripts/ci/test_lingdoc_check_kinds.py", "file"),
            # #499 的契约静态校验器：接单一权威，并把 F01 契约自己的断言纳入静态
            # 校验（此前只有运行到那个节点才会被校验）。
            ("docs/08-本轮实施方案/contracts/validate_artifacts.py", "file"),
            # #499 的失败路径契约档：已有 4xx 节点（409 冲突 / 403 撤权 / 422 整批
            # 拒绝等）挂上 http_error 与 not_in 断言。
            ("docs/08-本轮实施方案/contracts/scenarios.json", "file"),
            # #499 补 53-Spec 决策 3 的表：第 6 个 kind（non_empty）与新增的 5 个
            # kind，并补上「词表单一权威」「穷举校验」「失败态的表达方式」三条决策。
            ("docs/06-灵档产品开发规划/53-Spec-Beta首版验收执行器与证据目录.md", "file"),
            # #499 的场景执行器与它的两个回归测试：报告文案要覆盖每个 kind；
            # 断言的词表改从单一权威读（原来测试里自己写了一份 4 个算子）。
            ("scripts/lingdoc_mock/run_scenario.py", "file"),
            ("scripts/lingdoc_mock/test_workflow_specs.py", "file"),
            ("scripts/lingdoc_mock/test_workflow_scenario.py", "file"),
            # #533 的对称类型谓词（embedding 能力判定）与它的落点：词表唯一，不新增第二份类型词表。
            ("internal/types/model.go", "file"),
            # #533 的守护：谓词的对称性（chat 侧放行集合不变、embedding 侧只放行 Embedding）。
            ("internal/types/model_embedding_capable_test.go", "file"),
            # #533 的两个 embedding 出口守卫：建 embedder 之前 fail-closed，跨租户出口同守卫。
            ("internal/application/service/model.go", "file"),
            # #533 的守护：两个 embedding 服务出口（provider 请求数 = 0 的断言来自进程内桩）。
            ("internal/application/service/model_embedding_type_guard_test.go", "file"),
            # #533 的建库/改库守卫：embedding_model_id 指向的行必须确实 embedding-capable（与 summary_model_id 守卫成对）。
            ("internal/application/service/knowledgebase.go", "file"),
            # #533 的守护：建库与改库两条路径的 embedding 模型类型校验。
            ("internal/application/service/knowledgebase_embedding_model_guard_test.go", "file"),
            # #533 的连接自检守卫：chat 自检读已存行的 Type，非 chat 能力不发任何请求；embedding 自检镜像同一守卫；改库同守卫。
            ("internal/handler/initialization.go", "file"),
            # #533 的守护：chat 连接自检对非 chat 模型 0 请求。
            ("internal/handler/initialization_model_type_guard_test.go", "file"),
            # #533 的守护：embedding 连接自检的镜像守卫。
            ("internal/handler/initialization_embedding_model_guard_test.go", "file"),
            # #535 的权威位置、审计列语义、判定规则与第二段裁定清单。
            ("docs/08-本轮实施方案/T535-租户角色词表收敛.md", "file"),
            # #535 的唯一租户角色词表：IsValid 是唯一判定入口。
            ("internal/types/tenant_member.go", "file"),
            # #535 的守护：词表四层（定义唯一、无第二份同值判定、读取方不越权、写入方受守护）。
            ("internal/types/tenant_role_vocabulary_guard_test.go", "file"),
            # #535 的 Actor.Role 语义与三处按词表判定（validateActor / GetVersionForBinding / recordAudit）。
            ("internal/lingdoc/rulesetstore/store.go", "file"),
            # #535 消灭第二份同值判定：裸字面量 owner/admin 改为引用权威词表成员。
            ("internal/lingdoc/rulesetstore/tenant_authorizer.go", "file"),
            # #535 的守护：rulesetstore 侧的词表唯一性。
            ("internal/lingdoc/rulesetstore/actor_role_vocabulary_guard_test.go", "file"),
            # #535 的测试夹具：漂移的角色字面量改为引用权威词表成员。
            ("internal/lingdoc/rulesetstore/store_test.go", "file"),
            # #535 的测试夹具：漂移的角色字面量改为引用权威词表成员。
            ("internal/lingdoc/rulesetstore/store_postgres_test.go", "file"),
            # #535 的测试夹具：漂移的角色字面量改为引用权威词表成员。
            ("internal/lingdoc/rulesetstore/audit_page_test.go", "file"),
            # #535 的测试夹具：漂移的角色字面量改为引用权威词表成员。
            ("internal/lingdoc/rulesetstore/metrics_test.go", "file"),
            # #535 的守护：templategovernance 侧的词表唯一性与写侧守护。
            ("internal/lingdoc/templategovernance/actor_role_vocabulary_guard_test.go", "file"),
            # #535 的审计列语义注释：审计里记的 Role 到底是什么。
            ("internal/lingdoc/template/ruleset_use_audit.go", "file"),
            # #535 的审计列语义注释：交付侧读到的 Role 的含义。
            ("internal/lingdoc/delivery/export.go", "file"),
            # #535 的守护：delivery 侧的词表唯一性。
            ("internal/lingdoc/delivery/actor_role_vocabulary_guard_test.go", "file"),
            # #534 的守护：延迟重排路径与正常路径共用同一个重试上限（同一常量，不新增第二份数值）。
            ("internal/lingdoc/workspace/generation_retry_budget_test.go", "file"),
            # #501 的执行器单测：两步结构（同资源约束、前置不成立不得恒真 PASS、二次操作失败记 BLOCKED/FAIL）的独立新文件，不改既有的 test_workflow_*.py。
            ("scripts/lingdoc_mock/test_workflow_twostep.py", "file"),
            # #542 的守护：账本 output_limit_tokens == 实际请求的 completion budget（读原始 JSON 账本断言），且上限按生效 profile 取值、超出 profile 上限记 limit_violation。
            ("internal/lingdoc/workspace/acceptance_profile_limit_test.go", "file"),
            # #542 改动的既有测试文件：原先钉死 2048 旧口径，改为断言取生效 profile 的上限。
            ("internal/lingdoc/workspace/selected_rewrite_budget_test.go", "file"),
            # #558 的守护：导出的写入口与 Store.RecordRuleSetUse 的角色校验口径一致；词表外取值必须转红且不写库。
            ("internal/lingdoc/rulesetstore/use_audit_actor_role_test.go", "file"),
            # #557 的共享解析器：审计列 actor_role 的唯一读侧兜底（刻意不持有任何词表，词只在 locale 包里）。
            ("frontend/src/i18n/auditActorRoleLabel.ts", "file"),
            # #557 的守护：三处读点都读同一个解析器、词表外值与空串都有确定呈现、五个 locale 键集一致。
            ("frontend/src/i18n/auditActorRoleLabel.test.ts", "file"),
            # #564 的守护：迁移 down 链的语义不可逆边界与守卫行为（000043/000044 守卫存在、000011/000000 的不可逆点如实标注）。
            ("internal/database/migration_full_down_test.go", "file"),
            # #545 的守护：F01 契约补 model-use 许可节点后，F01-07 不手工插数据即返回 202；撤销/未建立时仍返回 422（不得默认放行）；许可记录与 asset_revision 的绑定（revision 前进后旧同意不再放行）。
            ("internal/lingdoc/workspace/model_use_consent_test.go", "file"),
            # #546 的受控窗口核验：按验收预算账本核对窗口内 embedding 调用行数；出现 embedding 行则整个受控窗口判失败（不得当作「已通过检索」）。
            ("scripts/lingdoc_mock/controlled_window_check.py", "file"),
            # #546 的守护：契约声明「本节点只走关键词检索」的节点确实不产生 embedding 请求；节点未声明检索方式、或声明与实际不符时的处理。
            ("scripts/ci/test_lingdoc_controlled_window.py", "file"),
            # #583 的 profile 配置表：把「单值判断」改成按 profile 取配置（v1 的取值逐字段不变，v2 新增 4 次调用与累计 90,000 token 的预留闸门）。
            ("internal/models/acceptancebudget/profile_config.go", "file"),
            # 2026-10-25 #546（embedding 授权执行）：本面在 profile 配置表里**新增一行**受控
            # embedding profile（4 次调用 ＋ 8000 token 累计 ＋ 16000 输入上限 ＋ 0 输出上限）——
            # 既有 profile 的取值逐字未变（守护逐项断言），新增的 profile 与它的常量/计价基准
            # 在下面两条路径里。
            # #546 的受控 embedding profile 的常量与计价基准：profile 名、4 次调用、8000 token
            # 累计上限、16000 输入上限、0 输出上限、金额上限（＝单次最大调用 × embedding 输入价）。
            ("internal/models/acceptancebudget/qwen_token_budget.go", "file"),
            # #546 的守护：embedding profile 的四项上限、模型白名单（放行两个 embedding 模型、
            # **拒绝 chat 模型** ⇒ 该 profile 下进程发不出 chat 调用）、账本自带自己的上限与
            # 计价基准、账本可被自己的校验器读回、**既有 profile 逐字未变**。
            ("internal/models/acceptancebudget/qwen_embedding_profile_test.go", "file"),
            # #583 的三个量的统一读取口：预留额度 / provider 实际 usage / 累计剩余额度（usage 不可核实时 RemainingActual 为 nil，不是 0）。
            ("internal/models/acceptancebudget/token_budget_state.go", "file"),
            # #583 的守护：v2 的调用数上限、累计预留闸门、usage 不可核实即停窗、旧 profile 逐字未变、并发只能一个通过。
            ("internal/models/acceptancebudget/qwen_token_budget_v2_test.go", "file"),
            # #583 的应用缝守护：超限不出网（provider 请求数 = 0）、预算预留失败不出网、第 5 次在出网前被拒、输出上限 4096 实际下发。
            ("internal/lingdoc/workspace/generation_qwen_v2_acceptance_budget_test.go", "file"),
            # #576 的租户角色中文标签唯一来源：前端整个租户角色词表（四个值）只在这一处产生中文标签；审计列解析器（A）引用它，权限验收台（B）不再自带同值映射。
            ("frontend/src/i18n/tenantRoleLabel.ts", "file"),
            # #576 的守护：断言「租户角色四值在整个前端只有一处中文标签来源」，并要求项目治理角色 / 课题角色这两张不同维度的表各自标注维度。
            ("frontend/src/i18n/tenantRoleLabel.test.ts", "file"),
            # #576：zh-CN locale 的两个租户角色 namespace 改为从唯一来源展开 ⇒ 两处说法结构上不会漂移。
            ("frontend/src/i18n/locales/zh-CN.ts", "file"),
            # #576：权限验收台的角色标签改为引用共享解析器（原 PERMISSION_ROLE_LABELS 表已删除）。
            ("frontend/src/views/lingdoc/permissionEvidence.ts", "file"),
            # #576：验收台「当前请求身份」列与「最近授权审计」行改走同一个中文说法。
            ("frontend/src/views/lingdoc/PermissionEvidence.vue", "file"),
            # #576：验收台单测适配新签名，并断言与审计列拿到同一个字符串。
            ("frontend/src/views/lingdoc/permissionEvidence.test.ts", "file"),
            # 错误形态的跨面归并文档：dependency_unavailable 的形态数与 retryable 取值的实况，以及新的策略拒绝形态。
            ("docs/08-本轮实施方案/Spec-47-段二-错误形态跨面归并与共享核心提取.md", "file"),
            # 检索失败的阴性验证：受控 profile 拒绝与非策略故障必须给出不同的对外形态。
            ("internal/lingdoc/workspace/generation_retrieval_error_test.go", "file"),
            # client 侧的知识库更新调用点：#590 让它把 embedding_model_id 一起传下去。
            ("client/knowledgebase.go", "file"),
            # 材料合格性前置门的命令行实现（两阶段：内容检查 ＋ 来源引文检查，fail-closed）。
            ("cmd/lingdoc-material-check/main.go", "file"),
            # 材料闸门命令自身的单测：退出码、分页聚合、token 读取与三条 fail-closed 路径。
            ("cmd/lingdoc-material-check/main_test.go", "file"),
            # 知识图谱查询工具的测试随本 PR 的分块语义调整。
            ("internal/agent/tools/query_knowledge_graph_test.go", "file"),
            # D1 的数据库级往返用例：PUT 写入 embedding_model_id 后走独立读回路径验证，删掉持久化调用必须转红。
            ("internal/application/repository/knowledgebase_embedding_model_roundtrip_test.go", "file"),
            # 数据源服务的测试随本 PR 的分块语义调整。
            ("internal/application/service/datasource_service_test.go", "file"),
            # 无 embedding 模型时的处理：解析不中止、分块照落、index_status 记 failed（#590 的核心行为变更）。
            ("internal/application/service/knowledge_embedding_failure_test.go", "file"),
            # 分块与 embedding 解耦的实现：取不到 embedding 模型不再中止解析，路由按可服务类型收窄。
            ("internal/application/service/knowledge_process.go", "file"),
            # 解耦的守护：分块路径上不调 embedder、路由只含关键词。
            ("internal/application/service/knowledge_process_embedding_decoupled_test.go", "file"),
            # D2 的守护：路由失败与 embedding 缺失各自记自己的原因，不互相覆盖。
            ("internal/application/service/knowledge_process_stage_failure_test.go", "file"),
            # fake 仓库改为真正落库并返回副本，使「已持久化」断言不再结构上不可能失败（D1 的根因）。
            ("internal/application/service/knowledgebase_pr3_test.go", "file"),
            # embedding_model_id 更新的服务层测试（读回值 ＋ 省略字段时保留原值）。
            ("internal/application/service/knowledgebase_update_embedding_model_test.go", "file"),
            # 新增的解析阶段错误码（embedding 模型不可用等），有唯一使用点。
            ("internal/errors/parse_error_codes.go", "file"),
            # 原文读取：新增存储层能力接口（OriginBytesReader），并把接口的 err 语义写准（D4）。
            ("internal/evidence/origin.go", "file"),
            # 存储层原文读取的测试（含三类失败降档与超上限拒绝）。
            ("internal/evidence/origin_storage_test.go", "file"),
            # 材料合格性判据：坐标越界／空内容／逐字不一致三道检查，与来源复核共用 quoteAt。
            ("internal/evidence/qualification.go", "file"),
            # 合格性判据的阴性用例（表头污染、坐标越界、空内容、无分块）。
            ("internal/evidence/qualification_test.go", "file"),
            # 知识库更新接口的请求/响应：embedding_model_id 真的进出。
            ("internal/handler/knowledgebase.go", "file"),
            # handler 层的 embedding_model_id 读回测试。
            ("internal/handler/knowledgebase_update_embedding_model_test.go", "file"),
            # 分块器：表头移出正文，正文只保留原文切片，坐标与正文长度自洽。
            ("internal/infrastructure/chunker/splitter.go", "file"),
            # 分块器的坐标与正文自洽测试。
            ("internal/infrastructure/chunker/splitter_test.go", "file"),
            # 逐字不变式的守护：分块正文必须等于原文在它自己坐标上的切片。
            ("internal/infrastructure/chunker/verbatim_invariant_test.go", "file"),
            # 存储层适配：把上传材料的 resource:// 引用读成字节（原文取回的真实路径）。
            ("internal/lingdoc/workspace/adapters_gorm.go", "file"),
            # 存储层原文读取的整链测试（含 resource:// 形状的真实路径）。
            ("internal/lingdoc/workspace/adapters_gorm_origin_test.go", "file"),
            # 来源复核：坐标与引文的自洽判定。
            ("internal/lingdoc/workspace/source_policy.go", "file"),
            # 知识库更新接口的签名（新增 embedding_model_id 参数）。
            ("internal/types/interfaces/knowledgebase.go", "file"),
            # 知识库模型：embedding_model_id 字段（读回即此字段）。
            ("internal/types/knowledgebase.go", "file"),
            # 材料合格性闸门的守护用例：两阶段的 fail-closed 行为（不合格材料必须被前置检查拒绝，且失败报告必须落盘）。
            ("scripts/lingdoc_mock/test_material_gate.py", "file"),
            # 工作流报告的守护用例：F01 报告的判定口径（未求值的 check 不得被记为通过，失败的阶段必须留下原因）。
            ("scripts/lingdoc_mock/test_workflow_reports.py", "file"),
            # 工作流执行器的守护用例：阶段顺序与两阶段闸门的接线（闸门位置错了必须转红）。
            ("scripts/lingdoc_mock/test_workflow_runner.py", "file"),
            # #546 口径②的裁定记录：F01-07 的检索方式由 vector_required 改为 keyword_only （事实依据、七项零 embedding 前置、声明前后对照、放行链层面的判定）＋ 验收矩阵
            ("docs/08-本轮实施方案/Spec-Beta-F01-07-检索方式口径变更.md", "file"),
            # 模型行经真实仓储读取路径读回的守护：TEXT 写入、应用写入、损坏写入三种载体。
            ("internal/application/repository/model_parameters_scan_test.go", "file"),
            # 本面在本轮（#609）改动的文件。
            ("internal/types/agent.go", "file"),
            # 本面在本轮（#609）改动的文件。
            ("internal/types/chat.go", "file"),
            # 本面在本轮（#609）改动的文件。
            ("internal/types/chat_history_config.go", "file"),
            # 本面在本轮（#609）改动的文件。
            ("internal/types/custom_agent.go", "file"),
            # 本面在本轮（#609）改动的文件。
            ("internal/types/indexing_strategy.go", "file"),
            # 本面在本轮（#609）改动的文件。
            ("internal/types/json.go", "file"),
            # JSON 列的共享读取器：接受 []byte 与 string 两种载体，其余类型明确报错（不再静默丢弃参数）。
            ("internal/types/json_column.go", "file"),
            # 驱动的真实载体类型实测（SQLite 生产驱动 ＋ 纯 Go 驱动 ＋ PostgreSQL），问驱动而不是问文档。
            ("internal/types/json_column_dialect_test.go", "file"),
            # 共享读取器的载体契约守护：[]byte/string 收、不认识的类型报错、NULL 不清空接收者。
            ("internal/types/json_column_test.go", "file"),
            # 本面在本轮（#609）改动的文件。
            ("internal/types/json_map.go", "file"),
            # 本面在本轮（#609）改动的文件。
            ("internal/types/mcp.go", "file"),
            # 本面在本轮（#609）改动的文件。
            ("internal/types/memory.go", "file"),
            # 本面在本轮（#609）改动的文件。
            ("internal/types/memory_extraction.go", "file"),
            # 本面在本轮（#609）改动的文件。
            ("internal/types/message.go", "file"),
            # 本面在本轮（#609）改动的文件。
            ("internal/types/message_suggestion.go", "file"),
            # 本面在本轮（#609）改动的文件。
            ("internal/types/message_test.go", "file"),
            # 本面在本轮（#609）改动的文件。
            ("internal/types/retrieval_config.go", "file"),
            # 本面在本轮（#609）改动的文件。
            ("internal/types/search.go", "file"),
            # 本面在本轮（#609）改动的文件。
            ("internal/types/session.go", "file"),
            # 本面在本轮（#609）改动的文件。
            ("internal/types/storagebackend.go", "file"),
            # 本面在本轮（#609）改动的文件。
            ("internal/types/tenant.go", "file"),
            # 本面在本轮（#609）改动的文件。
            ("internal/types/tenant_env_vars.go", "file"),
            # 本面在本轮（#609）改动的文件。
            ("internal/types/user.go", "file"),
            # 本面在本轮（#609）改动的文件。
            ("internal/types/vectorstore.go", "file"),
            # 本面在本轮（#609）改动的文件。
            ("internal/types/vectorstore_test.go", "file"),
            # 本面在本轮（#609）改动的文件。
            ("internal/types/web_search.go", "file"),
            # 本面在本轮（#609）改动的文件。
            ("internal/types/web_search_provider.go", "file"),
            # 本面在本轮（#609）改动的文件。
            ("internal/types/wiki_page.go", "file"),
            # client 模块的资源 URL 行为断言（对照变更：只改 client/ 的最小改动）。
            ("client/resource_urls_test.go", "file"),
            # client 面的门禁覆盖守护：只改 client ⇒ client job 跑、app job 不跑；只改 docs ⇒ 两者都不跑；以及覆盖判定的负向用例。
            ("scripts/ci/test_lingdoc_client_coverage.py", "file"),
            # 2026-10-24 #618（PR #620，合并提交 f404fd02）：PF-10 借用了本面持有的
            #   docs/08-本轮实施方案/contracts/validate_artifacts.py（让契约静态校验器与
            #   F01 跑批执行器共用同一份展开实现）。**owner 仍是本面**，不变更归属。
            #   依据票：#618（PR #620）。
            # 2026-10-24 #626（PR #637，内容提交 ab9fd141，合并提交 6b394ca7）：PF-10 **再次借用**
            #   本面持有的 docs/08-本轮实施方案/contracts/validate_artifacts.py —— 该 PR 在校验器里
            #   新增 is_marked()（逐条复刻 runner 的 _is_marked）并把 one_to_one 的 entry_non_empty
            #   改用同一口径。**owner 仍是本面**，不变更归属；本面不把它借给别的面。
            #   依据票：#626（PR #637）、#621。
            # 2026-10-25 #639（PR #644，内容提交 e0d1df5f）：PF-10 **再次借用**本面持有的
            #   docs/08-本轮实施方案/contracts/validate_artifacts.py —— 该 PR 把校验器里
            #   marked_or_absent / non_empty / all_non_empty 三处判断点改调 is_marked()，
            #   使校验器与跑批器对「什么算空」同一口径。**owner 仍是本面**，不变更归属。
            #   依据票：#639（PR #644）、#621。
            # 2026-10-25 #633（PR #643，内容提交 e01487b5）：PF-10 **借用**本面持有的两条路径 ——
            #   scripts/lingdoc_mock/test_workflow_runner.py、scripts/lingdoc_mock/test_workflow_reports.py
            #   —— 理由是「跑批器新增了 run_id / manual_decisions 两个报告键，那两处的精确 key 集合断言
            #   必须同步扩充」。改动只是**扩充**断言（未删未弱化）。**owner 仍是本面**，不变更归属。
            #   依据票：#633（PR #643）。
            # 2026-10-25 #619 契约侧（PR #646，内容提交 389a8df5 ＋ 0b86cc01）：PF-10 **借用**本面
            #   持有的五条路径 ——
            #   docs/08-本轮实施方案/contracts/scenarios.json（补 F34/F35 两条稳定化场景）
            #   scripts/lingdoc_mock/test_workflow_specs.py（节点数 28 → 31 的连锁断言）
            #   scripts/lingdoc_mock/test_workflow_twostep.py（同上）
            #   scripts/lingdoc_mock/test_workflow_runner.py（同上）
            #   docs/06-灵档产品开发规划/57-Spec-Beta首版放行验收.md（沿用它自己的日期化更新形态
            #   补一条节点数 28 → 31，**不改写它的历史问题陈述**）
            #   **owner 全部仍是本面**，不变更归属；本面不把它们借给别的面。
            #   依据票：#619（PR #646）、#633（PR #643）。
            # 2026-10-25 #654（PR #654，合并提交 5ed7ae06）：本面**认领**该 PR 新增的三条路径 ——
            #   scripts/lingdoc_mock/rejudge_report.py（零额度重判路径：只重算人工判定层，机器事实逐字节保留）
            #   scripts/lingdoc_mock/test_rejudge_report.py（26 条验收/负向/端到端用例，由 lingdoc-mock.yml discover）
            #   scripts/ci/test_lingdoc_rejudge_report.py（7 条跨路径守护，由 LingDoc PR gate 执行）
            ("scripts/lingdoc_mock/rejudge_report.py", "file"),
            ("scripts/lingdoc_mock/test_rejudge_report.py", "file"),
            ("scripts/ci/test_lingdoc_rejudge_report.py", "file"),
            # 2026-10-25 #649（PR #659，合并提交 540f77e4）：本面**认领**领域层新增的两条路径 ——
            #   internal/lingdoc/workspacecore/manual_judgement_audit.go
            #     （权威人工判定审计：actor/authorization/channel/occurred_at 全部由服务端推导；
            #      审计行落 lingdoc_project_audits，action=manual.judgement，details 顶层扁平）
            #   internal/lingdoc/workspacecore/manual_judgement_audit_test.go
            #     （14 条用例：服务端推导的事实、Owner 门禁、机器主体门禁、完整性闸门、
            #      幂等语义、人工 FAIL、原子回滚、业务键 run 维度、IM 渠道分类）
            ("internal/lingdoc/workspacecore/manual_judgement_audit.go", "file"),
            ("internal/lingdoc/workspacecore/manual_judgement_audit_test.go", "file"),
            # 2026-10-25 #649 HTTP 侧（本面）：本面**认领**HTTP 接缝与它的两条测试 ——
            #   internal/lingdoc/workspace/manual_judgement_routes.go
            #     （两个端点：POST 提交判定 / GET 导出审计记录；窄端口 ManualJudgementApplication）
            #   internal/lingdoc/workspace/manual_judgement_routes_test.go（12 条 HTTP 级用例）
            ("internal/lingdoc/workspace/manual_judgement_routes.go", "file"),
            ("internal/lingdoc/workspace/manual_judgement_routes_test.go", "file"),
            # 2026-10-25 #649 组合根侧的验收用例（生产装配下端点可调、窄端口是同一实例）——
            ("internal/container/lingdoc_manual_judgement_test.go", "file"),
            # 2026-10-25 #649 路由注册接缝：internal/router/router.go 此前**无主**（不在任何面的
            #   路径集里），本面在其中加了一个可选端口与一行注册 ⇒ 改它不构成跨面越界，
            #   但按「改了要记录」约定登记进本面。
            ("internal/router/router.go", "file"),
            # 2026-10-25 #664（PR #664）：本面**认领**该守卫测试文件 ——
            #   internal/router/lingdoc_manual_judgement_registration_test.go
            #     （守住人工判定端点在**生产 router**（router.NewRouter ＋ dig 真实装配）上的
            #      注册接缝：端口存在 ⇒ 两条路径在 engine.Routes() 里；端口为 nil ⇒ 不在。
            #      这条守卫补上了 #660 独立复核指出的 F1 覆盖缺口。）
            ("internal/router/lingdoc_manual_judgement_registration_test.go", "file"),
        ),
        "resolves": (),
        "summary": ("**2026-10-23 #499 断言表达力补齐**：本面新增 check kind 的**单一权威**（scripts/lingdoc_check_kinds.py），"
            "执行器、契约静态校验器与报告文案都从那里读——原来词表分三处且互不一致（校验器只认 4 个结构 kind，"
            "执行器只**数**它认识的 kind、把拼错的 kind 静默忽略）。新增 5 个领域 kind"
            "（http_error / not_in / all_non_empty / equals_step / provenance_real），"
            "在 F01 契约与失败路径契约上挂上断言并启用闲置的 keys 与 one_of；"
            "F01 契约自己的断言也纳入静态校验（此前只有运行到那个节点才会被校验）。"
            "**一处跨面借用（#499）**：F01 执行器（scripts/lingdoc_mock/run_f01.py）与 F01 契约"
            "（docs/08-本轮实施方案/contracts/workflow.json）**是 PF-10 的路径，owner 仍是 PF-10**"
            "（PF-10 已交面，done 状态仍占用路径）⇒ 本面借用它们，**不把它们声明进本面的 paths**，"
            "借用事实记在这里与 PF-10 侧。**两处未完成项已登记**：GAP-PROVENANCE-FIELD"
            "（OpenAPI 未声明 provenance 字段，逐段真实溯源挂不上节点断言）与 GAP-FAILURE-PATH-NODES"
            "（F01 序列内的 4xx 节点要等 #501 的两步结构）。"
            "**2026-10-24 补一条根因修复（求值分派不再落空）**：两个求值分派原先是**隐式**的——"
            "领域算子落到 _judge_domain_check 末尾的 fallthrough（静默按 non_empty 求值）、"
            "结构算子落到 _evaluate_checks 的 `else: # one_of`（静默按 one_of 求值）"
            "⇒ 断言看上去被求值了，实际回答的是另一个问题，报告却写「passed」。"
            "现在两个分派都是**显式**的：没有分支的算子一律报错（WorkflowError），"
            "并由 scripts/ci/test_lingdoc_beta_check_kinds.py 双向守着"
            "「词表里的每个算子都必须有求值分支，且求值器不会判词表以外的算子」。"
            "**2026-10-24 归属裁定（管理会话）**：① contracts/validate_artifacts.py 归本面"
            "（它是 workflow.json 的静态校验器、随契约形状变更走；此前不在任何面的路径集里，"
            "本面在 #530 已把它登记进来）⇒ 本面可直接改它，不必等锁。"
            "② scripts/acceptance/beta_evidence.py 的 owner 保持 PF-10、**不新增登记**；"
            "#501 若要改它，属本面借用 PF-10 的路径，由本面记借用事实，不得声明进本面 paths。"
            "③ #501 的执行器单测新建独立文件 scripts/lingdoc_mock/test_workflow_twostep.py，"
            "不写进既有的 test_workflow_*.py ⇒ 本面的待合改动与它零重叠。"
            "**#484 的材料接入能力已实现**（internal/lingdoc/materiallingest/，本面持有该路径）："
            "稳定材料身份＝固定标题前缀＋文件内容 SHA-256（不含时间戳）⇒ 同一份材料恒等；"
            "接入幂等（同一份材料重复接入不新增知识，结果以 Reused 标记复用）；"
            "处理状态取数落在 evidence.ProcessingStateOf → evidence.AssetStateReady 这条链 ⇒ "
            "与 F01-06 响应里的「就绪」同口径（与绑定层 evidence.Bindings.RefreshAsset 同一条链，不会有两套口径）。"
            "**未接 HTTP 端点与组合根**：路由基线断言与 contracts/openapi.json 归 L3 锁，"
            "且该包目前零生产调用点（git grep materiallingest 只命中包内与文档）⇒ "
            "**验收路径改用 F01 执行器现有的 --knowledge-id 注入**（F01-06 的契约节点已有 {{knowledge_id}} 与"
            "「processing_state=ready」断言，不需要新增契约节点）。"
            "Beta 首版放行的执行面：交付 Spec #482（Beta 首版放行验收），交付票 #481 / #484 / #485 / #486 / #487 / #488 / #489 / #490 / #491 / #483。**判定接缝唯一**（来自 #482 的裁定）：Beta 放行只在 **F01 契约执行序列**上判定，三个连续结果串行跑在同一序列里，不为每一行另造执行路径。**第二人独立复现不是放行门槛**（#483 的口径）：它是必须登记的未完成项，不得记为已通过。**2026-10-23 认领**（认领的人是 magician336）：先做 frontier 上能做的那一张，不跨票抢跑。**一处跨面借用**：F01 执行器（scripts/lingdoc_mock/run_f01.py）、F01 契约（docs/08-本轮实施方案/contracts/workflow.json）、证据产出器（scripts/acceptance/beta_evidence.py）与证据守护测试（scripts/ci/test_lingdoc_beta_evidence.py）**都是 PF-10 的路径，owner 仍是 PF-10**（PF-10 已交面，done 状态仍占用路径）；本面按「owner 保持拥有、借用方落地后归还」的先例借用它们（先例：PF-8 借 PF-4 的四个路径、PF-10 借 PF-7 的组合根），**故不把它们声明进本面的 paths**——重复声明会被登记处 loader 直接拒绝。**本面还新建了缺口登记表**（docs/08-本轮实施方案/acceptance/gaps.json）：稳定标识（GAP-…）与票号/条目的唯一绑定处——代码与证据只引用标识，票号变更只改这一份文件（#504）。**一处跨面借用（#486 账本写入端）**：F01 的模型调用在生成路径（internal/lingdoc/workspace/generation.go），那一路原先**不写** lingdoc_model_call_ledger ⇒ 即便真跑真实模型，账本仍是 0 行，「真实模型调用」这条结论项结构上不可能为 PASS。本面把「一次真实模型调用 ⇒ 一行账本」的记账口径**下沉**为共享函数（workspace/agent_model.go 里的 modelCallFacts + recordModelCallRow），让 Agent 轮次与生成路径**共用**它（不造第四套），并把账本实例放进 SourceRuntime ⇒ 所有适配器共用同一个账本。**借用的路径（owner 未变，不声明进本面 paths）**：internal/lingdoc/workspace/agent_model.go 与 internal/container/lingdoc.go（owner PF-7 —— 已于 2026-10-23 随 #509 归还 PF-7，PF-14 不再改它）、internal/lingdoc/generation/service.go（owner PF-2 的目录级声明 internal/lingdoc/generation/）。借用的改动事实记在 PF-10 侧与本面两侧。**一处跨面借用（#486 账本写入端）**：F01 的模型调用在 F01-07 startGeneration，走的是生成路径（internal/lingdoc/workspace/generation.go）——那一路原先**不写** lingdoc_model_call_ledger ⇒ 即便真跑真实模型，账本仍是 0 行，「真实模型调用」这条结论项结构上不可能为 PASS。本面把「一次真实模型调用 ⇒ 一行账本」的记账口径**下沉**为共享函数（workspace/agent_model.go 的 modelCallFacts + recordModelCallRow），让 Agent 轮次与生成路径**共用**它（不造第四套），并把账本实例放进 SourceRuntime ⇒ 所有适配器共用同一个账本。**借用的路径（owner 未变，不声明进本面 paths）**：internal/lingdoc/workspace/generation.go（owner PF-2 —— 该面已交面，done 状态仍占用路径；实际写权在本面，记账留痕在此）、internal/lingdoc/workspace/agent_model.go 与 internal/container/lingdoc.go（owner PF-10）、internal/lingdoc/generation/service.go（owner PF-2 的目录级声明 internal/lingdoc/generation/）。**一处跨面借用（#504 口径统一）**：scripts/acceptance/beta_http_readback.py 与 scripts/ci/test_lingdoc_beta_evidence.py 的 owner 仍是 PF-10（未变），本面**不独占**它们、也不把它们声明进本面 paths（done 状态仍占用路径，重复声明会被 loader 拒绝）；借用事实记录于此。借用的原因与事实：#504 统一对偶两侧的 base-url 口径——单一权威＝契约 contracts/openapi.json 的 servers[0].url（http://localhost:4010/api/v1/lingdoc）；HTTP 对偶侧 15 处 API 根字面量归零、改为从契约派生；守护测试由「把两套相反约定各自钉死」改为只钉死单一口径，并新增两条断言（API 根等于契约 servers[0].url 的 path；同一个 base-url 同时喂两侧、两侧给出同一判定）。**归还是否发生：不需要归还** —— owner 未变，本面只是借用，未独占该路径。"
            "**2026-10-24 跨面借用三条（owner 不变，故不声明进本面 paths；重复声明会被登记处 loader 直接拒绝）**：① **#535** 改动了 **PF-13** 拥有的 internal/lingdoc/templategovernance/{model.go, storage.go, catalog_test.go, catalog_postgres_test.go} 四个文件⇒ 本面借用 PF-13 的这四条路径；② **#534** 改动了 **L5（owner 属 PF-2）** 的 internal/lingdoc/workspace/generation.go⇒ 本面借用 PF-2 的这一条路径；③ **#534** 改动了 **PF-2 目录级声明内** 的 internal/lingdoc/generation/tasks.go 与 internal/lingdoc/generation/tasks_test.go⇒ 本面借用 PF-2 的这两条路径；本面只**新增**那条守护测试文件 internal/lingdoc/workspace/generation_retry_budget_test.go。"
            "**2026-10-24 #480 跨面借用（第五条；owner 不变，故不声明进本面 paths；重复声明会被登记处 loader 直接拒绝）**："
            "#480 补 #485 的两个放行链缺口（服务重启后的读回 / 浏览器端到端与三方一致性）。"
            "重启读回必须落在**隔离 PostgreSQL**（协议 §1），而 PF-10 拥有的重启脚本 "
            "scripts/acceptance/lingdoc_full_server_restart.py **写死 SQLite**（DB_DRIVER / DB_PATH / RETRIEVE_DRIVER 三个键），"
            "且 PF-10 自己的证据产出器 scripts/acceptance/beta_evidence.py **早已把 --db-driver / --db-dsn 传给它**——"
            "两者的契约是断的（该脚本的 argparse 不认这两个旗标）⇒ 本面借用 PF-10 的这条路径把它参数化（协议 §1 的双模式："
            "隔离 PostgreSQL ＝ 协议那一轮；SQLite ＝ 本地诊断），并把读回扩到协议 §2 第 5 步点名的四类对象"
            "（项目 / 章节 / 来源绑定 / 交付文件与下载哈希）。"
            "守护测试 scripts/acceptance/test_lingdoc_full_server_restart.py 此前不在任何面的路径集里（自由路径），本面改它；"
            "**2026-10-24 #546 跨面借用（owner 不变，故不声明进本面 paths；重复声明会被登记处 loader 直接拒绝）**：#546 口径②：F01-07 的检索方式声明由 vector_required 改为 keyword_only（裁定记录：docs/08-本轮实施方案/Spec-Beta-F01-07-检索方式口径变更.md） 改动了下列**别的面拥有**的文件，本面**借用**它们、owner 不变：`docs/08-本轮实施方案/contracts/workflow.json`（PF-10 的文件级声明；本面只改 F01-07 那一个节点的 retrieval 字段与 retrieval_modes 的两段说明文字）。 **2026-10-24 #546 跨面借用（owner 不变，故不声明进本面 paths；重复声明会被登记处 loader 直接拒绝）**：#546 口径②：F01-07 的检索方式声明由 vector_required 改为 keyword_only（裁定记录：docs/08-本轮实施方案/Spec-Beta-F01-07-检索方式口径变更.md） 改动了下列**别的面拥有**的文件，本面**借用**它们、owner 不变：`scripts/ci/test_lingdoc_beta_evidence.py`（PF-10 的文件级声明；本面把「恰好一个节点声明检索方式」那条守护改成新事实，并补 keyword_only 的三态守护）。 **未登记进本面 paths 的理由与上面一致**：登记处 loader 拒绝两个面声明同一条路径。"
            "**2026-10-24 #576 跨面借用两条（owner 不变，故不声明进本面 paths；重复声明会被登记处 loader 直接拒绝）**：#576（PR #589，已于 2026-10-24 合入 main，合并提交 a9374cab）把前端租户角色的中文说法收口为唯一来源（新增 frontend/src/i18n/tenantRoleLabel.ts 与它的守护测试；本面已拥有的 auditActorRoleLabel.ts 改为引用它）。该 PR 另改了两个**别的面拥有**的文件，且只做**维度标注**（不改行为、不改判定）：① **PF-4** 拥有的 frontend/src/views/lingdoc/ProjectMembers.vue（项目治理角色）；② **PF-13** 拥有的 frontend/src/views/lingdoc/firstPassEntryState.ts（课题角色）。这两条与租户角色词表是**不同维度**，按 #535 段二的裁定口径不得并入 A ⇒ 本面**借用**这两条路径，owner 不变。"
            "**2026-10-24 #608 跨面借用（owner 不变，故不声明进本面 paths；重复声明会被登记处 loader 直接拒绝）**：#608（F01-11 采纳节点按服务端 HTTP 契约补齐确认载荷与工作副本声明，PR #612 已合入 main） 改动了下列**别的面拥有**的文件，本面**借用**它们、owner 不变：`docs/08-本轮实施方案/contracts/openapi.json`（docs/08-本轮实施方案/contracts/openapi.json）。 **2026-10-24 #608 跨面借用（owner 不变，故不声明进本面 paths；重复声明会被登记处 loader 直接拒绝）**：#608（F01-11 采纳节点按服务端 HTTP 契约补齐确认载荷与工作副本声明，PR #612 已合入 main） 改动了下列**别的面拥有**的文件，本面**借用**它们、owner 不变：`docs/08-本轮实施方案/contracts/workflow.json`（docs/08-本轮实施方案/contracts/workflow.json）。 **2026-10-24 #608 跨面借用（owner 不变，故不声明进本面 paths；重复声明会被登记处 loader 直接拒绝）**：#608（F01-11 采纳节点按服务端 HTTP 契约补齐确认载荷与工作副本声明，PR #612 已合入 main） 改动了下列**别的面拥有**的文件，本面**借用**它们、owner 不变：`internal/lingdoc/candidateadoption/adoption_confirmation_http_test.go`（internal/lingdoc/candidateadoption/）；`internal/lingdoc/candidateadoption/adoption_contract_body_http_test.go`（internal/lingdoc/candidateadoption/）。 **2026-10-24 #590 跨面借用（owner 不变，故不声明进本面 paths；重复声明会被登记处 loader 直接拒绝）**：#590（材料分块与坐标逐字自洽 ＋ 分块与 embedding 解耦，已于 2026-10-24 合入 main，合并提交 781295c7） 改动了下列**别的面拥有**的文件，本面**借用**它们、owner 不变：`internal/container/lingdoc.go`（internal/container/lingdoc.go）。 **2026-10-24 #590 跨面借用（owner 不变，故不声明进本面 paths；重复声明会被登记处 loader 直接拒绝）**：#590（材料分块与坐标逐字自洽 ＋ 分块与 embedding 解耦，已于 2026-10-24 合入 main，合并提交 781295c7） 改动了下列**别的面拥有**的文件，本面**借用**它们、owner 不变：`scripts/lingdoc_mock/run_f01.py`（scripts/lingdoc_mock/run_f01.py）。 **2026-10-24 #592 跨面借用两条（owner 不变，故不声明进本面 paths；重复声明会被登记处 loader 直接拒绝）**：#592（PR #601，放行判定不得产生假 PASS：人工断言未判定、契约里没跑到的 checks、受控窗口核验接入放行链）改动了下列**别的面拥有**的文件，本面**借用**它们、owner 不变：`scripts/acceptance/beta_evidence.py`（PF-10 的文件级声明）、`scripts/ci/test_lingdoc_beta_evidence.py`（PF-10 的文件级声明）；另 `scripts/lingdoc_mock/controlled_window_check.py` 是**本面自有**路径（PF-14），owner 不变。**本面自有路径的语义变化**：受控窗口的 `retrieval` 声明由两态变三态（`keyword_only` / `vector_required` / `undeclared`），未知声明**被点名拒绝**，`vector_required` 在受控窗口恒为 NOT RUN，窗口出现 embedding 行则整窗失败，两本账本**读不到**则为 NOT RUN（不得推断为零调用或 PASS）。 **2026-10-24 #593 跨面借用（owner 不变，故不声明进本面 paths；重复声明会被登记处 loader 直接拒绝）**：#593（PR #595，检索侧的受控 profile 策略拒绝保留自己的错误形态 ＋ D14 的权威常量，已于 2026-10-24 合入 main，合并提交 5a3e3793）改动了下列**别的面拥有**的文件，本面**借用**它们、owner 不变：`internal/clienterror/catalog.go`（internal/clienterror/catalog.go）；`internal/lingdoc/generation/client_form_contract_test.go`（internal/lingdoc/generation/）；`internal/lingdoc/generation/http_test.go`（internal/lingdoc/generation/）；`internal/lingdoc/generation/service.go`（internal/lingdoc/generation/）；`internal/lingdoc/generation/source_status_vocabulary_guard_test.go`（internal/lingdoc/generation/）；`internal/lingdoc/workspace/generation.go`（internal/lingdoc/workspace/generation.go）。"
            "**2026-10-25 #654 认领三条新路径（owner 是本面，loader 不接受重复声明）**：#654（PR #654，"
            "合并提交 5ed7ae06，独立复核 PASS、门禁 22 行 0 fail）新增了零额度重判路径的三条文件："
            "`scripts/lingdoc_mock/rejudge_report.py`（只重算人工判定层，机器事实逐字节保留、run_id 不变）、"
            "`scripts/lingdoc_mock/test_rejudge_report.py`（26 条用例）、"
            "`scripts/ci/test_lingdoc_rejudge_report.py`（7 条守护）。该 PR **未改动别的面拥有的路径**。"
            "**口径备注（管理会话 2026-10-25 裁决）**：重算的键是**人工判定层三个**"
            "（manual_assertions / manual_decisions / provider_semantics_status），不是两个——"
            "manual_decisions 本就是跑批器自己产出的报告键，属于人工层；实现与该口径一致。"
            "**该 PR 只证明路径可用，不构成放行证据**（真实人工判定尚不存在，PASS 档位全部来自合成数据）。"
            "依据票：#654（PR #654）、#633（人工判定录入通道）、#490（放行结论表）。"
            "**2026-10-25 #649（服务端权威人工判定审计）**："
            "跑批器（#633）要能核验一条人工判定为 server_attested，服务端就得能产出一条"
            "以 audit_id 为键、**顶层**带齐那七个可核验字段（actor / authorization / channel /"
            "occurred_at / assertion_id / run_id / evidence_ref）且与判定记录**逐字节相等**的记录。"
            "**本面**认领该能力的全部路径：领域层（internal/lingdoc/workspacecore/manual_judgement_audit.go，"
            "审计行落 lingdoc_project_audits、action=manual.judgement、details 顶层扁平；判定结果落"
            "lingdoc_g5_records、kind=manual_judgement、id=sha256(run_id+\"\\x00\"+assertion_id)；两者同一写事务，"
            "先判定结果后审计行，任一失败整体回滚；**不需要迁移**）、HTTP 接缝（两个端点 + 窄端口）、"
            "组合根显式交接（internal/container/lingdoc.go）、路由注册接缝（internal/router/router.go）、"
            "以及跑批器读端认得服务端信封（scripts/lingdoc_mock/manual_decision_audit.py，**只拆信封、不动判定语义**）。"
            "**关键事实**：actor/authorization/channel/occurred_at/audit_id **物理上无处可填**——领域层的"
            "ManualJudgementInput 只有 5 个调用方字段；channel 对外恒为 unverified_channel（服务端只能证明"
            "凭据类别，UI 点击与脚本重放同属 user_session 凭据 ⇒ 结构上无法区分 ⇒ **宁可低估不可高估**），"
            "渠道两层语义在附加键 channel_class / channel_ui_origin / channel_automation（后两者恒 unprovable）。"
            "**跨语言端到端已跑通**：真实服务端导出信封 → 真实跑批器 → server_attested / passed / verified。"
            "**残留登记**：① 审计行 decision 列复用了 verdict 词汇（passed/held/rejected），而仓内其它行的"
            "同名列语义是 allow/deny —— 全仓无任何查询按该列过滤 ⇒ 当前无功能影响，verdict 另有顶层键备份，"
            "改它要动列语义或迁移 ⇒ 登记不改；② 审计完整性硬闸门在生产路径不可达（details 由已校验的非空值自建）"
            "⇒ 纵深防御；③ 批次绑定是子串判定（strings.Contains(evidence_ref, run_id)），与跑批器同口径。"
            "**跨面移交（本面按登记处约定单向取得 owner，其余面的路径不变）**：internal/container/lingdoc.go"
            "（原属 PF-7：组合根要把窄端口显式交给 router，否则端点在生产装配下不可调）、"
            "docs/08-本轮实施方案/contracts/openapi.json（原属 PF-4：新增两个端点与五个 schema）、"
            "scripts/ci/test_lingdoc_manual_decision_audit.py 与 scripts/lingdoc_mock/manual_decision_audit.py"
            "（原属 PF-10：跑批器读端认得服务端信封 + 守护扩充）。依据票：#649（PR #659 ＋ HTTP 侧 PR）、#633。"),
    },
    {
        "name": "PF-8 G6 告警规则与运行控制回退",
        "issue": 382,
        "issues": (145, 146),
        "status": "done",
        "claimed_by": "",
        "paths": (
            # #382-A/#382-B：告警规则的可读回与可停用（独立新文件）。
            ("internal/lingdoc/workspacecore/runtime_alert_rule.go", "file"),
            ("internal/lingdoc/workspacecore/runtime_alert_rule_test.go", "file"),
            # #383：运行控制回退（验收用例独立成文件）。
            ("internal/lingdoc/workspacecore/runtime_rollback_test.go", "file"),
            # #382-C：授权判定可观测（独立新文件）。
            ("internal/lingdoc/workspacecore/runtime_observation.go", "file"),
            ("internal/lingdoc/workspacecore/runtime_observation_test.go", "file"),
            # 拒绝的判定要带上「拒了什么能力」，所以授权策略包一层错误。
            ("internal/lingdoc/workspacecore/permission_policy.go", "file"),
            # 回退目标挂在控制项上，白名单加 rollback，事件加 capability 字段。
            ("internal/lingdoc/workspacecore/runtime_control.go", "file"),
            # 窄口多两个方法。
            ("internal/lingdoc/workspacecore/context_ports.go", "file"),
            # 操作管线的观察 holder 随操作走。
            ("internal/lingdoc/workspacecore/operation_pipeline.go", "file"),
            # 两个新端点。
            ("internal/lingdoc/workspace/impact_routes.go", "file"),
            # #384：G6 前端独立化。端点客户端各有自己的模块，界面有自己的面板。
            ("frontend/src/api/lingdoc/runtimeControl.ts", "file"),
            ("frontend/src/api/lingdoc/runtimeControl.test.ts", "file"),
            ("frontend/src/api/lingdoc/trialWindow.ts", "file"),
            ("frontend/src/api/lingdoc/trialWindow.test.ts", "file"),
            ("frontend/src/views/lingdoc/RuntimeOpsPanel.vue", "file"),
            ("frontend/src/views/lingdoc/RuntimeOpsPanel.test.js", "file"),
            # G6 段从这三个文件里搬走了，所以它们跟着本面的路径集走
            # （登记处的重叠检查不看 status，done 的面仍然占着路径，
            # 必须显式移交 —— PF-1 已交面，只保留自己的方案档）。
            ("frontend/src/api/lingdoc/mainlineChanges.ts", "file"),
            ("frontend/src/api/lingdoc/mainlineChanges.test.js", "file"),
            ("frontend/src/views/lingdoc/ImpactOpsPanel.vue", "file"),
            ("frontend/src/views/lingdoc/ImpactOpsPanel.test.js", "file"),
            ("frontend/src/views/lingdoc/runtimeMetrics.ts", "file"),
            ("frontend/src/views/lingdoc/runtimeMetrics.test.ts", "file"),
            # 契约产物与两份方案档。
            ("docs/08-本轮实施方案/G6-运行控制后端补齐-20261023.md", "file"),
            ("docs/08-本轮实施方案/G6-运维面板独立化-20261023.md", "file"),
            ("docs/08-本轮实施方案/README.md", "file"),
            # 2026-10-24: the shared path diagram moved on to PF-7 (#460),
            # which rewrote the face table and the claim-status section --
            # whoever changes the face table owns it.  The handover record is
            # in PF-7's summary.
            # 2026-10-23：从 PF-2（done）与 PF-4（done）接过来的两条路径。
            # 本面在 PR #446 里给这两处各加了一条注释（说明两个新 G6 端点
            # 不改变这两张路由数量快照），而登记处的重叠检查是路径级的
            # ——改了别面的路径就必须显式移交。上游
            # 「下一批施工面切分方案.md」§7.3 把这两条记为本 PR 的未申报改动，
            # 这里补齐申报。
            ("internal/router/lingdoc_routes_test.go", "file"),
            ("internal/container/lingdoc_test.go", "file"),
        ),
        "resolves": (),
        "summary": (
            "本面一次交付 #382 / #383 两张后端票，另加 #384 的前端独立化。"
            "#382：告警规则可读回（GET runtime/alert-rules）与可停用/可更新"
            "（POST runtime/alert-rules/:ruleId/actions），停用后的规则不再产生告警；"
            "授权拒绝落成 permission/deny 运行事件（只进事件流，不触发告警 —— "
            "告警规则的 pause 动作会直接暂停整个课题）。"
            "#383：运行控制新增 rollback 动作，回退经既有 reverseChangeSet 领域入口执行，"
            "停止新写入并记录回退目标；「切回已验证配置」这一半不在本面范围。"
            "#384：G6 前端从 G3 的变更面板里拆成自己的 RuntimeOpsPanel，"
            "端点客户端搬进 runtimeControl.ts 与 trialWindow.ts。"
            "**#327 已由 PR #389 在 main 上完成并关票，本面不重复交付**："
            "AuditEvent 的 target_version / project_revision 两列与"
            "读类审计失败接告警源（EmitAuditFailureAlert）都在 main 上，"
            "本面只在自己的实现里删掉重复的那一半（塞 details_json 的版本、"
            "recordAccessAuditFailOpen 接缝、audit/failed 观测）。"
            "**本面确实改了下列已被别的面占有的文件**（登记处的重叠检查是路径级的，"
            "改了别面的路径就必须显式移交）。"
            "**§5 要求面 summary 写清的那一句——本面从哪个面接了哪几条路径、"
            "依据哪张票、接的顺序**："
            "① 先接 PF-3（done，#327）的 1 条 internal/lingdoc/workspacecore/runtime_control.go，"
            "依据 #383（运行控制回退：回退目标挂在控制项上、白名单加 rollback，"
            "与 #382 的告警规则读回落在同一个文件）⇒ 永久留在本面；"
            "② 再接 PF-4（done，#328）的 4 条 service_facade.go / typed_contracts.go / "
            "context_ports_test.go / contracts/openapi.json，依据 #382/#383 的接线"
            "（授权拒绝观测的两个收敛点、窄口方法集登记、契约产物），"
            "经维护者裁决「PF-8 先走」⇒ **借用**，本面合入 main 后在交面时归还 PF-4；"
            "③ 接着接 PF-1（done，#325）的 5 条 ImpactOpsPanel.vue / ImpactOpsPanel.test.js / "
            "runtimeMetrics.ts / runtimeMetrics.test.ts / mainlineChanges.test.js，"
            "依据 #384（G6 段从 ImpactOpsPanel.vue 与 mainlineChanges.ts 里搬走，"
            "契约断言随之迁移）⇒ 永久留在本面"
            "（口径表也随后端补的 permission/deny 观测演进）；"
            "④ 最后接 PF-2（done，#326）的 internal/router/lingdoc_routes_test.go 与 "
            "PF-4 的 internal/container/lingdoc_test.go，依据上游"
            "「下一批施工面切分方案.md」§7.3 对本 PR 未申报改动的点名"
            "（本面给这两处各加了一条注释，说明两个新 G6 端点不改变那两张路由数量快照）"
            "⇒ **一次性移交，单向**，旧面不再声称拥有。"
            "**移交与归还是两件事**：①③④ 是单向移交（改了别面的路径就归你），"
            "② 是借用（PF-8 只是用它落接线，接线落地即还）。"
            "**2026-10-23 交面**：PF-8 随 PR #446 合入 main，本面 status 改 done，"
            "② 的四个路径归还 PF-4，本面路径集 30 → 28 条。"
            "**本面还改了一条不在任何路径集里的路径**："
            "docs/08-本轮实施方案/PF-3-审计告警接线与AuditEvent字段.md"
            "（PF-3 的方案档）——本面给它补了一段「2026-10-23 后续状态另标」"
            "（PF-3 的路径集当时 18 → 17 条，让出 runtime_control.go 给本面）；"
            "该文件属方案档性质，本面不把它登记进路径集，只在这里记录改动事实。"
            "**frontend/src/views/lingdoc/Workspace.vue 是所有前端面的集成点"
            "（保留路径，不得被任一面独占）**：本面在这里加 G6 面板的挂载位，"
            "按「共享维护点」约定不登记进任何面的路径集。"
            "**为什么动了它**：#384 的目标就是「值班人员在 Workspace 里有自己的入口位，"
            "不必先进 G3 面板再切 tab」——入口位只能落在这个文件里，不落在它就等于没做；"
            "**授权**：维护者在本轮显式授权（覆盖了 #384 票面原文"
            "「未获授权前不要改 Workspace.vue」）；"
            "**冲突预告**：PF-6（#330）将来集成时也要改这个文件，两边都改时冲突会显式报出来；"
            "上游新加的原生依赖边 #384 blocked_by #450 保留不动，"
            "它留给 #450（52-Spec 工作台入口与面板路由模型）的事实是："
            "Workspace.vue 里已经有 'operations' 这个成员，"
            "#450 的「面板可注册」模型要把它收编进去，而不是另起一套。"
            "**Spec #145 与 #146 保持开放**：本面只交付各自的一部分"
            "（「切回已验证配置」等仍需领域写路径支持）。"
            "2026-10-23 由 magician336 认领并完成；"
            "完成后本面不保留 claimed_by（登记处只允许 claimed 状态带认领人）。"
            "**2026-10-24 交面时把共享路径图移交 PF-7**"
            "（docs/08-本轮实施方案/G1-G7-Spec-完成路径图.md）——依据票 #506（PF-7 交面）："
            "PF-7 改了本图的面表与认领状态段，按「谁改面表谁拥有它」⇒ 本面让出 "
            "⇒ 本面路径集 28 → 27 条。本图里本面自己的内容原样保留，不受这次移交影响。"
        ),
    },
    {
        "name": "PF-7 审计链完整性",
        "issue": 460,
        "issues": (130,),
        "status": "done",
        "claimed_by": "",
        "paths": (
            # ① 从 PF-3 永久移交（3）：#415 要给 AuditEvent 补字段并让行模型与表列一致。
            ("internal/lingdoc/workspacecore/model.go", "file"),
            ("internal/lingdoc/workspacecore/service.go", "file"),
            ("internal/lingdoc/membership/fixture_test.go", "file"),
            # ② 无主直接拥有（1）：workspaceAuditSink.Record 的实现所在。
            # ⚠️ 借用记录在本面的 summary 里（PF-10 于 2026-10-23 借用本文件装模型调用账本，
            # 合入 main 后归还）；**路径所有权仍归本面**。
            ("internal/container/lingdoc.go", "file"),
            # ③ 本面新增迁移（4）：#415 的字段要真落库，两方言各一套。
            ("migrations/sqlite/000054_lingdoc_audit_decision_fields.up.sql", "file"),
            ("migrations/sqlite/000054_lingdoc_audit_decision_fields.down.sql", "file"),
            ("migrations/versioned/000133_lingdoc_audit_decision_fields.up.sql", "file"),
            ("migrations/versioned/000133_lingdoc_audit_decision_fields.down.sql", "file"),
            # ④ #454 的两个守护（2）：模型字段集合 == 表列集合。
            # Go 守护落 internal/database/ 是 56-Spec 决策 3 指定的位置
            # （与被测的迁移/schema 同属一处）。
            ("internal/database/audit_event_columns_test.go", "file"),
            # ④ 的 Python 守护（1）：形态扫描（有没有手抄 CREATE TABLE 列清单、
            # 硬编码迁移文件名、手改的迁移版本常量）。名字不带编号（命名登记表规则）。
            ("scripts/ci/test_lingdoc_fixture_schema_source.py", "file"),
            # ⑤ 施工中追加的夹具与用例（9）：本面在它们上面落 #415 的字段断言、
            # #414 的 sink 返错断言，以及共享路径图的同步。
            ("internal/lingdoc/workspacecore/audit_decision_fields_test.go", "file"),
            ("internal/container/workspace_audit_sink_test.go", "file"),
            # ⑤ 从 PF-3 永久移交（4）：#415 要让迁移子集与夹具跟着补齐。
            ("internal/lingdoc/workspacecore/pr150_migration_history_test.go", "file"),
            ("internal/lingdoc/workspace/audit_event_table_fixture_test.go", "file"),
            ("internal/lingdoc/workspace/save_chapter_policy_test.go", "file"),
            ("internal/container/lingdoc_ruleset_delivery_e2e_test.go", "file"),
            # ⑤ 从 PF-4 永久移交（1）：Owner 路由断言文件（#415 的读侧角色过滤）。
            ("internal/router/routes_lingdoc_owner_test.go", "file"),
            # ⑤ 无主直接拥有（1）：#415 的读侧角色过滤断言落在这里。
            ("internal/router/lingdoc_integration_test.go", "file"),
            # ⑤ 从 PF-8 移交（1）：共享路径图——谁改面表谁拥有它。
            ("docs/08-本轮实施方案/G1-G7-Spec-完成路径图.md", "file"),
        ),
        "resolves": (),
        "summary": (
            "**2026-10-24 交面**（票 #506，本 PR 只做交面）：PR-1（代码与测试 = PR #503）已于 "
            "2026-10-23 合入 main（合并提交 81d0b099），本面随之登记为 done、claimed_by 清空"
            "（完成态不保留认领人；**认领与完成的人是 magician336**）。"
            "本面在 main 上此前记的是 status=available —— PR-1 只含代码与测试、不碰登记处，"
            "所以交面在本 PR 里一次写完（status → done + claimed_by 清空）。"
            "**接的顺序与依据票（#506 要求的 §5 形态）**："
            "① 从 PF-3 永久接过 3 条（workspacecore/model.go、workspacecore/service.go、"
            "membership/fixture_test.go），依据票 #415，不归还（PF-3 已交面且不再需要它们）"
            "⇒ PF-3 路径集 17 → 14 条；"
            "② 无主直接拥有 1 条（internal/container/lingdoc.go），依据票 #414"
            "（workspaceAuditSink.Record 的实现所在）；"
            "③ 本面新增迁移 4 条（migrations/sqlite/000054_* 与 migrations/versioned/000133_*），"
            "依据票 #415；"
            "④ #454 的两个守护 2 条（internal/database/audit_event_columns_test.go 与 "
            "scripts/ci/test_lingdoc_fixture_schema_source.py），依据 56-Spec 决策 3"
            "（守护落 internal/database/，名字不带编号）；"
            "⑤ 施工中追加的夹具与用例 9 条，依据票 #415 / #414 —— 其中 pr150_migration_history_test.go、"
            "audit_event_table_fixture_test.go、save_chapter_policy_test.go 与 "
            "lingdoc_ruleset_delivery_e2e_test.go 从 PF-3 永久移交（⇒ PF-3 14 → 10 条）、"
            "routes_lingdoc_owner_test.go 从 PF-4 永久移交（⇒ PF-4 41 → 40 条）、"
            "lingdoc_integration_test.go 无主直接拥有、"
            "docs/08-本轮实施方案/G1-G7-Spec-完成路径图.md 从 PF-8 移交（谁改面表谁拥有它，"
            "⇒ PF-8 28 → 27 条）。"
            "**交付**：#414（平台事务外通道的审计写失败必须能被感知）、"
            "#415（AuditEvent 的五个决策事实成为一等列并真正落库）、"
            "#454 的两个守护（Go 守护落 internal/database/、Python 守护落 scripts/ci/）。"
            "**交付 Spec 是既有的 #130，不新开** —— 本面只交付了 #414 / #415 两个决策点，"
            "#130 的其余验收点仍未交付（Spec #130 保持开放）。"
            "**迁移编号 000054 / 000133 是本面的，不许让出**（PF-10 已改用 000055 / 000134 并合入 main）"
            "—— 登记处棘轮（test_lingdoc_parallel_consistency 的"
            "test_the_committed_manifest_does_not_regress_from_main）禁止本分支删掉 main 已记录的路径，"
            "所以「让号」只能由**尚未合并**的那一方改。**切分方案 §2.2 曾给 PF-8 预留 000055 / 000134**"
            "（回退记录落库用）——PF-8 已 done 且回退未落库 ⇒ 那个预留作废，现由 PF-10 使用；"
            "**PF-8 将来若要落库须重新取号**。"
            "**保留路径 frontend/src/views/lingdoc/Workspace.vue 本面没有动。**"
            "**#454 与本面合并的机制理由**：56-Spec 决策 3 已指定守护落 internal/database/，"
            "若 #415 另在 workspacecore/ 写一份，同一条不变量就有两个 module 各写一遍（ch05 信息泄漏）。"
            "**`internal/container/lingdoc.go` 被 PF-10 借用（2026-10-23）**：该路径归本面（#414 的 "
            "AuditSink 实现就在那里），PF-10（#465）在组合根装模型调用账本时改了它一处。"
            "按「owner 保持拥有、借用方落地后归还」的先例（PF-8 借 PF-4 的四个路径，落地后归还），"
            "**该路径仍归本面，PF-10 合入 main 后归还**；PF-10 侧已记同一笔借用。"
            "**残留（逐条保持开放，解除条件见票面 #506）**："
            "① #454 的更大范围不在本面 —— 把**全部**手抄 schema 的夹具迁成「应用生产迁移」"
            "（逐个迁移夹具，形态扫描基线随之变短，守护会挡住「基线变长」）；"
            "② 夹具的迁移子集仍手抄（本面补的是「必须带上本面新增的迁移」）；"
            "③ internal/container 的用例本地跑不了（缺 sqlite3.h / duckdb bindings，**基线同因**）⇒ CI 上跑；"
            "**2026-10-24 #590 跨面借用（owner 不变，故不声明进本面 paths；重复声明会被登记处 loader 直接拒绝）**：#590（材料分块与坐标逐字自洽 ＋ 分块与 embedding 解耦，已于 2026-10-24 合入 main，合并提交 781295c7） 改动了下列**别的面拥有**的文件，本面**借用**它们、owner 不变：`internal/container/lingdoc.go`（internal/container/lingdoc.go）。 ④ PostgreSQL 方言本地不跑（无 LINGDOC_TEST_POSTGRES_DSN）⇒ CI 的 integration 作业带 PG service。"
        ),
    },
    {
        "name": "PF-9 Owner 冻结归因",
        "issue": 461,
        "issues": (127,),
        "status": "claimed",
        "claimed_by": "magician336",
        "paths": (
            # 从 PF-4 永久移交（3）：#420 要改 Owner 专属能力的枚举与判定。
            ("internal/lingdoc/workspacecore/owner_unavailable.go", "file"),
            ("internal/lingdoc/membership/policy.go", "file"),
            ("internal/lingdoc/membership/results.go", "file"),
            # 从 PF-4 永久移交（2）：两张 HTTP 断言文件——本面在它们上面加断言。
            ("internal/router/routes_lingdoc_owner_read_outlet_test.go", "file"),
            ("internal/router/routes_lingdoc_owner_409_test.go", "file"),
            # 本面新增（3）：入口能力的枚举登记处 + 同名测试 + 旁路断言。
            ("internal/lingdoc/workspacecore/owner_capability_registry.go", "file"),
            ("internal/lingdoc/workspacecore/owner_capability_registry_test.go", "file"),
            ("internal/lingdoc/membership/owner_bypass_test.go", "file"),
        ),
        "resolves": (),
        "summary": (
            "本面从 PF-4 永久接过三条路径（workspacecore/owner_unavailable.go、"
            "membership/policy.go、membership/results.go）——依据票 #420：五项 Owner 专属动作"
            "在读侧共用 manage，需要一个显式的「入口能力 → 读出口能力」对应关系；"
            "#421 补写侧更早鉴权处的归因。2026-10-23 永久移交，**不归还**（PF-4 已交面）"
            "⇒ PF-4 路径集 47 → 44 条。owner_unavailable 的判定语义已由 PF-4 落地并冻结"
            "（owner_unavailable.go + 两处出口已在 main）⇒ 本面补的是已定义行为的接缝，"
            "不是新设计。交付 Spec 是既有的 #127，不新开。"
            "已有原生边 #420 blocked_by #127、#421 blocked_by #127（③ 档验收未满足）："
            "依赖方向正确且上游契约已冻结 ⇒ 本面今天就能开工。"
            "**2026-10-23 由 magician336 认领**，同日开工。"
            "**开工时实测更正了路径集里的三处前缀（异议已记在票面 #461 与 PR 正文）**："
            "指派的三条是 internal/lingdoc/router/…，而 internal/lingdoc/router/ "
            "**这个目录不存在**——真实位置是 internal/router/，那三个文件（routes_lingdoc.go、"
            "routes_lingdoc_owner_read_outlet_test.go、routes_lingdoc_owner_409_test.go）"
            "由 PF-4 拥有。修正前缀后与 PF-4 撞车 ⇒ 按登记处「路径在两面之间移交是合法的、"
            "单向的」与棘轮「done 面可释放路径」：本面永久接过**两张断言文件**"
            "（本面在它们上面加断言），**internal/router/routes_lingdoc.go 留在 PF-4**"
            "（本面不改它）⇒ PF-4 路径集 44 → 42 条，本面 9 → 8 条。"
            "三条错前缀路径是「悬空登记」（基线里只有声明、没有实体文件），"
            "按基线模块的新判据撤回它不算路径损失 ⇒ 棘轮不拦。"
        ),
    },
    {
        "name": "PF-13 G4 模板目录与版本治理",
        "issue": 248,
        "issues": (139,),
        "status": "done",
        "claimed_by": "",
        "paths": (
            ("docs/08-本轮实施方案/Spec-139-模板目录验收矩阵.md", "file"),
            ("docs/08-本轮实施方案/T139-验证报告.json", "file"),
            ("frontend/src/components/TemplateGovernancePanel.test.ts", "file"),
            ("frontend/src/components/TemplateGovernancePanel.vue", "file"),
            ("frontend/src/views/lingdoc/firstPassEntryState.test.ts", "file"),
            ("frontend/src/views/lingdoc/firstPassEntryState.ts", "file"),
            ("internal/lingdoc/templategovernance/README.md", "file"),
            ("internal/lingdoc/templategovernance/audit_history.go", "file"),
            ("internal/lingdoc/templategovernance/catalog.go", "file"),
            ("internal/lingdoc/templategovernance/catalog_postgres_test.go", "file"),
            ("internal/lingdoc/templategovernance/catalog_test.go", "file"),
            ("internal/lingdoc/templategovernance/model.go", "file"),
            ("internal/lingdoc/templategovernance/storage.go", "file"),
            ("internal/lingdoc/workspace/catalog_http_parity_test.go", "file"),
            ("internal/lingdoc/workspace/client_form_convergence_test.go", "file"),
            ("internal/lingdoc/workspace/template_governance_adapter_postgres_test.go", "file"),
            ("internal/lingdoc/workspace/template_governance_routes.go", "file"),
            ("internal/lingdoc/workspace/template_governance_routes_test.go", "file"),
            ("internal/lingdoc/workspace/template_governance_runtime.go", "file"),
            ("internal/lingdoc/workspacecore/core_governance_ports.go", "file"),
            ("internal/lingdoc/workspacecore/ruleset_source_policy_test.go", "file"),
            ("internal/lingdoc/workspacecore/semantic_delta_full_test.go", "file"),
            ("internal/lingdoc/workspacecore/service_facade.go", "file"),
            ("internal/lingdoc/workspacecore/tenant_permission_port.go", "file"),
        ),
        "resolves": (),
        "summary": (
            "G4 模板目录与版本治理（Spec #139）的施工面，票面即 PR #248 本身。"
            "**2026-10-23 交面**（本 PR 只做交面）：PR-1（代码、测试、契约产物与验收证据 = PR #248）已于 2026-10-23 合入 main（合并提交 19d9df9c），本面随之登记为 done，"
            "claimed_by 清空（完成态不保留认领人，认领的人记在 summary 里）；本面在登记处登记 24 条路径。"
            "**面号 PF-13**：最初按自取序号登记为 PF-10，与 #465「PF-10 Beta 首版验收证据生产」（PR #474）撞名——登记处 loader 拒绝同名面（duplicate face name），"
            "两个同名面同时进 main 会让登记处加载失败；按管理会话维护的面号分配表改为 PF-13。"
            "本面按 main 重做：#248 的提交里，段三改名（T369 弱语义名清理、T365 编号命名收口）与 clienterror 形态迁移（T257/#262）同时落在本面改动的文件上，"
            "逐提交 rebase 会与这些改名逐个相撞且丢掉 6 个提交的语义，因此以 main 为基线重做这一面：取 main 的实现与命名，再把 #248 的语义增量补上去。"
            "落基做了十二遍（main 在本会话内前移十一次）：从 main@041ebe0f（语义重做起点）一路到 main@3c65618b；"
            "其中 main@05fa6b6f（PR #446 合入 PF-8；service_facade.go / typed_contracts.go / context_ports_test.go / contracts/openapi.json 由 PF-8 还回 PF-4，"
            "冲突面 5 个文件：登记处、contracts/openapi.json、workspacecore/context_ports_test.go、workspacecore/service_facade.go、Workspace.vue）、"
            "main@a91e3697（PR #474 合入 #465 的 PF-10 Beta 面——**#474 把 #465 的 PF-10 面正式登记进 main，实证了本面改名的必要性**：若本面仍叫 PF-10，合并后 main 会有两个同名面，登记处 loader 直接拒绝加载）、"
            "main@6adc5b2a（PR #525 合入验收判定口径 v1.1 与 DEFERRED_MANUAL 协议）。"
            "每遍只有基线在动：**产品与测试候选始终是 0131b8eb（tree 0fbb6fa9）**，差别只在基线与随之而来的登记处。"
            "本面从 PF-4 接了 1 条路径（service_facade.go，永久移交），从 PF-5 接了 1 条（template_governance_runtime.go，单向移交）；PF-8 不再是移交来源。"
            "**另有三条路径的 owner 归 PF-11**（票 #466 / PR #149；管理会话裁决）：internal/lingdoc/workspacecore/context_ports_test.go（本面借用，用途：窄口登记表，补上下文端口断言）、docs/08-本轮实施方案/contracts/openapi.json（本面借用，用途：加模板目录相关接口）、internal/lingdoc/workspace/template_governance_adapter_test.go（**owner 归 PF-11**：#149 与本面各自新建，#149 侧按本面版本合成——本面版本是超集，另加 3 个用例；合入时内容以 #149 的合成为准）。这三条**不列进本面路径集**。"
            "**这三条在当前登记处里的实际持有者**：#149 已于 2026-10-23 合入 main（合并提交 4b716a1f），但 PF-11 是 done 却**尚未登记进登记处**（它自己的交面票还没落）⇒ context_ports_test.go 与 openapi.json 仍由 PF-4 持有（否则要么重复声明被 loader 拒绝，要么路径无人拥有），template_governance_adapter_test.go 当前无主；PF-11 自己的交面把 PF-11 登记进来时，再把这三条移过去。"
            "frontend/src/views/lingdoc/Workspace.vue 是保留路径（所有前端面的集成点），"
            "本 PR 触及它但不独占它，按约定不登记进任何面的路径集。"
            "本面自建的验收证据（Spec-139 验收矩阵与 T139-验证报告.json）也登记在本面路径集里：改了不申报是 #441 §7.3 记的偏差。"
            "**PR #248 相对 main 的净差集是 28 个文件** = 本面登记 24 条 + owner 归 PF-11 的 3 条（context_ports_test.go、contracts/openapi.json、template_governance_adapter_test.go）+ 保留路径 1 条（Workspace.vue）；"
            "登记处（scripts/lingdoc_parallel_surfaces.py）与共享路径图（docs/08-本轮实施方案/G1-G7-Spec-完成路径图.md）**不在 #248 的差集里**，它们落在本交面 PR（#508）——路径图登记在 PF-8 名下，本 PR 按 #441 §7.3「改了不申报要记录」处理：只改内容（补本面行），不夺所有权。"
            "依据票：#441（下一批面怎么切）、#248（PR）、#139（Spec）。"
            "**2026-10-24 #576 跨面借用一条（owner 仍是本面，不变更归属）**：#576（PR #589，合并提交 a9374cab）把前端租户角色的中文说法收口为唯一来源；其中 frontend/src/views/lingdoc/firstPassEntryState.ts（本面拥有）只被加了一处**维度标注**——说明该表是「课题角色」、不是租户角色词表。**行为未变、判定未变**，owner 仍在本面；PF-14 侧已把这条记为借用事实，两侧一致。"
        ),
    },
)


class ParallelSurfaceManifestError(ValueError):
    """Raised when the committed manifest is invalid."""


@dataclass(frozen=True)
class PathClaim:
    """One path a face owns."""

    path: str
    kind: str  # "file" or "dir"

    def overlaps(self, other: "PathClaim") -> bool:
        """Whether two path claims can designate the same file."""
        if self.kind == "file" and other.kind == "file":
            return self.path == other.path
        if self.kind == "file" and other.kind == "dir":
            return self.path == other.path or self.path.startswith(other.path.rstrip("/") + "/")
        if self.kind == "dir" and other.kind == "file":
            return other.overlaps(self)
        return (
            self.path == other.path
            or self.path.startswith(other.path.rstrip("/") + "/")
            or other.path.startswith(self.path.rstrip("/") + "/")
        )


@dataclass(frozen=True)
class ParallelFace:
    """One parallel unit of work."""

    name: str
    issue: int
    issues: tuple[int, ...]
    status: str
    claimed_by: str
    paths: tuple[PathClaim, ...]
    resolves: tuple[tuple[int, int], ...]
    summary: str


def _require_fields(entry: Mapping[str, Any], fields: Iterable[str], what: str) -> None:
    missing = [field for field in fields if field not in entry]
    if missing:
        raise ParallelSurfaceManifestError(f"{what}: missing field(s) {', '.join(missing)}")


def _require_text(value: Any, what: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise ParallelSurfaceManifestError(f"{what}: expected non-empty text")
    return value


def _face_from_record(entry: Any, index: int) -> ParallelFace:
    what = f"PARALLEL_FACES[{index}]"
    if not isinstance(entry, Mapping):
        raise ParallelSurfaceManifestError(f"{what}: expected a mapping")
    _require_fields(
        entry,
        ("name", "issue", "issues", "status", "claimed_by", "paths", "resolves", "summary"),
        what,
    )
    name = _require_text(entry["name"], f"{what}.name")

    issue = entry["issue"]
    if not isinstance(issue, int) or isinstance(issue, bool) or issue <= 0:
        raise ParallelSurfaceManifestError(f"{what}.issue: expected a positive integer")

    issues_raw = entry["issues"]
    if not isinstance(issues_raw, (list, tuple)) or not issues_raw:
        raise ParallelSurfaceManifestError(f"{what}.issues: expected a non-empty list")
    issues: list[int] = []
    for spec_issue in issues_raw:
        if not isinstance(spec_issue, int) or isinstance(spec_issue, bool):
            raise ParallelSurfaceManifestError(
                f"{what}.issues: expected integers, got {spec_issue!r}"
            )
        if spec_issue not in G_LINE_ISSUES:
            raise ParallelSurfaceManifestError(
                f"{what}.issues: #{spec_issue} is not a G1-G7 Spec issue"
            )
        issues.append(spec_issue)

    status = _require_text(entry["status"], f"{what}.status")
    if status not in FACE_STATUSES:
        raise ParallelSurfaceManifestError(
            f"{what}.status: {status!r} is not one of {', '.join(FACE_STATUSES)}"
        )

    claimed_by = entry["claimed_by"]
    if not isinstance(claimed_by, str):
        raise ParallelSurfaceManifestError(f"{what}.claimed_by: expected text")
    if status == "claimed" and not claimed_by.strip():
        raise ParallelSurfaceManifestError(
            f"{what}.claimed_by: a claimed face must name its claimant"
        )
    if status != "claimed" and claimed_by.strip():
        raise ParallelSurfaceManifestError(
            f"{what}.claimed_by: only a claimed face may name a claimant"
        )

    paths_raw = entry["paths"]
    if not isinstance(paths_raw, (list, tuple)) or not paths_raw:
        raise ParallelSurfaceManifestError(f"{what}.paths: expected a non-empty list")
    paths: list[PathClaim] = []
    for claim in paths_raw:
        if not isinstance(claim, (list, tuple)) or len(claim) != 2:
            raise ParallelSurfaceManifestError(
                f"{what}.paths: expected (path, kind) pairs"
            )
        path = _require_text(claim[0], f"{what}.paths")
        kind = _require_text(claim[1], f"{what}.paths")
        if kind not in ("file", "dir"):
            raise ParallelSurfaceManifestError(
                f"{what}.paths: kind {kind!r} is not 'file' or 'dir'"
            )
        paths.append(PathClaim(path=path, kind=kind))

    resolves_raw = entry["resolves"]
    if not isinstance(resolves_raw, (list, tuple)):
        raise ParallelSurfaceManifestError(f"{what}.resolves: expected a list")
    resolves: list[tuple[int, int]] = []
    for pair in resolves_raw:
        if not isinstance(pair, (list, tuple)) or len(pair) != 2:
            raise ParallelSurfaceManifestError(
                f"{what}.resolves: expected (blocker, blocked) pairs"
            )
        resolves.append((int(pair[0]), int(pair[1])))

    summary = _require_text(entry["summary"], f"{what}.summary")
    return ParallelFace(
        name=name,
        issue=issue,
        issues=tuple(issues),
        status=status,
        claimed_by=claimed_by,
        paths=tuple(paths),
        resolves=tuple(resolves),
        summary=summary,
    )


def _check_disjoint(faces: Sequence[ParallelFace]) -> None:
    """No two faces may own the same path."""
    for i, face in enumerate(faces):
        for other in faces[i + 1:]:
            for claim in face.paths:
                for other_claim in other.paths:
                    if claim.overlaps(other_claim):
                        raise ParallelSurfaceManifestError(
                            f"path {claim.path!r} is claimed by both "
                            f"{face.name!r} and {other.name!r}"
                        )


def _check_reserved(faces: Sequence[ParallelFace], reserved: Sequence[PathClaim]) -> None:
    """No face may own a reserved integration path."""
    for face in faces:
        for claim in face.paths:
            for reserved_claim in reserved:
                if claim.overlaps(reserved_claim):
                    raise ParallelSurfaceManifestError(
                        f"face {face.name!r} claims reserved path {claim.path!r}"
                    )


def validate_records(records: Sequence[Mapping[str, Any]]) -> tuple[ParallelFace, ...]:
    """Validate raw manifest records into faces, or raise."""
    faces = tuple(_face_from_record(entry, index) for index, entry in enumerate(records))

    seen: set[str] = set()
    for face in faces:
        if face.name in seen:
            raise ParallelSurfaceManifestError(f"duplicate face name {face.name!r}")
        seen.add(face.name)

    _check_disjoint(faces)
    _check_reserved(faces, tuple(PathClaim(path=p, kind=k) for p, k in RESERVED_PATHS))
    return faces


def load_and_validate(root: Path | None = None,
                      records: Sequence[Mapping[str, Any]] | None = None) -> tuple[ParallelFace, ...]:
    """Load the committed manifest and validate it."""
    return validate_records(PARALLEL_FACES if records is None else records)


def main() -> int:
    """Validate the committed manifest; exit non-zero when it is invalid."""
    # Face names and summaries are Chinese; a Windows console defaults to a
    # narrow code page and would print mojibake instead of the real names.
    for stream in (sys.stdout, sys.stderr):
        reconfigure = getattr(stream, "reconfigure", None)
        if reconfigure is not None:
            reconfigure(encoding="utf-8", errors="replace")
    try:
        faces = load_and_validate()
    except ParallelSurfaceManifestError as error:
        print(f"invalid parallel surface manifest: {error}", file=sys.stderr)
        return 1
    for face in faces:
        owner = face.claimed_by or "-"
        print(f"#{face.issue} {face.name}: {face.status} ({owner}) -> "
              f"{', '.join(p.path for p in face.paths)}")
    print(f"{len(faces)} parallel work faces validated")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
