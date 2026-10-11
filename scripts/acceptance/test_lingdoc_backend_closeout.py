import unittest
from lingdoc_backend_closeout import group_result, overall_result, parse_go_events, safe_postgres_target

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
