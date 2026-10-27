"""Guard tests for the witness-reuse judgement (#629, spec #627).

A pull request that turns from Draft to Ready reruns the whole gate for a
merge ref that has already been tested green.  The witness answers one
question from machine records alone: may this gate run end green right now
because an earlier run of the same pull request already tested **the same**
head against **the same** main tip and finished green?

The five conditions of spec #627 each get one pass test and one violation
test; the witness layer's own failures -- a missing record, a malformed
field -- must all fall back, and never one of them may read as reuse.  The
last class pins the queue-state shape guarantee: a reuse verdict is, in the
machine view, exactly the green verdict a full run would have published.

The tests never reach the network.  They feed the judgement the anchor
record the gate artifact publishes and the current observation of the pull
request, and ask it what it concludes.
"""
from __future__ import annotations

import unittest

import lingdoc_gate_attest_reuse as reuse
import lingdoc_gate_queue_state as queue_state
import lingdoc_gate_surfaces as manifest

HEAD = "a" * 40
OTHER_HEAD = "b" * 40
MAIN = "c" * 40
OTHER_MAIN = "d" * 40
PR = 629
RUN_ID = 40211234


def current_observation(**overrides):
    """The current observation of the pull request asking to reuse."""
    record = {"pr": PR, "head_sha": HEAD, "main_sha": MAIN}
    record.update(overrides)
    return record


def anchor_record(**overrides):
    """An anchor record of one earlier gate run, as the artifact freezes it."""
    record = {
        "pr": PR,
        "head_sha": HEAD,
        "main_sha": MAIN,
        "run_id": RUN_ID,
        "conclusion": "success",
        "status": "completed",
        "workflow": reuse.GATE_WORKFLOW_NAME,
    }
    record.update(overrides)
    return record


_UNSET = object()


def decided(current=_UNSET, candidate=_UNSET):
    """The judgement for the given observation and record (both good by default)."""
    return reuse.decide_reuse(
        current_observation() if current is _UNSET else current,
        anchor_record() if candidate is _UNSET else candidate,
    )


def assert_fallback(test, decision):
    """Every fallback reads as not green, in the queue-state machine view."""
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
    test.assertEqual(decision["evidence"]["run_id"], RUN_ID)


class ConditionPassTests(unittest.TestCase):
    """Each of the five conditions holds, and the run is reused."""

    def test_condition_1_the_same_pull_request(self):
        decision = decided(candidate=anchor_record(pr=PR))
        assert_reuse(self, decision)
        compared = decision["evidence"]["compared"]["pr"]
        self.assertTrue(compared["match"])

    def test_condition_2_the_same_head_sha(self):
        decision = decided(candidate=anchor_record(head_sha=HEAD))
        assert_reuse(self, decision)
        compared = decision["evidence"]["compared"]["head_sha"]
        self.assertEqual(compared["current"], HEAD)
        self.assertEqual(compared["candidate"], HEAD)
        self.assertTrue(compared["match"])

    def test_condition_3_the_same_main_tip(self):
        decision = decided(candidate=anchor_record(main_sha=MAIN))
        assert_reuse(self, decision)
        compared = decision["evidence"]["compared"]["main_sha"]
        self.assertEqual(compared["current"], MAIN)
        self.assertEqual(compared["candidate"], MAIN)
        self.assertTrue(compared["match"])

    def test_condition_4_green_and_completed(self):
        decision = decided(candidate=anchor_record(conclusion="success",
                                                   status="completed"))
        assert_reuse(self, decision)
        self.assertEqual(decision["observed_status"], "completed")
        self.assertEqual(decision["observed_conclusion"], "success")

    def test_condition_5_the_same_workflow_by_path(self):
        """The gate workflow may be named by its path as well as by its name."""
        decision = decided(
            candidate=anchor_record(workflow=reuse.GATE_WORKFLOW_PATH))
        assert_reuse(self, decision)

    def test_the_full_green_record_is_reused(self):
        decision = decided()
        assert_reuse(self, decision)
        self.assertIn("same head", decision["reason"])

    def test_no_time_limit_keeps_an_old_run_reusable(self):
        """head+main anchoring makes the merge ref identical; freshness is not
        a condition, so an extra timestamp a record carries is ignored."""
        decision = decided(candidate=anchor_record(
            completed_at="2020-01-01T00:00:00Z"))
        assert_reuse(self, decision)


class ManifestDerivationTests(unittest.TestCase):
    """The gate workflow's identity is the manifest's knowledge, read once.

    The manifest registers the gate workflow as a governance file; the
    judgement reads the path from there and never spells the file name a
    second time -- a second spelling can drift away from the manifest and
    quietly break condition 5.
    """

    def test_the_gate_workflow_path_is_the_manifests_governance_workflow(self):
        workflows = [path for path in manifest.GOVERNANCE_FILES
                     if path.startswith(".github/workflows/")
                     and path.endswith(".yml")]
        self.assertEqual(len(workflows), 1,
                         "the manifest must register exactly one governance "
                         "workflow for the derivation to be unambiguous")
        self.assertEqual(reuse.GATE_WORKFLOW_PATH, workflows[0])
        self.assertEqual(reuse.GATE_WORKFLOW_IDENTIFIERS,
                         (reuse.GATE_WORKFLOW_NAME, reuse.GATE_WORKFLOW_PATH))

    def test_the_workflow_file_name_is_not_spelled_a_second_time(self):
        self.assertFalse(hasattr(reuse, "GATE_WORKFLOW_FILE"),
                         "the workflow file name lives in the manifest only")


class ConditionViolationTests(unittest.TestCase):
    """One condition broken, and the gate falls back to a full rerun."""

    def test_condition_1_a_different_pull_request_is_not_reused(self):
        decision = decided(candidate=anchor_record(pr=630))
        assert_fallback(self, decision)
        self.assertIn("pull request", decision["reason"])

    def test_condition_2_a_different_head_is_not_reused(self):
        decision = decided(candidate=anchor_record(head_sha=OTHER_HEAD))
        assert_fallback(self, decision)
        self.assertIn("head", decision["reason"])

    def test_condition_3_a_different_main_tip_is_not_reused(self):
        decision = decided(candidate=anchor_record(main_sha=OTHER_MAIN))
        assert_fallback(self, decision)
        self.assertIn("main", decision["reason"])

    def test_condition_4_a_failed_run_is_not_reused(self):
        decision = decided(candidate=anchor_record(conclusion="failure"))
        assert_fallback(self, decision)
        self.assertIn("conclusion", decision["reason"])

    def test_condition_4_an_unfinished_run_is_not_reused(self):
        decision = decided(candidate=anchor_record(status="in_progress"))
        assert_fallback(self, decision)
        self.assertIn("completed", decision["reason"])

    def test_condition_5_another_workflow_is_not_reused(self):
        decision = decided(candidate=anchor_record(workflow="Other Workflow"))
        assert_fallback(self, decision)
        self.assertIn("workflow", decision["reason"])


class WitnessFailureTests(unittest.TestCase):
    """A record the witness cannot judge falls back; it never reads as reuse."""

    def test_no_candidate_record_at_all(self):
        decision = decided(candidate=None)
        assert_fallback(self, decision)
        self.assertIn("no usable record", decision["reason"])

    def test_a_candidate_that_is_not_a_record(self):
        decision = decided(candidate=["not", "a", "record"])
        assert_fallback(self, decision)

    def test_a_candidate_record_missing_a_field(self):
        record = anchor_record()
        del record["head_sha"]
        decision = decided(candidate=record)
        assert_fallback(self, decision)
        self.assertIn("head_sha", decision["reason"])

    def test_a_candidate_record_missing_the_status(self):
        record = anchor_record()
        del record["status"]
        decision = decided(candidate=record)
        assert_fallback(self, decision)
        self.assertIn("status", decision["reason"])

    def test_a_malformed_head_sha(self):
        decision = decided(candidate=anchor_record(head_sha=12345))
        assert_fallback(self, decision)
        self.assertIn("head_sha", decision["reason"])

    def test_a_malformed_run_id(self):
        decision = decided(candidate=anchor_record(run_id=str(RUN_ID)))
        assert_fallback(self, decision)
        self.assertIn("run_id", decision["reason"])

    def test_a_malformed_conclusion(self):
        decision = decided(candidate=anchor_record(conclusion=None))
        assert_fallback(self, decision)

    def test_a_malformed_pr(self):
        decision = decided(candidate=anchor_record(pr=True))
        assert_fallback(self, decision)

    def test_no_current_observation_at_all(self):
        decision = decided(current=None)
        assert_fallback(self, decision)

    def test_a_current_observation_missing_a_field(self):
        current = current_observation()
        del current["main_sha"]
        decision = decided(current=current)
        assert_fallback(self, decision)
        self.assertIn("main_sha", decision["reason"])

    def test_a_malformed_current_head(self):
        decision = decided(current=current_observation(head_sha={"sha": HEAD}))
        assert_fallback(self, decision)


class QueueStateShapeTests(unittest.TestCase):
    """A reuse verdict is, to a machine, a full run's green verdict.

    queue-state (issue #550) judges a check-run by its record alone and
    never learns which path produced it.  The guarantee is: when the witness
    reuses, its published green shape is field-for-field the shape the full
    run's green conclusion has; when it falls back, the shape is the
    ``missing`` one -- "not established, treat as not green" -- never green.
    """

    def _full_run_green_verdict(self):
        check = {
            "id": 120653396590,
            "name": queue_state.GATE_CHECK_NAME,
            "status": "completed",
            "conclusion": "success",
            "started_at": "2026-10-23T18:24:21Z",
            "completed_at": "2026-10-23T18:37:02Z",
        }
        job = dict(check)
        run = {"id": RUN_ID, "name": queue_state.GATE_WORKFLOW_NAME,
               "status": "completed", "conclusion": "success"}
        return queue_state.judge(check, [job], (), run)

    def test_a_reuse_verdict_is_shaped_like_a_full_run_green_verdict(self):
        green = self._full_run_green_verdict()
        decision = decided()
        for key in ("verdict", "queued", "running", "passed", "failed",
                    "missing", "observed_status", "observed_conclusion"):
            with self.subTest(key=key):
                self.assertEqual(decision[key], green[key])
        self.assertTrue(decision["reuse"])

    def test_a_fallback_is_shaped_like_an_unestablished_verdict(self):
        decision = decided(candidate=anchor_record(head_sha=OTHER_HEAD))
        assert_fallback(self, decision)
        self.assertFalse(decision["queued"])
        self.assertFalse(decision["running"])
        self.assertFalse(decision["failed"])

    def test_the_fallback_shape_holds_for_every_witness_failure(self):
        failures = [
            None,
            ["not", "a", "record"],
            {},
            anchor_record(head_sha=12345),
            anchor_record(conclusion="failure"),
            anchor_record(status="queued"),
            anchor_record(workflow="Other Workflow"),
        ]
        for candidate in failures:
            with self.subTest(candidate=candidate):
                decision = decided(candidate=candidate)
                assert_fallback(self, decision)


if __name__ == "__main__":
    unittest.main()
