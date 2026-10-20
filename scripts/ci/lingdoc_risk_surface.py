"""Compatibility shell for the gate decision.

ADR 0006's judge used to live here.  The judge now lives in
``lingdoc_gate_decision``, whose public interface is ``decide`` and
``write_outputs``; this module keeps the old names working for the callers
that still use them, and it holds no decision of its own.

The ownership knowledge (provider roots, contracts directory, governance
file set, frontend root) is registered once, in the gate surface manifest;
this module reads it from there.

Ticket T6 removes this file.
"""
from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
from typing import Any, Mapping

import lingdoc_gate_decision as gate
from lingdoc_gate_decision import Decision, Surface  # noqa: F401 - re-exported

# The ownership knowledge is registered once, in the gate surface manifest.
PROVIDER_ROOTS = gate.PROVIDER_ROOTS
CONTRACT_DIR = gate.CONTRACT_DIR
GOVERNANCE = gate.GOVERNANCE_FILES
FRONTEND_ROOT = gate.FRONTEND_ROOT


def decide(root: Path, event: Mapping[str, Any], *,
           force_all: bool = False) -> Decision:
    """The decision for one event (the old argument order, kept for callers)."""
    if force_all:
        return full_decision(root, "full scope requested")
    return gate.decide(event, root)


def decide_or_full(root: Path, event: Mapping[str, Any]) -> Decision:
    """The decision for one event; the full scope when it cannot be made."""
    return gate.decide(event, root)


def full_decision(root: Path, reason: str = "full scope requested") -> Decision:
    """Every applicable check, over the whole provider surface."""
    return gate.decide({"action": "manual", "reason": reason}, root)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--all", action="store_true", help="Run all applicable checks")
    parser.add_argument("--event", type=Path, default=os.environ.get("GITHUB_EVENT_PATH"))
    parser.add_argument("--root", type=Path, default=Path.cwd())
    args = parser.parse_args()
    root = args.root.resolve()

    if args.all:
        decision = full_decision(root, "full scope requested")
    else:
        if args.event is None:
            parser.error("--event or GITHUB_EVENT_PATH is required")
        event = json.loads(args.event.read_text(encoding="utf-8"))
        decision = decide_or_full(root, event)

    outputs = decision.as_outputs()
    report = {
        "decision": outputs,
        "contribution": list(decision.contribution),
        "note": "Selection only; not a test result or an approval.",
    }
    print(json.dumps(report, ensure_ascii=True, indent=2))
    if output_path := os.environ.get("GITHUB_OUTPUT"):
        with open(output_path, "a", encoding="utf-8") as handle:
            for key, value in outputs.items():
                handle.write(f"{key}={value}\n")


if __name__ == "__main__":
    main()
