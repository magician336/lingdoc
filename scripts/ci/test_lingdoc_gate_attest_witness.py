"""Guard tests for the gate step that assembles the reuse evidence (#631).

The witness decision (``lingdoc_gate_attest_reuse``) is a pure function; the
gate step that calls it has to fetch the records first, and every way that
fetching can fail -- a missing anchor, an expired artifact, a GitHub API
error, a record of the wrong shape -- must end in the same answer: no reuse,
full rerun, gate still green.  This file pins that assembly layer.

The rulings the earlier tickets of spec #627 made are pinned here too:

* the candidate's ``status`` and ``conclusion`` always come from the run
  record the Actions API reports, never from the anchor's artifact-side
  ``conclusion`` (#628: a frontend failure behind a passing scope step must
  not be read as a reusable green run); the anchor is used for the
  cross-check only;
* a record whose fields cannot all be read is a fallback, not a guess.

The tests never reach the network.  They hand the assembly layer a stub API
that answers from fixtures, the same way the queue-state tests do, and a
temporary artifact directory that holds the current run's own anchor.
"""
from __future__ import annotations

import contextlib
import io
import json
import tempfile
import unittest
import zipfile
from pathlib import Path

import lingdoc_gate_attest_reuse as reuse
import lingdoc_gate_attest_witness as witness
import lingdoc_gate_queue_state as queue_state

HEAD = "a" * 40
OTHER_HEAD = "b" * 40
MAIN = "c" * 40
OTHER_MAIN = "d" * 40
PR = 627
RUN_ID = 40211
CURRENT_RUN_ID = 40299
ARTIFACT_ID = 7
REPO = "magician336/lingdoc"


def anchor_record(**overrides):
    """A gate anchor, as the scope step of a full run freezes it."""
    record = {
        "head_sha": HEAD,
        "main_sha": MAIN,
        "main_sha_source": "merge_ref_parent",
        "main_sha_read_at": "2026-10-24T00:00:00+00:00",
        "run_id": RUN_ID,
        "pr": PR,
        "workflow": reuse.GATE_WORKFLOW_NAME,
        "conclusion": "success",
    }
    record.update(overrides)
    return record


def run_record(**overrides):
    """A gate workflow run, as the Actions API reports it."""
    record = {
        "id": RUN_ID,
        "name": reuse.GATE_WORKFLOW_NAME,
        "head_sha": HEAD,
        "status": "completed",
        "conclusion": "success",
    }
    record.update(overrides)
    return record


def artifact_record(**overrides):
    """The run's checks artifact, as the Artifacts API reports it."""
    record = {
        "id": ARTIFACT_ID,
        "name": f"lingdoc-checks-{RUN_ID}-1",
        "expired": False,
    }
    record.update(overrides)
    return record


def artifact_zip(files: dict[str, object]) -> bytes:
    """A checks artifact zip holding the given files."""
    buffer = io.BytesIO()
    with zipfile.ZipFile(buffer, "w") as bundle:
        for name, content in files.items():
            if isinstance(content, bytes):
                bundle.writestr(name, content)
            else:
                bundle.writestr(name, json.dumps(content))
    return buffer.getvalue()


class StubApi:
    """A GitHub API that answers from fixtures instead of the network."""

    def __init__(self, runs=(), artifacts=None, zips=None):
        self.run_records = list(runs)
        self.artifact_records = dict(artifacts or {})
        self.zip_records = dict(zips or {})
        self.calls: list[tuple] = []

    def runs_for_head(self, sha):
        self.calls.append(("runs_for_head", sha))
        return self.run_records

    def artifacts(self, run_id):
        self.calls.append(("artifacts", run_id))
        return self.artifact_records.get(run_id, [])

    def artifact_zip(self, artifact_id):
        self.calls.append(("artifact_zip", artifact_id))
        data = self.zip_records.get(artifact_id)
        if data is None:
            raise queue_state.GateStateError(
                f"artifact {artifact_id} could not be downloaded")
        return data


class _Temp:
    """A temporary artifact directory holding the current run's anchor."""

    def __init__(self, current: dict | None = None):
        self.folder = Path(tempfile.mkdtemp())
        self.add("gate-anchor.json", current if current is not None
                 else anchor_record(run_id=CURRENT_RUN_ID))

    def add(self, name: str, content) -> None:
        data = content if isinstance(content, bytes) else json.dumps(content)
        (self.folder / name).write_text(data, encoding="utf-8")

    def read(self, name: str) -> str:
        return (self.folder / name).read_text(encoding="utf-8")


def _qualified_fixture():
    """Everything a reuse needs: the current anchor and one green run."""
    fixture = _Temp()
    candidate_zip = artifact_zip({
        "gate-anchor.json": anchor_record(),
        "scope.json": {"surface": "go"},
    })
    api = StubApi(
        runs=[run_record(), run_record(id=CURRENT_RUN_ID)],
        artifacts={RUN_ID: [artifact_record()]},
        zips={ARTIFACT_ID: candidate_zip},
    )
    return fixture, api


def assert_fallback(test, decision):
    test.assertFalse(decision["reuse"])
    test.assertFalse(decision["passed"])
    test.assertTrue(decision["missing"])
    test.assertEqual(decision["verdict"], queue_state.VERDICT_MISSING)
    test.assertIsNone(decision["reused_run_id"])


def assert_reuse(test, decision):
    test.assertTrue(decision["reuse"])
    test.assertTrue(decision["passed"])
    test.assertFalse(decision["missing"])
    test.assertEqual(decision["verdict"], queue_state.VERDICT_PASSED)
    test.assertEqual(decision["reused_run_id"], RUN_ID)


class ReusePathTests(unittest.TestCase):
    """The assembly hands the decision a complete record, and reuse happens."""

    def test_a_qualified_green_run_is_reused(self):
        fixture, api = _qualified_fixture()
        decision = witness.decide("ready_for_review", fixture.folder, api,
                                  CURRENT_RUN_ID)
        assert_reuse(self, decision)
        evidence = decision["evidence"]
        self.assertEqual(evidence["run_id"], RUN_ID)
        for name in reuse.CONDITION_NAMES:
            self.assertTrue(evidence["conditions"][name], name)
        self.assertEqual(evidence["compared"]["pr"]["current"], PR)
        self.assertEqual(evidence["compared"]["head_sha"]["current"], HEAD)
        self.assertEqual(evidence["compared"]["main_sha"]["current"], MAIN)

    def test_the_candidate_status_and_conclusion_come_from_the_run_record(self):
        """Ruling of #628: the anchor's conclusion is a scope-step verdict, so
        a run whose later step failed must not be reused -- even though its
        own anchor still says success."""
        fixture, api = _qualified_fixture()
        api.run_records = [run_record(conclusion="failure"), run_record(
            id=CURRENT_RUN_ID)]
        decision = witness.decide("ready_for_review", fixture.folder, api,
                                  CURRENT_RUN_ID)
        assert_fallback(self, decision)
        self.assertIn("failure", decision["reason"])

    def test_the_reused_run_scope_record_is_handed_on(self):
        """The reuse path carries the reused run's scope verdict into this
        run's artifact, so require-coverage stays auditable."""
        fixture, api = _qualified_fixture()
        witness.decide("ready_for_review", fixture.folder, api, CURRENT_RUN_ID)
        handed_on = json.loads(fixture.read("reused-scope.json"))
        self.assertEqual(handed_on, {"surface": "go"})
        handed_anchor = json.loads(fixture.read("reused-gate-anchor.json"))
        self.assertEqual(handed_anchor, anchor_record())

    def test_the_current_run_is_never_its_own_candidate(self):
        fixture, api = _qualified_fixture()
        api.run_records = [run_record(id=CURRENT_RUN_ID)]
        decision = witness.decide("ready_for_review", fixture.folder, api,
                                  CURRENT_RUN_ID)
        assert_fallback(self, decision)


class TriggerBoundaryTests(unittest.TestCase):
    """Only ready_for_review asks; every other trigger stays untouched."""

    def test_other_triggers_fall_back_without_asking_github(self):
        for event in ("opened", "synchronize", "reopened", "workflow_dispatch",
                      "push"):
            with self.subTest(event=event):
                fixture, api = _qualified_fixture()
                decision = witness.decide(event, fixture.folder, api,
                                          CURRENT_RUN_ID)
                assert_fallback(self, decision)
                self.assertEqual(api.calls, [],
                                 f"{event}: the witness must not touch the API")
                self.assertIn(event, decision["reason"])


class FallbackTests(unittest.TestCase):
    """Every way the records can fail ends in a full rerun."""

    def test_a_missing_current_anchor_falls_back_without_asking_github(self):
        fixture, api = _qualified_fixture()
        (fixture.folder / "gate-anchor.json").unlink()
        decision = witness.decide("ready_for_review", fixture.folder, api,
                                  CURRENT_RUN_ID)
        assert_fallback(self, decision)
        self.assertEqual(api.calls, [])

    def test_a_malformed_current_anchor_falls_back(self):
        fixture, api = _qualified_fixture()
        (fixture.folder / "gate-anchor.json").write_text("{not json",
                                                         encoding="utf-8")
        decision = witness.decide("ready_for_review", fixture.folder, api,
                                  CURRENT_RUN_ID)
        assert_fallback(self, decision)

    def test_a_current_anchor_without_a_main_tip_falls_back(self):
        fixture, api = _qualified_fixture()
        fixture.add("gate-anchor.json", anchor_record(run_id=CURRENT_RUN_ID,
                                                      main_sha=""))
        decision = witness.decide("ready_for_review", fixture.folder, api,
                                  CURRENT_RUN_ID)
        assert_fallback(self, decision)

    def test_no_earlier_gate_run_for_this_head_falls_back(self):
        fixture, api = _qualified_fixture()
        api.run_records = [run_record(id=CURRENT_RUN_ID)]
        decision = witness.decide("ready_for_review", fixture.folder, api,
                                  CURRENT_RUN_ID)
        assert_fallback(self, decision)

    def test_a_run_of_another_workflow_is_no_candidate(self):
        fixture, api = _qualified_fixture()
        api.run_records = [run_record(name="Frontend"),
                           run_record(id=CURRENT_RUN_ID)]
        decision = witness.decide("ready_for_review", fixture.folder, api,
                                  CURRENT_RUN_ID)
        assert_fallback(self, decision)

    def test_an_incomplete_run_record_falls_back(self):
        fixture, api = _qualified_fixture()
        del api.run_records[0]["status"]
        decision = witness.decide("ready_for_review", fixture.folder, api,
                                  CURRENT_RUN_ID)
        assert_fallback(self, decision)

    def test_a_latest_run_that_failed_is_not_leapfrogged(self):
        """A failing run of the same merge ref is itself evidence; an older
        green run behind it must not be reused (user story 4)."""
        fixture, api = _qualified_fixture()
        api.run_records = [run_record(id=100),
                           run_record(id=200, conclusion="failure"),
                           run_record(id=CURRENT_RUN_ID)]
        api.artifact_records = {100: [artifact_record(id=71)],
                                200: [artifact_record(id=72)]}
        api.zip_records = {71: artifact_zip({"gate-anchor.json":
                                             anchor_record(run_id=100)}),
                           72: artifact_zip({"gate-anchor.json":
                                             anchor_record(run_id=200,
                                                           conclusion="failure")})}
        decision = witness.decide("ready_for_review", fixture.folder, api,
                                  CURRENT_RUN_ID)
        assert_fallback(self, decision)
        self.assertIn("failure", decision["reason"])

    def test_a_candidate_without_checks_artifact_falls_back(self):
        fixture, api = _qualified_fixture()
        api.artifact_records = {RUN_ID: []}
        decision = witness.decide("ready_for_review", fixture.folder, api,
                                  CURRENT_RUN_ID)
        assert_fallback(self, decision)

    def test_a_candidate_artifact_without_the_anchor_falls_back(self):
        fixture, api = _qualified_fixture()
        api.zip_records = {ARTIFACT_ID: artifact_zip({"scope.json": {}})}
        decision = witness.decide("ready_for_review", fixture.folder, api,
                                  CURRENT_RUN_ID)
        assert_fallback(self, decision)

    def test_a_malformed_candidate_anchor_falls_back(self):
        fixture, api = _qualified_fixture()
        api.zip_records = {ARTIFACT_ID: artifact_zip({
            "gate-anchor.json": {"head_sha": HEAD, "pr": PR}})}
        decision = witness.decide("ready_for_review", fixture.folder, api,
                                  CURRENT_RUN_ID)
        assert_fallback(self, decision)

    def test_an_anchor_of_another_run_falls_back(self):
        """The anchor and the run record must tell the same story."""
        fixture, api = _qualified_fixture()
        api.zip_records = {ARTIFACT_ID: artifact_zip({
            "gate-anchor.json": anchor_record(run_id=999)})}
        decision = witness.decide("ready_for_review", fixture.folder, api,
                                  CURRENT_RUN_ID)
        assert_fallback(self, decision)

    def test_an_anchor_of_another_head_falls_back(self):
        fixture, api = _qualified_fixture()
        api.zip_records = {ARTIFACT_ID: artifact_zip({
            "gate-anchor.json": anchor_record(head_sha=OTHER_HEAD)})}
        decision = witness.decide("ready_for_review", fixture.folder, api,
                                  CURRENT_RUN_ID)
        assert_fallback(self, decision)

    def test_an_anchor_that_says_failure_while_the_run_says_success_falls_back(
            self):
        """A record that contradicts itself is not evidence of anything."""
        fixture, api = _qualified_fixture()
        api.zip_records = {ARTIFACT_ID: artifact_zip({
            "gate-anchor.json": anchor_record(conclusion="failure")})}
        decision = witness.decide("ready_for_review", fixture.folder, api,
                                  CURRENT_RUN_ID)
        assert_fallback(self, decision)

    def test_an_api_failure_falls_back(self):
        for breaking in ("runs_for_head", "artifacts", "artifact_zip"):
            with self.subTest(call=breaking):
                fixture, api = _qualified_fixture()

                def explode(*args, _call=breaking, **kwargs):
                    raise queue_state.GateStateError("rate limited")

                setattr(api, breaking, explode)
                decision = witness.decide("ready_for_review", fixture.folder,
                                          api, CURRENT_RUN_ID)
                assert_fallback(self, decision)


class CommandLineTests(unittest.TestCase):
    """The gate step reads the answer from outputs and the summary."""

    def _run(self, api, event="ready_for_review", fixture=None):
        output = io.StringIO()
        summary = io.StringIO()
        stdout = io.StringIO()
        env = {
            "GITHUB_EVENT_NAME": event,
            "GITHUB_RUN_ID": str(CURRENT_RUN_ID),
            "GITHUB_REPOSITORY": REPO,
            "GITHUB_OUTPUT": "output.txt",
            "GITHUB_STEP_SUMMARY": "summary.md",
        }
        root = fixture.folder
        (root / "output.txt").write_text("", encoding="utf-8")
        (root / "summary.md").write_text("", encoding="utf-8")
        env["GITHUB_OUTPUT"] = str(root / "output.txt")
        env["GITHUB_STEP_SUMMARY"] = str(root / "summary.md")
        with contextlib.redirect_stdout(stdout):
            code = witness.main(["--artifact-dir", str(root)], api=api, env=env)
        return (code, (root / "output.txt").read_text(encoding="utf-8"),
                (root / "summary.md").read_text(encoding="utf-8"),
                json.loads(stdout.getvalue()))

    def test_a_reuse_publishes_outputs_summary_and_a_zero_exit(self):
        fixture, api = _qualified_fixture()
        code, output, summary, printed = self._run(api, fixture=fixture)
        self.assertEqual(code, 0)
        self.assertIn(f"reuse=true\nreused_run_id={RUN_ID}", output)
        self.assertIn("REUSED run #40211", summary)
        self.assertIn(f"https://github.com/{REPO}/actions/runs/{RUN_ID}",
                      summary)
        for name in reuse.CONDITION_NAMES:
            self.assertIn(name, summary)
        self.assertTrue(printed["reuse"])

    def test_a_fallback_publishes_reuse_false_and_a_zero_exit(self):
        fixture, api = _qualified_fixture()
        api.run_records = [run_record(id=CURRENT_RUN_ID)]
        code, output, summary, printed = self._run(api, fixture=fixture)
        self.assertEqual(code, 0)
        self.assertIn("reuse=false\n", output)
        self.assertIn("reused_run_id=\n", output)
        self.assertIn("full rerun", summary)
        self.assertFalse(printed["reuse"])

    def test_an_api_failure_still_exits_zero(self):
        fixture, api = _qualified_fixture()

        def explode(*args, **kwargs):
            raise queue_state.GateStateError("timeout")

        api.runs_for_head = explode
        code, output, summary, printed = self._run(api, fixture=fixture)
        self.assertEqual(code, 0)
        self.assertIn("reuse=false\n", output)
        self.assertFalse(printed["reuse"])

    def test_workflow_dispatch_never_reuses(self):
        fixture, api = _qualified_fixture()
        code, output, summary, printed = self._run(
            api, event="workflow_dispatch", fixture=fixture)
        self.assertEqual(code, 0)
        self.assertIn("reuse=false\n", output)
        self.assertEqual(api.calls, [])


if __name__ == "__main__":
    unittest.main()
