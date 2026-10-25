"""The manual-judgement channel of the F01 runner (issue #633).

A prose assertion in the F01 contract is one no machine can read.  Until a
human judges it, the runner recorded it as ``not_run`` and the provider's
semantics could never be verified -- so the release path could never reach
PASS on real evidence.  These cases pin down the channel that fixes that,
and, just as importantly, the refusals that keep it honest:

* a judgement that is missing a field, that names another run, that does
  not quote the contract's prose verbatim, that points at another batch's
  evidence or that repeats a judgement is refused outright;
* a judgement nobody could check against a server-side audit record is a
  claim: it satisfies nothing, and the report says so;
* the runner never invents the actor, the authorization, the time, the
  audit identifier or the channel of a judgement.

Nothing here contacts a provider: the runner is driven against a local
fake, and the release verdicts come from the two production decision paths
(``main_path_evidence`` and ``beta_evidence``) themselves.
"""
from __future__ import annotations

from contextlib import redirect_stderr, redirect_stdout
from email.message import Message
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from scripts.acceptance import beta_evidence
from scripts.lingdoc_check_kinds import declared_kind
from scripts.lingdoc_mock import main_path_evidence, manual_decision_audit
from scripts.lingdoc_mock.manual_decision_audit import ManualDecisionError
from scripts.lingdoc_mock.run_f01 import (ScenarioRunner, WorkflowError, main,
                                          read_json_object, spec_from_document)

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

#: A small specification of our own, with one prose assertion, so that the
#: runner can be driven end to end against a local fake.
MINI_STEP_ID = "F01-01"
MINI_API = {
    "servers": [{"url": "https://provider.test/api/v1/lingdoc"}],
    "paths": {"/projects": {"post": {"operationId": "createProject"}}},
}
MINI_WORKFLOW = {
    "id": "F01",
    "steps": [{
        "id": MINI_STEP_ID,
        "operation_id": "createProject",
        "expected_http": 201,
        "request": {"json": {"name": "synthetic"}},
        "capture": {"project_id": "/data/id"},
        "assertions": [PROSE["F01-06b"]],
    }],
}

#: The failure side of the main path: a refused duplicate write with
#: nothing half-written.  It is the shape the release path expects of a
#: good run.
FAILURE_OBSERVATION = {
    "failure_case": "duplicate_formal_write",
    "expected_http": 409,
    "actual_http": 409,
    "no_formal_side_effect": True,
    "readback": {"status": "unchanged", "revision": "revision.id"},
}


class FakeResponse:
    """A local answer for the fake provider."""

    def __init__(self, payload: bytes, status: int = 201):
        self.payload, self.status = payload, status
        self.headers = Message()
        self.headers["content-type"] = "application/json"
        self.closed = False

    def read(self, limit=-1):
        return self.payload[:limit]

    def close(self):
        self.closed = True


def never_called(request, timeout):  # pragma: no cover - only a guard
    raise AssertionError("the runner contacted a provider")


class JudgementChannelTestCase(unittest.TestCase):
    """The fixtures every case here shares: a judgement and its audit record."""

    def judgement(self, **overrides) -> dict[str, str]:
        record = {
            "run_id": RUN_ID,
            "assertion_id": "F01-06b",
            "assertion_text": PROSE["F01-06b"],
            "verdict": "passed",
            "actor": "user-owner",
            "authorization": "project_owner",
            "channel": "ui",
            "occurred_at": "2026-10-25T01:00:00+00:00",
            "evidence_ref": f"run:{RUN_ID}/f01-report.json",
            "audit_id": "audit-0001",
        }
        record.update(overrides)
        return record

    def audit_record(self, **overrides) -> dict[str, str]:
        record = {
            "audit_id": "audit-0001",
            "actor": "user-owner",
            "authorization": "project_owner",
            "channel": "ui",
            "occurred_at": "2026-10-25T01:00:00+00:00",
            "assertion_id": "F01-06b",
            "run_id": RUN_ID,
            "evidence_ref": f"run:{RUN_ID}/f01-report.json",
        }
        record.update(overrides)
        return record

    def write_ledger(self, directory: Path, records, name: str = "decisions.jsonl") -> Path:
        path = directory / name
        path.write_text("".join(json.dumps(record, ensure_ascii=False, sort_keys=True) + "\n"
                                for record in records), encoding="utf-8")
        return path

    def write_audit(self, directory: Path, records, name: str = "audit.jsonl") -> Path:
        path = directory / name
        path.write_text("".join(json.dumps(record, ensure_ascii=False, sort_keys=True) + "\n"
                                for record in records), encoding="utf-8")
        return path

    def four_judgements(self, root: Path, *, verdict: str = "passed", name: str = "four"):
        """One judgement for each of the contract's four prose assertions."""
        records = []
        audits = []
        for index, step_id in enumerate(PROSE_STEPS, start=1):
            records.append(self.judgement(
                assertion_id=step_id, assertion_text=PROSE[step_id],
                audit_id=f"audit-{index}", verdict=verdict))
            audits.append(self.audit_record(
                assertion_id=step_id, audit_id=f"audit-{index}"))
        return (self.write_ledger(root, records, name=f"ledger-{name}.jsonl"),
                self.write_audit(root, audits, name=f"audit-{name}.jsonl"))

    def mini_runner(self, ledger=None, audit_source=None, run_id: str = RUN_ID):
        """A runner over the small specification, with a local fake provider."""
        return ScenarioRunner(
            MINI_API, spec_from_document(MINI_WORKFLOW, "synthetic specification"),
            token="synthetic-test-token",
            opener=lambda request, timeout: FakeResponse(
                b'{"data":{"id":"synthetic-provider-id"}}', 201),
            run_id=run_id, decision_ledger=ledger, audit_source=audit_source)

    def f01_runner(self, ledger=None, audit_source=None, run_id: str = RUN_ID):
        """A runner over the repository's own F01 contract."""
        contract = json.loads(CONTRACT_PATH.read_text(encoding="utf-8"))
        return ScenarioRunner(
            read_json_object(OPENAPI_PATH, "OpenAPI document"),
            spec_from_document(contract, "F01 contract"),
            token="synthetic-test-token", opener=never_called,
            run_id=run_id, decision_ledger=ledger, audit_source=audit_source)

    def f01_report(self, ledger=None, audit_source=None, run_id: str = RUN_ID) -> dict:
        """The report the F01 runner writes for a run whose machine half held.

        The machine half is a fixture -- the contract's own checks, recorded
        as they hold.  The manual half and the aggregate come from the
        runner's own code, so nothing about a judgement is decided twice.
        """
        contract = json.loads(CONTRACT_PATH.read_text(encoding="utf-8"))
        runner = self.f01_runner(ledger=ledger, audit_source=audit_source, run_id=run_id)
        runner.step_results = [
            {"id": step["id"], "operation_id": step["operation_id"],
             "http_status": step["expected_http"], "content_type": "application/json"}
            for step in contract["steps"]]
        checks = []
        for step in contract["steps"]:
            for check in step.get("checks", []):
                checks.append({
                    "step_id": step["id"], "path": check["path"],
                    "operator": declared_kind(check, f"{step['id']} check"),
                    "intent": check["intent"], "expected": "synthetic",
                    "actual": "synthetic", "found": True, "status": "passed"})
        runner.check_results = checks
        return runner.report("completed")

    def release_verdicts(self, report: dict) -> tuple[str, str]:
        """The two production release paths, asked about one report."""
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


class RecorderEntryPointTests(JudgementChannelTestCase):
    """Acceptance point 1: the recorder writes, the runner reads."""

    def test_the_recorder_writes_one_judgement_into_the_ledger(self):
        with tempfile.TemporaryDirectory() as directory:
            ledger = Path(directory) / "decisions.jsonl"
            with redirect_stdout(io.StringIO()):
                status = manual_decision_audit.main([
                    "record", "--ledger", str(ledger),
                    "--run-id", RUN_ID,
                    "--assertion-id", "F01-06b",
                    "--assertion-text", PROSE["F01-06b"],
                    "--verdict", "passed",
                    "--actor", "user-owner",
                    "--authorization", "project_owner",
                    "--channel", "ui",
                    "--occurred-at", "2026-10-25T01:00:00+00:00",
                    "--evidence-ref", f"run:{RUN_ID}/f01-report.json",
                    "--audit-id", "audit-0001",
                ])
            self.assertEqual(status, 0)
            records = manual_decision_audit.load_ledger(ledger)
            self.assertEqual(len(records), 1)
            self.assertEqual(records[0]["actor"], "user-owner")
            self.assertEqual(records[0]["assertion_text"], PROSE["F01-06b"])

    def test_the_recorder_refuses_a_judgement_without_a_required_field(self):
        with tempfile.TemporaryDirectory() as directory:
            ledger = Path(directory) / "decisions.jsonl"
            for field in manual_decision_audit.JUDGMENT_FIELDS:
                with self.subTest(field=field):
                    record = self.judgement()
                    record.pop(field)
                    with self.assertRaises(ManualDecisionError) as raised:
                        manual_decision_audit.validate_record(record)
                    self.assertIn(field, str(raised.exception))
                    self.assertIn(manual_decision_audit.AUTHORITY_ABSENT, str(raised.exception))
                    self.assertFalse(ledger.exists())

    def test_the_recorder_refuses_a_second_judgement_of_the_same_assertion(self):
        with tempfile.TemporaryDirectory() as directory:
            ledger = Path(directory) / "decisions.jsonl"
            record = self.judgement()
            manual_decision_audit.append_record(ledger, record)
            with self.assertRaises(ManualDecisionError):
                manual_decision_audit.append_record(ledger, record)
            self.assertEqual(len(manual_decision_audit.load_ledger(ledger)), 1)

    def test_the_recorder_refuses_a_judgement_that_does_not_quote_the_contract_verbatim(self):
        with tempfile.TemporaryDirectory() as directory:
            ledger = Path(directory) / "decisions.jsonl"
            with redirect_stdout(io.StringIO()):
                status = manual_decision_audit.main([
                    "record", "--ledger", str(ledger),
                    "--run-id", RUN_ID,
                    "--assertion-id", "F01-06b",
                    "--assertion-text", "把这份资料改成可以生成模型。",
                    "--verdict", "passed",
                    "--actor", "user-owner",
                    "--authorization", "project_owner",
                    "--channel", "ui",
                    "--occurred-at", "2026-10-25T01:00:00+00:00",
                    "--evidence-ref", f"run:{RUN_ID}/f01-report.json",
                    "--audit-id", "audit-0001",
                ])
            self.assertEqual(status, 1)
            self.assertFalse(ledger.exists())


class RunnerIngestTests(JudgementChannelTestCase):
    """Acceptance points 2 to 10: what the runner accepts, and what it refuses."""

    def test_the_runner_reads_the_ledger_and_binds_the_judgement_to_the_run(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            ledger = self.write_ledger(root, [self.judgement(assertion_id=MINI_STEP_ID)])
            audit = self.write_audit(root, [self.audit_record(assertion_id=MINI_STEP_ID)])
            runner = self.mini_runner(ledger=ledger, audit_source=audit)
            with redirect_stdout(io.StringIO()):
                runner.run()
            report = runner.report("completed")
            self.assertEqual(report["run_id"], RUN_ID)
            self.assertEqual(len(report["manual_decisions"]), 1)
            self.assertEqual(report["manual_assertions"][0]["step_id"], MINI_STEP_ID)
            self.assertEqual(report["manual_assertions"][0]["status"], "passed")
            self.assertEqual(report["manual_assertions"][0]["authority"],
                             manual_decision_audit.AUTHORITY_SERVER_ATTESTED)
            self.assertEqual(report["provider_semantics_status"], "not_verified")

    def test_a_judgement_without_any_one_of_the_seven_judgement_fields_is_refused(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for field in manual_decision_audit.JUDGMENT_FIELDS:
                with self.subTest(field=field):
                    record = self.judgement()
                    record.pop(field)
                    ledger = self.write_ledger(root, [record], name=f"ledger-{field}.jsonl")
                    with self.assertRaises(ManualDecisionError) as raised:
                        self.f01_runner(ledger=ledger)
                    self.assertIn(field, str(raised.exception))
                    self.assertIn(manual_decision_audit.AUTHORITY_ABSENT, str(raised.exception))

    def test_a_judgement_without_any_of_the_three_binding_fields_is_refused(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for field in ("run_id", "assertion_id", "assertion_text"):
                with self.subTest(field=field):
                    record = self.judgement()
                    record.pop(field)
                    ledger = self.write_ledger(root, [record], name=f"ledger-{field}.jsonl")
                    with self.assertRaises(ManualDecisionError):
                        self.f01_runner(ledger=ledger)

    def test_a_refused_judgement_stops_the_run_before_the_first_request(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            ledger = self.write_ledger(root, [self.judgement(run_id=OTHER_RUN_ID)])
            calls = []

            def opener(request, timeout):
                calls.append(request)
                return FakeResponse(b'{"data":{}}', 201)

            contract = json.loads(CONTRACT_PATH.read_text(encoding="utf-8"))
            with self.assertRaises(ManualDecisionError):
                ScenarioRunner(read_json_object(OPENAPI_PATH, "OpenAPI document"),
                               spec_from_document(contract, "F01 contract"),
                               opener=opener, run_id=RUN_ID, decision_ledger=ledger)
            self.assertEqual(calls, [])

    def test_a_judgement_of_another_run_is_refused(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            ledger = self.write_ledger(root, [self.judgement(run_id=OTHER_RUN_ID)])
            with self.assertRaises(ManualDecisionError) as raised:
                self.f01_runner(ledger=ledger, run_id=RUN_ID)
            self.assertIn(OTHER_RUN_ID, str(raised.exception))
            self.assertIn(RUN_ID, str(raised.exception))

    def test_a_judgement_that_does_not_quote_the_contract_verbatim_is_refused(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            ledger = self.write_ledger(root, [
                self.judgement(assertion_text=PROSE["F01-06b"] + "（加一句）")])
            with self.assertRaises(ManualDecisionError) as raised:
                self.f01_runner(ledger=ledger)
            self.assertIn("verbatim", str(raised.exception))

    def test_a_judgement_of_an_assertion_the_contract_does_not_declare_is_refused(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            ledger = self.write_ledger(root, [self.judgement(assertion_id="F01-99")])
            with self.assertRaises(ManualDecisionError) as raised:
                self.f01_runner(ledger=ledger)
            self.assertIn("F01-99", str(raised.exception))

    def test_the_authorization_and_the_actor_are_recorded_verbatim(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            ledger = self.write_ledger(root, [
                self.judgement(actor="user-reviewer", authorization="reviewer_scope")])
            audit = self.write_audit(root, [self.audit_record(
                actor="user-reviewer", authorization="reviewer_scope")])
            report = self.f01_report(ledger=ledger, audit_source=audit)
            entry = report["manual_assertions"][0]
            self.assertEqual(entry["actor"], "user-reviewer")
            self.assertEqual(entry["authorization"], "reviewer_scope")
            decision = report["manual_decisions"][0]
            self.assertEqual(decision["actor"], "user-reviewer")
            self.assertEqual(decision["authorization"], "reviewer_scope")

    def test_the_channel_is_recorded_verbatim_and_its_verifiability_is_named(self):
        for channel in manual_decision_audit.CHANNELS:
            with self.subTest(channel=channel):
                with tempfile.TemporaryDirectory() as directory:
                    root = Path(directory)
                    ledger = self.write_ledger(root, [self.judgement(channel=channel)])
                    audit = self.write_audit(root, [self.audit_record(channel=channel)])
                    report = self.f01_report(ledger=ledger, audit_source=audit)
                    entry = report["manual_assertions"][0]
                    self.assertEqual(entry["channel"], channel)
                    self.assertEqual(entry["channel_verifiability"], "verified")

    def test_the_occurred_at_must_be_parseable(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            ledger = self.write_ledger(root, [self.judgement(occurred_at="yesterday")])
            with self.assertRaises(ManualDecisionError) as raised:
                self.f01_runner(ledger=ledger)
            self.assertIn("occurred_at", str(raised.exception))

    def test_the_audit_id_must_be_there_and_must_be_traceable(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            record = self.judgement()
            record.pop("audit_id")
            ledger = self.write_ledger(root, [record], name="without-audit.jsonl")
            with self.assertRaises(ManualDecisionError) as raised:
                self.f01_runner(ledger=ledger)
            self.assertIn("audit_id", str(raised.exception))
            self.assertIn(manual_decision_audit.AUTHORITY_ABSENT, str(raised.exception))

            ledger = self.write_ledger(root, [self.judgement()], name="kept.jsonl")
            audit = self.write_audit(root, [self.audit_record()])
            report = self.f01_report(ledger=ledger, audit_source=audit)
            self.assertEqual(report["manual_assertions"][0]["audit_id"], "audit-0001")
            self.assertEqual(report["manual_decisions"][0]["audit_id"], "audit-0001")

    def test_the_evidence_reference_must_name_this_batch(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            ledger = self.write_ledger(root, [
                self.judgement(evidence_ref=f"run:{OTHER_RUN_ID}/f01-report.json")])
            with self.assertRaises(ManualDecisionError) as raised:
                self.f01_runner(ledger=ledger)
            self.assertIn("batch", str(raised.exception))

    def test_a_second_judgement_of_the_same_assertion_is_refused(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            ledger = self.write_ledger(root, [self.judgement(), self.judgement()])
            with self.assertRaises(ManualDecisionError) as raised:
                self.f01_runner(ledger=ledger)
            self.assertIn("already judged", str(raised.exception))

    def test_the_report_carries_the_provenance_of_every_judgement(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            ledger, audit = self.four_judgements(root)
            report = self.f01_report(ledger=ledger, audit_source=audit)
            self.assertEqual(len(report["manual_decisions"]), 4)
            self.assertEqual([entry["step_id"] for entry in report["manual_assertions"]],
                             PROSE_STEPS)
            for index, entry in enumerate(report["manual_assertions"], start=1):
                self.assertEqual(entry["status"], "passed")
                self.assertEqual(entry["verdict"], "passed")
                self.assertEqual(entry["authority"],
                                 manual_decision_audit.AUTHORITY_SERVER_ATTESTED)
                self.assertEqual(entry["run_id"], RUN_ID)
                self.assertEqual(entry["audit_id"], f"audit-{index}")
                self.assertEqual(entry["occurred_at"], "2026-10-25T01:00:00+00:00")
                self.assertEqual(entry["channel"], "ui")
                self.assertEqual(entry["channel_verifiability"], "verified")
                self.assertIn(RUN_ID, entry["evidence_ref"])
                self.assertEqual(entry["actor"], "user-owner")
                self.assertEqual(entry["authorization"], "project_owner")
            for index, decision in enumerate(report["manual_decisions"], start=1):
                self.assertEqual(decision["audit_id"], f"audit-{index}")
                self.assertEqual(decision["assertion_id"], PROSE_STEPS[index - 1])
                self.assertEqual(decision["assertion_text"], PROSE[PROSE_STEPS[index - 1]])


class AuthorityTests(JudgementChannelTestCase):
    """The three tiers, and what the runner may not do on its own."""

    def test_a_judgement_the_runner_can_check_is_attested(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            ledger = self.write_ledger(root, [self.judgement()])
            audit = self.write_audit(root, [self.audit_record()])
            report = self.f01_report(ledger=ledger, audit_source=audit)
            decision = report["manual_decisions"][0]
            self.assertEqual(decision["authority"], manual_decision_audit.AUTHORITY_SERVER_ATTESTED)
            self.assertEqual(decision["channel_verifiability"], "verified")

    def test_a_judgement_without_an_audit_source_is_only_a_claim(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            ledger = self.write_ledger(root, [self.judgement()])
            report = self.f01_report(ledger=ledger, audit_source=None)
            decision = report["manual_decisions"][0]
            self.assertEqual(decision["authority"], manual_decision_audit.AUTHORITY_CLAIMED)
            self.assertEqual(decision["channel_verifiability"], "unverified")
            self.assertEqual(report["manual_assertions"][0]["status"],
                             manual_decision_audit.UNVERIFIED_STATUS)
            self.assertEqual(report["provider_semantics_status"], "not_verified")

    def test_a_judgement_whose_audit_record_disagrees_is_only_a_claim(self):
        for field in manual_decision_audit.AUDIT_FIELDS:
            with self.subTest(field=field):
                with tempfile.TemporaryDirectory() as directory:
                    root = Path(directory)
                    ledger = self.write_ledger(root, [self.judgement()])
                    audit = self.write_audit(root, [self.audit_record(**{field: "something-else"})])
                    report = self.f01_report(ledger=ledger, audit_source=audit)
                    decision = report["manual_decisions"][0]
                    self.assertEqual(decision["authority"], manual_decision_audit.AUTHORITY_CLAIMED)
                    self.assertEqual(report["manual_assertions"][0]["status"],
                                     manual_decision_audit.UNVERIFIED_STATUS)

    def test_an_audit_record_that_leaves_a_field_out_cannot_prove_it(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            ledger = self.write_ledger(root, [self.judgement()])
            incomplete = self.audit_record()
            incomplete.pop("channel")
            audit = self.write_audit(root, [incomplete])
            report = self.f01_report(ledger=ledger, audit_source=audit)
            self.assertEqual(report["manual_decisions"][0]["authority"],
                             manual_decision_audit.AUTHORITY_CLAIMED)

    def test_the_runner_does_not_invent_the_actor_the_authorization_the_time_or_the_audit_id(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            ledger = self.write_ledger(root, [self.judgement()])
            report = self.f01_report(ledger=ledger, audit_source=None)
            decision = report["manual_decisions"][0]
            for field in ("actor", "authorization", "occurred_at", "audit_id", "channel"):
                self.assertEqual(decision[field], self.judgement()[field])
            # A claim is not a proof, and nothing in the report says the
            # judgement was checked.
            self.assertEqual(decision["authority"], manual_decision_audit.AUTHORITY_CLAIMED)
            self.assertNotIn("owner_present", decision)
            self.assertNotIn("owner_present", json.dumps(report))

    def test_the_runner_names_a_channel_it_cannot_prove_as_unverified(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            ledger = self.write_ledger(root, [self.judgement(channel="ui")])
            report = self.f01_report(ledger=ledger, audit_source=None)
            entry = report["manual_assertions"][0]
            self.assertEqual(entry["channel"], "ui")
            self.assertEqual(entry["channel_verifiability"], "unverified")

    def test_the_runner_may_read_the_audit_source_over_a_read_only_query(self):
        class _Body:
            def __init__(self, body):
                self.body = body

            def read(self, *args):
                return self.body

            def __enter__(self):
                return self

            def __exit__(self, *args):
                return False

        class _Opener:
            def __init__(self, payload):
                self.payload = payload
                self.requests = []

            def open(self, request, timeout=None):
                self.requests.append(request)
                return _Body(json.dumps(self.payload).encode("utf-8"))

        opener = _Opener(self.audit_record())
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            ledger = self.write_ledger(root, [self.judgement()])
            with patch.object(manual_decision_audit, "build_opener", return_value=opener):
                report = self.f01_report(ledger=ledger, audit_source="https://audit.test/export")
            self.assertEqual(len(opener.requests), 1)
            self.assertEqual(opener.requests[0].get_method(), "GET")
        self.assertEqual(report["manual_decisions"][0]["authority"],
                         manual_decision_audit.AUTHORITY_SERVER_ATTESTED)

    def test_a_run_without_an_audit_source_asks_nobody_anything(self):
        with patch.object(manual_decision_audit, "build_opener") as opener:
            with tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                ledger = self.write_ledger(root, [self.judgement()])
                report = self.f01_report(ledger=ledger, audit_source=None)
        opener.assert_not_called()
        self.assertEqual(report["manual_decisions"][0]["authority"],
                         manual_decision_audit.AUTHORITY_CLAIMED)


class ReleaseDecisionTests(JudgementChannelTestCase):
    """Acceptance point 11 and the red lines: what the release may read."""

    def test_four_attested_judgements_verify_the_provider_semantics(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            ledger, audit = self.four_judgements(root)
            report = self.f01_report(ledger=ledger, audit_source=audit)
            self.assertEqual(report["provider_semantics_status"], "verified")
            self.assertEqual([entry["status"] for entry in report["manual_assertions"]],
                             ["passed"] * 4)

    def test_a_held_judgement_does_not_verify_the_provider_semantics(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            ledger, audit = self.four_judgements(root, verdict="held")
            report = self.f01_report(ledger=ledger, audit_source=audit)
            self.assertNotEqual(report["provider_semantics_status"], "verified")
            self.assertEqual(report["manual_assertions"][0]["status"],
                             manual_decision_audit.HELD_STATUS)
            self.assertEqual(report["manual_assertions"][0]["verdict"], "held")

    def test_a_rejected_judgement_does_not_verify_the_provider_semantics(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            ledger, audit = self.four_judgements(root, verdict="rejected")
            report = self.f01_report(ledger=ledger, audit_source=audit)
            self.assertNotEqual(report["provider_semantics_status"], "verified")
            self.assertEqual(report["manual_assertions"][0]["status"],
                             manual_decision_audit.HELD_STATUS)
            self.assertEqual(report["manual_assertions"][0]["verdict"], "rejected")

    def test_a_claim_does_not_verify_the_provider_semantics(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            records = [self.judgement(assertion_id=step_id, assertion_text=PROSE[step_id],
                                      audit_id=f"audit-{index}")
                       for index, step_id in enumerate(PROSE_STEPS, start=1)]
            ledger = self.write_ledger(root, records)
            report = self.f01_report(ledger=ledger, audit_source=None)
            self.assertEqual(report["provider_semantics_status"], "not_verified")
            self.assertEqual([entry["status"] for entry in report["manual_assertions"]],
                             [manual_decision_audit.UNVERIFIED_STATUS] * 4)

    def test_the_machine_checks_and_the_prose_assertions_stay_two_columns(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            ledger, audit = self.four_judgements(root)
            report = self.f01_report(ledger=ledger, audit_source=audit)
            self.assertTrue(report["checks"])
            self.assertTrue(report["manual_assertions"])
            # The two columns do not borrow each other's words.
            for entry in report["manual_assertions"]:
                self.assertNotIn("operator", entry)
                self.assertNotIn("path", entry)
                self.assertNotIn("intent", entry)
            for check in report["checks"]:
                self.assertNotIn("verdict", check)
                self.assertNotIn("authority", check)
                self.assertNotIn("audit_id", check)
            # A machine check that held says nothing about a prose assertion.
            self.assertTrue(all(check["status"] == "passed" for check in report["checks"]))

    def test_the_aggregate_does_not_overwrite_either_column(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            ledger, audit = self.four_judgements(root)
            report = self.f01_report(ledger=ledger, audit_source=audit)
            self.assertEqual(report["provider_semantics_status"], "verified")
            self.assertEqual(len(report["manual_assertions"]), 4)
            self.assertTrue(report["checks"])
            self.assertNotIn("provider_semantics_status",
                             {key for entry in report["manual_assertions"] for key in entry})
            self.assertNotIn("provider_semantics_status",
                             {key for check in report["checks"] for key in check})


class EndToEndReleaseTests(JudgementChannelTestCase):
    """The whole chain: a judgement, the runner, and the two release paths."""

    def _judged_report(self, root: Path, *, verdict="passed", attested=True):
        ledger, audit = self.four_judgements(root, verdict=verdict)
        return self.f01_report(ledger=ledger, audit_source=audit if attested else None)

    def test_four_attested_judgements_stop_the_main_path_saying_not_run(self):
        with tempfile.TemporaryDirectory() as directory:
            report = self._judged_report(Path(directory))
            second, main_verdict = self.release_verdicts(report)
            self.assertEqual(second, "PASS")
            self.assertEqual(main_verdict, "PASS")

    def test_a_judgement_nobody_made_still_leaves_the_main_path_not_run(self):
        with tempfile.TemporaryDirectory() as directory:
            report = self.f01_report()
            second, main_verdict = self.release_verdicts(report)
            self.assertEqual(second, "NOT RUN")
            self.assertEqual(main_verdict, "NOT RUN")

    def test_one_held_judgement_does_not_pass(self):
        with tempfile.TemporaryDirectory() as directory:
            report = self._judged_report(Path(directory), verdict="held")
            second, main_verdict = self.release_verdicts(report)
            self.assertEqual(second, "NOT RUN")
            self.assertEqual(main_verdict, "NOT RUN")

    def test_one_rejected_judgement_does_not_pass(self):
        with tempfile.TemporaryDirectory() as directory:
            report = self._judged_report(Path(directory), verdict="rejected")
            second, main_verdict = self.release_verdicts(report)
            self.assertEqual(second, "NOT RUN")
            self.assertEqual(main_verdict, "NOT RUN")

    def test_one_claim_does_not_pass(self):
        with tempfile.TemporaryDirectory() as directory:
            report = self._judged_report(Path(directory), attested=False)
            second, main_verdict = self.release_verdicts(report)
            self.assertEqual(second, "NOT RUN")
            self.assertEqual(main_verdict, "NOT RUN")

    def test_a_refused_judgement_never_reaches_a_report(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            record = self.judgement()
            record.pop("actor")
            ledger = self.write_ledger(root, [record])
            with self.assertRaises(ManualDecisionError):
                self.f01_report(ledger=ledger)

    def test_the_runner_cli_writes_a_refusal_report_for_a_refused_judgement(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            ledger = self.write_ledger(root, [self.judgement(run_id=OTHER_RUN_ID)])
            destination = root / "report.json"

            def refuse(*args, **kwargs):
                refusal = ManualDecisionError("F01: judgement 1: the judgement names another run")
                refusal.workflow_id = "F01"
                raise refusal

            stderr = io.StringIO()
            with patch.object(ScenarioRunner, "from_files", side_effect=refuse), \
                    redirect_stderr(stderr), redirect_stdout(io.StringIO()):
                status = main(["--report", str(destination), "--manual-decisions", str(ledger),
                               "--run-id", RUN_ID])
            self.assertEqual(status, 1)
            report = json.loads(destination.read_text(encoding="utf-8"))
            self.assertEqual(report["runner_status"], "failed")
            self.assertEqual(report["run_id"], RUN_ID)
            self.assertEqual(report["workflow"], "F01")
            self.assertEqual(report["failures"][0]["kind"], "manual_decision_rejected")
            self.assertEqual(report["provider_semantics_status"], "not_verified")
            self.assertNotIn("Traceback", stderr.getvalue())

    def test_the_runner_cli_passes_the_ledger_and_the_audit_source_to_the_runner(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            ledger = self.write_ledger(root, [self.judgement()])
            audit = self.write_audit(root, [self.audit_record()])
            captured = {}

            def capture(openapi_path, spec_path, **kwargs):
                captured.update(kwargs)
                raise WorkflowError("stop after the capture")

            with patch.object(ScenarioRunner, "from_files", side_effect=capture), \
                    redirect_stdout(io.StringIO()):
                status = main(["--manual-decisions", str(ledger),
                               "--audit-source", str(audit), "--run-id", RUN_ID])
            self.assertEqual(status, 1)
            self.assertEqual(captured["decision_ledger"], ledger)
            self.assertEqual(captured["audit_source"], str(audit))
            self.assertEqual(captured["run_id"], RUN_ID)


class ContractFactTests(unittest.TestCase):
    """The facts this channel is built on, read from the contract itself."""

    def test_the_contract_leaves_exactly_four_prose_assertions(self):
        contract = json.loads(CONTRACT_PATH.read_text(encoding="utf-8"))
        prose = [(step["id"], text) for step in contract["steps"]
                 for text in step.get("assertions", [])]
        self.assertEqual(len(prose), 4)
        self.assertEqual([step_id for step_id, _ in prose], PROSE_STEPS)
        self.assertEqual(dict(prose), PROSE)
        self.assertEqual(contract["manual_assertions"], [])

    def test_the_contract_has_thirty_one_steps(self):
        contract = json.loads(CONTRACT_PATH.read_text(encoding="utf-8"))
        self.assertEqual(len(contract["steps"]), 31)


if __name__ == "__main__":
    unittest.main()
