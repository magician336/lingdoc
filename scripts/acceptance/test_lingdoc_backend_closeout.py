import unittest
from functools import partial
from pathlib import Path
from tempfile import TemporaryDirectory
from unittest.mock import patch
from lingdoc_backend_closeout import (
    clean_failure_fixture_environment,
    cleanup_docker_container,
    group_result,
    overall_result,
    parse_go_events,
    safe_postgres_target,
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
            "DB_PASSWORD": "old-password",
        })
        self.assertEqual(environment, {"PATH": "path"})

    def test_cleanup_callback_is_bound_and_unregisterable(self):
        with TemporaryDirectory() as directory:
            callback = partial(cleanup_docker_container, "synthetic-container", Path(directory) / "cleanup.log")
            with patch("lingdoc_backend_closeout.stop_docker_container", return_value=True):
                self.assertTrue(callback())
            # atexit.unregister accepts the same bound callback object with one argument.
            import atexit
            atexit.unregister(callback)

    def test_test_events_distinguish_pass_skip_and_missing(self):
        events = parse_go_events([
            '{"Action":"pass","Test":"TestA","Package":"example"}',
            '{"Action":"skip","Test":"TestB","Package":"example"}',
        ])
        self.assertEqual(group_result(["TestA"], events)[0], "PASS")
        self.assertEqual(group_result(["TestB"], events)[0], "NOT RUN")
        self.assertEqual(group_result(["TestC"], events)[0], "BLOCKED")

    def test_full_closeout_never_passes_with_not_run_cases(self):
        self.assertEqual(overall_result([{"status":"PASS"}], [{"status":"PASS"}], [{"status":"NOT RUN"}]), "BLOCKED")
        self.assertEqual(overall_result([{"status":"FAIL"}], [], []), "FAIL")

    def test_cleanup_failure_blocks_closeout(self):
        self.assertEqual(overall_result([{"status":"PASS"}, {"status":"BLOCKED"}], [{"status":"PASS"}], []), "BLOCKED")

if __name__ == "__main__":
    unittest.main()
