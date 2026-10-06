"""Focused tests for the G6 security, observability, rollback and Beta gates."""
from __future__ import annotations

import json
import copy
from pathlib import Path
import tempfile
import unittest

from scripts.lingdoc_mock import (g6_evidence, g6_main_path, g6_metrics, g6_observability,
                                   g6_rollback, g6_run, g6_security)


class G6SecurityTest(unittest.TestCase):
    def test_unobserved_security_cases_are_blocked_and_redacted(self):
        report = g6_security.build_security_report()
        self.assertEqual(report["result"], "BLOCKED")
        self.assertTrue(all(case["result"] == "BLOCKED" for case in report["cases"]))
        self.assertNotIn("token-value", json.dumps(report))
        self.assertNotIn("body", report["quality_evidence"])

    def test_status_only_observations_can_pass_without_recording_body(self):
        observations = {}
        for case in g6_security.SECURITY_CASES:
            observation = {"actual_http": case["expected_http"],
                           "decision": case["expected_decision"]}
            if case["id"] == "SEC-12":
                observation["metadata_only"] = True
            observations[case["id"]] = observation
        report = g6_security.build_security_report(observations=observations)
        self.assertEqual(report["result"], "PASS")
        self.assertTrue(all(case["result"] == "PASS" for case in report["cases"]))
        self.assertNotIn("secret body", json.dumps(report))

    def test_restricted_content_in_any_security_observation_fails(self):
        observations = {
            case["id"]: {"actual_http": case["expected_http"],
                          "decision": case["expected_decision"],
                          **({"metadata_only": True} if case["id"] == "SEC-12" else {})}
            for case in g6_security.SECURITY_CASES
        }
        observations["SEC-01"]["body"] = "secret body"
        report = g6_security.build_security_report(observations=observations)
        self.assertEqual(report["result"], "FAIL")
        self.assertEqual(next(case for case in report["cases"] if case["id"] == "SEC-01")["result"], "FAIL")

    def test_unknown_security_case_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "unsupported cases"):
            g6_security.build_security_report(observations={"SEC-UNKNOWN": {"actual_http": 403}})

    def test_invalid_security_status_values_are_not_echoed(self):
        report = g6_security.build_security_report(observations={
            "SEC-01": {"actual_http": "secret-status", "decision": "secret-decision"},
        })
        case = next(case for case in report["cases"] if case["id"] == "SEC-01")
        self.assertIsNone(case["actual_http"])
        self.assertIsNone(case["observed_decision"])

    def test_metadata_query_with_body_is_a_security_failure(self):
        observations = {
            case["id"]: {"actual_http": case["expected_http"],
                          "decision": case["expected_decision"],
                          **({"metadata_only": True} if case["id"] == "SEC-12" else {})}
            for case in g6_security.SECURITY_CASES
        }
        observations["SEC-12"]["body"] = "restricted source"
        report = g6_security.build_security_report(observations=observations)
        self.assertEqual(report["result"], "FAIL")
        self.assertEqual(next(case for case in report["cases"] if case["id"] == "SEC-12")["result"], "FAIL")

    def test_nested_metadata_body_is_a_security_failure(self):
        observations = {
            case["id"]: {"actual_http": case["expected_http"],
                          "decision": case["expected_decision"],
                          **({"metadata_only": True} if case["id"] == "SEC-12" else {})}
            for case in g6_security.SECURITY_CASES
        }
        observations["SEC-12"]["response"] = {"body": "restricted source"}
        self.assertEqual(g6_security.build_security_report(observations=observations)["result"], "FAIL")

    def test_security_observation_requires_explicit_permission_decision(self):
        observations = {
            case["id"]: {"actual_http": case["expected_http"]}
            for case in g6_security.SECURITY_CASES
        }
        report = g6_security.build_security_report(observations=observations)
        self.assertEqual(report["result"], "BLOCKED")
        self.assertTrue(all(case["result"] == "BLOCKED" for case in report["cases"]))

    def test_audit_metadata_observation_requires_metadata_only_marker(self):
        observations = {
            case["id"]: {"actual_http": case["expected_http"],
                          "decision": case["expected_decision"]}
            for case in g6_security.SECURITY_CASES
        }
        report = g6_security.build_security_report(observations=observations)
        sec12 = next(case for case in report["cases"] if case["id"] == "SEC-12")
        self.assertEqual(sec12["result"], "BLOCKED")

    def test_security_fixture_id_must_be_redacted(self):
        with self.assertRaisesRegex(ValueError, "fixture_id"):
            g6_security.build_security_report(fixture_id="tenant/secret/project")


class G6EvidenceTest(unittest.TestCase):
    def test_shared_evidence_boundary_rejects_sensitive_metadata(self):
        with self.assertRaisesRegex(ValueError, "sensitive"):
            g6_evidence.build_evidence(
                fixture_id="G6:E1", runtime_mode="mock", result="BLOCKED",
                input_value={}, output_value={}, attribution=["runtime"],
                evidence_refs=[], owner="quality", reviewer="qa",
                dependency_versions={"token": "secret"})
        with self.assertRaisesRegex(ValueError, "credential-like"):
            g6_evidence.build_evidence(
                fixture_id="G6:E1", runtime_mode="mock", result="BLOCKED",
                input_value={}, output_value={}, attribution=["runtime"],
                evidence_refs=[], owner="quality", reviewer="qa",
                dependency_versions={"api": "SECRET_TOKEN"})


class G6ObservabilityTest(unittest.TestCase):
    def test_missing_chain_is_blocked(self):
        report = g6_observability.build_observability_report()
        self.assertEqual(report["result"], "BLOCKED")
        self.assertFalse(any(report["chain_coverage"].values()))

    def test_complete_redacted_chain_passes(self):
        events = [
            {"event_type": event_type, "status": "ok", "correlation_id": "corr-1",
             "runtime_mode": "real", "resource_type": event_type,
             **({"request_id": "request.id"} if event_type in {"request", "audit"} else {}),
             **({"task_id": "task.id"} if event_type in {"task", "validation"} else {}),
             **({"changeset_id": "changeset.id"} if event_type == "changeset" else {}),
             **({"snapshot_id": "snapshot.id"} if event_type == "snapshot" else {}),
             **({"export_id": "export.id"} if event_type in {"export", "download"} else {})}
            for event_type in g6_observability.EVENT_TYPES
        ]
        events[1].update({"status": "retry", "retry_count": 1, "stale": False})
        events[2]["permission_decision"] = "deny"
        events[6]["download_reauthorized"] = True
        events[6].update({"permission_decision": "deny", "permission_reason": "revoked"})
        events[4]["file_loss_class"] = "none"
        events[1].update({"provider": "weknora", "result_code": "ok"})
        events.extend([
            {"event_type": "request", "status": "denied", "correlation_id": "corr-1",
             "runtime_mode": "real", "request_id": "request.denied.id", "resource_type": "request"},
            {"event_type": "request", "status": "conflict", "correlation_id": "corr-1",
             "runtime_mode": "real", "request_id": "request.conflict.id", "resource_type": "request"},
        ])
        report = g6_observability.build_observability_report(events=events)
        self.assertEqual(report["result"], "PASS")
        self.assertEqual(report["correlation_ids"], ["corr-1"])
        self.assertTrue(report["signal_coverage"]["request_denied"])
        self.assertTrue(report["signal_coverage"]["request_conflict"])
        self.assertTrue(report["signal_coverage"]["revocation_intercept"])
        self.assertTrue(all(report["alert_coverage"].values()))
        self.assertEqual(report["uncovered_scope"], [])

    def test_sensitive_event_field_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "sensitive"):
            g6_observability.sanitize_event({"event_type": "request", "status": "ok",
                                              "correlation_id": "corr-1", "runtime_mode": "real",
                                              "request_id": "request.id",
                                              "body": "secret"})

    def test_unstructured_event_status_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "safe structured-log status"):
            g6_observability.sanitize_event({"event_type": "request", "status": "secret body",
                                              "correlation_id": "corr-1", "runtime_mode": "real",
                                              "request_id": "request.id"})

    def test_unredacted_resource_id_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "redacted"):
            g6_observability.sanitize_event({"event_type": "request", "status": "ok",
                                              "correlation_id": "corr-1", "runtime_mode": "real",
                                              "request_id": "request.id",
                                              "project_id": "project-123"})

    def test_each_event_requires_a_link_id(self):
        with self.assertRaisesRegex(ValueError, "link fields"):
            g6_observability.sanitize_event({"event_type": "request", "status": "ok",
                                              "correlation_id": "corr-1", "runtime_mode": "real"})

    def test_event_duration_must_be_non_negative(self):
        with self.assertRaisesRegex(ValueError, "duration_ms"):
            g6_observability.sanitize_event({"event_type": "request", "status": "ok",
                                              "correlation_id": "corr-1", "runtime_mode": "real",
                                              "request_id": "request.id", "duration_ms": "slow"})
        with self.assertRaisesRegex(ValueError, "duration_ms"):
            g6_observability.sanitize_event({"event_type": "request", "status": "ok",
                                              "correlation_id": "corr-1", "runtime_mode": "real",
                                              "request_id": "request.id", "duration_ms": float("nan")})
        with self.assertRaisesRegex(ValueError, "retry_count"):
            g6_observability.sanitize_event({"event_type": "task", "status": "retry",
                                              "correlation_id": "corr-1", "runtime_mode": "real",
                                              "task_id": "task.id", "retry_count": float("inf")})

    def test_event_signal_fields_have_status_only_shapes(self):
        event = {"event_type": "task", "status": "retry", "correlation_id": "corr-1",
                 "runtime_mode": "real", "task_id": "task.id"}
        for field in ("stale", "duplicate_side_effect", "download_reauthorized"):
            with self.subTest(field=field), self.assertRaisesRegex(ValueError, field):
                g6_observability.sanitize_event({**event, field: "yes"})
        with self.assertRaisesRegex(ValueError, "permission_decision"):
            g6_observability.sanitize_event({**event, "permission_decision": "secret"})

    def test_observability_accepts_success_and_error_status_aliases(self):
        events = [
            {"event_type": "request", "status": "success", "correlation_id": "corr-1",
             "runtime_mode": "real", "request_id": "request.id"},
            {"event_type": "task", "status": "error", "correlation_id": "corr-1",
             "runtime_mode": "real", "task_id": "task.id", "retry_count": 1},
        ]
        signals = g6_observability.build_observability_report(events=events)["signal_coverage"]
        self.assertTrue(signals["request_success"])
        self.assertTrue(signals["async_failure_retry"])


class G6RollbackTest(unittest.TestCase):
    def test_default_rehearsal_is_blocked_until_observed(self):
        report = g6_rollback.build_rollback_report()
        self.assertEqual(report["result"], "BLOCKED")
        self.assertIn("revision_pointer", report["plan"]["preserve"])

    def test_complete_status_only_rehearsal_passes(self):
        observations = {
            "drill_id": "rollback.id", "severity": "P1", "impact_scope": ["tenant.id"],
            "duration_ms": 1200, "recovery_verified": True, "uncovered_risks": ["wps-reopen"],
            "checks": {check_id: True for check_id, _ in g6_rollback.CHECKS},
        }
        self.assertEqual(g6_rollback.build_rollback_report(observations=observations)["result"], "PASS")

    def test_unknown_runtime_mode_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "runtime_mode"):
            g6_rollback.build_rollback_report(runtime_mode="arbitrary")

    def test_rehearsal_requires_valid_p0_or_p1_metadata(self):
        observations = {
            "drill_id": "rollback.id", "severity": "P3", "impact_scope": ["tenant.id"],
            "duration_ms": "unknown", "recovery_verified": False, "uncovered_risks": [],
            "checks": {check_id: True for check_id, _ in g6_rollback.CHECKS},
        }
        self.assertEqual(g6_rollback.build_rollback_report(observations=observations)["result"], "BLOCKED")

    def test_rehearsal_risk_labels_are_codes_and_plan_exposes_compatibility_edges(self):
        observations = {
            "drill_id": "rollback.id", "severity": "P1", "impact_scope": ["tenant.id"],
            "duration_ms": 1200, "recovery_verified": True, "uncovered_risks": ["wps-reopen"],
            "checks": {check_id: True for check_id, _ in g6_rollback.CHECKS},
        }
        report = g6_rollback.build_rollback_report(observations=observations)
        self.assertEqual(report["result"], "PASS")
        self.assertEqual(report["plan"]["migration_compatibility"]["unknown_default"], "UNKNOWN")
        observations["uncovered_risks"] = ["secret document text"]
        self.assertEqual(g6_rollback.build_rollback_report(observations=observations)["result"], "BLOCKED")

    def test_rehearsal_rejects_non_finite_duration_and_redacts_invalid_metadata(self):
        observations = {
            "drill_id": "rollback.id", "severity": "P1", "impact_scope": ["tenant.id"],
            "duration_ms": float("nan"), "recovery_verified": True,
            "uncovered_risks": ["secret document text"],
            "checks": {check_id: True for check_id, _ in g6_rollback.CHECKS},
        }
        report = g6_rollback.build_rollback_report(observations=observations)
        self.assertEqual(report["result"], "BLOCKED")
        self.assertIsNone(report["drill"]["duration_ms"])
        self.assertEqual(report["drill"]["uncovered_risks"], [])

    def test_invalid_rollback_check_values_are_not_echoed(self):
        report = g6_rollback.build_rollback_report(observations={
            "checks": {"old_objects_readable": "secret-check"},
        })
        check = next(check for check in report["checks"] if check["id"] == "old_objects_readable")
        self.assertIsNone(check["observed"])


class G6MetricsTest(unittest.TestCase):
    WINDOW = {
        "window_id": "window.id", "target_samples": 2, "minimum_reportable_samples": 2,
        "template_version": "template.v1", "ruleset_hash": "rules.key", "redaction": "fixture-v1",
        "included_projects": ["project.id", "project.key"],
        "included_users": ["user.id"], "runtime_modes": ["real"],
        "rollback_result": "PASS", "uncovered_risks": ["wps-reopen"],
        "process_evidence": {"authorization": "observed", "main_path": "observed",
                              "failure_path": "observed", "manual_review": "observed",
                              "repair_or_rollback": "observed", "key_scenario_rerun": "observed"},
    }

    def test_non_real_samples_do_not_enter_effect_rates(self):
        report = g6_metrics.build_beta_report(samples=[{
            "sample_id": "sample.mock", "sample_version": "sample.v1",
            "runtime_mode": "mock", "quality_result": "PASS",
            "supported_claims": 10, "total_claims": 10,
        }], window=self.WINDOW)
        self.assertEqual(report["result"], "BLOCKED")
        self.assertEqual(report["readiness"], "NOT READY")
        self.assertEqual(report["raw_counts"]["excluded_non_real"], 1)
        self.assertIsNone(report["metrics"]["source_support_rate"]["rate"])
        self.assertIsNone(report["metrics"]["manual_effort_minutes"]["rate"])

    def test_real_samples_with_complete_denominators_can_expand(self):
        samples = [
            {"sample_id": "sample.real1", "sample_version": "sample.v1",
             "runtime_mode": "real", "quality_result": "PASS", "dependency_status": "verified",
             "supported_claims": 9, "total_claims": 10, "dismissed_issues": 1, "issues_reviewed": 5,
             "unconfirmed_items": 1, "snapshots": 2, "export_loss_items": 0, "export_checks": 4,
             "manual_minutes": 20, "projects": 1, "missed_edits": 1, "changesets": 2},
            {"sample_id": "sample.real2", "sample_version": "sample.v1",
             "runtime_mode": "real", "quality_result": "PASS", "dependency_status": "verified",
             "supported_claims": 8, "total_claims": 10, "dismissed_issues": 0, "issues_reviewed": 5,
             "unconfirmed_items": 0, "snapshots": 2, "export_loss_items": 0, "export_checks": 4,
             "manual_minutes": 15, "projects": 1, "missed_edits": 0, "changesets": 2},
        ]
        report = g6_metrics.build_beta_report(samples=samples, window=self.WINDOW)
        self.assertEqual(report["result"], "PASS")
        self.assertEqual(report["decision"], "expand")
        self.assertEqual(report["metrics"]["source_support_rate"]["numerator"], 17)

    def test_unverified_real_dependency_blocks_expand_and_clears_rates(self):
        sample = {"sample_id": "sample.unverified", "sample_version": "sample.v1",
                  "runtime_mode": "real", "quality_result": "PASS", "dependency_status": "not_verified",
                  "supported_claims": 9, "total_claims": 10, "dismissed_issues": 0, "issues_reviewed": 1,
                  "unconfirmed_items": 0, "snapshots": 1, "export_loss_items": 0, "export_checks": 1,
                  "manual_minutes": 10, "projects": 1, "missed_edits": 0, "changesets": 1}
        report = g6_metrics.build_beta_report(
            samples=[sample], window={**self.WINDOW, "minimum_reportable_samples": 1})

        self.assertEqual(report["result"], "BLOCKED")
        self.assertEqual(report["decision"], "pause")
        self.assertEqual(report["raw_counts"]["dependency_blocked"], 1)
        self.assertEqual(report["raw_counts"]["quality_pass_real"], 1)
        self.assertEqual(report["raw_counts"]["eligible_real"], 0)
        self.assertTrue(all(values["rate"] is None for values in report["metrics"].values()))

    def test_incomplete_trial_process_keeps_effect_rates_unready(self):
        sample = {"sample_id": "sample.real1", "sample_version": "sample.v1",
                  "runtime_mode": "real", "quality_result": "PASS",
                  "dependency_status": "verified",
                  "supported_claims": 1, "total_claims": 1, "dismissed_issues": 0,
                  "issues_reviewed": 1, "unconfirmed_items": 0, "snapshots": 1,
                  "export_loss_items": 0, "export_checks": 1, "manual_minutes": 10,
                  "projects": 1, "missed_edits": 0, "changesets": 1}
        process = {**self.WINDOW["process_evidence"], "manual_review": "not_run"}
        report = g6_metrics.build_beta_report(samples=[sample], window={**self.WINDOW,
                                                                        "minimum_reportable_samples": 1,
                                                                        "process_evidence": process})
        self.assertEqual(report["result"], "BLOCKED")
        self.assertEqual(report["readiness"], "NOT READY")
        self.assertIsNone(report["metrics"]["source_support_rate"]["rate"])

    def test_p0_blocks_even_with_enough_samples(self):
        samples = [{"sample_id": "sample.real1", "sample_version": "sample.v1",
                    "runtime_mode": "real", "quality_result": "PASS",
                    "severity": "P0", "supported_claims": 1, "total_claims": 1,
                    "dismissed_issues": 0, "issues_reviewed": 1, "unconfirmed_items": 0,
                    "snapshots": 1, "export_loss_items": 0, "export_checks": 1,
                    "manual_minutes": 10, "projects": 1, "missed_edits": 0, "changesets": 1}]
        report = g6_metrics.build_beta_report(samples=samples,
                                               window={**self.WINDOW, "minimum_reportable_samples": 1})
        self.assertEqual(report["result"], "BLOCKED")
        self.assertEqual(report["decision"], "pause")

    def test_p0_or_dependency_block_takes_pause_precedence_over_incomplete_process(self):
        sample = {"sample_id": "sample.real1", "sample_version": "sample.v1",
                  "runtime_mode": "real", "quality_result": "PASS",
                  "severity": "P1", "dependency_status": "not_verified",
                  "supported_claims": 1, "total_claims": 1, "dismissed_issues": 0,
                  "issues_reviewed": 1, "unconfirmed_items": 0, "snapshots": 1,
                  "export_loss_items": 0, "export_checks": 1, "manual_minutes": 10,
                  "projects": 1, "missed_edits": 0, "changesets": 1}
        process = {**self.WINDOW["process_evidence"], "manual_review": "not_run"}
        report = g6_metrics.build_beta_report(
            samples=[sample], window={**self.WINDOW, "minimum_reportable_samples": 1,
                                      "process_evidence": process})
        self.assertEqual(report["result"], "BLOCKED")
        self.assertEqual(report["decision"], "pause")
        self.assertEqual(report["readiness"], "BLOCKED")

    def test_blocked_real_dependency_prevents_not_ready_from_looking_harmless(self):
        sample = {"sample_id": "sample.blocked", "sample_version": "sample.v1",
                  "runtime_mode": "real", "quality_result": "BLOCKED"}
        report = g6_metrics.build_beta_report(samples=[sample], window=self.WINDOW)
        self.assertEqual(report["result"], "BLOCKED")
        self.assertEqual(report["decision"], "pause")

    def test_sensitive_window_field_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "sensitive"):
            g6_metrics.build_beta_report(samples=[], window={**self.WINDOW, "body": "secret"})

    def test_invalid_sample_counts_are_rejected(self):
        sample = {"sample_id": "sample.real1", "sample_version": "sample.v1",
                  "runtime_mode": "real", "quality_result": "PASS",
                  "supported_claims": 1.5, "total_claims": 2}
        with self.assertRaisesRegex(ValueError, "non-negative integer"):
            g6_metrics.build_beta_report(samples=[sample], window=self.WINDOW)

    def test_non_finite_sample_values_are_rejected(self):
        sample = {"sample_id": "sample.real1", "sample_version": "sample.v1",
                  "runtime_mode": "real", "quality_result": "PASS",
                  "manual_minutes": float("inf"), "projects": 1}
        with self.assertRaisesRegex(ValueError, "non-negative number"):
            g6_metrics.build_beta_report(samples=[sample], window=self.WINDOW)

    def test_beta_window_requires_process_evidence_and_redacted_risks(self):
        incomplete = {key: value for key, value in self.WINDOW.items() if key != "process_evidence"}
        with self.assertRaisesRegex(ValueError, "process_evidence"):
            g6_metrics.build_beta_report(samples=[], window=incomplete)
        with self.assertRaisesRegex(ValueError, "uncovered_risks"):
            g6_metrics.build_beta_report(samples=[], window={**self.WINDOW,
                                                              "uncovered_risks": ["secret document"]})
        with self.assertRaisesRegex(ValueError, "redaction"):
            g6_metrics.build_beta_report(samples=[], window={**self.WINDOW,
                                                              "redaction": "raw document text"})

    def test_missing_sample_values_are_reported_and_block_effect_rates(self):
        sample = {"sample_id": "sample.real1", "sample_version": "sample.v1",
                  "runtime_mode": "real", "quality_result": "PASS", "dependency_status": "verified",
                  "supported_claims": 1, "total_claims": 2}
        report = g6_metrics.build_beta_report(samples=[sample], window={**self.WINDOW,
                                                                          "minimum_reportable_samples": 1})
        self.assertEqual(report["result"], "BLOCKED")
        self.assertEqual(report["readiness"], "NOT READY")
        self.assertEqual(report["sample_versions"][0]["sample_version"], "sample.v1")
        self.assertIn("manual_minutes", report["missing_values"][0]["missing_fields"])
        self.assertEqual(report["metrics"]["source_support_rate"]["rate"], 0.5)
        self.assertIsNone(report["metrics"]["manual_effort_minutes"]["rate"])


class G6MainPathTest(unittest.TestCase):
    def _complete_report(self):
        workflow = json.loads(g6_main_path.WORKFLOW_PATH.read_text(encoding="utf-8"))
        steps = [{"id": step["id"], "operation_id": step["operation_id"],
                  "verdict": "passed", "actual_http": step["expected_http"]}
                 for step in workflow["steps"]]
        return {"runner_status": "completed", "completed_steps": len(steps),
                "workflow": "F01",
                "total_steps": len(steps), "verification_scope": "http_smoke_only",
                "steps": steps}

    def test_complete_path_stays_blocked_until_failure_side_effect_is_read_back(self):
        pending = g6_main_path.build_main_path_report(main_report=self._complete_report())
        self.assertEqual(pending["fixture_coverage"]["constructed_complete"], True)
        self.assertEqual(pending["result"], "BLOCKED")
        verified = g6_main_path.build_main_path_report(
            main_report=self._complete_report(),
            failure_observation={"failure_case": "duplicate_formal_write", "expected_http": 409,
                                 "actual_http": 409, "no_formal_side_effect": True,
                                 "readback": {"status": "unchanged"}},
        )
        self.assertEqual(verified["result"], "PASS")

    def test_mock_fixture_is_deterministic_and_redacted(self):
        first = g6_main_path.build_mock_main_path_fixture()
        second = g6_main_path.build_mock_main_path_fixture()
        self.assertEqual(first, second)
        report = g6_main_path.build_main_path_report(
            main_report=first[0], failure_observation=first[1])
        repeat = g6_main_path.build_main_path_report(
            main_report=second[0], failure_observation=second[1])
        self.assertEqual(json.dumps(report, ensure_ascii=False, sort_keys=True),
                         json.dumps(repeat, ensure_ascii=False, sort_keys=True))
        self.assertEqual(report["result"], "PASS")
        evidence = report["quality_evidence"]
        self.assertEqual(evidence["template_version"], "template.v1")
        self.assertEqual(evidence["object_versions"]["project"], "project.v1")
        self.assertEqual(evidence["permission_snapshot"]["status"], "mock")
        self.assertEqual(sorted(report["fixture_state"]),
                         ["asset", "chapter", "check", "download", "export", "project", "release"])
        self.assertNotIn("duplicate_formal_write", json.dumps(report))

    def test_runner_shaped_http_status_steps_are_accepted(self):
        main, failure = g6_main_path.build_mock_main_path_fixture()
        runner_shaped = copy.deepcopy(main)
        runner_shaped["steps"] = [
            {"id": step["step_id"], "operation_id": step["operation_id"],
             "http_status": step["actual_http"]}
            for step in runner_shaped["steps"]
        ]
        runner_shaped["verification_scope"] = "http_smoke_only"
        report = g6_main_path.build_main_path_report(
            main_report=runner_shaped, failure_observation=failure)
        self.assertEqual(report["main_path"]["result"], "PASS")
        self.assertEqual(report["result"], "PASS")

    def test_fixture_digest_changes_when_redacted_fixture_state_changes(self):
        main, failure = g6_main_path.build_mock_main_path_fixture()
        first = g6_main_path.build_main_path_report(
            main_report=copy.deepcopy(main), failure_observation=copy.deepcopy(failure))
        changed = copy.deepcopy(main)
        changed["fixture_state"]["project"]["version"] = "project.v2"
        second = g6_main_path.build_main_path_report(
            main_report=changed, failure_observation=copy.deepcopy(failure))
        self.assertNotEqual(first["fixture_digest"], second["fixture_digest"])
        self.assertNotEqual(first["quality_evidence"]["fixture_digest"],
                            second["quality_evidence"]["fixture_digest"])

    def test_incomplete_f01_step_sequence_is_blocked(self):
        main, failure = g6_main_path.build_mock_main_path_fixture()
        main["steps"] = main["steps"][1:]
        main["completed_steps"] -= 1
        report = g6_main_path.build_main_path_report(
            main_report=main, failure_observation=failure)
        self.assertEqual(report["result"], "BLOCKED")
        self.assertFalse(report["fixture_coverage"]["step_sequence_complete"])
        self.assertIn("F01-01", report["fixture_coverage"]["missing_step_ids"])

    def test_canonical_step_status_cannot_be_overridden_by_verdict(self):
        main, failure = g6_main_path.build_mock_main_path_fixture()
        main["steps"][0]["actual_http"] = 500
        report = g6_main_path.build_main_path_report(
            main_report=main, failure_observation=failure)
        self.assertEqual(report["main_path"]["result"], "FAIL")
        self.assertEqual(report["result"], "FAIL")

    def test_canonical_step_without_http_status_is_blocked(self):
        main, failure = g6_main_path.build_mock_main_path_fixture()
        main["steps"][0].pop("actual_http")
        report = g6_main_path.build_main_path_report(
            main_report=main, failure_observation=failure)
        self.assertEqual(report["main_path"]["result"], "BLOCKED")

    def test_main_path_evidence_drops_untrusted_report_payloads(self):
        main = self._complete_report()
        main["steps"][0]["response"] = "SECRET_DOCUMENT"
        main["verification_scope"] = "SECRET_SCOPE"
        main["workflow"] = "SECRET_WORKFLOW"
        report = g6_main_path.build_main_path_report(
            main_report={**main, "workflow": "F01"},
            failure_observation={"failure_case": "duplicate_formal_write", "expected_http": 409,
                                 "actual_http": 409, "no_formal_side_effect": True,
                                 "readback": {"status": "unchanged"}})
        self.assertNotIn("SECRET_DOCUMENT", json.dumps(report))
        self.assertNotIn("SECRET_SCOPE", json.dumps(report))
        self.assertNotIn("SECRET_WORKFLOW", json.dumps(report))

    def test_main_path_metadata_is_redacted_before_evidence(self):
        main, failure = g6_main_path.build_mock_main_path_fixture()
        main["fixture_metadata"]["permission_snapshot"]["project"] = "tenant/secret-project"
        main["fixture_metadata"]["dependency_versions"]["api"] = "Bearer SECRET"
        report = g6_main_path.build_main_path_report(main_report=main, failure_observation=failure)
        encoded = json.dumps(report)
        self.assertNotIn("tenant/secret-project", encoded)
        self.assertNotIn("Bearer SECRET", encoded)
        self.assertEqual(report["quality_evidence"]["permission_snapshot"]["status"], "mock")

    def test_failure_readback_rejects_unredacted_status_values(self):
        report = g6_main_path.build_main_path_report(
            main_report=self._complete_report(),
            failure_observation={"failure_case": "duplicate_formal_write", "expected_http": 409,
                                 "actual_http": 409, "no_formal_side_effect": True,
                                 "readback": {"status": "secret body", "revision": "real-revision"}})
        self.assertEqual(report["result"], "FAIL")
        self.assertEqual(report["failure_path"]["readback"], {})
        self.assertNotIn("secret body", json.dumps(report))

    def test_failure_readback_requires_explicit_unchanged_status(self):
        for readback in ("secret body", "observed", ["unchanged"], {"unchanged": True}):
            with self.subTest(readback=readback):
                report = g6_main_path.build_main_path_report(
                    main_report=self._complete_report(),
                    failure_observation={"failure_case": "duplicate_formal_write",
                                         "expected_http": 409, "actual_http": 409,
                                         "no_formal_side_effect": True,
                                         "readback": readback})
                self.assertEqual(report["result"], "FAIL")

    def test_failure_readback_accepts_status_and_state_unchanged_shapes(self):
        for readback in ("unchanged", {"status": "unchanged"}, {"state": "unchanged"}):
            with self.subTest(readback=readback):
                report = g6_main_path.build_main_path_report(
                    main_report=self._complete_report(),
                    failure_observation={"failure_case": "duplicate_formal_write",
                                         "expected_http": 409, "actual_http": 409,
                                         "no_formal_side_effect": True,
                                         "readback": readback})
                self.assertEqual(report["result"], "PASS")

    def test_incomplete_failure_observation_redacts_non_boolean_side_effect(self):
        report = g6_main_path.build_main_path_report(
            main_report=self._complete_report(),
            failure_observation={"failure_case": "duplicate_formal_write",
                                 "expected_http": 409, "actual_http": 409,
                                 "no_formal_side_effect": "secret body"})
        self.assertIsNone(report["failure_path"]["no_formal_side_effect"])
        self.assertNotIn("secret body", json.dumps(report))

    def test_missing_export_part_cannot_claim_main_path_pass(self):
        report = self._complete_report()
        report["steps"] = [step for step in report["steps"] if step["operation_id"] != "downloadExport"]
        report["completed_steps"] -= 1
        result = g6_main_path.build_main_path_report(main_report=report,
                                                     failure_observation={"no_formal_side_effect": True})
        self.assertEqual(result["result"], "BLOCKED")
        self.assertIn("download", result["fixture_coverage"]["missing_parts"])

    def test_main_path_rejects_non_object_failure_and_boolean_step_numbers(self):
        with self.assertRaisesRegex(ValueError, "failure observation must be an object"):
            g6_main_path.build_main_path_report(main_report=self._complete_report(), failure_observation=[])
        report = self._complete_report()
        report["completed_steps"] = True
        report["steps"][0]["actual_http"] = True
        result = g6_main_path.build_main_path_report(
            main_report=report,
            failure_observation={"failure_case": "duplicate_formal_write", "expected_http": 409,
                                 "actual_http": 409, "no_formal_side_effect": True,
                                 "readback": {"status": "unchanged"}},
        )
        self.assertEqual(result["main_path"]["completed_steps"], 0)
        self.assertNotIn("actual_http", result["main_path"])


class G6RunTest(unittest.TestCase):
    def test_f01_reports_can_be_assembled_into_a_runtime_matrix(self):
        main, failure = g6_main_path.build_mock_main_path_fixture()
        reports = {}
        for mode in g6_run.RUNTIME_MODES:
            report = g6_main_path.build_main_path_report(
                main_report=copy.deepcopy(main), failure_observation=copy.deepcopy(failure),
                runtime_mode=mode, fixture_id="F01:S1")
            if mode != "mock":
                report["main_path"]["verification_scope"] = "observed"
            reports[mode] = report
        dependencies = {
            mode: {"required": list(g6_run.RUNTIME_DEPENDENCIES[mode]),
                   "verified": list(g6_run.RUNTIME_DEPENDENCIES[mode]),
                   "missing": [], "status": "verified"}
            for mode in g6_run.RUNTIME_MODES
        }
        matrix = g6_run.build_runtime_matrix_from_main_path_reports(
            reports, dependency_matrix=dependencies,
            provider_semantics={mode: "verified" for mode in g6_run.RUNTIME_MODES})
        self.assertEqual(matrix["fixture_ids"], ["F01:S1"])
        self.assertTrue(matrix["shared_fixture"])
        report = g6_run.build_g6_report(runtime_matrix_report=matrix)
        self.assertEqual(report["gate_results"]["G6-02"], "PASS")

    def test_f01_report_assembly_cannot_pass_without_failure_side_effect_evidence(self):
        main, failure = g6_main_path.build_mock_main_path_fixture()
        reports = {}
        for mode in g6_run.RUNTIME_MODES:
            report = g6_main_path.build_main_path_report(
                main_report=copy.deepcopy(main), failure_observation=copy.deepcopy(failure),
                runtime_mode=mode, fixture_id="F01:S1")
            report["failure_path"]["no_formal_side_effect"] = False
            reports[mode] = report

        matrix = g6_run.build_runtime_matrix_from_main_path_reports(reports)

        self.assertEqual(matrix["result"], "BLOCKED")
        self.assertTrue(all(mode["result"] == "BLOCKED" for mode in matrix["modes"]))

    def test_f01_report_assembly_cannot_pass_without_dependency_and_provider_evidence(self):
        main, failure = g6_main_path.build_mock_main_path_fixture()
        reports = {}
        for mode in g6_run.RUNTIME_MODES:
            report = g6_main_path.build_main_path_report(
                main_report=copy.deepcopy(main), failure_observation=copy.deepcopy(failure),
                runtime_mode=mode, fixture_id="F01:S1")
            if mode != "mock":
                report["main_path"]["verification_scope"] = "observed"
            reports[mode] = report

        matrix = g6_run.build_runtime_matrix_from_main_path_reports(
            reports, provider_semantics={mode: "verified" for mode in g6_run.RUNTIME_MODES})

        self.assertEqual(matrix["result"], "BLOCKED")
        self.assertTrue(all(mode["result"] == "BLOCKED" for mode in matrix["modes"]))

    def test_f01_report_conversion_accepts_state_shaped_readback(self):
        main, failure = g6_main_path.build_mock_main_path_fixture()
        failure = copy.deepcopy(failure)
        failure["readback"] = {"state": "unchanged"}
        report = g6_main_path.build_main_path_report(
            main_report=main, failure_observation=failure,
            runtime_mode="mock", fixture_id="F01:S1")

        evidence = g6_run.main_path_report_to_scenario_evidence(report)

        self.assertEqual(evidence["key_failure"]["readback_status"], "unchanged")

    def test_cli_assembles_main_path_report_files_into_g6_02(self):
        main, failure = g6_main_path.build_mock_main_path_fixture()
        reports = {}
        for mode in g6_run.RUNTIME_MODES:
            report = g6_main_path.build_main_path_report(
                main_report=copy.deepcopy(main), failure_observation=copy.deepcopy(failure),
                runtime_mode=mode, fixture_id="F01:S1")
            if mode != "mock":
                report["main_path"]["verification_scope"] = "observed"
            reports[mode] = report
        dependencies = {
            mode: {"required": list(g6_run.RUNTIME_DEPENDENCIES[mode]),
                   "verified": list(g6_run.RUNTIME_DEPENDENCIES[mode]),
                   "missing": [], "status": "verified"}
            for mode in g6_run.RUNTIME_MODES
        }
        semantics = {mode: "verified" for mode in g6_run.RUNTIME_MODES}
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            reports_path = root / "reports.json"
            dependencies_path = root / "dependencies.json"
            semantics_path = root / "semantics.json"
            output_path = root / "g6.json"
            reports_path.write_text(json.dumps(reports), encoding="utf-8")
            dependencies_path.write_text(json.dumps(dependencies), encoding="utf-8")
            semantics_path.write_text(json.dumps(semantics), encoding="utf-8")

            status = g6_run.main([
                "--output", str(output_path),
                "--main-path-reports", str(reports_path),
                "--runtime-dependencies", str(dependencies_path),
                "--provider-semantics", str(semantics_path),
            ])

            self.assertEqual(status, 1, "other G6 gates remain blocked without live evidence")
            output = json.loads(output_path.read_text(encoding="utf-8"))
            self.assertEqual(output["gate_results"]["G6-02"], "PASS")

    def test_default_run_is_explicitly_blocked_and_contains_all_gates(self):
        report = g6_run.build_g6_report()
        self.assertEqual(report["result"], "BLOCKED")
        self.assertEqual(set(report["gates"]), {"G6-01", "G6-02", "G6-03", "G6-04", "G6-05", "G6-06"})
        self.assertNotIn("secret-token", json.dumps(report))

    def test_runtime_matrix_is_a_first_class_gate(self):
        matrix = {"runtime_modes": ["mock", "real_api_fake_model", "real"],
                  "result": "BLOCKED", "shared_fixture": True,
                  "fixture_ids": ["G6-F01-v1"], "dependency_matrix": {}}
        report = g6_run.build_g6_report(runtime_matrix_report=matrix)
        self.assertEqual(report["gate_results"]["G6-02"], "BLOCKED")
        self.assertIn("gates.G6-02", report["quality_evidence"]["evidence_refs"])

    def test_valid_runtime_matrix_can_pass_only_with_mode_and_dependency_evidence(self):
        dependency_matrix = {
            "mock": {"required": ["contract"], "verified": ["contract"], "missing": [], "status": "verified"},
            "real_api_fake_model": {"required": ["api", "permissions", "queue", "file"],
                                     "verified": ["api", "permissions", "queue", "file"],
                                     "missing": [], "status": "verified"},
            "real": {"required": ["api", "permissions", "queue", "file", "model", "weknora", "docx"],
                      "verified": ["api", "permissions", "queue", "file", "model", "weknora", "docx"],
                      "missing": [], "status": "verified"},
        }
        matrix = {"runtime_modes": ["mock", "real_api_fake_model", "real"], "result": "PASS",
                  "shared_fixture": True, "fixture_ids": ["F01:S1"],
                  "dependency_matrix": dependency_matrix,
                  "modes": [{"runtime_mode": mode, "result": "PASS",
                             "dependency_status": "verified",
                             "provider_semantics_status": "verified",
                             "dependency_versions": {"api": "api.v1"},
                             "environment_versions": {"runtime": "python.v1"},
                             "verification_scope": "contract_fixture_only" if mode == "mock" else "observed",
                             "scenario_evidence": {
                                 "main_path": {"result": "PASS", "fixture_id": "F01:S1",
                                               "completed_steps": 23,
                                               "verification_scope": "contract_fixture_only" if mode == "mock" else "observed"},
                                 "key_failure": {"result": "PASS", "fixture_id": "F01:S1",
                                                 "actual_http": 409, "no_formal_side_effect": True,
                                                 "readback_status": "unchanged"},
                             }}
                            for mode in ("mock", "real_api_fake_model", "real")]}
        report = g6_run.build_g6_report(runtime_matrix_report=matrix)
        self.assertEqual(report["gate_results"]["G6-02"], "PASS")
        self.assertNotIn("SECRET_TOKEN", json.dumps(report))

    def test_runtime_matrix_preserves_redacted_version_evidence(self):
        dependency_matrix = {
            mode: {"required": list(g6_run.RUNTIME_DEPENDENCIES[mode]),
                   "verified": list(g6_run.RUNTIME_DEPENDENCIES[mode]), "missing": [],
                   "status": "verified"}
            for mode in g6_run.RUNTIME_MODES
        }
        matrix = {"runtime_modes": list(g6_run.RUNTIME_MODES), "result": "PASS",
                  "shared_fixture": True, "fixture_ids": ["F01:S1"],
                  "dependency_matrix": dependency_matrix,
                  "modes": [{"runtime_mode": mode, "result": "PASS",
                             "dependency_status": "verified", "provider_semantics_status": "verified",
                             "dependency_versions": {"api": "api.v1"},
                             "environment_versions": {"runtime": "python.v1"},
                             "verification_scope": "contract_fixture_only" if mode == "mock" else "observed",
                             "scenario_evidence": {
                                 "main_path": {"result": "PASS", "fixture_id": "F01:S1",
                                               "completed_steps": 23,
                                               "verification_scope": "contract_fixture_only" if mode == "mock" else "observed"},
                                 "key_failure": {"result": "PASS", "fixture_id": "F01:S1",
                                                 "actual_http": 409, "no_formal_side_effect": True,
                                                 "readback_status": "unchanged"},
                             }} for mode in g6_run.RUNTIME_MODES]}
        report = g6_run.build_g6_report(runtime_matrix_report=matrix)
        self.assertEqual(report["gate_results"]["G6-02"], "PASS")
        self.assertEqual(report["gates"]["G6-02"]["modes"][0]["dependency_versions"], {"api": "api.v1"})
        self.assertEqual(report["gates"]["G6-02"]["modes"][0]["environment_versions"], {"runtime": "python.v1"})

    def test_runtime_matrix_blocks_real_modes_without_version_evidence(self):
        dependency_matrix = {
            mode: {"required": list(g6_run.RUNTIME_DEPENDENCIES[mode]),
                   "verified": list(g6_run.RUNTIME_DEPENDENCIES[mode]), "missing": [],
                   "status": "verified"}
            for mode in g6_run.RUNTIME_MODES
        }
        matrix = {"runtime_modes": list(g6_run.RUNTIME_MODES), "result": "PASS",
                  "shared_fixture": True, "fixture_ids": ["F01:S1"],
                  "dependency_matrix": dependency_matrix,
                  "modes": [{"runtime_mode": mode, "result": "PASS",
                             "dependency_status": "verified", "provider_semantics_status": "verified",
                             "verification_scope": "contract_fixture_only" if mode == "mock" else "observed",
                             "scenario_evidence": {
                                 "main_path": {"result": "PASS", "fixture_id": "F01:S1",
                                               "completed_steps": 23,
                                               "verification_scope": "contract_fixture_only" if mode == "mock" else "observed"},
                                 "key_failure": {"result": "PASS", "fixture_id": "F01:S1",
                                                 "actual_http": 409, "no_formal_side_effect": True,
                                                 "readback_status": "unchanged"},
                             }} for mode in g6_run.RUNTIME_MODES]}
        report = g6_run.build_g6_report(runtime_matrix_report=matrix)
        self.assertEqual(report["gate_results"]["G6-02"], "BLOCKED")
        self.assertEqual(report["gates"]["G6-02"]["modes"][0]["result"], "PASS")
        self.assertTrue(all(mode["result"] == "BLOCKED"
                            for mode in report["gates"]["G6-02"]["modes"][1:]))

    def test_runtime_version_values_reject_credential_like_labels(self):
        safe, valid = g6_run._safe_version_map({"api": "SECRET_TOKEN"})
        self.assertFalse(valid)
        self.assertEqual(safe, {"status": "not_observed"})

    def test_runtime_matrix_requires_main_and_failure_evidence_per_mode(self):
        dependency_matrix = {
            mode: {"required": list(g6_run.RUNTIME_DEPENDENCIES[mode]),
                   "verified": list(g6_run.RUNTIME_DEPENDENCIES[mode]), "missing": [], "status": "verified"}
            for mode in g6_run.RUNTIME_MODES
        }
        matrix = {"runtime_modes": list(g6_run.RUNTIME_MODES), "result": "PASS",
                  "shared_fixture": True, "fixture_ids": ["F01:S1"],
                  "dependency_matrix": dependency_matrix,
                  "modes": [{"runtime_mode": mode, "result": "PASS",
                             "dependency_status": "verified", "provider_semantics_status": "verified",
                             "verification_scope": "observed"}
                            for mode in g6_run.RUNTIME_MODES]}
        report = g6_run.build_g6_report(runtime_matrix_report=matrix)
        self.assertEqual(report["gate_results"]["G6-02"], "BLOCKED")
        self.assertTrue(all(mode["scenario_evidence"]["main_path"]["result"] == "BLOCKED"
                            for mode in report["gates"]["G6-02"]["modes"]))

    def test_runtime_matrix_requires_all_three_modes_and_redaction(self):
        incomplete = {"runtime_modes": ["mock"], "result": "PASS"}
        report = g6_run.build_g6_report(runtime_matrix_report=incomplete)
        self.assertEqual(report["gate_results"]["G6-02"], "BLOCKED")
        with self.assertRaisesRegex(ValueError, "sensitive"):
            g6_run.build_g6_report(runtime_matrix_report={
                "runtime_modes": ["mock", "real_api_fake_model", "real"],
                "result": "BLOCKED", "body": "secret"})

        report = g6_run.build_g6_report(runtime_matrix_report={
            "runtime_modes": ["mock", "real_api_fake_model", "real"],
            "result": "BLOCKED", "reports": [{"response": "SECRET_TOKEN"}]})
        self.assertNotIn("SECRET_TOKEN", json.dumps(report))

    def test_failures_take_precedence_over_blocked_gates(self):
        observations = {
            case["id"]: {"actual_http": 500, "decision": "deny"}
            for case in g6_security.SECURITY_CASES
        }
        report = g6_run.build_g6_report(security_observations=observations)
        self.assertEqual(report["gate_results"]["G6-03"], "FAIL")
        self.assertEqual(report["result"], "FAIL")

if __name__ == "__main__":
    unittest.main()
