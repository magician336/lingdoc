"""The required check names of this repository: one list, one truth.

A *required check* is a check the branch ruleset demands before a pull
request may merge.  The ruleset lives in the repository settings, not in the
code base, so this file is the in-repo copy of that contract: the names the
workflows have to keep publishing.

Only the **names** are registered here.  Which job publishes a name is read
from the workflow files themselves, so this list cannot drift away from the
workflows, and it does not repeat a path that another script owns.

How it is used
--------------
* ``scripts/ci/test_lingdoc_gate_contracts.py`` asserts that every name here
  is a check a workflow really publishes, and that a workflow which publishes
  a required check publishes no *other* check without registering it here.
  Both directions matter: a one-way check lets a check silently appear or
  disappear from the merge requirement.
* When a workflow's job ``name:`` changes, this list changes with it.  The
  ruleset asks for the name the workflow *shows*, not for the job key.

The repository settings side
----------------------------
The ruleset "Protect main" was read online on 2026-10-21::

    gh api repos/magician336/lingdoc/rulesets
    gh api repos/magician336/lingdoc/rulesets/23885172

Its ``required_status_checks`` rule lists exactly one context,
``LingDoc PR gate`` -- the value registered below.  The same ruleset also
carries ``strict_required_status_checks_policy`` and the ``non_fast_forward``
rule; those belong to #298 and are deliberately not duplicated here.
"""
from __future__ import annotations

# The required checks of the ruleset "Protect main", by the display name the
# ruleset asks for.  Keep the spelling exactly as the workflow shows it.
REQUIRED_CHECK_NAMES: tuple[str, ...] = (
    "LingDoc PR gate",
)
