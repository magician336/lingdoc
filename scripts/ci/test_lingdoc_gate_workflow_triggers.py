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

Why the other workflows keep their path filter
------------------------------------------------
The workflows that are **not** gates scope themselves by their own subtree.
Their path filter is not a copy of the manifest -- the manifest has no entry
for them -- so it is the only statement of what they watch, and removing it
would make them build on every pull request.  The tests below keep that fact
true and keep the decision layer and the manifest from drifting apart about
which workflows are gates.

The tests below read the workflow files with PyYAML, the same way the
manifest validates its consumers.
"""
from __future__ import annotations

from pathlib import Path
import re
import unittest

import yaml

import lingdoc_gate_surfaces as manifest

REPO_ROOT = Path(__file__).resolve().parents[2]
WORKFLOWS = REPO_ROOT / ".github" / "workflows"


def load(name: str) -> dict:
    document = yaml.safe_load((WORKFLOWS / name).read_text(encoding="utf-8"))
    return document or {}


# The pull request gate is not one of the seven, but it is the model they
# follow: it has never carried a top-level path filter.
PULL_REQUEST_GATE = "lingdoc-review.yml"

# The decision module.  Running it is what makes a workflow part of the
# gate layer, so the gate workflows are derived from the workflow files
# instead of being written down twice.
DECISION_MODULE = "scripts/ci/lingdoc_gate_decision.py"


def decision_module_workflows() -> set[str]:
    """The workflows that run the decision module.

    The module is looked for in the ``run:`` of a step, not in the text of
    the file: a comment that names the module does not make a workflow a
    gate.
    """
    names = set()
    for path in WORKFLOWS.glob("*.yml"):
        for job in (load(path.name).get("jobs") or {}).values():
            for step in job.get("steps") or []:
                if DECISION_MODULE in str(step.get("run") or ""):
                    names.add(path.name)
                    break
    return names


# Every workflow whose trigger must not filter paths.
UNFILTERED_WORKFLOWS = tuple(sorted(decision_module_workflows()))

# The workflow-level gates: the gate layer without the pull request gate.
GATED_WORKFLOWS = tuple(name for name in UNFILTERED_WORKFLOWS
                         if name != PULL_REQUEST_GATE)

# The workflows that are not gates and scope themselves with a path filter.
# They are not submodules -- the repository has no submodules -- they are
# workflows that own a directory of this repository and watch it themselves.
NON_GATE_WORKFLOWS = (
    "anydoc.yml",
    "cli.yml",
    "cli-e2e.yml",
    "docreader.yml",
    "dsh-plugin.yml",
    "mcp-server.yml",
)

# The directories those workflows own.  No gate surface may claim one of
# them: that would turn the workflow's own filter into a copy of the
# manifest, which is the drift #309 removed from the gates.
NON_GATE_ROOTS = {
    "cli/": ("cli.yml", "cli-e2e.yml"),
    "docreader/": ("docreader.yml",),
    "mcp-server/": ("mcp-server.yml",),
    "packages/dsh-weknora/": ("dsh-plugin.yml",),
    "third_party/anydoc-go/": ("anydoc.yml",),
    "internal/infrastructure/docparser/": ("anydoc.yml",),
}

# The explanation the gates have to carry, so the next reader knows why the
# path filter is gone and what the push mirror owes to the manifest.
TRIGGER_EXPLANATION = "docs/agents/development-rules.md:26"
MIRROR_EXPLANATION = "lingdoc_gate_surfaces.py"

# The rules file the gates point at, and the fragment that identifies the
# rule inside it.  The gates cite the rule by line number, so the citation
# has to be checked: a rule that moved must not leave the gates pointing at
# the wrong line.
RULES = REPO_ROOT / "docs" / "agents" / "development-rules.md"
RULE_FRAGMENT = "**门禁始终出现**"

# The citation the gates carry: ``<file>:<line>``.
CITATION = re.compile(r"docs/agents/development-rules\.md:(\d+)")


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


def manifest_workflows() -> set[str]:
    """The workflows the manifest registers as gate surface consumers."""
    return {Path(consumer.workflow).name
            for surface in manifest.load_surfaces()
            for consumer in surface.consumers}


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


class GateSurfaceBoundaryTests(unittest.TestCase):
    """Which workflows are gates, and which ones scope themselves.

    The gate layer is the decision module plus the manifest that says what
    belongs to what.  A workflow is part of it when it runs the decision
    module, and the manifest registers exactly those workflows.  The two
    answers have to agree; when they drift, the decision layer judges a
    different set of workflows than the manifest describes.

    The workflows below are **not** gates.  They own a directory of this
    repository and watch it themselves with a path filter.  The manifest has
    no entry for them, so their filter is not a copy of anything -- it is
    their own statement of what they watch.  ``cli-e2e.yml`` has a second
    constraint, the ``acceptance-e2e`` label on the pull request, so its
    filter narrows the labelled pull requests rather than being the only
    one.
    """

    def test_the_decision_layer_and_the_manifest_agree_on_the_gate_workflows(self):
        # Both sides are read, not written down: the workflows that run the
        # decision module, and the workflows the manifest registers.
        self.assertEqual(
            decision_module_workflows(), manifest_workflows(),
            "the workflows that run the decision module and the workflows "
            "the manifest registers have drifted apart; the decision layer "
            "judges a different set of workflows than the manifest describes")

    def test_non_gate_workflows_keep_their_own_pull_request_path_filter(self):
        for name in NON_GATE_WORKFLOWS:
            with self.subTest(workflow=name):
                trigger = trigger_of(name, "pull_request")
                self.assertTrue(
                    trigger.get("paths"),
                    f"{name}: it scopes itself with its own path filter, "
                    "which is not a copy of the manifest; deleting it widens "
                    "the build to pull requests the workflow does not own")
                self.assertNotIn(
                    name, manifest_workflows(),
                    f"{name}: it is not a gate, so the manifest must not "
                    "register it; a registered workflow has no path filter "
                    "and this one has")

    def test_no_gate_surface_claims_a_directory_a_non_gate_workflow_owns(self):
        """A directory owned by a non-gate workflow stays out of the manifest.

        Registering one of these directories as a gate surface would make
        the workflow's own filter a copy of the manifest -- the drift #309
        removed from the gates.  The entries a non-gate workflow shares with
        a gate (``go.mod``, ``testdata/**``) are genuinely shared ownership
        and are not claimed here.
        """
        for surface in manifest.load_surfaces():
            for path in surface.paths:
                if path.startswith("!"):
                    continue  # a negated pattern excludes, it does not claim
                for root, owners in NON_GATE_ROOTS.items():
                    if path != root.rstrip("/") and not path.startswith(root):
                        continue
                    with self.subTest(surface=surface.name, path=path):
                        self.fail(
                            f"surface {surface.name!r} claims {root!r}, which "
                            f"{' and '.join(owners)} own; the workflow's own "
                            "filter would become a copy of the manifest")


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


class RuleCitationTests(unittest.TestCase):
    """A cited line number has to be the line the rule is on.

    The gates cite the rule by line number.  When the rules file grows, the
    citation drifts and the gate points at the wrong paragraph -- silently.
    This test locates the rule by its text and compares the line numbers.
    """

    def _rule_line(self) -> int:
        """The line the rule is on, found by its text."""
        for number, line in enumerate(
                RULES.read_text(encoding="utf-8").splitlines(), 1):
            if RULE_FRAGMENT in line:
                return number
        self.fail(f"{RULES.name} no longer states the rule {RULE_FRAGMENT!r}")

    def test_the_rules_file_states_the_rule(self):
        self.assertGreater(self._rule_line(), 0)

    def test_every_cited_line_number_is_the_rule(self):
        rule_line = self._rule_line()
        for name in GATED_WORKFLOWS:
            text = (WORKFLOWS / name).read_text(encoding="utf-8")
            citations = CITATION.findall(text)
            with self.subTest(workflow=name):
                self.assertTrue(citations,
                                f"{name}: the trigger does not cite the rule "
                                "by line number")
                for cited in citations:
                    self.assertEqual(
                        int(cited), rule_line,
                        f"{name}: cites development-rules.md:{cited}, but the "
                        f"rule is on line {rule_line}; a citation that points "
                        "at the wrong line sends the reader to the wrong "
                        "paragraph")


if __name__ == "__main__":
    unittest.main()
