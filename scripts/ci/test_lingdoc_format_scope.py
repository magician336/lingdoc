"""Tests for the formatting-only classifier behind the CI scope gate.

A formatting-only change keeps the meaning of the code, so the expensive
test matrix does not have to run again. The classifier has to be
conservative in both directions: a formatting-only change must be
recognised, and anything else must fall back to the full matrix.
"""
from __future__ import annotations

import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import lingdoc_change_scope as change_scope
import lingdoc_format_scope as fmt


GOFMT_PROGRAM = """package sample

// A documented function.
func Value() int {
	return 42
}
"""


def git(root: Path, *args: str) -> str:
    return subprocess.check_output(["git", *args], cwd=root, text=True,
                                   stderr=subprocess.PIPE).strip()


class FormatScopeTests(unittest.TestCase):
    def setUp(self):
        self._temp = tempfile.TemporaryDirectory()
        self.root = Path(self._temp.name)
        git(self.root, "init", "-q")
        git(self.root, "config", "user.email", "test@example.com")
        git(self.root, "config", "user.name", "Test")
        self.write("sample.go", GOFMT_PROGRAM)
        git(self.root, "add", "sample.go")
        git(self.root, "commit", "-q", "-m", "base")

    def tearDown(self):
        self._temp.cleanup()

    def write(self, name: str, content: str) -> None:
        (self.root / name).write_text(content, encoding="utf-8")

    def commit(self, message: str = "change") -> None:
        git(self.root, "add", "-A")
        git(self.root, "commit", "-q", "-m", message)

    def classify_last(self):
        return fmt.classify(self.root, "HEAD~1...HEAD")

    # --- judgement (b): comments and whitespace only -----------------------

    def test_comment_only_change_is_formatting_only(self):
        self.write("sample.go", GOFMT_PROGRAM.replace("// A documented function.",
                                                      "// A documented function.\n// It returns a constant."))
        self.commit()
        verdict = self.classify_last()
        self.assertTrue(verdict.format_only, verdict.reasons)
        self.assertTrue(any("sample.go" in reason for reason in verdict.reasons))

    def test_whitespace_only_change_is_formatting_only(self):
        self.write("sample.go", GOFMT_PROGRAM.replace("\treturn 42", "    return 42"))
        self.commit()
        self.assertTrue(self.classify_last().format_only)

    def test_adding_a_blank_line_is_formatting_only(self):
        self.write("sample.go", GOFMT_PROGRAM.replace("func Value() int {", "\nfunc Value() int {"))
        self.commit()
        self.assertTrue(self.classify_last().format_only)

    # --- judgement (a): gofmt equivalence -----------------------------------

    def test_gofmt_output_is_formatting_only(self):
        # gofmt sorts the imports inside a block. That changes the token
        # order, so only the gofmt judgement can accept it.
        before = "package sample\n\nimport (\n\t\"os\"\n\t\"fmt\"\n)\n"
        after = "package sample\n\nimport (\n\t\"fmt\"\n\t\"os\"\n)\n"
        self.write("sample.go", before)
        git(self.root, "add", "sample.go")
        git(self.root, "commit", "-q", "-m", "base with unsorted imports")
        self.write("sample.go", after)
        self.commit()
        verdict = self.classify_last()
        self.assertTrue(verdict.format_only, verdict.reasons)
        self.assertTrue(any("gofmt" in reason for reason in verdict.reasons))

    # --- judgement (b) must not be fooled by string literals ---------------

    def test_whitespace_inside_a_string_literal_is_not_formatting_only(self):
        before = 'package sample\n\nfunc Value() string {\n\treturn "a b"\n}\n'
        after = 'package sample\n\nfunc Value() string {\n\treturn "a  b"\n}\n'
        self.write("sample.go", before)
        git(self.root, "add", "sample.go")
        git(self.root, "commit", "-q", "-m", "base with string")
        self.write("sample.go", after)
        self.commit()
        verdict = self.classify_last()
        self.assertFalse(verdict.format_only, verdict.reasons)

    def test_comment_marker_inside_a_string_is_not_a_comment(self):
        before = 'package sample\n\nfunc Value() string {\n\treturn "http://example.com/x"\n}\n'
        after = 'package sample\n\nfunc Value() string {\n\treturn "http://example.com/y"\n}\n'
        self.write("sample.go", before)
        git(self.root, "add", "sample.go")
        git(self.root, "commit", "-q", "-m", "base with url")
        self.write("sample.go", after)
        self.commit()
        self.assertFalse(self.classify_last().format_only)

    # --- judgement (b) must not be fooled by code changes ------------------

    def test_logic_change_is_not_formatting_only(self):
        self.write("sample.go", GOFMT_PROGRAM.replace("return 42", "return 43"))
        self.commit()
        verdict = self.classify_last()
        self.assertFalse(verdict.format_only, verdict.reasons)

    def test_added_code_line_is_not_formatting_only(self):
        self.write("sample.go", GOFMT_PROGRAM.replace("\treturn 42", "\tx := 1\n\treturn x"))
        self.commit()
        self.assertFalse(self.classify_last().format_only)

    def test_reordered_code_lines_are_not_formatting_only(self):
        self.write("sample.go", GOFMT_PROGRAM.replace("\treturn 42", "\tx := 1\n\ty := 2\n\treturn x + y"))
        self.commit()
        self.assertFalse(self.classify_last().format_only)

    def test_compiler_directive_change_is_not_formatting_only(self):
        before = "//go:build linux\n\npackage sample\n\nfunc Value() int {\n\treturn 42\n}\n"
        after = "//go:build windows\n\npackage sample\n\nfunc Value() int {\n\treturn 42\n}\n"
        self.write("sample.go", before)
        git(self.root, "add", "sample.go")
        git(self.root, "commit", "-q", "-m", "base with build tag")
        self.write("sample.go", after)
        self.commit()
        self.assertFalse(self.classify_last().format_only)

    # --- mixed and non-Go changes -----------------------------------------

    def test_mixed_change_is_not_formatting_only(self):
        self.write("sample.go", GOFMT_PROGRAM.replace("return 42", "return 43"))
        self.write("other.go", "package sample\n\nfunc Other() int {\n\treturn 1\n}\n")
        self.commit()
        verdict = self.classify_last()
        self.assertFalse(verdict.format_only, verdict.reasons)
        self.assertTrue(any("other.go" in reason for reason in verdict.reasons))

    def test_only_non_go_files_changed_is_not_formatting_only(self):
        # Documentation-only updates are handled by the path-based scope; the
        # content-level verdict must not claim them as formatting.
        self.write("notes.md", "hello\n")
        self.commit()
        self.assertFalse(self.classify_last().format_only)

    def test_added_file_is_not_formatting_only(self):
        self.write("added.go", "package sample\n\nfunc Added() int {\n\treturn 1\n}\n")
        self.commit()
        self.assertFalse(self.classify_last().format_only)

    def test_deleted_file_is_not_formatting_only(self):
        (self.root / "sample.go").unlink()
        self.commit()
        self.assertFalse(self.classify_last().format_only)

    # --- fail closed -------------------------------------------------------

    def test_unresolvable_range_is_not_formatting_only(self):
        verdict = fmt.classify(self.root, "does-not-exist...also-missing")
        self.assertFalse(verdict.format_only)
        self.assertTrue(verdict.reasons)

    def test_empty_range_is_not_formatting_only(self):
        verdict = fmt.classify(self.root, "")
        self.assertFalse(verdict.format_only)

    def test_reason_mentions_the_file_that_decided(self):
        self.write("sample.go", GOFMT_PROGRAM.replace("return 42", "return 43"))
        self.commit()
        verdict = self.classify_last()
        self.assertTrue(any("sample.go" in reason for reason in verdict.reasons))


class FastPathTests(unittest.TestCase):
    """The fast path may only be used for a pull request update."""

    def test_pull_request_update_uses_the_fast_path(self):
        applies, _ = fmt.fast_path_applies(
            {"action": "synchronize", "pull_request": {"number": 1}})
        self.assertTrue(applies)

    def test_opened_pull_request_runs_the_full_matrix(self):
        applies, reason = fmt.fast_path_applies(
            {"action": "opened", "pull_request": {"number": 1}})
        self.assertFalse(applies)
        self.assertIn("not an update", reason)

    def test_reopened_pull_request_runs_the_full_matrix(self):
        applies, _ = fmt.fast_path_applies(
            {"action": "reopened", "pull_request": {"number": 1}})
        self.assertFalse(applies)

    def test_push_to_main_runs_the_full_matrix(self):
        applies, reason = fmt.fast_path_applies(
            {"ref": "refs/heads/main", "before": "a", "after": "b"})
        self.assertFalse(applies)
        self.assertIn("not a pull request event", reason)


class ChangeScopeIntegrationTests(unittest.TestCase):
    """The scope script must publish the formatting-only decision."""

    def setUp(self):
        self._temp = tempfile.TemporaryDirectory()
        self.root = Path(self._temp.name)
        git(self.root, "init", "-q")
        git(self.root, "config", "user.email", "test@example.com")
        git(self.root, "config", "user.name", "Test")
        (self.root / "sample.go").write_text(GOFMT_PROGRAM, encoding="utf-8")
        git(self.root, "add", "sample.go")
        git(self.root, "commit", "-q", "-m", "base")
        self.event_path = self.root / "event.json"
        self.output_path = self.root / "output.txt"

    def tearDown(self):
        self._temp.cleanup()

    def change(self, content: str, message: str) -> None:
        (self.root / "sample.go").write_text(content, encoding="utf-8")
        git(self.root, "add", "sample.go")
        git(self.root, "commit", "-q", "-m", message)

    def run_scope(self, action: str) -> dict[str, str]:
        base = git(self.root, "rev-parse", "HEAD~1")
        head = git(self.root, "rev-parse", "HEAD")
        self.event_path.write_text(json.dumps({
            "action": action,
            "before": base,
            "after": head,
            "pull_request": {"base": {"sha": base}, "head": {"sha": head}},
        }), encoding="utf-8")
        self.output_path.write_text("", encoding="utf-8")
        with patch.dict(os.environ, {"GITHUB_EVENT_PATH": str(self.event_path),
                                      "GITHUB_OUTPUT": str(self.output_path)}), \
             patch.object(sys, "argv", ["lingdoc_change_scope.py", "--root", str(self.root),
                                       "--patterns", "**/*.go"]):
            change_scope.main()
        values: dict[str, str] = {}
        for line in self.output_path.read_text(encoding="utf-8").splitlines():
            key, _, value = line.partition("=")
            values[key] = value
        return values

    def test_update_with_a_formatting_only_change_is_reported_as_format_only(self):
        self.change(GOFMT_PROGRAM.replace("// A documented function.",
                                           "// A documented function.\n// It returns a constant."),
                    "comment")
        values = self.run_scope("synchronize")
        self.assertEqual(values.get("format_only"), "true")
        # The path-based decision is unchanged: the code path still matches.
        self.assertEqual(values.get("run"), "true")

    def test_opened_pull_request_keeps_the_full_matrix(self):
        self.change(GOFMT_PROGRAM.replace("// A documented function.",
                                           "// A documented function.\n// It returns a constant."),
                    "comment")
        values = self.run_scope("opened")
        self.assertEqual(values.get("format_only"), "false")

    def test_a_code_change_is_not_formatting_only(self):
        self.change(GOFMT_PROGRAM.replace("return 42", "return 43"), "logic")
        values = self.run_scope("synchronize")
        self.assertEqual(values.get("format_only"), "false")
        self.assertEqual(values.get("run"), "true")


class WorkflowWiringTests(unittest.TestCase):
    """Every test-class job must carry the gate; the judge must not."""

    WORKFLOWS = Path(__file__).resolve().parents[2] / ".github" / "workflows"

    # The jobs that run the test matrix. A new test job has to be listed here
    # so that it cannot be added without the formatting-only gate.
    TEST_JOBS = {
        "app.yml": ["verify"],
        "frontend.yml": ["verify", "docker-image"],
        "lingdoc-f01-runner.yml": ["runner-tests"],
        "lingdoc-interface-integration.yml": ["integration", "frontend"],
        "lingdoc-mock.yml": ["mock"],
        "lingdoc-workspace-concurrency.yml": ["workspace-concurrency"],
    }

    def load(self, name: str) -> dict:
        import yaml

        document = yaml.safe_load((self.WORKFLOWS / name).read_text(encoding="utf-8"))
        return document.get("jobs") or {}

    def test_every_test_job_is_gated_on_format_only(self):
        for name, jobs in self.TEST_JOBS.items():
            document = self.load(name)
            for job in jobs:
                condition = document[job].get("if", "")
                self.assertIn("format_only", condition,
                              f"{name}: job {job} runs the test matrix and must skip a formatting-only update")

    def test_every_gated_workflow_declares_the_format_only_output(self):
        # A job's outputs are an **explicit allow-list**: a step that writes
        # `format_only` to GITHUB_OUTPUT does not make it visible as
        # `needs.scope.outputs.format_only` unless the scope job declares it.
        # When it is missing, the gate below reads an empty string, the
        # comparison is true, and the job silently runs the full matrix —
        # the rule looks wired but does nothing.
        for name in self.TEST_JOBS:
            document = self.load(name)
            outputs = document["scope"].get("outputs", {})
            self.assertIn("format_only", outputs,
                          f"{name}: scope job must declare the format_only output, "
                          f"otherwise needs.scope.outputs.format_only is empty and the gate never fires")

    def test_the_lint_job_keeps_running(self):
        # Lint is the judge of whether an update is well formatted, so it must
        # not be skipped by the rule it judges.
        condition = self.load("go-lint.yml")["changes"].get("if", "")
        self.assertNotIn("format_only", condition)

    def test_the_contract_validation_steps_keep_running(self):
        import yaml

        document = yaml.safe_load((self.WORKFLOWS / "lingdoc-review.yml").read_text(encoding="utf-8"))
        steps = document["jobs"]["gate"]["steps"] if "gate" in document["jobs"] else None
        if steps is None:
            steps = [step for job in document["jobs"].values() for step in job.get("steps") or []]
        contract_steps = [step for step in steps
                          if "contracts" in str(step.get("if", "")) and "provider_tests" not in str(step.get("if", ""))]
        self.assertTrue(contract_steps, "the contract validation steps were not found")
        for step in contract_steps:
            self.assertNotIn("format_only", str(step.get("if", "")))

    def test_the_provider_and_frontend_steps_are_gated(self):
        import yaml

        document = yaml.safe_load((self.WORKFLOWS / "lingdoc-review.yml").read_text(encoding="utf-8"))
        steps = [step for job in document["jobs"].values() for step in job.get("steps") or []]
        gated = [step for step in steps
                 if "provider_tests" in str(step.get("if", "")) or
                 ("frontend" in str(step.get("if", "")) and "contracts" not in str(step.get("if", "")))]
        self.assertTrue(gated, "the provider and frontend steps were not found")
        for step in gated:
            self.assertIn("format_only", str(step.get("if", "")))


class ProjectionTests(unittest.TestCase):
    def test_projection_drops_comments_and_outer_whitespace(self):
        source = "package a // trailing\n\nfunc F() int {\n\treturn 1 // value\n}\n"
        self.assertEqual(fmt.semantic_projection(source),
                         ["packagea", "funcF()int{", "return1", "}"])

    def test_projection_keeps_string_literals_verbatim(self):
        source = 'package a\n\nvar s = "keep  me"\n'
        self.assertEqual(fmt.semantic_projection(source), ["packagea", 'vars="keep  me"'])

    def test_projection_keeps_raw_string_verbatim(self):
        source = "package a\n\nvar s = `raw  text`\n"
        self.assertEqual(fmt.semantic_projection(source), ["packagea", "vars=`raw  text`"])

    def test_projection_handles_block_comments_across_lines(self):
        source = "package a\n/* block\n   comment */\nvar x = 1\n"
        self.assertEqual(fmt.semantic_projection(source), ["packagea", "varx=1"])


if __name__ == "__main__":
    unittest.main()