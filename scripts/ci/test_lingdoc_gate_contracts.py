"""Guard tests for the three contracts of the gate wiring (#303).

The gate decides once (in ``lingdoc_gate_decision``) and the workflows only
read the answer.  Three things have to agree about that answer, and none of
them can be checked by looking at one file alone:

1. **The wiring triad.**  The keys the decision module actually writes, the
   keys a workflow's ``scope`` job declares in ``outputs:``, and the keys a
   job condition reads must be the *same* set.  A key that is written but not
   declared is read as an empty string; a key that is read but not written
   compares ``'' != 'true'``.  Both fail silently -- the gate simply does the
   wrong thing.  This test reads the **real workflow YAML** with PyYAML and
   the **real decision module**; it keeps no copy of either, so it cannot
   drift away from them.

2. **The required check names.**  The branch ruleset demands a check by its
   display name.  ``lingdoc_required_checks`` is the single in-repo list of
   those names; the ruleset itself lives in the repository settings and is
   checked online.  The list and the workflows must agree in *both*
   directions, or a check silently appears or disappears from the merge
   requirement.

3. **The documentation contract.**  Every judgement concept the documents
   name has a field in the decision, and every field the decision carries is
   named by a document.  ``lingdoc_gate_concepts`` holds that vocabulary in
   one place; the documents themselves are never edited here.

The tests are hermetic: no network, no git fixture, no mocks.  They read the
repository as it is.
"""
from __future__ import annotations

import dataclasses
from pathlib import Path
import re
import unittest
from typing import Any, Iterator, Mapping

import yaml

import lingdoc_gate_concepts as concepts
import lingdoc_gate_decision as decision
import lingdoc_required_checks as required_checks

REPO_ROOT = Path(__file__).resolve().parents[2]
WORKFLOWS = REPO_ROOT / ".github" / "workflows"

# The decision module the workflows ask.
DECISION_MODULE = "lingdoc_gate_decision.py"

# The id of the step that runs the decision module, and the id of the job
# that carries it in the workflow-level gates.
SCOPE_STEP = "scope"
SCOPE_JOB = "scope"

# ``<job>.outputs.<alias>: ${{ steps.scope.outputs.<key> }}``
# The key may carry a dot here on purpose: a dotted output id is refused by
# GitHub's workflow schema, and the guard has to name the offending name as
# the workflow spells it.
FORWARD = re.compile(r"steps\.([A-Za-z0-9_-]+)\.outputs\.([A-Za-z0-9_.-]+)")
# ``<job>.outputs.<alias>: ${{ steps.<step>.outcome }}`` -- a step outcome a
# dependent job reads across the job boundary.  It is a declaration like any
# other: an undeclared one reads an empty string (#724's gate job reads the
# decision job's step outcomes this way).
OUTCOME = re.compile(r"steps\.([A-Za-z0-9_-]+)\.outcome\s*\}")
# ``needs.<job>.outputs.<alias>``
NEEDS_OUTPUT = re.compile(r"needs\.([A-Za-z0-9_-]+)\.outputs\.([A-Za-z0-9_.-]+)")
# ``steps.<step>.outputs.<key>``
STEP_OUTPUT = re.compile(r"steps\.([A-Za-z0-9_-]+)\.outputs\.([A-Za-z0-9_.-]+)")

# The scope allow-list's one registered extension (#737): app.yml also
# forwards the decision's provider_delegate key -- the reverse delegation's
# package list, the packages the gate's provider shards verify on this head.
# Like the allow-list itself the extension is explicit: a workflow that
# forwards an undeclared key reads an empty string, and a key that joins
# this map without the workflow carrying it fails the triad below.
DELEGATION_FORWARDS = {"app.yml": {"provider_delegate"}}


# --------------------------------------------------------------------------
# reading the repository
# --------------------------------------------------------------------------
def load_workflow(name: str) -> Mapping[str, Any]:
    document = yaml.safe_load((WORKFLOWS / name).read_text(encoding="utf-8"))
    return document or {}


def jobs_of(name: str) -> Mapping[str, Any]:
    return load_workflow(name).get("jobs") or {}


def decision_workflows() -> list[str]:
    """The workflows that ask the decision module for its answer."""
    return sorted(path.name for path in WORKFLOWS.glob("*.yml")
                  if DECISION_MODULE in path.read_text(encoding="utf-8"))


def iter_strings(node: Any) -> Iterator[str]:
    if isinstance(node, str):
        yield node
    elif isinstance(node, Mapping):
        for value in node.values():
            yield from iter_strings(value)
    elif isinstance(node, (list, tuple)):
        for item in node:
            yield from iter_strings(item)


def declared_outputs(name: str) -> dict[str, dict[str, tuple[str, str]]]:
    """``job key -> {alias: (step, key)}`` the alias forwards.

    Not every job output comes from the decision step -- the lint workflow
    publishes the lint modules from its own step -- so the step is kept
    beside the key and the decision's own aliases are picked out below.
    """
    declared: dict[str, dict[str, tuple[str, str]]] = {}
    for job_key, job in jobs_of(name).items():
        aliases: dict[str, tuple[str, str]] = {}
        for alias, value in (job.get("outputs") or {}).items():
            match = FORWARD.search(str(value))
            if match:
                aliases[alias] = (match.group(1), match.group(2))
        if aliases:
            declared[job_key] = aliases
    return declared


def decision_aliases(name: str, job_key: str) -> dict[str, str]:
    """``alias -> decision key`` for the aliases the decision step feeds."""
    aliases = declared_outputs(name).get(job_key, {})
    return {alias: key for alias, (step, key) in aliases.items()
            if step == SCOPE_STEP}


def declared_outcomes(name: str) -> dict[str, dict[str, str]]:
    """``job key -> {alias: step}`` for aliases that forward an outcome."""
    declared: dict[str, dict[str, str]] = {}
    for job_key, job in jobs_of(name).items():
        aliases: dict[str, str] = {}
        for alias, value in (job.get("outputs") or {}).items():
            match = OUTCOME.search(str(value))
            if match:
                aliases[alias] = match.group(1)
        if aliases:
            declared[job_key] = aliases
    return declared


def output_reads(name: str) -> list[tuple[str, str, str, str]]:
    """``(job, kind, provider or step, alias or key)`` read from an output.

    A declaration (``jobs.<id>.outputs``) is not a read: the declaration is
    what the other side of the contract is checked against.
    """
    reads: list[tuple[str, str, str, str]] = []
    for job_key, job in jobs_of(name).items():
        body = {key: value for key, value in job.items() if key != "outputs"}
        for text in iter_strings(body):
            for provider, alias in NEEDS_OUTPUT.findall(text):
                reads.append((job_key, "needs", provider, alias))
            for step, key in STEP_OUTPUT.findall(text):
                reads.append((job_key, "steps", step, key))
    return reads


def decision_keys() -> set[str]:
    """The keys the decision module actually writes.

    A manual run is forced to the full scope, so every job verdict the
    manifest knows about is present in the result -- the key set does not
    depend on which paths this particular checkout happens to touch.
    """
    verdict = decision.decide({"action": "workflow_dispatch"}, REPO_ROOT)
    return set(decision.write_outputs(verdict))


def workflow_key_prefix(name: str) -> str:
    """The prefix the decision module gives the keys of one workflow."""
    stem = Path(name).stem
    return "job_" + decision.OUTPUT_KEY_SEPARATORS.sub("_", stem) + "_"


def workflow_visible_keys() -> set[str]:
    """The decision keys a workflow can actually read.

    A key is visible when a workflow reads it directly from the decision
    step, or when it is the decision key behind an alias that a job
    condition reads.
    """
    visible: set[str] = set()
    for name in decision_workflows():
        # the aliases each job publishes for the decision's own keys
        decision_aliases_by_job = {
            job_key: {alias: key for alias, (step, key) in aliases.items()
                      if step == SCOPE_STEP}
            for job_key, aliases in declared_outputs(name).items()}
        for _job, kind, provider, alias in output_reads(name):
            if kind == "steps":
                if provider == SCOPE_STEP:
                    visible.add(alias)
            elif alias in decision_aliases_by_job.get(provider, {}):
                visible.add(decision_aliases_by_job[provider][alias])
    return visible


def line_of(name: str, needle: str) -> str:
    """``file:line`` of the first line of a workflow that mentions the needle.

    A failure has to say where the offending name is written, not only what
    is wrong with it.
    """
    text = (WORKFLOWS / name).read_text(encoding="utf-8")
    for number, line in enumerate(text.splitlines(), 1):
        if needle in line:
            return f"{name}:{number}"
    return name


def decision_members() -> set[str]:
    """Every member of the decision result, by its qualified name."""
    members: set[str] = set()
    for owner, klass in (("Decision", decision.Decision),
                         ("JobDecision", decision.JobDecision)):
        for field in dataclasses.fields(klass):
            members.add(f"{owner}.{field.name}")
        for name, value in vars(klass).items():
            if isinstance(value, property):
                members.add(f"{owner}.{name}")
    return members


# --------------------------------------------------------------------------
# 1. the wiring triad
# --------------------------------------------------------------------------
class WiringTriadTests(unittest.TestCase):
    """The written keys, the declared outputs and the read keys agree."""

    @classmethod
    def setUpClass(cls) -> None:
        cls.keys = decision_keys()
        cls.workflows = decision_workflows()

    def test_a_workflow_declares_exactly_the_keys_it_publishes(self):
        """The scope job's ``outputs:`` and the decision keys are one set."""
        for name in self.workflows:
            if SCOPE_JOB not in jobs_of(name):
                continue  # the pull request gate reads the decision step itself
            forwarded = set(decision_aliases(name, SCOPE_JOB).values())
            expected = {key for key in self.keys
                        if key.startswith(workflow_key_prefix(name))}
            expected |= DELEGATION_FORWARDS.get(name, set())
            with self.subTest(workflow=name):
                self.assertEqual(
                    forwarded, expected,
                    f"{line_of(name, 'outputs:')}: the scope job declares "
                    f"{sorted(forwarded)} but the decision module publishes "
                    f"{sorted(expected)} for this workflow; a key that is not "
                    f"declared is read as an empty string and the condition "
                    f"degenerates silently")

    def test_every_declared_output_forwards_a_key_the_decision_writes(self):
        for name in self.workflows:
            for job, aliases in declared_outputs(name).items():
                for alias, (step, key) in aliases.items():
                    if step != SCOPE_STEP:
                        continue  # a job output of its own, not the decision's
                    with self.subTest(workflow=name, job=job, alias=alias):
                        self.assertIn(
                            key, self.keys,
                            f"{line_of(name, alias + ':')}: the output "
                            f"{job}.outputs.{alias} forwards "
                            f"steps.{step}.outputs.{key}, which the "
                            f"decision module never writes")

    def test_every_alias_a_condition_reads_is_declared_by_its_job(self):
        for name in self.workflows:
            declared = declared_outputs(name)
            outcomes = declared_outcomes(name)
            for job, kind, provider, alias in output_reads(name):
                if kind != "needs":
                    continue
                with self.subTest(workflow=name, job=job,
                                  provider=provider, alias=alias):
                    where = line_of(name, f"needs.{provider}.outputs.{alias}")
                    self.assertTrue(
                        provider in declared or provider in outcomes,
                        f"{where}: {job} reads needs.{provider}."
                        f"outputs.{alias}, but {provider} declares no output "
                        f"at all")
                    self.assertTrue(
                        alias in declared.get(provider, {})
                        or alias in outcomes.get(provider, {}),
                        f"{where}: {job} reads needs.{provider}.outputs.{alias}, "
                        f"which {provider} never declares; the read yields an "
                        f"empty string")

    def test_every_key_read_from_the_decision_step_is_written_by_it(self):
        for name in self.workflows:
            for job, kind, step, key in output_reads(name):
                if kind != "steps" or step != SCOPE_STEP:
                    continue
                with self.subTest(workflow=name, job=job, key=key):
                    self.assertIn(
                        key, self.keys,
                        f"{line_of(name, f'steps.{step}.outputs.{key}')}: {job} "
                        f"reads steps.{step}.outputs.{key}, which the decision "
                        f"module never writes; the comparison degenerates "
                        f"instead of failing")

    def test_every_declared_output_is_read_by_some_job(self):
        """A declared output nobody reads is a silently dead declaration."""
        for name in self.workflows:
            declared = declared_outputs(name)
            read_aliases = {(provider, alias)
                             for _job, kind, provider, alias
                             in output_reads(name) if kind == "needs"}
            for job, aliases in declared.items():
                for alias in aliases:
                    with self.subTest(workflow=name, job=job, alias=alias):
                        self.assertIn(
                            (job, alias), read_aliases,
                            f"{line_of(name, alias + ':')}: {job}.outputs.{alias} "
                            f"is declared but no job reads it; the declaration "
                            f"is dead weight")


# --------------------------------------------------------------------------
# 2. the required check names
# --------------------------------------------------------------------------
class RequiredCheckTests(unittest.TestCase):
    """The list of required checks and the workflows agree, both ways.

    Which job publishes a required check is read from the workflow files:
    the list holds the names only, so it cannot drift away from the
    workflows and it does not repeat a path another script owns.
    """

    def _published_checks(self) -> dict[str, list[tuple[str, str]]]:
        """display name -> [(workflow, job)] that publish it."""
        published: dict[str, list[tuple[str, str]]] = {}
        for path in sorted(WORKFLOWS.glob("*.yml")):
            for job_key, job in (jobs_of(path.name)).items():
                name = job.get("name") or job_key
                published.setdefault(str(name), []).append((path.name, job_key))
        return published

    def test_every_registered_check_is_published_by_a_job(self):
        published = self._published_checks()
        for name in required_checks.REQUIRED_CHECK_NAMES:
            with self.subTest(check=name):
                self.assertTrue(
                    name in published,
                    f"no job publishes the check {name!r}; the ruleset asks "
                    f"for that name and the workflow has to show it")

    def test_a_required_check_workflow_publishes_no_unregistered_check(self):
        """The other direction: no check appears without being registered.

        Since #724 the gate workflow also carries the decision job and the
        provider shard matrix.  Those checks hold no merge requirement of
        their own -- the ruleset asks for the aggregated gate check alone --
        so they are registered separately, in
        ``lingdoc_required_checks.GATE_INTERNAL_JOB_NAMES``.  An unregistered
        name here means either a check that joined the merge requirement in
        silence or a helper nobody signed off on.
        """
        published = self._published_checks()
        registry = set(required_checks.REQUIRED_CHECK_NAMES)
        internal = set(required_checks.GATE_INTERNAL_JOB_NAMES)
        workflows = {workflow
                     for name in registry
                     for workflow, _job in published.get(name, ())}
        for workflow in sorted(workflows):
            for job_key, job in jobs_of(workflow).items():
                name = str(job.get("name") or job_key)
                with self.subTest(workflow=workflow, job=job_key):
                    self.assertIn(
                        name, registry | internal,
                        f"{line_of(workflow, 'name: ' + name)}: "
                        f"{workflow}:{job_key} publishes the check {name!r}, "
                        f"which is not registered; register it as a required "
                        f"check, add it to GATE_INTERNAL_JOB_NAMES with the "
                        f"reason it holds no merge requirement, or move it "
                        f"out of this workflow")

    def test_the_required_check_names_are_unique(self):
        names = list(required_checks.REQUIRED_CHECK_NAMES)
        self.assertEqual(len(names), len(set(names)),
                         f"the required check list repeats a name: {names}")

    def test_the_required_check_list_is_not_empty(self):
        self.assertTrue(required_checks.REQUIRED_CHECK_NAMES,
                        "the required check list is empty; the ruleset asks for "
                        "at least one check")


# --------------------------------------------------------------------------
# 3. the documentation contract
# --------------------------------------------------------------------------
class DocumentationContractTests(unittest.TestCase):
    """The documents and the decision name the same concepts, both ways."""

    @classmethod
    def setUpClass(cls) -> None:
        cls.keys = decision_keys()
        cls.members = decision_members()
        cls.visible = workflow_visible_keys()

    def test_every_covered_document_section_still_exists(self):
        for path, heading in concepts.DOC_SECTIONS:
            with self.subTest(document=path, section=heading):
                text = (REPO_ROOT / path).read_text(encoding="utf-8")
                self.assertIn(
                    heading, text,
                    f"{path} no longer has the section {heading!r}; the "
                    f"document contract has to be moved to the new wording")

    def test_every_documented_concept_is_still_written_that_way(self):
        for concept in concepts.CONCEPTS:
            with self.subTest(concept=concept.term):
                text = (REPO_ROOT / concept.doc).read_text(encoding="utf-8")
                self.assertTrue(
                    concept.term in text,
                    f"{concept.doc} no longer calls the concept "
                    f"{concept.term!r}; the vocabulary moved without the "
                    f"decision following")
                self.assertTrue(
                    concept.snippet in text,
                    f"{concept.doc} no longer states {concept.snippet!r} for "
                    f"the concept {concept.term!r}; the wording changed under "
                    f"the contract")

    def test_every_concept_names_a_field_of_the_decision(self):
        for concept in concepts.CONCEPTS:
            for member in concept.fields:
                with self.subTest(concept=concept.term, member=member):
                    self.assertIn(
                        member, self.members,
                        f"the concept {concept.term!r} names {member}, which "
                        f"the decision result does not have")

    def test_every_concept_names_a_key_the_decision_publishes(self):
        for concept in concepts.CONCEPTS:
            for key in concept.keys:
                with self.subTest(concept=concept.term, key=key):
                    self.assertIn(
                        key, self.keys,
                        f"the concept {concept.term!r} names the key {key!r}, "
                        f"which the decision module does not publish")

    def test_every_concept_is_readable_by_a_workflow(self):
        """A concept a workflow cannot read is a concept nobody consumes."""
        for concept in concepts.CONCEPTS:
            if not concept.fields:
                continue  # a wiring rule, not a judgement fact
            with self.subTest(concept=concept.term):
                self.assertTrue(
                    any(key in self.visible for key in concept.keys)
                    or concept.covers_job_keys,
                    f"the concept {concept.term!r} is carried by "
                    f"{list(concept.keys)}, but no workflow reads any of them; "
                    f"the decision result says it, the gate never sees it")

    def test_every_field_of_the_decision_is_named_by_a_concept(self):
        claimed: set[str] = set()
        for concept in concepts.CONCEPTS:
            claimed.update(concept.fields)
        for member in sorted(self.members):
            if member in concepts.INTERNAL_MEMBERS:
                continue
            with self.subTest(member=member):
                self.assertIn(
                    member, claimed,
                    f"{member} is a field of the decision result but no "
                    f"documented concept names it; either the documents lost "
                    f"the concept or the decision gained one silently")

    def test_every_published_key_is_named_by_a_concept(self):
        claimed: set[str] = set()
        for concept in concepts.CONCEPTS:
            claimed.update(concept.keys)
        job_keys = {key for key in self.keys if key.startswith("job_")}
        for key in sorted(self.keys):
            if key in concepts.INTERNAL_KEYS or key in job_keys:
                continue
            with self.subTest(key=key):
                self.assertIn(
                    key, claimed,
                    f"the decision module publishes {key!r} but no documented "
                    f"concept names it; the implementation gained a concept "
                    f"the documents do not have")

    def test_every_key_a_workflow_reads_is_named_by_a_concept(self):
        claimed: set[str] = set()
        for concept in concepts.CONCEPTS:
            claimed.update(concept.keys)
        job_keys = {key for key in self.keys if key.startswith("job_")}
        for key in sorted(self.visible):
            with self.subTest(key=key):
                self.assertTrue(
                    key in claimed or key in job_keys,
                    f"a workflow reads the key {key!r}, but no documented "
                    f"concept names it; the gate consumes something the "
                    f"documents do not describe")


# --------------------------------------------------------------------------
# 4. the guard tests have to run somewhere
# --------------------------------------------------------------------------
class GuardTestExecutionTests(unittest.TestCase):
    """A guard test that nobody runs is not a guard.

    The pull request gate runs the whole ``scripts/ci`` suite in one step.
    That step must be unconditional: a pure formatting commit must not be
    able to switch the protection net off.
    """

    SUITE_PATTERN = "test_lingdoc_*.py"

    def _suite_steps(self) -> list[tuple[str, Mapping[str, Any]]]:
        """The steps that run the whole ``scripts/ci`` suite."""
        steps: list[tuple[str, Mapping[str, Any]]] = []
        for name in decision_workflows():
            for job_key, job in jobs_of(name).items():
                for step in job.get("steps") or []:
                    run = str(step.get("run") or "")
                    if "unittest discover -s scripts/ci" in run \
                            and self.SUITE_PATTERN in run:
                        steps.append((f"{name}:{job_key}", step))
        return steps

    def test_a_workflow_runs_the_whole_ci_suite(self):
        steps = self._suite_steps()
        self.assertTrue(steps,
                        f"no workflow runs `unittest discover -s scripts/ci "
                        f"-p '{self.SUITE_PATTERN}'`; the guard tests in this "
                        f"directory would never run")

    def test_the_suite_runs_unconditionally(self):
        for where, step in self._suite_steps():
            with self.subTest(step=where):
                self.assertNotIn(
                    "if", step,
                    f"{where} is conditional; a formatting-only commit could "
                    f"switch the protection net off")

    def test_this_guard_test_is_collected_by_the_suite(self):
        self.assertRegex(Path(__file__).name,
                         r"^test_lingdoc_.*\.py$",
                         "this file does not match the suite's collection "
                         "pattern, so it would never run in CI")


if __name__ == "__main__":
    unittest.main()
