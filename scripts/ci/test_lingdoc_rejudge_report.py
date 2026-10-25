"""The guard on the rejudge: what the two release paths say about a rejudged report.

A report that already exists cannot be run again, so the only way a human
judgement can reach the evidence is the rejudge: read the machine facts of
the finished run, read the ledger, read the server-side audit records, and
recompute the manual layer.  These cases ask the two production release
paths -- the main path (``beta_evidence``) and the second path
(``main_path_evidence``) -- what they say about a report the rejudge wrote.

The target shape is the one this whole path exists for: four judgements the
server's own audit records prove let both release paths reach PASS.  The
other two shapes have to stay NOT RUN, because a claim is not a proof and a
judgement that held its assertion back did not hold it.

The rejudge's own cases live in ``scripts/lingdoc_mock/test_rejudge_report.py``;
these ones are the cross-module half of the work.
"""
from __future__ import annotations

import json
from pathlib import Path
import tempfile
import unittest

from scripts.acceptance import beta_evidence
from scripts.lingdoc_check_kinds import declared_kind
from scripts.lingdoc_mock import main_path_evidence, manual_decision_audit, rejudge_report
from scripts.lingdoc_mock.run_f01 import (
    ScenarioRunner,
    read_json_object,
    spec_from_document,
)

ROOT = Path(__file__).resolve().parents[2]
CONTRACT_PATH = ROOT / "docs/08-本轮实施方案/contracts/workflow.json"
OPENAPI_PATH = ROOT / "docs/08-本轮实施方案/contracts/openapi.json"

RUN_ID = "f01-20261025T010000Z-batch01"
OTHER_RUN_ID = "f01-20261024T010000Z-batch09"

PROSE = {
    "F01-06b": "把「允许用这份资料做模型生成」写成一条显式提案，绑定该资料当前的修订号与项目的上下文修订号；版本不符即冲突，不猜版本。",
    "F01-06c": "变更集评估后才允许应用；服务端的合法前态只有 assessed。",
    "F01-06d": "应用这一步才真正写下 model-use 许可记录（对象 kind = project_asset_policy），并把资料推进到新一版修订。",
    "F01-06e": "把许可读回来看：它带 model_use_allowed=true，且钉在这一版资料修订上。",
}
PROSE_STEPS = ["F01-06b", "F01-06c", "F01-06d", "F01-06e"]

FAILURE_OBSERVATION = {
    "failure_case": "duplicate_formal_write",
    "expected_http": 409,
    "actual_http": 409,
    "no_formal_side_effect": True,
    "readback": {"status": "unchanged", "revision": "revision.id"},
}


def judgement(index: int, step_id: str, **overrides) -> dict[str, str]:
    record = {
        "run_id": RUN_ID,
        "assertion_id": step_id,
        "assertion_text": PROSE[step_id],
        "verdict": "passed",
        "actor": "user-owner",
        "authorization": "project_owner",
        "channel": "ui",
        "occurred_at": "2026-10-25T01:00:00+00:00",
        "evidence_ref": f"run:{RUN_ID}/f01-report.json",
        "audit_id": f"audit-{index}",
    }
    record.update(overrides)
    return record


def audit_record(index: int, step_id: str, **overrides) -> dict[str, str]:
    record = {
        "audit_id": f"audit-{index}",
        "actor": "user-owner",
        "authorization": "project_owner",
        "channel": "ui",
        "occurred_at": "2026-10-25T01:00:00+00:00",
        "assertion_id": step_id,
        "run_id": RUN_ID,
        "evidence_ref": f"run:{RUN_ID}/f01-report.json",
    }
    record.update(overrides)
    return record


def write_lines(path: Path, records) -> Path:
    path.write_text("".join(json.dumps(record, ensure_ascii=False, sort_keys=True) + "\n"
                            for record in records), encoding="utf-8")
    return path


def batch_report() -> dict:
    """A finished batch's report, with nothing judged yet.

    The machine half is the contract's own checks, recorded as they hold;
    the prose half is what the runner writes when nobody judged anything.
    """
    contract = json.loads(CONTRACT_PATH.read_text(encoding="utf-8"))
    runner = ScenarioRunner(
        read_json_object(OPENAPI_PATH, "OpenAPI document"),
        spec_from_document(contract, "F01 contract"),
        token="synthetic-test-token",
        opener=lambda request, timeout: (_ for _ in ()).throw(
            AssertionError("the runner contacted a provider")),
        run_id=RUN_ID)
    runner.step_results = [
        {"id": step["id"], "operation_id": step["operation_id"],
         "http_status": step["expected_http"], "content_type": "application/json"}
        for step in contract["steps"]]
    runner.check_results = [
        {"step_id": step["id"], "path": check["path"],
         "operator": declared_kind(check, f"{step['id']} check"),
         "intent": check["intent"], "expected": "synthetic", "actual": "synthetic",
         "found": True, "status": "passed"}
        for step in contract["steps"] for check in step.get("checks", [])]
    return runner.report("completed")


def verdicts(report: dict) -> tuple[str, str]:
    """What the two production release paths say about one report."""
    contract = json.loads(CONTRACT_PATH.read_text(encoding="utf-8"))
    second = main_path_evidence.build_main_path_report(
        main_report=report, failure_observation=FAILURE_OBSERVATION)
    outcomes = beta_evidence.check_outcomes(report, contract)
    outcomes.extend(beta_evidence.manual_assertion_outcomes(report))
    outcomes.extend(beta_evidence.provider_semantics_outcome(report))
    outcomes.extend(beta_evidence.unevaluated_outcomes(report, contract))
    main_verdict = beta_evidence.overall_result(
        outcomes, beta_evidence.provider_semantics_status(report))
    return second["main_path"]["result"], main_verdict


class RejudgedReport:
    """A rejudged report, written into a temporary directory."""

    def __init__(self, directory: Path) -> None:
        self.directory = directory
        self.original = batch_report()
        self.report_path = directory / "f01-report.json"
        self.report_path.write_text(
            json.dumps(self.original, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        self.out = directory / "f01-report.rejudged.json"

    def ledger(self, records) -> Path:
        return write_lines(self.directory / "decisions.jsonl", records)

    def audit(self, records) -> Path:
        return write_lines(self.directory / "audit.jsonl", records)

    def rejudge(self, *, records, audits) -> dict:
        return rejudge_report.rejudge_report(
            report_path=self.report_path, out_path=self.out,
            manual_decisions=self.ledger(records),
            audit_source=str(self.audit(audits)) if audits is not None else None)

    def rejudged(self) -> dict:
        return json.loads(self.out.read_text(encoding="utf-8"))


class ReleasePathTests(unittest.TestCase):
    """What the two release paths say about the three shapes of judgement."""

    def setUp(self) -> None:
        self.workspace = tempfile.TemporaryDirectory()
        self.addCleanup(self.workspace.cleanup)
        self.case = RejudgedReport(Path(self.workspace.name))

    def test_four_attested_judgements_let_both_paths_pass(self):
        """The shape this path exists for."""
        records = [judgement(index, step_id)
                   for index, step_id in enumerate(PROSE_STEPS, start=1)]
        audits = [audit_record(index, step_id)
                  for index, step_id in enumerate(PROSE_STEPS, start=1)]
        report = self.case.rejudge(records=records, audits=audits)
        self.assertEqual(report["provider_semantics_status"], "verified")
        second, main_verdict = verdicts(report)
        self.assertEqual(second, "PASS")
        self.assertEqual(main_verdict, "PASS")

    def test_a_judgement_nobody_could_verify_keeps_both_paths_at_not_run(self):
        """A claim is not a proof: the release may not read it as one."""
        records = [judgement(index, step_id)
                   for index, step_id in enumerate(PROSE_STEPS, start=1)]
        # The audit source proves three of the four judgements.
        audits = [audit_record(index, step_id)
                  for index, step_id in enumerate(PROSE_STEPS[:3], start=1)]
        report = self.case.rejudge(records=records, audits=audits)
        by_step = {entry["step_id"]: entry for entry in report["manual_assertions"]}
        self.assertEqual(by_step["F01-06e"]["authority"],
                         manual_decision_audit.AUTHORITY_CLAIMED)
        self.assertNotEqual(by_step["F01-06e"]["status"], "passed")
        self.assertEqual(report["provider_semantics_status"], "not_verified")
        second, main_verdict = verdicts(report)
        self.assertEqual(second, "NOT RUN")
        self.assertEqual(main_verdict, "NOT RUN")

    def test_a_judgement_that_held_its_assertion_back_keeps_both_paths_at_not_run(self):
        for verdict in ("held", "rejected"):
            with self.subTest(verdict=verdict):
                with tempfile.TemporaryDirectory() as directory:
                    case = RejudgedReport(Path(directory))
                    records = [judgement(index, step_id, verdict=verdict)
                               for index, step_id in enumerate(PROSE_STEPS, start=1)]
                    audits = [audit_record(index, step_id)
                              for index, step_id in enumerate(PROSE_STEPS, start=1)]
                    report = case.rejudge(records=records, audits=audits)
                    self.assertEqual(report["provider_semantics_status"], "not_verified")
                    second, main_verdict = verdicts(report)
                    self.assertEqual(second, "NOT RUN")
                    self.assertEqual(main_verdict, "NOT RUN")

    def test_a_report_nobody_judged_keeps_both_paths_at_not_run(self):
        report = self.case.rejudge(records=[], audits=[])
        self.assertEqual(report["provider_semantics_status"], "not_verified")
        second, main_verdict = verdicts(report)
        self.assertEqual(second, "NOT RUN")
        self.assertEqual(main_verdict, "NOT RUN")


class MachineFactTests(unittest.TestCase):
    """The rejudge recomputes the manual layer and nothing else."""

    def setUp(self) -> None:
        self.workspace = tempfile.TemporaryDirectory()
        self.addCleanup(self.workspace.cleanup)
        self.case = RejudgedReport(Path(self.workspace.name))

    def test_the_machine_facts_of_the_finished_run_are_untouched(self):
        records = [judgement(index, step_id)
                   for index, step_id in enumerate(PROSE_STEPS, start=1)]
        audits = [audit_record(index, step_id)
                  for index, step_id in enumerate(PROSE_STEPS, start=1)]
        self.case.rejudge(records=records, audits=audits)
        rejudged = self.case.rejudged()
        self.assertEqual(set(rejudged), set(self.case.original))
        for key in self.case.original:
            if key in rejudge_report.MANUAL_LAYER_KEYS:
                continue
            self.assertEqual(rejudge_report.canonical(self.case.original[key]),
                             rejudge_report.canonical(rejudged[key]), key)

    def test_the_batch_of_the_finished_run_is_untouched(self):
        records = [judgement(index, step_id)
                   for index, step_id in enumerate(PROSE_STEPS, start=1)]
        audits = [audit_record(index, step_id)
                  for index, step_id in enumerate(PROSE_STEPS, start=1)]
        self.case.rejudge(records=records, audits=audits)
        self.assertEqual(self.case.rejudged()["run_id"], RUN_ID)

    def test_a_judgement_naming_another_batch_is_refused_and_writes_nothing(self):
        records = [judgement(index, step_id, run_id=OTHER_RUN_ID)
                   for index, step_id in enumerate(PROSE_STEPS, start=1)]
        audits = [audit_record(index, step_id)
                  for index, step_id in enumerate(PROSE_STEPS, start=1)]
        with self.assertRaises(rejudge_report.RejudgeError):
            self.case.rejudge(records=records, audits=audits)
        self.assertFalse(self.case.out.exists())


if __name__ == "__main__":
    unittest.main()
