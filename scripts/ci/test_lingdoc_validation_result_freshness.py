"""Guard: the committed contract-validation artifact is what the current contracts produce (#596).

What this file guards
---------------------
``docs/08-本轮实施方案/contracts/validation-result.json`` is a machine product:
``validate_artifacts.py`` writes it on every run, and everything downstream --
release verdicts, review reports, acceptance matrices -- reads it as a fact
about the contract's real shape.  It drifted once (#596): the contract moved
from 24 continuous-trace steps / 957 shape checks to 28 / 976, and the
committed artifact kept saying 24 / 957, because nothing re-ran the validator
and nothing compared the committed report with a fresh one.  The review
workflow runs the validator in a scratch copy and checks only that it passes;
it never looks at the committed artifact, so the drift could not turn anything
red.

This file closes that loop: it re-runs the validator over a scratch copy of
the contract directory -- the same shape the review workflow uses -- and
refuses unless the report it produces equals the committed artifact field by
field.  The comparison is over the parsed report, so line-ending style does
not matter; the numbers and the route-parity inventory do.  The failure
message names the fields that drifted and the one command that regenerates
the artifact, because the only correct fix is to re-run the validator -- never
to hand-edit the numbers back into agreement.
"""
from __future__ import annotations

import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

REPO_ROOT = Path(__file__).resolve().parents[2]
CONTRACTS = REPO_ROOT / "docs" / "08-本轮实施方案" / "contracts"
COMMITTED_REPORT = CONTRACTS / "validation-result.json"

#: The one command that regenerates the artifact: the validator writes its
#: report next to itself, so running it in place rewrites the committed file.
REGENERATE = "python -X utf8 docs/08-本轮实施方案/contracts/validate_artifacts.py --source-root ."

#: The drift this guard was born from (#596): an artifact that kept the numbers
#: of an older contract shape -- 24 steps / 957 checks -- after the contract
#: had already moved on to 28 / 976.  Kept here so the guard's own comparison
#: can be proven to see that historical drift.
STALE_SHAPE = {"continuous_trace_steps": 24, "shape_checks": 957}


def fresh_report() -> dict:
    """Re-run the contract's static validator over a scratch copy of the contract.

    The scratch copy is what the review workflow runs, and it is the shape that
    proves the validator finds its helpers under ``--source-root`` instead of
    next to itself.  The committed report is deleted from the copy first, so a
    validator that failed to write one cannot pass this guard by leaving the
    committed file standing.
    """
    with tempfile.TemporaryDirectory() as directory:
        scratch = Path(directory) / "contracts"
        shutil.copytree(CONTRACTS, scratch)
        # Never mistake the committed report for a result from this run.
        (scratch / "validation-result.json").unlink(missing_ok=True)
        run = subprocess.run(
            [sys.executable, "-X", "utf8", str(scratch / "validate_artifacts.py"),
             "--source-root", str(REPO_ROOT)],
            capture_output=True, text=True, encoding="utf-8", errors="replace")
        if run.returncode != 0:
            raise AssertionError(
                "the contract validator itself failed, so there is no fresh report "
                "to compare the committed artifact with:\n" + run.stderr[-2000:])
        return json.loads((scratch / "validation-result.json").read_text(encoding="utf-8"))


def drifted_fields(fresh: dict, committed: dict) -> list:
    """Name every top-level report field where the fresh run and the artifact disagree."""
    names = []
    for key in sorted(set(fresh) | set(committed)):
        if key not in fresh or key not in committed or fresh[key] != committed[key]:
            names.append(key)
    return names


def describe(key: str, fresh: dict, committed: dict) -> str:
    """Say what one drifted field holds, briefly; structures collapse to their shape."""
    def brief(value):
        if isinstance(value, (int, str, bool)):
            return repr(value)
        if isinstance(value, list):
            return f"a list of {len(value)} items"
        if isinstance(value, dict):
            return f"an object of {len(value)} keys"
        return type(value).__name__
    return f"{key}: committed {brief(committed.get(key))}, current run {brief(fresh.get(key))}"


class ValidationArtifactIsFreshTest(unittest.TestCase):
    """The committed validation result is a product of the current contracts."""

    def test_the_committed_artifact_equals_a_fresh_validator_run(self):
        committed = json.loads(COMMITTED_REPORT.read_text(encoding="utf-8"))
        fresh = fresh_report()
        drift = drifted_fields(fresh, committed)
        self.assertEqual(
            drift, [],
            "docs/08-本轮实施方案/contracts/validation-result.json no longer matches "
            "what the current contracts produce; it describes a contract shape that "
            "no longer exists (#596).  Do not hand-edit the numbers back into "
            "agreement -- regenerate the artifact, then commit what the validator "
            f"rewrote:\n    {REGENERATE}\nDrifted fields: "
            + "; ".join(describe(key, fresh, committed) for key in drift))

    def test_the_comparison_sees_the_historical_drift(self):
        """The guard is not vacuous: the #596 drift, replayed, is reported.

        The committed artifact with the numbers of the older contract shape put
        back has to be reported as drifted, or the test above could pass with a
        comparison that looks at nothing.
        """
        committed = json.loads(COMMITTED_REPORT.read_text(encoding="utf-8"))
        stale = dict(committed, **STALE_SHAPE)
        self.assertEqual(drifted_fields(committed, stale),
                         sorted(STALE_SHAPE),
                         "a report with the #596 stale numbers was not reported as drifted")


if __name__ == "__main__":
    unittest.main()
