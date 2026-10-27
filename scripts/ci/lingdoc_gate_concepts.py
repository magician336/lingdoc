"""The documented vocabulary of the gate decision.

The gate decision is a fact that the documents describe and the workflows
consume.  This module is the **one place where the two meet**: for every
judgement concept the documents name, it records

* the wording the document uses (``term`` and ``snippet``), so a document
  that changes its wording without the decision following is caught;
* the members of the decision result that carry the concept (``fields``);
* the published output keys that carry it (``keys``), and whether the
  per-job verdict keys are part of it (``covers_job_keys``).

The guard test ``scripts/ci/test_lingdoc_gate_contracts.py`` turns that table
into a two-way contract:

* document -> decision: the wording is still there, the named members still
  exist, the named keys are still published, and at least one of them is
  read by a workflow;
* decision -> document: every member and every published key is claimed by
  a concept (or listed as deliberately internal), so an implementation that
  gains a concept the documents do not have is caught.

The documents themselves are never rewritten here.  When a document changes
its wording, the row in this table changes with it -- that is the point of
the contract.
"""
from __future__ import annotations

import dataclasses


@dataclasses.dataclass(frozen=True)
class Concept:
    """One documented judgement concept.

    ``fields`` names members of the decision result as ``Decision.<name>``
    or ``JobDecision.<name>``.  ``keys`` names published output keys.
    ``guard`` names the file that enforces a wiring rule (a rule about how
    the workflows are wired, not a fact the decision carries).
    """

    term: str
    doc: str
    snippet: str
    fields: tuple[str, ...] = ()
    keys: tuple[str, ...] = ()
    covers_job_keys: bool = False
    guard: str = ""


# The document sections the contract covers.  A section that is renamed or
# dropped has to be moved here on purpose, not by accident.
DOC_SECTIONS: tuple[tuple[str, str], ...] = (
    ("docs/agents/development-rules.md", "### 文档提交的 CI 边界"),
    ("docs/agents/development-rules.md", "#### 门禁面边界"),
    ("docs/agents/development-rules.md", "### 纯格式提交的 CI 边界"),
    ("docs/adr/0005-ci-gate-by-diff-content.md", "### 六、接线要求"),
    ("docs/adr/0006-gate-surface-by-contribution.md", "### 一、验证单元"),
    ("docs/adr/0007-gate-decision-deep-module.md", "### 四、每个 job 的布尔量"),
    ("GLOSSARY.md", "## 门禁判定"),
)

GLOSSARY = "GLOSSARY.md"
ADR_0005 = "docs/adr/0005-ci-gate-by-diff-content.md"
ADR_0006 = "docs/adr/0006-gate-surface-by-contribution.md"
ADR_0007 = "docs/adr/0007-gate-decision-deep-module.md"
RULES = "docs/agents/development-rules.md"

# The concepts the documents name, and the decision members that carry them.
CONCEPTS: tuple[Concept, ...] = (
    Concept(
        term="验证单元",
        doc=GLOSSARY,
        snippet="一次门禁判定所依据的两个事实",
        fields=("Decision.fingerprint", "Decision.surface_changed",
                "Decision.base_moved"),
        keys=("reason", "surface_changed", "base_moved"),
    ),
    Concept(
        term="贡献面",
        doc=GLOSSARY,
        snippet="相对基线，一个 PR 实际改动的路径集合",
        fields=("Decision.contribution", "Decision.diff_range",
                "Decision.changed_paths"),
        keys=("diff_range", "changed_paths", "reason"),
    ),
    Concept(
        term="基线合并",
        doc=GLOSSARY,
        snippet="把基线并入 PR 分支",
        fields=("Decision.base_moved",),
        keys=("base_moved", "reason"),
    ),
    Concept(
        term="基线前移",
        doc=GLOSSARY,
        snippet="基线自身前进，PR 分支没有动",
        fields=("Decision.base_moved",),
        keys=("base_moved", "reason"),
    ),
    Concept(
        term="门禁判定",
        doc=GLOSSARY,
        snippet="## 门禁判定",
        fields=("Decision.jobs",),
        covers_job_keys=True,
    ),
    Concept(
        term="`surface`",
        doc=ADR_0006,
        snippet="这次测了哪些包/目录",
        fields=("Decision.surface",),
        keys=("surface",),
    ),
    Concept(
        term="`reason`",
        doc=ADR_0006,
        snippet="为什么是这个面",
        fields=("Decision.reason",),
        keys=("reason",),
    ),
    Concept(
        term="`base`",
        doc=ADR_0006,
        snippet="这次判断所依据的基线提交",
        fields=("Decision.base",),
        keys=("base",),
    ),
    Concept(
        term="前端侧",
        doc=ADR_0006,
        snippet="前端侧无法缩",
        fields=("Decision.frontend",),
        keys=("frontend",),
    ),
    Concept(
        term="委托",
        doc=GLOSSARY,
        snippet="委托的证据在另一个 job 上真实存在",
        # The delegation is carried by the frontend verdict: the published
        # ``frontend`` key names the delegated job instead of reporting this
        # gate's own outcome (#723, ADR 0014).  ``frontend_delegate`` is the
        # key the failure verification reads: the delegated job's display
        # name, empty exactly when the delegation does not hold (#734).
        fields=("Decision.frontend",),
        keys=("frontend", "frontend_delegate"),
    ),
    Concept(
        term="分片",
        doc=GLOSSARY,
        snippet="分片策略只写在判定模块一处",
        # The sharding is carried by the shard list the decision publishes:
        # the workflow's matrix is generated from this output (#724, ADR
        # 0014), and the coverage table names the shard jobs.
        fields=("Decision.provider_shards",),
        keys=("provider_shards",),
    ),
    Concept(
        term="未判定回退全量",
        doc=ADR_0006,
        snippet="未判定回退全量",
        fields=("Decision.undecided",),
        keys=("reason",),
    ),
    Concept(
        term="被测面内容",
        doc=ADR_0006,
        snippet="**被测面内容**变了",
        fields=("Decision.run_code", "Decision.surface_changed"),
        keys=("run_code", "surface_changed", "reason"),
    ),
    Concept(
        term="基线内容",
        doc=ADR_0006,
        snippet="**基线内容**变了",
        fields=("Decision.run_targeted", "Decision.base_moved"),
        keys=("run_targeted", "base_moved", "reason"),
    ),
    Concept(
        term="每个 job 的布尔量",
        doc=ADR_0007,
        snippet="每个 job 的布尔量",
        fields=("Decision.jobs", "JobDecision.workflow", "JobDecision.job",
                "JobDecision.job_name", "JobDecision.surface",
                "JobDecision.run", "JobDecision.gate_level",
                "JobDecision.path_kind", "JobDecision.paths",
                "JobDecision.skipped_on_format_only", "JobDecision.key",
                "JobDecision.readable_key", "JobDecision.role"),
        keys=("run",),
        covers_job_keys=True,
    ),
    Concept(
        term="provider 根目录",
        doc=ADR_0007,
        snippet="provider 根目录",
        fields=("Decision.provider_packages", "Decision.provider_tests"),
        keys=("provider_packages", "provider_tests", "run_providers"),
    ),
    Concept(
        term="治理类改动",
        doc=ADR_0007,
        snippet="治理类改动",
        fields=("Decision.governance_changed",),
        keys=("reason",),
    ),
    Concept(
        term="强制全量",
        doc=ADR_0007,
        snippet="是否强制全量",
        fields=("Decision.forced",),
        keys=("forced", "reason"),
    ),
    Concept(
        term="纯格式提交",
        doc=ADR_0005,
        snippet="纯格式提交跳过测试类 job",
        fields=("Decision.format_only", "Decision.format_reasons"),
        keys=("format_only", "format_reasons", "run_providers"),
    ),
    Concept(
        term="白名单",
        doc=ADR_0005,
        snippet="job 的 outputs 是显式白名单",
        guard="scripts/ci/test_lingdoc_gate_contracts.py",
    ),
    Concept(
        term="契约",
        doc=RULES,
        snippet="贡献包含代码、迁移、契约或工作流变更时",
        fields=("Decision.contracts",),
        keys=("contracts",),
    ),
    Concept(
        term="推送被测面",
        doc=RULES,
        snippet="这次推送自己的 diff",
        fields=("Decision.diff_range", "Decision.base"),
        keys=("diff_range", "base"),
    ),
    Concept(
        term="门禁始终出现",
        doc=RULES,
        snippet="门禁 job 永远创建",
        guard="scripts/ci/test_lingdoc_gate_workflow_triggers.py",
    ),
    Concept(
        term="门禁面",
        doc=GLOSSARY,
        snippet="一份归属声明：哪些路径属于谁",
        # The surface a job belongs to is carried by the job decision.  The
        # published key ``surface`` is *not* this concept: it names the
        # tested surface (the packages and directories this run measured),
        # which the concept "`surface`" already owns.
        fields=("JobDecision.surface",),
        covers_job_keys=True,
    ),
    Concept(
        term="门禁面边界",
        doc=RULES,
        snippet="清单里没有登记的工作流不是门禁",
        guard="scripts/ci/test_lingdoc_gate_workflow_triggers.py",
    ),
)

# The members of the decision result that no document names, and why.
INTERNAL_MEMBERS: dict[str, str] = {
    "Decision.backend": (
        "后端范围分类（internal/、migrations/、config/ 等路径）。文档没有为"
        "它命名，工作流也不读它；它只参与判定，不进入产物。"
    ),
    "Decision.coverage": (
        "贡献触及的每个面的覆盖情况：谁在这个 head 上编译、测试或构建了它。"
        "文档没有为它命名；它进入门禁结论（产物与摘要），工作流不读它作为"
        "输出。"
    ),
    "Decision.uncovered": (
        "是否有一个贡献触及的面在这个 head 上完全没有编译或测试证据。文档"
        "没有为它命名；它只参与判定，并作为门禁的放行条件。"
    ),
}

# The published keys that no document names, and why.
INTERNAL_KEYS: dict[str, str] = {
    "backend": (
        "后端范围分类的字符串形式，与 Decision.backend 同源；同样没有文档"
        "概念，也没有工作流读取。"
    ),
    "coverage": (
        "覆盖情况的单行字符串形式（每个面一行）。GitHub 输出值不能带换行，"
        "所以完整表格在门禁结论里；工作流不读它作为输出。"
    ),
    "uncovered": (
        "是否存在完全没有证据的面。与 Decision.uncovered 同源；工作流不读"
        "它作为输出。"
    ),
}
