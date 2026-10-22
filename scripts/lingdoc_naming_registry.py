"""The code naming registry: the one place that says *what each code file owns*.

What this file is
-----------------
LingDoc's code layer names files and identifiers after **project numbers**
(``g5_policy.go``, ``G6CAS``, ``G3SnapshotProject``).  A number says which
product line a piece of work came from; it never says what the code *knows*.
The reader therefore has to look up a table that only exists in prose before
they can read the code at all.

This registry replaces that lookup with a machine-checkable declaration.  For
every code file in the repository that breaks the naming rules it records

* the **path**,
* the **ticket** that will rename it (``#362``-``#370``),
* the **batch** it belongs to (Spec #48's batch axis),
* whether it is still **legacy** (a naming defect we have accepted),
* whether the file is **acceptance harness / fixture** code,
* which naming rules it breaks, and
* the **knowledge it owns** -- one line of plain language, derived from the
  symbols the file exports.

For every numbered **identifier** it records the name, whether it is exported
or file-private, the file that defines it, whether it is still legacy, and
whether it is a random fixture string rather than a naming defect.

Why the knowledge statement matters
------------------------------------
``g5_policy.go`` owns ``EffectivePermission`` and ``CurrentTenantRole``: the
single place where "may this tenant do this" is decided.  ``g6_runtime.go``
owns ``ControlRuntime`` and the runtime event log.  ``g6_trial.go`` owns
``TrialWindow`` and ``TrialSample``.  A reader who knows those three facts
does not need the number, and the number is what forces the lookup.  The
knowledge statement is the thing that survives the rename; the number is the
thing that has to go.

The ratchet
-----------
The registry also carries the **ratchet baseline**: how many legacy files and
how many legacy identifiers exist today.  Debt may shrink; it may not grow.
``RATCHET_BASELINE`` holds the counts, and the loader refuses a registry whose
legacy segment is longer than the baseline.  Every repaying ticket lowers the
baseline in the same change that clears its own segment; ticket ``#370``
finally drives it to zero, at which point the guard test judges every file by
the full rule instead of by exemption.

The three kinds of number
-------------------------
A number in the code layer is a defect in three different shapes, and the
rules below say which shape they are:

``project number`` (``g5``, ``G6CAS``)
    The product line a piece of work came from.  Spec #48's first target.
``task number`` (``t09``, ``T15_SCENARIO_IDS``)
    The historical task pool ``T01``-``T16``.  The documentation layer keeps
    those numbers on purpose; the code layer does not.
``segment number`` (``seg3_context_ports.go``)
    Spec #47's third segment.  It says which batch of work a file came from,
    exactly like ``G5`` does, and it says nothing about what the file knows.

A number may sit at the front of a name (``G2ActionExecutor``) or inside it
(``verify_t09_test.go``).  Both are defects, and the rules below judge both.

What the rules cover
--------------------
Three coverage holes used to sit between this registry and the repository,
and the rules below close them:

* A file whose **name** is clean but whose **body** defines numbered
  identifiers was invisible: it broke no name-level rule, so it was never
  declared, so no ticket owned it and nothing would ever rename those
  identifiers.  The ``numbered_identifiers`` rule makes the body part of the
  file's verdict.
* The **segment number** (``seg3_``) was not a rule at all, so the files
  named after Spec #47's third segment carried no ticket.
* The **task number** (``t09``) was only caught at the front of a name, so
  ``verify_t09_test.go`` slipped through.

Terms
-----
``legacy``
    The entry is a naming defect we have accepted and will repay.  It counts
    towards the ratchet.
``harness``
    The entry belongs to the acceptance harness or a test fixture.  The
    harness has its own vocabulary (``G3SnapshotProject`` is an object of the
    acceptance rig, not of the product), so harness identifiers are declared
    but never counted.
``random fixture string``
    A random blob that happens to start with a project number, e.g. the
    generated password ``G3n3r4t3dP4ssw0rd`` in a test fixture.  It is test
    data, not a naming defect.
``knowledge``
    One line of plain language saying what the file owns.  It must be unique:
    one piece of knowledge has one owner file.

How the guard test uses this file
---------------------------------
``scripts/ci/test_lingdoc_naming_registry.py`` imports the scanner below and
compares the repository against this registry.  The scanner lives here so that
the policy and its mechanical application cannot drift apart: the registry
states the rule, the scanner applies the rule, the guard test compares.

Where this file lives
---------------------
It sits in ``scripts/`` and not in ``scripts/ci/``: that directory belongs to
the gate judgement layer, whose whole point is that exactly one module answers
"what runs".  This registry answers a different question -- "what does this
code own" -- and a second registry in the judgement layer would blur that
boundary.  Its guard test lives in ``scripts/ci/`` because that is where the
CI runs the guard tests from.
"""
from __future__ import annotations

from dataclasses import dataclass
import os
from pathlib import Path
import re
import sys
from typing import Any, Iterable, Mapping, Sequence

REPO_ROOT = Path(__file__).resolve().parents[1]

# ---------------------------------------------------------------------------
# The naming policy
# ---------------------------------------------------------------------------
#
# Spec #48, "Implementation Decisions" 1-3.  A name may enter the code layer
# only when it states the knowledge it owns, the action it performs, or the
# generic domain word for its concept.  Project numbers, ticket numbers and
# acceptance-criteria numbers are none of those three.

#: The code area.  ``docs/`` keeps its ``G*``/``T*`` names on purpose: there
#: a number is a project coordinate and it is useful.
CODE_AREA_ROOTS = ("internal", "frontend", "scripts")

#: The file extensions the rules apply to.
CODE_EXTENSIONS = frozenset({
    ".go", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".vue", ".py",
})

#: Directories that are never source code.
SKIPPED_DIRECTORIES = frozenset({
    "node_modules", ".git", "dist", "__pycache__", ".venv", "vendor",
})

#: A path segment (directory or file stem) that starts with a project number.
NUMBERED_NAME = re.compile(r"^[gGtT]\d")

#: A name that starts with a work-segment number (``seg3``).  Spec #47's
#: third segment is a work coordinate like ``G5``: it says which batch of work
#: the code came from, never what the code knows.  The rule covers the
#: *prefix*, exactly like ``NUMBERED_NAME`` does for the project numbers.
SEGMENT_NAME = re.compile(r"^[sS][eE][gG]\d")

#: A ticket, spec or acceptance-criteria number inside a file name.
TICKET_NUMBER = re.compile(r"(?<![A-Za-z0-9])(?:issue|spec|ac)\d+")

#: A task-pool number (``t09``, ``T15``) inside a file name.  ``T01``-``T16``
#: are the historical task numbers: the documentation layer keeps them on
#: purpose, the code layer does not.  Same mouth as ``TICKET_NUMBER``: the
#: number may sit anywhere in the name.
TASK_NUMBER = re.compile(r"(?<![A-Za-z0-9])[tT]\d+")

#: A test file suffix.  The stem of a test file names the module it exercises.
TEST_SUFFIX = re.compile(r"\.(?:test|spec)\.[A-Za-z0-9]+$")

#: A TypeScript declaration file.  It declares shapes, it is not a module.
TYPE_DECLARATION = re.compile(r"\.d\.ts$")

#: Frontend trees where the file-name convention does not apply, each with a
#: reason.  These are carve-outs from the *convention*, not exemptions of
#: debt: the files in them are not naming defects at all.
FRONTEND_STYLE_EXEMPT = (
    ("frontend/src/i18n/locales/", "locale files are named by their BCP-47 tag"),
    ("frontend/src/wailsjs/", "generated Wails bindings, not hand-authored"),
    ("frontend/public/", "vendored static assets"),
    ("frontend/tests/", "browser test harness"),
)

#: The acceptance rigs.  Their identifiers are the rig's own vocabulary
#: (``G3SnapshotProject``, ``G3MatrixRow``, ``G3FaultSnapshot``): they are
#: declared in the registry but they never count towards the ratchet.
ACCEPTANCE_RIG_DIRS = (
    "frontend/src/views/lingdoc/g3demo/",
    "frontend/scripts/",
    "scripts/acceptance/",
    "scripts/lingdoc_mock/",
)

#: The rules that do not reach into the acceptance rigs.  A rig's vocabulary
#: (``G3SnapshotProject``, ``T15_SCENARIO_IDS``) is the rig's own: a number
#: there names a rig concept, not a product concept.  The rigs' grandfathered
#: ``numbered`` entries stay declared -- they were recognised before this
#: exemption existed -- but the rules added afterwards do not add rig debt.
RIG_EXEMPT_RULES = frozenset({
    "segment_number",
    "task_number",
    "numbered_identifiers",
})

#: Weak-semantic names: a "temporary" concept, an underscore-prefixed debug
#: leftover, a bare "copy", or a leftover "legacy" marker.  Spec #48 batch 5.
WEAK_NAME_PATTERNS = (
    re.compile(r"^temporary_"),
    re.compile(r"^_"),
    re.compile(r"^copy$"),
    re.compile(r"legacy"),
)

#: The file-level rules.  A file breaks a rule when its name does, or when
#: its body defines identifiers that carry a work number.
FILE_DEFECTS = (
    "numbered",
    "segment_number",
    "ticket_number",
    "task_number",
    "numbered_identifiers",
    "frontend_kebab",
    "frontend_vue_case",
    "frontend_camel",
    "weak_name",
)

#: The identifier scopes.
IDENTIFIER_SCOPES = ("exported", "private")

#: The tickets that may own a registry entry.
RENAME_TICKETS = (362, 363, 364, 365, 366, 367, 368, 369, 370)

#: Spec #48's batch axis, mapped onto the published tickets.
#:
#: 1  the registry and the guard test (#361) plus the closing sweep (#370)
#: 2  the numbered exported identifiers, repaid inside the package tickets
#: 3  the front-end impact-centre directories (#365, #366)
#: 4  the numbered files inside the Go packages and the script package
#:    (#362, #363, #364, #367)
#: 5  file-name style and weak semantic names (#368, #369)
#:
#: Spec #48 batch 6 (universal package names) has no ticket and no entry here.
BATCH_BY_TICKET = {
    361: 1,
    362: 4,
    363: 4,
    364: 4,
    365: 3,
    366: 3,
    367: 4,
    368: 5,
    369: 5,
    370: 1,
}

BATCH_LABELS = {
    1: "登记表与守护测试",
    2: "带编号的导出标识符",
    3: "前端影响中心演示目录",
    4: "Go 包与脚本包内的编号文件",
    5: "文件名风格与弱语义名",
}

#: Knowledge statements that say nothing.  A knowledge statement has to point
#: at one file.
VACUOUS_KNOWLEDGE = ("相关代码", "相关逻辑", "相关实现", "相关文件", "待补充", "TODO")

#: The shortest acceptable knowledge statement.
MIN_KNOWLEDGE_LENGTH = 10

#: Random blobs that happen to start with a project number.  They are test
#: data, never naming defects.  The scan works on *definitions*, so these
#: cannot show up as identifiers today; the list keeps the exemption explicit
#: for any scanner that looks at occurrences instead.
RANDOM_FIXTURE_TOKENS = (
    "G1E9bTP",
    "G2MZaX0cwDuXsfw",
    "G2UbfbBEF32wiepXIsMlTW9",
    "G3n3r4t3dP4ssw0rd",
    "G3xwnC0GqY4gmQ3j",
    "G454KI8BXM5",
    "G4jtmfIb",
)

# ---------------------------------------------------------------------------
# The ratchet baseline
# ---------------------------------------------------------------------------
#
# Measured on the baseline commit below, with the scanner in this file.
#
# Scope of the three counts:
#   * ``files``             -- code files in the code area that break at least
#                              one file-level rule.  A file inside a numbered
#                              directory counts as one entry.
#   * ``exported_identifiers`` -- numbered identifiers defined at package level
#                              and visible outside their file (Go's exported
#                              names, TypeScript's ``export``ed names, Python's
#                              module-level classes).
#   * ``private_identifiers``  -- numbered identifiers defined at package level
#                              and visible only inside their own file.
#
# Both identifier counts are counted at *definition sites*, not at
# occurrences: a reference to an exported name in fifty files is one
# identifier, not fifty.  Occurrence counting would also pull in import path
# fragments (``g2_service``), directory names (``g3demo``) and bare family
# markers (``G3``), none of which are identifiers.
#
# The acceptance rigs (ACCEPTANCE_RIG_DIRS) are excluded from the identifier
# counts: their ``G3*`` names are the rig's own vocabulary, not product debt.
# Unit test files are *not* excluded -- a helper named ``g2Fixture`` is
# renamed together with the package it tests.
#
# The ``files`` count is the one that moved when the rule set grew.  The
# registry originally only judged a file by its *name*, so a file whose name
# was clean but whose body defined numbered identifiers was never declared
# and no ticket owned it.  The ``numbered_identifiers`` rule recognises that
# debt, and the ``segment_number`` / ``task_number`` rules recognise the two
# number families the name rules had missed.  The baseline was raised once,
# in the change that recognised the debt; from there it only goes down.
#
# For comparison, Spec #48 measured 56 exported and 210 private identifiers
# over the whole code area by occurrence; the definition-site counts below are
# the same measure made exact.
RATCHET_BASELINE = {
    "base_sha": "058637c4f869f58a9283180a4b1396e7b23db001",
    "scope": (
        "code area internal/ + frontend/ + scripts/, source-code extensions only, "
        "counted at definition sites; acceptance rigs excluded from the "
        "identifier counts"
    ),
    "files": 199,
    "exported_identifiers": 54,
    "private_identifiers": 202,
}

# ---------------------------------------------------------------------------
# The declarative data
# ---------------------------------------------------------------------------

#: Every code file that breaks a naming rule, and the knowledge it owns.
NAMED_FILES: tuple[Mapping[str, Any], ...] = (
    {
        "path": "frontend/scripts/g3demo/fetchClient.ts",
        "ticket": 366,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "演示取数客户端：给脚本包一层带超时的 fetch",
    },
    {
        "path": "frontend/scripts/g3demo/run-blocked.ts",
        "ticket": 366,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered", "frontend_kebab", "frontend_camel"),
        "knowledge": "阻断链路运行脚本：跑一遍阻断链路并把结果落盘",
    },
    {
        "path": "frontend/scripts/g3demo/run-chain.ts",
        "ticket": 366,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered", "frontend_kebab", "frontend_camel"),
        "knowledge": "主链路运行脚本：跑一遍完整链路并把结果落盘",
    },
    {
        "path": "frontend/scripts/g3demo/run-changeset.ts",
        "ticket": 366,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered", "frontend_kebab", "frontend_camel"),
        "knowledge": "变更集链路运行脚本：跑一遍变更集链路并把结果落盘",
    },
    {
        "path": "frontend/scripts/g3demo/run-dependencies.ts",
        "ticket": 366,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered", "frontend_kebab", "frontend_camel"),
        "knowledge": "依赖链路运行脚本：跑一遍依赖链路并把结果落盘",
    },
    {
        "path": "frontend/scripts/g3demo/run-evidence.ts",
        "ticket": 366,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered", "frontend_kebab", "frontend_camel"),
        "knowledge": "证据链路运行脚本：跑一遍证据导出并把结果落盘",
    },
    {
        "path": "frontend/scripts/g3demo/run-model.ts",
        "ticket": 366,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered", "frontend_kebab", "frontend_camel"),
        "knowledge": "模型链路运行脚本：跑一遍模型链路并把结果落盘",
    },
    {
        "path": "frontend/scripts/g3demo/run-stabilization.ts",
        "ticket": 366,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered", "frontend_kebab", "frontend_camel"),
        "knowledge": "稳定化链路运行脚本：跑一遍稳定化链路并把结果落盘",
    },
    {
        "path": "frontend/scripts/verify-browser-preview-pip.mjs",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": True,
        "defects": ("frontend_kebab", "frontend_camel"),
        "knowledge": "浏览器画中画验收脚本：用真实画中画窗口与生产组件跑一遍预览",
    },
    {
        "path": "frontend/src/api/chat-history.ts",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": False,
        "defects": ("frontend_kebab", "frontend_camel"),
        "knowledge": "对话历史：租户对话历史配置、消息检索与知识库统计",
    },
    {
        "path": "frontend/src/api/chat/chatAttachments.ts",
        "ticket": 369,
        "batch": 5,
        "legacy": False,
        "harness": False,
        "defects": ("frontend_kebab", "frontend_camel"),
        "knowledge": "聊天附件接口：会话聊天附件的上传、预览、查询与删除",
    },
    {
        "path": "frontend/src/api/env-vars.ts",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": False,
        "defects": ("frontend_kebab", "frontend_camel"),
        "knowledge": "环境变量：技能与沙箱的环境变量读写与分组",
    },
    {
        "path": "frontend/src/api/lingdoc/g1Initialization.ts",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": False,
        "defects": ("numbered",),
        "knowledge": "项目初始化：激活差异、激活回执、废弃草稿与草稿候选",
    },
    {
        "path": "frontend/src/api/lingdoc/g2.test.js",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "Agent 前端接口验收：客户端请求与后端契约一致",
    },
    {
        "path": "frontend/src/api/lingdoc/g2.ts",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": False,
        "defects": ("numbered", "numbered_identifiers"),
        "knowledge": "Agent 前端接口：研究任务、导入候选、来源锚点与证据选择",
    },
    {
        "path": "frontend/src/api/mcp-service.ts",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": False,
        "defects": ("frontend_kebab", "frontend_camel"),
        "knowledge": "MCP 服务管理：服务、工具、审批行与连通性测试",
    },
    {
        "path": "frontend/src/api/message-suggestion.ts",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": False,
        "defects": ("frontend_kebab", "frontend_camel"),
        "knowledge": "消息建议：确保建议已就绪、读取建议集与记录使用事件",
    },
    {
        "path": "frontend/src/api/storage-backend.ts",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": False,
        "defects": ("frontend_kebab", "frontend_camel"),
        "knowledge": "存储后端：后端列表、类型、增删改、默认项与连通性测试",
    },
    {
        "path": "frontend/src/api/tenant/audit-log.ts",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": False,
        "defects": ("frontend_kebab", "frontend_camel"),
        "knowledge": "租户审计日志：按动作与结果分页查询审计条目",
    },
    {
        "path": "frontend/src/api/user-favorites.ts",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": False,
        "defects": ("frontend_kebab", "frontend_camel"),
        "knowledge": "用户收藏：收藏条目的增删查与收藏的资源类型",
    },
    {
        "path": "frontend/src/api/vector-store.ts",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": False,
        "defects": ("frontend_kebab", "frontend_camel"),
        "knowledge": "向量库：向量库实体、类型信息、字段模式与连通性测试",
    },
    {
        "path": "frontend/src/api/web-search-provider.ts",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": False,
        "defects": ("frontend_kebab", "frontend_camel"),
        "knowledge": "网页搜索供应商：供应商配置、凭据与类型信息",
    },
    {
        "path": "frontend/src/api/web-search.ts",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": False,
        "defects": ("frontend_kebab", "frontend_camel"),
        "knowledge": "网页搜索配置：租户级搜索供应商与配置读写",
    },
    {
        "path": "frontend/src/components/Input-field.agent-switch.test.mjs",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": True,
        "defects": ("frontend_kebab",),
        "knowledge": "输入框验收：输入框里的 Agent 模式切换",
    },
    {
        "path": "frontend/src/components/Input-field.browser.test.mjs",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": True,
        "defects": ("frontend_kebab",),
        "knowledge": "输入框验收：浏览器来源按钮在扩展离线时禁用并跳到设置",
    },
    {
        "path": "frontend/src/components/Input-field.focus.test.mjs",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": True,
        "defects": ("frontend_kebab",),
        "knowledge": "输入框验收：发送后清空草稿并把焦点还给输入框",
    },
    {
        "path": "frontend/src/components/Input-field.vue",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": False,
        "defects": ("frontend_kebab", "frontend_vue_case"),
        "knowledge": "输入框：聊天输入、@提及、文件拖放与选择模式",
    },
    {
        "path": "frontend/src/components/css/chat-citations.test.mjs",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": True,
        "defects": ("frontend_kebab",),
        "knowledge": "引用块样式验收：引用胶囊的紧凑基线对齐样式",
    },
    {
        "path": "frontend/src/components/css/chat-timeline-loading.test.mjs",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": True,
        "defects": ("frontend_kebab",),
        "knowledge": "时间线加载样式验收：时间线样式不得新增独立的加载行",
    },
    {
        "path": "frontend/src/components/doc-content.vue",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": False,
        "defects": ("frontend_kebab", "frontend_vue_case"),
        "knowledge": "文档内容区：文档正文、差异行与抽屉宽度",
    },
    {
        "path": "frontend/src/components/document-preview.focus.test.mjs",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": True,
        "defects": ("frontend_kebab",),
        "knowledge": "文档预览验收：预览器的焦点与滚动行为",
    },
    {
        "path": "frontend/src/components/document-preview.fullscreen.test.mjs",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": True,
        "defects": ("frontend_kebab",),
        "knowledge": "文档预览验收：预览器的全屏切换行为",
    },
    {
        "path": "frontend/src/components/document-preview.vue",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": False,
        "defects": ("frontend_kebab", "frontend_vue_case"),
        "knowledge": "文档预览：文档、图片与 PPTX 的预览器",
    },
    {
        "path": "frontend/src/components/empty-knowledge.vue",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": False,
        "defects": ("frontend_kebab", "frontend_vue_case"),
        "knowledge": "空知识库提示：拖放上传引导与可接受格式提示",
    },
    {
        "path": "frontend/src/components/knowledge-processing-timeline.test.ts",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": True,
        "defects": ("frontend_kebab",),
        "knowledge": "知识处理时间线验收：处理阶段的划分与内联渲染上限",
    },
    {
        "path": "frontend/src/components/knowledge-processing-timeline.vue",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": False,
        "defects": ("numbered_identifiers", "frontend_kebab", "frontend_vue_case"),
        "knowledge": "知识处理时间线：知识入库各阶段的进度与诊断展示",
    },
    {
        "path": "frontend/src/components/manual-knowledge-editor.vue",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": False,
        "defects": ("frontend_kebab", "frontend_vue_case"),
        "knowledge": "手工知识编辑：手工条目的状态、工具栏动作与分组",
    },
    {
        "path": "frontend/src/components/picture-preview.vue",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": False,
        "defects": ("frontend_kebab", "frontend_vue_case"),
        "knowledge": "图片预览：审阅图片的大图查看器与关闭事件",
    },
    {
        "path": "frontend/src/components/upload-mask.vue",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": False,
        "defects": ("frontend_kebab", "frontend_vue_case"),
        "knowledge": "上传遮罩：拖放上传时的遮罩与格式提示",
    },
    {
        "path": "frontend/src/embed-main.ts",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": False,
        "defects": ("frontend_kebab", "frontend_camel"),
        "knowledge": "嵌入式入口：以独立挂载方式启动只含受保护资源预览的前端",
    },
    {
        "path": "frontend/src/types/tool-results.ts",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": False,
        "defects": ("frontend_kebab", "frontend_camel"),
        "knowledge": "工具结果类型：检索、分块、知识库与图谱等工具返回体的前端类型",
    },
    {
        "path": "frontend/src/utils/agent-mode.test.mjs",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": True,
        "defects": ("frontend_kebab",),
        "knowledge": "Agent 模式验收：内置快捷问答与智能推理的标识判定",
    },
    {
        "path": "frontend/src/utils/agent-mode.ts",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": False,
        "defects": ("frontend_kebab", "frontend_camel"),
        "knowledge": "Agent 模式：内置 Agent 标识判定与模式对齐",
    },
    {
        "path": "frontend/src/utils/agent-readiness.test.mjs",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": True,
        "defects": ("frontend_kebab",),
        "knowledge": "Agent 就绪度验收：未就绪原因、聊天模型与重排模型的要求",
    },
    {
        "path": "frontend/src/utils/agent-readiness.ts",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": False,
        "defects": ("frontend_kebab", "frontend_camel"),
        "knowledge": "Agent 就绪度：是否已配聊天模型、是否需要重排模型，以及未就绪原因定位",
    },
    {
        "path": "frontend/src/utils/agent-tool-display.test.mjs",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": True,
        "defects": ("frontend_kebab",),
        "knowledge": "Agent 工具展示验收：检索来源、问答摘要与工具图标名",
    },
    {
        "path": "frontend/src/utils/agent-tool-display.ts",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": False,
        "defects": ("frontend_kebab", "frontend_camel"),
        "knowledge": "Agent 工具展示：检索来源、问答摘要与流水线步骤标题",
    },
    {
        "path": "frontend/src/utils/agent-tool-icons.ts",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": False,
        "defects": ("frontend_kebab", "frontend_camel"),
        "knowledge": "Agent 工具图标：工具类型到图标名的映射",
    },
    {
        "path": "frontend/src/utils/api-base.ts",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": False,
        "defects": ("frontend_kebab", "frontend_camel"),
        "knowledge": "API 基址：前端请求的后端基址解析",
    },
    {
        "path": "frontend/src/utils/card-list-badge.ts",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": False,
        "defects": ("frontend_kebab", "frontend_camel"),
        "knowledge": "卡片列表徽标：资源来源徽标与组织关系标签的显示判定",
    },
    {
        "path": "frontend/src/utils/disable-autofill.ts",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": False,
        "defects": ("frontend_kebab", "frontend_camel"),
        "knowledge": "禁自动填充：浏览器自动填充的拦截安装",
    },
    {
        "path": "frontend/src/utils/format-list.ts",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": False,
        "defects": ("frontend_kebab", "frontend_camel"),
        "knowledge": "本地化列表格式化：按当前语言拼装列表文案",
    },
    {
        "path": "frontend/src/utils/rag-pipeline-history.test.mjs",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": True,
        "defects": ("frontend_kebab",),
        "knowledge": "RAG 流水线历史验收：工具事件识别与历史消息流补齐",
    },
    {
        "path": "frontend/src/utils/rag-pipeline-history.ts",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": False,
        "defects": ("frontend_kebab", "frontend_camel"),
        "knowledge": "RAG 流水线历史：识别流水线工具事件并补齐历史消息",
    },
    {
        "path": "frontend/src/utils/rag-pipeline-state.test.ts",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": True,
        "defects": ("frontend_kebab",),
        "knowledge": "RAG 等待态验收：等待类别判定与等待控制器的延迟",
    },
    {
        "path": "frontend/src/utils/rag-pipeline-state.ts",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": False,
        "defects": ("frontend_kebab", "frontend_camel"),
        "knowledge": "RAG 等待态：等待类别判定与等待控制器",
    },
    {
        "path": "frontend/src/utils/tdesign-icon-offline.ts",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": False,
        "defects": ("frontend_kebab", "frontend_camel"),
        "knowledge": "TDesign 图标离线守卫：拦截外链图标与脚本",
    },
    {
        "path": "frontend/src/utils/tool-capabilities.ts",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": False,
        "defects": ("frontend_kebab", "frontend_camel"),
        "knowledge": "工具能力矩阵：工具所需能力、知识库过滤与是否消费文件",
    },
    {
        "path": "frontend/src/utils/tool-icons.ts",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": False,
        "defects": ("frontend_kebab", "frontend_camel"),
        "knowledge": "工具图标：工具与匹配类型的图标与显示名",
    },
    {
        "path": "frontend/src/views/chat/components/deepThink.vue",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": False,
        "defects": ("frontend_vue_case",),
        "knowledge": "深度思考折叠块：展示模型思考过程与当前思考状态",
    },
    {
        "path": "frontend/src/views/chat/components/docInfo.vue",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": False,
        "defects": ("frontend_vue_case",),
        "knowledge": "引用来源块：展示本轮回答引用的知识库来源与时间线模式",
    },
    {
        "path": "frontend/src/views/chat/components/sendMsg.vue",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": False,
        "defects": ("frontend_vue_case",),
        "knowledge": "发送描述输入框：新建对话时填写问题描述",
    },
    {
        "path": "frontend/src/views/creatChat/creatChat.vue",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": False,
        "defects": ("frontend_vue_case",),
        "knowledge": "新建对话面板：推荐问题、问题输入与新建对话流程",
    },
    {
        "path": "frontend/src/views/lingdoc/G2ResearchPanel.test.js",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "研究面板验收：面板交互、撤销输入与候选确认",
    },
    {
        "path": "frontend/src/views/lingdoc/G2ResearchPanel.vue",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": False,
        "defects": ("numbered",),
        "knowledge": "研究面板：展示研究轮次、来源副本与规格候选的确认",
    },
    {
        "path": "frontend/src/views/lingdoc/G2RevokedInput.runtime.test.js",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "撤销输入运行时验收：AI 来源与人工来源在撤销后的可见性",
    },
    {
        "path": "frontend/src/views/lingdoc/G2SpecCandidate.test.js",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "规格候选验收：候选面板展示与来源固定行为",
    },
    {
        "path": "frontend/src/views/lingdoc/g2UiState.test.ts",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "研究界面状态验收：研究查询、选区与来源固定",
    },
    {
        "path": "frontend/src/views/lingdoc/g2UiState.ts",
        "ticket": 368,
        "batch": 5,
        "legacy": True,
        "harness": False,
        "defects": ("numbered", "numbered_identifiers"),
        "knowledge": "研究界面状态：研究查询、选区、来源固定与候选输入构造",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/G3Demo.vue",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "影响中心演示主界面：把影响中心各条链路的结论与证据装配成一屏",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/g3Evidence.test.ts",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "行证据验收：每条行证据规则的成立条件",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/g3Evidence.ts",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "行证据规则：把每条影响任务与它要满足的证据规则对应起来",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/g3Export.test.ts",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "证据导出验收：导出目录、文件名前缀与结论来源的稳定性",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/g3Export.ts",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "证据导出：把运行证据导出成可归档的矩阵与缺口清单",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/g3Flow.test.ts",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "执行状态验收：流程各步执行状态的取值集合",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/g3Flow.ts",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "执行状态：流程各步当前处于哪一种执行状态",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/g3Gaps.test.ts",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "实现缺口验收：缺口按票号登记与镜像缺失的处理",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/g3Gaps.ts",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "实现缺口清单：每条影响任务背后还欠哪些实现，并关联到具体票",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/g3HttpAdapter.ts",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "HTTP 适配器：把请求包装成统一的客户端调用并带上超时",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/g3Matrix.test.ts",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "影响矩阵验收：基线锚点的解析与比较",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/g3Matrix.ts",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "影响矩阵：把验收标准、生命周期读数与证据规则拼成一张矩阵",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/g3Snapshot.test.ts",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "运行时快照验收：快照头与运行时提交的取值",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/g3Snapshot.ts",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "运行时快照：导出演示运行所依据的项目、变更集、任务与发布快照",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/g3Status.test.ts",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "状态与严重度验收：状态取值、语气与是否阻断发布",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/g3Status.ts",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "任务状态与严重度：状态取值、语气、摘要与是否阻断发布",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/runner/assistant.test.ts",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "助手链路验收：助手步骤、项目路径与轮次建议",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/runner/assistant.ts",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "助手链路：助手如何读建议、给出解释并生成改写草稿",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/runner/blocked.test.ts",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "阻断链路验收：阻断步骤、处置输入与驳回边界",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/runner/blocked.ts",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "阻断链路：发现阻断项、读取处置事实并判定可否驳回",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/runner/chain.test.ts",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "链路执行验收：串行执行的步骤顺序与结果传递",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/runner/chain.ts",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "链路执行：把若干步骤按顺序跑成一条可复现的链",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/runner/changeset.test.ts",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "变更集链路验收：项目、问题、方法、租户与评估事实",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/runner/changeset.ts",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "变更集链路：读取变更集事实、影响对象、已解决影响与失败事实",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/runner/dependencies.test.ts",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "依赖链路验收：依赖步骤与候选事实",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/runner/dependencies.ts",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "依赖链路：依赖边、候选依赖与抑制事实，以及任务状态词表",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/runner/factHelpers.ts",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "事实读取辅助：把响应体读成可断言的事实，并区分客户端拒绝与失败请求",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/runner/fakeHttp.ts",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "假客户端：用固定回放替代真实 HTTP，供链路测试使用",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/runner/faultInjection.test.ts",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "故障注入验收：注入点、故障码与观测结果",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/runner/faultInjection.ts",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "故障注入：在指定注入点制造可观测故障，并识别被注入的故障",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/runner/httpClient.test.ts",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "HTTP 客户端验收：幂等键长度与豁免后缀",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/runner/httpClient.ts",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "HTTP 客户端契约：幂等键规则、响应信封与错误码读取",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/runner/identity.test.ts",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "演示身份验收：演示账号存储键与当前身份读取",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/runner/identity.ts",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "演示身份：创建、保存与读取演示账号，注册与登录",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/runner/model.test.ts",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "模型链路验收：模型步骤与边界动作",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/runner/model.ts",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "模型链路：跑模型、比对候选并识别被禁止的助手路径",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/runner/project.test.ts",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "演示项目验收：演示项目必填字段与章节",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/runner/project.ts",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "演示项目：创建项目、保存规格、激活与必填字段清单",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/runner/roadmap.test.ts",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "路线图链路验收：路线图步骤与基线事实",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/runner/roadmap.ts",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "路线图链路：路线图基线、节点、边、提案、提交与会话事实",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/runner/runContext.test.ts",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "运行上下文验收：链路错误与运行上下文的传递",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/runner/runContext.ts",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "运行上下文：运行标识、幂等键、步骤执行与后端存活探测",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/runner/stabilization.test.ts",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "稳定化链路验收：稳定化步骤与项目正文",
    },
    {
        "path": "frontend/src/views/lingdoc/g3demo/runner/stabilization.ts",
        "ticket": 365,
        "batch": 3,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "稳定化链路：判断事实是否已稳定、列出阻断项与关闭证据",
    },
    {
        "path": "internal/application/repository/retriever/opensearch/copy.go",
        "ticket": 369,
        "batch": 5,
        "legacy": True,
        "harness": False,
        "defects": ("weak_name",),
        "knowledge": "检索器索引副本：把文档复制进检索索引的动作",
    },
    {
        "path": "internal/application/repository/chat_attachment.go",
        "ticket": 369,
        "batch": 5,
        "legacy": False,
        "harness": False,
        "defects": ("weak_name",),
        "knowledge": "聊天附件仓储：聊天附件与分块的存储、状态流转与过期清理",
    },
    {
        "path": "internal/application/service/chat_attachment.go",
        "ticket": 369,
        "batch": 5,
        "legacy": False,
        "harness": False,
        "defects": ("weak_name",),
        "knowledge": "聊天附件服务：聊天附件的创建、读取、处理与提示词解析",
    },
    {
        "path": "internal/application/service/chat_attachment_test.go",
        "ticket": 369,
        "batch": 5,
        "legacy": False,
        "harness": True,
        "defects": ("weak_name",),
        "knowledge": "聊天附件服务验收：图片理解的 OCR、描述与降级路径",
    },
    {
        "path": "internal/config/auth_tenant_env_defaults_test.go",
        "ticket": 369,
        "batch": 5,
        "legacy": False,
        "harness": True,
        "defects": ("weak_name",),
        "knowledge": "认证与租户的环境变量默认值验收：注册开关、租户模式与跨租户访问的环境变量契约",
    },
    {
        "path": "internal/container/g4_issue196_legacy_e2e_test.go",
        "ticket": 364,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("numbered", "ticket_number", "weak_name"),
        "knowledge": "规则集遗留路径端到端：遗留模板路径在重启后仍然生效（SQLite 与 Postgres）",
    },
    {
        "path": "internal/container/lingdoc_ruleset_delivery_e2e_test.go",
        "ticket": 364,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("numbered_identifiers",),
        "knowledge": "规则集投递端到端验收：HTTP 导出落到库里并跨重启仍然读得回",
    },
    {
        "path": "internal/evidence/verify_asset_service_test.go",
        "ticket": 369,
        "batch": 5,
        "legacy": False,
        "harness": True,
        "defects": ("task_number", "numbered_identifiers"),
        "knowledge": "资料服务验收报告：把绑定存储、原文读取与迁移表各条验收跑一遍并落盘",
    },
    {
        "path": "internal/handler/session/chat_attachment_persist_test.go",
        "ticket": 369,
        "batch": 5,
        "legacy": False,
        "harness": True,
        "defects": ("weak_name",),
        "knowledge": "聊天附件回写验收：解析后的附件内容回写到会话消息",
    },
    {
        "path": "internal/handler/session/chat_attachment.go",
        "ticket": 369,
        "batch": 5,
        "legacy": False,
        "harness": False,
        "defects": ("weak_name",),
        "knowledge": "聊天附件接口：上传、预览、查询与删除聊天附件",
    },
    {
        "path": "internal/lingdoc/generation/g2_evidence_store.go",
        "ticket": 364,
        "batch": 4,
        "legacy": True,
        "harness": False,
        "defects": ("numbered",),
        "knowledge": "生成输入读取：加载生成所需输入并支持重放读取",
    },
    {
        "path": "internal/lingdoc/generation/g2_evidence_test.go",
        "ticket": 364,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("numbered", "numbered_identifiers"),
        "knowledge": "生成证据验收：卡在模型前或模型中变更不得发布候选、快照被篡改要在模型前停住、机器身份不得升级",
    },
    {
        "path": "internal/lingdoc/workspace/agent_context.go",
        "ticket": 363,
        "batch": 4,
        "legacy": True,
        "harness": False,
        "defects": ("numbered_identifiers",),
        "knowledge": "智能体行动执行器：把行动提案分派到保存规格、绑定资产等应用口",
    },
    {
        "path": "internal/lingdoc/workspace/agent_model.go",
        "ticket": 363,
        "batch": 4,
        "legacy": True,
        "harness": False,
        "defects": ("numbered_identifiers",),
        "knowledge": "智能体模型宿主：复用对话模型登记表与验收额度账本回答一次提问",
    },
    {
        "path": "internal/lingdoc/workspace/agent_model_test.go",
        "ticket": 363,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("numbered_identifiers",),
        "knowledge": "智能体模型验收用例：同意、输出与超时账本三条边界的断言",
    },
    {
        "path": "internal/lingdoc/workspace/assets_http_test.go",
        "ticket": 363,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("numbered_identifiers",),
        "knowledge": "来源路由的 HTTP 验收：挂上路由后状态码与响应体仍合领域判据",
    },
    {
        "path": "internal/lingdoc/workspace/g1_provenance_oneconn_test.go",
        "ticket": 363,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("numbered", "numbered_identifiers"),
        "knowledge": "单连接来源验收：回放须复核历史来源、人工同值改写不得抹掉来源固定",
    },
    {
        "path": "internal/lingdoc/workspace/g2_full_http.go",
        "ticket": 363,
        "batch": 4,
        "legacy": True,
        "harness": False,
        "defects": ("numbered", "numbered_identifiers"),
        "knowledge": "Agent 全量 HTTP 路由：注册 Agent 全量接口与统一错误回写",
    },
    {
        "path": "internal/lingdoc/workspace/g2_generation_evidence.go",
        "ticket": 363,
        "batch": 4,
        "legacy": True,
        "harness": False,
        "defects": ("numbered", "numbered_identifiers"),
        "knowledge": "生成证据授权：人工生成与章节生成的授权、证据校验与证据约束",
    },
    {
        "path": "internal/lingdoc/workspace/g2_generation_evidence_test.go",
        "ticket": 363,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("numbered", "numbered_identifiers"),
        "knowledge": "生成证据验收：缺模型同意或章节写入不得调模型、只许使用选定的不可变来源",
    },
    {
        "path": "internal/lingdoc/workspace/g2_model_evidence_payload.go",
        "ticket": 363,
        "batch": 4,
        "legacy": True,
        "harness": False,
        "defects": ("numbered",),
        "knowledge": "模型证据载荷：只允许被授权的字段从已校验的证据卡进入模型输入",
    },
    {
        "path": "internal/lingdoc/workspace/g2_model_evidence_payload_test.go",
        "ticket": 363,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "模型证据载荷验收：未选定或未提供的来源不得进入载荷",
    },
    {
        "path": "internal/lingdoc/workspace/g2_surface.go",
        "ticket": 363,
        "batch": 4,
        "legacy": True,
        "harness": False,
        "defects": ("numbered", "numbered_identifiers"),
        "knowledge": "Agent 表面措辞：Agent 这一侧对四类失败的自有措辞与状态码",
    },
    {
        "path": "internal/lingdoc/workspace/g4_delivery_propagation_test.go",
        "ticket": 363,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "交付传播验收：独立副本的边注必须到达真实的冻结检查与文档校验",
    },
    {
        "path": "internal/lingdoc/workspace/g4_full_http.go",
        "ticket": 363,
        "batch": 4,
        "legacy": True,
        "harness": False,
        "defects": ("numbered", "numbered_identifiers"),
        "knowledge": "治理全量 HTTP 路由：注册治理全量接口与「需人工决策」的错误形态",
    },
    {
        "path": "internal/lingdoc/workspace/g4_full_http_test.go",
        "ticket": 363,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("numbered", "numbered_identifiers"),
        "knowledge": "治理全量 HTTP 验收：当前权限、严格请求体、重放登记与机器不得替人决策",
    },
    {
        "path": "internal/lingdoc/workspace/g5_c1_contract_test.go",
        "ticket": 363,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("numbered", "numbered_identifiers"),
        "knowledge": "权限 HTTP 契约验收：代表性 HTTP 规格契约与处理器组合的上下文端口",
    },
    {
        "path": "internal/lingdoc/workspace/g5_machine_read_http.go",
        "ticket": 363,
        "batch": 4,
        "legacy": True,
        "harness": False,
        "defects": ("numbered",),
        "knowledge": "机器只读 HTTP 路由：注册机器只读接口、资产可读判定与最小校验",
    },
    {
        "path": "internal/lingdoc/workspace/g5_machine_read_http_test.go",
        "ticket": 363,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "机器只读 HTTP 验收：保留认证主体与最小响应形状、缺依赖一律失败关闭",
    },
    {
        "path": "internal/lingdoc/workspace/research_adapter.go",
        "ticket": 363,
        "batch": 4,
        "legacy": True,
        "harness": False,
        "defects": ("numbered_identifiers",),
        "knowledge": "调研适配器：把检索、原文读取与来源核验转给现有检索服务",
    },
    {
        "path": "internal/lingdoc/workspace/research_adapter_test.go",
        "ticket": 363,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("numbered_identifiers",),
        "knowledge": "调研适配器替身：授权、资产、来源检索、原文与网页搜索的五个假端口",
    },
    {
        "path": "internal/lingdoc/workspace/seg3_full_workflow_ports.go",
        "ticket": 363,
        "batch": 4,
        "legacy": True,
        "harness": False,
        "defects": ("segment_number",),
        "knowledge": "全流程路由的成员、复核与生命周期三门窄口束",
    },
    {
        "path": "internal/lingdoc/workspace/seg3_full_workflow_ports_test.go",
        "ticket": 363,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("segment_number",),
        "knowledge": "全流程守卫窄口装配替身：把聚合体接到交付、溯源、影响等六个窄口",
    },
    {
        "path": "internal/lingdoc/workspace/seg3_g2_ports.go",
        "ticket": 363,
        "batch": 4,
        "legacy": True,
        "harness": False,
        "defects": ("segment_number", "numbered_identifiers"),
        "knowledge": "G2 路由的九门上下文束：把这条面要用的窄口写在一处清单里",
    },
    {
        "path": "internal/lingdoc/workspace/seg3_g2_ports_test.go",
        "ticket": 363,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("segment_number", "numbered_identifiers"),
        "knowledge": "G2 路由窄口装配替身：测试侧把聚合体接到九个上下文窄口上",
    },
    {
        "path": "internal/lingdoc/workspace/seg3_impact_ports.go",
        "ticket": 363,
        "batch": 4,
        "legacy": True,
        "harness": False,
        "defects": ("segment_number",),
        "knowledge": "影响中心的变更集、影响、依赖、运行时、试验与路线图六门窄口束",
    },
    {
        "path": "internal/lingdoc/workspace/seg3_impact_ports_test.go",
        "ticket": 363,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("segment_number",),
        "knowledge": "影响中心窄口装配替身：把聚合体接到影响中心的六个上下文窄口",
    },
    {
        "path": "internal/lingdoc/workspace/seg3_no_backdoor_test.go",
        "ticket": 363,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("segment_number",),
        "knowledge": "传输层后门断言：运行期类型断言不得指向聚合体方法集的子集",
    },
    {
        "path": "internal/lingdoc/workspace/seg3_optional_capabilities_test.go",
        "ticket": 363,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("segment_number",),
        "knowledge": "可选能力接线替身：把溯源与证据选择四个可选能力显式交给运行时",
    },
    {
        "path": "internal/lingdoc/workspace/seg3_port_stubs_test.go",
        "ticket": 363,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("segment_number",),
        "knowledge": "五个上下文窄口的测试替身：一个服务替身装配成五条具名窄口",
    },
    {
        "path": "internal/lingdoc/workspace/seg3_project_ports.go",
        "ticket": 363,
        "batch": 4,
        "legacy": True,
        "harness": False,
        "defects": ("segment_number",),
        "knowledge": "项目侧三条窄口：授权、项目读与生成上下文各答各的问题",
    },
    {
        "path": "internal/lingdoc/workspace/seg3_transport_ports_test.go",
        "ticket": 363,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("segment_number",),
        "knowledge": "传输层不持有聚合体的源码断言：逐个类型表达式查出聚合体具体类型",
    },
    {
        "path": "internal/lingdoc/workspace/template_governance_adapter_test.go",
        "ticket": 363,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("numbered_identifiers",),
        "knowledge": "模板治理验收夹具：共享权限、章节授权与模板草稿规则集的装配",
    },
    {
        "path": "internal/lingdoc/workspace/template_governance_runtime.go",
        "ticket": 363,
        "batch": 4,
        "legacy": True,
        "harness": False,
        "defects": ("numbered_identifiers",),
        "knowledge": "模板治理运行时：装配工作区、证据、模型与目录服务并负责影响规则检查",
    },
    {
        "path": "internal/lingdoc/workspacecore/change_set_test.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("numbered_identifiers",),
        "knowledge": "变更集事务替身：藏掉审计能力、留下变更集与来源读写口",
    },
    {
        "path": "internal/lingdoc/workspacecore/g1_baseline_test.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("numbered", "numbered_identifiers"),
        "knowledge": "项目基线与激活验收：激活差异绑定确认版本、Owner 转移只能接受一次、模板迁移预览与废弃项目恢复的基线用例",
    },
    {
        "path": "internal/lingdoc/workspacecore/g1_discarded.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": False,
        "defects": ("numbered",),
        "knowledge": "废弃草稿项目清单：Owner 找回自己可恢复的被丢弃草稿，它们不混进普通项目列表",
    },
    {
        "path": "internal/lingdoc/workspacecore/g1_g7_closed_loop_test.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "首轮人工阅读闭环验收：初稿激活须当前人工阅读、评审评论锁定不可变版本",
    },
    {
        "path": "internal/lingdoc/workspacecore/g1_inherited_fields.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": False,
        "defects": ("numbered", "numbered_identifiers"),
        "knowledge": "继承字段还原：把服务端历史里的引用来源恢复到草稿字段，客户端断言不得抹掉来源绑定",
    },
    {
        "path": "internal/lingdoc/workspacecore/g1_operation_provenance.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": False,
        "defects": ("numbered", "numbered_identifiers"),
        "knowledge": "操作级来源凭证：可变草稿每次写操作的身份、正文哈希与不可变历史副本",
    },
    {
        "path": "internal/lingdoc/workspacecore/g1_provenance.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": False,
        "defects": ("numbered", "numbered_identifiers"),
        "knowledge": "项目来源快照：项目每个字段的来源标识、冻结版本与来源输入校验",
    },
    {
        "path": "internal/lingdoc/workspacecore/g1_provenance_history.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": False,
        "defects": ("numbered", "numbered_identifiers"),
        "knowledge": "来源变更历史：一次草稿变更的来源快照、继承字段、提议字段与反向来源键",
    },
    {
        "path": "internal/lingdoc/workspacecore/g1_provenance_service.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": False,
        "defects": ("numbered", "numbered_identifiers"),
        "knowledge": "来源凭证服务：准备、断言并在事务内校验项目来源快照",
    },
    {
        "path": "internal/lingdoc/workspacecore/g1_provenance_test.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("numbered", "numbered_identifiers"),
        "knowledge": "来源快照验收：来源须出自真实轮次、跨项目改写被拒、未知旧来源保留但不可激活",
    },
    {
        "path": "internal/lingdoc/workspacecore/g2_action_proposal_replay.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": False,
        "defects": ("numbered",),
        "knowledge": "动作提案重放：只对已完成的创建操作重建来源集合，再交回引擎重新授权回执",
    },
    {
        "path": "internal/lingdoc/workspacecore/g2_assistant_explanation.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": False,
        "defects": ("numbered",),
        "knowledge": "助手影响解释：某一轮建议影响了哪些对象、依据是什么、如何解码与判缺席",
    },
    {
        "path": "internal/lingdoc/workspacecore/g2_assistant_explanation_test.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "助手影响解释验收：沉默与缺席必须分开、无依据的建议不可信、载荷不可用一律拒绝",
    },
    {
        "path": "internal/lingdoc/workspacecore/g2_change_draft_action.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": False,
        "defects": ("numbered",),
        "knowledge": "变更草稿动作：把 Agent 的改写草稿解码成可审阅的候选建议",
    },
    {
        "path": "internal/lingdoc/workspacecore/g2_change_draft_edit.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": False,
        "defects": ("numbered",),
        "knowledge": "变更草稿编辑上下文：缺失旧来源按 AI 辅助处理，草稿编辑的证明与字段来源表",
    },
    {
        "path": "internal/lingdoc/workspacecore/g2_gate_declaration_test.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "门禁声明验收：研究域必须逐次声明它实际使用的那道门禁",
    },
    {
        "path": "internal/lingdoc/workspacecore/g2_model.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": False,
        "defects": ("numbered", "numbered_identifiers"),
        "knowledge": "Agent 会话与上下文包：会话、轮次、消息、来源引用、动作提案与证据卡的数据形状",
    },
    {
        "path": "internal/lingdoc/workspacecore/g2_service.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": False,
        "defects": ("numbered", "numbered_identifiers"),
        "knowledge": "Agent 动作服务：创建会话与提案、确认与导入、构建上下文包、研究任务的管控",
    },
    {
        "path": "internal/lingdoc/workspacecore/g2_service_test.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("numbered", "numbered_identifiers"),
        "knowledge": "Agent 动作服务验收：确认幂等、模型超时保持未知、撤销后会话清空、资产策略按修订固定",
    },
    {
        "path": "internal/lingdoc/workspacecore/g2_shared_pipeline_test.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("numbered", "numbered_identifiers"),
        "knowledge": "共享流水线验收：研究域行为须与共用流水线一致、机器身份被门禁拒绝、门禁清单不得私抄",
    },
    {
        "path": "internal/lingdoc/workspacecore/g2_storage.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": False,
        "defects": ("numbered", "numbered_identifiers"),
        "knowledge": "Agent 对象存储：把对象、版本、锚点与证据卡存进同一事务",
    },
    {
        "path": "internal/lingdoc/workspacecore/g3_preflight_refresh_policy_test.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "预检刷新策略验收：刷新必须拒绝过期或不匹配的证明，且不得产生写入",
    },
    {
        "path": "internal/lingdoc/workspacecore/g3_preflight_refresh_test.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "预检刷新验收：当前证明下刷新已完成的发布任务",
    },
    {
        "path": "internal/lingdoc/workspacecore/g4_issue_disposition_finding_test.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "问题处置验收：处置须按发现项绑定，而不是按问题编号绑定",
    },
    {
        "path": "internal/lingdoc/workspacecore/g4_issue_disposition_test.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "问题处置验收：撤销成员不得产生审计副作用、评审人只能处置自己范围内的阻断项",
    },
    {
        "path": "internal/lingdoc/workspacecore/g5_auth_review_regression_test.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("numbered", "numbered_identifiers"),
        "knowledge": "授权评审回归：空作者不得写全局规格、前任不得恢复旧作者范围、转移失败整体回滚",
    },
    {
        "path": "internal/lingdoc/workspacecore/g5_c1_contract_test.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "权限契约验收：类型化改动的权限契约矩阵",
    },
    {
        "path": "internal/lingdoc/workspacecore/g5_extended.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": False,
        "defects": ("numbered",),
        "knowledge": "成员与授权扩展：项目成员名录、邀请、Owner 转移决策与租户停用捕获",
    },
    {
        "path": "internal/lingdoc/workspacecore/g5_g7_firstpass_test.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "首轮权限验收：邀请须接受与重发版本、移除不恢复已撤销范围、租户恢复保持待处理",
    },
    {
        "path": "internal/lingdoc/workspacecore/g5_initialization_ruleset_test.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "初始化规则集验收：初始化使用带来源边注的有效规范规则集",
    },
    {
        "path": "internal/lingdoc/workspacecore/g5_lifecycle.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": False,
        "defects": ("numbered", "numbered_identifiers"),
        "knowledge": "成员生命周期：邀请、接受、成员状态变更、租户停用与激活回执",
    },
    {
        "path": "internal/lingdoc/workspacecore/g5_machine_read.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": False,
        "defects": ("numbered",),
        "knowledge": "机器只读授权：只读范围、授权人与来源策略构成的最小可读集合",
    },
    {
        "path": "internal/lingdoc/workspacecore/g5_machine_read_test.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "机器只读验收：只读授权不得开写、撤销与依赖缺失一律失败关闭",
    },
    {
        "path": "internal/lingdoc/workspacecore/g5_owner_transfer_proof.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": False,
        "defects": ("numbered",),
        "knowledge": "Owner 转移证明：转移发起人、恢复场景与当前 Owner 是否仍可用的判定",
    },
    {
        "path": "internal/lingdoc/workspacecore/g5_policy.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": False,
        "defects": ("numbered",),
        "knowledge": "统一权限判定：当前租户角色与有效权限的唯一判定点",
    },
    {
        "path": "internal/lingdoc/workspacecore/g6_delivery_guard.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": False,
        "defects": ("numbered",),
        "knowledge": "交付导出守卫：导出前校验运行态、来源依据与授权",
    },
    {
        "path": "internal/lingdoc/workspacecore/g6_runtime.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": False,
        "defects": ("numbered", "numbered_identifiers"),
        "knowledge": "运行控制与事件：暂停、恢复、指标、告警与运行记录的读写",
    },
    {
        "path": "internal/lingdoc/workspacecore/g6_trace.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": False,
        "defects": ("numbered",),
        "knowledge": "运行链路追踪：把运行操作上下文串成可观测的追踪",
    },
    {
        "path": "internal/lingdoc/workspacecore/g6_trial.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": False,
        "defects": ("numbered",),
        "knowledge": "试用窗口与样本：试用窗口、样本采集、判定签署与试用报告",
    },
    {
        "path": "internal/lingdoc/workspacecore/g7_review.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": False,
        "defects": ("numbered",),
        "knowledge": "章节评审与评论：章节评审状态流转、评审意见与评论存取",
    },
    {
        "path": "internal/lingdoc/workspacecore/seg3_context_ports.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": False,
        "defects": ("segment_number",),
        "knowledge": "按上下文分桶的窄口：把聚合体的方法按治理复核、智能体上下文等语境重新分门",
    },
    {
        "path": "internal/lingdoc/workspacecore/seg3_context_ports_test.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("segment_number",),
        "knowledge": "窄口方法表断言：每个窄口的方法签名逐字取自聚合体且不得私自增宽",
    },
    {
        "path": "internal/lingdoc/workspacecore/seg3_migrated_ports.go",
        "ticket": 362,
        "batch": 4,
        "legacy": True,
        "harness": False,
        "defects": ("segment_number",),
        "knowledge": "从传输层迁回的四条上下文窄口：项目、章节、模板与变更集各带自己那门语言",
    },
    {
        "path": "internal/types/interfaces/chat_attachment.go",
        "ticket": 369,
        "batch": 5,
        "legacy": False,
        "harness": False,
        "defects": ("weak_name",),
        "knowledge": "聊天附件接口定义：仓储与服务在类型层的契约",
    },
    {
        "path": "internal/types/chat_attachment.go",
        "ticket": 369,
        "batch": 5,
        "legacy": False,
        "harness": False,
        "defects": ("weak_name",),
        "knowledge": "聊天附件领域模型：聊天附件、分块、图片与任务载荷",
    },
    {
        "path": "scripts/acceptance/governance_real_http.py",
        "ticket": 369,
        "batch": 5,
        "legacy": False,
        "harness": True,
        "defects": ("ticket_number",),
        "knowledge": "真实 HTTP 治理验收脚本：跑一遍治理接口的项目、规则集绑定与发布导出链路",
    },
    {
        "path": "scripts/lingdoc_mock/g6_evidence.py",
        "ticket": 367,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "质量证据形状：各运行模式共享的证据结构与运行依赖声明",
    },
    {
        "path": "scripts/lingdoc_mock/g6_main_path.py",
        "ticket": 367,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "主路径证据：在既有主路径 runner 上包一层失败侧证据要求",
    },
    {
        "path": "scripts/lingdoc_mock/g6_metrics.py",
        "ticket": 367,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "可审计的试用窗口指标：带样本量与分母护栏的指标计算",
    },
    {
        "path": "scripts/lingdoc_mock/g6_observability.py",
        "ticket": 367,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "可观测性契约：运行证据里必须出现的确定性观测字段",
    },
    {
        "path": "scripts/lingdoc_mock/g6_rollback.py",
        "ticket": 367,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "迁移、灰度发布与回滚的演练闸门：判断回滚路径是否可执行",
    },
    {
        "path": "scripts/lingdoc_mock/g6_run.py",
        "ticket": 367,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "运行记录：把各闸门报告装配成一份确定且脱敏的运行记录",
    },
    {
        "path": "scripts/lingdoc_mock/g6_security.py",
        "ticket": 367,
        "batch": 4,
        "legacy": True,
        "harness": True,
        "defects": ("numbered",),
        "knowledge": "安全验收矩阵：负面路径矩阵，未提供实际状态码一律不算通过",
    },
)

#: Every numbered identifier defined in the code area.
NAMED_IDENTIFIERS: tuple[Mapping[str, Any], ...] = (
    {"name": "G2ActionExecutor", "scope": "exported", "file": "internal/lingdoc/workspace/agent_context.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2ActionExecutor", "scope": "exported", "file": "internal/lingdoc/workspacecore/g2_model.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2ActionKind", "scope": "exported", "file": "internal/lingdoc/workspacecore/g2_model.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2AgentModel", "scope": "exported", "file": "internal/lingdoc/workspacecore/g2_model.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2AnchorCurrent", "scope": "exported", "file": "internal/lingdoc/workspacecore/g2_storage.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2AnchorKind", "scope": "exported", "file": "internal/lingdoc/workspacecore/g2_model.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2AssetPolicyKind", "scope": "exported", "file": "internal/lingdoc/workspacecore/g2_model.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2AssetTruth", "scope": "exported", "file": "internal/lingdoc/workspacecore/g2_storage.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2AssetVersion", "scope": "exported", "file": "internal/lingdoc/workspacecore/g2_model.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2Basis", "scope": "exported", "file": "frontend/src/api/lingdoc/g2.ts", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2Basis", "scope": "exported", "file": "internal/lingdoc/workspacecore/g2_model.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2CardKind", "scope": "exported", "file": "internal/lingdoc/workspacecore/g2_model.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2ClaimKind", "scope": "exported", "file": "internal/lingdoc/workspacecore/g2_model.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2ConfirmedInput", "scope": "exported", "file": "frontend/src/api/lingdoc/g2.ts", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2ConfirmedInput", "scope": "exported", "file": "internal/lingdoc/workspacecore/g2_model.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2DependencyFact", "scope": "exported", "file": "internal/lingdoc/workspacecore/g2_model.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2EvidenceSelection", "scope": "exported", "file": "internal/lingdoc/workspacecore/g2_model.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2Expectation", "scope": "exported", "file": "frontend/src/api/lingdoc/g2.ts", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2Expectation", "scope": "exported", "file": "internal/lingdoc/workspacecore/g2_model.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2HostAgentModel", "scope": "exported", "file": "internal/lingdoc/workspace/agent_model.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2ImportKind", "scope": "exported", "file": "internal/lingdoc/workspacecore/g2_model.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2ImportProcessor", "scope": "exported", "file": "internal/lingdoc/workspacecore/g2_model.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2ImportedAsset", "scope": "exported", "file": "internal/lingdoc/workspacecore/g2_model.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2ModelReply", "scope": "exported", "file": "internal/lingdoc/workspacecore/g2_model.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2ModelRequest", "scope": "exported", "file": "internal/lingdoc/workspacecore/g2_model.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2Object", "scope": "exported", "file": "internal/lingdoc/workspacecore/g2_storage.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2ObjectVersion", "scope": "exported", "file": "internal/lingdoc/workspacecore/g2_storage.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2Objects", "scope": "exported", "file": "internal/lingdoc/workspacecore/g2_storage.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2OperationKeys", "scope": "exported", "file": "frontend/src/views/lingdoc/g2UiState.ts", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2ResearchAdapter", "scope": "exported", "file": "internal/lingdoc/workspace/research_adapter.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2ResearchCatalog", "scope": "exported", "file": "internal/lingdoc/workspacecore/g2_model.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2ResearchInspector", "scope": "exported", "file": "internal/lingdoc/workspacecore/g2_service.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2ResearchRoundIdentity", "scope": "exported", "file": "internal/lingdoc/workspacecore/g2_model.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2ResearchRunKind", "scope": "exported", "file": "internal/lingdoc/workspacecore/g2_model.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2ResearchSearcher", "scope": "exported", "file": "internal/lingdoc/workspacecore/g2_model.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2ResearchTaskKind", "scope": "exported", "file": "internal/lingdoc/workspacecore/g2_model.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2ResponseEpoch", "scope": "exported", "file": "frontend/src/views/lingdoc/g2UiState.ts", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2RoutePorts", "scope": "exported", "file": "internal/lingdoc/workspace/seg3_g2_ports.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2Runtime", "scope": "exported", "file": "internal/lingdoc/workspacecore/g2_model.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2SessionKind", "scope": "exported", "file": "internal/lingdoc/workspacecore/g2_model.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2Source", "scope": "exported", "file": "internal/lingdoc/workspacecore/g2_model.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2SourceReader", "scope": "exported", "file": "internal/lingdoc/workspacecore/g2_model.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2StoredObject", "scope": "exported", "file": "internal/lingdoc/workspacecore/g2_storage.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2Transaction", "scope": "exported", "file": "internal/lingdoc/workspacecore/g2_storage.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2TurnKind", "scope": "exported", "file": "internal/lingdoc/workspacecore/g2_model.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2VersionRef", "scope": "exported", "file": "frontend/src/api/lingdoc/g2.ts", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G2VersionRef", "scope": "exported", "file": "internal/lingdoc/workspacecore/g2_model.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G3AC0907Input", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/blocked.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3AC0909Input", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/blocked.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3AC0912Input", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/blocked.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3Ac06Fact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/model.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3Ac14Fact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/model.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3Ac15Fact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/model.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3AccountStorage", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/identity.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3AggregateExportInput", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Export.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3AssistantChainOptions", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/assistant.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3AssistantChainResult", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/assistant.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3AssistantChangeDraftCandidate", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/assistant.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3AssistantDraftFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/assistant.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3AssistantDraftOptions", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/assistant.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3AssistantDraftRun", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/assistant.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3AssistantExplanation", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/assistant.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3AssistantExplanationEntry", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/assistant.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3AssistantSnapshot", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/assistant.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3AssistantTurnFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/assistant.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3BackendHead", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/runContext.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3BlockedChainError", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/blocked.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3BlockedChainOptions", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/blocked.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3BlockedChainResult", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/blocked.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3BlockedSnapshot", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/blocked.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3CandidateChainFacts", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/dependencies.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3CandidateDecisionFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/assistant.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3CandidateDecisionFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/dependencies.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ChainError", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/httpClient.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ChainExportInput", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Export.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ChainOptions", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/chain.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ChainResult", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/chain.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ChainRunInput", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Export.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ChangeDraftSuggestion", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/model.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ChangeSetChainOptions", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/changeset.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ChangeSetChainResult", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/changeset.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ChangeSetFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/changeset.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ChapterFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/runContext.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ChapterSnapshot", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/changeset.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3CheckFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/stabilization.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3CheckIssueFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/stabilization.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3CheckWithIssuesFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/blocked.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ClosureEvidenceKind", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/stabilization.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ConclusionSource", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Export.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ContextRevisionFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/changeset.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3CurrentIdentity", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/identity.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3DeliveryClosureResult", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/stabilization.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3DemoAccount", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/identity.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3DependencyCandidateBatchFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/assistant.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3DependencyCandidateBatchFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/dependencies.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3DependencyCandidateFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/assistant.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3DependencyCandidateFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/dependencies.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3DependencyChainOptions", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/dependencies.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3DependencyChainResult", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/dependencies.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3DependencyEdgeFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/dependencies.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3DependencySnapshot", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/dependencies.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3DependencySuppressionFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/assistant.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3DependencySuppressionFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/dependencies.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3DismissAttempt", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/blocked.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3DismissBoundaryInput", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/blocked.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3DownloadFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/stabilization.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3EvidenceExport", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Export.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3EvidenceFileInput", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Export.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3EvidenceKind", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Matrix.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ExportArtifactFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/stabilization.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ExportEvidenceSource", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Export.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ExportGap", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Export.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ExportRow", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Export.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ExportRun", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Export.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ExportSource", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Export.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ExportVerdict", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Export.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3FailureFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/changeset.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3FakeClient", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/fakeHttp.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3FakeReply", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/fakeHttp.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3FaultApplyObservation", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/faultInjection.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3FaultAuditSnapshot", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/faultInjection.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3FaultChainOptions", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/faultInjection.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3FaultChainResult", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/faultInjection.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3FaultProjectSnapshot", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/faultInjection.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3FaultSnapshot", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/faultInjection.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3FaultTaskSnapshot", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/faultInjection.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3FlowStep", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Flow.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3GapTicketRegistryEntry", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Gaps.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3HttpClient", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/httpClient.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3HttpMethod", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/httpClient.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3HttpRequest", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/httpClient.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3HttpResponse", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/httpClient.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ImpactAssessmentFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/changeset.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ImpactCauseFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/dependencies.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ImpactGroupEntry", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/dependencies.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ImpactGrouping", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/dependencies.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ImpactObjectFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/changeset.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ImpactTaskAction", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/dependencies.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ImpactTaskDetailFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/dependencies.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ImpactTaskFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/changeset.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ImpactTaskStatus", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Status.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ImplementationGap", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Gaps.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3IssueDispositionFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/blocked.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3JudgedBy", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Matrix.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3LifecycleReading", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Matrix.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3LifecycleSnapshot", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/changeset.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3MatrixGroup", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Matrix.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3MatrixRow", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Matrix.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3MatrixWithRun", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Evidence.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ModelCandidate", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/model.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ModelChainOptions", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/model.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ModelChainResult", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/model.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ModelRunFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/model.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ModelSnapshot", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/model.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3NonOwnerOptions", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/changeset.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ObjectVersionRef", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/dependencies.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ObjectVersionRef", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/factHelpers.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3PermissionProbe", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/changeset.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ProjectChainOptions", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/project.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ProjectFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/project.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ProjectState", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/changeset.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ReceiptFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/stabilization.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3RegisteredIdentity", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/identity.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ReleaseSnapshotFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/stabilization.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ResolvedImpactFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/changeset.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ResponsibilityDomain", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Status.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ReviewProbeFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/blocked.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3RevisionObjects", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/model.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3RoadmapBaselineFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/roadmap.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3RoadmapChainOptions", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/roadmap.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3RoadmapChainResult", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/roadmap.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3RoadmapCommitFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/roadmap.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3RoadmapComparisonFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/roadmap.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3RoadmapEdgeFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/roadmap.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3RoadmapEdgeProposalFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/roadmap.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3RoadmapNodeFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/roadmap.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3RoadmapNodeProposalFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/roadmap.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3RoadmapRefFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/roadmap.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3RoadmapSessionFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/roadmap.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3RoadmapSnapshot", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/roadmap.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3RoadmapStabilizeFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/roadmap.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3RoadmapTaskFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/roadmap.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3RoadmapVersionFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/roadmap.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3RowEvidence", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Evidence.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3RowEvidenceAlternative", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Evidence.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3RowEvidenceRule", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Evidence.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3RuleIssueDispositionRecord", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/blocked.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3RuleIssueFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/blocked.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3RunContext", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/runContext.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3RunExportInput", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Export.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3RunStorage", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Evidence.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3RunSummary", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Evidence.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3RunVerdict", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/runContext.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3SatisfiedReading", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Matrix.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3Severity", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Status.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3Snapshot", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Snapshot.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3SnapshotChangeSet", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Snapshot.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3SnapshotExport", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Snapshot.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3SnapshotIdentity", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Snapshot.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3SnapshotProject", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Snapshot.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3SnapshotRaw", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Snapshot.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3SnapshotRelease", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Snapshot.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3SnapshotStep", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Snapshot.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3SnapshotTask", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Snapshot.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3StabilizationBlocker", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/stabilization.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3StabilizationChainError", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/stabilization.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3StabilizationChainOptions", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/stabilization.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3StabilizationChainResult", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/stabilization.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3StabilizationFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/stabilization.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3StabilizationPhaseResult", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/stabilization.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3StabilizationSnapshot", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/stabilization.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3StabilizeOutcome", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/blocked.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3Status", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Status.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3StatusTone", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Status.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3StepRecord", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/runContext.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3StepSpec", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/runContext.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3TaskSemantics", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/blocked.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3TaskTransition", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/dependencies.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3TemplateCheckFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/stabilization.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3Tolerated", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/roadmap.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3ToleratedFailure", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/roadmap.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3TurnFact", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/model.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3Verdict", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/changeset.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_ACCOUNT_STORAGE_KEY", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/identity.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_AC_ROWS", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Matrix.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_ASSISTANT_BASIS_TYPES", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/assistant.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_ASSISTANT_DRAFT_USER_INPUT", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/assistant.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_ASSISTANT_FORBIDDEN_PATHS", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/assistant.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_ASSISTANT_FORBIDDEN_PATTERNS", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/model.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_ASSISTANT_STEPS", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/assistant.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_BLOCKED_STEPS", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/blocked.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_CONCLUSION_SOURCE_LABELS", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Export.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_CS_STEPS", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/changeset.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_DEMO_REQUIRED_FIELDS", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/project.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_DEMO_REQUIRED_ROUTE_NODE_ID", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/project.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_DEMO_REQUIRED_SECTIONS", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/project.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_DEMO_ROUTE_TEMPLATE_VERSION", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/project.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_DEMO_TEMPLATE_ID", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/project.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_DEMO_TEMPLATE_VERSION", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/project.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_DEP_STEPS", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/dependencies.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_DOMAIN_DESCRIPTIONS", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Status.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_EVIDENCE_DIR", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Export.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_EVIDENCE_FILE_PREFIX", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Export.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_EVIDENCE_TICKET", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Export.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_EXPORT_GAP_ISSUE", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Export.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_FAULT_FIXTURE_EVIDENCE", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/faultInjection.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_FAULT_POINTS", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/faultInjection.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_FAULT_STEPS", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/faultInjection.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_FIRST_RELEASE_REVIEW_PATH", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Matrix.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_FLOW_EXECUTION_STATE", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Flow.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_FLOW_STEPS", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Flow.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_GAP_LIST_ISSUE", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Gaps.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_GAP_TICKET_REGISTRY", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Gaps.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_IDEMPOTENCY_EXEMPT_SUFFIXES", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/httpClient.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_IDEMPOTENCY_MAX_LENGTH", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/httpClient.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_IDEMPOTENCY_MIN_LENGTH", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/httpClient.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_IMPLEMENTATION_GAPS", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Gaps.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_INJECTED_FAULT_CODE", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/faultInjection.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_INJECTED_FAULT_STATUS", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/faultInjection.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_LIFECYCLE_READINGS", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Matrix.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_LIFECYCLE_SPEC_PATH", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Matrix.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_LINGDOC_PREFIX", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/httpClient.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_MAIN_BASELINE_PATH", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Matrix.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_MATRIX_COLUMNS", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Matrix.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_MATRIX_ROWS", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Matrix.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_MODEL_EDGE_ACTIONS", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/model.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_MODEL_STEPS", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/model.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_RESPONSIBILITY_DOMAINS", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Status.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_ROADMAP_STEPS", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/roadmap.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_ROW_EVIDENCE_RULES", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Evidence.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_RUN_HISTORY_LIMIT", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Evidence.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_RUN_HISTORY_STORAGE_KEY", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Evidence.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_RUN_SUMMARY_STORAGE_KEY", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Evidence.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_SATISFIED_READING", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Matrix.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_SEVERITIES", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Status.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_SEVERITY_DESCRIPTIONS", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Status.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_SLICE_ROWS", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Matrix.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_SNAPSHOT_DISCLAIMER", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Snapshot.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_SNAPSHOT_HEAD", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Snapshot.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_SNAPSHOT_RAW", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Snapshot.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_SNAPSHOT_RUNTIME_COMMIT", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Snapshot.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_SNAPSHOT_SOURCE_DIR", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Snapshot.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_STAB_STEPS", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/runner/stabilization.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_STATUS_VALUES", "scope": "exported", "file": "frontend/src/views/lingdoc/g3demo/g3Status.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G5Get", "scope": "exported", "file": "internal/lingdoc/workspacecore/g5_lifecycle.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G5List", "scope": "exported", "file": "internal/lingdoc/workspacecore/g5_lifecycle.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G5Put", "scope": "exported", "file": "internal/lingdoc/workspacecore/g5_lifecycle.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G6CAS", "scope": "exported", "file": "internal/lingdoc/workspacecore/g6_runtime.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G6EvidenceTest", "scope": "exported", "file": "scripts/lingdoc_mock/test_g6_quality.py", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G6Insert", "scope": "exported", "file": "internal/lingdoc/workspacecore/g6_runtime.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G6List", "scope": "exported", "file": "internal/lingdoc/workspacecore/g6_runtime.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G6MainPathTest", "scope": "exported", "file": "scripts/lingdoc_mock/test_g6_quality.py", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G6MetricsTest", "scope": "exported", "file": "scripts/lingdoc_mock/test_g6_quality.py", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G6ObservabilityTest", "scope": "exported", "file": "scripts/lingdoc_mock/test_g6_quality.py", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G6Read", "scope": "exported", "file": "internal/lingdoc/workspacecore/g6_runtime.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "G6RollbackTest", "scope": "exported", "file": "scripts/lingdoc_mock/test_g6_quality.py", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G6RunTest", "scope": "exported", "file": "scripts/lingdoc_mock/test_g6_quality.py", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G6SecurityTest", "scope": "exported", "file": "scripts/lingdoc_mock/test_g6_quality.py", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "T15MatrixTest", "scope": "exported", "file": "scripts/lingdoc_mock/test_workflow_matrix.py", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3GroupEvaluation", "scope": "private", "file": "frontend/src/views/lingdoc/g3demo/g3Evidence.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3RowEvaluation", "scope": "private", "file": "frontend/src/views/lingdoc/g3demo/g3Evidence.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3RowProjection", "scope": "private", "file": "frontend/src/views/lingdoc/g3demo/g3Evidence.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "G3_SLICE_EVIDENCE_FILE", "scope": "private", "file": "frontend/src/views/lingdoc/g3demo/g3Matrix.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "T15_SCENARIO_IDS", "scope": "private", "file": "scripts/lingdoc_mock/build_t15_matrix.py", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "g1AIStrings", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_provenance.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1ActionProjection", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_inherited_fields.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1AssertActionProvenance", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_inherited_fields.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1CachedChangeProjection", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_operation_provenance.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1CachedOperationProvenanceKey", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_operation_provenance.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1ChangeProjection", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_provenance_history.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1ChangeProvenance", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_provenance_history.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1DraftProjection", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_provenance_service.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1FieldRequests", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_provenance.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1IdentityMatches", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_provenance.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1LoadPin", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_provenance.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1MutableChangeReceipt", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_operation_provenance.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1NativeFixture", "scope": "private", "file": "internal/lingdoc/workspace/g1_provenance_oneconn_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1NativeProvenanceFixture", "scope": "private", "file": "internal/lingdoc/workspace/g1_provenance_oneconn_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1NativeSourceDigest", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_provenance.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1NativeSourceScope", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_provenance.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1OperationProjection", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_provenance_service.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1OperationProvenance", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_operation_provenance.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1OperationProvenanceID", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_operation_provenance.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1OperationProvenanceKind", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_operation_provenance.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1PersistSnapshot", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_provenance_history.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1PinCurrent", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_provenance.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1Prepare", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_provenance_service.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1PrepareCachedChangeOperation", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_operation_provenance.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1PrepareChangeProvenance", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_provenance_history.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1PrepareChangeProvenanceHistory", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_provenance_history.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1PrepareOperation", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_provenance_service.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1PreviewProjection", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_provenance_history.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1ProjectHash", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_provenance.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1ProjectProjection", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_provenance_service.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1ProvenancePin", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_provenance.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1ProvenanceProof", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_provenance.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1ProvenanceProofKey", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_provenance.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1ProvenanceRequest", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_provenance.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1RecordChangeProvenance", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_provenance_history.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1RecordKey", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_provenance.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1RedactProject", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_provenance.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1RedactRecord", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_provenance.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1RequestKey", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_provenance.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1ResolvePin", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_provenance.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1RestoreInheritedFields", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_inherited_fields.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1ReverseSourceKey", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_provenance_history.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1SeedActualTurn", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_provenance_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1ServerProvenance", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_provenance_service.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1Snapshot", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_provenance.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1SnapshotDigest", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_provenance.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1SourceContext", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_provenance.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1SourceIOKey", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_provenance.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1SourceVersion", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_provenance.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1StorageSnapshot", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_provenance.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1StoreActionProvenance", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_inherited_fields.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1StoreOperationPins", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_provenance_service.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1StoreOperationProvenance", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_operation_provenance.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1StoreReverseSources", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_provenance_history.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1TemplateReader", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_baseline_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1UnknownPin", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_provenance.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1ValidateOperation", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_provenance_service.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g1VersionMatches", "scope": "private", "file": "internal/lingdoc/workspacecore/g1_provenance.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2ActionCapability", "scope": "private", "file": "internal/lingdoc/workspacecore/g2_service.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2ActionInput", "scope": "private", "file": "internal/lingdoc/workspace/agent_context.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2AdapterDB", "scope": "private", "file": "internal/lingdoc/workspace/research_adapter_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2AdapterFetcher", "scope": "private", "file": "internal/lingdoc/workspace/research_adapter_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2AdapterHash", "scope": "private", "file": "internal/lingdoc/workspace/research_adapter.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2AdapterProjects", "scope": "private", "file": "internal/lingdoc/workspace/research_adapter_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2AdapterSources", "scope": "private", "file": "internal/lingdoc/workspace/research_adapter_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2AdapterWeb", "scope": "private", "file": "internal/lingdoc/workspace/research_adapter_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2AgentTurnGate", "scope": "private", "file": "internal/lingdoc/workspacecore/g2_service.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2AssetPolicyTransaction", "scope": "private", "file": "internal/lingdoc/workspacecore/g2_storage.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2AssetTruth", "scope": "private", "file": "internal/lingdoc/workspacecore/g2_storage.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2Audit", "scope": "private", "file": "internal/lingdoc/workspacecore/g2_service.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2BasisCurrent", "scope": "private", "file": "internal/lingdoc/workspacecore/g2_service.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2BasisFor", "scope": "private", "file": "internal/lingdoc/workspacecore/g2_service.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2BoundProject", "scope": "private", "file": "internal/lingdoc/workspace/g2_generation_evidence_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2CallerContext", "scope": "private", "file": "internal/lingdoc/workspace/research_adapter.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2CardConfirmationCapability", "scope": "private", "file": "internal/lingdoc/workspacecore/g2_service.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2CardConfirmationCurrent", "scope": "private", "file": "internal/lingdoc/workspacecore/g2_storage.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2ChangingModel", "scope": "private", "file": "internal/lingdoc/generation/g2_evidence_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2ChangingRewriteModel", "scope": "private", "file": "internal/lingdoc/workspace/g2_generation_evidence_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2CheckConfirmed", "scope": "private", "file": "internal/lingdoc/workspacecore/g2_service.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2ConfirmationTupleCurrent", "scope": "private", "file": "internal/lingdoc/workspacecore/g2_service.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2Decode", "scope": "private", "file": "internal/lingdoc/workspacecore/g2_service_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2EmbeddedSources", "scope": "private", "file": "internal/lingdoc/workspacecore/g2_service.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2EvidenceConstraints", "scope": "private", "file": "internal/lingdoc/workspace/g2_generation_evidence.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2EvidenceIncomplete", "scope": "private", "file": "internal/lingdoc/workspace/g2_surface.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2EvidenceModelInstructions", "scope": "private", "file": "internal/lingdoc/workspace/g2_generation_evidence.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2EvidenceRequest", "scope": "private", "file": "internal/lingdoc/generation/g2_evidence_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2EvidenceTestInputs", "scope": "private", "file": "internal/lingdoc/generation/g2_evidence_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2ExpectationMatches", "scope": "private", "file": "internal/lingdoc/workspacecore/g2_service.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2Failure", "scope": "private", "file": "internal/lingdoc/workspacecore/g2_service.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2Fixture", "scope": "private", "file": "internal/lingdoc/workspacecore/g2_service_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2FormalChangeRequired", "scope": "private", "file": "internal/lingdoc/workspace/g2_surface.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2FormalGenerationFixture", "scope": "private", "file": "internal/lingdoc/generation/g2_evidence_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2FormalRewriteFixture", "scope": "private", "file": "internal/lingdoc/workspace/g2_generation_evidence_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2FullError", "scope": "private", "file": "internal/lingdoc/workspace/g2_full_http.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2FullIdentity", "scope": "private", "file": "internal/lingdoc/workspace/g2_full_http.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2FullPostRead", "scope": "private", "file": "internal/lingdoc/workspace/g2_full_http.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2FullRead", "scope": "private", "file": "internal/lingdoc/workspace/g2_full_http.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2FullWrite", "scope": "private", "file": "internal/lingdoc/workspace/g2_full_http.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2GenerationCurrentWorkspace", "scope": "private", "file": "internal/lingdoc/workspace/g2_generation_evidence_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2GenerationEvidenceValidator", "scope": "private", "file": "internal/lingdoc/workspace/g2_generation_evidence.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2GenerationRefs", "scope": "private", "file": "internal/lingdoc/workspace/g2_generation_evidence.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2Hash", "scope": "private", "file": "internal/lingdoc/workspacecore/g2_service.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2HumanAuthorizer", "scope": "private", "file": "internal/lingdoc/generation/g2_evidence_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2IDs", "scope": "private", "file": "internal/lingdoc/workspacecore/g2_service.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2ImportStatusReader", "scope": "private", "file": "internal/lingdoc/workspacecore/g2_service.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2Load", "scope": "private", "file": "internal/lingdoc/workspacecore/g2_storage.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2MachineContext", "scope": "private", "file": "internal/lingdoc/workspacecore/g2_shared_pipeline_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2ObjectFromRow", "scope": "private", "file": "internal/lingdoc/workspacecore/g2_storage.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2ObjectRow", "scope": "private", "file": "internal/lingdoc/workspacecore/g2_storage.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2ObjectVersionRow", "scope": "private", "file": "internal/lingdoc/workspacecore/g2_storage.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2Operation", "scope": "private", "file": "internal/lingdoc/workspacecore/g2_service.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2OriginalFetcher", "scope": "private", "file": "internal/lingdoc/workspace/research_adapter.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2OutcomeUnknown", "scope": "private", "file": "internal/lingdoc/workspace/g2_surface.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2PolicyGateway", "scope": "private", "file": "internal/lingdoc/workspace/research_adapter.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2PolicyProposalInput", "scope": "private", "file": "internal/lingdoc/workspace/g2_full_http.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2PortsFor", "scope": "private", "file": "internal/lingdoc/workspace/seg3_g2_ports_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2PublishCardDependencies", "scope": "private", "file": "internal/lingdoc/workspacecore/g2_service.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2Read", "scope": "private", "file": "internal/lingdoc/workspacecore/g2_service.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2RewriteModelScope", "scope": "private", "file": "internal/lingdoc/workspace/g2_generation_evidence.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2RewriteProjectBasis", "scope": "private", "file": "internal/lingdoc/workspace/g2_generation_evidence.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2RewriteTemplateBasis", "scope": "private", "file": "internal/lingdoc/workspace/g2_generation_evidence.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2RewriteWorkspace", "scope": "private", "file": "internal/lingdoc/workspace/g2_generation_evidence_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2ScopedAuthorizer", "scope": "private", "file": "internal/lingdoc/generation/g2_evidence_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2SeedNativeEvidence", "scope": "private", "file": "internal/lingdoc/workspacecore/g2_service_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2SelectFormal", "scope": "private", "file": "internal/lingdoc/workspace/g2_generation_evidence.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2Storage", "scope": "private", "file": "internal/lingdoc/workspacecore/g2_storage.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2Store", "scope": "private", "file": "internal/lingdoc/workspacecore/g2_storage.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2SurfaceError", "scope": "private", "file": "internal/lingdoc/workspace/g2_surface.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2TestActionExecutor", "scope": "private", "file": "internal/lingdoc/workspacecore/g2_service_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2TestEvidenceValidator", "scope": "private", "file": "internal/lingdoc/generation/g2_evidence_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2TestImporter", "scope": "private", "file": "internal/lingdoc/workspacecore/g2_service_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2TestModel", "scope": "private", "file": "internal/lingdoc/workspacecore/g2_service_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2TestModelContext", "scope": "private", "file": "internal/lingdoc/workspace/agent_model_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2TestSearcher", "scope": "private", "file": "internal/lingdoc/workspacecore/g2_service_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2TestSourceReader", "scope": "private", "file": "internal/lingdoc/workspacecore/g2_service_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2Unavailable", "scope": "private", "file": "internal/lingdoc/workspace/g2_surface.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2ValidMode", "scope": "private", "file": "internal/lingdoc/workspacecore/g2_service.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g2ValidURL", "scope": "private", "file": "internal/lingdoc/workspacecore/g2_service.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g32ResultOf", "scope": "private", "file": "frontend/src/views/lingdoc/g3demo/runner/dependencies.test.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "g34ResultOf", "scope": "private", "file": "frontend/src/views/lingdoc/g3demo/runner/blocked.test.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "g35ResultOf", "scope": "private", "file": "frontend/src/views/lingdoc/g3demo/runner/roadmap.test.ts", "legacy": False, "harness": True, "random_fixture": False},
    {"name": "g4ActivateProject", "scope": "private", "file": "internal/lingdoc/workspace/template_governance_adapter_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g4AdapterChapterAuthorizer", "scope": "private", "file": "internal/lingdoc/workspace/template_governance_adapter_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g4AdapterFixture", "scope": "private", "file": "internal/lingdoc/workspace/template_governance_adapter_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g4AdapterFixtureWithProjects", "scope": "private", "file": "internal/lingdoc/workspace/template_governance_adapter_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g4AdapterShares", "scope": "private", "file": "internal/lingdoc/workspace/template_governance_adapter_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g4AdapterSource", "scope": "private", "file": "internal/lingdoc/workspace/template_governance_adapter_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g4AuditlessRepository", "scope": "private", "file": "internal/lingdoc/workspacecore/change_set_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g4AuditlessSourcePorts", "scope": "private", "file": "internal/lingdoc/workspacecore/change_set_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g4AuditlessTransaction", "scope": "private", "file": "internal/lingdoc/workspacecore/change_set_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g4BridgeModel", "scope": "private", "file": "internal/lingdoc/workspace/template_governance_adapter_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g4CallerContext", "scope": "private", "file": "internal/lingdoc/workspace/template_governance_runtime.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g4CoreActor", "scope": "private", "file": "internal/lingdoc/workspace/template_governance_runtime.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g4E2EActorID", "scope": "private", "file": "internal/container/lingdoc_ruleset_delivery_e2e_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g4E2EEnvelope", "scope": "private", "file": "internal/container/lingdoc_ruleset_delivery_e2e_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g4E2EHTTP", "scope": "private", "file": "internal/container/lingdoc_ruleset_delivery_e2e_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g4E2EPostgresDSN", "scope": "private", "file": "internal/container/lingdoc_ruleset_delivery_e2e_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g4E2ERuleSet", "scope": "private", "file": "internal/container/lingdoc_ruleset_delivery_e2e_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g4E2ETenantID", "scope": "private", "file": "internal/container/lingdoc_ruleset_delivery_e2e_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g4E2EVersion", "scope": "private", "file": "internal/container/lingdoc_ruleset_delivery_e2e_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g4FullHTTP", "scope": "private", "file": "internal/lingdoc/workspace/g4_full_http.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g4GovActor", "scope": "private", "file": "internal/lingdoc/workspace/template_governance_runtime.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g4HTTPContext", "scope": "private", "file": "internal/lingdoc/workspace/g4_full_http_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g4HTTPRequest", "scope": "private", "file": "internal/lingdoc/workspace/g4_full_http_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g4PathBody", "scope": "private", "file": "internal/lingdoc/workspace/g4_full_http.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g4ProjectBasis", "scope": "private", "file": "internal/lingdoc/workspace/template_governance_runtime.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g4PublishDemoCatalog", "scope": "private", "file": "internal/lingdoc/workspace/template_governance_adapter_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g4ReadOnlyBindings", "scope": "private", "file": "internal/lingdoc/workspace/template_governance_runtime.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g4RuntimeDemoVersion", "scope": "private", "file": "internal/lingdoc/workspace/template_governance_adapter_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g4RuntimeHash", "scope": "private", "file": "internal/lingdoc/workspace/template_governance_runtime.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g4SaveDemoTemplateDraft", "scope": "private", "file": "internal/lingdoc/workspace/template_governance_adapter_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g4SeedDemoRuleSet", "scope": "private", "file": "internal/lingdoc/workspace/template_governance_adapter_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g4TenantKBReader", "scope": "private", "file": "internal/lingdoc/workspace/template_governance_runtime.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g4WriteConfirmedChapters", "scope": "private", "file": "internal/lingdoc/workspace/template_governance_adapter_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g5Audit", "scope": "private", "file": "internal/lingdoc/workspacecore/g5_lifecycle.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g5AuthReviewAcceptMember", "scope": "private", "file": "internal/lingdoc/workspacecore/g5_auth_review_regression_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g5AuthReviewBlockAudit", "scope": "private", "file": "internal/lingdoc/workspacecore/g5_auth_review_regression_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g5AuthReviewCount", "scope": "private", "file": "internal/lingdoc/workspacecore/g5_auth_review_regression_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g5AuthReviewDropAuditTrigger", "scope": "private", "file": "internal/lingdoc/workspacecore/g5_auth_review_regression_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g5AuthReviewOwner", "scope": "private", "file": "internal/lingdoc/workspacecore/g5_auth_review_regression_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g5AuthReviewProject", "scope": "private", "file": "internal/lingdoc/workspacecore/g5_auth_review_regression_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g5C1SaveSpecErrorStub", "scope": "private", "file": "internal/lingdoc/workspace/g5_c1_contract_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g5RecordRow", "scope": "private", "file": "internal/lingdoc/workspacecore/g5_lifecycle.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g5Transaction", "scope": "private", "file": "internal/lingdoc/workspacecore/g5_lifecycle.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "g6RecordRow", "scope": "private", "file": "internal/lingdoc/workspacecore/g6_runtime.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "t0", "scope": "private", "file": "frontend/src/components/knowledge-processing-timeline.vue", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "t09AssertEvidenceFixture", "scope": "private", "file": "internal/lingdoc/workspace/assets_http_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "t09Call", "scope": "private", "file": "internal/lingdoc/workspace/assets_http_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "t09Context", "scope": "private", "file": "internal/lingdoc/workspace/assets_http_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "t09Data", "scope": "private", "file": "internal/lingdoc/workspace/assets_http_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "t09Decode", "scope": "private", "file": "internal/lingdoc/workspace/assets_http_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "t09EditedNeighbour", "scope": "private", "file": "internal/lingdoc/workspace/assets_http_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "t09Envelope", "scope": "private", "file": "internal/lingdoc/workspace/assets_http_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "t09ErrorCode", "scope": "private", "file": "internal/lingdoc/workspace/assets_http_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "t09Expect", "scope": "private", "file": "internal/lingdoc/workspace/assets_http_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "t09Origin", "scope": "private", "file": "internal/lingdoc/workspace/assets_http_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "t09OriginLines", "scope": "private", "file": "internal/lingdoc/workspace/assets_http_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "t09ParentSentinel", "scope": "private", "file": "internal/lingdoc/workspace/assets_http_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "t09Request", "scope": "private", "file": "internal/lingdoc/workspace/assets_http_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "t09RouteBase", "scope": "private", "file": "internal/lingdoc/workspace/assets_http_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "t09S4EvidencePath", "scope": "private", "file": "internal/lingdoc/workspace/assets_http_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "t09S4MinimumCases", "scope": "private", "file": "internal/lingdoc/workspace/assets_http_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "t09Serve", "scope": "private", "file": "internal/lingdoc/workspace/assets_http_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "t09SourceID", "scope": "private", "file": "internal/lingdoc/workspace/assets_http_test.go", "legacy": True, "harness": False, "random_fixture": False},
    {"name": "t09Span", "scope": "private", "file": "internal/lingdoc/workspace/assets_http_test.go", "legacy": True, "harness": False, "random_fixture": False},
)


# ---------------------------------------------------------------------------
# The scanner: the mechanical application of the policy above
# ---------------------------------------------------------------------------

_GO_TOP_LEVEL = re.compile(r"^(?:func|type|var|const)\s+(?:\([^)]*\)\s*)?([A-Za-z_]\w*)")
_GO_BLOCK_ENTRY = re.compile(r"^\s+([A-Za-z_]\w*)\s*(?:[A-Za-z_\[\]\*\.\w]*\s*)?=")
_TS_EXPORT = re.compile(
    r"^export\s+(?:declare\s+)?(?:default\s+)?"
    r"(?:const|let|var|function|async\s+function|class|interface|type|enum|namespace)\s+"
    r"([A-Za-z_$][\w$]*)")
_TS_EXPORT_LIST = re.compile(r"^export\s*\{([^}]*)\}")
_TS_LOCAL = re.compile(
    r"^(?:const|let|var|function|async\s+function|class|interface|type|enum|namespace)\s+"
    r"([A-Za-z_$][\w$]*)")
_PY_DEF = re.compile(r"^(?:async\s+)?def\s+([A-Za-z_]\w*)")
_PY_CLASS = re.compile(r"^class\s+([A-Za-z_]\w*)")
_PY_ASSIGN = re.compile(r"^([A-Za-z_]\w*)\s*(?::[^=]+)?=")

_SCAN_CACHE: dict[tuple[str, str], Any] = {}


def stem_of(rel: str) -> str:
    """The name of a path without its directory, its test suffix and its
    extension -- the part a human reads when they name a thing."""
    base = rel.rsplit("/", 1)[-1]
    base = TEST_SUFFIX.sub("", base)
    return base.rsplit(".", 1)[0]


def is_multiword(stem: str) -> bool:
    """Whether a name carries more than one word."""
    return bool(re.search(r"[A-Z]", stem[1:])) or "-" in stem or "_" in stem


def is_acceptance_rig(rel: str) -> bool:
    """Whether a path belongs to an acceptance rig or a test fixture tree."""
    return any(rel.startswith(prefix) for prefix in ACCEPTANCE_RIG_DIRS)


def is_harness_file(rel: str) -> bool:
    """Whether a path is acceptance-harness or test-fixture code."""
    if is_acceptance_rig(rel):
        return True
    base = rel.rsplit("/", 1)[-1]
    if base.endswith("_test.go") or base.endswith("_test.py"):
        return True
    if base.startswith("test_") and base.endswith(".py"):
        return True
    return bool(TEST_SUFFIX.search(base))


def file_defects(rel: str, content: str | None = None) -> tuple[str, ...]:
    """Which file-level rules this path breaks.

    ``content`` is the file's source.  It is optional, because the name-level
    rules can be asked about a path alone; the body-level rule -- a file
    whose own identifiers carry a work number -- needs the source and is
    therefore only decided when the content is given.
    """
    hits: list[str] = []
    segments = rel.split("/")
    if any(NUMBERED_NAME.match(segment.rsplit(".", 1)[0]) for segment in segments):
        hits.append("numbered")
    if any(SEGMENT_NAME.match(segment.rsplit(".", 1)[0]) for segment in segments):
        hits.append("segment_number")
    stem = stem_of(rel)
    if TICKET_NUMBER.search(stem):
        hits.append("ticket_number")
    if TASK_NUMBER.search(stem):
        hits.append("task_number")
    if content is not None and numbered_identifier_names(rel, content):
        hits.append("numbered_identifiers")
    if rel.startswith("frontend/") and not _frontend_style_exempt(rel):
        extension = rel.rsplit(".", 1)[-1]
        if "-" in stem:
            hits.append("frontend_kebab")
        if extension == "vue" and is_multiword(stem) and not re.match(r"^[A-Z][A-Za-z0-9]*$", stem):
            hits.append("frontend_vue_case")
        if (extension in ("ts", "tsx", "js", "jsx", "mjs")
                and not TEST_SUFFIX.search(rel)
                and not TYPE_DECLARATION.search(rel)
                and is_multiword(stem)
                and not re.match(r"^[a-z][A-Za-z0-9]*$", stem)):
            hits.append("frontend_camel")
    if any(pattern.search(stem) for pattern in WEAK_NAME_PATTERNS):
        hits.append("weak_name")
    if is_acceptance_rig(rel):
        hits = [hit for hit in hits if hit not in RIG_EXEMPT_RULES]
    return tuple(hits)


def _frontend_style_exempt(rel: str) -> bool:
    return any(rel.startswith(prefix) for prefix, _reason in FRONTEND_STYLE_EXEMPT)


def scan_code_files(root: Path) -> tuple[str, ...]:
    """Every code file in the code area, as repo-relative POSIX paths."""
    key = ("files", str(root))
    cached = _SCAN_CACHE.get(key)
    if cached is not None:
        return cached
    found: list[str] = []
    for area in CODE_AREA_ROOTS:
        base = Path(root) / area
        if not base.is_dir():
            continue
        for dirpath, dirnames, filenames in os.walk(base):
            dirnames[:] = sorted(d for d in dirnames if d not in SKIPPED_DIRECTORIES)
            for name in sorted(filenames):
                if Path(name).suffix in CODE_EXTENSIONS:
                    rel = Path(dirpath, name).relative_to(root).as_posix()
                    found.append(rel)
    result = tuple(sorted(found))
    _SCAN_CACHE[key] = result
    return result


def clear_scan_cache() -> None:
    """Forget every cached scan result.

    The guard test builds throw-away trees and asks the scanner about them;
    a stale cache would answer about the previous tree.
    """
    _SCAN_CACHE.clear()


def _read_text(path: Path) -> str:
    """The source of a file, or an empty string when it cannot be read."""
    try:
        return path.read_text(encoding="utf-8", errors="replace")
    except OSError:
        return ""


def scan_file_defects(root: Path) -> dict[str, tuple[str, ...]]:
    """Every code file that breaks at least one file-level rule."""
    key = ("defects", str(root))
    cached = _SCAN_CACHE.get(key)
    if cached is not None:
        return cached
    found: dict[str, tuple[str, ...]] = {}
    for rel in scan_code_files(root):
        defects = file_defects(rel, _read_text(Path(root) / rel))
        if defects:
            found[rel] = defects
    _SCAN_CACHE[key] = found
    return found


def _go_definitions(text: str) -> list[tuple[str, str]]:
    out: list[tuple[str, str]] = []
    in_block = False
    for line in text.splitlines():
        match = _GO_TOP_LEVEL.match(line)
        if match:
            name = match.group(1)
            out.append((name, "exported" if name[:1].isupper() else "private"))
            in_block = False
            continue
        if re.match(r"^(?:const|var)\s*\($", line):
            in_block = True
            continue
        if in_block:
            if line.startswith(")"):
                in_block = False
                continue
            match = _GO_BLOCK_ENTRY.match(line)
            if match:
                name = match.group(1)
                out.append((name, "exported" if name[:1].isupper() else "private"))
    return out


def _ts_definitions(text: str) -> list[tuple[str, str]]:
    out: list[tuple[str, str]] = []
    for line in text.splitlines():
        match = _TS_EXPORT.match(line)
        if match:
            out.append((match.group(1), "exported"))
            continue
        match = _TS_EXPORT_LIST.match(line)
        if match:
            for part in match.group(1).split(","):
                part = part.strip().replace("type ", "").strip()
                if " as " in part:
                    part = part.split(" as ")[-1].strip()
                if part:
                    out.append((part, "exported"))
            continue
        match = _TS_LOCAL.match(line)
        if match:
            out.append((match.group(1), "private"))
    return out


def _py_definitions(text: str) -> list[tuple[str, str]]:
    out: list[tuple[str, str]] = []
    for line in text.splitlines():
        match = _PY_DEF.match(line)
        if match:
            out.append((match.group(1), "private"))
            continue
        match = _PY_CLASS.match(line)
        if match:
            out.append((match.group(1), "exported"))
            continue
        match = _PY_ASSIGN.match(line)
        if match:
            out.append((match.group(1), "private"))
    return out


def definitions_of(rel: str, text: str) -> list[tuple[str, str]]:
    """The identifiers this file defines, each with its scope."""
    extension = rel.rsplit(".", 1)[-1]
    if extension == "go":
        return _go_definitions(text)
    if extension in ("ts", "tsx", "js", "jsx", "mjs", "vue"):
        return _ts_definitions(text)
    return _py_definitions(text)


def is_work_numbered(name: str) -> bool:
    """Whether an identifier carries a work number at its front.

    ``G5``/``T09`` name the product line or the task pool, ``seg3`` names the
    work segment.  All three say *which batch of work* the code came from;
    none of them says what the code knows.
    """
    return bool(NUMBERED_NAME.match(name) or SEGMENT_NAME.match(name))


def numbered_identifier_names(rel: str, content: str) -> list[str]:
    """The identifiers this file defines that carry a work number.

    This is what makes a file with a clean name but a numbered body a
    naming defect: the file owns those identifiers, so it needs an owner
    entry -- and an owner entry is what gives them a rename ticket.
    """
    return [name for name, _scope in definitions_of(rel, content)
            if is_work_numbered(name)]


def scan_identifier_definitions(root: Path) -> dict[tuple[str, str, str], None]:
    """Every numbered identifier defined in the code area.

    The key is ``(name, scope, defining file)``.  Only definition sites count:
    a reference in fifty files is one identifier.
    """
    key = ("identifiers", str(root))
    cached = _SCAN_CACHE.get(key)
    if cached is not None:
        return cached
    found: dict[tuple[str, str, str], None] = {}
    for rel in scan_code_files(root):
        text = _read_text(Path(root) / rel)
        for name, scope in definitions_of(rel, text):
            if not is_work_numbered(name):
                continue
            found[(name, scope, rel)] = None
    _SCAN_CACHE[key] = found
    return found


# ---------------------------------------------------------------------------
# The loader
# ---------------------------------------------------------------------------


class NamingRegistryError(ValueError):
    """Raised when the committed registry is invalid."""


@dataclass(frozen=True)
class NamedFile:
    """One code file the registry knows about."""

    path: str
    ticket: int
    batch: int
    legacy: bool
    harness: bool
    defects: tuple[str, ...]
    knowledge: str


@dataclass(frozen=True)
class NamedIdentifier:
    """One numbered identifier the registry knows about."""

    name: str
    scope: str
    file: str
    legacy: bool
    harness: bool
    random_fixture: bool


@dataclass(frozen=True)
class RatchetBaseline:
    """How much debt the repository is allowed to carry."""

    base_sha: str
    scope: str
    files: int
    exported_identifiers: int
    private_identifiers: int


def _require_fields(entry: Mapping[str, Any], fields: Iterable[str], what: str) -> None:
    missing = [field for field in fields if field not in entry]
    if missing:
        raise NamingRegistryError(f"{what}: missing field(s) {', '.join(missing)}")


def _require_text(value: Any, what: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise NamingRegistryError(f"{what}: expected non-empty text")
    return value


def _require_bool(value: Any, what: str) -> bool:
    if not isinstance(value, bool):
        raise NamingRegistryError(f"{what}: expected true or false")
    return value


def _require_repo_path(value: Any, what: str) -> str:
    path = _require_text(value, what)
    if path.startswith("/") or "\\" in path or ".." in path.split("/"):
        raise NamingRegistryError(f"{what}: expected a repo-relative POSIX path")
    return path


def _named_file_from_record(entry: Any, index: int) -> NamedFile:
    what = f"NAMED_FILES[{index}]"
    if not isinstance(entry, Mapping):
        raise NamingRegistryError(f"{what}: expected a mapping")
    _require_fields(
        entry,
        ("path", "ticket", "batch", "legacy", "harness", "defects", "knowledge"),
        what,
    )
    path = _require_repo_path(entry["path"], f"{what}.path")

    ticket = entry["ticket"]
    if not isinstance(ticket, int) or isinstance(ticket, bool) or ticket not in RENAME_TICKETS:
        raise NamingRegistryError(
            f"{what}.ticket: expected one of {', '.join(str(t) for t in RENAME_TICKETS)}")

    batch = entry["batch"]
    if not isinstance(batch, int) or isinstance(batch, bool) or batch not in BATCH_LABELS:
        raise NamingRegistryError(
            f"{what}.batch: expected one of {', '.join(str(b) for b in sorted(BATCH_LABELS))}")
    if BATCH_BY_TICKET.get(ticket) != batch:
        raise NamingRegistryError(
            f"{what}.batch: ticket #{ticket} belongs to batch "
            f"{BATCH_BY_TICKET.get(ticket)}, not {batch}")

    legacy = _require_bool(entry["legacy"], f"{what}.legacy")
    harness = _require_bool(entry["harness"], f"{what}.harness")

    defects_raw = entry["defects"]
    if not isinstance(defects_raw, (list, tuple)) or not defects_raw:
        raise NamingRegistryError(f"{what}.defects: expected a non-empty list")
    defects: list[str] = []
    for defect in defects_raw:
        if defect not in FILE_DEFECTS:
            raise NamingRegistryError(
                f"{what}.defects: {defect!r} is not one of {', '.join(FILE_DEFECTS)}")
        defects.append(defect)

    knowledge = _require_text(entry["knowledge"], f"{what}.knowledge")
    if len(knowledge) < MIN_KNOWLEDGE_LENGTH:
        raise NamingRegistryError(
            f"{what}.knowledge: a knowledge statement has to say what the file owns, "
            f"not just point at it ({knowledge!r})")
    for vacuous in VACUOUS_KNOWLEDGE:
        if vacuous in knowledge:
            raise NamingRegistryError(
                f"{what}.knowledge: {knowledge!r} says nothing about what the file owns")

    return NamedFile(
        path=path,
        ticket=ticket,
        batch=batch,
        legacy=legacy,
        harness=harness,
        defects=tuple(defects),
        knowledge=knowledge,
    )


def _named_identifier_from_record(entry: Any, index: int) -> NamedIdentifier:
    what = f"NAMED_IDENTIFIERS[{index}]"
    if not isinstance(entry, Mapping):
        raise NamingRegistryError(f"{what}: expected a mapping")
    _require_fields(
        entry,
        ("name", "scope", "file", "legacy", "harness", "random_fixture"),
        what,
    )
    name = _require_text(entry["name"], f"{what}.name")
    if not is_work_numbered(name):
        raise NamingRegistryError(
            f"{what}.name: {name!r} does not start with a work number")

    scope = _require_text(entry["scope"], f"{what}.scope")
    if scope not in IDENTIFIER_SCOPES:
        raise NamingRegistryError(
            f"{what}.scope: {scope!r} is not one of {', '.join(IDENTIFIER_SCOPES)}")

    file = _require_repo_path(entry["file"], f"{what}.file")
    legacy = _require_bool(entry["legacy"], f"{what}.legacy")
    harness = _require_bool(entry["harness"], f"{what}.harness")
    random_fixture = _require_bool(entry["random_fixture"], f"{what}.random_fixture")

    if harness and not is_acceptance_rig(file):
        raise NamingRegistryError(
            f"{what}.harness: only identifiers inside an acceptance rig may be "
            f"declared as harness vocabulary ({file!r})")
    if random_fixture and name not in RANDOM_FIXTURE_TOKENS:
        raise NamingRegistryError(
            f"{what}.random_fixture: {name!r} is not a known random fixture string")
    if legacy != (not (harness or random_fixture)):
        raise NamingRegistryError(
            f"{what}.legacy: an identifier is legacy exactly when it is neither "
            f"harness vocabulary nor a random fixture string")

    return NamedIdentifier(
        name=name,
        scope=scope,
        file=file,
        legacy=legacy,
        harness=harness,
        random_fixture=random_fixture,
    )


def _baseline_from_record(entry: Any) -> RatchetBaseline:
    what = "RATCHET_BASELINE"
    if not isinstance(entry, Mapping):
        raise NamingRegistryError(f"{what}: expected a mapping")
    _require_fields(entry, ("base_sha", "scope", "files",
                            "exported_identifiers", "private_identifiers"), what)
    base_sha = _require_text(entry["base_sha"], f"{what}.base_sha")
    scope = _require_text(entry["scope"], f"{what}.scope")
    counts = {}
    for field in ("files", "exported_identifiers", "private_identifiers"):
        value = entry[field]
        if not isinstance(value, int) or isinstance(value, bool) or value < 0:
            raise NamingRegistryError(f"{what}.{field}: expected a non-negative integer")
        counts[field] = value
    return RatchetBaseline(
        base_sha=base_sha,
        scope=scope,
        files=counts["files"],
        exported_identifiers=counts["exported_identifiers"],
        private_identifiers=counts["private_identifiers"],
    )


def validate_records(
    files: Sequence[Mapping[str, Any]],
    identifiers: Sequence[Mapping[str, Any]],
    baseline: Mapping[str, Any],
) -> tuple[tuple[NamedFile, ...], tuple[NamedIdentifier, ...], RatchetBaseline]:
    """Validate raw registry records into entries, or raise."""
    named_files = tuple(_named_file_from_record(entry, index)
                        for index, entry in enumerate(files))
    named_identifiers = tuple(_named_identifier_from_record(entry, index)
                              for index, entry in enumerate(identifiers))
    ratchet = _baseline_from_record(baseline)

    seen_paths: set[str] = set()
    for entry in named_files:
        if entry.path in seen_paths:
            raise NamingRegistryError(f"path {entry.path!r} is declared twice")
        seen_paths.add(entry.path)

    seen_knowledge: dict[str, str] = {}
    for entry in named_files:
        other = seen_knowledge.get(entry.knowledge)
        if other is not None:
            raise NamingRegistryError(
                f"{entry.path!r} and {other!r} both claim the knowledge "
                f"{entry.knowledge!r}; one piece of knowledge has one owner file")
        seen_knowledge[entry.knowledge] = entry.path

    seen_identifiers: set[tuple[str, str, str]] = set()
    for entry in named_identifiers:
        key = (entry.name, entry.scope, entry.file)
        if key in seen_identifiers:
            raise NamingRegistryError(
                f"identifier {entry.name!r} ({entry.scope}) in {entry.file!r} is declared twice")
        seen_identifiers.add(key)

    legacy_files = sum(1 for entry in named_files if entry.legacy)
    # One entry is one definition site.  Two packages may export the same
    # name (``G2Expectation`` exists in both ``workspacecore`` and
    # ``workspace``); they are two identifiers with two definitions, so the
    # ratchet counts definition sites, not distinct spellings.
    legacy_exported = sum(1 for entry in named_identifiers
                          if entry.legacy and entry.scope == "exported")
    legacy_private = sum(1 for entry in named_identifiers
                         if entry.legacy and entry.scope == "private")

    if legacy_files > ratchet.files:
        raise NamingRegistryError(
            f"the legacy file segment grew: {legacy_files} entries against a "
            f"baseline of {ratchet.files}; lower the baseline in the same change "
            f"that repays the debt")
    if legacy_exported > ratchet.exported_identifiers:
        raise NamingRegistryError(
            f"the legacy exported-identifier segment grew: {legacy_exported} entries "
            f"against a baseline of {ratchet.exported_identifiers}")
    if legacy_private > ratchet.private_identifiers:
        raise NamingRegistryError(
            f"the legacy private-identifier segment grew: {legacy_private} entries "
            f"against a baseline of {ratchet.private_identifiers}")

    if (legacy_files, legacy_exported, legacy_private) != (
            ratchet.files, ratchet.exported_identifiers, ratchet.private_identifiers):
        raise NamingRegistryError(
            f"the ratchet baseline does not match the registry: baseline "
            f"{ratchet.files}/{ratchet.exported_identifiers}/"
            f"{ratchet.private_identifiers} against actual "
            f"{legacy_files}/{legacy_exported}/{legacy_private}; the baseline is "
            f"lowered in the same change that clears a segment")

    return named_files, named_identifiers, ratchet


def load_and_validate(
    files: Sequence[Mapping[str, Any]] | None = None,
    identifiers: Sequence[Mapping[str, Any]] | None = None,
    baseline: Mapping[str, Any] | None = None,
) -> tuple[tuple[NamedFile, ...], tuple[NamedIdentifier, ...], RatchetBaseline]:
    """Load the committed registry and validate it."""
    return validate_records(
        NAMED_FILES if files is None else files,
        NAMED_IDENTIFIERS if identifiers is None else identifiers,
        RATCHET_BASELINE if baseline is None else baseline,
    )


def check_repository(
    root: Path | None = None,
    files: Sequence[NamedFile] | None = None,
    identifiers: Sequence[NamedIdentifier] | None = None,
    ratchet: RatchetBaseline | None = None,
) -> list[str]:
    """Compare the repository with the registry; return the complaints.

    An empty list means the repository and the registry agree: every naming
    defect is declared, every declared file exists, and the live debt is
    inside the ratchet.
    """
    base = Path(root) if root is not None else REPO_ROOT
    if files is None or identifiers is None or ratchet is None:
        files, identifiers, ratchet = load_and_validate()
    assert files is not None and identifiers is not None and ratchet is not None

    complaints: list[str] = []

    declared = {entry.path: entry for entry in files}
    live_defects = scan_file_defects(base)

    for path in sorted(set(live_defects) - set(declared)):
        complaints.append(
            f"{path}: breaks {', '.join(live_defects[path])} but is not declared "
            f"in the registry")
    for path in sorted(set(declared) - set(live_defects)):
        entry = declared[path]
        if entry.legacy:
            complaints.append(
                f"{path}: is declared legacy but breaks no rule any more; clear "
                f"its legacy flag in the change that renames it")
    for path in sorted(declared):
        entry = declared[path]
        if not entry.legacy and path in live_defects:
            complaints.append(
                f"{path}: is declared as renamed but still breaks "
                f"{', '.join(live_defects[path])}")
        if not (base / path).is_file():
            complaints.append(
                f"{path}: is declared in the registry but does not exist")

    live_identifiers = set(scan_identifier_definitions(base))
    declared_identifiers = {(e.name, e.scope, e.file) for e in identifiers}
    for name, scope, path in sorted(live_identifiers - declared_identifiers):
        complaints.append(
            f"{name} ({scope} in {path}): is a numbered identifier that the "
            f"registry does not declare")
    for name, scope, path in sorted(declared_identifiers - set(live_identifiers)):
        complaints.append(
            f"{name} ({scope} in {path}): is declared in the registry but is "
            f"not defined there any more")

    live_legacy_files = sum(1 for path in live_defects if path in declared
                            and declared[path].legacy)
    if live_legacy_files > ratchet.files:
        complaints.append(
            f"the legacy file segment grew: {live_legacy_files} files against a "
            f"baseline of {ratchet.files}")

    # The acceptance rig's own vocabulary is declared but never counted.
    legacy_exported = sum(1 for name, scope, path in live_identifiers
                          if scope == "exported" and not is_acceptance_rig(path))
    legacy_private = sum(1 for name, scope, path in live_identifiers
                         if scope == "private" and not is_acceptance_rig(path))
    if legacy_exported > ratchet.exported_identifiers:
        complaints.append(
            f"the legacy exported-identifier segment grew: {legacy_exported} "
            f"identifiers against a baseline of {ratchet.exported_identifiers}")
    if legacy_private > ratchet.private_identifiers:
        complaints.append(
            f"the legacy private-identifier segment grew: {legacy_private} "
            f"identifiers against a baseline of {ratchet.private_identifiers}")

    return complaints


def main() -> int:
    """Validate the committed registry and the repository; exit non-zero when
    either is invalid."""
    for stream in (sys.stdout, sys.stderr):
        reconfigure = getattr(stream, "reconfigure", None)
        if reconfigure is not None:
            reconfigure(encoding="utf-8", errors="replace")
    try:
        files, identifiers, ratchet = load_and_validate()
    except NamingRegistryError as error:
        print(f"invalid naming registry: {error}", file=sys.stderr)
        return 1
    complaints = check_repository(REPO_ROOT, files, identifiers, ratchet)
    if complaints:
        for complaint in complaints:
            print(f"naming registry: {complaint}", file=sys.stderr)
        return 1
    legacy_files = sum(1 for entry in files if entry.legacy)
    print(f"naming registry: {len(files)} code files, {len(identifiers)} numbered "
          f"identifiers, {legacy_files} legacy files against a baseline of "
          f"{ratchet.files}/{ratchet.exported_identifiers}/"
          f"{ratchet.private_identifiers} at {ratchet.base_sha}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
