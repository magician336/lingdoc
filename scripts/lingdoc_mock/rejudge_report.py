#!/usr/bin/env python3
"""Attach authoritative human judgements to a batch report that already exists.

Why this exists
---------------

A batch that has already run cannot be run again: the provider's budget is
spent, and a replay of the same idempotency key returns the *old* result --
the very result that was written before anybody judged the contract's prose.
So a judgement that arrives afterwards cannot reach the evidence by running
the batch again, and both release paths keep saying NOT RUN.

There is one way that costs nothing.  The machine facts of the finished run
are already written down, and the judgement is written down too: in the
ledger of human judgements, and in the server-side audit record that proves
it.  This module reads the three of them and recomputes **only** the manual
layer of the report -- the prose assertions' own statuses, and the aggregate
the runner's own rule derives from them.  The machine facts are copied byte
for byte: a rejudge may not turn a check that failed into one that passed.

What it refuses
---------------

* a judgement that names another batch;
* a judgement that does not quote the contract's prose verbatim;
* a judgement about prose the report does not carry;
* a report that already carries a judgement the ledger does not contain (a
  judgement may not be dropped by omission);
* anything that would change a machine fact.

The rules themselves are not written here.  The binding, the three tiers of
authority and the wording of a judgement entry live in
``scripts/lingdoc_mock/manual_decision_audit.py``; the one rule that decides
whether the provider's semantics were verified lives in the runner
(``ScenarioRunner._provider_semantics_status``).  This module asks both of
them for their verdicts instead of keeping a second copy of either.
"""
from __future__ import annotations

import argparse
import copy
import json
from pathlib import Path
import sys
from typing import Any

ROOT = Path(__file__).resolve().parents[2]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

# The channel a human judgement travels through: the ledger, the rules that
# bind one judgement to one batch and to the contract's prose, the three
# tiers of authority, and the wording a judgement takes in a report.
from scripts.lingdoc_mock import manual_decision_audit  # noqa: E402  (the path must be set first)
# The runner: the report's shape, its one serialization, and the one rule
# that says whether the provider's semantics were verified.
from scripts.lingdoc_mock import run_f01  # noqa: E402  (the path must be set first)

#: The contract whose prose a judgement has to quote.
CONTRACT_PATH = ROOT / "docs/08-本轮实施方案/contracts/workflow.json"

#: The report keys this module recomputes.  Every other key is a machine
#: fact: it is copied, never rewritten.
MANUAL_LAYER_KEYS = ("manual_assertions", "manual_decisions", "provider_semantics_status")

#: The keys a judgement leaves in a report entry.  An entry that carries one
#: of them carries a judgement -- and a judgement may not be dropped.
JUDGEMENT_MARKERS = ("verdict", "authority", "actor", "authorization", "channel",
                     "channel_verifiability", "occurred_at", "evidence_ref", "audit_id")


class RejudgeError(manual_decision_audit.ManualDecisionError):
    """A rejudge this tool refuses."""


def canonical(value: Any) -> str:
    """One text for one value, so two values can be compared byte for byte."""
    return json.dumps(value, ensure_ascii=False, sort_keys=True)


def read_report(path: Path | str) -> dict[str, Any]:
    """Read the report the batch produced."""
    target = Path(path)
    try:
        text = target.read_text(encoding="utf-8")
    except OSError as error:
        raise RejudgeError(f"cannot read the report {target}: {error}") from error
    try:
        document = json.loads(text)
    except json.JSONDecodeError as error:
        raise RejudgeError(f"the report {target} is not readable JSON: {error}") from error
    if not isinstance(document, dict):
        raise RejudgeError(f"the report {target} must hold a JSON object")
    return document


def read_contract(path: Path | str) -> dict[str, Any]:
    """Read the contract the judgements quote."""
    target = Path(path)
    try:
        text = target.read_text(encoding="utf-8")
    except OSError as error:
        raise RejudgeError(f"cannot read the contract {target}: {error}") from error
    try:
        document = json.loads(text)
    except json.JSONDecodeError as error:
        raise RejudgeError(f"the contract {target} is not readable JSON: {error}") from error
    if not isinstance(document, dict):
        raise RejudgeError(f"the contract {target} must hold a JSON object")
    return document


def resolve_run_id(report: dict[str, Any], requested: str | None) -> str:
    """The batch this rejudge is about.

    The report names its own batch, and that name is never rewritten: a
    judgement is about one batch, and a rejudge that moved a report to
    another batch would publish a judgement under a batch it never saw.
    So ``--run-id`` is an assertion, not an override -- it has to agree
    with the report, and a report that names no batch has to be told one.
    """
    reported = report.get("run_id")
    if isinstance(reported, str) and reported.strip():
        if requested and requested != reported:
            raise RejudgeError(
                f"the report is batch {reported!r}, but the judgements were said to "
                f"belong to batch {requested!r}: a judgement is about one batch")
        return reported
    if requested and requested.strip():
        return requested
    raise RejudgeError("the report names no batch, so no judgement may be bound to it")


def bind_decisions_to_batch(decisions: list[dict[str, Any]], run_id: str) -> None:
    """Every judgement has to belong to this batch.

    The ledger's own rules already refuse a judgement that names another
    batch; this is the same rule stated once more at the seam that writes
    the report, so that a judgement can never reach a report of a batch it
    does not describe.
    """
    for decision in decisions:
        if decision.get("run_id") != run_id:
            raise RejudgeError(
                f"the judgement about {decision.get('assertion_id')!r} names batch "
                f"{decision.get('run_id')!r}, but this report is batch {run_id!r}")


def _entry_key(entry: dict[str, Any]) -> tuple[str, Any]:
    return (str(entry.get("step_id")), entry.get("assertion"))


def bind_decisions_to_report(report: dict[str, Any], decisions: list[dict[str, Any]]) -> None:
    """Every judgement has to be about prose this report carries.

    Two ways to be wrong, and both are refusals rather than repairs: a
    judgement about prose the report does not carry cannot be attached to
    it, and a report that already carries a judgement the ledger does not
    contain may not be downgraded to "nobody judged this" by omission.
    """
    entries = report.get("manual_assertions")
    if not isinstance(entries, list):
        raise RejudgeError("the report carries no list of prose assertions")
    carried = {_entry_key(entry): entry for entry in entries if isinstance(entry, dict)}
    judged = {(decision["assertion_id"], decision["assertion_text"]) for decision in decisions}
    for decision in decisions:
        key = (decision["assertion_id"], decision["assertion_text"])
        if key not in carried:
            raise RejudgeError(
                f"the judgement about {decision['assertion_id']!r} quotes prose this "
                f"report does not carry")
    for key, entry in carried.items():
        if key in judged:
            continue
        judged_before = (any(marker in entry for marker in JUDGEMENT_MARKERS) or
                         (entry.get("status") is not None and
                          entry.get("status") != manual_decision_audit.UNJUDGED_STATUS))
        if judged_before:
            raise RejudgeError(
                f"this report already carries a judgement about {key[0]!r} that the "
                f"ledger does not contain: a judgement may not be dropped")


def manual_layer(entries: list[Any], decisions: list[dict[str, Any]]) -> list[dict[str, Any]]:
    """The prose assertions of a report, enriched by the judgements about them.

    An assertion nobody judged stays unjudged -- the tool never invents a
    judgement.  An assertion somebody judged carries that judgement's own
    words, exactly as the runner writes them.
    """
    judged = {(decision["assertion_id"], decision["assertion_text"]): decision
              for decision in decisions}
    layer: list[dict[str, Any]] = []
    for entry in entries:
        if not isinstance(entry, dict):
            raise RejudgeError("a prose assertion of the report is not an object")
        updated = dict(entry)
        decision = judged.get(_entry_key(entry))
        if decision is not None:
            updated.update(manual_decision_audit.report_entry(decision))
        layer.append(updated)
    return layer


class _SemanticsView:
    """The two things the runner's own semantics rule reads.

    The rule is the runner's, and it is asked for its verdict rather than
    copied here.  It reads the machine checks of the run and the prose
    assertions with their judgements; this view carries exactly those two.
    """

    def __init__(self, checks: Any, entries: list[dict[str, Any]]) -> None:
        self.check_results = list(checks) if isinstance(checks, list) else []
        self._entries = list(entries)

    def manual_assertions(self) -> list[dict[str, Any]]:
        return self._entries


def provider_semantics_status(checks: Any, entries: list[dict[str, Any]]) -> str:
    """Whether the provider's semantics were verified -- the runner's rule.

    The runner decides this in one place, and that place is asked here: a
    second copy of the rule would drift, and a drift would be a release
    path reading a different verdict than the runner wrote.
    """
    view = _SemanticsView(checks, entries)
    return run_f01.ScenarioRunner._provider_semantics_status(view)


def assert_machine_facts_unchanged(original: dict[str, Any], result: dict[str, Any]) -> None:
    """Refuse a rejudge that touched a machine fact.

    The machine facts are what the run actually did.  A rejudge may read
    them and may say something new about the human half of the run; it may
    not restate them.
    """
    for key, value in original.items():
        if key in MANUAL_LAYER_KEYS:
            continue
        if key not in result:
            raise RejudgeError(f"the rejudge dropped the machine fact {key!r}")
        if canonical(value) != canonical(result[key]):
            raise RejudgeError(f"the rejudge changed the machine fact {key!r}")
    for key in result:
        if key not in original and key not in MANUAL_LAYER_KEYS:
            raise RejudgeError(f"the rejudge invented the machine fact {key!r}")


def recompute_manual_layer(report: dict[str, Any],
                            decisions: list[dict[str, Any]]) -> dict[str, Any]:
    """The report with its manual layer recomputed and its machine facts kept."""
    if not isinstance(report, dict):
        raise RejudgeError("a report must be a JSON object")
    entries = report.get("manual_assertions")
    if not isinstance(entries, list):
        raise RejudgeError("the report carries no list of prose assertions")
    bind_decisions_to_report(report, decisions)
    layer = manual_layer(entries, decisions)
    result = copy.deepcopy(report)
    result["manual_assertions"] = layer
    result["manual_decisions"] = [dict(decision) for decision in decisions]
    result["provider_semantics_status"] = provider_semantics_status(
        result.get("checks"), layer)
    assert_machine_facts_unchanged(report, result)
    return result


def rejudge_report(*, report_path: Path | str, out_path: Path | str,
                   manual_decisions: Path | str, audit_source: str | None = None,
                   run_id: str | None = None,
                   contract_path: Path | str = CONTRACT_PATH) -> dict[str, Any]:
    """Read the three sources, recompute the manual layer, write the report.

    The report is written only after every refusal has been passed: a
    refused rejudge leaves no half-written report behind.
    """
    report = read_report(report_path)
    if Path(out_path).resolve() == Path(report_path).resolve():
        raise RejudgeError("the rejudged report may not overwrite the report it reads")
    batch = resolve_run_id(report, run_id)
    contract = read_contract(contract_path)
    # The contract is the authority on what a judgement may quote: a
    # judgement has to name the contract's own sentence, word for word.
    prose = manual_decision_audit.prose_index_from_document(contract)
    contract_workflow = contract.get("id")
    if contract_workflow and report.get("workflow") and report.get("workflow") != contract_workflow:
        raise RejudgeError(
            f"the report is about {report.get('workflow')!r}, but the contract "
            f"{Path(contract_path).name} is about {contract_workflow!r}")
    try:
        records = manual_decision_audit.load_ledger(manual_decisions)
        audits = manual_decision_audit.load_audit_source(audit_source)
        decisions = manual_decision_audit.ingest(
            records, batch, prose, manual_decision_audit.audit_index(audits))
    except manual_decision_audit.ManualDecisionError as error:
        raise RejudgeError(str(error)) from error
    bind_decisions_to_batch(decisions, batch)
    result = recompute_manual_layer(report, decisions)
    try:
        run_f01.write_report(Path(out_path), result)
    except run_f01.WorkflowError as error:
        raise RejudgeError(str(error)) from error
    return result


def main(argv: list[str] | None = None) -> int:
    """The tool: one existing report in, one rejudged report out."""
    parser = argparse.ArgumentParser(
        description="Attach authoritative human judgements to an existing batch report. "
                    "No provider is contacted: the machine facts are read, not produced.")
    parser.add_argument("--report", type=Path, required=True,
                        help="the report the batch produced")
    parser.add_argument("--out", type=Path, required=True,
                        help="where the rejudged report is written")
    parser.add_argument("--manual-decisions", type=Path, required=True,
                        help="the ledger of human judgements (JSON Lines)")
    parser.add_argument("--audit-source",
                        help="the server-side audit records (a file or a URL); "
                             "without one every judgement is a claim, not a proof")
    parser.add_argument("--run-id",
                        help="the batch the judgements belong to; it has to be the "
                             "report's own batch -- the report's batch is never rewritten")
    parser.add_argument("--contract", type=Path, default=CONTRACT_PATH,
                        help="the contract whose prose the judgements quote")
    args = parser.parse_args(argv)
    try:
        result = rejudge_report(
            report_path=args.report, out_path=args.out,
            manual_decisions=args.manual_decisions, audit_source=args.audit_source,
            run_id=args.run_id, contract_path=args.contract)
    except RejudgeError as error:
        print(f"REFUSED: {error}", file=sys.stderr)
        return 1
    print(json.dumps({
        "run_id": result.get("run_id"),
        "provider_semantics_status": result.get("provider_semantics_status"),
        "prose_assertions": len(result.get("manual_assertions") or []),
        "judgements": len(result.get("manual_decisions") or []),
        "out": str(args.out),
    }, ensure_ascii=False, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
