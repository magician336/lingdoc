import unittest
from functools import partial
from pathlib import Path
from tempfile import TemporaryDirectory
from unittest.mock import patch
from lingdoc_backend_closeout import (
    clean_failure_fixture_environment,
    cleanup_docker_container,
    cleanup_failure_fixture,
    group_result,
    overall_result,
    parse_go_events,
    safe_postgres_target,
    atomic_write,
)

class TestBackendCloseout(unittest.TestCase):
    def test_safe_synthetic_postgres_target(self):
        target = safe_postgres_target("host=localhost port=5432 user=lingdoc_test password=synthetic_ci_only dbname=lingdoc_test")
        self.assertEqual(target["database"], "lingdoc_test")
        self.assertEqual(target["port"], "5432")

    def test_refuses_remote_or_production_target(self):
        with self.assertRaises(ValueError):
            safe_postgres_target("host=db.example user=lingdoc_test dbname=lingdoc_test")
        with self.assertRaises(ValueError):
            safe_postgres_target(
                "host=localhost hostaddr=203.0.113.10 user=lingdoc_test dbname=lingdoc_test"
            )
        for dsn in (
            "service=remote_profile host=localhost user=lingdoc_test dbname=lingdoc_test",
            "service = remote_profile host=localhost user=lingdoc_test dbname=lingdoc_test",
        ):
            with self.subTest(dsn=dsn):
                with self.assertRaises(ValueError):
                    safe_postgres_target(dsn)
        with self.assertRaises(ValueError):
            safe_postgres_target("host=localhost user=postgres dbname=production")
        with self.assertRaises(ValueError):
            safe_postgres_target("host=localhost port=99999 user=lingdoc_test dbname=lingdoc_test")

    def test_refuses_url_query_target_overrides(self):
        for query in ("host=remote.example", "port=5433", "user=postgres", "dbname=production"):
            with self.subTest(query=query):
                with self.assertRaisesRegex(ValueError, "query"):
                    safe_postgres_target(
                        "postgresql://lingdoc_test:synthetic_ci_only@127.0.0.1:5432/lingdoc_test?" + query
                    )

    def test_cleans_inherited_failure_fixture_environment(self):
        environment = clean_failure_fixture_environment({
            "PATH": "path",
            "LINGDOC_FAILURE_TEST_DB": "old-db",
            "LINGDOC_FAILURE_TEST_USER": "old-user",
            "LINGDOC_FAILURE_TEST_DSN": "old-dsn",
            "LINGDOC_FAILURE_FIXTURE_MARKER": "old-marker",
            "LINGDOC_FAILURE_CRASH_RUN": "old-run",
            "LINGDOC_FAILURE_CRASH_READY": "old-ready",
            "DB_PASSWORD": "old-password",
        })
        self.assertEqual(environment, {"PATH": "path"})

    def test_cleanup_callback_is_bound_and_unregisterable(self):
        with TemporaryDirectory() as directory:
            cleanup_log = Path(directory) / "cleanup.log"
            callback = partial(cleanup_docker_container, "synthetic-container", cleanup_log)
            with patch("lingdoc_backend_closeout.stop_docker_container", return_value=True):
                self.assertTrue(callback())
            # atexit.unregister accepts the same bound callback object with one argument.
            import atexit
            atexit.unregister(callback)
            with patch("lingdoc_backend_closeout.stop_docker_container", return_value=False):
                self.assertFalse(callback())
            self.assertIn("BLOCKED", cleanup_log.read_text(encoding="utf-8"))

    def test_failure_fixture_cleanup_callback_runs_drop_command(self):
        with TemporaryDirectory() as directory:
            cleanup_log = Path(directory) / "postgres-fixture-cleanup.log"
            environment = {"LINGDOC_TEST_POSTGRES_DSN": "host=localhost"}
            result = {
                "name": cleanup_log.stem,
                "status": "PASS",
                "exit_code": 0,
                "log": str(cleanup_log),
            }
            with patch("lingdoc_backend_closeout.run_command", return_value=result) as run:
                self.assertEqual(cleanup_failure_fixture(environment, cleanup_log), result)
            run.assert_called_once_with(
                ["go", "run", "./scripts/acceptance/postgres_failure_fixture.go", "drop"],
                environment,
                cleanup_log,
                300,
            )

    def test_atomic_write_replaces_previous_report(self):
        with TemporaryDirectory() as directory:
            report = Path(directory) / "report.json"
            report.write_text("old", encoding="utf-8")
            atomic_write(report, "new")
            self.assertEqual(report.read_text(encoding="utf-8"), "new")
            self.assertFalse((Path(directory) / "report.json.tmp").exists())

    def test_test_events_distinguish_pass_skip_and_missing(self):
        events = parse_go_events([
            '{"Action":"pass","Test":"TestA","Package":"example"}',
            '{"Action":"skip","Test":"TestB","Package":"example"}',
        ])
        self.assertEqual(group_result([("example", "TestA")], events)[0], "PASS")
        self.assertEqual(group_result([("example", "TestB")], events)[0], "NOT RUN")
        self.assertEqual(group_result([("example", "TestC")], events)[0], "BLOCKED")

    def test_test_events_do_not_cross_package_boundaries(self):
        events = parse_go_events([
            '{"Action":"pass","Test":"TestA","Package":"wrong-package"}',
        ])
        self.assertEqual(group_result([("expected-package", "TestA")], events)[0], "BLOCKED")

    def test_full_closeout_never_passes_with_not_run_cases(self):
        self.assertEqual(overall_result([{"status":"PASS"}], [{"status":"PASS"}], [{"status":"NOT RUN"}]), "BLOCKED")
        self.assertEqual(overall_result([{"status":"FAIL"}], [], []), "FAIL")

    def test_cleanup_failure_blocks_closeout(self):
        self.assertEqual(overall_result([{"status":"PASS"}, {"status":"BLOCKED"}], [{"status":"PASS"}], []), "BLOCKED")

if __name__ == "__main__":
    unittest.main()
