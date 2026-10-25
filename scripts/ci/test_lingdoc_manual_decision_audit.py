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
import re
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
DOMAIN_SOURCE = ROOT / "internal/lingdoc/workspacecore/manual_judgement_audit.go"


def go_struct_json_tags(name: str) -> list[str]:
    """The JSON fields of one Go struct, in the order the struct declares them.

    The server's shapes are the domain side's Go types.  Reading their tags
    straight out of the source is what lets the contract follow the
    implementation instead of copying it.
    """
    source = DOMAIN_SOURCE.read_text(encoding="utf-8")
    start = source.index("type " + name + " struct {")
    body = source[start:]
    body = body[:body.index("\n}")]
    tags = []
    for line in body.splitlines():
        match = re.search(r'json:"([^",]+)', line)
        if match:
            tags.append(match.group(1))
    return tags

RUN_ID = "f01-20261025T010000Z-batch01"

# The server side of the channel (#649) answers with the string it stored.
# The timestamp is deliberately not the canonical form: the export has to
# echo what was written down, not a reformatted copy of it.
SERVER_OCCURRED_AT = "2026-10-25T01:33:41.5105+00:00"
SERVER_EVIDENCE_REF = f"run:{RUN_ID}/f01-report.json"
SERVER_AUDIT_ID = "audit-649-0001"

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


class ServerExportShapeTests(unittest.TestCase):
    """The server's export shape is what the runner can actually consume.

    Issue #649 adds the server side of this channel: a write route that
    answers with the authoritative judgement record, and a read route that
    exports the audit records.  The runner checks a judgement against those
    exported records, so the export's shape is not a documentation detail:
    a field the runner cannot read by name is a field the runner cannot
    check, and a judgement that cannot be checked stays a claim.

    These cases feed the export shape the server publishes into the runner's
    own ``audit_index`` / ``verify_against_audit`` and read the authority off
    the result, so the tier the release paths read is the tier the server's
    shape earns.
    """

    def server_ledger_record(self) -> dict[str, str]:
        """The ten fields the write route answers with.

        The answer is filed as a ledger line unchanged, so it carries
        exactly the fields the ledger requires -- no more, no less.
        """
        return {
            "run_id": RUN_ID,
            "assertion_id": "F01-06c",
            "assertion_text": PROSE["F01-06c"],
            "verdict": "passed",
            "actor": "user-owner",
            "authorization": "project_owner",
            "channel": "unverified_channel",
            "occurred_at": SERVER_OCCURRED_AT,
            "evidence_ref": SERVER_EVIDENCE_REF,
            "audit_id": SERVER_AUDIT_ID,
        }

    def server_export_record(self) -> dict[str, str]:
        """The exported audit record: the seven audit fields at the top level.

        The three channel keys are the two-layer channel semantics; the
        runner compares the seven named fields and ignores the rest.
        """
        return {
            "audit_id": SERVER_AUDIT_ID,
            "actor": "user-owner",
            "authorization": "project_owner",
            "channel": "unverified_channel",
            "occurred_at": SERVER_OCCURRED_AT,
            "assertion_id": "F01-06c",
            "run_id": RUN_ID,
            "evidence_ref": SERVER_EVIDENCE_REF,
            "channel_class": "user_session",
            "channel_ui_origin": "unprovable",
            "channel_automation": "unprovable",
        }

    def test_the_server_export_shape_attests_the_judgement(self):
        """The export shape earns the tier the release paths read.

        This is the whole point of the export: the runner can check the
        judgement against it, so the judgement is ``server_attested`` and
        not a ``claimed`` one.
        """
        record = self.server_ledger_record()
        index = manual_decision_audit.audit_index([self.server_export_record()])
        self.assertTrue(manual_decision_audit.verify_against_audit(record, index))
        self.assertEqual(
            manual_decision_audit.resolve_authority(record, index),
            manual_decision_audit.AUTHORITY_SERVER_ATTESTED)
        self.assertEqual(
            manual_decision_audit.decision_status(
                record["verdict"], manual_decision_audit.resolve_authority(record, index)),
            manual_decision_audit.PASSED_STATUS)

    def test_the_runner_ingests_the_server_export_as_an_attested_judgement(self):
        """The whole runner path, on the server's own shape.

        The ledger holds what the write route answered; the index holds what
        the export answered.  The runner binds them and says how much of the
        judgement it is willing to believe.
        """
        record = self.server_ledger_record()
        index = manual_decision_audit.audit_index([self.server_export_record()])
        decisions = manual_decision_audit.ingest(
            [record], RUN_ID, {"F01-06c": [PROSE["F01-06c"]]}, index)
        self.assertEqual(len(decisions), 1)
        decision = decisions[0]
        self.assertEqual(decision["authority"], manual_decision_audit.AUTHORITY_SERVER_ATTESTED)
        self.assertEqual(decision["channel_verifiability"], "verified")
        self.assertEqual(decision["assertion_status"], manual_decision_audit.PASSED_STATUS)

    def test_the_exported_records_are_readable_from_a_file_by_the_runner(self):
        """The runner reads the export the way it reads any audit source.

        ``load_audit_source`` is the runner's own door into an audit
        export, so the export shape has to survive it: the array of records
        is what the runner indexes.
        """
        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory) / "audit-records.json"
            source.write_text(
                json.dumps([self.server_export_record()], ensure_ascii=False),
                encoding="utf-8")
            loaded = manual_decision_audit.load_audit_source(source)
            self.assertEqual(len(loaded), 1)
            index = manual_decision_audit.audit_index(loaded)
            record = self.server_ledger_record()
            self.assertEqual(
                manual_decision_audit.resolve_authority(record, index),
                manual_decision_audit.AUTHORITY_SERVER_ATTESTED)

    def test_a_missing_audit_field_does_not_attest(self):
        """An audit record that leaves a field out cannot prove that field.

        One field short of the seven, the judgement stays a claim: the
        runner says so instead of pretending the field was there.
        """
        record = self.server_ledger_record()
        for field in manual_decision_audit.AUDIT_FIELDS:
            with self.subTest(field=field):
                exported = self.server_export_record()
                del exported[field]
                index = manual_decision_audit.audit_index([exported])
                self.assertFalse(manual_decision_audit.verify_against_audit(record, index))
                self.assertEqual(
                    manual_decision_audit.resolve_authority(record, index),
                    manual_decision_audit.AUTHORITY_CLAIMED)

    def test_a_differing_audit_field_does_not_attest(self):
        """A field the export states differently does not prove the judgement.

        The comparison is byte for byte, so even a differently formatted
        timestamp is a disagreement.
        """
        record = self.server_ledger_record()
        for field in manual_decision_audit.AUDIT_FIELDS:
            with self.subTest(field=field):
                exported = self.server_export_record()
                exported[field] = exported[field] + "-different"
                index = manual_decision_audit.audit_index([exported])
                self.assertFalse(manual_decision_audit.verify_against_audit(record, index))
                self.assertEqual(
                    manual_decision_audit.resolve_authority(record, index),
                    manual_decision_audit.AUTHORITY_CLAIMED)

    def test_a_nested_audit_field_does_not_attest(self):
        """The audit fields have to be at the top level.

        A record that hides one of them one level down cannot be read by
        name, so the runner cannot check it.
        """
        record = self.server_ledger_record()
        exported = self.server_export_record()
        exported["judgement"] = {field: exported.pop(field)
                                 for field in manual_decision_audit.AUDIT_FIELDS}
        index = manual_decision_audit.audit_index([exported])
        self.assertFalse(manual_decision_audit.verify_against_audit(record, index))
        self.assertEqual(
            manual_decision_audit.resolve_authority(record, index),
            manual_decision_audit.AUTHORITY_CLAIMED)

    def test_the_extra_channel_keys_do_not_disturb_the_comparison(self):
        """The three channel keys are extra, and extras do not count.

        The runner compares seven named fields; the export carries three
        more that say what the server could prove about the channel.
        """
        exported = self.server_export_record()
        self.assertEqual(
            set(exported) - set(manual_decision_audit.AUDIT_FIELDS) - {"audit_id"},
            {"channel_class", "channel_ui_origin", "channel_automation"})
        index = manual_decision_audit.audit_index([exported])
        record = self.server_ledger_record()
        self.assertEqual(
            manual_decision_audit.resolve_authority(record, index),
            manual_decision_audit.AUTHORITY_SERVER_ATTESTED)

    def test_the_export_names_only_the_channel_the_server_can_prove(self):
        """The channel the server cannot prove says so.

        A UI click and a script replay are the same credential, so the
        server claims the credential class and refuses to claim more.
        """
        exported = self.server_export_record()
        self.assertEqual(exported["channel"], "unverified_channel")
        self.assertEqual(exported["channel_ui_origin"], "unprovable")
        self.assertEqual(exported["channel_automation"], "unprovable")


class ServerContractShapeTests(unittest.TestCase):
    """The published contract is the shape the server and the runner share.

    The transport refuses a request that names the server-derived fields,
    and the export publishes the seven audit fields at the top level.  Both
    facts live in the contract, so they are read from the contract here:
    a contract that drifts from the server stops being a description of it.
    """

    def setUp(self):
        self.contract = json.loads(OPENAPI_PATH.read_text(encoding="utf-8"))
        self.schemas = self.contract["components"]["schemas"]

    def test_the_request_schema_names_only_the_five_caller_fields(self):
        """A caller cannot say who judged.

        The five fields a caller may send are the whole request body, and
        the body is closed: the four server-derived fields cannot even be
        written down, let alone believed.
        """
        schema = self.schemas["ManualJudgementInput"]
        self.assertIs(schema.get("additionalProperties"), False)
        self.assertEqual(
            set(schema["properties"]),
            {"assertion_id", "assertion_text", "verdict", "run_id", "evidence_ref"})
        for forbidden in ("actor", "authorization", "channel", "occurred_at", "audit_id"):
            self.assertNotIn(forbidden, schema["properties"])

    def test_the_answer_carries_exactly_the_ten_ledger_fields(self):
        """The answer can be filed as a ledger line unchanged."""
        schema = self.schemas["ManualJudgementRecord"]
        self.assertIs(schema.get("additionalProperties"), False)
        self.assertEqual(set(schema["properties"]), set(manual_decision_audit.REQUIRED_FIELDS))
        self.assertEqual(set(schema["required"]), set(manual_decision_audit.REQUIRED_FIELDS))

    def test_the_export_schema_keeps_the_audit_fields_at_the_top_level(self):
        """The published export carries the seven fields by name."""
        schema = self.schemas["ManualJudgementAudit"]
        self.assertIs(schema.get("additionalProperties"), False)
        for field in manual_decision_audit.AUDIT_FIELDS:
            self.assertIn(field, schema["properties"])
        self.assertIn("audit_id", schema["properties"])
        for extra in ("channel_class", "channel_ui_origin", "channel_automation"):
            self.assertIn(extra, schema["properties"])

    def test_the_contract_pins_the_channel_the_server_can_prove(self):
        """The published channel is the one the server can stand behind."""
        for name in ("ManualJudgementRecord", "ManualJudgementAudit"):
            with self.subTest(schema=name):
                schema = self.schemas[name]
                self.assertEqual(schema["properties"]["channel"].get("enum"),
                                 ["unverified_channel"])
        # The two layers the server cannot prove are named on the export
        # record; the ten-field answer carries the channel itself.
        export = self.schemas["ManualJudgementAudit"]
        self.assertEqual(export["properties"]["channel_ui_origin"].get("enum"),
                         ["unprovable"])
        self.assertEqual(export["properties"]["channel_automation"].get("enum"),
                         ["unprovable"])
        self.assertNotIn("channel_ui_origin", self.schemas["ManualJudgementRecord"]["properties"])

    def test_the_published_shapes_follow_the_domain_types_field_for_field(self):
        """The contract is the domain's own shapes, not a copy of them.

        The three shapes are the domain side's Go types: the request body,
        the authoritative record and the export record.  The published
        schemas follow them field for field, in the order the structs
        declare them, so a field the domain adds or renames shows up here
        instead of on the wire.
        """
        for go_type, schema_name in (("ManualJudgementInput", "ManualJudgementInput"),
                                     ("ManualJudgementRecord", "ManualJudgementRecord"),
                                     ("ManualJudgementAudit", "ManualJudgementAudit")):
            with self.subTest(go_type=go_type):
                tags = go_struct_json_tags(go_type)
                self.assertTrue(tags, "the Go type " + go_type + " declares no JSON field")
                self.assertEqual(list(self.schemas[schema_name]["properties"]), tags)
                self.assertEqual(set(self.schemas[schema_name]["required"]), set(tags))

    def test_the_two_operations_are_published(self):
        """Both routes are published, and they say where they are registered."""
        paths = self.contract["paths"]
        write = paths["/projects/{projectId}/manual-judgements"]["post"]
        read = paths["/projects/{projectId}/manual-judgements/audit-records"]["get"]
        self.assertEqual(write["operationId"], "recordManualJudgement")
        self.assertEqual(read["operationId"], "listManualJudgementAuditRecords")
        for operation in (write, read):
            self.assertEqual(operation["x-lingdoc-route-source"], "manual_judgement_routes.go")


if __name__ == "__main__":
    unittest.main()
