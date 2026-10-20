"""Guard tests for the triggers of the seven workflow-level gates.

Why the gates carry no ``pull_request`` path filter
----------------------------------------------------
A top-level ``paths:`` filter does not create a skipped job -- it stops the
workflow from starting at all.  The pull request page then shows no check and
no conclusion, and "why did this gate not run?" has no answer.  The rule in
``docs/agents/development-rules.md:25`` asks for a *successful skipped* job
instead, which means the trigger must not filter paths and the job-level
``if`` (reading the decision module) must decide what runs.  The pull request
gate in ``.github/workflows/lingdoc-review.yml`` already works that way.

Why the ``push:`` path list survives
------------------------------------
A push carries no pull request semantics, and GitHub cannot generate
``paths:`` dynamically, so the push trigger keeps a static path list.  That
list is a *mirror* of the surface paths in ``scripts/ci/lingdoc_gate_surfaces.py``
-- the manifest stays the single place that says what belongs to what, and
the mirror has to be updated by hand whenever the manifest changes.

The tests below read the workflow files with PyYAML, the same way the
manifest validates its consumers.
"""
from __future__ import annotations

from pathlib import Path
import unittest

import yaml

import lingdoc_gate_surfaces as manifest

REPO_ROOT = Path(__file__).resolve().parents[2]
WORKFLOWS = REPO_ROOT / ".github" / "workflows"

# The seven workflow-level gates.  Each of them carries a scope job that asks
# the decision module, and the jobs below it read its answer.
GATED_WORKFLOWS = (
    "frontend.yml",
    "go-lint.yml",
    "app.yml",
    "lingdoc-mock.yml",
    "lingdoc-f01-runner.yml",
    "lingdoc-workspace-concurrency.yml",
    "lingdoc-interface-integration.yml",
)

# The pull request gate is not one of the seven, but it is the model they
# follow: it has never carried a top-level path filter.
PULL_REQUEST_GATE = "lingdoc-review.yml"

# Every workflow whose trigger must not filter paths.
UNFILTERED_WORKFLOWS = GATED_WORKFLOWS + (PULL_REQUEST_GATE,)

# The explanation the gates have to carry, so the next reader knows why the
# path filter is gone and what the push mirror owes to the manifest.
TRIGGER_EXPLANATION = "docs/agents/development-rules.md:25"
MIRROR_EXPLANATION = "lingdoc_gate_surfaces.py"


def load(name: str) -> dict:
    document = yaml.safe_load((WORKFLOWS / name).read_text(encoding="utf-8"))
    return document or {}


def on_of(name: str) -> dict:
    """The ``on:`` section of a workflow.

    YAML 1.1 reads the bare key ``on`` as the boolean ``True``, so the
    section is looked up under both spellings.
    """
    document = load(name)
    section = document.get("on")
    if section is None:
        section = document.get(True)
    return section or {}


def trigger_of(name: str, event: str) -> dict:
    """The ``<event>:`` trigger of a workflow, or an empty mapping."""
    section = on_of(name).get(event)
    return section or {}


def surfaces_of(workflow: str):
    """The manifest surfaces that the given workflow file consumes.

    A surface can be consumed by several jobs of the same workflow, so the
    surfaces are returned once each.
    """
    path = f".github/workflows/{workflow}"
    found = {}
    for surface in manifest.load_surfaces():
        for consumer in surface.consumers:
            if consumer.workflow == path:
                found[surface.name] = surface
    return [found[name] for name in sorted(found)]


class PullRequestTriggerTests(unittest.TestCase):
    """A pull request always starts the gates; the job conditions decide."""

    def test_no_gated_workflow_filters_pull_request_paths(self):
        for name in UNFILTERED_WORKFLOWS:
            with self.subTest(workflow=name):
                trigger = trigger_of(name, "pull_request")
                self.assertNotIn("paths", trigger)
                self.assertNotIn("paths-ignore", trigger)

    def test_every_gated_workflow_still_runs_on_pull_request(self):
        # Deleting the filter must not delete the trigger: without it the
        # workflow would never start, which is the very failure this ticket
        # removes.
        for name in GATED_WORKFLOWS:
            with self.subTest(workflow=name):
                self.assertIn("pull_request", on_of(name))

    def test_the_pull_request_gate_keeps_its_filterless_trigger(self):
        trigger = trigger_of(PULL_REQUEST_GATE, "pull_request")
        self.assertNotIn("paths", trigger)
        self.assertNotIn("paths-ignore", trigger)


class PushMirrorTests(unittest.TestCase):
    """The push path list is a static mirror of the manifest."""

    def test_the_push_path_list_mirrors_the_manifest_surface_paths(self):
        checked = 0
        for name in GATED_WORKFLOWS:
            trigger = trigger_of(name, "push")
            paths = trigger.get("paths")
            if not paths:
                continue
            checked += 1
            surfaces = surfaces_of(name)
            with self.subTest(workflow=name):
                self.assertEqual(len(surfaces), 1)
                self.assertEqual(list(paths), list(surfaces[0].paths))
        self.assertGreater(checked, 0,
                           "no gated workflow keeps a push path list; the "
                           "mirror and its maintenance note are untested")

    def test_the_mirror_is_explained_where_it_exists(self):
        for name in GATED_WORKFLOWS:
            trigger = trigger_of(name, "push")
            if not trigger.get("paths"):
                continue
            text = (WORKFLOWS / name).read_text(encoding="utf-8")
            with self.subTest(workflow=name):
                self.assertIn(MIRROR_EXPLANATION, text,
                              f"{name}: the push path list must say that it "
                              f"mirrors {MIRROR_EXPLANATION}")


class TriggerExplanationTests(unittest.TestCase):
    """Every gate explains why it carries no pull request path filter."""

    def test_every_gated_workflow_explains_its_trigger(self):
        for name in GATED_WORKFLOWS:
            text = (WORKFLOWS / name).read_text(encoding="utf-8")
            with self.subTest(workflow=name):
                self.assertIn(TRIGGER_EXPLANATION, text,
                              f"{name}: the trigger has to point at "
                              f"{TRIGGER_EXPLANATION}")


if __name__ == "__main__":
    unittest.main()
