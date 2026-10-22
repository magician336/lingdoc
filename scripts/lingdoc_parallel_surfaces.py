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
G_LINE_ISSUES = frozenset(range(123, 144)) | {145, 146} | {391, 393}

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
            ("frontend/src/views/lingdoc/ImpactOpsPanel.vue", "file"),
            ("frontend/src/views/lingdoc/runtimeMetrics.ts", "file"),
            ("frontend/src/views/lingdoc/runtimeMetrics.test.ts", "file"),
            ("frontend/src/api/lingdoc/mainlineChanges.test.js", "file"),
            ("frontend/src/views/lingdoc/ImpactOpsPanel.test.js", "file"),
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
            ("internal/router/lingdoc_routes_test.go", "file"),
            # The production composition's route-count assertion follows the
            # new endpoints: the review report endpoints are part of the
            # rewrite capability, so the composition serves four more routes.
            ("internal/container/lingdoc_test.go", "file"),
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
            "完成后本面不保留 claimed_by（登记处只允许 claimed 状态带认领人）。"
        ),
    },
    {
        "name": "PF-3 审计告警接线与 AuditEvent 字段",
        "issue": 327,
        "issues": (130,),
        "status": "done",
        "claimed_by": "",
        "paths": (
            ("internal/lingdoc/workspacecore/runtime_control.go", "file"),
            ("internal/lingdoc/workspacecore/model.go", "file"),
            ("internal/lingdoc/workspacecore/machine_read_grant.go", "file"),
            ("internal/lingdoc/workspacecore/service.go", "file"),
            ("internal/lingdoc/workspacecore/audit_failure_alert_test.go", "file"),
            ("internal/lingdoc/workspacecore/audit_event_version_fields_test.go", "file"),
            ("internal/lingdoc/workspacecore/pr150_migration_history_test.go", "file"),
            ("internal/lingdoc/membership/fixture_test.go", "file"),
            # These fixtures hand-wrote the column list of lingdoc_project_audits.
            # Adding two columns to that table broke them, and the failure
            # surfaced as an opaque HTTP 500 instead of a schema error, so they
            # now apply the production migration instead of copying the shape.
            ("internal/lingdoc/workspace/audit_event_table_fixture_test.go", "file"),
            ("internal/lingdoc/workspace/change_set_http_test.go", "file"),
            ("internal/lingdoc/workspace/change_set_delivery_test.go", "file"),
            ("internal/lingdoc/workspace/delivery_http_test.go", "file"),
            # These two run a curated subset of the production migration chain.
            # The subset stopped before 000052, so the audit table had no
            # version columns and every audit write failed.
            ("internal/lingdoc/workspace/save_chapter_policy_test.go", "file"),
            ("internal/container/lingdoc_ruleset_delivery_e2e_test.go", "file"),
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
            "**本面已完成**（2026-10-23，认领与完成的人是 magician336）："
            "PR #389 已合入 main（合并提交 5c31fc9b），票 #327 随之关闭。"
            "交付两件事：读类审计写入失败接到 G6 告警源"
            "（EmitAuditFailureAlert 发 Category=audit 的运行事件并被告警规则命中），"
            "以及 AuditEvent 补 target_version / project_revision 两个一等字段并真正落库"
            "（sqlite/000052 与 versioned/000131）。"
            "验收 AC-327-01 至 AC-327-17 全部 passed，证据见 T327-验证报告.json；"
            "合并前健康度复评 H_L 64.4 / F_R 65.0（置信度 B，无独立维度阻断项）。"
            "**本面不释放路径**：done 状态仍占用这 18 条路径，后续面不要重复声明。"
            "**残留票保持开放**：#387（PostgreSQL 上普通读的 fail-open 不成立）、"
            "#414（平台事务外通道的审计写失败恒返回 nil）、"
            "#415（AuditEvent 声明字段与落库列不一致）、"
            "#416（测试夹具自持一份生产 schema 形状的机制治理）。"
            "Spec #130 保持开放：本面只交付了其中的告警接线与两个版本字段，"
            "角色过滤查询等其余验收点未交付（见 #415）。"
        ),
    },
    {
        "name": "PF-4 owner_unavailable 状态与 Owner-only 冻结",
        "issue": 328,
        "issues": (127, 391),
        "status": "claimed",
        "claimed_by": "magician336",
        "paths": (
            ("internal/lingdoc/workspacecore/member_lifecycle_state.go", "file"),
            ("internal/lingdoc/workspacecore/member_judgement.go", "file"),
            ("internal/lingdoc/workspacecore/owner_unavailable.go", "file"),
            ("internal/lingdoc/workspacecore/change_draft_edit.go", "file"),
            ("internal/lingdoc/workspacecore/chapter_review.go", "file"),
            ("internal/lingdoc/workspacecore/context_revision.go", "file"),
            ("internal/lingdoc/workspacecore/member_records.go", "file"),
            ("internal/lingdoc/workspacecore/human_decision.go", "file"),
            ("internal/lingdoc/workspacecore/migrated_context_ports.go", "file"),
            ("internal/lingdoc/workspacecore/context_ports_test.go", "file"),
            ("internal/lingdoc/workspacecore/project_archive.go", "file"),
            ("internal/lingdoc/workspacecore/delivery_export_guard.go", "file"),
            ("internal/lingdoc/workspacecore/service_facade.go", "file"),
            ("internal/lingdoc/workspacecore/typed_contracts.go", "file"),
            ("internal/lingdoc/workspacecore/membership.go", "file"),
            ("internal/lingdoc/membership/results.go", "file"),
            ("internal/lingdoc/membership/policy.go", "file"),
            ("internal/lingdoc/membership/ports.go", "file"),
            ("internal/lingdoc/membership/ports_test.go", "file"),
            ("internal/lingdoc/membership/owner_transfer_test.go", "file"),
            ("internal/clienterror/clienterror.go", "file"),
            ("internal/lingdoc/workspace/full_workflow_http.go", "file"),
            ("internal/router/routes_lingdoc.go", "file"),
            ("internal/router/routes_lingdoc_owner_test.go", "file"),
            ("internal/router/routes_lingdoc_owner_407_test.go", "file"),
            ("internal/router/routes_lingdoc_owner_408_test.go", "file"),
            ("internal/router/routes_lingdoc_owner_409_test.go", "file"),
            ("internal/router/routes_lingdoc_owner_410_test.go", "file"),
            ("docs/08-本轮实施方案/Spec-49-owner_unavailable派生状态与Owner-only冻结.md", "file"),
            ("docs/08-本轮实施方案/contracts/openapi.json", "file"),
            ("docs/08-本轮实施方案/contracts/validation-result.json", "file"),
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
            "原条目换成它。补进来的还有：判定内核周边（change_draft_edit.go、chapter_review.go、"
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
            "这些改动与基线合并组合在同一棵树上，后续冲突会显式报出，不会静默覆盖。"
            "**本面还改了一个 owner 冻结夹具**：internal/router/routes_lingdoc_owner_test.go 的"
            "迁移清单补上 PF-3 的 000052 审计版本列迁移 —— 清单停在 000041 时，"
            "夹具建不出 target_version / project_revision，任何带审计的写入都以 500 结束，"
            "被测的冻结拒绝根本没机会发生。"
        ),
    },
    {
        "name": "PF-5 Agent research purpose",
        "issue": 329,
        "issues": (135, 125, 393),
        "status": "claimed",
        "claimed_by": "magician336",
        "paths": (
            ("internal/lingdoc/workspace/agent_model.go", "file"),
            ("internal/lingdoc/workspacecore/agent_action_service.go", "file"),
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
            ("frontend/src/views/lingdoc/ResearchPanel.vue", "file"),
            ("frontend/src/views/lingdoc/ResearchPanel.test.js", "file"),
        ),
        "resolves": (),
        "summary": (
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
        ),
    },
    {
        "name": "PF-6 上下文包前端消费",
        "issue": 330,
        "issues": (124,),
        "status": "available",
        "claimed_by": "",
        "paths": (
            ("frontend/src/api/lingdoc/g2Context.ts", "file"),
            ("frontend/src/views/lingdoc/ContextPackagePanel.vue", "file"),
        ),
        "resolves": (),
        "summary": (
            "Agent 上下文包、Session、Turn 的后端契约已落地，"
            "但前端零消费——用户不能编辑本轮引用的卡片与资料。"
            "本面只新增文件；与 PF-1 同属前端目录，两者不得互相改对方的文件。"
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
