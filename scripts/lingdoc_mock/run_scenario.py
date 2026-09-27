#!/usr/bin/env python3
"""Drive one contract scenario from its declared starting state to a per-item report.

A scenario is not a scenario without a place to start, and the place is written in the
contract: `starting_states`. So one run does three things and records all three — it
builds the declared state into the provider and reads it back, it sends the steps the
scenario declares as the actors those steps name, and it decides every expectation the
steps carry against the responses that actually arrived.

The output is one report at a fixed path with no timestamps and no provider identifiers:
the same input twice must produce the same bytes, because a conclusion that changes
between two identical runs is not a conclusion. What the report means about the service
is in docs/08-本轮实施方案/T15-场景到报告.md; this file only drives and records.

It composes the two tools that already exist rather than re-implementing either: the
loading and read-back of a declared state (load_state.py) and the driving of steps
through one HTTP transport (run_f01.py).
"""
from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import sys
from typing import Any
from urllib.parse import urlsplit

ROOT = Path(__file__).resolve().parents[2]
if str(ROOT) not in sys.path:  # `python scripts/lingdoc_mock/run_scenario.py` has no repo root on the path
    sys.path.insert(0, str(ROOT))

from scripts.lingdoc_mock.load_state import (  # noqa: E402  (path setup must run first)
    Bindings,
    StateLoader,
    load_state,
)
from scripts.lingdoc_mock.run_f01 import (  # noqa: E402
    OPENAPI_PATH,
    SCENARIOS_PATH,
    ProviderClient,
    ScenarioRunner,
    WorkflowError,
    load_spec,
    operation_index,
    parse_environment_fact,
    read_json_object,
    redact,
    render_report,
    scenario_entry,
    write_report,
)


REPORT_PATH = ROOT / "docs/08-本轮实施方案/T15-验证报告.json"

# The gaps this run found in the service, registered here rather than fixed here: #27's
# agreement is that T15 records what a scenario run turns up, and the line that owns the
# code fixes it. The evidence column points at the report section that shows it.
REGISTERED_GAPS = (
    {
        "id": "T15-04-R1",
        "title": "startGeneration 声明的整条 422 分支在实现里不存在",
        "contract_says": "openapi.json 给 startGeneration 声明了 5 个 422 例子：insufficient_evidence / "
                         "asset_not_ready / preflight_blocked / invalid_state / asset_not_authorized，"
                         "其中 asset_not_authorized 还要求被拒资料逐项带原因（details.denied[].reason）。",
        "service_does": "这 5 个形状一条都发不出来：internal/lingdoc/generation/ 里没有任何 422 分支"
                        "（`grep -rn 422 internal/lingdoc/generation/` 零命中）。对已绑定但不可用的资料，"
                        "startGeneration 答 403 source_access_denied——只有一个错误码，没有 details.denied，"
                        "调用者看不出是哪一条、为什么。",
        "where": "internal/lingdoc/workspace/generation.go 的 ResolveGenerationInput 把任何被拒资料折成 "
                 "generation.ErrSourceAccessDenied；internal/lingdoc/generation/http.go 的 "
                 "generationHTTPStatus / writeGenerationError 把它映射成 403 source_access_denied。",
        "consequence": "契约承诺的逐项原因在生成这一步拿不到。受这条作废的不止一处：F22 原本指向的那一步在内，"
                       "契约里 F03 的第 2 步（startGeneration → 422 asset_not_ready，setup 说「资料仍 processing」）"
                       "同样落在一条实现里不存在的分支上。F22 因此改指 retrieveSources，并把这里登记为缺口，"
                       "而不是把断言迁就实现。",
        "owner": "生成线的实现者",
        "status": "open",
    },
)

# What this run cannot conclude, stated where the conclusion is written rather than in a
# commit message. A boundary that only lives in a conversation is one the next reader
# will re-derive by hand.
BOUNDARIES = (
    {
        "id": "T15-04-B1",
        "statement": "F22 的授权维（同租户内一条被授权、另一条不被授权）在本构建不可达；本票驱动的是可用性那一维。",
        "why": "绑定与读取用同一判定（bindAsset 与 evidence 网关都走 kbReadChecker.CanReadKB），同租户读取无条件放行，"
               "跨租户的差异只来自租户级分享，而分享的有效权限按租户算、角色下限是 viewer——"
               "同一租户的两名成员不可能一个放行一个被拒。",
        "verified_by": "T15-04 的代码核对与实跑：同租户协作者在业主租户下读到 200；跨租户读取 403、把该知识绑进本项目 404。",
        "unblocks": "撤权（移除分享、把租户移出组织）落地后 not_authorized 才有可达路径；#27 把撤权族分给 #35。",
    },
    {
        "id": "T15-04-B2",
        "statement": "报告只到「HTTP 通道 + 契约里可求值的形状」这一层。",
        "why": "断言读的是响应里能取到的值。模型质量、数据库副作用、DOCX 可编辑性不在任何通道能证的范围内，"
               "所以它们既不出现在通过里，也不出现在失败里。",
    },
)


def executability(scenario: dict[str, Any]) -> tuple[bool, str]:
    """Whether *this driver* can run a scenario, and which part is missing when it cannot.

    Derived from the contract rather than listed by hand, so the report keeps telling the truth
    after the next person fills a request definition in. This answers a narrower question than
    `load_spec` does: that one asks whether a drivable specification can be built at all, which a
    continuous specification file satisfies without ever saying where a run should start.

    One missing part is reported, and it is the one that actually blocks the scenario: a scenario
    with no steps or no request definitions is not held back by its starting state, it is held
    back by the contract not having been filled in yet.
    """
    steps = scenario.get("steps")
    if steps is None:
        source = scenario.get("specification_file")
        if source:
            return False, (f"由 {source} 的连续规格驱动，但没有声明 starting_state："
                           "本驱动先装起点再发步骤，不知道从哪里起跑")
        return False, "场景没有步骤，只有合成样例：能写请求的那部分还没写进契约"
    missing = [index for index, step in enumerate(steps, start=1)
               if isinstance(step, dict) and "request" not in step]
    if missing:
        listed = "、".join(f"第 {index} 步" for index in missing)
        return False, f"{len(steps)} 步里这 {len(missing)} 步只有操作名、期望状态码与断言文本，没有请求定义：{listed}"
    if not scenario.get("starting_state"):
        return False, "没有声明 starting_state：本驱动先装起点再发步骤，不知道从哪里起跑"
    return True, "步骤已带请求定义，起点已声明"


def variables_from(loader: StateLoader) -> dict[str, Any]:
    """The names a scenario's request may use, resolved to this run's real values.

    `asset.<declared name>` is the *binding* the bind response returned, not the knowledge
    behind it, because that is what the retrieval preflight addresses; the declared name
    is the only way to ask for it without publishing the id.
    """
    project_id = loader.identifiers.get("project_id")
    if not project_id:
        raise WorkflowError("the state build produced no project id, so no request can be addressed")
    variables: dict[str, Any] = {"project.id": project_id}
    variables.update({f"asset.{name}": asset_id for name, asset_id in loader.asset_ids.items()})
    variables.update({key: value for key, value in loader.identifiers.items() if key.startswith("chapter.")})
    return variables


def redactions_from(loader: StateLoader) -> dict[str, str]:
    """Every identifier this run knows, mapped to the declared name it stands for."""
    redactions: dict[str, str] = {}
    project_id = loader.identifiers.get("project_id")
    if project_id:
        redactions[project_id] = "project.id"
    for name, asset_id in loader.asset_ids.items():
        redactions[asset_id] = f"asset.{name}"
    for key, value in loader.identifiers.items():
        # `identifiers["asset.<name>"]` is the knowledge behind the binding; for a reader of
        # the report both ids are the same declared asset, so both get the same name.
        if key.startswith(("asset.", "chapter.")):
            redactions[value] = key
    return redactions


def expectation_text(check: dict[str, Any]) -> str:
    """Say an expectation the way the report's reader has to read it."""
    expected = json.dumps(check["expected"], ensure_ascii=False)
    return {
        "equals": f"期望等于 {expected}",
        "length": f"期望长度 {expected}",
        "keys": f"期望键集 {expected}",
        "one_of": f"期望属于 {expected}",
    }[check["operator"]]


def step_verdicts(spec, runner: ScenarioRunner) -> list[dict[str, Any]]:
    """One record per declared step: what was sent, what came back, and every expectation's outcome."""
    recorded = {step["id"]: step for step in runner.step_results}
    failed_status = {failure["step_id"]: failure for failure in runner.failures}
    checks: dict[str, list[dict[str, Any]]] = {}
    for check in runner.check_results:
        checks.setdefault(check["step_id"], []).append(check)

    verdicts: list[dict[str, Any]] = []
    for step in spec.steps:
        step_id = step["id"]
        answered = recorded.get(step_id)
        mismatch = failed_status.get(step_id)
        step_checks = checks.get(step_id, [])
        broken = [check for check in step_checks if check["status"] == "failed"]
        if answered is not None:
            actual = answered["http_status"]
        else:
            actual = mismatch["actual"] if mismatch else None
        entry: dict[str, Any] = {
            "step_id": step_id,
            "operation_id": step["operation_id"],
            "actor": step["actor"],
            "expected_http": step["expected_http"],
            "actual_http": actual,
            "verdict": "failed" if mismatch or broken else "passed",
            "intent": step["assertions"][0] if step["assertions"] else "",
            "checks": step_checks,
        }
        if entry["verdict"] == "failed":
            why = []
            if mismatch:
                why.append(f"期望 HTTP {mismatch['expected']}，实际 HTTP {mismatch['actual']}")
            for check in broken:
                observed = (json.dumps(check["actual"], ensure_ascii=False) if check["found"]
                            else "<该位置在响应里不存在>")
                why.append(f"{check['path']}：{expectation_text(check)}，实际 {observed}")
            entry["why"] = why
        verdicts.append(entry)
    return verdicts


def scenario_verdicts(document: dict[str, Any], executed_id: str, state_id: str,
                      verdict: str) -> list[dict[str, Any]]:
    """Every scenario in the contract, with the one this run drove carrying its conclusion."""
    entries: list[dict[str, Any]] = []
    for scenario in document["scenarios"]:
        scenario_id = scenario["id"]
        if scenario_id == executed_id:
            entries.append({"scenario": scenario_id, "name": scenario["name"], "verdict": verdict,
                            "executable": True, "reason": f"本次运行驱动的那一条，起点 {state_id}"})
            continue
        executable, why = executability(scenario)
        reason = why if not executable else "可执行；本次运行只驱动一条，未执行"
        entries.append({"scenario": scenario_id, "name": scenario["name"], "verdict": "not_run",
                        "executable": executable, "reason": reason})
    return entries


def run_scenario(scenario_id: str, states_path: Path, openapi_path: Path, *,
                 knowledge: dict[str, str], member: dict[str, str],
                 identities: dict[str, str] | None = None,
                 base_url: str | None = None, token: str | None = None, timeout: float = 20,
                 opener=None) -> dict[str, Any]:
    """Build the declared state, send the declared steps, and return the report for both halves."""
    document = read_json_object(states_path, "states document")
    scenario = scenario_entry(document, scenario_id, states_path.name)
    # The specification is loaded first on purpose: a scenario whose steps cannot be sent
    # must be refused before a single write is issued, not after the state is already built.
    spec = load_spec(states_path, scenario_id)
    named_actors = sorted({step["actor"] for step in spec.steps if step.get("actor")})
    effective_identities = dict(identities or {})
    if len(named_actors) > 1:
        missing = [actor for actor in named_actors if actor not in effective_identities]
        if missing:
            raise WorkflowError(f"{scenario_id} names multiple actors but no credential was supplied for "
                                + ", ".join(missing))
    unused = sorted(set(effective_identities) - set(named_actors))
    if unused:
        raise WorkflowError(f"{scenario_id} does not use supplied identity: " + ", ".join(unused))
    if len(named_actors) == 1 and named_actors[0] not in effective_identities and token:
        effective_identities[named_actors[0]] = token
    state_id = scenario.get("starting_state")
    if not state_id:
        raise WorkflowError(f"{scenario_id} declares no starting_state, so there is nothing to drive it from")

    openapi = read_json_object(openapi_path, "OpenAPI document")
    server_url, _ = operation_index(openapi)
    state = load_state(states_path, state_id)
    knowledge_bindings = Bindings("knowledge", "knowledge")
    member_bindings = Bindings("member", "user")
    for name, resolved in knowledge.items():
        knowledge_bindings.bind(name, resolved)
    for name, resolved in member.items():
        member_bindings.bind(name, resolved)
    client = ProviderClient(base_url or server_url, token=token, timeout=timeout, opener=opener)
    loader = StateLoader(openapi, state, client, knowledge_bindings, member_bindings)
    loader.preflight()
    loader.build()
    settled = loader.read_back()

    redactions = redactions_from(loader)
    state_report = redact({key: value for key, value in loader.report("completed", settled).items()
                           if key != "provider_host"}, redactions)
    executed: dict[str, Any] = {
        "scenario": scenario_id,
        "name": scenario["name"],
        "starting_state": state_id,
        "state": state_report,
        "steps": [],
    }
    # A state that did not read back as declared is not a place to run a scenario from: the
    # steps would be testing something other than what the contract says they test.
    if settled["mismatches"]:
        executed["verdict"] = "failed"
    else:
        runner = ScenarioRunner(openapi, spec, base_url=base_url or server_url, token=token,
                                timeout=timeout, opener=opener, identities=effective_identities,
                                variables=variables_from(loader), redactions=redactions)
        runner.run(stop_on_mismatch=False)
        executed["steps"] = step_verdicts(runner.spec, runner)
        executed["verdict"] = "failed" if any(step["verdict"] == "failed" for step in executed["steps"]) else "passed"

    entries = scenario_verdicts(document, scenario_id, state_id, executed["verdict"])
    summary = {verdict: sum(entry["verdict"] == verdict for entry in entries)
               for verdict in ("passed", "failed", "not_run")}
    return {
        "report_version": 1,
        "contract_version": document.get("contract_version"),
        "scope": "one scenario driven from its declared starting state to a per-item verdict",
        "provider_host": urlsplit(client.base_url).netloc,
        "executed": executed,
        "scenarios": entries,
        "summary": summary,
        "registered_gaps": list(REGISTERED_GAPS),
        "boundaries": list(BOUNDARIES),
        "verification_scope": "http_smoke_only",
        "provider_semantics_status": "not_verified",
        # Counted from the summary rather than written down: the sentence has to keep telling the
        # truth the next time somebody fills a request definition into the contract.
        "not_run": ["模型质量、数据库副作用、DOCX 可打开性：没有任何通道能证",
                    f"其余 {summary['not_run']} 个场景：没跑的原因逐条写在 scenarios 里它自己的 reason 字段"],
    }


def run_scenarios(scenario_ids: list[str], states_path: Path, openapi_path: Path, **kwargs: Any) -> dict[str, Any]:
    """Run independent declared starting states and combine their per-scenario conclusions.

    A single scenario keeps the version-1 report shape for existing callers. A multi-scenario
    report records one complete execution per ID and merges only those IDs' verdicts into the
    shared contract listing.
    """
    if not scenario_ids:
        raise WorkflowError("at least one --scenario is required")
    if len(scenario_ids) != len(set(scenario_ids)):
        raise WorkflowError("--scenario contains a duplicate scenario id")

    provided_identities = dict(kwargs.get("identities") or {})
    document = read_json_object(states_path, "states document")
    scenario_specs = {scenario_id: load_spec(states_path, scenario_id) for scenario_id in scenario_ids}
    scenario_states = {}
    for scenario_id in scenario_ids:
        scenario = scenario_entry(document, scenario_id, states_path.name)
        state_id = scenario.get("starting_state")
        if not state_id:
            raise WorkflowError(f"{scenario_id} declares no starting_state, so there is nothing to drive it from")
        scenario_states[scenario_id] = load_state(states_path, state_id)
    all_actors = {step["actor"] for spec in scenario_specs.values() for step in spec.steps if step.get("actor")}
    unused_identities = sorted(set(provided_identities) - all_actors)
    if unused_identities:
        raise WorkflowError("--identity supplies actors not used by the selected scenarios: "
                            + ", ".join(unused_identities))
    multi_actor_names = {actor for spec in scenario_specs.values()
                         for actor in {step["actor"] for step in spec.steps if step.get("actor")}
                         if len({step["actor"] for step in spec.steps if step.get("actor")}) > 1}
    missing_identities = sorted(multi_actor_names - set(provided_identities))
    if missing_identities:
        raise WorkflowError("--scenario needs a credential for every actor in multi-actor scenarios: "
                            + ", ".join(missing_identities))
    known_knowledge = {asset.knowledge_id for state in scenario_states.values() for asset in state.assets}
    known_members = {name for state in scenario_states.values() for name in state.members}
    unused_knowledge = sorted(set(kwargs.get("knowledge", {})) - known_knowledge)
    unused_members = sorted(set(kwargs.get("member", {})) - known_members)
    if unused_knowledge:
        raise WorkflowError("--knowledge supplies names not used by the selected starting states: "
                            + ", ".join(unused_knowledge))
    if unused_members:
        raise WorkflowError("--member supplies names not used by the selected starting states: "
                            + ", ".join(unused_members))
    reports = []
    for scenario_id in scenario_ids:
        actors = {step["actor"] for step in scenario_specs[scenario_id].steps if step.get("actor")}
        state = scenario_states[scenario_id]
        state_knowledge = {asset.knowledge_id for asset in state.assets}
        scenario_kwargs = {
            **kwargs,
            "identities": {name: token for name, token in provided_identities.items() if name in actors},
            "knowledge": {name: value for name, value in kwargs.get("knowledge", {}).items()
                          if name in state_knowledge},
            "member": {name: value for name, value in kwargs.get("member", {}).items() if name in state.members},
        }
        reports.append(run_scenario(scenario_id, states_path, openapi_path, **scenario_kwargs))
    if len(reports) == 1:
        return reports[0]

    merged = dict(reports[0])
    verdicts = {entry["scenario"]: entry for entry in reports[0]["scenarios"]}
    for report in reports[1:]:
        for entry in report["scenarios"]:
            if entry["verdict"] != "not_run":
                verdicts[entry["scenario"]] = entry

    scenarios = [verdicts[entry["scenario"]] for entry in reports[0]["scenarios"]]
    summary = {verdict: sum(entry["verdict"] == verdict for entry in scenarios)
               for verdict in ("passed", "failed", "not_run")}
    merged.update({
        "scope": "multiple independent scenarios driven from their declared starting states",
        "executed": [report["executed"] for report in reports],
        "scenarios": scenarios,
        "summary": summary,
        "not_run": ["模型质量、数据库副作用、DOCX 可打开性：没有任何通道能证",
                    f"其余 {summary['not_run']} 个场景：没跑的原因逐条写在 scenarios 里它自己的 reason 字段"],
    })
    return merged


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Drive contract scenarios from their declared states to a report.")
    parser.add_argument("--scenario", required=True, action="append",
                        help="scenario id declared in the states document; repeat to drive independent scenarios")
    parser.add_argument("--states", type=Path, default=SCENARIOS_PATH, help="document holding scenarios and starting_states")
    parser.add_argument("--openapi", type=Path, default=OPENAPI_PATH)
    parser.add_argument("--base-url", help="OpenAPI server base URL; defaults to the first server in openapi.json")
    parser.add_argument("--token", default=os.environ.get("LINGDOC_TEST_TOKEN"), help="credential for the state build and for steps that name no actor (or LINGDOC_TEST_TOKEN)")
    parser.add_argument("--identity", action="append", default=[], metavar="NAME=TOKEN",
                        help="credential for an actor a step names (repeatable)")
    parser.add_argument("--knowledge", action="append", default=[], metavar="NAME=ID",
                        help="bind a declared asset name to a real knowledge id (repeatable)")
    parser.add_argument("--member", action="append", default=[], metavar="NAME=ID",
                        help="bind a declared member name to a real user id (repeatable)")
    parser.add_argument("--request-timeout", type=float, default=20)
    parser.add_argument("--report", type=Path, default=REPORT_PATH, help="where the matrix report is written")
    args = parser.parse_args(argv)
    report_started = False
    try:
        knowledge = dict(parse_environment_fact(value, "knowledge") for value in args.knowledge)
        member = dict(parse_environment_fact(value, "member") for value in args.member)
        identities = dict(parse_environment_fact(value, "identity") for value in args.identity)
        # Verify the destination before any provider mutation: a run that dies halfway then
        # leaves an explicit record instead of a stale success from the previous run.
        scenario_label = ",".join(args.scenario)
        write_report(args.report, {"report_version": 1, "runner_status": "not_started",
                                   "scenarios": args.scenario})
        report_started = True
        report = run_scenarios(args.scenario, args.states, args.openapi, knowledge=knowledge, member=member,
                               identities=identities, base_url=args.base_url, token=args.token,
                               timeout=args.request_timeout)
        write_report(args.report, report)
        executions = report["executed"] if isinstance(report["executed"], list) else [report["executed"]]
        verdict = "failed" if any(execution["verdict"] != "passed" for execution in executions) else "passed"
        print(f"{scenario_label} {verdict}; report written to {args.report}")
        for execution in executions:
            for step in execution["steps"]:
                print(f"{execution['scenario']} {step['step_id']} {step['verdict'].upper()} {step['operation_id']} "
                      f"HTTP {step['actual_http']} (expected {step['expected_http']})")
                for line in step.get("why", []):
                    print(f"  {line}")
            for mismatch in execution["state"]["mismatches"]:
                print(f"{execution['scenario']} MISMATCH {mismatch['pointer']}: declared {mismatch['declared']!r}, "
                      f"read back {mismatch['actual']!r}", file=sys.stderr)
    except (WorkflowError, OSError) as error:
        print(f"{scenario_label if 'scenario_label' in locals() else args.scenario} FAILED: {error}", file=sys.stderr)
        if report_started:
            try:
                write_report(args.report, {"report_version": 1, "runner_status": "failed",
                                           "scenarios": args.scenario})
            except WorkflowError as report_error:
                print(f"{scenario_label} REPORT FAILED: {report_error}", file=sys.stderr)
        return 1
    return 0 if verdict == "passed" else 1


if __name__ == "__main__":
    raise SystemExit(main())
