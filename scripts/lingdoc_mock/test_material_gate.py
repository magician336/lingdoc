"""The material qualification gate's self-test, in the CI test discovery.

The gate's cases live with the gate (`scripts/lingdoc_mock/run_f01.py`), because
the gate and its cases belong together. But a case that only a manual
`--self-test` invocation runs is a case that CI never runs: the gate could be
silently disconnected and every automated path would stay green.

This module puts those cases where the CI finds them:
`python -m unittest discover -s scripts/lingdoc_mock -p 'test_*.py' -v`
(the `lingdoc-mock` workflow's "Test engine, actual contract and
corrupt-response rejection" step) collects the imported TestCase class, so
the material gate is covered by the same command that runs the other mock
tests.
"""
from __future__ import annotations

from scripts.lingdoc_mock.run_f01 import MaterialPreflightSelfTest

__all__ = ["MaterialPreflightSelfTest"]
