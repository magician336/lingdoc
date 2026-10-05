"""Focused tests for the G6 security, observability, rollback and Beta gates."""
from __future__ import annotations

import json
import unittest

from scripts.lingdoc_mock import g6_main_path, g6_metrics, g6_observability, g6_rollback, g6_run, g6_security


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
            observations[case["id"]] = observation
        report = g6_security.build_security_report(observations=observations)
        self.assertEqual(report["result"], "PASS")
        self.assertTrue(all(case["result"] == "PASS" for case in report["cases"]))
        self.assertNotIn("secret body", json.dumps(report))

    def test_restricted_content_in_any_security_observation_fails(self):
        observations = {
            case["id"]: {"actual_http": case["expected_http"],
                          "decision": case["expected_decision"]}
            for case in g6_security.SECURITY_CASES
        }
        observations["SEC-01"]["body"] = "secret body"
        report = g6_security.build_security_report(observations=observations)
        self.assertEqual(report["result"], "FAIL")
        self.assertEqual(next(case for case in report["cases"] if case["id"] == "SEC-01")["result"], "FAIL")

    def test_metadata_query_with_body_is_a_security_failure(self):
        observations = {
            case["id"]: {"actual_http": case["expected_http"],
                          "decision": case["expected_decision"]}
            for case in g6_security.SECURITY_CASES
        }
        observations["SEC-12"]["body"] = "restricted source"
        report = g6_security.build_security_report(observations=observations)
        self.assertEqual(report["result"], "FAIL")
        self.assertEqual(next(case for case in report["cases"] if case["id"] == "SEC-12")["result"], "FAIL")

    def test_nested_metadata_body_is_a_security_failure(self):
        observations = {
            case["id"]: {"actual_http": case["expected_http"],
                          "decision": case["expected_decision"]}
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

    def test_security_fixture_id_must_be_redacted(self):
        with self.assertRaisesRegex(ValueError, "fixture_id"):
            g6_security.build_security_report(fixture_id="tenant/secret/project")


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
             "runtime_mode": "real", "quality_result": "PASS",
             "supported_claims": 9, "total_claims": 10, "dismissed_issues": 1, "issues_reviewed": 5,
             "unconfirmed_items": 1, "snapshots": 2, "export_loss_items": 0, "export_checks": 4,
             "manual_minutes": 20, "projects": 1, "missed_edits": 1, "changesets": 2},
            {"sample_id": "sample.real2", "sample_version": "sample.v1",
             "runtime_mode": "real", "quality_result": "PASS",
             "supported_claims": 8, "total_claims": 10, "dismissed_issues": 0, "issues_reviewed": 5,
             "unconfirmed_items": 0, "snapshots": 2, "export_loss_items": 0, "export_checks": 4,
             "manual_minutes": 15, "projects": 1, "missed_edits": 0, "changesets": 2},
        ]
        report = g6_metrics.build_beta_report(samples=samples, window=self.WINDOW)
        self.assertEqual(report["result"], "PASS")
        self.assertEqual(report["decision"], "expand")
        self.assertEqual(report["metrics"]["source_support_rate"]["numerator"], 17)

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

    def test_beta_window_requires_process_evidence_and_redacted_risks(self):
        incomplete = {key: value for key, value in self.WINDOW.items() if key != "process_evidence"}
        with self.assertRaisesRegex(ValueError, "process_evidence"):
            g6_metrics.build_beta_report(samples=[], window=incomplete)
        with self.assertRaisesRegex(ValueError, "uncovered_risks"):
            g6_metrics.build_beta_report(samples=[], window={**self.WINDOW,
                                                              "uncovered_risks": ["secret document"]})

    def test_missing_sample_values_are_reported_and_block_effect_rates(self):
        sample = {"sample_id": "sample.real1", "sample_version": "sample.v1",
                  "runtime_mode": "real", "quality_result": "PASS",
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
        operations = [
            "createProject", "bindAsset", "saveChapter", "confirmChapter", "prepareRelease",
            "startExport", "getExport", "downloadExport",
        ]
        return {"runner_status": "completed", "completed_steps": len(operations),
                "workflow": "F01",
                "total_steps": len(operations), "verification_scope": "http_smoke_only",
                "steps": [{"operation_id": operation, "verdict": "passed"} for operation in operations]}

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
        self.assertEqual(report["result"], "PASS")
        evidence = report["quality_evidence"]
        self.assertEqual(evidence["template_version"], "template.v1")
        self.assertEqual(evidence["object_versions"]["project"], "project.v1")
        self.assertEqual(evidence["permission_snapshot"]["status"], "mock")
        self.assertEqual(sorted(report["fixture_state"]),
                         ["asset", "chapter", "check", "download", "export", "project", "release"])
        self.assertNotIn("duplicate_formal_write", json.dumps(report))

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

    def test_failure_readback_rejects_unredacted_status_values(self):
        report = g6_main_path.build_main_path_report(
            main_report=self._complete_report(),
            failure_observation={"failure_case": "duplicate_formal_write", "expected_http": 409,
                                 "actual_http": 409, "no_formal_side_effect": True,
                                 "readback": {"status": "secret body", "revision": "real-revision"}})
        self.assertEqual(report["result"], "FAIL")
        self.assertEqual(report["failure_path"]["readback"], {})
        self.assertNotIn("secret body", json.dumps(report))

    def test_missing_export_part_cannot_claim_main_path_pass(self):
        report = self._complete_report()
        report["steps"] = [step for step in report["steps"] if step["operation_id"] != "downloadExport"]
        report["completed_steps"] -= 1
        result = g6_main_path.build_main_path_report(main_report=report,
                                                     failure_observation={"no_formal_side_effect": True})
        self.assertEqual(result["result"], "BLOCKED")
        self.assertIn("download", result["fixture_coverage"]["missing_parts"])


class G6RunTest(unittest.TestCase):
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
                             "verification_scope": "observed",
                             "scenario_evidence": {
                                 "main_path": {"result": "PASS", "fixture_id": "F01:S1",
                                               "completed_steps": 23, "verification_scope": "observed"},
                                 "key_failure": {"result": "PASS", "fixture_id": "F01:S1",
                                                 "actual_http": 409, "no_formal_side_effect": True,
                                                 "readback_status": "unchanged"},
                             }}
                            for mode in ("mock", "real_api_fake_model", "real")]}
        report = g6_run.build_g6_report(runtime_matrix_report=matrix)
        self.assertEqual(report["gate_results"]["G6-02"], "PASS")
        self.assertNotIn("SECRET_TOKEN", json.dumps(report))

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
