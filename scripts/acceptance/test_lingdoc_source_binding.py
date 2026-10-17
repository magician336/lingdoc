import json
from pathlib import Path
import subprocess
from types import SimpleNamespace
import unittest
from unittest.mock import patch

import lingdoc_backend_closeout as backend
import lingdoc_minimal_summary as minimal
from lingdoc_source_binding import capture_source_binding, compare_source_bindings


ROOT = Path(__file__).resolve().parents[2]
HEAD, CLAIM, TREE, OTHER_TREE = (value * 40 for value in "abcd")


class TestSourceBinding(unittest.TestCase):
    def capture(self, *, claimed=HEAD, tree=TREE, claimed_tree=TREE,
                status="", environment=None, parents=None, tracked_output="", post=False):
        def git(root, *args):
            values = {
                ("rev-parse", "--show-toplevel"): str(ROOT),
                ("rev-parse", "--verify", "HEAD"): HEAD,
                ("rev-parse", "--verify", "HEAD^{tree}"): tree,
                ("rev-parse", "--verify", str(claimed) + "^{commit}"): claimed,
                ("rev-parse", "--verify", str(claimed) + "^{tree}"): claimed_tree,
                ("status", "--porcelain=v1", "-z", "--untracked-files=all"): status,
                ("show", "-s", "--format=%P", "HEAD"): " ".join(parents or [OTHER_TREE, CLAIM]),
                ("ls-files", "-z", "--", "artifacts/run"): tracked_output,
            }
            return values[args]
        with patch("lingdoc_source_binding._git", side_effect=git):
            return capture_source_binding(ROOT, claimed, artifact_paths=(ROOT / "artifacts/run",),
                                          ignore_new_artifacts=post, environment=environment or {})

    def test_clean_exact_head_is_bound(self):
        result = self.capture()
        self.assertEqual((result["status"], result["candidate_kind"]), ("PASS", "exact_head"))

    def test_different_commit_with_same_tree_is_explicit(self):
        result = self.capture(claimed=CLAIM)
        self.assertEqual(result["candidate_kind"], "head_tree_equivalent")
        self.assertEqual(result["checkout_commit"], HEAD)
        self.assertEqual(result["claimed_head"], CLAIM)

    def test_arbitrary_claim_cannot_pass(self):
        result = self.capture(claimed="this-is-not-a-git-sha")
        self.assertEqual(result["status"], "BLOCKED")

    def test_valid_but_different_tree_cannot_pass_as_head(self):
        self.assertEqual(self.capture(claimed=CLAIM, claimed_tree=OTHER_TREE)["status"], "BLOCKED")

    def test_ci_merge_is_bound_to_its_actual_tree(self):
        ci = {"GITHUB_EVENT_NAME": "pull_request", "GITHUB_REF": "refs/pull/122/merge", "GITHUB_SHA": HEAD}
        result = self.capture(claimed=CLAIM, claimed_tree=OTHER_TREE, environment=ci)
        self.assertEqual((result["status"], result["candidate_kind"]), ("PASS", "integrated_candidate"))
        self.assertEqual(result["checkout_tree"], TREE)
        self.assertNotEqual(result["checkout_tree"], result["claimed_tree"])

    def test_ci_label_does_not_allow_unrelated_claim(self):
        ci = {"GITHUB_EVENT_NAME": "pull_request", "GITHUB_REF": "refs/pull/122/merge", "GITHUB_SHA": HEAD}
        self.assertEqual(self.capture(claimed=CLAIM, claimed_tree=OTHER_TREE, environment=ci,
                                      parents=[OTHER_TREE, TREE])["status"], "BLOCKED")

    def test_dirty_tracked_or_untracked_source_blocks(self):
        for status in (" M internal/source.go\0", "?? internal/new.go\0"):
            with self.subTest(status=status):
                self.assertEqual(self.capture(status=status)["status"], "BLOCKED")

    def test_only_new_untracked_outputs_are_ignored_after_tests(self):
        output = "?? artifacts/run/go-test.jsonl\0"
        self.assertEqual(self.capture(status=output)["status"], "BLOCKED")
        self.assertEqual(self.capture(status=output, post=True)["status"], "PASS")
        self.assertEqual(self.capture(status=" M artifacts/run/source.py\0", post=True)["status"], "BLOCKED")
        self.assertEqual(self.capture(tracked_output="artifacts/run/source.py\0")["status"], "BLOCKED")

    def test_output_cannot_hide_all_untracked_sources(self):
        with patch("lingdoc_source_binding._git", side_effect=[str(ROOT), HEAD, TREE]):
            result = capture_source_binding(ROOT, artifact_paths=(ROOT,))
        self.assertEqual(result["status"], "BLOCKED")

    def test_git_errors_do_not_turn_into_unknown_pass(self):
        for error in (FileNotFoundError("git"), ValueError("Git identity lookup failed"),
                      subprocess.TimeoutExpired("git", 30)):
            with self.subTest(error=type(error).__name__):
                with patch("lingdoc_source_binding._git", side_effect=error):
                    self.assertEqual(capture_source_binding(ROOT)["status"], "BLOCKED")

    def test_source_change_during_tests_blocks(self):
        before = self.capture()
        after = {**before, "checkout_tree": OTHER_TREE}
        self.assertEqual(compare_source_bindings(before, after)["status"], "BLOCKED")
        self.assertEqual(compare_source_bindings(before, before)["status"], "PASS")

    def test_nonzero_git_process_blocks_without_exposing_stderr(self):
        failed = SimpleNamespace(returncode=128, stdout="", stderr="private diagnostic")
        with patch("lingdoc_source_binding.subprocess.run", return_value=failed):
            result = capture_source_binding(ROOT)
        self.assertEqual(result["status"], "BLOCKED")
        self.assertNotIn("private diagnostic", result["reason"])


class TestBindingEntrypoints(unittest.TestCase):
    def setUp(self):
        self.clean = {"status": "PASS", "checkout_commit": HEAD, "checkout_tree": TREE,
                      "candidate_kind": "checkout", "root": str(ROOT)}
        self.blocked = {**self.clean, "status": "BLOCKED", "reason": "source changed"}

    def test_backend_preflight_blocks_before_database_or_go(self):
        with patch.object(backend, "capture_source_binding", return_value=self.blocked), \
             patch.object(backend, "start_postgres") as database, \
             patch.object(backend.subprocess, "run") as command:
            self.assertEqual(backend.main(["--output-dir", "unused"]), 2)
        database.assert_not_called()
        command.assert_not_called()

    def test_minimal_preflight_blocks_before_checks_or_writes(self):
        with patch.object(minimal, "capture_source_binding", return_value=self.blocked), \
             patch.object(minimal, "run_check") as check, \
             patch.object(Path, "write_text") as write, \
             patch("sys.argv", ["summary", "--output", "unused.json"]):
            self.assertEqual(minimal.main(), 2)
        check.assert_not_called()
        write.assert_not_called()

    def test_minimal_postflight_blocks_even_when_checks_pass(self):
        with patch.object(minimal, "capture_source_binding", side_effect=[self.clean, self.blocked]), \
             patch.object(minimal, "run_check", return_value={"status": "PASS"}), \
             patch.object(Path, "mkdir"), patch.object(Path, "write_text") as write, \
             patch("sys.argv", ["summary", "--output", "unused.json"]):
            self.assertNotEqual(minimal.main(), 0)
        report = json.loads(write.call_args.args[0])
        self.assertEqual(report["overall"], "BLOCKED")
        self.assertEqual(report["commit_binding"]["status"], "BLOCKED")

    def test_minimal_reports_actual_checkout_not_claimed_head(self):
        merged = {**self.clean, "claimed_head": CLAIM, "candidate_kind": "integrated_candidate"}
        with patch.object(minimal, "capture_source_binding", return_value=merged), \
             patch.object(minimal, "run_check", return_value={"status": "PASS"}), \
             patch.object(Path, "mkdir"), patch.object(Path, "write_text") as write, \
             patch("sys.argv", ["summary", "--output", "unused.json"]):
            self.assertEqual(minimal.main(), 0)
        report = json.loads(write.call_args.args[0])
        self.assertEqual(report["commit"], HEAD)
        self.assertEqual(report["commit_binding"]["before"]["claimed_head"], CLAIM)

    def test_backend_postflight_blocks_without_require_postgres(self):
        with patch.object(backend, "capture_source_binding", side_effect=[self.clean, self.blocked]), \
             patch.object(backend, "start_postgres", return_value=(None, {"status": "BLOCKED"}, None)), \
             patch.object(backend.subprocess, "run", return_value=SimpleNamespace(stdout="go version fake")), \
             patch.object(backend, "run_command", return_value={"name": "fake", "status": "PASS"}), \
             patch.object(backend, "atomic_write") as write, \
             patch.object(Path, "mkdir"), patch.object(Path, "exists", return_value=False):
            self.assertEqual(backend.main(["--output-dir", "unused"]), 2)
        report = json.loads(write.call_args_list[0].args[1])
        self.assertEqual(report["automated_result"], "BLOCKED")
        self.assertEqual(report["source_binding"]["status"], "BLOCKED")


if __name__ == "__main__":
    unittest.main()
