"""Tests for the gate surface manifest (``scripts/ci/lingdoc_gate_surfaces.py``).

The manifest is the one place that says *which gate surface belongs to which
workflow and job*.  It is declarative data: it answers ownership questions and
nothing else.  These tests pin the seams that matter for a maintainer:

* ``load_and_validate()`` loads the committed manifest offline and rejects a
  manifest that points at a workflow, a job, or a job name that does not exist.
* the manifest's path sets are compared against the workflow files themselves,
  which are the independent source of truth for what each gate watches.
* the ownership knowledge (provider roots, contracts directory, governance file
  set, frontend root) is registered once, in the manifest.

Bad manifest data must fail loudly: a silently degraded manifest would let a
gate watch nothing, or watch the wrong surface.
"""
from __future__ import annotations

import ast
from pathlib import Path
import subprocess
import sys
import unittest

import yaml

import lingdoc_gate_surfaces as manifest
import lingdoc_pr_scope as pr_scope
import lingdoc_risk_surface as risk_surface

CI_DIR = Path(__file__).resolve().parent
REPO_ROOT = CI_DIR.parents[1]
WORKFLOWS = REPO_ROOT / ".github" / "workflows"

# The seven workflow-level gates that carry their own path table.  This list is
# the expectation; the manifest has to agree with it.
GATED_WORKFLOWS = (
    "frontend.yml",
    "go-lint.yml",
    "app.yml",
    "lingdoc-mock.yml",
    "lingdoc-f01-runner.yml",
    "lingdoc-workspace-concurrency.yml",
    "lingdoc-interface-integration.yml",
)

# Files that still carry their own copy of the ownership knowledge.  Ticket
# T2 moved them to the manifest, so no file may register it any more: any
# file that does is a bug.
LEGACY_REGISTRATION_SITES = frozenset()


def workflow_document(name: str) -> dict:
    return yaml.safe_load((WORKFLOWS / name).read_text(encoding="utf-8"))


def workflow_scope_paths(name: str) -> list[str]:
    """The path table a workflow feeds to the scope script, read from YAML."""
    document = workflow_document(name)
    for job in document["jobs"].values():
        scopes = [(job.get("env") or {})]
        scopes += [(step.get("env") or {}) for step in job.get("steps") or []]
        for env in scopes:
            if "LINGDOC_SCOPE_PATHS" in env:
                text = env["LINGDOC_SCOPE_PATHS"]
                return [line.strip() for line in text.splitlines() if line.strip()]
    return []


def workflow_trigger_paths(name: str) -> list[str]:
    """The paths that start the workflow, read from YAML."""
    document = workflow_document(name)
    triggers = document.get("on") or document.get(True) or {}
    paths: list[str] = []
    for event in triggers.values() if isinstance(triggers, dict) else []:
        if isinstance(event, dict) and isinstance(event.get("paths"), list):
            paths.extend(event["paths"])
    return paths


def job_display_name(document: dict, job: str) -> str:
    """GitHub shows the job key when the job declares no display name."""
    definition = document["jobs"][job]
    return definition.get("name") or job


def record(**overrides) -> dict:
    """A minimal, valid manifest record to corrupt one field at a time."""
    base = {
        "name": "example-surface",
        "gate_level": manifest.GATE_LEVEL_REPOSITORY,
        "path_kind": manifest.PATH_KIND_GLOB,
        "paths": ("frontend/**",),
        "consumers": ({
            "workflow": ".github/workflows/frontend.yml",
            "job": "verify",
            "job_name": "Test, type-check, and build",
        },),
        "skipped_on_format_only": True,
    }
    base.update(overrides)
    return base


class _RegistrationVisitor(ast.NodeVisitor):
    """Collects the strings a module *writes down*, ignoring references."""

    def __init__(self):
        self.literals: list[str] = []

    def visit_Constant(self, node):
        if isinstance(node.value, str):
            self.literals.append(node.value)

    def visit_Dict(self, node):
        for key, value in zip(node.keys, node.values):
            # A consumer names a workflow and a job; that is a reference to an
            # owner, not a second registration of the ownership knowledge.
            if isinstance(key, ast.Constant) and key.value == "consumers":
                continue
            self.visit(value)


def _registration_literals(source: str) -> list[str]:
    """The string literals a module writes down, docstring and references aside."""
    tree = ast.parse(source)
    statements = tree.body
    if statements and isinstance(statements[0], ast.Expr) \
            and isinstance(statements[0].value, ast.Constant) \
            and isinstance(statements[0].value.value, str):
        statements = statements[1:]
    visitor = _RegistrationVisitor()
    for statement in statements:
        visitor.visit(statement)
    return visitor.literals


class ManifestLoadTests(unittest.TestCase):
    """The committed manifest loads offline and says what it must say."""

    def test_manifest_loads_and_validates(self):
        loaded = manifest.load_and_validate()
        self.assertTrue(loaded.surfaces)
        self.assertEqual(len(loaded.surfaces), len({s.name for s in loaded.surfaces}))

    def test_every_record_registers_a_surface_with_its_consumers(self):
        for surface in manifest.load_and_validate().surfaces:
            with self.subTest(surface=surface.name):
                self.assertTrue(surface.name)
                self.assertTrue(surface.paths)
                self.assertTrue(surface.consumers)
                self.assertIn(surface.gate_level,
                              (manifest.GATE_LEVEL_REPOSITORY,
                               manifest.GATE_LEVEL_TARGETED))
                self.assertIn(surface.path_kind,
                              (manifest.PATH_KIND_GLOB,
                               manifest.PATH_KIND_PREFIX,
                               manifest.PATH_KIND_EXACT))
                self.assertIsInstance(surface.skipped_on_format_only, bool)
                for consumer in surface.consumers:
                    self.assertTrue(consumer.workflow)
                    self.assertTrue(consumer.job)
                    self.assertTrue(consumer.job_name)

    def test_manifest_loads_without_any_network_dependency(self):
        source = (CI_DIR / "lingdoc_gate_surfaces.py").read_text(encoding="utf-8")
        imported = set()
        for node in ast.walk(ast.parse(source)):
            if isinstance(node, ast.Import):
                imported.update(alias.name.split(".")[0] for alias in node.names)
            elif isinstance(node, ast.ImportFrom) and node.module:
                imported.add(node.module.split(".")[0])
        self.assertEqual(imported & {"socket", "http", "urllib", "requests"}, set())

    def test_manifest_validates_as_a_script(self):
        result = subprocess.run(
            [sys.executable, str(CI_DIR / "lingdoc_gate_surfaces.py")],
            capture_output=True, text=True,
        )
        self.assertEqual(result.returncode, 0, result.stderr)


class ManifestValidationTests(unittest.TestCase):
    """Bad manifest data is rejected loudly, never silently degraded."""

    def assertRejected(self, records, *expected_fragments):
        with self.assertRaises(manifest.SurfaceManifestError) as caught:
            manifest.validate(records, root=REPO_ROOT)
        message = str(caught.exception)
        for fragment in expected_fragments:
            self.assertIn(fragment, message)

    def test_duplicate_surface_names_are_rejected(self):
        self.assertRejected([record(), record()], "duplicate surface name",
                            "example-surface")

    def test_empty_path_set_is_rejected(self):
        self.assertRejected([record(paths=())], "empty path set")

    def test_missing_field_is_rejected(self):
        broken = record()
        del broken["paths"]
        self.assertRejected([broken], "missing field", "paths")

    def test_missing_consumer_field_is_rejected(self):
        broken = record(consumers=({"workflow": ".github/workflows/frontend.yml",
                                    "job": "verify"},))
        self.assertRejected([broken], "missing field", "job_name")

    def test_unknown_workflow_file_is_rejected(self):
        self.assertRejected(
            [record(consumers=({"workflow": ".github/workflows/absent.yml",
                                 "job": "verify",
                                 "job_name": "Test, type-check, and build"},))],
            "workflow file not found")

    def test_unknown_job_key_is_rejected(self):
        self.assertRejected(
            [record(consumers=({"workflow": ".github/workflows/frontend.yml",
                                 "job": "absent-job",
                                 "job_name": "Test, type-check, and build"},))],
            "job not found", "absent-job")

    def test_mismatched_job_display_name_is_rejected(self):
        self.assertRejected(
            [record(consumers=({"workflow": ".github/workflows/frontend.yml",
                                 "job": "verify",
                                 "job_name": "Some other name"},))],
            "display name", "Some other name")

    def test_unknown_gate_level_is_rejected(self):
        self.assertRejected([record(gate_level="sometimes")], "gate level")

    def test_unknown_path_kind_is_rejected(self):
        self.assertRejected([record(path_kind="magic")], "path kind")


class WorkflowWiringTests(unittest.TestCase):
    """The manifest agrees with the workflow files, which are the truth."""

    def test_every_gated_surface_is_watched_inside_its_workflow_trigger(self):
        # The path table of a gate lives in the manifest.  The workflow only
        # says which paths start it.  Since #309 the pull request trigger
        # carries no path filter at all -- the gate has to start on every
        # pull request, and the job-level condition decides what runs -- so a
        # workflow carries a path list only when it keeps a static mirror of
        # the manifest for its push trigger.  Where such a mirror exists the
        # gate may watch less than the trigger starts -- go-lint also starts
        # on its own tooling script -- but it may not watch more, or the gate
        # could ask for work that the workflow never starts.
        loaded = manifest.load_and_validate()
        for workflow in GATED_WORKFLOWS:
            with self.subTest(workflow=workflow):
                trigger = workflow_trigger_paths(workflow)
                owners = [surface for surface in loaded.surfaces
                          if surface.path_kind == manifest.PATH_KIND_GLOB
                          and any(consumer.workflow == f".github/workflows/{workflow}"
                                  for consumer in surface.consumers)]
                self.assertEqual(len(owners), 1,
                                 f"{workflow} must be owned by exactly one surface")
                if not trigger:
                    continue
                self.assertTrue(set(owners[0].paths) <= set(trigger),
                                f"{workflow}: the gate watches "
                                f"{sorted(set(owners[0].paths) - set(trigger))}, "
                                f"which the workflow does not start on")

    def test_no_workflow_carries_a_path_table_any_more(self):
        # The path table of a gate is registered once, in the manifest.  A
        # workflow that still carried one would be a second registration of
        # the same knowledge, and the two could drift apart.
        for path in sorted(WORKFLOWS.glob("*.yml")):
            with self.subTest(workflow=path.name):
                self.assertEqual(workflow_scope_paths(path.name), [])

    def test_registered_jobs_exist_with_the_registered_display_name(self):
        for surface in manifest.load_and_validate().surfaces:
            for consumer in surface.consumers:
                with self.subTest(surface=surface.name, job=consumer.job):
                    document = workflow_document(Path(consumer.workflow).name)
                    self.assertIn(consumer.job, document["jobs"])
                    self.assertEqual(job_display_name(document, consumer.job),
                                     consumer.job_name)

    def test_the_format_only_shortcut_is_not_re_read_by_the_workflows(self):
        # The shortcut is folded into the decision: the manifest says which
        # jobs it skips, the decision module applies it, and the workflow
        # reads the verdict.  A workflow that read the shortcut itself would
        # decide a second time.
        for surface in manifest.load_and_validate().surfaces:
            for consumer in surface.consumers:
                with self.subTest(surface=surface.name, job=consumer.job):
                    document = workflow_document(Path(consumer.workflow).name)
                    condition = document["jobs"][consumer.job].get("if") or ""
                    self.assertNotIn("format_only", condition)


class KnowledgeRegistrationTests(unittest.TestCase):
    """Ownership knowledge is registered once, in the manifest."""

    def test_manifest_knowledge_matches_the_scripts_in_use_today(self):
        self.assertEqual(tuple(manifest.PROVIDER_ROOTS), tuple(risk_surface.PROVIDER_ROOTS))
        self.assertEqual(tuple(manifest.PROVIDER_ROOTS), tuple(pr_scope.PROVIDER_ROOTS))
        self.assertEqual(manifest.CONTRACT_DIR, risk_surface.CONTRACT_DIR)
        self.assertEqual(tuple(manifest.GOVERNANCE_FILES), tuple(risk_surface.GOVERNANCE))
        self.assertEqual(tuple(manifest.GOVERNANCE_FILES), tuple(pr_scope.GOVERNANCE))
        self.assertEqual(manifest.FRONTEND_ROOT, risk_surface.FRONTEND_ROOT)

    def test_knowledge_appears_exactly_once_in_the_manifest(self):
        source = (CI_DIR / "lingdoc_gate_surfaces.py").read_text(encoding="utf-8")
        literals = _registration_literals(source)
        values = [
            *manifest.PROVIDER_ROOTS,
            manifest.CONTRACT_DIR,
            *manifest.GOVERNANCE_FILES,
            *manifest.GOVERNANCE_DIRS,
            manifest.FRONTEND_ROOT,
        ]
        for value in values:
            with self.subTest(value=value):
                self.assertEqual(literals.count(value), 1,
                                 f"{value!r} is written down more than once in the manifest")

    def test_no_other_ci_script_registers_the_same_knowledge(self):
        values = [
            *manifest.PROVIDER_ROOTS,
            manifest.CONTRACT_DIR,
            *manifest.GOVERNANCE_FILES,
            *manifest.GOVERNANCE_DIRS,
            manifest.FRONTEND_ROOT,
        ]
        for path in sorted(CI_DIR.glob("*.py")):
            if path.name.startswith("test_") or path.name == "lingdoc_gate_surfaces.py":
                continue
            source = path.read_text(encoding="utf-8")
            literals = {node.value for node in ast.walk(ast.parse(source))
                        if isinstance(node, ast.Constant) and isinstance(node.value, str)}
            for value in values:
                if value not in literals:
                    continue
                with self.subTest(file=path.name, value=value):
                    self.assertIn(
                        path.name, LEGACY_REGISTRATION_SITES,
                        f"{path.name} registers {value!r}; the manifest is the only "
                        "registration site.")


if __name__ == "__main__":
    unittest.main()
