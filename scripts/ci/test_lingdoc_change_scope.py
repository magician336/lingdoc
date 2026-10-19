"""Tests for commit-scoped workflow selection."""
from __future__ import annotations

import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import lingdoc_change_scope as scope


class ChangeScopeTests(unittest.TestCase):
    def test_documentation_only_paths_do_not_match_code_patterns(self):
        self.assertFalse(scope.matches(["docs/report.md"], ["internal/lingdoc/**", "**/*.go"]))

    def test_code_paths_match(self):
        self.assertTrue(scope.matches(["internal/lingdoc/rulesetstore/metrics.go"], ["internal/lingdoc/**"]))

    def test_double_star_matches_repository_root(self):
        self.assertTrue(scope.matches(["main.go"], ["**/*.go"]))

    def test_negative_patterns_exclude_unrelated_go_modules(self):
        patterns = ["**/*.go", "!cli/**", "!client/**"]
        self.assertFalse(scope.matches(["cli/main.go"], patterns))
        self.assertTrue(scope.matches(["internal/main.go"], patterns))

    def test_synchronize_uses_previous_head(self):
        event = {
            "action": "synchronize",
            "pull_request": {"base": {"sha": "base"}, "head": {"sha": "head"}},
        }
        with patch.object(scope, "_run_git", return_value="previous"):
            self.assertEqual(scope.event_range(event, Path.cwd()), ("previous...head", False))

    def test_synchronize_prefers_event_update_range(self):
        event = {
            "action": "synchronize",
            "before": "before",
            "after": "head",
            "pull_request": {"base": {"sha": "base"}, "head": {"sha": "head"}},
        }
        with patch.object(scope, "_run_git", return_value="merge-base"):
            self.assertEqual(scope.event_range(event, Path.cwd()), ("before...head", False))

    def test_synchronize_unresolvable_before_falls_back_to_head_parent(self):
        event = {
            "action": "synchronize",
            "before": "old-before",
            "after": "head",
            "pull_request": {"base": {"sha": "base"}, "head": {"sha": "head"}},
        }
        with patch.object(
            scope,
            "_run_git",
            side_effect=[subprocess.CalledProcessError(128, "git merge-base"), "parent"],
        ):
            self.assertEqual(scope.event_range(event, Path.cwd()), ("parent...head", False))

    def test_opened_uses_complete_pr(self):
        event = {
            "action": "opened",
            "pull_request": {"base": {"sha": "base"}, "head": {"sha": "head"}},
        }
        self.assertEqual(scope.event_range(event, Path.cwd()), ("base...head", False))

    def test_workflow_dispatch_is_forced(self):
        event = {"action": "workflow_dispatch"}
        with patch.dict("os.environ", {"GITHUB_EVENT_NAME": "workflow_dispatch"}):
            self.assertEqual(scope.event_range(event, Path.cwd()), (None, True))

    def test_scope_diff_failure_falls_back_to_complete_pr(self):
        event = {
            "action": "synchronize",
            "pull_request": {"base": {"sha": "base"}, "head": {"sha": "head"}},
        }
        with patch.object(scope, "event_range", return_value=("before...head", False)), \
             patch.object(scope, "changed_paths", side_effect=[
                 subprocess.CalledProcessError(128, "git diff"), ["internal/main.go"]
             ]):
            self.assertEqual(
                scope.select_paths(Path.cwd(), event),
                ("base...head", False, ["internal/main.go"]),
            )


if __name__ == "__main__":
    unittest.main()
