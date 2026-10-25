"""The rejudge: authoritative judgements attached to a report that already exists.

A batch that already ran cannot be run again -- the provider's budget is
spent and a replay returns the old result -- so a judgement that arrives
afterwards cannot reach the evidence by running the batch.  These cases pin
down the way that does work: read the machine facts, read the ledger, read
the server-side audit records, and recompute **only** the manual layer.

The seven boundaries the rejudge has to keep are pinned here, one case each:

1. a judgement is bound to one batch -- a judgement about another one is
   refused, and nothing is written;
2. the machine facts are copied byte for byte;
3. a judgement nobody could verify is a claim: it satisfies nothing;
4. an assertion nobody judged stays unjudged -- no judgement is invented;
5. a machine conclusion is never upgraded;
6. an illegal ledger is refused, and no half-written report is left behind;
7. the same input produces the same report.

Nothing here contacts a provider: the report is built by the runner against a
local fake, and the rejudge only reads files.
"""
from __future__ import annotations

import hashlib
import json
from pathlib import Path
import tempfile
import unittest

from scripts.lingdoc_check_kinds import declared_kind
from scripts.lingdoc_mock import manual_decision_audit, rejudge_report, run_f01
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

#: The contract's own prose, quoted here so that a contract that moves on
#: turns these cases red instead of quietly changing what they mean.
PROSE = {
    "F01-06b": "把「允许用这份资料做模型生成」写成一条显式提案，绑定该资料当前的修订号与项目的上下文修订号；版本不符即冲突，不猜版本。",
    "F01-06c": "变更集评估后才允许应用；服务端的合法前态只有 assessed。",
    "F01-06d": "应用这一步才真正写下 model-use 许可记录（对象 kind = project_asset_policy），并把资料推进到新一版修订。",
    "F01-06e": "把许可读回来看：它带 model_use_allowed=true，且钉在这一版资料修订上。",
}
PROSE_STEPS = ["F01-06b", "F01-06c", "F01-06d", "F01-06e"]

#: The keys of a report the rejudge recomputes; every other key is a machine
#: fact and has to come out of the rejudge unchanged.
MACHINE_FACT_KEYS = ("workflow", "run_id", "runner_status", "completed_steps",
                     "total_steps", "steps", "verification_scope",
                     "material_gate_stages", "checks")


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


def batch_report(*, failed_check: bool = False) -> dict:
    """A report in the shape the F01 runner writes, with nothing judged.

    The machine half is the contract's own checks, recorded as they hold;
    the manual half is what the runner writes when nobody judged anything.
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
    checks = [
        {"step_id": step["id"], "path": check["path"],
         "operator": declared_kind(check, f"{step['id']} check"),
         "intent": check["intent"], "expected": "synthetic", "actual": "synthetic",
         "found": True, "status": "passed"}
        for step in contract["steps"] for check in step.get("checks", [])]
    if failed_check and checks:
        checks[0] = {**checks[0], "status": "failed"}
    runner.check_results = checks
    return runner.report("completed")


class RejudgeFixture(unittest.TestCase):
    """The fixtures every case here writes to a temporary directory."""

    def setUp(self) -> None:
        self.workspace = tempfile.TemporaryDirectory()
        self.directory = Path(self.workspace.name)
        self.addCleanup(self.workspace.cleanup)
        self.report = batch_report()
        self.report_path = self.directory / "f01-report.json"
        self.report_path.write_text(
            json.dumps(self.report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        self.out = self.directory / "rejudged-report.json"

    def write_ledger(self, records) -> Path:
        return write_lines(self.directory / "decisions.jsonl", records)

    def write_audit(self, records) -> Path:
        return write_lines(self.directory / "audit.jsonl", records)

    def attested(self, *, verdict: str = "passed", steps=None) -> tuple[Path, Path]:
        steps = list(steps or PROSE_STEPS)
        records = [judgement(index, step_id, verdict=verdict)
                   for index, step_id in enumerate(steps, start=1)]
        audits = [audit_record(index, step_id)
                  for index, step_id in enumerate(steps, start=1)]
        return self.write_ledger(records), self.write_audit(audits)

    def rejudge(self, *, ledger: Path, audit: Path | None = None, **kwargs) -> dict:
        result = rejudge_report.rejudge_report(
            report_path=self.report_path, out_path=self.out,
            manual_decisions=ledger,
            audit_source=str(audit) if audit is not None else None, **kwargs)
        return result

    def rejudged_document(self) -> dict:
        return json.loads(self.out.read_text(encoding="utf-8"))


class BindingTests(RejudgeFixture):
    """Boundary 1: a judgement belongs to one batch."""

    def test_a_judgement_naming_another_batch_is_refused(self):
        ledger = self.write_ledger([judgement(1, "F01-06b", run_id=OTHER_RUN_ID)])
        audit = self.write_audit([audit_record(1, "F01-06b")])
        with self.assertRaises(rejudge_report.RejudgeError) as raised:
            self.rejudge(ledger=ledger, audit=audit)
        self.assertIn(OTHER_RUN_ID, str(raised.exception))
        self.assertFalse(self.out.exists(), "a refused rejudge may not leave a report behind")

    def test_the_batch_of_the_report_is_never_rewritten(self):
        ledger, audit = self.attested()
        result = self.rejudge(ledger=ledger, audit=audit)
        self.assertEqual(result["run_id"], RUN_ID)
        self.assertEqual(self.rejudged_document()["run_id"], RUN_ID)

    def test_the_operator_may_not_move_the_report_to_another_batch(self):
        ledger, audit = self.attested()
        with self.assertRaises(rejudge_report.RejudgeError) as raised:
            self.rejudge(ledger=ledger, audit=audit, run_id=OTHER_RUN_ID)
        self.assertIn(OTHER_RUN_ID, str(raised.exception))
        self.assertFalse(self.out.exists())

    def test_the_judgement_batch_is_checked_against_the_report_s_batch(self):
        decision = manual_decision_audit.validate_record(
            judgement(1, "F01-06b", run_id=OTHER_RUN_ID))
        with self.assertRaises(rejudge_report.RejudgeError):
            rejudge_report.bind_decisions_to_batch([decision], RUN_ID)

    def test_the_report_may_not_overwrite_the_report_it_reads(self):
        ledger, audit = self.attested()
        with self.assertRaises(rejudge_report.RejudgeError):
            rejudge_report.rejudge_report(
                report_path=self.report_path, out_path=self.report_path,
                manual_decisions=ledger, audit_source=str(audit))
        self.assertEqual(json.loads(self.report_path.read_text(encoding="utf-8")), self.report)


class MachineFactTests(RejudgeFixture):
    """Boundary 2: the machine facts are copied, never rewritten."""

    def test_the_machine_facts_are_byte_identical(self):
        ledger, audit = self.attested()
        self.rejudge(ledger=ledger, audit=audit)
        original = json.loads(self.report_path.read_text(encoding="utf-8"))
        rejudged = self.rejudged_document()
        self.assertEqual(set(original), set(rejudged))
        for key in original:
            if key in rejudge_report.MANUAL_LAYER_KEYS:
                continue
            self.assertEqual(rejudge_report.canonical(original[key]),
                             rejudge_report.canonical(rejudged[key]), key)

    def test_the_machine_facts_the_release_paths_read_are_untouched(self):
        ledger, audit = self.attested()
        self.rejudge(ledger=ledger, audit=audit)
        rejudged = self.rejudged_document()
        for key in MACHINE_FACT_KEYS:
            self.assertEqual(rejudge_report.canonical(self.report[key]),
                             rejudge_report.canonical(rejudged[key]), key)
        self.assertEqual(len(rejudged["steps"]), len(self.report["steps"]))
        self.assertEqual(len(rejudged["checks"]), len(self.report["checks"]))
        self.assertEqual(rejudged["runner_status"], self.report["runner_status"])
        self.assertEqual(rejudged["completed_steps"], self.report["completed_steps"])
        self.assertEqual(rejudged["total_steps"], self.report["total_steps"])
        self.assertEqual(rejudged["verification_scope"], self.report["verification_scope"])

    def test_the_guard_inside_the_tool_notices_a_rewritten_machine_fact(self):
        rewritten = dict(self.report)
        rewritten["completed_steps"] = 99
        with self.assertRaises(rejudge_report.RejudgeError):
            rejudge_report.assert_machine_facts_unchanged(self.report, rewritten)


class AuthorityTests(RejudgeFixture):
    """Boundary 3: a claim is not a proof."""

    def test_a_claim_may_not_satisfy_an_assertion(self):
        ledger = self.write_ledger([judgement(index, step_id)
                                    for index, step_id in enumerate(PROSE_STEPS, start=1)])
        result = self.rejudge(ledger=ledger)
        for entry in result["manual_assertions"]:
            self.assertEqual(entry["authority"], manual_decision_audit.AUTHORITY_CLAIMED)
            self.assertNotEqual(entry["status"], "passed")
            self.assertEqual(entry["status"], manual_decision_audit.UNVERIFIED_STATUS)
        self.assertNotEqual(result["provider_semantics_status"], "verified")

    def test_a_claim_does_not_satisfy_an_assertion_the_audit_record_denies(self):
        ledger, audit = self.attested()
        # The audit record names another channel: the judgement cannot be
        # checked against it, so it stays a claim.
        denied = self.write_audit([{**audit_record(index, step_id), "channel": "api"}
                                   for index, step_id in enumerate(PROSE_STEPS, start=1)])
        result = self.rejudge(ledger=ledger, audit=denied)
        for entry in result["manual_assertions"]:
            self.assertEqual(entry["authority"], manual_decision_audit.AUTHORITY_CLAIMED)
            self.assertNotEqual(entry["status"], "passed")
        self.assertNotEqual(result["provider_semantics_status"], "verified")

    def test_an_attested_judgement_satisfies_its_assertion(self):
        ledger, audit = self.attested()
        result = self.rejudge(ledger=ledger, audit=audit)
        for entry in result["manual_assertions"]:
            self.assertEqual(entry["authority"], manual_decision_audit.AUTHORITY_SERVER_ATTESTED)
            self.assertEqual(entry["status"], manual_decision_audit.PASSED_STATUS)
        self.assertEqual(result["provider_semantics_status"], "verified")

    def test_the_semantics_verdict_comes_from_the_runner_s_own_rule(self):
        """The rejudge asks the runner; it does not keep a copy of the rule."""
        ledger = self.write_ledger([judgement(index, step_id)
                                    for index, step_id in enumerate(PROSE_STEPS, start=1)])
        original = run_f01.ScenarioRunner._provider_semantics_status
        try:
            run_f01.ScenarioRunner._provider_semantics_status = lambda self: "verified"
            result = self.rejudge(ledger=ledger)
        finally:
            run_f01.ScenarioRunner._provider_semantics_status = original
        self.assertEqual(result["provider_semantics_status"], "verified")


class NoInventionTests(RejudgeFixture):
    """Boundary 4: no judgement is invented."""

    def test_an_assertion_nobody_judged_stays_unjudged(self):
        ledger, audit = self.attested(steps=["F01-06b", "F01-06c"])
        result = self.rejudge(ledger=ledger, audit=audit)
        by_step = {entry["step_id"]: entry for entry in result["manual_assertions"]}
        self.assertEqual(by_step["F01-06b"]["status"], manual_decision_audit.PASSED_STATUS)
        for step_id in ("F01-06d", "F01-06e"):
            self.assertEqual(by_step[step_id]["status"], manual_decision_audit.UNJUDGED_STATUS)
            self.assertNotIn("authority", by_step[step_id])
            self.assertNotIn("verdict", by_step[step_id])
        self.assertEqual(len(result["manual_decisions"]), 2)
        self.assertNotEqual(result["provider_semantics_status"], "verified")

    def test_an_empty_ledger_judges_nothing(self):
        ledger = self.write_ledger([])
        result = self.rejudge(ledger=ledger)
        for entry in result["manual_assertions"]:
            self.assertEqual(entry["status"], manual_decision_audit.UNJUDGED_STATUS)
        self.assertEqual(result["manual_decisions"], [])
        self.assertNotEqual(result["provider_semantics_status"], "verified")

    def test_a_judgement_about_prose_the_report_does_not_carry_is_refused(self):
        ledger = self.write_ledger([judgement(1, "F01-06b")])
        audit = self.write_audit([audit_record(1, "F01-06b")])
        self.report["manual_assertions"] = [
            entry for entry in self.report["manual_assertions"]
            if entry["step_id"] != "F01-06b"]
        self.report_path.write_text(
            json.dumps(self.report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        with self.assertRaises(rejudge_report.RejudgeError) as raised:
            self.rejudge(ledger=ledger, audit=audit)
        self.assertIn("F01-06b", str(raised.exception))
        self.assertFalse(self.out.exists())

    def test_a_judgement_the_ledger_drops_may_not_be_silently_dropped(self):
        ledger, audit = self.attested()
        result = self.rejudge(ledger=ledger, audit=audit)
        self.assertEqual(result["manual_assertions"][0]["status"],
                         manual_decision_audit.PASSED_STATUS)
        # The judged report, read again with a ledger that no longer holds the
        # judgement: the report may not be downgraded to "nobody judged this".
        self.report_path.write_bytes(self.out.read_bytes())
        empty = self.write_ledger([])
        with self.assertRaises(rejudge_report.RejudgeError) as raised:
            self.rejudge(ledger=empty)
        self.assertIn("F01-06b", str(raised.exception))


class MachineConclusionTests(RejudgeFixture):
    """Boundary 5: a machine conclusion is never upgraded."""

    def test_a_failed_machine_check_keeps_the_semantics_unverified(self):
        self.report = batch_report(failed_check=True)
        self.report_path.write_text(
            json.dumps(self.report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        ledger, audit = self.attested()
        result = self.rejudge(ledger=ledger, audit=audit)
        self.assertNotEqual(result["provider_semantics_status"], "verified")
        self.assertEqual(self.rejudged_document()["provider_semantics_status"], "not_verified")

    def test_a_failed_machine_check_is_not_rewritten_by_the_rejudge(self):
        self.report = batch_report(failed_check=True)
        self.report_path.write_text(
            json.dumps(self.report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        ledger, audit = self.attested()
        self.rejudge(ledger=ledger, audit=audit)
        rejudged = self.rejudged_document()
        self.assertEqual(rejudged["checks"][0]["status"], "failed")
        self.assertEqual(rejudge_report.canonical(rejudged["checks"]),
                         rejudge_report.canonical(self.report["checks"]))


class RefusalTests(RejudgeFixture):
    """Boundary 6: an illegal ledger is refused, visibly, and writes nothing."""

    def test_a_judgement_missing_a_field_is_refused(self):
        record = judgement(1, "F01-06b")
        del record["authorization"]
        ledger = self.write_ledger([record])
        with self.assertRaises(rejudge_report.RejudgeError):
            self.rejudge(ledger=ledger)
        self.assertFalse(self.out.exists())

    def test_a_judgement_that_does_not_quote_the_prose_verbatim_is_refused(self):
        ledger = self.write_ledger([judgement(1, "F01-06b", assertion_text=PROSE["F01-06b"][:20])])
        with self.assertRaises(rejudge_report.RejudgeError):
            self.rejudge(ledger=ledger)
        self.assertFalse(self.out.exists())

    def test_a_judgement_about_an_assertion_that_does_not_exist_is_refused(self):
        ledger = self.write_ledger([{**judgement(1, "F01-06b"), "assertion_id": "F01-99"}])
        with self.assertRaises(rejudge_report.RejudgeError):
            self.rejudge(ledger=ledger)
        self.assertFalse(self.out.exists())

    def test_a_judgement_naming_another_batch_s_evidence_is_refused(self):
        ledger = self.write_ledger([judgement(1, "F01-06b",
                                               evidence_ref=f"run:{OTHER_RUN_ID}/f01-report.json")])
        with self.assertRaises(rejudge_report.RejudgeError):
            self.rejudge(ledger=ledger)
        self.assertFalse(self.out.exists())

    def test_the_cli_refuses_loudly_and_writes_nothing(self):
        ledger = self.write_ledger([judgement(1, "F01-06b", run_id=OTHER_RUN_ID)])
        audit = self.write_audit([audit_record(1, "F01-06b")])
        code = rejudge_report.main([
            "--report", str(self.report_path), "--out", str(self.out),
            "--manual-decisions", str(ledger), "--audit-source", str(audit)])
        self.assertEqual(code, 1)
        self.assertFalse(self.out.exists())


class IdempotencyTests(RejudgeFixture):
    """Boundary 7: the same input produces the same report."""

    def test_the_same_input_produces_the_same_bytes(self):
        ledger, audit = self.attested()
        self.rejudge(ledger=ledger, audit=audit)
        first = hashlib.sha256(self.out.read_bytes()).hexdigest()
        self.rejudge(ledger=ledger, audit=audit)
        second = hashlib.sha256(self.out.read_bytes()).hexdigest()
        self.assertEqual(first, second)

    def test_the_report_is_written_the_way_the_runner_writes_it(self):
        ledger, audit = self.attested()
        self.rejudge(ledger=ledger, audit=audit)
        self.assertEqual(self.out.read_text(encoding="utf-8"),
                         run_f01.render_report(self.rejudged_document()))

    def test_the_cli_writes_the_same_report_twice(self):
        ledger, audit = self.attested()
        arguments = ["--report", str(self.report_path), "--out", str(self.out),
                     "--manual-decisions", str(ledger), "--audit-source", str(audit)]
        self.assertEqual(rejudge_report.main(arguments), 0)
        first = hashlib.sha256(self.out.read_bytes()).hexdigest()
        self.assertEqual(rejudge_report.main(arguments), 0)
        self.assertEqual(first, hashlib.sha256(self.out.read_bytes()).hexdigest())


if __name__ == "__main__":
    unittest.main()
