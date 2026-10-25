"""The guard on the F01 manual-judgement channel (issue #633).

The channel only helps if the two release paths read it the same way.  The
second path (``main_path_evidence``) and the main path (``beta_evidence``)
each decide on their own what "somebody judged this" means, and a judgement
the runner could not verify has to look like "nobody proved this" in both
of them -- otherwise the release reads the optimistic verdict of one run.

These cases are the cross-module half of the work: the channel's own cases
live in ``scripts/lingdoc_mock/test_workflow_manual_decisions.py``, and
these ones ask what the two release paths do with what the channel wrote.
"""
from __future__ import annotations

import json
from pathlib import Path
import tempfile
import unittest

from scripts.acceptance import beta_evidence
from scripts.lingdoc_check_kinds import declared_kind
from scripts.lingdoc_mock import main_path_evidence, manual_decision_audit
from scripts.lingdoc_mock.run_f01 import (
    ScenarioRunner,
    WorkflowError,
    read_json_object,
    spec_from_document,
)

ROOT = Path(__file__).resolve().parents[2]
CONTRACT_PATH = ROOT / "docs/08-本轮实施方案/contracts/workflow.json"
OPENAPI_PATH = ROOT / "docs/08-本轮实施方案/contracts/openapi.json"

RUN_ID = "f01-20261025T010000Z-batch01"

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


def audit_record(index: int, step_id: str) -> dict[str, str]:
    return {
        "audit_id": f"audit-{index}",
        "actor": "user-owner",
        "authorization": "project_owner",
        "channel": "ui",
        "occurred_at": "2026-10-25T01:00:00+00:00",
        "assertion_id": step_id,
        "run_id": RUN_ID,
        "evidence_ref": f"run:{RUN_ID}/f01-report.json",
    }


def write_lines(path: Path, records) -> Path:
    path.write_text("".join(json.dumps(record, ensure_ascii=False, sort_keys=True) + "\n"
                            for record in records), encoding="utf-8")
    return path


def f01_report(directory: Path, *, verdict: str = "passed", attested: bool = True) -> dict:
    """A report in the shape the F01 runner writes.

    The machine half is the contract's own checks, recorded as they hold;
    the manual half is produced by the runner's own code, so the release
    paths are asked about a report nothing here decided twice.
    """
    contract = json.loads(CONTRACT_PATH.read_text(encoding="utf-8"))
    records = [judgement(index, step_id, verdict=verdict)
               for index, step_id in enumerate(PROSE_STEPS, start=1)]
    audits = [audit_record(index, step_id)
              for index, step_id in enumerate(PROSE_STEPS, start=1)]
    ledger = write_lines(directory / "decisions.jsonl", records)
    audit = write_lines(directory / "audit.jsonl", audits)
    runner = ScenarioRunner(
        read_json_object(OPENAPI_PATH, "OpenAPI document"),
        spec_from_document(contract, "F01 contract"),
        token="synthetic-test-token",
        opener=lambda request, timeout: (_ for _ in ()).throw(
            AssertionError("the runner contacted a provider")),
        run_id=RUN_ID, decision_ledger=ledger,
        audit_source=audit if attested else None)
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


class VocabularyTests(unittest.TestCase):
    """The two release paths have to mean the same thing by "judged"."""

    def test_the_two_release_paths_read_the_same_judged_statuses(self):
        """One run may not have two verdicts, so the two vocabularies agree.

        The second path keeps its set in a name; the main path keeps the
        same three words inline.  If one of them gains a word the other
        does not know, a judgement could pass one path and fail the other.
        """
        source = (ROOT / "scripts/acceptance/beta_evidence.py").read_text(encoding="utf-8")
        self.assertIn('{"passed", "held", "judged"}', source)
        self.assertEqual(main_path_evidence.JUDGED_MANUAL_ASSERTION_STATUSES,
                         {"passed", "held", "judged"})

    def test_the_channel_speaks_the_vocabulary_the_release_paths_read(self):
        self.assertTrue(set(manual_decision_audit.VERDICT_STATUS.values())
                        <= main_path_evidence.JUDGED_MANUAL_ASSERTION_STATUSES)
        # A judgement nobody could verify may not look judged.
        self.assertNotIn(manual_decision_audit.UNVERIFIED_STATUS,
                         main_path_evidence.JUDGED_MANUAL_ASSERTION_STATUSES)
        self.assertNotIn(manual_decision_audit.UNJUDGED_STATUS,
                         main_path_evidence.JUDGED_MANUAL_ASSERTION_STATUSES)

    def test_the_channel_names_three_authorities_and_three_verdicts(self):
        self.assertEqual(manual_decision_audit.AUTHORITY_SERVER_ATTESTED, "server_attested")
        self.assertEqual(manual_decision_audit.AUTHORITY_CLAIMED, "claimed")
        self.assertEqual(manual_decision_audit.AUTHORITY_ABSENT, "absent")
        self.assertEqual(manual_decision_audit.VERDICTS, ("passed", "held", "rejected"))
        self.assertEqual(manual_decision_audit.CHANNELS,
                         ("ui", "api", "script", "automation", "unverified_channel"))

    def test_the_judgement_carries_the_ten_fixed_fields(self):
        self.assertEqual(len(manual_decision_audit.REQUIRED_FIELDS), 10)
        self.assertEqual(len(manual_decision_audit.JUDGMENT_FIELDS), 7)
        self.assertTrue(set(manual_decision_audit.JUDGMENT_FIELDS)
                        <= set(manual_decision_audit.REQUIRED_FIELDS))


class ReleaseAgreementTests(unittest.TestCase):
    """What the two release paths say about the three states of a judgement."""

    def test_four_attested_judgements_let_both_paths_pass(self):
        with tempfile.TemporaryDirectory() as directory:
            report = f01_report(Path(directory))
            second, main_verdict = verdicts(report)
            self.assertEqual(second, "PASS")
            self.assertEqual(main_verdict, "PASS")

    def test_a_run_nobody_judged_is_not_run_on_both_paths(self):
        with tempfile.TemporaryDirectory() as directory:
            report = f01_report(Path(directory))
            # The same report without any judgement: the machine half held,
            # the prose half nobody judged.
            report["manual_assertions"] = [
                {"step_id": step_id, "assertion": PROSE[step_id], "status": "not_run"}
                for step_id in PROSE_STEPS]
            report["manual_decisions"] = []
            report["provider_semantics_status"] = "not_verified"
            second, main_verdict = verdicts(report)
            self.assertEqual(second, "NOT RUN")
            self.assertEqual(main_verdict, "NOT RUN")

    def test_a_judgement_nobody_could_verify_is_not_run_on_both_paths(self):
        with tempfile.TemporaryDirectory() as directory:
            report = f01_report(Path(directory), attested=False)
            self.assertEqual(report["provider_semantics_status"], "not_verified")
            second, main_verdict = verdicts(report)
            self.assertEqual(second, "NOT RUN")
            self.assertEqual(main_verdict, "NOT RUN")

    def test_a_judgement_nobody_could_verify_does_not_carry_a_judged_status(self):
        """A claim may not wear the status that satisfies an assertion.

        The aggregate is protected by the authority check as well, but the
        status the report publishes about the assertion itself has to say
        "nobody proved this" -- otherwise a reader of the report alone
        would read a claim as a judgement.
        """
        with tempfile.TemporaryDirectory() as directory:
            report = f01_report(Path(directory), attested=False)
            for entry in report["manual_assertions"]:
                self.assertEqual(entry["authority"], manual_decision_audit.AUTHORITY_CLAIMED)
                self.assertNotIn(entry["status"],
                                 main_path_evidence.JUDGED_MANUAL_ASSERTION_STATUSES)
                self.assertEqual(entry["status"], manual_decision_audit.UNVERIFIED_STATUS)

    def test_a_judgement_that_held_its_assertion_back_is_not_run_on_both_paths(self):
        for verdict in ("held", "rejected"):
            with self.subTest(verdict=verdict):
                with tempfile.TemporaryDirectory() as directory:
                    report = f01_report(Path(directory), verdict=verdict)
                    self.assertEqual(report["provider_semantics_status"], "not_verified")
                    second, main_verdict = verdicts(report)
                    self.assertEqual(second, "NOT RUN")
                    self.assertEqual(main_verdict, "NOT RUN")

    def test_the_second_path_does_not_read_a_machine_check_as_a_judgement(self):
        """A machine check that held is not a sentence a human judged."""
        with tempfile.TemporaryDirectory() as directory:
            report = f01_report(Path(directory))
            self.assertTrue(report["checks"])
            second = main_path_evidence.build_main_path_report(
                main_report=report, failure_observation=FAILURE_OBSERVATION)
            self.assertEqual(second["main_path"]["unjudged_manual_assertions"], [])
            self.assertEqual(second["main_path"]["provider_semantics_status"], "verified")
            # And with the prose half taken away, the machine half alone is
            # not a pass: the aggregate still says what the runner said.
            report["manual_assertions"] = []
            report["provider_semantics_status"] = "not_verified"
            second = main_path_evidence.build_main_path_report(
                main_report=report, failure_observation=FAILURE_OBSERVATION)
            self.assertEqual(second["main_path"]["result"], "NOT RUN")


class ContractFactTests(unittest.TestCase):
    """The facts the channel is built on, read from the contract itself."""

    def test_the_contract_leaves_exactly_four_prose_assertions(self):
        contract = json.loads(CONTRACT_PATH.read_text(encoding="utf-8"))
        prose = [(step["id"], text) for step in contract["steps"]
                 for text in step.get("assertions", [])]
        self.assertEqual(len(prose), 4)
        self.assertEqual([step_id for step_id, _ in prose], PROSE_STEPS)
        self.assertEqual(dict(prose), PROSE)
        self.assertEqual(contract["manual_assertions"], [])

    def test_the_runner_writes_the_batch_and_the_judgements_into_the_report(self):
        with tempfile.TemporaryDirectory() as directory:
            report = f01_report(Path(directory))
            self.assertEqual(report["run_id"], RUN_ID)
            self.assertEqual(len(report["manual_decisions"]), 4)
            self.assertEqual(len(report["manual_assertions"]), 4)


class ChannelGuardTests(unittest.TestCase):
    """The guards an independent review found unpinned.

    An independent review of issue #633 ran nine mutations and found three
    that left the suite green: the authority sub-clause (redundant with
    ``decision_status``), the truncated-quote direction of the verbatim
    comparison, and the channel vocabulary check.  The last two are behaviour
    that is correct today but had no regression protection, so they are pinned
    here.
    """

    def test_a_truncated_quote_is_refused(self):
        """A judgement that quotes only part of the sentence is not a judgement of it."""
        record = judgement(1, "F01-06b", assertion_text=PROSE["F01-06b"][:20])
        with self.assertRaises(manual_decision_audit.ManualDecisionError):
            manual_decision_audit.ingest(
                [record], RUN_ID, {"F01-06b": [PROSE["F01-06b"]]}, {})

    def test_a_channel_outside_the_vocabulary_is_refused(self):
        """The channel names five things; anything else is not a channel."""
        record = judgement(1, "F01-06b", channel="telepathy")
        with self.assertRaises(manual_decision_audit.ManualDecisionError):
            manual_decision_audit.validate_record(record)

    def test_the_release_path_can_carry_the_judgements_into_the_run(self):
        """``beta_evidence.run_f01`` must be able to hand the ledger to the runner.

        Without this the release path could never reach PASS on real evidence:
        the runner would never see a judgement, the contract's prose would
        stay ``not_run``, and the sequence would stay NOT RUN.
        """
        captured: dict = {}

        class _FakeRunner:
            def run(self, stop_on_mismatch: bool = True):
                raise WorkflowError("the fake runner stops here")

            def report(self, status: str) -> dict:
                return {"workflow": "F01", "runner_status": status,
                        "completed_steps": 0, "total_steps": 0, "steps": [],
                        "checks": [], "manual_assertions": [],
                        "provider_semantics_status": "not_verified"}

        class _FakeScenarioRunner:
            @staticmethod
            def from_files(*args, **kwargs):
                captured.update(kwargs)
                return _FakeRunner()

        original = beta_evidence.ScenarioRunner
        beta_evidence.ScenarioRunner = _FakeScenarioRunner
        try:
            beta_evidence.run_f01("http://example.invalid", "token", {}, "",
                                  run_id=RUN_ID,
                                  decision_ledger=Path("ledger.jsonl"),
                                  audit_source="audit.json")
        finally:
            beta_evidence.ScenarioRunner = original
        self.assertEqual(captured.get("run_id"), RUN_ID)
        self.assertEqual(str(captured.get("decision_ledger")), "ledger.jsonl")
        self.assertEqual(captured.get("audit_source"), "audit.json")


if __name__ == "__main__":
    unittest.main()
